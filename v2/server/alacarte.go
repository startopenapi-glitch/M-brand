package app

import (
	"github.com/startopenapi-glitch/M-brand/v2/server/internal/domain/catalog"
	"context"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"sort"
	"strings"
	"time"
)

type ItemPrice struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Category   string `json:"category"`
	PriceCents int64  `json:"price_cents"`
}

const bundlePriceCents int64 = catalog.BundlePriceCents

func itemPrices(ctx context.Context, q commerceQuery) ([]ItemPrice, error) {
	rows, err := q.Query(ctx, "SELECT kind,name,category,price_cents FROM item_pricing ORDER BY sort_order")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ItemPrice{}
	for rows.Next() {
		var it ItemPrice
		if err = rows.Scan(&it.Kind, &it.Name, &it.Category, &it.PriceCents); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}
func listItems(c *gin.Context) {
	items, err := itemPrices(c, pool)
	if err != nil {
		c.JSON(503, gin.H{"error": "价格暂不可用"})
		return
	}
	var total int64
	for _, it := range items {
		total += it.PriceCents
	}
	c.JSON(200, gin.H{"items": items, "total_if_all": total, "bundle_price": bundlePriceCents, "savings": total - bundlePriceCents})
}

func attribution(c *gin.Context) (string, string, *int, error) {
	code, _ := c.Cookie(refCookie)
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" || len(code) > 64 {
		return "", "", nil, nil
	}
	var id string
	var pct int
	err := pool.QueryRow(c, "SELECT id,commission_pct FROM agents WHERE upper(code)=$1 AND active=true AND (SELECT COUNT(*) FROM agents WHERE upper(code)=$1)=1", code).Scan(&id, &pct)
	if err == pgx.ErrNoRows {
		return "", "", nil, nil
	}
	if err != nil {
		return "", "", nil, err
	}
	return code, id, &pct, nil
}

