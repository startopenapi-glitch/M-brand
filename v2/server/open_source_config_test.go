package app

import (
	"net/http"
	"testing"
)

// A self-hosted installation must never send referrals or payment callbacks to
// the original service. Exercise the actual handlers/configuration boundary.
func TestMBrandReferralUsesDeploymentOrigin(t *testing.T) {
	t.Setenv("ZEROBRAND_PUBLIC_ORIGIN", "https://shop.example.test/")
	_, token := agentFlowFixture(t)
	r := buildRouter()
	status, _, cookies := opsJSON(t, r, "POST", "/0brand/api/agent/invitations/"+token+"/accept", `{"Password":"Self-hosted-test-123"}`)
	if status != 201 {
		t.Fatalf("accept=%d", status)
	}
	var session *http.Cookie
	for _, c := range cookies {
		if c.Name == agentCookie {
			session = c
		}
	}
	if session == nil {
		t.Fatal("agent session missing")
	}
	status, body, _ := opsJSON(t, r, "GET", "/0brand/api/agent/me", "", session)
	code, _ := body["code"].(string)
	if status != 200 || code == "" || body["link"] != "https://shop.example.test/?ref="+code {
		t.Fatalf("referral does not belong to this installation: status=%d link=%v", status, body["link"])
	}
}

func TestMBrandPaymentCallbackUsesDeploymentOrigin(t *testing.T) {
	t.Setenv("ZEROBRAND_PUBLIC_ORIGIN", "https://shop.example.test/")
	t.Setenv("WXPAY_NOTIFY_URL", "")
	loadWxConfig()
	if wxcfg.notifyURL != "https://shop.example.test/api/pay/notify" {
		t.Fatalf("unexpected callback %q", wxcfg.notifyURL)
	}
	t.Setenv("WXPAY_NOTIFY_URL", "https://callback.example.test/custom")
	loadWxConfig()
	if wxcfg.notifyURL != "https://callback.example.test/custom" {
		t.Fatal("explicit callback ignored")
	}
}
