package app

// Read-only audit evidence: copy into an isolated test checkout's server/ only.
// These regression assertions intentionally FAIL on the audited e1d2239 source.
// Uses the existing TestMain guard, local zerobrand_test and temporary images;
// starts no workers and invokes no payment or image provider.
import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func auditAuthImage(t *testing.T, jid, name, kind string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ZEROBRAND_GEN_DIR", dir)
	if err := os.MkdirAll(filepath.Join(dir, jid), 0700); err != nil { t.Fatal(err) }
	mkTestPNG(t, filepath.Join(dir, jid, name))
	file := webPath(jid, name)
	mustExec(t, "INSERT INTO job_images(job_id,kind,idx,file,tokens) VALUES($1,$2,1,$3,0)", jid, kind, file)
	return file
}

func auditAuthAgent(t *testing.T) (string, string, string) {
	t.Helper()
	id, email, password := "AG"+randHex(6), "audit-"+randHex(5)+"@example.invalid", "audit-only-password"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil { t.Fatal(err) }
	mustExec(t, "INSERT INTO agents(id,name,email,password_hash,code) VALUES($1,'audit agent',$2,$3,$4)", id, email, string(hash), "AG"+randHex(4))
	t.Cleanup(func() {
		mustExec(t, "DELETE FROM agent_commissions WHERE agent_id=$1", id)
		mustExec(t, "DELETE FROM agents WHERE id=$1", id)
	})
	return id, email, password
}

func TestAuditAuthDetailMustRequireOwner(t *testing.T) {
	jid, vid := commercialFixture(t, "done", "done")
	file := auditAuthImage(t, jid, "logo_h.png", "logo_h")
	cl, base := commercialServer(t)
	for _, ck := range []*http.Cookie{nil, commercialCookie(visitorCookie, randHex(16), 0)} {
		var cookies []*http.Cookie
		if ck != nil { cookies = append(cookies, ck) }
		st, body := commercialRequest(t, cl, "GET", base+"/api/jobs/"+jid+"/detail", "", cookies...)
		t.Logf("unauthorized detail status=%d contains_private_file=%v", st, strings.Contains(body,file))
		if st != 404 { t.Errorf("detail must reject anonymous/foreign visitor, got %d", st) }
	}
	st, _ := commercialRequest(t, cl, "GET", base+"/api/jobs/"+jid+"/detail", "", commercialCookie(visitorCookie,vid,0))
	if st != 200 { t.Errorf("owner detail should remain available: %d", st) }
}

func TestAuditAuthZIPMustRequireOwner(t *testing.T) {
	jid, _ := commercialFixture(t, "done", "done")
	auditAuthImage(t, jid, "logo_h.png", "logo_h")
	mustExec(t, "UPDATE jobs SET unlocked_packs=ARRAY['all'] WHERE id=$1", jid)
	cl, base := commercialServer(t)
	st, body := commercialRequest(t, cl, "GET", base+"/api/jobs/"+jid+"/download", "")
	t.Logf("anonymous ZIP status=%d bytes=%d (normal file paths currently yield an empty ZIP)", st, len(body))
	if st != 404 { t.Errorf("ZIP must reject anonymous requests before creating archive, got %d", st) }
}

func TestAuditAuthZIPMustContainCanonicalGeneratedFile(t *testing.T) {
	jid, vid := commercialFixture(t, "done", "done")
	auditAuthImage(t, jid, "logo_h.png", "logo_h")
	mustExec(t, "UPDATE jobs SET unlocked_packs=ARRAY['all'] WHERE id=$1", jid)
	cl, base := commercialServer(t)
	st, body := commercialRequest(t, cl, "GET", base+"/api/jobs/"+jid+"/download", "", commercialCookie(visitorCookie,vid,0))
	if st != 200 { t.Fatalf("owner ZIP: %d", st) }
	z, err := zip.NewReader(bytes.NewReader([]byte(body)), int64(len(body)))
	if err != nil { t.Fatal(err) }
	t.Logf("owner ZIP status=%d bytes=%d entries=%d", st, len(body), len(z.File))
	if len(z.File) != 1 { t.Errorf("canonical gen/<job>/<name> file was omitted, archive has %d entries", len(z.File)) }
}