// All creation endpoints share this one item-checkout contract. Old pack payloads
// cannot create a second type of purchase; existing paid orders remain fulfillable.
func alacartePayCreate(c *gin.Context) {
	var req struct {
		JobID string   `json:"job_id"`
		Items []string `json:"items"`
		All   bool     `json:"all"`
	}
	if !readJSONBodyLimit(c, &req, 64<<10) {
		return
	}
	if req.JobID == "" || (!req.All && len(req.Items) == 0) || len(req.Items) > 50 {
		c.JSON(400, gin.H{"error": "请在品牌提案中勾选要购买的项目"})
		return
	}
	if !requireJobOwner(c, req.JobID) {
		return
	}
	if paused, _ := salesPaused(); paused {
		c.JSON(503, gin.H{"error": "生成服务维护中，暂不收款"})
		return
	}
	if !wxEnabled() {
		c.JSON(503, gin.H{"error": "支付暂不可用"})
		return
	}
	agCode, agID, pct, err := attribution(c)
	if err != nil {
		c.JSON(503, gin.H{"error": "暂时无法下单"})
		return
	}
	tx, err := pool.Begin(c)
	if err != nil {
		c.JSON(503, gin.H{"error": "暂时无法下单"})
		return
	}
	defer tx.Rollback(contextBG())
	var owner, code, status, stage string
	if err = tx.QueryRow(c, `SELECT COALESCE(visitor,''),COALESCE(NULLIF(quota_code,''),code,''),status,stage FROM jobs WHERE id=$1 FOR UPDATE`, req.JobID).Scan(&owner, &code, &status, &stage); err != nil || !jobOwnedQuery(c, tx, visitorID(c), "", owner, code) {
		c.JSON(404, gin.H{"error": "任务不存在"})
		return
	}
	if stage == "unlock" {
		c.JSON(409, gin.H{"error": "请先完成 9.9 元方向解锁"})
		return
	}
	if stage == "directions" || stage == "choose" {
		c.JSON(409, gin.H{"error": "请先选定方向"})
		return
	}
	if stage == "logo" || stage == "logo_revision" {
		c.JSON(409, gin.H{"error": "请先确认正式 Logo，再购买应用项"})
		return
	}
	rights, err := jobEntitlements(c, tx, req.JobID)
	if err != nil {
		c.JSON(503, gin.H{"error": "暂时无法读取权益"})
		return
	}
	prices, err := itemPrices(c, tx)
	if err != nil {
		c.JSON(503, gin.H{"error": "价格暂不可用"})
		return
	}
	wanted := map[string]bool{}
	for _, k := range req.Items {
		wanted[k] = true
	}
	valid := map[string]bool{}
	var selected []ItemPrice
	var total int64
	for _, it := range prices {
		valid[it.Kind] = true
		if (req.All || wanted[it.Kind]) && !rights[it.Kind] {
			selected = append(selected, it)
			total += it.PriceCents
		}
	}
	for k := range wanted {
		if !valid[k] {
			c.JSON(400, gin.H{"error": "选择中含未知项目，请刷新后重试"})
			return
		}
	}
	if len(selected) == 0 {
		c.JSON(200, gin.H{"already_purchased": true})
		return
	}
	if stage == "boards" {
		ready, e := systemCardReady(c, tx, req.JobID)
		if e != nil {
			c.JSON(503, gin.H{"error": "暂时无法核对 Logo 组合卡状态"})
			return
		}
		if !ready {
			c.JSON(409, gin.H{"error": "Logo 组合卡尚未完成，完成后才能购买付费交付项", "system_card_pending": true})
			return
		}
	}
	if req.All {
		var spent int64
		if err = tx.QueryRow(c, `SELECT COALESCE(SUM(amount_cents),0) FROM orders WHERE job_id=$1 AND status='paid'`, req.JobID).Scan(&spent); err != nil {
			c.JSON(503, gin.H{"error": "暂时无法核对已付金额"})
			return
		}
		remaining := bundlePriceCents - spent
		if remaining < 1 {
			c.JSON(409, gin.H{"error": "已付金额与权益不一致，请联系支持核对，未创建新付款"})
			return
		}
		if total > remaining {
			total = remaining
		}
	}
	// One unresolved order per job: changed selections cannot overlap an unpaid order.
	var pending string
	err = tx.QueryRow(c, `SELECT id FROM orders WHERE job_id=$1 AND status IN ('pending','expired') ORDER BY created_at LIMIT 1`, req.JobID).Scan(&pending)
	if err == nil {
		tx.Rollback(contextBG())
		queryAndSettle(pending)
		checkoutView(c, pending, 200)
		return
	}
	if err != pgx.ErrNoRows {
		c.JSON(503, gin.H{"error": "暂时无法核对订单"})
		return
	}
	if !allowCreate(c.ClientIP()) {
		c.JSON(429, gin.H{"error": "下单过于频繁，请稍后再试"})
		return
	}
	keys := []string{}
	for _, it := range selected {
		keys = append(keys, it.Kind)
	}
	sort.Strings(keys)
	id := "0B" + time.Now().Format("060102150405") + randHex(8)
	snapshot := wxNativeRequest(id, total, fmt.Sprintf("M-brand %d项", len(selected)))
	mock := wxMock()
	cu := ""
	if mock {
		cu = "wxp://mock/" + id
	}
	initialState := "preparing"
	if mock {
		initialState = "ready"
	}
	if _, err = tx.Exec(c, `INSERT INTO orders(id,code,amount_cents,channel,status,code_url,mock,visitor,job_id,pack,agent_code,agent_id,agent_pct,checkout_started,checkout_lease_until,checkout_request,checkout_state)
 VALUES($1,'',$2,'native','pending',$3,$4,$5,$6,$7,$8,$9,$10,true,now()+interval '30 seconds',$11,$12)`, id, total, cu, mock, visitorID(c), req.JobID, "items:"+strings.Join(keys, ","), nullS(agCode), nullS(agID), pct, string(snapshot), initialState); err != nil {
		c.JSON(503, gin.H{"error": "订单保存失败"})
		return
	}
	// Allocate discount deterministically so item snapshot sums equal the channel amount.
	remaining := total
	for i, it := range selected {
		amount := it.PriceCents
		if i == len(selected)-1 || amount > remaining {
			amount = remaining
		}
		remaining -= amount
		if _, err = tx.Exec(c, `INSERT INTO order_items(order_id,item_kind,item_name,price_cents) VALUES($1,$2,$3,$4)`, id, it.Kind, it.Name, amount); err != nil {
			c.JSON(503, gin.H{"error": "订单明细保存失败"})
			return
		}
	}
	if err = tx.Commit(contextBG()); err != nil {
		c.JSON(503, gin.H{"error": "订单保存结果待核对，请刷新项目后继续"})
		return
	}
	if !mock {
		cu, err = wxCreateNativeRequest(snapshot)
		if err != nil {
			slog.Error("wx native create failed", "order", id, "definitive", isDefinitiveWxError(err), "error", safeWxError(err))
			if isDefinitiveWxError(err) {
				markCheckoutFailed(id, err)
				c.JSON(503, gin.H{"error": "支付入口创建失败，请重新选择支付项目", "retryable": true})
				return
			}
			markCheckoutUnknown(id, err)
			checkoutView(c, id, 202)
			return
		}
		if _, err = pool.Exec(contextBG(), "UPDATE orders SET code_url=$2,checkout_state='ready',checkout_error='',updated_at=now() WHERE id=$1", id, cu); err != nil {
			slog.Error("wx checkout stored but QR update failed", "order", id)
			checkoutView(c, id, 202)
			return
		}
	}
	checkoutView(c, id, 201)
}

