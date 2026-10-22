package app

// One-time 9.9 checkout for the three directions + Logo combination card.
// It is a pre-generation order, separate from item purchases and invite codes.

import (
	"fmt"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
)

func unlockDirections(c *gin.Context) {
	if !directionsPaywallEnabled() {
		c.JSON(409, gin.H{"error": "当前未启用方向解锁收费"})
		return
	}
	jid := c.Param("id")
	if !requireJobOwner(c, jid) {
		return
	}
	if paused, reason := salesPaused(); paused {
		c.JSON(503, gin.H{"error": reason})
		return
	}
	if !wxEnabled() {
		c.JSON(503, gin.H{"error": "支付暂不可用"})
		return
	}
	var stage, status string
	var unlocked bool
	if err := pool.QueryRow(c, "SELECT stage,status,directions_unlocked FROM jobs WHERE id=$1", jid).Scan(&stage, &status, &unlocked); err != nil {
		c.JSON(404, gin.H{"error": "任务不存在"})
		return
	}
	if unlocked {
		c.JSON(409, gin.H{"error": "方向已经解锁"})
		return
	}
	if stage != "unlock" {
		c.JSON(409, gin.H{"error": "当前状态不可解锁"})
		return
	}
	// Do not create a second checkout beside an unresolved unlock order.
	var pending string
	if err := pool.QueryRow(c, "SELECT id FROM orders WHERE job_id=$1 AND pack='directions_unlock' AND status IN ('pending','expired') ORDER BY created_at LIMIT 1", jid).Scan(&pending); err == nil {
		queryAndSettle(pending)
		checkoutView(c, pending, 200)
		return
	}
	if !allowCreate(c.ClientIP()) {
		c.JSON(429, gin.H{"error": "下单过于频繁，请稍后再试"})
		return
	}
	agCode, agID, pct, err := attribution(c)
	if err != nil {
		c.JSON(503, gin.H{"error": "暂时无法下单"})
		return
	}
	id := "0B" + time.Now().Format("060102150405") + randHex(8)
	snapshot := wxNativeRequest(id, directionUnlockPriceCents, "M-brand 9.9元解锁方向与Logo卡")
	mock := wxMock()
	codeURL := ""
	initialState := "preparing"
	if mock {
		codeURL = "wxp://mock/" + id
		initialState = "ready"
	}
	keys := []string{"directions_unlock"}
	sort.Strings(keys)
	if _, err = pool.Exec(c, `INSERT INTO orders(id,code,amount_cents,channel,status,code_url,mock,visitor,job_id,pack,agent_code,agent_id,agent_pct,checkout_started,checkout_lease_until,checkout_request,checkout_state)
		VALUES($1,'',$2,'native','pending',$3,$4,$5,$6,'directions_unlock',$7,$8,$9,true,now()+interval '30 seconds',$10,$11)`,
		id, directionUnlockPriceCents, codeURL, mock, visitorID(c), jid, nullS(agCode), nullS(agID), pct, string(snapshot), initialState); err != nil {
		c.JSON(503, gin.H{"error": "订单保存失败"})
		return
	}
	if !mock {
		codeURL, err = wxCreateNativeRequest(snapshot)
		if err != nil {
			if isDefinitiveWxError(err) {
				markCheckoutFailed(id, err)
				c.JSON(503, gin.H{"error": "支付入口创建失败，请重试", "retryable": true})
				return
			}
			markCheckoutUnknown(id, err)
			checkoutView(c, id, 202)
			return
		}
		if _, err = pool.Exec(c, "UPDATE orders SET code_url=$2,checkout_state='ready',checkout_error='',updated_at=now() WHERE id=$1", id, codeURL); err != nil {
			c.JSON(503, gin.H{"error": "支付入口已生成，请稍后核对订单"})
			return
		}
	}
	checkoutView(c, id, 201)
}

var _ = fmt.Sprintf
