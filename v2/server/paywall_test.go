package app

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDirectionsPaywallAndUnlock(t *testing.T) {
	commercialRequireTestDB(t)
	resetCreateLimiter()
	t.Cleanup(resetCreateLimiter)
	t.Setenv("ZEROBRAND_DIRECTIONS_PAYWALL", "1")
	enablePayMockV2(t)
	srv := httptest.NewTLSServer(buildRouter())
	defer srv.Close()
	cl := v2Client(srv)
	base := srv.URL + "/0brand"
	st, out := postJSONv2(t, cl, base+"/api/jobs", `{"cn_name":"九块九测试","industry":"茶饮"}`)
	if st != 201 {
		t.Fatalf("create=%d %v", st, out)
	}
	jid, _ := out["id"].(string)
	if jid == "" {
		t.Fatal("missing job id")
	}
	var vid string
	if err := pool.QueryRow(contextBG(), "SELECT COALESCE(visitor,'') FROM jobs WHERE id=$1", jid).Scan(&vid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		mustExec(t, "DELETE FROM order_items WHERE order_id IN (SELECT id FROM orders WHERE job_id=$1)", jid)
		mustExec(t, "DELETE FROM orders WHERE job_id=$1", jid)
		mustExec(t, "DELETE FROM job_images WHERE job_id=$1", jid)
		mustExec(t, "DELETE FROM jobs WHERE id=$1", jid)
		if vid != "" {
			mustExec(t, "DELETE FROM visitors WHERE id=$1", vid)
		}
	})
	var unlocked bool
	var stage, status string
	if err := pool.QueryRow(contextBG(), "SELECT directions_unlocked,stage,status FROM jobs WHERE id=$1", jid).Scan(&unlocked, &stage, &status); err != nil {
		t.Fatal(err)
	}
	if unlocked || stage != "unlock" || status != "waiting_unlock" {
		t.Fatalf("locked state=%v/%s/%s", unlocked, stage, status)
	}
	st, order := postJSONv2(t, cl, base+"/api/jobs/"+jid+"/unlock", `{}`)
	if st != 201 {
		t.Fatalf("unlock=%d %v", st, order)
	}
	oid, _ := order["id"].(string)
	if oid == "" {
		t.Fatalf("missing order: %v", order)
	}
	var cents int64
	if err := pool.QueryRow(contextBG(), "SELECT amount_cents FROM orders WHERE id=$1", oid).Scan(&cents); err != nil || cents != 990 {
		t.Fatalf("amount=%d err=%v", cents, err)
	}
	if err := settleOrder(oid, "", "", 990, "990-fake-tx"); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(contextBG(), "SELECT directions_unlocked,stage,status FROM jobs WHERE id=$1", jid).Scan(&unlocked, &stage, &status); err != nil {
		t.Fatal(err)
	}
	if !unlocked || stage != "directions" || status != "queued" {
		t.Fatalf("after pay state=%v/%s/%s", unlocked, stage, status)
	}
	var raw []byte
	_ = json.Unmarshal([]byte(`{}`), &raw)
}

