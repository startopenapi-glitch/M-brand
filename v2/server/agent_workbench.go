package app

// One-level agent self-service withdrawals. Minimum ¥100, seven-day cooldown.
// A withdrawal reserves commission ledger rows only when an admin approves it;
// the agent's balance subtracts pending and approved requests so it cannot be
// requested twice.

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/startopenapi-glitch/M-brand/v2/server/internal/domain/agents"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

type agentPayoutInput struct {
	AmountCents int64  `json:"AmountCents"`
	RequestKey  string `json:"RequestKey"`
}
type agentAccountInput struct {
	Method  string `json:"Method"`
	Account string `json:"Account"`
	Name    string `json:"Name"`
}
type agentPayoutReview struct {
	Action    string `json:"Action"`
	Note      string `json:"Note"`
	Reference string `json:"Reference"`
}

func agentPayoutAccount(c *gin.Context) {
	p := agentOK(c)
	if p == nil {
		c.JSON(401, gin.H{"error": "请先登录"})
		return
	}
	var in agentAccountInput
	if c.BindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "参数无效"})
		return
	}
	in.Method, in.Account, in.Name = strings.TrimSpace(in.Method), strings.TrimSpace(in.Account), strings.TrimSpace(in.Name)
	if (in.Method != "alipay" && in.Method != "bank") || in.Account == "" || len([]rune(in.Account)) > 100 || len([]rune(in.Name)) > 50 {
		c.JSON(400, gin.H{"error": "请填写有效的收款方式和账户"})
		return
	}
	_, err := pool.Exec(c, `UPDATE agents SET payout_method=$2,payout_account=$3,payout_name=$4,payout_updated_at=now() WHERE id=$1`, p.Code, in.Method, in.Account, in.Name)
	if err != nil {
		c.JSON(500, gin.H{"error": "系统错误"})
		return
	}
	audit("agent:"+p.Code, "payout_account_update", "agent", p.Code, `{"method":"`+in.Method+`"}`)
	c.JSON(200, gin.H{"ok": true})
}

func agentWithdrawable(c *gin.Context, agentID string) (earned, paid, pending int64, err error) {
	err = pool.QueryRow(c, `SELECT total_earned_cents,total_paid_cents FROM agents WHERE id=$1`, agentID).Scan(&earned, &paid)
	if err != nil {
		return
	}
	_ = pool.QueryRow(c, `SELECT COALESCE(SUM(amount_cents),0) FROM agent_payouts WHERE agent_id=$1 AND status IN ('pending_review','approved')`, agentID).Scan(&pending)
	return
}

