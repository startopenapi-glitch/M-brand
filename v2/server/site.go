package app

// site.go — V2：匿名会话身份、免费试用、草稿、找回、价格单源
//
// 第一性原理：身份/权益/凭证分离——
//   身份 = 匿名签名 cookie（visitor）     进门不验票
//   权益 = 已核验的按项订单；历史票仅保留原已购权益
//   凭证 = 邀请码/订单号可互相找回（跨设备迁移）

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// ---------- 会话身份（匿名，签名 cookie） ----------

const visitorCookie = "zb_visitor"

func setVisitorCookie(c *gin.Context, id string) {
	p := gatePayload{Code: id, Expiry: time.Now().Add(365 * 24 * time.Hour).Unix()}
	c.SetCookie(visitorCookie, makeCookieValue(p), 365*24*3600, "/", "", true, true) // Path=/(根空间为规范 URL；/0brand 为兼容空间)
}

// visitorID：只读当前会话 id（可为空）
func visitorID(c *gin.Context) string {
	if p := readCookie(c, visitorCookie); p != nil && p.Admin == 0 && p.Code != "" {
		return p.Code
	}
	return ""
}

// ensureVisitor：读取或签发会话身份（匿名，不代表任何额度）
func ensureVisitor(c *gin.Context) string {
	if id := visitorID(c); id != "" {
		pool.Exec(contextBG(), "UPDATE visitors SET last_seen=now() WHERE id=$1", id)
		return id
	}
	id := randHex(16)
	pool.Exec(contextBG(), "INSERT INTO visitors(id) VALUES ($1) ON CONFLICT DO NOTHING", id)
	setVisitorCookie(c, id)
	return id
}

// ---------- 通用滑动窗口限速 ----------

var rlMu sync.Mutex
var rlMap = map[string][]time.Time{}

func rateBlocked(key string, limit int) bool {
 rlMu.Lock(); defer rlMu.Unlock()
 recent:=0
 for _,at:=range rlMap[key] {if time.Since(at)<time.Minute {recent++}}
 return recent>=limit
}

func rateOK(key string, perMinute int) bool {
	rlMu.Lock()
	defer rlMu.Unlock()
	now := time.Now()
	keep := make([]time.Time, 0, perMinute)
	for _, t := range rlMap[key] {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	if len(keep) >= perMinute {
		rlMap[key] = keep
		return false
	}
	rlMap[key] = append(keep, now)
	if len(rlMap) > 8192 { // 防御性清理
		for k, v := range rlMap {
			if len(v) == 0 {
				delete(rlMap, k)
			}
		}
	}
	return true
}

// ---------- 每品牌免费方向与系统卡（保留短时防刷及维护保护） ----------

func envInt(key string, def int) int {
	if s := strings.TrimSpace(os.Getenv(key)); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
	}
	return def
}

func trialEnabled() bool   { return os.Getenv("ZEROBRAND_TRIAL_ENABLED") != "0" }
func trialPerSession() int { return 0 } // 0 means each new brand is free; not a lifetime quota.
func trialPerIP() int { return 0 }
// Free proposals have no lifetime/daily purchase quota. Short-window request
// limiting and the provider health circuit still protect the generation service.
type trialQuerier interface { QueryRow(context.Context, string, ...any) pgx.Row }
func allowTrialQuery(ctx context.Context,q trialQuerier,vid,ip string)(bool,string){
 if !trialEnabled(){return false,"免费生成暂未开放，请稍后再试"};return true,""
}

// ---------- 票（邀请码 = 额度载体，与 SKILL 商店同构） ----------

// ticketCandidates：可用票 = 本会话绑定的码 / 本任务携带且未绑定或归属本会话的码（一码一人）
// 不再接受「gate 码直通」——知道码文本不等于拥有它（旧行为可被他人占票）
func ticketCandidates(vid, jobCode, gateCode string) []string {
	_ = gateCode
	// 注意：SQL 只引用 $1/$2（pgx 无法推断未引用参数的类型 → 整条查询报错）
	rows, err := pool.Query(contextBG(), `
		SELECT code FROM invite_codes
		WHERE revoked=false AND used<max_uses
		  AND ((code=$1 AND $1<>'' AND (COALESCE(bound_visitor,'')='' OR bound_visitor=$2))
		       OR (bound_visitor=$2 AND $2<>''))
		ORDER BY (code=$1) DESC, created_at ASC LIMIT 5`, jobCode, vid)
	if err != nil {
		slog.Warn("ticketCandidates 查询失败", "err", err)
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var cd string
		if rows.Scan(&cd) == nil {
			out = append(out, cd)
		}
	}
	return out
}