func TestDirectionsUnlockCodeBypassesPaywallOnce(t *testing.T) {
	commercialRequireTestDB(t)
	resetCreateLimiter()
	t.Setenv("ZEROBRAND_DIRECTIONS_PAYWALL", "1")
	t.Cleanup(resetCreateLimiter)
	vid := "unlock-code-" + newID(10)
	code := "UNLOCK" + newID(8)
	mustExec(t, "INSERT INTO visitors(id) VALUES($1)", vid)
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,bound_visitor,item_kind) VALUES($1,1,$2,'directions_unlock')", code, vid)
	t.Cleanup(func() {
		mustExec(t, "DELETE FROM job_images WHERE job_id IN (SELECT id FROM jobs WHERE visitor=$1)", vid)
		mustExec(t, "DELETE FROM jobs WHERE visitor=$1", vid)
		mustExec(t, "DELETE FROM invite_codes WHERE code=$1", code)
		mustExec(t, "DELETE FROM visitors WHERE id=$1", vid)
	})
	req := httptest.NewRequest("POST", "/0brand/api/jobs", strings.NewReader(`{"cn_name":"兑换解锁测试","industry":"茶饮"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(commercialCookie(visitorCookie, vid, 0))
	req.AddCookie(commercialCookie(gateCookie, code, 0))
	rec := httptest.NewRecorder()
	buildRouter().ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("create=%d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		ID       string `json:"id"`
		Unlocked bool   `json:"directions_unlocked"`
		Stage    string `json:"stage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.ID == "" || !out.Unlocked || out.Stage != "directions" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	var used int
	if err := pool.QueryRow(contextBG(), "SELECT used FROM invite_codes WHERE code=$1", code).Scan(&used); err != nil || used != 1 {
		t.Fatalf("used=%d err=%v", used, err)
	}
	var quota string
	if err := pool.QueryRow(contextBG(), "SELECT COALESCE(quota_code,'') FROM jobs WHERE id=$1", out.ID).Scan(&quota); err != nil || quota != code {
		t.Fatalf("job quota=%q err=%v", quota, err)
	}
}

func TestUnlockWithCodeOnExistingLockedJob(t *testing.T) {
	commercialRequireTestDB(t)
	resetCreateLimiter()
	t.Cleanup(resetCreateLimiter)
	t.Setenv("ZEROBRAND_DIRECTIONS_PAYWALL", "1")
	vid := "unlock-job-" + newID(10)
	code := "JOBCODE" + strings.ToUpper(newID(8))
	mustExec(t, "INSERT INTO visitors(id) VALUES($1)", vid)
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,bound_visitor,item_kind) VALUES($1,1,$2,'directions_unlock')", code, vid)
	t.Cleanup(func() {
		mustExec(t, "DELETE FROM invite_codes WHERE code=$1", code)
		mustExec(t, "DELETE FROM jobs WHERE visitor=$1", vid)
		mustExec(t, "DELETE FROM visitors WHERE id=$1", vid)
	})
	jid := "LOCK" + newID(10)
	mustExec(t, "INSERT INTO jobs(id,code,form,status,stage,visitor,directions_unlocked) VALUES($1,'','{}','waiting_unlock','unlock',$2,false)", jid, vid)
	req := httptest.NewRequest("POST", "/0brand/api/jobs/"+jid+"/unlock-code", strings.NewReader(`{"code":"`+code+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(commercialCookie(visitorCookie, vid, 0))
	rec := httptest.NewRecorder()
	buildRouter().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("unlock-code=%d %s", rec.Code, rec.Body.String())
	}
	var unlocked bool
	var stage, method string
	if err := pool.QueryRow(contextBG(), "SELECT directions_unlocked,stage,unlock_method FROM jobs WHERE id=$1", jid).Scan(&unlocked, &stage, &method); err != nil {
		t.Fatal(err)
	}
	if !unlocked || stage != "directions" || method != "code" {
		t.Fatalf("state=%v/%s/%s", unlocked, stage, method)
	}
	var used int
	if err := pool.QueryRow(contextBG(), "SELECT used FROM invite_codes WHERE code=$1", code).Scan(&used); err != nil || used != 1 {
		t.Fatalf("used=%d err=%v", used, err)
	}
}

func TestUnlockCodeBlockedByPendingPayment(t *testing.T) {
	commercialRequireTestDB(t)
	resetCreateLimiter()
	t.Cleanup(resetCreateLimiter)
	t.Setenv("ZEROBRAND_DIRECTIONS_PAYWALL", "1")
	vid := "unlock-pending-" + newID(10)
	code := "PENDCODE" + strings.ToUpper(newID(8))
	mustExec(t, "INSERT INTO visitors(id) VALUES($1)", vid)
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,bound_visitor,item_kind) VALUES($1,1,$2,'directions_unlock')", code, vid)
	jid := "PEND" + newID(10)
	mustExec(t, "INSERT INTO jobs(id,code,form,status,stage,visitor,directions_unlocked) VALUES($1,'','{}','waiting_unlock','unlock',$2,false)", jid, vid)
	oid := "PENDORDER" + newID(8)
	mustExec(t, `INSERT INTO orders(id,code,amount_cents,channel,status,mock,visitor,job_id,pack,checkout_request,checkout_state) VALUES($1,'',990,'native','pending',true,$2,$3,'directions_unlock','{}','ready')`, oid, vid, jid)
	t.Cleanup(func() {
		mustExec(t, "DELETE FROM orders WHERE id=$1", oid)
		mustExec(t, "DELETE FROM invite_codes WHERE code=$1", code)
		mustExec(t, "DELETE FROM jobs WHERE id=$1", jid)
		mustExec(t, "DELETE FROM visitors WHERE id=$1", vid)
	})
	req := httptest.NewRequest("POST", "/0brand/api/jobs/"+jid+"/unlock-code", strings.NewReader(`{"code":"`+code+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(commercialCookie(visitorCookie, vid, 0))
	rec := httptest.NewRecorder()
	buildRouter().ServeHTTP(rec, req)
	if rec.Code != 409 {
		t.Fatalf("pending must block code: %d %s", rec.Code, rec.Body.String())
	}
	var used int
	if err := pool.QueryRow(contextBG(), "SELECT used FROM invite_codes WHERE code=$1", code).Scan(&used); err != nil || used != 0 {
		t.Fatalf("code consumed despite pending: used=%d err=%v", used, err)
	}
}
