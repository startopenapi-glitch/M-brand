package app

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func agentFlowFixture(t *testing.T) (string, string) {
	t.Helper()
	commercialRequireTestDB(t)
	for _, key := range []string{"ZEROBRAND_SMTP_FROM", "RESEND_FROM", "SMTP_FROM", "ZEROBRAND_RESEND_API_KEY", "RESEND_API_KEY"} {
		t.Setenv(key, "")
	}
	email := "agent-flow-" + newID(12) + "@example.test"
	t.Cleanup(func() {
		mustExec(t, "DELETE FROM audit_events WHERE object_id IN (SELECT id FROM agents WHERE email=$1) OR object_id IN (SELECT id FROM agent_invitations WHERE email=$1)", email)
		mustExec(t, "DELETE FROM agent_invitations WHERE email=$1", email)
		mustExec(t, "DELETE FROM agents WHERE email=$1", email)
	})
	status, body, _ := opsJSON(t, buildRouter(), "POST", "/0brand/api/admin/agents/invite", `{"Email":"`+email+`"}`, commercialCookie(adminCookie, "agent-flow-fixture", 1))
	if status != 201 {
		t.Fatalf("invite status=%d", status)
	}
	link, _ := body["invite_url"].(string)
	if link == "" {
		t.Fatal("missing local fallback link")
	}
	return email, link[strings.LastIndex(link, "/")+1:]
}

func TestAgentOnboardingLoginLogoutAndOneTimeLink(t *testing.T) {
	email, token := agentFlowFixture(t)
	client, base := commercialServer(t)
	status, _, _ := opsJSON(t, buildRouter(), "POST", "/0brand/api/agent/invitations/"+token+"/accept", `{"Password":"short"}`)
	if status != 400 {
		t.Fatalf("short password=%d", status)
	}
	status, body := postJSONv2(t, client, base+"/api/agent/invitations/"+token+"/accept", `{"Password":"Agent-flow-pass-123"}`)
	if status != 201 || body["ok"] != true {
		t.Fatalf("accept=%d", status)
	}
	status, _ = commercialRequest(t, client, "GET", base+"/api/agent/me", "")
	if status != 200 {
		t.Fatalf("new session=%d", status)
	}
	parsed, _ := url.Parse(base)
	oldCookies := client.Jar.Cookies(parsed)
	status, _ = commercialRequest(t, client, "GET", base+"/api/agent/invitations/"+token, "")
	if status != 404 {
		t.Fatalf("used link=%d", status)
	}
	status, _ = commercialRequest(t, client, "POST", base+"/api/agent/logout", `{}`)
	if status != 200 {
		t.Fatalf("logout=%d", status)
	}
	bare := &http.Client{Transport: client.Transport, Timeout: 10 * time.Second}
	for _, cookie := range oldCookies {
		if cookie.Name == agentCookie {
			status, _ = commercialRequest(t, bare, "GET", base+"/api/agent/me", "", cookie)
			if status != 401 {
				t.Fatalf("replayed logged out cookie=%d", status)
			}
		}
	}
	status, _ = postJSONv2(t, client, base+"/api/agent/login", `{"Email":"`+email+`","Password":"wrong-password"}`)
	if status != 401 {
		t.Fatalf("wrong password=%d", status)
	}
	status, _ = postJSONv2(t, client, base+"/api/agent/login", `{"Email":"`+strings.ToUpper(email)+`","Password":"Agent-flow-pass-123"}`)
	if status != 200 {
		t.Fatalf("fresh login=%d", status)
	}
	var count int
	pool.QueryRow(contextBG(), "SELECT count(*) FROM agents WHERE email=$1", email).Scan(&count)
	if count != 1 {
		t.Fatalf("accounts=%d", count)
	}
	status, _, _ = opsJSON(t, buildRouter(), "POST", "/0brand/api/admin/agents/invite", `{"Email":"`+email+`"}`, commercialCookie(adminCookie, "agent-flow-fixture", 1))
	if status != 409 {
		t.Fatalf("reinvite registered agent=%d", status)
	}
}

func TestAgentOnboardingExpiredInvitation(t *testing.T) {
	email, token := agentFlowFixture(t)
	mustExec(t, "UPDATE agent_invitations SET expires_at=now()-interval '1 second' WHERE email=$1", email)
	for _, method := range []string{"GET", "POST"} {
		path := "/0brand/api/agent/invitations/" + token
		if method == "POST" {
			path += "/accept"
		}
		status, _, _ := opsJSON(t, buildRouter(), method, path, `{"Password":"Agent-flow-pass-123"}`)
		if status != 404 {
			t.Fatalf("expired %s=%d", method, status)
		}
	}
	var count int
	pool.QueryRow(contextBG(), "SELECT count(*) FROM agents WHERE email=$1", email).Scan(&count)
	if count != 0 {
		t.Fatal("expired invitation created account")
	}
}

func TestAgentOnboardingConcurrentAcceptCreatesOneAccount(t *testing.T) {
	email, token := agentFlowFixture(t)
	router := buildRouter()
	statuses := make(chan int, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, _, _ := opsJSON(t, router, "POST", "/0brand/api/agent/invitations/"+token+"/accept", `{"Password":"Agent-flow-pass-123"}`)
			statuses <- status
		}()
	}
	wg.Wait()
	close(statuses)
	counts := map[int]int{}
	for s := range statuses {
		counts[s]++
	}
	if counts[201] != 1 || counts[404] != 1 {
		t.Fatalf("concurrent status counts=%v", counts)
	}
	var count int
	pool.QueryRow(contextBG(), "SELECT count(*) FROM agents WHERE email=$1", email).Scan(&count)
	if count != 1 {
		t.Fatalf("concurrent accounts=%d", count)
	}
}