// Expiry only controls presentation and whether explicit recovery should query
// and close the original order. It is never evidence of a financial terminal state.
func nativeCheckoutExpired(snapshot, id string, cents int64) bool {
	if !validNativeSnapshot([]byte(snapshot), id, cents) {
		return false
	}
	var p struct {
		Expiry string `json:"time_expire"`
	}
	if json.Unmarshal([]byte(snapshot), &p) != nil {
		return false
	}
	expiry, err := time.Parse(time.RFC3339, p.Expiry)
	return err == nil && !expiry.After(time.Now())
}

func checkoutView(c *gin.Context, id string, httpStatus int) {
	var cents int64
	var channel, cu, h5, status, job, pack, snapshot, checkoutState, checkoutError string
	var count int
	err := pool.QueryRow(c, `SELECT amount_cents,channel,COALESCE(code_url,''),COALESCE(h5_url,''),status,COALESCE(job_id,''),COALESCE(pack,''),(SELECT COUNT(*) FROM order_items WHERE order_id=orders.id),checkout_request,checkout_state,checkout_error FROM orders WHERE id=$1`, id).Scan(&cents, &channel, &cu, &h5, &status, &job, &pack, &count, &snapshot, &checkoutState, &checkoutError)
	if err != nil {
		c.JSON(503, gin.H{"error": "订单查询失败"})
		return
	}
	expired := channel == "native" && nativeCheckoutExpired(snapshot, id, cents)
	if expired || (status != "pending" && status != "expired") {
		cu = ""
		h5 = ""
	}
	payload := orderPayload(id, channel, cu, h5)
	payload["amount_cents"] = cents
	payload["job_id"] = job
	payload["item_count"] = count
	payload["status"] = status
	if pack == "directions_unlock" {
		payload["item_count"] = 1
		payload["unlock"] = true
	}
	payload["checkout_state"] = checkoutState
	if checkoutError != "" {
		payload["checkout_error"] = checkoutError
	}
	if status == "failed" {
		payload["message"] = "支付入口创建失败，请重新选择支付项目。"
		payload["retryable"] = true
	} else if cu == "" && status == "pending" {
		switch checkoutState {
		case "failed":
			payload["message"] = "支付入口创建失败，请重新选择支付项目。"
			payload["retryable"] = true
		case "unknown":
			payload["message"] = "支付入口结果待核对，请勿重复付款。可稍后核对原订单。"
		default:
			payload["message"] = "支付入口生成中，可稍后核对原订单。"
		}
	}
	if expired && (status == "pending" || status == "expired") {
		payload["message"] = "原二维码已到期，请核对并恢复支付入口。确认原订单关闭后，才能重新选择付款。"
	}
	c.JSON(httpStatus, payload)
}

