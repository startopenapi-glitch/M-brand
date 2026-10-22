package app

// onecode_test.go — 一码一次 · 一码一人（2026-10-13 用户确认口径）
//  1) 他人兑换已绑定/已使用码 → 403；原持有人仍可进入查看历史
//  2) 一码两任务：只有一个能占票
//  3) 失败返还 → used 归零且清 used_at/used_by
//  4) 订单号找回 → 覆盖重绑到新会话，新会话可用
//  5) 管理员批量发码：全部单次码
//  6) 兑换失败限速（10 次/分/IP）

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// resetRateWindow：清空滑动窗口限速（供测试隔离用）
func resetRateWindow() {
	rlMu.Lock()
	rlMap = map[string][]time.Time{}
	rlMu.Unlock()
}

func TestOneCodeOneUseRedeemRules(t *testing.T) {
	resetCreateLimiter()
	resetRateWindow()
	defer resetRateWindow()
	srv, base := v2Server(t)
	clA, clB := v2Client(srv), v2Client(srv)

	stA, oA := postJSONv2(t, clA, base+"/api/jobs", `{"cn_name":"一码一人","industry":"精制茶饮"}`)
	if stA != 201 {
		t.Fatalf("建任务失败: %d %v", stA, oA)
	}
	jidA := oA["id"].(string)
	var vid string
	code := upperCode("ONEUSE")
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,used,note) VALUES($1,1,0,'onecode')", code)
	defer func() {
		mustExec(t, "DELETE FROM invite_codes WHERE code=$1", code)
		mustExec(t, "DELETE FROM jobs WHERE id=$1", jidA)
		if vid != "" {
			mustExec(t, "DELETE FROM visitors WHERE id=$1", vid)
		}
	}()

	// A 首次兑换 → 200 且绑定
	if st, o := postJSONv2(t, clA, base+"/api/code", `{"code":"`+code+`"}`); st != 200 {
		t.Fatalf("首次兑换应 200: %d %v", st, o)
	}
	var bound string
	pool.QueryRow(contextBG(), "SELECT COALESCE(bound_visitor,'') FROM invite_codes WHERE code=$1", code).Scan(&bound)
	if bound == "" {
		t.Fatal("兑换应绑定会话")
	}
	// B（他人）兑换 → 403（一码一人）
	if st, o := postJSONv2(t, clB, base+"/api/code", `{"code":"`+code+`"}`); st != 403 {
		t.Fatalf("他人兑换应 403: %d %v", st, o)
	}
	// 用尽后：本人仍可进入（看历史），他人 403
	mustExec(t, "UPDATE invite_codes SET used=1 WHERE code=$1", code)
	if st, _ := postJSONv2(t, clA, base+"/api/code", `{"code":"`+code+`"}`); st != 200 {
		t.Fatalf("原持有人进入应 200: %d", st)
	}
	if st, _ := postJSONv2(t, clB, base+"/api/code", `{"code":"`+code+`"}`); st != 403 {
		t.Fatalf("他人兑换已用尽码应 403: %d", st)
	}
	pool.QueryRow(contextBG(), "SELECT COALESCE(visitor,'') FROM jobs WHERE id=$1", jidA).Scan(&vid)
}

func TestOneCodeOneUseSingleConsume(t *testing.T) {
	resetCreateLimiter()
	resetRateWindow()
	defer resetRateWindow()
	srv, base := v2Server(t)
	cl := v2Client(srv)

	code := upperCode("ONECON")
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,used,note) VALUES($1,1,0,'onecode')", code)
	var vid string
	var jobs []string
	defer func() {
		for _, j := range jobs {
			mustExec(t, "DELETE FROM jobs WHERE id=$1", j)
		}
		mustExec(t, "DELETE FROM invite_codes WHERE code=$1", code)
		if vid != "" {
			mustExec(t, "DELETE FROM visitors WHERE id=$1", vid)
		}
	}()

	// 兑换（绑定本会话）
	if st, _ := postJSONv2(t, cl, base+"/api/code", `{"code":"`+code+`"}`); st != 200 {
		t.Fatalf("兑换失败: %d", st)
	}
	// 持票建两个任务，都推到待选定
	for i := 0; i < 2; i++ {
		st, o := postJSONv2(t, cl, base+"/api/jobs", `{"cn_name":"一码一次","industry":"精制茶饮"}`)
		if st != 201 {
			t.Fatalf("第 %d 个任务创建失败: %d %v", i+1, st, o)
		}
		jid := o["id"].(string)
		jobs = append(jobs, jid)
		mustExec(t, "UPDATE jobs SET stage='choose', status='ready_to_choose' WHERE id=$1", jid)
		pool.QueryRow(contextBG(), "SELECT COALESCE(visitor,'') FROM jobs WHERE id=$1", jid).Scan(&vid)
	}
	// 第一个任务占票成功；第二个 402（一码只能用一次）
	if st, o := postJSONv2(t, cl, base+"/api/jobs/"+jobs[0]+"/choose", `{"direction":1}`); st != 200 {
		t.Fatalf("首任务 choose 应 200: %d %v", st, o)
	}
	if st, _ := postJSONv2(t, cl, base+"/api/jobs/"+jobs[1]+"/choose", `{"direction":0}`); st != 200 {
		t.Fatalf("第二任务免费系统卡应 200: %d", st)
	}
	rights,e:=jobEntitlements(contextBG(),pool,jobs[1]);if e!=nil||rights["worldview"]||rights["logo_h"]{t.Fatalf("exhausted ticket granted paid rights: %v %v",rights,e)}
	var used int
	var usedAt, usedBy *string
	pool.QueryRow(contextBG(),
		"SELECT used, used_at::text, used_by FROM invite_codes WHERE code=$1", code).Scan(&used, &usedAt, &usedBy)
	if used != 1 || usedAt == nil || usedBy == nil {
		t.Fatalf("占票应记录使用痕迹: used=%d used_at=%v used_by=%v", used, usedAt, usedBy)
	}
}

