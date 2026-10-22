package app

// payapi.go — 支付端点：create / status/:id / order/:id / notify / my/orders / confirm
// + 匿名支付闭环（无 gate 自动发码，入账后 +1 生效）+ 入账幂等（条件更新抢占）
// + 对账 worker + 限速（订单维度 + 匿名下单 IP 维度）

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/skip2/go-qrcode"
)

const orderReuseWindow = 5 * time.Minute

// ---------- 限速（进程内存态，单实例足够）----------
var rateMu sync.Mutex
var confirmAt = map[string]time.Time{} // confirm：订单维度 ≥5s
var pollAt = map[string]time.Time{}    // status 轮询：订单维度 ≥2s

func allowConfirm(id string) bool {
	rateMu.Lock()
	defer rateMu.Unlock()
	if t, ok := confirmAt[id]; ok && time.Since(t) < 5*time.Second {
		return false
	}
	confirmAt[id] = time.Now()
	return true
}

func allowPoll(id string) bool {
	rateMu.Lock()
	defer rateMu.Unlock()
	if t, ok := pollAt[id]; ok && time.Since(t) < 2*time.Second {
		return false
	}
	pollAt[id] = time.Now()
	return true
}

// ---------- 匿名下单限速（单 IP 每分钟 ≤3 单；仅真实创建计数，复用不计数） ----------
const createPerMinute = 3

var createMu sync.Mutex
var createAt = map[string][]time.Time{}

func allowCreate(ip string) bool {
	createMu.Lock()
	defer createMu.Unlock()
	now := time.Now()
	recent := make([]time.Time, 0, createPerMinute)
	for _, t := range createAt[ip] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= createPerMinute {
		createAt[ip] = recent
		return false
	}
	createAt[ip] = append(recent, now)
	if len(createAt) > 4096 { // 防御性清理：仅保留有近期记录的 IP
		for k, v := range createAt {
			if len(v) == 0 {
				delete(createAt, k)
			}
		}
	}
	return true
}

// ---------- 端点 ----------
func payMeta(c *gin.Context) {
	c.JSON(200, gin.H{"enabled": wxEnabled(), "h5Enabled": wxEnabled() && wxcfgH5(), "mock": wxMock()})
}

func wxcfgH5() bool {
	wxcfg.mu.RLock()
	defer wxcfg.mu.RUnlock()
	return wxcfg.h5Enabled
}

func payCreate(c *gin.Context) { alacartePayCreate(c) }

