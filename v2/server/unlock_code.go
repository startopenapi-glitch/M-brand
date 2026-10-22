package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

var errUnlockCodeInvalid = errors.New("邀请码无效或不可用")

func claimDirectionsUnlockCode(ctx context.Context, tx pgx.Tx, vid, raw string) (string, error) {
	code := strings.ToUpper(strings.TrimSpace(raw))
	if code == "" {
		return "", errUnlockCodeInvalid
	}
	var max, used int
	var revoked bool
	var bound, itemKind string
	if err := tx.QueryRow(ctx, `SELECT max_uses,used,revoked,COALESCE(bound_visitor,''),COALESCE(item_kind,'')
		FROM invite_codes WHERE code=$1 FOR UPDATE`, code).
		Scan(&max, &used, &revoked, &bound, &itemKind); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errUnlockCodeInvalid
		}
		return "", fmt.Errorf("query claim code: %w", err)
	}
	if revoked || used >= max || (bound != "" && bound != vid) || itemKind != "directions_unlock" {
		return "", errUnlockCodeInvalid
	}
	tag, err := tx.Exec(ctx, `UPDATE invite_codes SET
		bound_visitor=COALESCE(NULLIF(bound_visitor,''),$2),
		used=used+1,used_by=COALESCE(NULLIF(used_by,''),$2),used_at=COALESCE(used_at,now())
		WHERE code=$1 AND revoked=false AND used<max_uses
		  AND (COALESCE(bound_visitor,'')='' OR bound_visitor=$2)`, code, vid)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() != 1 {
		return "", fmt.Errorf("%w: update rows=%d", errUnlockCodeInvalid, tag.RowsAffected())
	}
	return code, nil
}

func unlockDirectionsWithCode(c *gin.Context) {
	if !directionsPaywallEnabled() {
		c.JSON(409, gin.H{"error": "当前未启用方向解锁"})
		return
	}
	jid := c.Param("id")
	var req struct {
		Code string `json:"code"`
	}
	if c.BindJSON(&req) != nil {
		c.JSON(400, gin.H{"error": "请输入邀请码"})
		return
	}
	ip := c.ClientIP()
	if rateBlocked("unlock-code:"+ip, 10) {
		c.JSON(429, gin.H{"error": "尝试过于频繁，请 1 分钟后再试"})
		return
	}
	fail := func() {
		_ = rateOK("unlock-code:"+ip, 10)
		c.JSON(403, gin.H{"error": "邀请码无效、已使用或不属于本设备"})
	}
	vid := visitorID(c)
	if vid == "" {
		fail()
		return
	}
	gateCode := ""
	if gate := gateOK(c); gate != nil {
		gateCode = gate.Code
	}
	ctx := c.Request.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		c.JSON(500, gin.H{"error": "系统错误"})
		return
	}
	defer tx.Rollback(ctx)
	var status, stage, owner, jobCode string
	var unlocked bool
	if err = tx.QueryRow(ctx, `SELECT status,stage,COALESCE(visitor,''),COALESCE(NULLIF(quota_code,''),code,''),directions_unlocked
		FROM jobs WHERE id=$1 FOR UPDATE`, jid).Scan(&status, &stage, &owner, &jobCode, &unlocked); err != nil {
		c.JSON(404, gin.H{"error": "任务不存在"})
		return
	}
	if !jobOwnedQuery(c, tx, vid, gateCode, owner, jobCode) {
		c.JSON(404, gin.H{"error": "任务不存在"})
		return
	}
	if unlocked {
		c.JSON(200, gin.H{"ok": true, "reused": true, "directions_unlocked": true})
		return
	}
	if stage != "unlock" || status != "waiting_unlock" {
		c.JSON(409, gin.H{"error": "当前状态不可解锁"})
		return
	}
	// A pending/expired payment must be resolved first; this prevents paying and
	// redeeming a code for the same unlock.
	var pending string
	err = tx.QueryRow(ctx, `SELECT id FROM orders WHERE job_id=$1 AND pack='directions_unlock'
		AND status IN ('pending','expired') ORDER BY created_at LIMIT 1 FOR UPDATE`, jid).Scan(&pending)
	if err == nil {
		c.JSON(409, gin.H{"error": "已有一笔 9.9 元支付待核对，请先继续支付或等待关闭", "pending_order_id": pending, "pending_payment": true})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		c.JSON(500, gin.H{"error": "系统错误"})
		return
	}
	code, err := claimDirectionsUnlockCode(ctx, tx, vid, req.Code)
	if err != nil {
		if errors.Is(err, errUnlockCodeInvalid) {
			fail()
			return
		}
		c.JSON(500, gin.H{"error": "系统错误"})
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE jobs SET directions_unlocked=true,stage='directions',status='queued',
		quota_code=$2,unlock_method='code',unlocked_at=now(),error='',updated_at=now()
		WHERE id=$1 AND directions_unlocked=false`, jid, code); err != nil {
		c.JSON(500, gin.H{"error": "系统错误"})
		return
	}
	if err = tx.Commit(ctx); err != nil {
		c.JSON(500, gin.H{"error": "系统错误"})
		return
	}
	audit("visitor:"+vid, "code_redeem", "job", jid, `{"unlock_method":"code"}`)
	c.JSON(200, gin.H{"ok": true, "directions_unlocked": true, "stage": "directions", "status": "queued"})
}