func TestOneCodeOneUseRefundResets(t *testing.T) {
	code := upperCode("ONEREF")
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,used,used_at,used_by,note) VALUES($1,1,1,now(),'whoever','onecode')", code)
	jid := newID(12)
	mustExec(t, `INSERT INTO jobs(id, code, form, status, stage, quota_code) VALUES($1,'','{}','boards_running','boards',$2)`, jid, code)
	defer func() {
		mustExec(t, "DELETE FROM jobs WHERE id=$1", jid)
		mustExec(t, "DELETE FROM invite_codes WHERE code=$1", code)
	}()

	markJobFailed(jid, "测试失败")
	var used int
	var usedAt, usedBy *string
	pool.QueryRow(contextBG(),
		"SELECT used, used_at::text, used_by FROM invite_codes WHERE code=$1", code).Scan(&used, &usedAt, &usedBy)
	if used != 0 || usedAt != nil || usedBy != nil {
		t.Fatalf("返还后应清零: used=%d used_at=%v used_by=%v", used, usedAt, usedBy)
	}
	var refunded bool
	pool.QueryRow(contextBG(), "SELECT refunded FROM jobs WHERE id=$1", jid).Scan(&refunded)
	if !refunded {
		t.Fatal("应标记 refunded")
	}
	// 幂等：再失败一次不得重复返还
	markJobFailed(jid, "再次失败")
	pool.QueryRow(contextBG(), "SELECT used FROM invite_codes WHERE code=$1", code).Scan(&used)
	if used != 0 {
		t.Fatalf("不得重复返还: used=%d", used)
	}
}

func TestRecoverRebindsCodeToNewSession(t *testing.T) {
	enablePayMockV2(t)
	resetCreateLimiter()
	resetRateWindow()
	defer resetRateWindow()
	srv, base := v2Server(t)
	clA := v2Client(srv)

	st, o := recordedLegacyOrder(t, clA, base)
	if st != 201 {
		t.Fatalf("下单失败: %d %v", st, o)
	}
	oid := o["id"].(string)
	var vidA string
	pool.QueryRow(contextBG(), "SELECT COALESCE(visitor,'') FROM orders WHERE id=$1", oid).Scan(&vidA)
	if err := settleOrder(oid, "", "", int64(priceCents), "TXREB1"); err != nil {
		t.Fatal(err)
	}
	var code, boundA string
	pool.QueryRow(contextBG(), "SELECT COALESCE(code,'') FROM orders WHERE id=$1", oid).Scan(&code)
	pool.QueryRow(contextBG(), "SELECT COALESCE(bound_visitor,'') FROM invite_codes WHERE code=$1", code).Scan(&boundA)
	if code == "" || boundA != vidA {
		t.Fatalf("入账应发新码并绑定买家: code=%q bound=%q vidA=%q", code, boundA, vidA)
	}

	// B：凭订单号找回 → 覆盖重绑
	clB := v2Client(srv)
	var vidB string
	var jidB string
	defer func() {
		mustExec(t, "DELETE FROM orders WHERE id=$1", oid)
		mustExec(t, "DELETE FROM invite_codes WHERE code=$1", code)
		if jidB != "" {
			mustExec(t, "DELETE FROM jobs WHERE id=$1", jidB)
		}
		if vidA != "" {
			mustExec(t, "DELETE FROM visitors WHERE id=$1", vidA)
		}
		if vidB != "" {
			mustExec(t, "DELETE FROM visitors WHERE id=$1", vidB)
		}
	}()
	if st2, o2 := postJSONv2(t, clB, base+"/api/recover", `{"order_id":"`+oid+`"}`); st2 != 200 || o2["code"] != code {
		t.Fatalf("凭订单号找回应 200: %d %v", st2, o2)
	}
	// B 建任务（持找回的票）并占票成功
	st3, o3 := postJSONv2(t, clB, base+"/api/jobs", `{"cn_name":"找回重绑","industry":"精制茶饮"}`)
	if st3 != 201 {
		t.Fatalf("B 建任务失败: %d %v", st3, o3)
	}
	jidB = o3["id"].(string)
	pool.QueryRow(contextBG(), "SELECT COALESCE(visitor,'') FROM jobs WHERE id=$1", jidB).Scan(&vidB)
	mustExec(t, "UPDATE jobs SET stage='choose', status='ready_to_choose' WHERE id=$1", jidB)
	var boundB string
	pool.QueryRow(contextBG(), "SELECT COALESCE(bound_visitor,'') FROM invite_codes WHERE code=$1", code).Scan(&boundB)
	if vidB == "" || boundB != vidB {
		t.Fatalf("订单号找回应把码重绑到新会话: bound=%q vidB=%q", boundB, vidB)
	}
	if st4, o4 := postJSONv2(t, clB, base+"/api/jobs/"+jidB+"/choose", `{"direction":0}`); st4 != 200 {
		t.Fatalf("新会话应能用该码占票: %d %v", st4, o4)
	}
}

func TestAdminBatchSingleUseCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	admin := r.Group("/0brand/api/admin", func(c *gin.Context) { c.Set("adminName", "unittest"); c.Next() })
	admin.POST("/codes", adminCreateCode)
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := srv.Client().Post(srv.URL+"/0brand/api/admin/codes", "application/json",
		bytes.NewReader([]byte(`{"count":3,"note":"batch test"}`)))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Codes []string `json:"codes"`
		Count int      `json:"count"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if resp.StatusCode != 201 || out.Count != 3 || len(out.Codes) != 3 {
		t.Fatalf("批量发码应返回 3 张: %d %v", resp.StatusCode, out)
	}
	for _, c := range out.Codes {
		defer mustExec(t, "DELETE FROM invite_codes WHERE code=$1", c)
		var max int
		var bound string
		pool.QueryRow(contextBG(), "SELECT max_uses, COALESCE(bound_visitor,'') FROM invite_codes WHERE code=$1", c).Scan(&max, &bound)
		if max != 1 {
			t.Fatalf("应为单次码: code=%s max=%d", c, max)
		}
		if bound != "" {
			t.Fatalf("新码不应预绑定: %s", c)
		}
		if len(c) < 8 {
			t.Fatalf("码长应 ≥8: %s", c)
		}
	}
	// 超过上限 → 400
	resp2, _ := srv.Client().Post(srv.URL+"/0brand/api/admin/codes", "application/json",
		bytes.NewReader([]byte(`{"count":51}`)))
	resp2.Body.Close()
	if resp2.StatusCode != 400 {
		t.Fatalf("超过 50 张应 400: %d", resp2.StatusCode)
	}
}

func TestCodeRedeemFailureRateLimit(t *testing.T) {
	resetRateWindow()
	defer resetRateWindow()
	srv, base := v2Server(t)
	cl := v2Client(srv)
	got429 := false
	for i := 0; i < 12; i++ {
		st, _ := postJSONv2(t, cl, base+"/api/code", `{"code":"NOSUCH00"}`)
		if st == 429 {
			got429 = true
			break
		}
		if st != 403 {
			t.Fatalf("无效码应 403（第 %d 次）: %d", i+1, st)
		}
	}
	if !got429 {
		t.Fatal("连续失败应在限额（10/分）后 429")
	}
}

// 回归：无 gate 的新会话不得看到他人 code 为空的匿名订单（曾因 code=$1 且 $1=” 全表命中）
func TestMyOrdersNoCrossVisitorLeak(t *testing.T) {
	resetCreateLimiter()
	resetRateWindow()
	defer resetRateWindow()
	srv, base := v2Server(t)
	cl := v2Client(srv)

	other := upperCode("OTHER")
	oid := "0BLEAK" + time.Now().Format("150405") + "0001"
	mustExec(t, `INSERT INTO orders(id, code, amount_cents, channel, status, visitor)
		VALUES($1,'',6900,'native','pending',$2)`, oid, other)
	defer mustExec(t, "DELETE FROM orders WHERE id=$1", oid)

	// 本会话先建身份（发一条任务）
	st, o := postJSONv2(t, cl, base+"/api/jobs", `{"cn_name":"泄漏回归","industry":"精制茶饮"}`)
	if st != 201 {
		t.Fatalf("建任务失败: %d %v", st, o)
	}
	jid := o["id"].(string)
	var vid string
	pool.QueryRow(contextBG(), "SELECT COALESCE(visitor,'') FROM jobs WHERE id=$1", jid).Scan(&vid)
	defer func() {
		mustExec(t, "DELETE FROM jobs WHERE id=$1", jid)
		if vid != "" {
			mustExec(t, "DELETE FROM visitors WHERE id=$1", vid)
		}
	}()

	resp, err := cl.Get(base + "/api/my/orders")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if bytes.Contains(body, []byte(oid)) {
		t.Fatalf("本会话不应看到他人订单: %s", body)
	}
}