func nullS(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func orderPayload(id, channel, codeURL, h5URL string) gin.H {
	out := gin.H{"id": id, "channel": channel,
		"expires_in": 1800}
	if codeURL != "" {
		png, err := qrcode.Encode(codeURL, qrcode.Medium, 512)
		if err == nil {
			out["qr"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
		}
		out["code_url"] = codeURL
	}
	if h5URL != "" {
		out["h5_url"] = h5URL
	}
	return out
}

func payOrderStatus(c *gin.Context) {
	id := c.Param("id")
	if !orderExists(id) {
		c.JSON(404, gin.H{"error": "订单不存在"})
		return
	}
	if allowPoll(id) {
		queryAndSettle(id)
	}
	c.JSON(200, orderView(id))
}

// payOrderDetail：公开只读单订单查询（订单 ID 为不可枚举凭证；不触发渠道查单）
func payOrderDetail(c *gin.Context) {
	id := c.Param("id")
	if !orderExists(id) {
		c.JSON(404, gin.H{"error": "订单不存在"})
		return
	}
	c.JSON(200, orderView(id))
}

func payConfirm(c *gin.Context) {
	id := c.Param("id")
	if !orderExists(id) {
		c.JSON(404, gin.H{"error": "订单不存在"})
		return
	}
	if !allowConfirm(id) {
		c.JSON(429, gin.H{"error": "查询过于频繁，请 5 秒后再试"})
		return
	}
	queryAndSettle(id)
	c.JSON(200, orderView(id))
}

func myOrders(c *gin.Context) {
	vid := visitorID(c)
	gateCode := ""
	if gate := gateOK(c); gate != nil {
		if vid!="" {var bound string;if pool.QueryRow(c,"SELECT COALESCE(bound_visitor,'') FROM invite_codes WHERE code=$1 AND revoked=false",gate.Code).Scan(&bound)==nil && bound==vid {gateCode=gate.Code}}
	}
	if vid == "" && gateCode == "" {
		c.JSON(200, []gin.H{})
		return
	}
	rows, err := pool.Query(contextBG(), `
		SELECT o.id, o.amount_cents, o.channel, o.status, o.mock, o.created_at, o.paid_at,
		       COALESCE(o.code,''), COALESCE(ic.used,0), COALESCE(ic.max_uses,0), COALESCE(ic.revoked,false)
		FROM orders o LEFT JOIN invite_codes ic ON ic.code = o.code
		WHERE ($1<>'' AND o.code=$1)
		   OR ($2<>'' AND (o.visitor=$2 OR o.code IN (SELECT code FROM invite_codes WHERE bound_visitor=$2)))
		ORDER BY o.created_at DESC LIMIT 50`, gateCode, vid)
 if err!=nil{c.JSON(503,gin.H{"error":"订单暂不可用"});return}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id, channel, status string
		var cents int64
		var mock bool
		var created time.Time
		var paid *time.Time
		var ocode string
		var cUsed, cMax int
		var cRev bool
		if err := rows.Scan(&id, &cents, &channel, &status, &mock, &created, &paid,
			&ocode, &cUsed, &cMax, &cRev); err != nil {
			continue
		}
		paidStr := ""
		if paid != nil {
			paidStr = paid.Format("01-02 15:04")
		}
		codeState := ""
		if ocode != "" {
			switch {
			case cRev:
				codeState = "已作废"
			case cUsed >= cMax:
				codeState = "已使用"
			default:
				codeState = "未使用"
			}
		}
		out = append(out, gin.H{"id": id, "amount_cents": cents, "channel": channel,
			"status": status, "mock": mock, "created_at": created.Format("01-02 15:04"), "paid_at": paidStr,
			"invite_code": ocode, "code_state": codeState})
	}
	c.JSON(200, out)
}

// ---------- 回调 ----------
func payNotify(c *gin.Context) {
	body, _ := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20+1))
	fail := func(code int, msg string) {
		audit("", "wx_notify_reject", "order", "", fmt.Sprintf(`{"reason":"%s"}`, msg))
		c.JSON(code, gin.H{"code": "FAIL", "message": msg})
	}
	if !wxCallbackReady() {
		c.JSON(503, gin.H{"code": "FAIL", "message": "回调验签未配置（主动查单模式下正常运行）"})
		return
	}
	if err := verifyNotifyHeaders(
		c.GetHeader("Wechatpay-Serial"),
		c.GetHeader("Wechatpay-Timestamp"),
		c.GetHeader("Wechatpay-Nonce"),
		string(body),
		c.GetHeader("Wechatpay-Signature")); err != nil {
		fail(401, err.Error())
		return
	}
	var evt struct {
		EventType string `json:"event_type"`
		Resource  struct {
			Algorithm      string `json:"algorithm"`
			Ciphertext     string `json:"ciphertext"`
			Nonce          string `json:"nonce"`
			AssociatedData string `json:"associated_data"`
		} `json:"resource"`
	}
	if json.Unmarshal(body, &evt) != nil {
		fail(400, "报文格式无效")
		return
	}
	if evt.EventType != "TRANSACTION.SUCCESS" {
		fail(400, "event_type 不支持: "+evt.EventType) // 非成功事件明确拒绝，微信侧停止重试
		return
	}
	if evt.Resource.Algorithm != "AEAD_AES_256_GCM" {
		fail(400, "algorithm 不支持")
		return
	}
	wxcfg.mu.RLock()
	key := wxcfg.apiV3Key
	wxcfg.mu.RUnlock()
	pt, err := decryptAESGCM(key, evt.Resource.Nonce, evt.Resource.AssociatedData, evt.Resource.Ciphertext)
	if err != nil {
		fail(400, err.Error())
		return
	}
	var res wxNotifyResource
	if json.Unmarshal(pt, &res) != nil {
		fail(400, "resource 解析失败")
		return
	}
	if res.Amount.Currency!="CNY" || res.TransactionID=="" {fail(400,"交易信息无效");return}
 if err := settleOrder(res.OutTradeNo, res.Mchid, res.Appid, res.Amount.Total, res.TransactionID); err != nil {
		fail(403, err.Error()) // 金额不符/订单不存在/mchid 不匹配 → 拒绝
		return
	}
	c.JSON(200, gin.H{"code": "SUCCESS"})
}

// ---------- 入账（回调与查单共用；并发只入账一次）----------
// ---------- 查单落账 ----------
func queryAndSettle(id string) {
	var status string
	var mock bool;var cents int64
 if pool.QueryRow(contextBG(), "SELECT status,mock,amount_cents FROM orders WHERE id=$1", id).Scan(&status,&mock,&cents)!=nil{return}
	if status == "paid" {
		return
	}
	if mock {
		if q := mockQueryOrder(id); q != nil && q.TradeState == "SUCCESS" {
			settleOrder(id, q.Mchid, q.Appid, q.Amount.Total, q.TransactionID)
		}
		return
	}
	if !wxReady() {
		return
	}
	q, err := wxQueryOrder(id)
	if err != nil {
		return
	}
	_ = applyChannelOrder(id,cents,q)
}

// Apply the already signature-verified response; do not discard a known fact
// by querying again. Both polling and explicit recovery use the same checks.
func applyChannelOrder(id string,cents int64,q *wxQueryResp) error {
	if !validChannelOrder(q,id,cents){return fmt.Errorf("渠道订单信息待人工核对")}
 switch q.TradeState {
	case "SUCCESS":
		return settleOrder(id, q.Mchid, q.Appid, q.Amount.Total, q.TransactionID)
	case "CLOSED":
		_,err:=pool.Exec(contextBG(), "UPDATE orders SET status='closed', updated_at=now() WHERE id=$1 AND status IN ('pending','expired')", id);return err
	case "PAYERROR", "REFUND":
		_,err:=pool.Exec(contextBG(), "UPDATE orders SET status='failed', updated_at=now() WHERE id=$1 AND status IN ('pending','expired')", id);return err
	}
	return nil
}