func TestAgentOnboardingOversizedPasswordDoesNotConsumeInvite(t *testing.T) {
	for _, password := range []string{strings.Repeat("a", 73), strings.Repeat("测", 25)} {
		t.Run(strconv.Itoa(len(password)), func(t *testing.T) {
			email, token := agentFlowFixture(t)
			payload, _ := json.Marshal(map[string]string{"Password": password})
			status, _, _ := opsJSON(t, buildRouter(), "POST", "/0brand/api/agent/invitations/"+token+"/accept", string(payload))
			if status != 400 {
				t.Fatalf("oversized password must reject before account creation; got %d", status)
			}
			var count int
			pool.QueryRow(contextBG(), "SELECT count(*) FROM agents WHERE email=$1", email).Scan(&count)
			if count != 0 {
				t.Fatal("oversized password created unusable account")
			}
			status, _, _ = opsJSON(t, buildRouter(), "GET", "/0brand/api/agent/invitations/"+token, "")
			if status != 200 {
				t.Fatalf("rejected password consumed invite=%d", status)
			}
		})
	}
}

func TestAgentOnboardingSendsUsableLinkThroughSMTP(t *testing.T) {
	commercialRequireTestDB(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	messages := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(10 * time.Second))
		p := textproto.NewConn(conn)
		p.PrintfLine("220 localhost ESMTP")
		for {
			line, err := p.ReadLine()
			if err != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				fmt.Fprint(conn, "250-localhost\r\n250 AUTH PLAIN\r\n")
			case strings.HasPrefix(line, "AUTH"):
				p.PrintfLine("235 authenticated")
			case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"), strings.HasPrefix(line, "RSET"):
				p.PrintfLine("250 accepted")
			case line == "DATA":
				p.PrintfLine("354 continue")
				raw, err := p.ReadDotBytes()
				if err != nil {
					return
				}
				messages <- string(raw)
				p.PrintfLine("250 queued")
			case line == "QUIT":
				p.PrintfLine("221 bye")
				return
			default:
				p.PrintfLine("500 unsupported")
			}
		}
	}()
	email, _ := agentFlowFixture(t)
	// First fixture invitation used the non-sending fallback; this one exercises real SMTP.
	t.Setenv("ZEROBRAND_SMTP_FROM", "invites@example.test")
	t.Setenv("ZEROBRAND_SMTP_HOST", "127.0.0.1")
	t.Setenv("ZEROBRAND_SMTP_PORT", strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	t.Setenv("ZEROBRAND_SMTP_USER", "fixture")
	t.Setenv("ZEROBRAND_SMTP_PASS", "fixture-only")
	t.Setenv("ZEROBRAND_SMTP_TLS", "")
	status, body, _ := opsJSON(t, buildRouter(), "POST", "/0brand/api/admin/agents/invite", `{"Email":"`+email+`","CommissionPct":27}`, commercialCookie(adminCookie, "agent-flow-fixture", 1))
	if status != 201 || body["email_sent"] != true {
		t.Fatalf("SMTP invite status=%d sent=%v", status, body["email_sent"])
	}
	var message string
	select {
	case message = <-messages:
	case <-time.After(10 * time.Second):
		t.Fatal("no SMTP message captured")
	}
	if !strings.Contains(message,"佣金比例：27%") { t.Fatal("mail does not show configured commission") }
	marker := "/agent/invite/"
	offset := strings.Index(message, marker)
	if offset < 0 {
		t.Fatal("message lacks invitation link")
	}
	token := strings.Fields(message[offset+len(marker):])[0]
	client, base := commercialServer(t)
	status, _ = postJSONv2(t, client, base+"/api/agent/invitations/"+token+"/accept", `{"Password":"Agent-flow-pass-123"}`)
	if status != 201 {
		t.Fatalf("mailed link cannot activate=%d", status)
	}
	status, _ = commercialRequest(t, client, "GET", base+"/api/agent/me", "")
	if status != 200 {
		t.Fatalf("mailed link session=%d", status)
	}
}

func TestAgentOnboardingMaximumPasswordCanLogIn(t *testing.T) {
	for _, password := range []string{strings.Repeat("a", 72), strings.Repeat("测", 24)} {
		t.Run(fmt.Sprintf("%d-bytes-unicode=%t", len(password), strings.HasPrefix(password, "测")), func(t *testing.T) {
			email, token := agentFlowFixture(t)
			client, base := commercialServer(t)
			payload, _ := json.Marshal(map[string]string{"Password": password})
			status, _ := postJSONv2(t, client, base+"/api/agent/invitations/"+token+"/accept", string(payload))
			if status != 201 {
				t.Fatalf("maximum password accept=%d", status)
			}
			status, _ = commercialRequest(t, client, "POST", base+"/api/agent/logout", `{}`)
			if status != 200 {
				t.Fatalf("logout=%d", status)
			}
			login, _ := json.Marshal(map[string]string{"Email": email, "Password": password})
			status, _ = postJSONv2(t, client, base+"/api/agent/login", string(login))
			if status != 200 {
				t.Fatalf("maximum password login=%d", status)
			}
		})
	}
}