func jobDetailAlacarte(c *gin.Context) {
	jid := c.Param("id")
	if !requireJobOwner(c, jid) {
		return
	}
	var status, stage string
	if err := pool.QueryRow(c, "SELECT status,stage FROM jobs WHERE id=$1", jid).Scan(&status, &stage); err != nil {
		readFailure(c, err, "任务不存在")
		return
	}
	rights, err := jobEntitlements(c, pool, jid)
	if err != nil {
		c.JSON(503, gin.H{"error": "权益暂不可用"})
		return
	}
	prices, err := itemPrices(c, pool)
	if err != nil {
		c.JSON(503, gin.H{"error": "价格暂不可用"})
		return
	}
	files := map[string]string{}
	rows, err := pool.Query(c, "SELECT kind,idx,COALESCE(file,'') FROM job_images WHERE job_id=$1 ORDER BY id", jid)
	if err != nil {
		c.JSON(503, gin.H{"error": "交付物暂不可用"})
		return
	}
	for rows.Next() {
		var k, f string
		var idx int
		if err = rows.Scan(&k, &idx, &f); err != nil {
			break
		}
		if k == "board" {
			k = legacyBoardKind(idx)
		}
		if k != "" {
			files[k] = f
		}
	}
	re := rows.Err()
	rows.Close()
	if err != nil || re != nil {
		c.JSON(503, gin.H{"error": "交付物暂不可用"})
		return
	}
	items := []gin.H{}
	var total int64
	for _, it := range prices {
		total += it.PriceCents
		file := ""
		if rights[it.Kind] {
			file = files[it.Kind]
		}
		items = append(items, gin.H{"kind": it.Kind, "name": it.Name, "category": it.Category, "price_cents": it.PriceCents, "purchased": rights[it.Kind], "unlocked": rights[it.Kind], "generated": file != "", "file": file})
	}
	sys := files["system_card"]
	if sys == "" {
		sys = files["system"]
	}
	items = append(items, gin.H{"kind": "system_card", "name": "Logo 组合卡", "category": "free", "price_cents": 0, "purchased": true, "unlocked": true, "generated": sys != "", "file": sys})
	var spent int64
	if err = pool.QueryRow(c, "SELECT COALESCE(SUM(amount_cents),0) FROM orders WHERE job_id=$1 AND status='paid'", jid).Scan(&spent); err != nil {
		c.JSON(503, gin.H{"error": "订单暂不可用"})
		return
	}
	remaining := bundlePriceCents - spent
	if remaining < 0 {
		remaining = 0
	}
	var pending string
	err = pool.QueryRow(c, "SELECT id FROM orders WHERE job_id=$1 AND status IN ('pending','expired') ORDER BY created_at LIMIT 1", jid).Scan(&pending)
	if err != nil && err != pgx.ErrNoRows {
		c.JSON(503, gin.H{"error": "订单暂不可用"})
		return
	}
	c.JSON(200, gin.H{"id": jid, "status": status, "stage": stage, "items": items, "total_if_all": total, "bundle_price": bundlePriceCents, "remaining_bundle_price": remaining, "savings": total - bundlePriceCents, "pending_order_id": pending})
}

func checkoutOwner(c *gin.Context) bool {
	var job string
	if e := pool.QueryRow(c, "SELECT COALESCE(job_id,'') FROM orders WHERE id=$1", c.Param("id")).Scan(&job); e != nil {
		readFailure(c, e, "订单不存在")
		return false
	}
	if job == "" {
		c.JSON(404, gin.H{"error": "订单不存在"})
		return false
	}
	return requireJobOwner(c, job)
}
func getCheckout(c *gin.Context) {
	if checkoutOwner(c) {
		checkoutView(c, c.Param("id"), 200)
	}
}

// Only called after verifying NOTPAY or successfully creating the same original
// merchant order. A lost close response is unknown, never proof of closure.
func closeKnownCheckout(id string) (bool, error) {
	if err := wxCloseOrder(id); err != nil {
		queryAndSettle(id)
		return false, err
	}
	_, err := pool.Exec(contextBG(), "UPDATE orders SET status='closed',updated_at=now() WHERE id=$1 AND status IN ('pending','expired')", id)
	return true, err
}
func closeCheckoutOrder(c *gin.Context, id string) {
	if confirmed, err := closeKnownCheckout(id); err != nil {
		if confirmed {
			c.JSON(503, gin.H{"error": "渠道已关闭订单，本地仍在同步，请稍后核对"})
		} else {
			checkoutView(c, id, 202)
		}
		return
	}
	checkoutView(c, id, 200)
}

