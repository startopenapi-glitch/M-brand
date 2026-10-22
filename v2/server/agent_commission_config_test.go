package app

import (
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func commissionInvite(t *testing.T, pct int) (string, string, string) {
	t.Helper()
	email, _ := agentFlowFixture(t)
	status, body, _ := opsJSON(t, buildRouter(), "POST", "/0brand/api/admin/agents/invite", fmt.Sprintf(`{"Email":%q,"CommissionPct":%d}`, email, pct), commercialCookie(adminCookie, "commission-admin", 1))
	if status != 201 {
		t.Fatalf("custom invite=%d", status)
	}
	link, _ := body["invite_url"].(string)
	if link == "" {
		t.Fatal("missing isolated fallback")
	}
	id, _ := body["id"].(string)
	return email, link[strings.LastIndex(link, "/")+1:], id
}

func TestAgentCommissionInvitationConfiguredBeforeAndAfterSending(t *testing.T) {
	email, token, id := commissionInvite(t, 27)
	r := buildRouter()
	admin := commercialCookie(adminCookie, "commission-admin", 1)
	status, info, _ := opsJSON(t, r, "GET", "/0brand/api/agent/invitations/"+token, "")
	if status != 200 || info["commission_pct"] != float64(27) {
		t.Fatalf("initial invitation=%d pct=%v", status, info["commission_pct"])
	}
	status, _, _ = opsJSON(t, r, "PATCH", "/0brand/api/admin/agents/invitations/"+id, `{"CommissionPct":32}`, admin)
	if status != 200 {
		t.Fatalf("pending commission update=%d", status)
	}
	status, info, _ = opsJSON(t, r, "GET", "/0brand/api/agent/invitations/"+token, "")
	if status != 200 || info["commission_pct"] != float64(32) {
		t.Fatalf("updated invitation=%d pct=%v", status, info["commission_pct"])
	}
	req := httptest.NewRequest("GET", "/0brand/api/admin/agents/invitations", nil)
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "token_hash") || strings.Contains(rec.Body.String(), token) {
		t.Fatalf("pending list unsafe or unavailable=%d", rec.Code)
	}
	var invitations []map[string]any
	if json.Unmarshal(rec.Body.Bytes(), &invitations) != nil || len(invitations) != 2 {
		t.Fatal("pending invitations missing")
	}
	status, _, cookies := opsJSON(t, r, "POST", "/0brand/api/agent/invitations/"+token+"/accept", `{"Password":"Commission-pass-123"}`)
	if status != 201 {
		t.Fatalf("accept=%d", status)
	}
	var aid string
	var pct int
	pool.QueryRow(contextBG(), "SELECT id,commission_pct FROM agents WHERE email=$1", email).Scan(&aid, &pct)
	if pct != 32 {
		t.Fatalf("accepted pct=%d", pct)
	}
	status, _, _ = opsJSON(t, r, "PATCH", "/0brand/api/admin/agents/invitations/"+id, `{"CommissionPct":41}`, admin)
	if status != 409 {
		t.Fatalf("already accepted edit=%d", status)
	}
	status, _, _ = opsJSON(t, r, "PATCH", "/0brand/api/admin/agents/"+aid, `{"CommissionPct":35}`, admin)
	if status != 200 {
		t.Fatalf("registered edit=%d", status)
	}
	var agentCookieValue *http.Cookie
	for _, c := range cookies {
		if c.Name == agentCookie {
			agentCookieValue = c
		}
	}
	if agentCookieValue == nil {
		t.Fatal("missing agent cookie")
	}
	status, info, _ = opsJSON(t, r, "GET", "/0brand/api/agent/me", "", agentCookieValue)
	if status != 200 || info["commission_pct"] != float64(35) {
		t.Fatalf("agent sees updated rate=%d/%v", status, info["commission_pct"])
	}
	req = httptest.NewRequest("GET", "/0brand/api/admin/agents/invitations", nil)
	req.AddCookie(admin)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != "[]" {
		t.Fatalf("registered email must leave pending list=%d %s", rec.Code, rec.Body.String())
	}
	var count int
	pool.QueryRow(contextBG(), "SELECT count(*) FROM audit_events WHERE object_id=$1 AND action='agent_commission_updated'", aid).Scan(&count)
	if count != 1 {
		t.Fatalf("commission audit=%d", count)
	}
}