func hasTicket(vid, jobCode, gateCode string) bool {
	return len(ticketCandidates(vid, jobCode, gateCode)) > 0
}

// jobOwned：任务归属（本会话 / 名下的码 / 当前 gate 码 / 管理员）
func jobOwned(c *gin.Context, vid, gateCode, jobVisitor, jobCode string) bool {
	return jobOwnedQuery(c,pool,vid,gateCode,jobVisitor,jobCode)
}
func jobOwnedQuery(c *gin.Context, q trialQuerier, vid, gateCode, jobVisitor, jobCode string) bool {
	if adminOK(c) != nil {
		return true
	}
	if vid != "" && jobVisitor != "" && jobVisitor == vid {
		return true
	}
	_ = gateCode // cookie文本不能代替当前数据库归属
	if vid != "" && jobCode != "" {
		var one int
		if q.QueryRow(c.Request.Context(),
			"SELECT 1 FROM invite_codes WHERE code=$1 AND bound_visitor=$2 AND revoked=false", jobCode, vid).Scan(&one) == nil {
			return true
		}
	}
	return false
}

// ---------- 价格与权益（单源：前端所有界面从这里渲染） ----------

func planPayload() gin.H {
	paused, pausedReason := salesPaused()
	return gin.H{
		"price_fen":        bundlePriceCents,
		"price_label":      "¥99.00",
		"direction_unlock_price_cents": directionUnlockPriceCents,
		"title":            "M-brand 按项生成",
		"trial_directions": 3,
		"includes": []string{"¥9.90 解锁自己的品牌（3 个 Logo 方向 + Logo 组合卡）","按需选择 17 项交付物，仅生成已购内容","全选 ¥99.00，支持追加购买"},
		"trial": gin.H{
			"enabled":     trialEnabled() && !directionsPaywallEnabled(),
			"directions":  3,
			"per_session": trialPerSession(),
			"per_ip_day":  trialPerIP(),
		},
		"pay_enabled":   wxEnabled(),
		"h5":            wxEnabled() && wxcfgH5(),
		"sales_paused":  paused,
		"paused_reason": pausedReason,
		"fallback_img":  fallbackConfigured(),
	}
}

func planHandler(c *gin.Context) { c.JSON(200, planPayload()) }

// ---------- 草稿（属于会话，永不丢失） ----------

func draftGet(c *gin.Context) {
	vid := visitorID(c)
	if vid == "" {
		c.JSON(200, gin.H{})
		return
	}
	var raw []byte
	err:=pool.QueryRow(c.Request.Context(), "SELECT payload FROM drafts WHERE visitor_id=$1", vid).Scan(&raw)
	if err==pgx.ErrNoRows {
		c.JSON(200, gin.H{})
		return
	}
 if err!=nil{readFailure(c,err,"草稿不存在");return}
	var out map[string]string
	if json.Unmarshal(raw, &out) != nil {
		c.JSON(503,gin.H{"error":"已保存草稿暂时无法读取，请稍后重试"});return
	}
	c.JSON(200, out)
}

