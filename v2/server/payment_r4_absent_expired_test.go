package app

// Independent desired-behavior reproduction; not executed by this reviewer.
// Copy beside payment_r3_repro_test.go, run serially in local zerobrand_test.
// Uses only generated test keys and signed in-memory WeChat responses.
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPaymentR4AbsentExpiredRecovery(t *testing.T) {
	for _, tc := range []struct{ name, closeResult, wantStatus string }{
		{"verified_close", "closed", "closed"},
		{"close_timeout_then_closed", "lost_closed", "closed"},
		{"close_timeout_then_paid", "paid", "paid"},
		{"close_timeout_still_notpay", "notpay", "pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := auditPaymentConfig(t, false)
			oid, jid, vid := paymentR3Fixture(t, 45*time.Minute, false)
			aid, agentCode := auditPaymentAgent(t, 0)
			mustExec(t, "UPDATE orders SET agent_id=$2,agent_code=$3,agent_pct=40 WHERE id=$1", oid, aid, agentCode)
			var original string
			if err := pool.QueryRow(contextBG(), "SELECT checkout_request FROM orders WHERE id=$1", oid).Scan(&original); err != nil {
				t.Fatal(err)
			}
			exists, closed, paid := false, false, false
			var creates, closes, queries int
			wxHTTP = &http.Client{Transport: auditPaymentTransport(func(req *http.Request) (*http.Response, error) {
				switch {
				case req.Method == "GET":
					queries++
					if !strings.HasSuffix(req.URL.Path, "/"+oid) {
						return nil, fmt.Errorf("queried replacement order")
					}
					if !exists {
						return paymentR3SignedResponse(key, 404, `{"code":"ORDER_NOT_EXIST"}`), nil
					}
					state, txid := "NOTPAY", ""
					if closed {
						state = "CLOSED"
					}
					if paid {
						state = "SUCCESS"
						txid = "PAYMENT-R4-" + oid
					}
					body := fmt.Sprintf(`{"out_trade_no":%q,"trade_state":%q,"transaction_id":%q,"mchid":"audit-merchant","appid":"audit-app","amount":{"total":390,"currency":"CNY"}}`, oid, state, txid)
					return paymentR3SignedResponse(key, 200, body), nil
				case req.Method == "POST" && req.URL.Path == "/v3/pay/transactions/native":
					creates++
					raw, err := io.ReadAll(req.Body)
					if err != nil {
						return nil, err
					}
					if string(raw) != original {
						return nil, fmt.Errorf("original Native request snapshot changed")
					}
					exists = true
					// Official Native adjusts a too-early expiry to placement + 1 minute.
					// Success is valid here; do not assume an inevitable stale-expiry rejection.
					return paymentR3SignedResponse(key, 200, `{"code_url":"weixin://wxpay/bizpayurl?pr=r4-fresh"}`), nil
				case req.Method == "POST" && strings.HasSuffix(req.URL.Path, "/"+oid+"/close"):
					closes++
					if !exists {
						return nil, fmt.Errorf("fixture cannot close an absent order")
					}
					if tc.closeResult == "closed" {
						closed = true
						return paymentR3SignedResponse(key, 204, ""), nil
					}
					if tc.closeResult == "paid" {
						paid = true
					}
					if tc.closeResult == "lost_closed" {
						closed = true
					}
					return nil, context.DeadlineExceeded
				default:
					return nil, fmt.Errorf("unexpected channel request %s %s", req.Method, req.URL.Path)
				}
			})}
			rec := commerceRequest(t, "POST", "/0brand/api/pay/orders/"+oid+"/resume", vid, `{}`)
			var view map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
				t.Fatal(err)
			}
			status := orderStatus(t, oid)
			if creates != 1 || closes != 1 || status != tc.wantStatus || view["id"] != oid {
				t.Fatalf("recovery must finish from same-order channel facts: HTTP=%d queries=%d creates=%d closes=%d status=%s want=%s response_id=%v", rec.Code, queries, creates, closes, status, tc.wantStatus, view["id"])
			}
			var stored string
			var orders, commissions int
			var credited bool
			var earned, version int64
			if err := pool.QueryRow(contextBG(), "SELECT checkout_request,credited FROM orders WHERE id=$1", oid).Scan(&stored, &credited); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(contextBG(), "SELECT count(*) FROM orders WHERE job_id=$1", jid).Scan(&orders); err != nil {
				t.Fatal(err)
			}
			if stored != original || orders != 1 {
				t.Fatalf("recovery changed snapshot or created replacement: identical=%t orders=%d", stored == original, orders)
			}
			if status == "paid" {
				// Duplicate authenticated callback must not add rights/version/commission.
				if err := settleOrder(oid, "audit-merchant", "audit-app", 390, "PAYMENT-R4-"+oid); err != nil {
					t.Fatal(err)
				}
			}
			if err := pool.QueryRow(contextBG(), "SELECT count(*) FROM agent_commissions WHERE order_id=$1", oid).Scan(&commissions); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(contextBG(), "SELECT total_earned_cents FROM agents WHERE id=$1", aid).Scan(&earned); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(contextBG(), "SELECT delivery_version FROM jobs WHERE id=$1", jid).Scan(&version); err != nil {
				t.Fatal(err)
			}
			rights, err := jobEntitlements(contextBG(), pool, jid)
			if err != nil {
				t.Fatal(err)
			}
			wantPaid := tc.wantStatus == "paid"
			if credited != wantPaid || rights["logo_h"] != wantPaid {
				t.Fatalf("wrong rights outcome: credited=%t rights=%t want_paid=%t", credited, rights["logo_h"], wantPaid)
			}
			if wantPaid {
				if commissions != 1 || earned != 156 || version != 1 {
					t.Fatalf("paid replay duplicated accounting: commissions=%d earned=%d version=%d", commissions, earned, version)
				}
			} else if commissions != 0 || earned != 0 || version != 0 {
				t.Fatalf("unpaid recovery granted accounting: commissions=%d earned=%d version=%d", commissions, earned, version)
			}
		})
	}
}
