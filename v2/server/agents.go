package app

// agents.go — v5 代理分销：登录 + 业绩查看 + 管理员CRUD + 佣金自动记录

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/startopenapi-glitch/M-brand/v2/server/internal/platform"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

const agentCookie = "zb_agent"
const refCookie = "zb_ref"

// ---------- 代理登录 ----------

func agentLogin(c *gin.Context) {
	if !rateOK("agent-login:"+c.ClientIP(), 10) {
		c.JSON(429, gin.H{"error": "尝试过于频繁，请一分钟后重试"})
		return
	}
	var req struct{ Email, Password string }
	if c.BindJSON(&req) != nil || req.Email == "" || req.Password == "" {
		c.JSON(400, gin.H{"error": "请输入邮箱和密码"})
		return
	}
	var id, name, code string
	var hash string
	var active bool
	var pct int
	var version int64
	err := pool.QueryRow(contextBG(),
		"SELECT id, name, code, password_hash, active, commission_pct, session_version FROM agents WHERE lower(email)=lower($1)",
		req.Email).Scan(&id, &name, &code, &hash, &active, &pct, &version)
	if err != nil || !active || bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		c.JSON(401, gin.H{"error": "邮箱或密码不正确"})
		return
	}
	// 设置代理会话 cookie（7 天）
	p := gatePayload{Admin: 0, Code: id, Expiry: time.Now().Add(7 * 24 * time.Hour).Unix(), CSRF: randHex(8), Purpose: "agent", SessionVersion: version}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(agentCookie, "", -1, basePath, "", true, true)
	c.SetCookie(agentCookie, makeCookieValue(p), 7*24*3600, "/", "", true, true)
	c.JSON(200, gin.H{"ok": true, "name": name, "code": code})
}

func agentOK(c *gin.Context) *gatePayload {
	p := readCookie(c, agentCookie)
	if p == nil || p.Admin != 0 || (p.Purpose != "" && p.Purpose != "agent") {
		return nil
	}
	var ok bool
	if pool.QueryRow(c, "SELECT active AND session_version=$2 FROM agents WHERE id=$1", p.Code, p.SessionVersion).Scan(&ok) != nil || !ok {
		return nil
	}
	return p
}

// ---------- 代理信息 ----------

func agentMe(c *gin.Context) {
	p := agentOK(c)
	if p == nil {
		c.JSON(401, gin.H{"error": "请先登录"})
		return
	}
	var name, email, code, wechat string
	var pct int
	var active bool
	var earned, paid, pendingPayout int64
	var payoutMethod, payoutAccount, payoutName string
	pool.QueryRow(contextBG(),
		`SELECT name, email, code, wechat, commission_pct, active, total_earned_cents, total_paid_cents,
		        COALESCE(payout_method,''),COALESCE(payout_account,''),COALESCE(payout_name,'')
		 FROM agents WHERE id=$1`, p.Code).Scan(
		&name, &email, &code, &wechat, &pct, &active, &earned, &paid, &payoutMethod, &payoutAccount, &payoutName)
	pool.QueryRow(contextBG(), `SELECT COALESCE(SUM(amount_cents),0) FROM agent_payouts WHERE agent_id=$1 AND status IN ('pending_review','approved')`, p.Code).Scan(&pendingPayout)
	link := fmt.Sprintf("%s/?ref=%s", platform.LoadConfig().PublicOrigin, code)
	c.JSON(200, gin.H{
		"name": name, "email": email, "code": code, "wechat": wechat,
		"commission_pct": pct, "active": active,
		"link":                     link,
		"total_earned_cents":       earned,
		"total_paid_cents":         paid,
		"pending_withdrawal_cents": pendingPayout,
		"withdrawable_cents":       earned - paid - pendingPayout,
		"payout_method":            payoutMethod,
		"payout_account":           maskAccount(payoutAccount),
		"payout_name":              payoutName,
	})
}

func agentStats(c *gin.Context) {
	p := agentOK(c)
	if p == nil {
		c.JSON(401, gin.H{"error": "请先登录"})
		return
	}
	var orderCount int
	var totalEarned, totalPaid int64
	pool.QueryRow(contextBG(),
		`SELECT COUNT(*), COALESCE(SUM(commission_cents),0) FROM agent_commissions WHERE agent_id=$1`,
		p.Code).Scan(&orderCount, &totalEarned)
	pool.QueryRow(contextBG(),
		"SELECT total_paid_cents FROM agents WHERE id=$1", p.Code).Scan(&totalPaid)
	var pendingPayout int64
	pool.QueryRow(contextBG(), `SELECT COALESCE(SUM(amount_cents),0) FROM agent_payouts WHERE agent_id=$1 AND status IN ('pending_review','approved')`, p.Code).Scan(&pendingPayout)
	var pendingCount int
	var pendingAmount int64
	pool.QueryRow(contextBG(),
		`SELECT COUNT(*), COALESCE(SUM(commission_cents-settled_cents),0) FROM agent_commissions
		 WHERE agent_id=$1 AND status='pending'`, p.Code).Scan(&pendingCount, &pendingAmount)
	c.JSON(200, gin.H{
		"order_count":              orderCount,
		"total_earned_cents":       totalEarned,
		"total_paid_cents":         totalPaid,
		"withdrawable_cents":       totalEarned - totalPaid - pendingPayout,
		"pending_withdrawal_cents": pendingPayout,
		"pending_count":            pendingCount,
		"pending_cents":            pendingAmount,
	})
}

