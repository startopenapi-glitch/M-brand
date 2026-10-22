package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func opsJSON(t *testing.T, r http.Handler, method, path, body string, cookies ...*http.Cookie) (int, map[string]any, []*http.Cookie) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies { req.AddCookie(c) }
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out, rec.Result().Cookies()
}

func TestAdminTOTPSetupVerifyAndSessionInvalidation(t *testing.T) {
	commercialRequireTestDB(t)
	t.Setenv("ZEROBRAND_ADMIN_2FA", "1")
	user := "totp-admin-" + newID(6)
	hash, _ := bcrypt.GenerateFromPassword([]byte("A-strong-test-pass"), bcrypt.MinCost)
	mustExec(t, "INSERT INTO admins(username,password_hash) VALUES($1,$2)", user, string(hash))
	t.Cleanup(func(){ mustExec(t,"DELETE FROM admins WHERE username=$1",user) })
	adminLoginMu.Lock(); adminLoginAt=map[string]time.Time{}; adminLoginMu.Unlock()
	r := buildRouter()
	code, body, _ := opsJSON(t,r,"POST","/0brand/api/admin/login",`{"Username":"`+user+`","Password":"A-strong-test-pass"}`)
	if code!=200 || body["totp_setup_required"]!=true { t.Fatalf("setup login=%d %v",code,body) }
	secret,_ := body["secret"].(string); token,_ := body["setup_token"].(string)
	if !validTOTPSecret(secret) || token=="" { t.Fatalf("bad setup payload") }
	code, body, cookies := opsJSON(t,r,"POST","/0brand/api/admin/totp/setup",`{"Token":"`+token+`","Secret":"`+secret+`","Code":"`+totpCode(secret,time.Now())+`"}`)
	if code!=200 { t.Fatalf("setup=%d %v",code,body) }
	var cookie *http.Cookie
	for _, c := range cookies { if c.Name==adminCookie { cookie=c } }
	if cookie==nil { t.Fatal("missing admin cookie") }
	code, _, _ = opsJSON(t,r,"GET","/0brand/api/admin/stats","",cookie)
	if code!=200 { t.Fatalf("authed stats=%d",code) }
	mustExec(t,"UPDATE admins SET session_version=session_version+1 WHERE username=$1",user)
	code, _, _ = opsJSON(t,r,"GET","/0brand/api/admin/stats","",cookie)
	if code!=403 { t.Fatalf("old session must fail, got %d",code) }
	adminLoginMu.Lock(); adminLoginAt=map[string]time.Time{}; adminLoginMu.Unlock()
	code, body, _ = opsJSON(t,r,"POST","/0brand/api/admin/login",`{"Username":"`+user+`","Password":"A-strong-test-pass"}`)
	if code!=200 || body["totp_required"]!=true { t.Fatalf("verify login=%d %v",code,body) }
	challenge,_ := body["challenge"].(string)
	code, body, _ = opsJSON(t,r,"POST","/0brand/api/admin/totp/verify",`{"Challenge":"`+challenge+`","Code":"`+totpCode(secret,time.Now())+`"}`)
	if code!=200 { t.Fatalf("verify=%d %v",code,body) }
}

func TestSupportCaseIdempotentAndAnalytics(t *testing.T) {
	commercialRequireTestDB(t)
	vid := "support-" + newID(10)
	mustExec(t,"INSERT INTO visitors(id) VALUES($1)",vid)
	jid := "SUP" + newID(10)
	mustExec(t,`INSERT INTO jobs(id,code,form,status,stage,visitor) VALUES($1,'','{}','done','done',$2)`,jid,vid)
	t.Cleanup(func(){ mustExec(t,"DELETE FROM support_events WHERE case_id IN (SELECT id FROM support_cases WHERE visitor=$1)",vid);mustExec(t,"DELETE FROM support_cases WHERE visitor=$1",vid);mustExec(t,"DELETE FROM analytics_events WHERE job_id=$1",jid);mustExec(t,"DELETE FROM jobs WHERE id=$1",jid);mustExec(t,"DELETE FROM visitors WHERE id=$1",vid) })
	r := buildRouter(); cookie := commercialCookie(visitorCookie,vid,0)
	payload := `{"request_key":"REQ-`+strings.ToUpper(newID(12))+`","job_id":"`+jid+`","category":"generation","description":"方向图没有生成"}`
	code, body, _ := opsJSON(t,r,"POST","/0brand/api/support",payload,cookie)
	if code!=200 { t.Fatalf("support create=%d %v",code,body) }
	first,_ := body["id"].(string)
	code, body, _ = opsJSON(t,r,"POST","/0brand/api/support",payload,cookie)
	if code!=200 || body["id"]!=first { t.Fatalf("idempotency=%d %v",code,body) }
	var n int
	pool.QueryRow(contextBG(),"SELECT COUNT(*) FROM support_cases WHERE visitor=$1",vid).Scan(&n)
	if n!=1 { t.Fatalf("cases=%d",n) }
	pool.QueryRow(contextBG(),"SELECT COUNT(*) FROM analytics_events WHERE job_id=$1 AND event='support_created'",jid).Scan(&n)
	if n!=1 { t.Fatalf("analytics=%d",n) }
	adminVid := commercialCookie(adminCookie,"fixture-admin",1)
	code, _, _ = opsJSON(t,r,"GET","/0brand/api/admin/support","",adminVid)
	if code!=200 { t.Fatalf("admin support=%d",code) }
	code, _, _ = opsJSON(t,r,"GET","/0brand/api/admin/agents/reconcile","",adminVid)
	if code!=200 { t.Fatalf("agent reconcile=%d",code) }
}