func draftPut(c *gin.Context) {
	vid := ensureVisitor(c)
	if !rateOK("draft:"+vid, 30) {
		c.JSON(429, gin.H{"error": "保存过于频繁"})
		return
	}
	var form map[string]string
	if !readJSONBodyLimit(c,&form,16<<10) { return }
	if form==nil { c.JSON(400,gin.H{"error":"草稿数据无效"}); return }
	body,_ := json.Marshal(form)
	if _, err := pool.Exec(contextBG(),
		`INSERT INTO drafts(visitor_id,payload,updated_at) VALUES($1,$2,now())
		 ON CONFLICT (visitor_id) DO UPDATE SET payload=$2, updated_at=now()`, vid, body); err != nil {
		c.JSON(500, gin.H{"error": "系统错误"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// ---------- 找回（凭订单号取回票并绑定本会话） ----------

func recoverOrder(c *gin.Context) {
	ip := c.ClientIP()
	if !rateOK("recover:"+ip, 5) {
		c.JSON(429, gin.H{"error": "查询过于频繁，请稍后再试"})
		return
	}
	var req struct {
		OrderID string `json:"order_id"`
	}
	if c.BindJSON(&req) != nil || len(strings.TrimSpace(req.OrderID)) < 8 {
		c.JSON(400, gin.H{"error": "请输入完整订单号"})
		return
	}
	oid := strings.TrimSpace(req.OrderID)
	var status, code string
	if err := pool.QueryRow(contextBG(),
		"SELECT status, COALESCE(code,'') FROM orders WHERE id=$1", oid).Scan(&status, &code); err != nil {
		// 也接受邀请码直接找回（票随码走）
		up := strings.ToUpper(oid)
		if len(up) < 6 || len(up) > 24 {
			c.JSON(404, gin.H{"error": "订单不存在"})
			return
		}
		var max, used int
		var revoked bool
		var bound string
		if e2 := pool.QueryRow(contextBG(),
			"SELECT max_uses, used, revoked, COALESCE(bound_visitor,'') FROM invite_codes WHERE code=$1", up).
			Scan(&max, &used, &revoked, &bound); e2 != nil || revoked {
			c.JSON(404, gin.H{"error": "订单不存在或邀请码无效"})
			return
		}
		// 一码一人：已绑定他人或已使用的码不可凭码找回（防抢绑）；换设备请用订单号
		if bound != "" || used > 0 {
			c.JSON(403, gin.H{"error": "该邀请码已被使用或已绑定；请凭订单号找回"})
			return
		}
		vid := ensureVisitor(c)
		tag, bindErr := pool.Exec(contextBG(),
			"UPDATE invite_codes SET bound_visitor=$2 WHERE code=$1 AND revoked=false AND used=0 AND (bound_visitor IS NULL OR bound_visitor='')", up, vid)
		if bindErr != nil { c.JSON(500,gin.H{"error":"系统错误"}); return }
		if tag.RowsAffected()!=1 { c.JSON(403,gin.H{"error":"该邀请码已被使用或已绑定；请凭订单号找回"}); return }
		setGateCookie(c, up)
		audit("visitor:"+vid, "recover_bind", "invite_code", up, "")
		c.JSON(200, gin.H{"ok": true, "code": up, "remaining": max - used})
		return
	}
 // Current item orders recover their purchased project without minting a ticket.
 var purchasedJob string
 if e:=pool.QueryRow(contextBG(),"SELECT COALESCE(job_id,'') FROM orders WHERE id=$1",oid).Scan(&purchasedJob);e==nil && purchasedJob!="" {
  if status!="paid" {c.JSON(403,gin.H{"error":"订单尚未支付"});return}
  vid:=ensureVisitor(c);tx,e:=pool.Begin(c);if e!=nil{c.JSON(503,gin.H{"error":"暂时无法找回"});return};defer tx.Rollback(contextBG())
  var locked string;if e=tx.QueryRow(c,"SELECT id FROM jobs WHERE id=$1 FOR UPDATE",purchasedJob).Scan(&locked);e!=nil{c.JSON(404,gin.H{"error":"提案不存在"});return}
  var st string;if e=tx.QueryRow(c,"SELECT status FROM orders WHERE id=$1 FOR UPDATE",oid).Scan(&st);e!=nil||st!="paid"{c.JSON(403,gin.H{"error":"订单权益不可用"});return}
  if _,e=tx.Exec(c,"UPDATE jobs SET visitor=$2,updated_at=now() WHERE id=$1",purchasedJob,vid);e!=nil{c.JSON(503,gin.H{"error":"暂时无法找回"});return}
  if _,e=tx.Exec(c,"UPDATE orders SET visitor=$2 WHERE job_id=$1",purchasedJob,vid);e!=nil{c.JSON(503,gin.H{"error":"暂时无法找回"});return}
  if _,e=tx.Exec(c,`UPDATE invite_codes SET bound_visitor=$2 WHERE code IN (SELECT COALESCE(NULLIF(quota_code,''),code,'') FROM jobs WHERE id=$1)`,purchasedJob,vid);e!=nil{c.JSON(503,gin.H{"error":"暂时无法找回"});return}
  if e=tx.Commit(contextBG());e!=nil{c.JSON(503,gin.H{"error":"暂时无法确认找回结果"});return}
  c.JSON(200,gin.H{"ok":true,"job_id":purchasedJob});return
 }
	if status != "paid" || code == "" {
		c.JSON(403, gin.H{"error": "该订单尚未支付成功，暂无权益可找回"})
		return
	}
	vid := ensureVisitor(c)
	ctx:=c.Request.Context()
	tx,err:=pool.Begin(ctx)
	if err!=nil {c.JSON(500,gin.H{"error":"系统错误"});return}
	defer tx.Rollback(ctx)
	// 订单凭证、票归属、已购交付在同一事务迁移，旧设备不再保留访问权。
	if err=tx.QueryRow(ctx,"SELECT status,COALESCE(code,'') FROM orders WHERE id=$1 FOR UPDATE",oid).Scan(&status,&code);err!=nil || status!="paid" || code=="" {c.JSON(403,gin.H{"error":"该订单暂无可找回权益"});return}
	if _,err=tx.Exec(ctx,"SELECT id FROM jobs WHERE quota_code=$1 OR (COALESCE(quota_code,'')='' AND code=$1) ORDER BY id FOR UPDATE",code);err!=nil {c.JSON(500,gin.H{"error":"系统错误"});return}
	var max,used int
	if err=tx.QueryRow(ctx,"UPDATE invite_codes SET bound_visitor=$2 WHERE code=$1 AND revoked=false RETURNING max_uses,used",code,vid).Scan(&max,&used);err!=nil {c.JSON(403,gin.H{"error":"该订单权益暂不可用，请联系支持"});return}
	if _,err=tx.Exec(ctx,"UPDATE jobs SET visitor=$2,updated_at=now() WHERE quota_code=$1 OR (COALESCE(quota_code,'')='' AND code=$1)",code,vid);err!=nil {c.JSON(500,gin.H{"error":"系统错误"});return}
 if _,err=tx.Exec(ctx,"UPDATE orders SET visitor=$2 WHERE code=$1",code,vid);err!=nil{c.JSON(500,gin.H{"error":"系统错误"});return}
	if err=tx.Commit(ctx);err!=nil {c.JSON(500,gin.H{"error":"系统错误"});return}
	setGateCookie(c,code)
	audit("visitor:"+vid,"recover_bind","invite_code",code,"")
	c.JSON(200,gin.H{"ok":true,"code":code,"remaining":max-used})
}

// webpify：上架时把生成的 PNG 转 WebP（服务器有 cwebp；失败则原样返回，不阻断上架）
func webpify(rel string) string {
	if !strings.HasPrefix(rel, "gen/") || !strings.HasSuffix(strings.ToLower(rel), ".png") {
		return rel
	}
	src := filepath.Join(genDir(), strings.TrimPrefix(rel, "gen/"))
	dst := strings.TrimSuffix(src, ".png") + ".webp"
	if st, err := os.Stat(dst); err == nil {
		if si, err2 := os.Stat(src); err2 == nil && st.ModTime().After(si.ModTime()) {
			return strings.TrimSuffix(rel, ".png") + ".webp"
		}
	}
	if err := exec.Command("cwebp", "-quiet", "-q", "82", src, "-o", dst).Run(); err != nil {
		slog.Warn("webp 转换失败，上架使用原图", "file", rel, "err", err)
		return rel
	}
	return strings.TrimSuffix(rel, ".png") + ".webp"
}

// ---------- 我的提案（会话归属） ----------

func myJobs(c *gin.Context) {
	vid := visitorID(c)
	if vid == "" {
		c.JSON(200, []gin.H{})
		return
	}
	rows, err := pool.Query(c.Request.Context(), `
		SELECT id, status, stage, COALESCE(chosen,-1), trial, created_at,
		       (SELECT file FROM job_images ji WHERE ji.job_id=jobs.id AND ji.kind='direction' ORDER BY ji.idx LIMIT 1),
		       (SELECT COUNT(*) FROM job_images ji WHERE ji.job_id=jobs.id)
		FROM jobs WHERE visitor=$1 ORDER BY created_at DESC LIMIT 20`, vid)
 if err!=nil{readFailure(c,err,"提案暂不可用");return}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id, status, stage string
		var chosen, imgs int
		var trial bool
		var created time.Time
		var thumb *string
		if err=rows.Scan(&id, &status, &stage, &chosen, &trial, &created, &thumb, &imgs);err!=nil {
			readFailure(c,err,"提案暂不可用");return
		}
		th := ""
		if thumb != nil {
			th = *thumb
		}
		out = append(out, gin.H{"id": id, "status": status, "stage": stage, "chosen": chosen,
			"trial": trial, "images": imgs, "thumb": th, "created_at": created.Format("01-02 15:04")})
	}
 if err=rows.Err();err!=nil{readFailure(c,err,"提案暂不可用");return}
	c.JSON(200, out)
}