func TestAgentCommissionValidationAndAdministratorOnly(t *testing.T) {
	email, _, id := commissionInvite(t, 40)
	r := buildRouter()
	admin := commercialCookie(adminCookie, "commission-admin", 1)
	for _, value := range []string{"0", "91", "-1", "1.5", "\"30\""} {
		status, _, _ := opsJSON(t, r, "POST", "/0brand/api/admin/agents/invite", `{"Email":"`+email+`","CommissionPct":`+value+`}`, admin)
		if status != 400 {
			t.Fatalf("invalid invite rate %s=%d", value, status)
		}
		status, _, _ = opsJSON(t, r, "PATCH", "/0brand/api/admin/agents/invitations/"+id, `{"CommissionPct":`+value+`}`, admin)
		if status != 400 {
			t.Fatalf("invalid pending rate %s=%d", value, status)
		}
	}
	status, _, _ := opsJSON(t, r, "PATCH", "/0brand/api/admin/agents/invitations/"+id, `{"CommissionPct":30}`)
	if status != 403 {
		t.Fatalf("anonymous edit=%d", status)
	}
	status, _, _ = opsJSON(t, r, "GET", "/0brand/api/admin/agents/invitations", "")
	if status != 403 {
		t.Fatalf("anonymous pending list=%d", status)
	}
}