func agentCommissions(c *gin.Context) {
	p := agentOK(c)
	if p == nil {
		c.JSON(401, gin.H{"error": "请先登录"})
		return
	}
	rows, err := pool.Query(contextBG(),
		`SELECT ac.order_id, ac.order_amount_cents, ac.commission_cents, ac.status, ac.created_at
		 FROM agent_commissions ac WHERE ac.agent_id=$1 ORDER BY ac.id DESC LIMIT 100`, p.Code)
	if err != nil {
		c.JSON(503, gin.H{"error": "数据暂不可用"})
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var oid, status string
		var orderAmt, commAmt int64
		var created time.Time
		if err = rows.Scan(&oid, &orderAmt, &commAmt, &status, &created); err != nil {
			c.JSON(503, gin.H{"error": "账务暂不可用"})
			return
		}
		out = append(out, gin.H{
			"order_id":           agentOrderReference(p.Code, oid),
			"order_amount_cents": orderAmt,
			"commission_cents":   commAmt,
			"status":             status,
			"created_at":         created.Format("01-02 15:04"),
		})
	}
	if rows.Err() != nil {
		c.JSON(503, gin.H{"error": "数据暂不可用"})
		return
	}
	c.JSON(200, out)
}

// ---------- 管理员：代理 CRUD ----------

func adminListAgents(c *gin.Context) {
	rows, err := pool.Query(contextBG(),
		`SELECT id, name, email, code, commission_pct, active,
		        total_earned_cents, total_paid_cents, COALESCE(wechat,''), created_at
		 FROM agents ORDER BY created_at DESC`)
	if err != nil {
		c.JSON(503, gin.H{"error": "数据暂不可用"})
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id, name, email, code, wechat string
		var pct int
		var active bool
		var earned, paid int64
		var created time.Time
		if err = rows.Scan(&id, &name, &email, &code, &pct, &active, &earned, &paid, &wechat, &created); err != nil {
			c.JSON(503, gin.H{"error": "数据暂不可用"})
			return
		}
		out = append(out, gin.H{
			"id": id, "name": name, "email": email, "code": code,
			"commission_pct": pct, "active": active, "wechat": wechat,
			"total_earned_cents": earned, "total_paid_cents": paid,
			"withdrawable_cents": earned - paid,
			"created_at":         created.Format("01-02"),
		})
	}
	if rows.Err() != nil {
		c.JSON(503, gin.H{"error": "数据暂不可用"})
		return
	}
	c.JSON(200, out)
}

