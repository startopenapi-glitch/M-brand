package app

// site_retry.go — 用户侧失败安置：人话错误文案 + 自助重试
//
// 第一性原理：失败是产品的一部分。任何失败都要回答三问——
//   发生了什么（人话）/ 影响（扣没扣额度、耗没耗试用次数）/ 下一步（重试、等待、找人）

import (
	"context"
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// userMessage：把内部错误翻译成人话（原始细节仍留在日志与管理端）
func userMessage(errText string, trial, refunded bool) string {
	if errText == "" {
		return ""
	}
	var what string
	switch {
	case strings.Contains(errText,errGenerationUnknown.Error()):
  return "生成结果正在核对，已完成内容和已购权益已保留。请联系站点管理员 协助处理，无需重复购买。"
 case strings.Contains(errText, "API_KEY_QUOTA_EXHAUSTED"):
		what = "生成服务当前繁忙（服务商额度告警），我们已收到通知，正在处理。"
	case strings.Contains(errText, "生图接口 HTTP 4"):
		what = "生成服务暂时拒绝了本次请求，我们已记录该问题。"
	case strings.Contains(errText, "生图接口 HTTP 5"):
		what = "生成服务暂时不可用，我们已收到通知。"
	case strings.Contains(errText, "timeout"), strings.Contains(errText, "超时"):
		what = "本次生成超时中断，已保留已完成的部分。"
	default:
		what = "本次生成没有成功完成。"
	}
	var impact string
	switch {
	case trial:
		impact = "已保留品牌信息，可以免费重试。"
	case refunded:
		impact = "本次不消耗额度（已自动返还），可以直接重试。"
	default:
		impact = "如重试仍失败，请联系站点管理员，我们会为你处理。"
	}
	return what + impact
}

var (
	errRetryNotFound  = errors.New("任务不存在")
	errRetryNotFailed = errors.New("当前任务无需重试")
	errRetryNoTicket  = errors.New("当前没有可用额度，购买或兑换后可继续")
)

// retryFailedJob 将归属/状态复核、重新占票与排队放进同一事务。
// trial 是获客来源；是否已付费由阶段和 quota_code 决定，不能以 trial 免占票。
func retryFailedJob(ctx context.Context, jid, vid, gateCode string, asAdmin bool) (bool, error) {
	_ = gateCode // cookie 中旧码文本不代表当前票归属
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var status, stage, owner, jobCode, quota string
	var trial, refunded bool
	if err := tx.QueryRow(ctx,
		`SELECT status, stage, COALESCE(visitor,''), COALESCE(code,''), COALESCE(quota_code,''), trial, refunded
		 FROM jobs WHERE id=$1 FOR UPDATE`, jid).
		Scan(&status, &stage, &owner, &jobCode, &quota, &trial, &refunded); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, errRetryNotFound
		}
		return false, err
	}
	if !asAdmin {
		owned := vid != "" && owner == vid
		effectiveCode := jobCode
		if quota != "" {
			effectiveCode = quota
		}
		if !owned && vid != "" && effectiveCode != "" {
			if err := tx.QueryRow(ctx,
				"SELECT EXISTS(SELECT 1 FROM invite_codes WHERE code=$1 AND bound_visitor=$2 AND revoked=false)", effectiveCode, vid).
				Scan(&owned); err != nil {
				return false, err
			}
		}
		if !owned {
			return false, errRetryNotFound
		}
	}
	if status != "error" {
		return false, errRetryNotFailed
	}
	var pending bool
 if e:=tx.QueryRow(ctx,"SELECT EXISTS(SELECT 1 FROM generation_attempts WHERE job_id=$1 AND state IN ('submitted','unknown'))",jid).Scan(&pending);e!=nil{return false,e};if pending{return false,errGenerationUnknown}
 next := "queued"
	switch stage {
	case "directions", "boards", "logo", "logo_revision":
	case "choose":
		next = "ready_to_choose"
	case "done":
		next = "done"
	default:
		return false, errRetryNotFailed
	}
	paidStage := quota != ""
	if paidStage && refunded {
		ticketOwner := vid
		if asAdmin {
			ticketOwner = owner
		}
		// 原票仅在仍归属该会话时优先；锁票后再次用条件更新防跨任务双花。
		var claimed string
		err := tx.QueryRow(ctx, `WITH candidate AS (
			SELECT code FROM invite_codes
			WHERE bound_visitor=$1 AND $1<>'' AND revoked=false AND used<max_uses
			ORDER BY (code=$2) DESC, created_at, code
			LIMIT 1 FOR UPDATE
		)
		UPDATE invite_codes ic SET used=ic.used+1, used_by=$1, used_at=now()
		FROM candidate c WHERE ic.code=c.code AND ic.bound_visitor=$1
		  AND ic.revoked=false AND ic.used<ic.max_uses
		RETURNING ic.code`, ticketOwner, quota).Scan(&claimed)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, errRetryNoTicket
		}
		if err != nil {
			return false, err
		}
		quota, refunded = claimed, false
	}
	tag, err := tx.Exec(ctx,
		`UPDATE jobs SET status=$2, error='', quota_code=$3, refunded=$4, updated_at=now()
		 WHERE id=$1 AND status='error'`, jid, next, quota, refunded)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() != 1 {
		return false, errRetryNotFailed
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return trial && !paidStage, nil
}

// retryJob：用户自助重试，免费方向重试不额外计次，已返还的付费阶段重新占票。
func retryJob(c *gin.Context) {
	jid, vid := c.Param("id"), visitorID(c)
	if !rateOK("retry:"+vid+":"+jid, 3) {
		c.JSON(429, gin.H{"error": "重试过于频繁，请稍后再试"})
		return
	}
	trial, err := retryFailedJob(c.Request.Context(), jid, vid, "", adminOK(c) != nil)
	if err != nil {
		switch {
		case errors.Is(err, errRetryNotFound):
			c.JSON(404, gin.H{"error": errRetryNotFound.Error()})
		case errors.Is(err,errGenerationUnknown):
   c.JSON(409,gin.H{"error":errGenerationUnknown.Error()})
  case errors.Is(err, errRetryNotFailed):
			c.JSON(409, gin.H{"error": errRetryNotFailed.Error()})
		case errors.Is(err, errRetryNoTicket):
			c.JSON(402, gin.H{"error": errRetryNoTicket.Error(), "need_ticket": true, "plan": planPayload()})
		default:
			c.JSON(500, gin.H{"error": "系统错误"})
		}
		return
	}
	audit("visitor:"+vid, "job_retry", "job", jid, "")
	c.JSON(200, gin.H{"ok": true, "trial": trial})
}
