package app

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentPayoutMinimumCooldownApproval(t *testing.T) {
	commercialRequireTestDB(t)
	aid := "agp-" + newID(8)
	code := "AG" + strings.ToUpper(newID(6))
	mustExec(t, `INSERT INTO agents(id,name,email,password_hash,code,commission_pct,total_earned_cents) VALUES($1,'测试代理',$2,'x',$3,40,20000)`, aid, aid+"@example.test", code)
	oid := "AGORD" + newID(8)
	mustExec(t, `INSERT INTO agent_commissions(agent_id,order_id,order_amount_cents,commission_cents) VALUES($1,$2,20000,20000)`, aid, oid)
	t.Cleanup(func() {
		mustExec(t, "DELETE FROM agent_payout_allocations WHERE payout_id IN (SELECT id FROM agent_payouts WHERE agent_id=$1)", aid)
		mustExec(t, "DELETE FROM agent_payouts WHERE agent_id=$1", aid)
		mustExec(t, "DELETE FROM agent_commissions WHERE agent_id=$1", aid)
		mustExec(t, "DELETE FROM agents WHERE id=$1", aid)
	})
	r := buildRouter()
	agent := commercialCookie(agentCookie, aid, 0)
	admin := commercialCookie(adminCookie, "fixture-admin", 1)
	codeResp, body, _ := opsJSON(t, r, "PUT", "/0brand/api/agent/me/payout-account", `{"Method":"alipay","Account":"wx-test-1234","Name":"测试代理"}`, agent)
	if codeResp != 200 {
		t.Fatalf("account=%d %v", codeResp, body)
	}
	codeResp, body, _ = opsJSON(t, r, "POST", "/0brand/api/agent/me/payouts", `{"AmountCents":5000,"RequestKey":"REQ-`+strings.ToUpper(newID(20))+`"}`, agent)
	if codeResp != 400 {
		t.Fatalf("minimum must reject got %d %v", codeResp, body)
	}
	key := "REQ-" + strings.ToUpper(newID(20))
	codeResp, body, _ = opsJSON(t, r, "POST", "/0brand/api/agent/me/payouts", `{"AmountCents":10000,"RequestKey":"`+key+`"}`, agent)
	if codeResp != 201 {
		t.Fatalf("create=%d %v", codeResp, body)
	}
	codeResp, body, _ = opsJSON(t, r, "POST", "/0brand/api/agent/me/payouts", `{"AmountCents":10000,"RequestKey":"REQ-`+strings.ToUpper(newID(20))+`"}`, agent)
	if codeResp != 429 {
		t.Fatalf("cooldown must reject got %d %v", codeResp, body)
	}
	var id int
	pool.QueryRow(contextBG(), "SELECT id FROM agent_payouts WHERE agent_id=$1 AND request_key=$2", aid, key).Scan(&id)
	if id == 0 {
		t.Fatal("payout missing")
	}
	codeResp, body, _ = opsJSON(t, r, "POST", `/0brand/api/admin/agent-payouts/`+itoa(id)+`/review`, `{"Action":"approve"}`, admin)
	if codeResp != 200 {
		t.Fatalf("approve=%d %v", codeResp, body)
	}
	codeResp, body, _ = opsJSON(t, r, "POST", `/0brand/api/admin/agent-payouts/`+itoa(id)+`/review`, `{"Action":"paid","Reference":"TEST-TX"}`, admin)
	if codeResp != 200 {
		t.Fatalf("paid=%d %v", codeResp, body)
	}
	var paid int64
	pool.QueryRow(contextBG(), "SELECT total_paid_cents FROM agents WHERE id=$1", aid).Scan(&paid)
	if paid != 10000 {
		t.Fatalf("paid=%d", paid)
	}
	req := httptest.NewRequest("GET", "/0brand/api/agent/me/statement.csv", nil)
	req.AddCookie(agent)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "佣金") {
		t.Fatalf("statement=%d %s", rec.Code, rec.Body.String())
	}
}

func TestAgentEmailInvitationAccept(t *testing.T) {
	commercialRequireTestDB(t)
	email := "invite-" + newID(8) + "@example.test"
	admin := commercialCookie(adminCookie, "fixture-admin", 1)
	r := buildRouter()
	code, body, _ := opsJSON(t, r, "POST", "/0brand/api/admin/agents/invite", `{"Email":"`+email+`"}`, admin)
	if code != 201 {
		t.Fatalf("invite=%d %v", code, body)
	}
	link, _ := body["invite_url"].(string)
	if link == "" {
		t.Fatal("missing fallback invite_url")
	}
	token := link[strings.LastIndex(link, "/")+1:]
	var aid string
	t.Cleanup(func() {
		mustExec(t, "DELETE FROM agent_invitations WHERE lower(email)=$1", email)
		pool.QueryRow(contextBG(), "SELECT id FROM agents WHERE lower(email)=$1", email).Scan(&aid)
		if aid != "" {
			mustExec(t, "DELETE FROM agents WHERE id=$1", aid)
		}
	})
	code, body, _ = opsJSON(t, r, "GET", "/0brand/api/agent/invitations/"+token, "")
	if code != 200 {
		t.Fatalf("invite info=%d %v", code, body)
	}
	code, body, cookies := opsJSON(t, r, "POST", "/0brand/api/agent/invitations/"+token+"/accept", `{"Password":"agent-pass-123"}`)
	if code != 201 {
		t.Fatalf("accept=%d %v", code, body)
	}
	if len(cookies) == 0 {
		t.Fatal("missing agent session cookie")
	}
	var n int
	pool.QueryRow(contextBG(), "SELECT COUNT(*) FROM agents WHERE lower(email)=$1", email).Scan(&n)
	if n != 1 {
		t.Fatalf("agent count=%d", n)
	}
}