// Explicit recovery only. First verify the same merchant order at the channel;
// never fabricate a close or replace an order after an ambiguous timeout.
func resumeCheckout(c *gin.Context) {
	if !checkoutOwner(c) {
		return
	}
	id := c.Param("id")
	if !allowConfirm(id) {
		c.JSON(429, gin.H{"error": "核对过于频繁，请稍后重试"})
		return
	}
	var cents int64
	var mock bool
	var status, cu, snapshot, checkoutState string
	tx, err := pool.Begin(c)
	if err != nil {
		c.JSON(503, gin.H{"error": "暂时无法核对"})
		return
	}
	defer tx.Rollback(contextBG())
	var lease *time.Time
	err = tx.QueryRow(c, `SELECT amount_cents,mock,status,COALESCE(code_url,''),checkout_lease_until,checkout_request,checkout_state FROM orders WHERE id=$1 FOR UPDATE`, id).Scan(&cents, &mock, &status, &cu, &lease, &snapshot, &checkoutState)
	if err != nil {
		readFailure(c, err, "订单不存在")
		return
	}
	if status == "failed" || checkoutState == "failed" {
		c.JSON(409, gin.H{"error": "原支付入口已失败，请返回项目重新选择支付项目"})
		return
	}
	expired := nativeCheckoutExpired(snapshot, id, cents)
	if status == "paid" || (status == "pending" && cu != "" && !expired) {
		tx.Rollback(contextBG())
		checkoutView(c, id, 200)
		return
	}
	if status != "pending" && status != "expired" {
		c.JSON(409, gin.H{"error": "订单已关闭，请返回项目重新选择"})
		return
	}
	if lease != nil && lease.After(time.Now()) {
		c.JSON(409, gin.H{"error": "订单仍在核对中，请稍后重试"})
		return
	}
	// Recovery can perform four sequential channel calls, each bounded to 10s:
	// query, same-order creation, close, and a final reconciliation query.
	if _, err = tx.Exec(c, "UPDATE orders SET checkout_lease_until=now()+interval '1 minute' WHERE id=$1", id); err != nil {
		c.JSON(503, gin.H{"error": "暂时无法核对"})
		return
	}
	if err = tx.Commit(contextBG()); err != nil {
		c.JSON(503, gin.H{"error": "暂时无法核对"})
		return
	}
	if mock {
		cu = "wxp://mock/" + id
	} else {
		q, e := wxQueryOrder(id)
		if e != nil {
			ae, ok := e.(*wxAPIError)
			if !ok || ae.Code != "ORDER_NOT_EXIST" {
				c.JSON(503, gin.H{"error": "渠道尚未确认订单，请勿重复支付，稍后重试或联系支持"})
				return
			}
		} else {
			if !validChannelOrder(q, id, cents) {
				c.JSON(409, gin.H{"error": "渠道订单信息待人工核对"})
				return
			}
			if q.TradeState != "NOTPAY" {
				if err = applyChannelOrder(id, cents, q); err != nil {
					c.JSON(503, gin.H{"error": "订单结果仍在同步，请稍后核对"})
					return
				}
				checkoutView(c, id, 200)
				return
			}
			if expired {
				// Signed NOTPAY is not CLOSED. A successful, verified close is required.
				// If payment races the close (or its response is lost), query that same
				// order again and retain pending unless the channel proves otherwise.
				closeCheckoutOrder(c, id)
				return
			}
		}
		if !validNativeSnapshot([]byte(snapshot), id, cents) {
			c.JSON(409, gin.H{"error": "原订单下单参数缺失或已变更，请联系支持核对；未重新发起付款"})
			return
		}
		cu, err = wxCreateNativeRequest([]byte(snapshot))
		if err != nil {
			slog.Error("wx native resume failed", "order", id, "definitive", isDefinitiveWxError(err), "error", safeWxError(err))
			if isDefinitiveWxError(err) {
				markCheckoutFailed(id, err)
				c.JSON(503, gin.H{"error": "支付入口创建失败，请重新选择支付项目", "retryable": true})
				return
			}
			markCheckoutUnknown(id, err)
			c.JSON(503, gin.H{"error": "支付入口仍待核对，已保留原订单，请勿重复付款"})
			return
		}
		// ORDER_NOT_EXIST can precede this successful replay. Keep the exact original
		// snapshot; an expired one must finish its same-order close/reconcile cycle
		// instead of returning a fresh channel QR that local expiry would hide.
		if expired {
			closeCheckoutOrder(c, id)
			return
		}
	}
	if _, err = pool.Exec(contextBG(), "UPDATE orders SET code_url=$2,status='pending',checkout_state='ready',checkout_error='',updated_at=now() WHERE id=$1 AND status IN ('pending','expired')", id, cu); err != nil {
		c.JSON(503, gin.H{"error": "订单暂未同步"})
		return
	}
	checkoutView(c, id, 200)
}