func TestAgentCommissionChangePreservesOldOrdersAndLedger(t *testing.T) {
	email, token, _ := commissionInvite(t, 27)
	r := buildRouter()
	status, _, _ := opsJSON(t, r, "POST", "/0brand/api/agent/invitations/"+token+"/accept", `{"Password":"Commission-pass-123"}`)
	if status != 201 {
		t.Fatal(status)
	}
	var aid, code string
	pool.QueryRow(contextBG(), "SELECT id,code FROM agents WHERE email=$1", email).Scan(&aid, &code)
	oid := "AGCFG" + newID(12)
	t.Cleanup(func() {
		mustExec(t, "DELETE FROM agent_commissions WHERE order_id=$1", oid)
		mustExec(t, "DELETE FROM orders WHERE id=$1", oid)
	})
	mustExec(t, `INSERT INTO orders(id,code,amount_cents,channel,status,agent_id,agent_code,agent_pct) VALUES($1,'',1000,'native','paid',$2,$3,27)`, oid, aid, code)
	mustExec(t, `INSERT INTO agent_commissions(agent_id,order_id,order_amount_cents,commission_cents) VALUES($1,$2,1000,270)`, aid, oid)
	mustExec(t, "UPDATE agents SET total_earned_cents=270 WHERE id=$1", aid)
	status, _, _ = opsJSON(t, r, "PATCH", "/0brand/api/admin/agents/"+aid, `{"CommissionPct":35}`, commercialCookie(adminCookie, "commission-admin", 1))
	if status != 200 {
		t.Fatal(status)
	}
	var oldPct int
	var oldCommission, earned int64
	pool.QueryRow(contextBG(), "SELECT agent_pct FROM orders WHERE id=$1", oid).Scan(&oldPct)
	pool.QueryRow(contextBG(), "SELECT commission_cents FROM agent_commissions WHERE order_id=$1", oid).Scan(&oldCommission)
	pool.QueryRow(contextBG(), "SELECT total_earned_cents FROM agents WHERE id=$1", aid).Scan(&earned)
	if oldPct != 27 || oldCommission != 270 || earned != 270 {
		t.Fatalf("historical values changed %d/%d/%d", oldPct, oldCommission, earned)
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("POST", "/", nil)
	ctx.Request.AddCookie(&http.Cookie{Name: refCookie, Value: code})
	_, attributed, pct, err := attribution(ctx)
	if err != nil || attributed != aid || pct == nil || *pct != 35 {
		t.Fatal("new attribution does not use updated rate")
	}
}

func TestAgentCommissionAuditFailureRollsBack(t *testing.T) {
	email, token, id := commissionInvite(t, 27)
	r := buildRouter()
	admin := commercialCookie(adminCookie, "commission-admin", 1)
	pendingConstraint := "agent_pct_pending_" + strings.ToLower(newID(8))
	mustExec(t, fmt.Sprintf(`ALTER TABLE audit_events ADD CONSTRAINT %s CHECK(NOT(object_id='%s' AND action='agent_invitation_commission_updated'))`, pendingConstraint, id))
	t.Cleanup(func() { mustExec(t, "ALTER TABLE audit_events DROP CONSTRAINT IF EXISTS "+pendingConstraint) })
	status, _, _ := opsJSON(t, r, "PATCH", "/0brand/api/admin/agents/invitations/"+id, `{"CommissionPct":32}`, admin)
	if status != 503 {
		t.Fatalf("audit failure pending=%d", status)
	}
	var pct int
	pool.QueryRow(contextBG(), "SELECT commission_pct FROM agent_invitations WHERE id=$1", id).Scan(&pct)
	if pct != 27 {
		t.Fatal("pending update not rolled back")
	}
	mustExec(t, "ALTER TABLE audit_events DROP CONSTRAINT "+pendingConstraint)
	status, _, _ = opsJSON(t, r, "POST", "/0brand/api/agent/invitations/"+token+"/accept", `{"Password":"Commission-pass-123"}`)
	if status != 201 {
		t.Fatal(status)
	}
	var aid string
	pool.QueryRow(contextBG(), "SELECT id FROM agents WHERE email=$1", email).Scan(&aid)
	activeConstraint := "agent_pct_active_" + strings.ToLower(newID(8))
	mustExec(t, fmt.Sprintf(`ALTER TABLE audit_events ADD CONSTRAINT %s CHECK(NOT(object_id='%s' AND action='agent_commission_updated'))`, activeConstraint, aid))
	t.Cleanup(func() { mustExec(t, "ALTER TABLE audit_events DROP CONSTRAINT IF EXISTS "+activeConstraint) })
	status, _, _ = opsJSON(t, r, "PATCH", "/0brand/api/admin/agents/"+aid, `{"CommissionPct":35}`, admin)
	if status != 503 {
		t.Fatalf("audit failure active=%d", status)
	}
	pool.QueryRow(contextBG(), "SELECT commission_pct FROM agents WHERE id=$1", aid).Scan(&pct)
	if pct != 27 {
		t.Fatal("active update not rolled back")
	}
}

func TestAgentCommissionEditRacingAcceptanceIsConsistent(t *testing.T) {
	email, token, id := commissionInvite(t, 27)
	r := buildRouter()
	admin := commercialCookie(adminCookie, "commission-admin", 1)
	type result struct {
		kind   string
		status int
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	go func() {
		<-start
		status, _, _ := opsJSON(t, r, "PATCH", "/0brand/api/admin/agents/invitations/"+id, `{"CommissionPct":32}`, admin)
		results <- result{"edit", status}
	}()
	go func() {
		<-start
		status, _, _ := opsJSON(t, r, "POST", "/0brand/api/agent/invitations/"+token+"/accept", `{"Password":"Commission-pass-123"}`)
		results <- result{"accept", status}
	}()
	close(start)
	statuses := map[string]int{}
	for i := 0; i < 2; i++ {
		r := <-results
		statuses[r.kind] = r.status
	}
	if statuses["accept"] != 201 || (statuses["edit"] != 200 && statuses["edit"] != 409) {
		t.Fatalf("race statuses=%v", statuses)
	}
	var pct int
	pool.QueryRow(contextBG(), "SELECT commission_pct FROM agents WHERE email=$1", email).Scan(&pct)
	want := 27
	if statuses["edit"] == 200 {
		want = 32
	}
	if pct != want {
		t.Fatalf("race accepted pct=%d want=%d", pct, want)
	}
}