func TestAuditAuthLockedOriginalMustRequirePurchase(t *testing.T) {
	jid, vid := commercialFixture(t, "done", "done")
	file := auditAuthImage(t, jid, "logo_h.png", "logo_h")
	cl, base := commercialServer(t)
	ck := commercialCookie(visitorCookie,vid,0)
	st, detail := commercialRequest(t, cl, "GET", base+"/api/jobs/"+jid+"/detail", "", ck)
	if st != 200 { t.Fatal(st) }
	var out struct { Items []struct { Kind string; Purchased, Unlocked bool } }
	if err := json.Unmarshal([]byte(detail), &out); err != nil { t.Fatal(err) }
	found := false
	for _, it := range out.Items { if it.Kind=="logo_h" { found=true; if it.Purchased || it.Unlocked { t.Fatal("fixture unexpectedly purchased") } } }
	if !found { t.Fatal("logo_h pricing fixture missing") }
	st, body := commercialRequest(t, cl, "GET", base+"/"+file, "", ck)
	t.Logf("unlocked=false, direct original status=%d PNG=%v", st, strings.HasPrefix(body,"\x89PNG"))
	if st == 200 { t.Error("unpaid locked original must not be served merely because requester owns the job") }
}

func TestAuditAuthAgentCookieMustReachCanonicalAPI(t *testing.T) {
	_, email, password := auditAuthAgent(t)
	r := buildRouter()
	body, _ := json.Marshal(map[string]string{"Email":email,"Password":password})
	req := httptest.NewRequest("POST", "https://example.test/0brand/api/agent/login", bytes.NewReader(body))
	req.Header.Set("Content-Type","application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w,req)
	if w.Code != 200 { t.Fatalf("login failed: %d",w.Code) }
	jar, _ := cookiejar.New(nil)
	canonicalLogin, _ := url.Parse("https://example.test/api/agent/login")
	canonicalAPI, _ := url.Parse("https://example.test/api/agent/me")
	jar.SetCookies(canonicalLogin,w.Result().Cookies())
	for _, ck := range w.Result().Cookies() { t.Logf("Set-Cookie name=%s path=%s secure=%v httponly=%v",ck.Name,ck.Path,ck.Secure,ck.HttpOnly) }
	if len(jar.Cookies(canonicalAPI))==0 { t.Error("successful canonical-root login has no cookie applicable to /api/agent/me") }
}

func TestAuditAuthAgentLoginMustThrottle(t *testing.T) {
	_, email, _ := auditAuthAgent(t)
	r := buildRouter()
	limited := false
	for i:=0;i<20;i++ {
		body, _ := json.Marshal(map[string]string{"Email":email,"Password":"wrong-password"})
		req := httptest.NewRequest("POST", "https://example.test/0brand/api/agent/login", bytes.NewReader(body))
		req.Header.Set("Content-Type","application/json")
		req.RemoteAddr="192.0.2.73:12345"
		w := httptest.NewRecorder(); r.ServeHTTP(w,req)
		if w.Code==429 { limited=true }
		if w.Code!=401 && w.Code!=429 { t.Fatalf("unexpected login status %d",w.Code) }
	}
	if !limited { t.Error("20 same-IP same-account bad passwords all reached credential verification without 429") }
}

func TestAuditAuthDisabledAgentSessionMustBeRejected(t *testing.T) {
	id, _, _ := auditAuthAgent(t)
	mustExec(t,"UPDATE agents SET active=false WHERE id=$1",id)
	cl, base := commercialServer(t)
	st, body := commercialRequest(t,cl,"GET",base+"/api/agent/me","",commercialCookie(agentCookie,id,0))
	t.Logf("disabled agent status=%d active_false_disclosed=%v",st,strings.Contains(body,`"active":false`))
	if st!=401 && st!=403 { t.Errorf("disabled existing agent session must be revoked, got %d",st) }
}

func TestAuditAuthRedeemLimiterMustBlockFurtherGuesses(t *testing.T) {
	_, vid := commercialFixture(t,"done","done")
	code := upperCode("AUDITRL")
	mustExec(t,"INSERT INTO invite_codes(code,max_uses) VALUES($1,1)",code)
	t.Cleanup(func(){ mustExec(t,"DELETE FROM invite_codes WHERE code=$1",code) })
	resetRateWindow(); t.Cleanup(resetRateWindow)
	cl,base := commercialServer(t)
	ck := commercialCookie(visitorCookie,vid,0)
	for i:=0;i<11;i++ { commercialRequest(t,cl,"POST",base+"/api/code",`{"code":"AUDIT-NONEXISTENT-CODE"}`,ck) }
	st,_ := commercialRequest(t,cl,"POST",base+"/api/code",`{"code":"`+code+`"}`,ck)
	t.Logf("valid guess immediately after 11 failures status=%d",st)
	if st!=429 { t.Errorf("limit must block attempts before validating codes, got %d",st) }
}

func TestAuditAuthAgentMustNotReceiveCustomerRecoveryCredential(t *testing.T) {
	jid, owner := commercialFixture(t,"done","done")
	agentID,_,_ := auditAuthAgent(t)
	code,orderID,attacker := upperCode("AUDITREC"),"0BAUDIT"+randHex(8),randHex(16)
	mustExec(t,"INSERT INTO visitors(id) VALUES($1)",attacker)
	mustExec(t,"INSERT INTO invite_codes(code,max_uses,used,bound_visitor) VALUES($1,1,1,$2)",code,owner)
	mustExec(t,"UPDATE jobs SET quota_code=$2 WHERE id=$1",jid,code)
	mustExec(t,"INSERT INTO orders(id,code,amount_cents,channel,status,visitor) VALUES($1,$2,6900,'native','paid',$3)",orderID,code,owner)
	mustExec(t,"INSERT INTO agent_commissions(agent_id,order_id,order_amount_cents,commission_cents) VALUES($1,$2,6900,2760)",agentID,orderID)
	t.Cleanup(func(){
		mustExec(t,"DELETE FROM audit_events WHERE object_id=$1",code)
		mustExec(t,"DELETE FROM orders WHERE id=$1",orderID)
		mustExec(t,"DELETE FROM invite_codes WHERE code=$1",code)
		mustExec(t,"DELETE FROM visitors WHERE id=$1",attacker)
	})
	resetRateWindow();t.Cleanup(resetRateWindow)
	cl,base := commercialServer(t)
	st,body := commercialRequest(t,cl,"GET",base+"/api/agent/me/commissions","",commercialCookie(agentCookie,agentID,0))
	if st!=200 { t.Fatalf("agent commissions: %d",st) }
	if !strings.Contains(body,orderID) { return } // Opaque display reference fixes disclosure.
	st,_=commercialRequest(t,cl,"POST",base+"/api/recover",`{"order_id":"`+orderID+`"}`,commercialCookie(visitorCookie,attacker,0))
	var newOwner string
	if err:=pool.QueryRow(contextBG(),"SELECT visitor FROM jobs WHERE id=$1",jid).Scan(&newOwner);err!=nil { t.Fatal(err) }
	t.Logf("agent-visible order ID recovery status=%d changed_customer_job_owner=%v",st,newOwner==attacker)
	if newOwner==attacker { t.Error("referral agent received a bearer recovery credential and seized the customer's delivery") }
}

func TestAuditAuthWebPAlternativeMustRejectSymlink(t *testing.T) {
	jid,vid:=commercialFixture(t,"done","done")
	file:=auditAuthImage(t,jid,"logo_h.png","logo_h")
	outside:=filepath.Join(t.TempDir(),"private.webp")
	if err:=os.WriteFile(outside,[]byte("AUDIT_PRIVATE_WEBP_MARKER"),0600);err!=nil { t.Fatal(err) }
	if err:=os.Symlink(outside,filepath.Join(genDir(),jid,"logo_h.webp"));err!=nil { t.Fatal(err) }
	r:=buildRouter()
	req:=httptest.NewRequest("GET","https://example.test/0brand/"+file,nil)
	req.AddCookie(commercialCookie(visitorCookie,vid,0));req.Header.Set("Accept","image/webp")
	w:=httptest.NewRecorder();r.ServeHTTP(w,req)
	if strings.Contains(w.Body.String(),"AUDIT_PRIVATE_WEBP_MARKER") { t.Error("WebP negotiation followed a sibling symlink outside generated; original-path Lstat did not protect alternate file") }
}