func adminCreateAgent(c *gin.Context) {
	var req struct {
		Name, Email, Password, Wechat string
		CommissionPct                 int
	}
	if c.BindJSON(&req) != nil || req.Name == "" || req.Email == "" || req.Password == "" {
		c.JSON(400, gin.H{"error": "名称、邮箱、密码为必填"})
		return
	}
	if req.CommissionPct <= 0 || req.CommissionPct > 90 {
		req.CommissionPct = 40
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	id := "AG" + randHex(6)
	code := "AG" + strings.ToUpper(randHex(4))
	_, err := pool.Exec(contextBG(),
		`INSERT INTO agents(id, name, email, password_hash, wechat, code, commission_pct)
		 VALUES ($1,$2,lower($3),$4,$5,$6,$7)`,
		id, req.Name, req.Email, string(hash), req.Wechat, code, req.CommissionPct)
	if err != nil {
		c.JSON(409, gin.H{"error": "创建失败（邮箱可能已存在）"})
		return
	}
	link := fmt.Sprintf("%s/?ref=%s", platform.LoadConfig().PublicOrigin, code)
	c.JSON(201, gin.H{"id": id, "code": code, "link": link, "commission_pct": req.CommissionPct})
}

func adminUpdateAgent(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Name, Wechat  *string
		CommissionPct *int
		Active        *bool
		Password      *string
	}
	if c.BindJSON(&req) != nil {
		c.JSON(400, gin.H{"error": "参数无效"})
		return
	}
	if req.CommissionPct != nil && (*req.CommissionPct <= 0 || *req.CommissionPct > 90) {
		c.JSON(400, gin.H{"error": "佣金比例无效"})
		return
	}
	var hash *string
	if req.Password != nil {
		if len(*req.Password) < 6 || len(*req.Password) > 72 {
			c.JSON(400, gin.H{"error": "密码长度应为6至72字节"})
			return
		}
		h, e := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
		if e != nil {
			c.JSON(503, gin.H{"error": "暂时无法更新密码"})
			return
		}
		v := string(h)
		hash = &v
	}
	tx, err := pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(503, gin.H{"error": "未完成更新，请重试"})
		return
	}
	defer tx.Rollback(c.Request.Context())
	var previousPct int
	err = tx.QueryRow(c, "SELECT commission_pct FROM agents WHERE id=$1 FOR UPDATE", id).Scan(&previousPct)
	if err == pgx.ErrNoRows {
		c.JSON(404, gin.H{"error": "代理不存在"})
		return
	}
	if err != nil {
		c.JSON(503, gin.H{"error": "未完成更新，请重试"})
		return
	}
	_, err = tx.Exec(c, `UPDATE agents SET name=COALESCE($2,name),wechat=COALESCE($3,wechat),commission_pct=COALESCE($4,commission_pct),
 active=COALESCE($5,active),password_hash=COALESCE($6,password_hash),session_version=session_version+CASE WHEN $7 THEN 1 ELSE 0 END WHERE id=$1`, id, req.Name, req.Wechat, req.CommissionPct, req.Active, hash, req.Active != nil || hash != nil)
	if err != nil {
		c.JSON(503, gin.H{"error": "未完成更新，请重试"})
		return
	}
	if req.CommissionPct != nil && *req.CommissionPct != previousPct {
		_, err = tx.Exec(c, `INSERT INTO audit_events(actor,action,object_type,object_id,detail) VALUES($1,'agent_commission_updated','agent',$2,jsonb_build_object('old_pct',$3::int,'new_pct',$4::int))`, adminName(c), id, previousPct, *req.CommissionPct)
		if err != nil {
			c.JSON(503, gin.H{"error": "佣金调整未完成，请重试"})
			return
		}
	}
	if err = tx.Commit(c.Request.Context()); err != nil {
		c.JSON(503, gin.H{"error": "更新结果待确认，请刷新核对"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// agentOrderReference cannot be presented to the customer recovery endpoint.
func agentOrderReference(agentID, orderID string) string {
	mac := hmac.New(sha256.New, sessionSecret)
	mac.Write([]byte("agent-order-reference\x00" + agentID + "\x00" + orderID))
	return "REF-" + hex.EncodeToString(mac.Sum(nil)[:12])
}

func adminAgentPayout(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		AmountCents int64
		Note        string
		RequestKey  string
	}
	if c.ShouldBindJSON(&req) != nil || req.AmountCents <= 0 || len(req.RequestKey) < 16 || len(req.RequestKey) > 128 {
		c.JSON(400, gin.H{"error": "金额或请求编号无效"})
		return
	}
	// A stable operation key is mandatory; uncertain responses may be retried safely.
	tx, err := pool.Begin(contextBG())
	if err != nil {
		c.JSON(503, gin.H{"error": "账务服务暂不可用"})
		return
	}
	defer tx.Rollback(contextBG())
	var earned, paid int64
	if err = tx.QueryRow(contextBG(), "SELECT total_earned_cents,total_paid_cents FROM agents WHERE id=$1 FOR UPDATE", id).Scan(&earned, &paid); err != nil {
		c.JSON(404, gin.H{"error": "代理不存在"})
		return
	}
	var oldAmount int64
	err = tx.QueryRow(contextBG(), "SELECT amount_cents FROM agent_payouts WHERE agent_id=$1 AND request_key=$2", id, req.RequestKey).Scan(&oldAmount)
	if err == nil {
		if oldAmount != req.AmountCents {
			c.JSON(409, gin.H{"error": "同一请求编号的金额不一致"})
			return
		}
		c.JSON(200, gin.H{"ok": true, "replayed": true})
		return
	}
	if err != pgx.ErrNoRows {
		c.JSON(503, gin.H{"error": "账务查询失败"})
		return
	}
	if req.AmountCents > earned-paid {
		c.JSON(409, gin.H{"error": "可结算余额不足，请刷新后核对"})
		return
	}
	rows, err := tx.Query(contextBG(), `SELECT id,commission_cents-settled_cents FROM agent_commissions
 WHERE agent_id=$1 AND status='pending' AND commission_cents>settled_cents ORDER BY id FOR UPDATE`, id)
	if err != nil {
		c.JSON(503, gin.H{"error": "账务查询失败"})
		return
	}
	type allocation struct {
		id     int
		amount int64
	}
	alloc := []allocation{}
	remain := req.AmountCents
	var ledgerRemaining int64
	for rows.Next() {
		var n int
		var available int64
		if err = rows.Scan(&n, &available); err != nil {
			break
		}
		ledgerRemaining += available
		take := available
		if take > remain {
			take = remain
		}
		if take > 0 {
			alloc = append(alloc, allocation{n, take})
			remain -= take
		}
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		c.JSON(503, gin.H{"error": "账务查询失败"})
		return
	}
	if remain != 0 || ledgerRemaining != earned-paid {
		c.JSON(409, gin.H{"error": "历史账务明细与余额不一致，请先核对，未记录结算"})
		return
	}
	var payoutID int
	if err = tx.QueryRow(contextBG(), `INSERT INTO agent_payouts(agent_id,amount_cents,note,request_key) VALUES($1,$2,$3,$4) RETURNING id`, id, req.AmountCents, req.Note, req.RequestKey).Scan(&payoutID); err != nil {
		c.JSON(503, gin.H{"error": "结算记录失败"})
		return
	}
	for _, a := range alloc {
		if _, err = tx.Exec(contextBG(), `INSERT INTO agent_payout_allocations(payout_id,commission_id,amount_cents) VALUES($1,$2,$3)`, payoutID, a.id, a.amount); err != nil {
			c.JSON(503, gin.H{"error": "结算记录失败"})
			return
		}
		if _, err = tx.Exec(contextBG(), `UPDATE agent_commissions SET settled_cents=settled_cents+$2,
   status=CASE WHEN settled_cents+$2=commission_cents THEN 'paid' ELSE 'pending' END,
   paid_at=CASE WHEN settled_cents+$2=commission_cents THEN now() ELSE NULL END WHERE id=$1`, a.id, a.amount); err != nil {
			c.JSON(503, gin.H{"error": "结算记录失败"})
			return
		}
	}
	if _, err = tx.Exec(contextBG(), "UPDATE agents SET total_paid_cents=total_paid_cents+$2 WHERE id=$1", id, req.AmountCents); err != nil {
		c.JSON(503, gin.H{"error": "结算记录失败"})
		return
	}
	if err = tx.Commit(contextBG()); err != nil {
		c.JSON(503, gin.H{"error": "结算结果暂不确定，请使用相同请求编号重试"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func adminAgentStats(c *gin.Context) {
	var agentCount, activeCount int
	var totalComm, totalPaid, totalPending int64
	pool.QueryRow(contextBG(), "SELECT COUNT(*), COUNT(*) FILTER (WHERE active) FROM agents").Scan(&agentCount, &activeCount)
	pool.QueryRow(contextBG(), "SELECT COALESCE(SUM(commission_cents),0) FROM agent_commissions").Scan(&totalComm)
	pool.QueryRow(contextBG(),
		"SELECT COALESCE(SUM(commission_cents-settled_cents),0) FROM agent_commissions WHERE status='pending'").Scan(&totalPending)
	pool.QueryRow(contextBG(), "SELECT COALESCE(SUM(amount_cents),0) FROM agent_payouts WHERE status='paid'").Scan(&totalPaid)
	c.JSON(200, gin.H{
		"agent_count": agentCount, "active_count": activeCount,
		"total_commission_cents": totalComm,
		"total_paid_cents":       totalPaid,
		"total_pending_cents":    totalPending,
	})
}

// ---------- 佣金自动记录（支付成功后调用） ----------

// ---------- 推广码验证（公开） ----------

func checkAgentCode(c *gin.Context) {
	code := strings.ToUpper(strings.TrimSpace(c.Param("code")))
	var name string
	err := pool.QueryRow(contextBG(),
		"SELECT name FROM agents WHERE upper(code)=upper($1) AND active=true", code).Scan(&name)
	if err != nil {
		c.JSON(404, gin.H{"error": "推广码无效"})
		return
	}
	c.JSON(200, gin.H{"ok": true, "name": name})
}

func agentLogout(c *gin.Context) {
	if p := agentOK(c); p != nil {
		if _, err := pool.Exec(c, "UPDATE agents SET session_version=session_version+1 WHERE id=$1", p.Code); err != nil {
			c.JSON(503, gin.H{"error": "退出暂未完成，请重试"})
			return
		}
	}
	c.SetSameSite(http.SameSiteLaxMode)
	for _, path := range []string{"/", basePath} {
		c.SetCookie(agentCookie, "", -1, path, "", true, true)
	}
	c.JSON(200, gin.H{"ok": true})
}

func maskAccount(s string) string {
	if len(s) <= 4 {
		return s
	}
	return "****" + s[len(s)-4:]
}