func agentPayoutCreate(c *gin.Context) {
	p := agentOK(c)
	if p == nil {
		c.JSON(401, gin.H{"error": "请先登录"})
		return
	}
	var in agentPayoutInput
	if c.BindJSON(&in) != nil || len(in.RequestKey) < 16 || len(in.RequestKey) > 128 {
		c.JSON(400, gin.H{"error": "请提供有效请求编号"})
		return
	}
	ctx := c.Request.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		c.JSON(503, gin.H{"error": "账务服务暂不可用"})
		return
	}
	defer tx.Rollback(ctx)
	var method, account, name string
	if err = tx.QueryRow(ctx, `SELECT payout_method,payout_account,payout_name FROM agents WHERE id=$1 FOR UPDATE`, p.Code).Scan(&method, &account, &name); err != nil {
		c.JSON(404, gin.H{"error": "代理不存在"})
		return
	}
	if method == "" || account == "" || name == "" {
		c.JSON(409, gin.H{"error": "请先填写收款账户"})
		return
	}
	var oldID int
	var oldAmount int64
	err = tx.QueryRow(ctx, `SELECT id,amount_cents FROM agent_payouts WHERE agent_id=$1 AND request_key=$2`, p.Code, in.RequestKey).Scan(&oldID, &oldAmount)
	if err == nil {
		if oldAmount != in.AmountCents {
			c.JSON(409, gin.H{"error": "同一请求编号金额不一致"})
			return
		}
		c.JSON(200, gin.H{"ok": true, "replayed": true, "id": oldID})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		c.JSON(503, gin.H{"error": "账务查询失败"})
		return
	}
	var pending int64
	_ = tx.QueryRow(ctx, `SELECT COALESCE(SUM(amount_cents),0) FROM agent_payouts WHERE agent_id=$1 AND status IN ('pending_review','approved')`, p.Code).Scan(&pending)
	var earned, paid int64
	if err = tx.QueryRow(ctx, `SELECT total_earned_cents,total_paid_cents FROM agents WHERE id=$1`, p.Code).Scan(&earned, &paid); err != nil {
		c.JSON(503, gin.H{"error": "账务查询失败"})
		return
	}
	var last time.Time
	err = tx.QueryRow(ctx, `SELECT requested_at FROM agent_payouts WHERE agent_id=$1 AND status<>'rejected' ORDER BY requested_at DESC LIMIT 1`, p.Code).Scan(&last)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		c.JSON(503, gin.H{"error": "账务查询失败"})
		return
	}
	if err = agents.ValidatePayout(in.AmountCents, earned-paid-pending, last, time.Now()); err != nil {
		status := 409
		if errors.Is(err, agents.ErrBelowMinimum) {
			status = 400
		}
		if errors.Is(err, agents.ErrCooldown) {
			status = 429
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	acct, _ := json.Marshal(map[string]string{"method": method, "account": account, "name": name})
	var id int
	if err = tx.QueryRow(ctx, `INSERT INTO agent_payouts(agent_id,amount_cents,method,note,request_key,status,account_snapshot) VALUES($1,$2,$3,'代理申请提现',$4,'pending_review',$5) RETURNING id`, p.Code, in.AmountCents, method, in.RequestKey, string(acct)).Scan(&id); err != nil {
		c.JSON(503, gin.H{"error": "提现申请保存失败"})
		return
	}
	if err = tx.Commit(ctx); err != nil {
		c.JSON(503, gin.H{"error": "提现结果暂不确定，请使用相同请求编号重试"})
		return
	}
	audit("agent:"+p.Code, "payout_request", "agent", p.Code, `{"amount_cents":`+strconv.FormatInt(in.AmountCents, 10)+`}`)
	c.JSON(201, gin.H{"ok": true, "id": id, "status": "pending_review"})
}

func agentPayoutList(c *gin.Context) {
	p := agentOK(c)
	if p == nil {
		c.JSON(401, gin.H{"error": "请先登录"})
		return
	}
	rows, err := pool.Query(c, `SELECT id,amount_cents,status,COALESCE(note,''),COALESCE(reference,''),COALESCE(to_char(requested_at,'YYYY-MM-DD HH24:MI'),''),COALESCE(to_char(reviewed_at,'YYYY-MM-DD HH24:MI'),'') FROM agent_payouts WHERE agent_id=$1 ORDER BY id DESC LIMIT 100`, p.Code)
	if err != nil {
		c.JSON(503, gin.H{"error": "提现记录暂不可用"})
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id int
		var amt int64
		var status, note, ref, created, reviewed string
		if rows.Scan(&id, &amt, &status, &note, &ref, &created, &reviewed) != nil {
			continue
		}
		out = append(out, gin.H{"id": id, "amount_cents": amt, "status": status, "note": note, "reference": ref, "requested_at": created, "reviewed_at": reviewed})
	}
	c.JSON(200, out)
}

func agentStatementCSV(c *gin.Context) {
	p := agentOK(c)
	if p == nil {
		c.JSON(401, gin.H{"error": "请先登录"})
		return
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="M-brand-agent-statement.csv"`)
	w := csv.NewWriter(c.Writer)
	defer w.Flush()
	_ = w.Write([]string{"类型", "编号", "日期", "订单金额(分)", "佣金(分)", "提现(分)", "状态"})
	rows, err := pool.Query(c, `SELECT order_id,COALESCE(to_char(created_at,'YYYY-MM-DD'),''),order_amount_cents,commission_cents,status FROM agent_commissions WHERE agent_id=$1 ORDER BY id`, p.Code)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var oid, date, status string
			var amt, comm int64
			if rows.Scan(&oid, &date, &amt, &comm, &status) == nil {
				_ = w.Write([]string{"佣金", oid, date, strconv.FormatInt(amt, 10), strconv.FormatInt(comm, 10), "", status})
			}
		}
	}
	rows2, err := pool.Query(c, `SELECT id,COALESCE(to_char(requested_at,'YYYY-MM-DD'),''),amount_cents,status FROM agent_payouts WHERE agent_id=$1 ORDER BY id`, p.Code)
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var id int
			var date, status string
			var amt int64
			if rows2.Scan(&id, &date, &amt, &status) == nil {
				_ = w.Write([]string{"提现", strconv.Itoa(id), date, "", "", strconv.FormatInt(amt, 10), status})
			}
		}
	}
}

func adminAgentPayoutList(c *gin.Context) {
	rows, err := pool.Query(c, `SELECT p.id,p.agent_id,a.name,p.amount_cents,p.status,COALESCE(p.review_note,''),COALESCE(p.reference,''),COALESCE(to_char(p.requested_at,'YYYY-MM-DD HH24:MI'),'')
		FROM agent_payouts p JOIN agents a ON a.id=p.agent_id WHERE p.status IN ('pending_review','approved') OR p.requested_at>now()-interval '90 days' ORDER BY CASE p.status WHEN 'pending_review' THEN 0 WHEN 'approved' THEN 1 ELSE 2 END,p.requested_at DESC LIMIT 300`)
	if err != nil {
		c.JSON(503, gin.H{"error": "提现记录暂不可用"})
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id int
		var aid, name, status, note, ref, created string
		var amt int64
		if rows.Scan(&id, &aid, &name, &amt, &status, &note, &ref, &created) != nil {
			continue
		}
		out = append(out, gin.H{"id": id, "agent_id": aid, "agent_name": name, "amount_cents": amt, "status": status, "review_note": note, "reference": ref, "requested_at": created})
	}
	c.JSON(200, out)
}

func adminAgentPayoutReview(c *gin.Context) {
	var in agentPayoutReview
	if c.BindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "参数无效"})
		return
	}
	if in.Action != "approve" && in.Action != "paid" && in.Action != "reject" {
		c.JSON(400, gin.H{"error": "操作无效"})
		return
	}
	tx, err := pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(503, gin.H{"error": "账务服务暂不可用"})
		return
	}
	defer tx.Rollback(c.Request.Context())
	var agentID, status string
	var amount int64
	if err = tx.QueryRow(c, `SELECT agent_id,status,amount_cents FROM agent_payouts WHERE id=$1 FOR UPDATE`, c.Param("id")).Scan(&agentID, &status, &amount); err != nil {
		c.JSON(404, gin.H{"error": "提现不存在"})
		return
	}
	switch in.Action {
	case "reject":
		if status != "pending_review" {
			c.JSON(409, gin.H{"error": "当前状态不可拒绝"})
			return
		}
		_, err = tx.Exec(c, `UPDATE agent_payouts SET status='rejected',review_note=$2,reviewed_at=now() WHERE id=$1`, c.Param("id"), strings.TrimSpace(in.Note))
	case "approve":
		if status != "pending_review" {
			c.JSON(409, gin.H{"error": "当前状态不可审核"})
			return
		}
		var earned, paid int64
		if err = tx.QueryRow(c, `SELECT total_earned_cents,total_paid_cents FROM agents WHERE id=$1 FOR UPDATE`, agentID).Scan(&earned, &paid); err != nil {
			c.JSON(503, gin.H{"error": "账务查询失败"})
			return
		}
		var pendingOther int64
		_ = tx.QueryRow(c, `SELECT COALESCE(SUM(amount_cents),0) FROM agent_payouts WHERE agent_id=$1 AND status='approved' AND id<>$2`, agentID, c.Param("id")).Scan(&pendingOther)
		if amount > earned-paid-pendingOther {
			c.JSON(409, gin.H{"error": "可结算余额不足"})
			return
		}
		rows, e := tx.Query(c, `SELECT id,commission_cents-settled_cents FROM agent_commissions WHERE agent_id=$1 AND status='pending' AND commission_cents>settled_cents ORDER BY id FOR UPDATE`, agentID)
		if e != nil {
			c.JSON(503, gin.H{"error": "账务查询失败"})
			return
		}
		type alloc struct {
			id   int
			take int64
		}
		var allocs []alloc
		remain := amount
		for rows.Next() {
			var cid int
			var avail int64
			if rows.Scan(&cid, &avail) != nil {
				break
			}
			take := avail
			if take > remain {
				take = remain
			}
			if take > 0 {
				allocs = append(allocs, alloc{cid, take})
				remain -= take
			}
		}
		rowErr := rows.Err()
		rows.Close()
		if rowErr != nil {
			c.JSON(503, gin.H{"error": "账务查询失败"})
			return
		}
		for _, a := range allocs {
			if _, e = tx.Exec(c, `INSERT INTO agent_payout_allocations(payout_id,commission_id,amount_cents) VALUES($1,$2,$3)`, c.Param("id"), a.id, a.take); e != nil {
				c.JSON(503, gin.H{"error": "结算记录失败"})
				return
			}
			if _, e = tx.Exec(c, `UPDATE agent_commissions SET settled_cents=settled_cents+$2,status=CASE WHEN settled_cents+$2=commission_cents THEN 'paid' ELSE 'pending' END,paid_at=CASE WHEN settled_cents+$2=commission_cents THEN now() ELSE NULL END WHERE id=$1`, a.id, a.take); e != nil {
				c.JSON(503, gin.H{"error": "结算记录失败"})
				return
			}
		}
		if remain != 0 {
			c.JSON(409, gin.H{"error": "历史账务明细与余额不一致"})
			return
		}
		_, err = tx.Exec(c, `UPDATE agent_payouts SET status='approved',review_note=$2,reviewed_at=now() WHERE id=$1`, c.Param("id"), strings.TrimSpace(in.Note))
	case "paid":
		if status != "approved" {
			c.JSON(409, gin.H{"error": "提现尚未审核通过"})
			return
		}
		if strings.TrimSpace(in.Reference) == "" {
			c.JSON(400, gin.H{"error": "请填写打款流水号"})
			return
		}
		_, err = tx.Exec(c, `UPDATE agent_payouts SET status='paid',reference=$2,review_note=$3,reviewed_at=now() WHERE id=$1`, c.Param("id"), strings.TrimSpace(in.Reference), strings.TrimSpace(in.Note))
		if err == nil {
			_, err = tx.Exec(c, `UPDATE agents SET total_paid_cents=total_paid_cents+$2 WHERE id=$1`, agentID, amount)
		}
	}
	if err != nil {
		c.JSON(503, gin.H{"error": "提现处理失败"})
		return
	}
	if err = tx.Commit(c.Request.Context()); err != nil {
		c.JSON(503, gin.H{"error": "提现结果暂不确定"})
		return
	}
	c.JSON(200, gin.H{"ok": true, "status": in.Action})
}
