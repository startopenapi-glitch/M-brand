package app

// Regression coverage for the 2026-10-21 delivery/pricing change:
// - the free item is a Logo lockup card, not a full system card
// - 17 paid items sum to ¥149; the all-items bundle is ¥99
// - payment cannot start until the free Logo card exists
import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReviewPricingAndLogoCardContract(t *testing.T) {
	var count int
	var sum int64
	if err := pool.QueryRow(contextBG(), "SELECT COUNT(*), COALESCE(SUM(price_cents),0) FROM item_pricing").Scan(&count, &sum); err != nil {
		t.Fatal(err)
	}
	if count != 17 || sum != 14900 {
		t.Fatalf("paid item pricing must be 17 items and 14900 cents, got %d/%d", count, sum)
	}
	if bundlePriceCents != 9900 {
		t.Fatalf("bundle must be 9900 cents, got %d", bundlePriceCents)
	}
	for _, spec := range itemSpecs(normalizeForm(BrandForm{"cn_name": "测试品牌", "industry": "测试", "en_name": "Test"})) {
		if spec.Kind != "system_card" {
			continue
		}
		if spec.Name != "Logo 组合卡" {
			t.Fatalf("free item name = %q", spec.Name)
		}
		if !strings.Contains(spec.Prompt, "Symbol") || !strings.Contains(spec.Prompt, "中文标准字") || !strings.Contains(spec.Prompt, "英文标准字") {
			t.Fatalf("Logo card prompt missing lockup parts: %s", spec.Prompt)
		}
		if strings.Contains(spec.Prompt, "3 种材质小样") || strings.Contains(spec.Prompt, "字体系统说明") || strings.Contains(spec.Prompt, "主色/辅助色色值") {
			t.Fatalf("Logo card still contains paid system-card content: %s", spec.Prompt)
		}
		return
	}
	t.Fatal("system_card spec not found")
}

func TestReviewPaidCheckoutRequiresLogoCard(t *testing.T) {
	auditPaymentConfig(t, true)
	jid, vid := auditPaymentJob(t)
	mustExec(t, "UPDATE jobs SET status='queued',stage='boards' WHERE id=$1", jid)

	body, _ := json.Marshal(map[string]any{"job_id": jid, "items": []string{"logo_h"}})
	req := httptest.NewRequest(http.MethodPost, "/0brand/api/pay/create", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: visitorCookie, Value: makeCookieValue(gatePayload{Code: vid, Expiry: time.Now().Add(time.Hour).Unix()})})
	w := httptest.NewRecorder()
	buildRouter().ServeHTTP(w, req)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"system_card_pending":true`) {
		t.Fatalf("checkout must wait for Logo card: HTTP %d %s", w.Code, w.Body)
	}

	commercialWritePNG(t, jid, "system_card.png", commercialPNG(t, 30))
	mustExec(t, "INSERT INTO job_images(job_id,kind,idx,file,tokens) VALUES($1,'system_card',0,$2,0)", jid, webPath(jid, "system_card.png"))
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/0brand/api/pay/create", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: visitorCookie, Value: makeCookieValue(gatePayload{Code: vid, Expiry: time.Now().Add(time.Hour).Unix()})})
	buildRouter().ServeHTTP(w, req)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"qr"`) {
		t.Fatalf("checkout after Logo card must return QR: HTTP %d %s", w.Code, w.Body)
	}
	var out struct{ ID string `json:"id"` }
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.ID == "" {
		t.Fatal("checkout response missing order id")
	}
	mustExec(t, "DELETE FROM order_items WHERE order_id=$1", out.ID)
	mustExec(t, "DELETE FROM orders WHERE id=$1", out.ID)
}