// orderExists：按 ID 判在（公开端点的存在性检查，替代原 ownership 校验；
// 订单 ID 含 64bit 随机熵，不可枚举）
func orderExists(id string) bool {
	var one int
	return pool.QueryRow(contextBG(), "SELECT 1 FROM orders WHERE id=$1", id).Scan(&one) == nil
}

func orderView(id string) gin.H {
	var cents int64
	var channel, status, code string
	var mock bool
	var created time.Time
	var paid *time.Time
	pool.QueryRow(contextBG(),
		"SELECT amount_cents, channel, status, COALESCE(code,''), mock, created_at, paid_at FROM orders WHERE id=$1", id).
		Scan(&cents, &channel, &status, &code, &mock, &created, &paid)
	var job string
 pool.QueryRow(contextBG(),"SELECT COALESCE(job_id,'') FROM orders WHERE id=$1",id).Scan(&job)
 h := gin.H{"job_id":job,"id": id, "amount_cents": cents, "channel": channel,
		"status": status, "created_at": created.Format("01-02 15:04")}
	if paid != nil {
		h["paid_at"] = paid.Format("01-02 15:04")
	}
	// 支付成功后才公开邀请码（匿名单的码入账前 max_uses=0，不可用）
	if status == "paid" && code != "" {
		h["invite_code"] = code
	}
	return h
}

// ---------- 对账 worker：持久化未决订单按退避时间公平核对；不以订单年龄丢弃付款事实 ----------
func startReconciler() {
 go func() { for { reconcileOnce(); time.Sleep(30*time.Second) } }()
}

// Finish reading before channel IO. A partial/failed database read must not
// become a list of orders eligible for financial mutation.
func reconciliationIDs(query string) ([]string,error) {
 rows,err:=pool.Query(contextBG(),query);if err!=nil{return nil,err};defer rows.Close()
 var ids []string
 for rows.Next(){var id string;if err=rows.Scan(&id);err!=nil{return nil,err};ids=append(ids,id)}
 if err=rows.Err();err!=nil{return nil,err};return ids,nil
}

func reconcileOnce() {
 // Pending and historical expired records remain recoverable after any outage.
 // Bounded oldest-first batches prevent the same early rows starving the rest.
 for _,query:=range []string{
  `SELECT id FROM orders WHERE status='pending' AND updated_at < now()-interval '30 seconds' ORDER BY updated_at,id LIMIT 50`,
  `SELECT id FROM orders WHERE status='expired' AND updated_at < now()-interval '30 seconds' ORDER BY updated_at,id LIMIT 30`,
 }{
  ids,err:=reconciliationIDs(query);if err!=nil{return}
  for _,id:=range ids{queryAndSettle(id);pool.Exec(contextBG(),"UPDATE orders SET updated_at=now() WHERE id=$1",id)}
 }
 ids,err:=reconciliationIDs(`SELECT id FROM orders WHERE status='pending' AND created_at < now()-interval '2 hours'
 AND (checkout_lease_until IS NULL OR checkout_lease_until<=now()) ORDER BY updated_at,id LIMIT 20`)
 if err!=nil{return};for _,id:=range ids{reconcileOverdueCheckout(id)}
}

// The worker and explicit browser recovery use the same persisted order lease.
// It survives service restarts; the atomic condition also protects two workers
// that selected the same order before either one acquired the lease.
func reconcileOverdueCheckout(id string) {
 var cents int64;var mock bool
 err:=pool.QueryRow(contextBG(),`UPDATE orders SET checkout_lease_until=now()+interval '1 minute'
 WHERE id=$1 AND status='pending' AND created_at < now()-interval '2 hours'
 AND (checkout_lease_until IS NULL OR checkout_lease_until<=now()) RETURNING amount_cents,mock`,id).Scan(&cents,&mock)
 if err!=nil{return}
 if mock{
  queryAndSettle(id)
  pool.Exec(contextBG(),"UPDATE orders SET status='closed',updated_at=now() WHERE id=$1 AND status='pending'",id)
  return
 }
 if !wxReady(){return}
 q,err:=wxQueryOrder(id)
 // Failure/invalid identity is not NOTPAY. Never close based on local age alone.
 if err!=nil||!validChannelOrder(q,id,cents){return}
 if q.TradeState!="NOTPAY"{_ = applyChannelOrder(id,cents,q);return}
 _,_ = closeKnownCheckout(id)
}

func validChannelOrder(q *wxQueryResp,id string,cents int64)bool {
 wxcfg.mu.RLock();m,a:=wxcfg.mchid,wxcfg.appid;wxcfg.mu.RUnlock()
 return q!=nil&&q.OutTradeNo==id&&q.Amount.Total==cents&&q.Amount.Currency=="CNY"&&(m==""||q.Mchid==m)&&(a==""||q.Appid==a)
}
