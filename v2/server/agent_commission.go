package app

import (
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"time"
)

func validAgentCommission(pct int) bool { return pct >= 1 && pct <= 90 }

func adminListAgentInvitations(c *gin.Context) {
	rows, err := pool.Query(c, `SELECT id,email,name,commission_pct,created_at,expires_at FROM agent_invitations i WHERE accepted_at IS NULL AND expires_at>now() AND NOT EXISTS(SELECT 1 FROM agents a WHERE lower(a.email)=lower(i.email)) ORDER BY created_at DESC,id DESC LIMIT 100`)
	if err != nil {
		c.JSON(503, gin.H{"error": "邀请列表暂不可用"})
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id, email, name string
		var pct int
		var created, expires time.Time
		if err = rows.Scan(&id, &email, &name, &pct, &created, &expires); err != nil {
			c.JSON(503, gin.H{"error": "邀请列表暂不可用"})
			return
		}
		out = append(out, gin.H{"id": id, "email": email, "name": name, "commission_pct": pct, "created_at": created.Format(time.RFC3339), "expires_at": expires.Format(time.RFC3339)})
	}
	if rows.Err() != nil {
		c.JSON(503, gin.H{"error": "邀请列表暂不可用"})
		return
	}
	c.JSON(200, out)
}

func adminUpdateAgentInvitationCommission(c *gin.Context) {
	var in struct{ CommissionPct *int }
	if c.BindJSON(&in) != nil || in.CommissionPct == nil || !validAgentCommission(*in.CommissionPct) {
		c.JSON(400, gin.H{"error": "佣金比例需为 1–90 之间的整数"})
		return
	}
	tx, err := pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(503, gin.H{"error": "佣金调整未完成，请重试"})
		return
	}
	defer tx.Rollback(c.Request.Context())
	var old int
	var accepted, expired, registered bool
	err = tx.QueryRow(c, `SELECT commission_pct,accepted_at IS NOT NULL,expires_at<=now(),EXISTS(SELECT 1 FROM agents a WHERE lower(a.email)=lower(i.email)) FROM agent_invitations i WHERE id=$1 FOR UPDATE`, c.Param("id")).Scan(&old, &accepted, &expired, &registered)
	if err == pgx.ErrNoRows {
		c.JSON(404, gin.H{"error": "邀请不存在"})
		return
	}
	if err != nil {
		c.JSON(503, gin.H{"error": "佣金调整未完成，请重试"})
		return
	}
	if accepted || expired || registered {
		c.JSON(409, gin.H{"error": "邀请已开通或失效，请刷新后在代理列表调整"})
		return
	}
	if _, err = tx.Exec(c, "UPDATE agent_invitations SET commission_pct=$2 WHERE id=$1", c.Param("id"), *in.CommissionPct); err != nil {
		c.JSON(503, gin.H{"error": "佣金调整未完成，请重试"})
		return
	}
	if old != *in.CommissionPct {
		_, err = tx.Exec(c, `INSERT INTO audit_events(actor,action,object_type,object_id,detail) VALUES($1,'agent_invitation_commission_updated','agent_invitation',$2,jsonb_build_object('old_pct',$3::int,'new_pct',$4::int))`, adminName(c), c.Param("id"), old, *in.CommissionPct)
		if err != nil {
			c.JSON(503, gin.H{"error": "佣金调整未完成，请重试"})
			return
		}
	}
	if err = tx.Commit(c.Request.Context()); err != nil {
		c.JSON(503, gin.H{"error": "更新结果待确认，请刷新核对"})
		return
	}
	c.JSON(200, gin.H{"ok": true, "commission_pct": *in.CommissionPct})
}
