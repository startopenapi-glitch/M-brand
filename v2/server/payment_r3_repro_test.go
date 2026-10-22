package app

// Reviewer-only desired-behavior regressions.
// Copy into an isolated source snapshot; run serially with the existing
// TestMain guard requiring local zerobrand_test. No workers are started.
// Every payment HTTP request uses a signed in-memory transport.
import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func paymentR3SignedResponse(key *rsa.PrivateKey, status int, body string) *http.Response {
	ts, nonce := fmt.Sprint(time.Now().Unix()), "payment-r3-nonce"
	h := make(http.Header)
	h.Set("Wechatpay-Serial", "audit-platform-serial")
	h.Set("Wechatpay-Timestamp", ts)
	h.Set("Wechatpay-Nonce", nonce)
	h.Set("Wechatpay-Signature", signWithPlatform(key, ts+"\n"+nonce+"\n"+body+"\n"))
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body))}
}

func paymentR3Fixture(t *testing.T, age time.Duration, withQR bool) (string, string, string) {
	t.Helper()
	jid, vid := auditPaymentJob(t)
	oid := auditPaymentOrder(t, jid, "items:logo_h", "", 390)
	mustExec(t, "INSERT INTO order_items(order_id,item_kind,item_name,price_cents) VALUES($1,'logo_h','fixture logo',390)", oid)
	var snapshot map[string]any
	if err := json.Unmarshal(wxNativeRequest(oid, 390, "0brand 1项"), &snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot["time_expire"] = time.Now().Add(-age + 30*time.Minute).Format(time.RFC3339)
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	qr := ""
	if withQR {
		qr = "weixin://wxpay/bizpayurl?pr=payment-r3-fixture"
	}
	mustExec(t, `UPDATE orders SET created_at=$2, updated_at=now(), code_url=$3,
checkout_started=true, checkout_lease_until=now()-interval '1 second',
checkout_request=$4 WHERE id=$1`, oid, time.Now().Add(-age), qr, string(raw))
	return oid, jid, vid
}

// Native time_expire is 30 minutes, whereas the current reconciler closes at
// two hours. Official Native docs state expiry prevents payment but does not
// mean the order is closed. Recovery must obtain a channel-confirmed terminal
// state before allowing another order; it must not simply return the old QR.
func TestPaymentR3ExpiredQRRecoveryNeedsChannelClose(t *testing.T) {
	key := auditPaymentConfig(t, false)
	oid, _, vid := paymentR3Fixture(t, 45*time.Minute, true)
	var queries, closes, nativeCreates int
	wxHTTP = &http.Client{Transport: auditPaymentTransport(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == "GET" && strings.Contains(req.URL.Path, oid):
			queries++
			body := fmt.Sprintf(`{"out_trade_no":%q,"trade_state":"NOTPAY","mchid":"audit-merchant","appid":"audit-app","amount":{"total":390,"currency":"CNY"}}`, oid)
			return paymentR3SignedResponse(key, 200, body), nil
		case req.Method == "POST" && strings.HasSuffix(req.URL.Path, "/"+oid+"/close"):
			closes++
			return paymentR3SignedResponse(key, 204, ""), nil
		case req.Method == "POST" && req.URL.Path == "/v3/pay/transactions/native":
			nativeCreates++
			return nil, fmt.Errorf("expired existing order must not be recreated")
		default:
			return nil, fmt.Errorf("unexpected payment request")
		}
	})}
	r := commerceRequest(t, "POST", "/0brand/api/pay/orders/"+oid+"/resume", vid, `{}`)
	status := orderStatus(t, oid)
	t.Logf("resume HTTP=%d status=%s query_calls=%d close_calls=%d create_calls=%d", r.Code, status, queries, closes, nativeCreates)
	if status != "closed" || queries == 0 || closes == 0 || nativeCreates != 0 {
		t.Fatalf("expired QR cannot be paid but still blocks checkout: HTTP=%d status=%s query=%d close=%d create=%d; want verified query + successful close without another Native create",
			r.Code, status, queries, closes, nativeCreates)
	}
}

func TestPaymentR3ExpiredCheckoutHidesUnpayableQR(t *testing.T) {
	auditPaymentConfig(t, false)
	oid, _, vid := paymentR3Fixture(t, 45*time.Minute, true)
	r := commerceRequest(t, "GET", "/0brand/api/pay/checkout/"+oid, vid, "")
	var view struct {
		QR, CodeURL, Message, Status string
	}
	if err := json.Unmarshal(r.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	json.Unmarshal(r.Body.Bytes(), &wire)
	if r.Code != 200 || view.Status != "pending" || view.QR != "" || wire["code_url"] != nil && wire["code_url"] != "" || view.Message == "" {
		t.Fatalf("unpayable QR should be hidden with recovery guidance, preserving pending: HTTP=%d qr_present=%v code_url_present=%v status=%s message_present=%v",
			r.Code, view.QR != "", wire["code_url"] != nil && wire["code_url"] != "", view.Status, view.Message != "")
	}
}

func TestPaymentR3CloseTimeoutPreservesPending(t *testing.T) {
	key := auditPaymentConfig(t, false)
	oid, _, vid := paymentR3Fixture(t, 45*time.Minute, true)
	var closes int
	wxHTTP = &http.Client{Transport: auditPaymentTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method == "GET" && strings.Contains(req.URL.Path, oid) {
			body := fmt.Sprintf(`{"out_trade_no":%q,"trade_state":"NOTPAY","mchid":"audit-merchant","appid":"audit-app","amount":{"total":390,"currency":"CNY"}}`, oid)
			return paymentR3SignedResponse(key, 200, body), nil
		}
		if req.Method == "POST" && strings.HasSuffix(req.URL.Path, "/"+oid+"/close") {
			closes++
			return nil, context.DeadlineExceeded
		}
		return nil, fmt.Errorf("unexpected payment request")
	})}
	r := commerceRequest(t, "POST", "/0brand/api/pay/orders/"+oid+"/resume", vid, `{}`)
	if closes != 1 || orderStatus(t, oid) != "pending" {
		t.Fatalf("ambiguous close must preserve pending: HTTP=%d closes=%d status=%s", r.Code, closes, orderStatus(t, oid))
	}
	var credited bool
	if err := pool.QueryRow(contextBG(), "SELECT credited FROM orders WHERE id=$1", oid).Scan(&credited); err != nil || credited {
		t.Fatalf("timeout must not grant rights: credited=%v query_error=%v", credited, err != nil)
	}
}

func TestPaymentR3ClosePaidRaceSettlesExactlyOnce(t *testing.T) {
	key := auditPaymentConfig(t, false)
	oid, jid, vid := paymentR3Fixture(t, 45*time.Minute, true)
	aid, code := auditPaymentAgent(t, 0)
	mustExec(t, "UPDATE orders SET agent_id=$2,agent_code=$3,agent_pct=40 WHERE id=$1", oid, aid, code)
	var queries, closes int
	wxHTTP = &http.Client{Transport: auditPaymentTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method == "GET" && strings.Contains(req.URL.Path, oid) {
			queries++
			state, txid := "NOTPAY", ""
			if closes != 0 {
				state, txid = "SUCCESS", "PAYMENT-R3-"+oid
			}
			body := fmt.Sprintf(`{"out_trade_no":%q,"trade_state":%q,"transaction_id":%q,"mchid":"audit-merchant","appid":"audit-app","amount":{"total":390,"currency":"CNY"}}`, oid, state, txid)
			return paymentR3SignedResponse(key, 200, body), nil
		}
		if req.Method == "POST" && strings.HasSuffix(req.URL.Path, "/"+oid+"/close") {
			closes++
			return paymentR3SignedResponse(key, 400, `{"code":"ORDERPAID","message":"order already paid"}`), nil
		}
		return nil, fmt.Errorf("unexpected payment request")
	})}
	r := commerceRequest(t, "POST", "/0brand/api/pay/orders/"+oid+"/resume", vid, `{}`)
	if orderStatus(t, oid) != "paid" || queries < 2 || closes != 1 {
		t.Fatalf("payment racing close must reconcile same order: HTTP=%d status=%s query=%d close=%d", r.Code, orderStatus(t, oid), queries, closes)
	}
	// Simulate duplicate authenticated callback after the recovery query settled it.
	if err := settleOrder(oid, "audit-merchant", "audit-app", 390, "PAYMENT-R3-"+oid); err != nil {
		t.Fatal(err)
	}
	var count int
	var earned, version int64
	if err := pool.QueryRow(contextBG(), "SELECT COUNT(*) FROM agent_commissions WHERE order_id=$1", oid).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(contextBG(), "SELECT total_earned_cents FROM agents WHERE id=$1", aid).Scan(&earned); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(contextBG(), "SELECT delivery_version FROM jobs WHERE id=$1", jid).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if count != 1 || earned != 156 || version != 1 {
		t.Fatalf("race duplicated settlement: commissions=%d earned=%d delivery_version=%d", count, earned, version)
	}
}

func TestPaymentR3ConcurrentRecoveryAndCreateKeepOneOrder(t *testing.T) {
	key := auditPaymentConfig(t, false)
	resetCreateLimiter()
	oid, jid, vid := paymentR3Fixture(t, 45*time.Minute, true)
	started, release := make(chan struct{}), make(chan struct{})
	var queries, closes, creates atomic.Int32
	wxHTTP = &http.Client{Transport: auditPaymentTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method == "GET" && strings.Contains(req.URL.Path, oid) {
			if queries.Add(1) == 1 {
				close(started)
				<-release
			}
			body := fmt.Sprintf(`{"out_trade_no":%q,"trade_state":"NOTPAY","mchid":"audit-merchant","appid":"audit-app","amount":{"total":390,"currency":"CNY"}}`, oid)
			return paymentR3SignedResponse(key, 200, body), nil
		}
		if req.Method == "POST" && strings.HasSuffix(req.URL.Path, "/"+oid+"/close") {
			closes.Add(1)
			return paymentR3SignedResponse(key, 204, ""), nil
		}
		if req.Method == "POST" && req.URL.Path == "/v3/pay/transactions/native" {
			creates.Add(1)
		}
		return nil, fmt.Errorf("unexpected payment request")
	})}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- commerceRequest(t, "POST", "/0brand/api/pay/orders/"+oid+"/resume", vid, `{}`) }()
	select {
	case <-started:
	case r := <-done:
		t.Fatalf("first recovery skipped channel query: HTTP=%d", r.Code)
	case <-time.After(3 * time.Second):
		close(release)
		<-done
		t.Fatal("recovery never reached channel transport")
	}
	// Ignore the process-local five-second throttle to exercise the persistent
	// cross-process lease: a second request must not race the first channel call.
	rateMu.Lock()
	delete(confirmAt, oid)
	rateMu.Unlock()
	second := commerceRequest(t, "POST", "/0brand/api/pay/orders/"+oid+"/resume", vid, `{}`)
	// Changed selections in another tab must still reuse the unresolved order.
	newAttempt := commerceRequest(t, "POST", "/0brand/api/pay/create", vid, `{"job_id":"`+jid+`","items":["logo_v"]}`)
	var view struct{ ID string }
	json.Unmarshal(newAttempt.Body.Bytes(), &view)
	close(release)
	first := <-done
	var orders int
	if err := pool.QueryRow(contextBG(), "SELECT COUNT(*) FROM orders WHERE job_id=$1", jid).Scan(&orders); err != nil {
		t.Fatal(err)
	}
	if second.Code != 409 || view.ID != oid || orders != 1 || closes.Load() != 1 || creates.Load() != 0 || orderStatus(t, oid) != "closed" {
		t.Fatalf("recovery/create concurrency violated: first=%d second=%d create=%d same_order=%v rows=%d close=%d native=%d status=%s",
			first.Code, second.Code, newAttempt.Code, view.ID == oid, orders, closes.Load(), creates.Load(), orderStatus(t, oid))
	}
}

func TestPaymentR3UntrustedQueryCannotCloseOrRecreate(t *testing.T) {
	for _, name := range []string{"unsigned", "wrong_amount", "wrong_merchant", "wrong_app", "wrong_order", "wrong_currency"} {
		t.Run(name, func(t *testing.T) {
			key := auditPaymentConfig(t, false)
			oid, _, vid := paymentR3Fixture(t, 45*time.Minute, true)
			var queries, closes, nativeCreates int
			responseID, merchant, app, currency, amount := oid, "audit-merchant", "audit-app", "CNY", 390
			switch name {
			case "wrong_amount":
				amount++
			case "wrong_merchant":
				merchant = "another-merchant"
			case "wrong_app":
				app = "another-app"
			case "wrong_order":
				responseID = "another-order"
			case "wrong_currency":
				currency = "USD"
			}
			wxHTTP = &http.Client{Transport: auditPaymentTransport(func(req *http.Request) (*http.Response, error) {
				switch {
				case req.Method == "GET":
					queries++
					body := fmt.Sprintf(`{"out_trade_no":%q,"trade_state":"NOTPAY","mchid":%q,"appid":%q,"amount":{"total":%d,"currency":%q}}`, responseID, merchant, app, amount, currency)
					r := paymentR3SignedResponse(key, 200, body)
					if name == "unsigned" {
						r.Header = make(http.Header)
					}
					return r, nil
				case strings.HasSuffix(req.URL.Path, "/close"):
					closes++
				case req.URL.Path == "/v3/pay/transactions/native":
					nativeCreates++
				}
				return nil, fmt.Errorf("unexpected channel mutation")
			})}
			r := commerceRequest(t, "POST", "/0brand/api/pay/orders/"+oid+"/resume", vid, `{}`)
			if r.Code < 400 || queries != 1 || closes != 0 || nativeCreates != 0 || orderStatus(t, oid) != "pending" {
				t.Fatalf("untrusted channel fact changed order: HTTP=%d query=%d close=%d native=%d status=%s", r.Code, queries, closes, nativeCreates, orderStatus(t, oid))
			}
		})
	}
}

func TestPaymentR3HistoricalMissingSnapshotNeverInfersExpiry(t *testing.T) {
	for _, withQR := range []bool{false, true} {
		t.Run(fmt.Sprintf("with_qr_%t", withQR), func(t *testing.T) {
			key := auditPaymentConfig(t, false)
			oid, _, vid := paymentR3Fixture(t, 3*time.Hour, withQR)
			mustExec(t, "UPDATE orders SET checkout_request='' WHERE id=$1", oid)
			var closes, nativeCreates int
			wxHTTP = &http.Client{Transport: auditPaymentTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method == "GET" {
					body := fmt.Sprintf(`{"out_trade_no":%q,"trade_state":"NOTPAY","mchid":"audit-merchant","appid":"audit-app","amount":{"total":390,"currency":"CNY"}}`, oid)
					return paymentR3SignedResponse(key, 200, body), nil
				}
				if strings.HasSuffix(req.URL.Path, "/close") {
					closes++
				} else if req.URL.Path == "/v3/pay/transactions/native" {
					nativeCreates++
				}
				return nil, fmt.Errorf("historical snapshot must not be invented")
			})}
			r := commerceRequest(t, "POST", "/0brand/api/pay/orders/"+oid+"/resume", vid, `{}`)
			if orderStatus(t, oid) != "pending" || closes != 0 || nativeCreates != 0 {
				t.Fatalf("historical order was inferred from age: HTTP=%d status=%s close=%d native=%d", r.Code, orderStatus(t, oid), closes, nativeCreates)
			}
			if !withQR && r.Code != 409 {
				t.Fatalf("missing original request should require manual reconciliation: HTTP=%d", r.Code)
			}
			view := commerceRequest(t, "GET", "/0brand/api/pay/checkout/"+oid, vid, "")
			var body map[string]any
			json.Unmarshal(view.Body.Bytes(), &body)
			if withQR && body["qr"] == nil {
				t.Fatal("unknown historic expiry was treated as confirmed expiry")
			}
		})
	}
}

func TestPaymentR3UnexpiredQRRemainsUsable(t *testing.T) {
	auditPaymentConfig(t, false)
	oid, _, vid := paymentR3Fixture(t, 5*time.Minute, true)
	var requests int
	wxHTTP = &http.Client{Transport: auditPaymentTransport(func(req *http.Request) (*http.Response, error) {
		requests++
		return nil, fmt.Errorf("existing unexpired QR should be reused")
	})}
	for _, endpoint := range []struct{ method, path string }{
		{"GET", "/0brand/api/pay/checkout/" + oid},
		{"POST", "/0brand/api/pay/orders/" + oid + "/resume"},
	} {
		r := commerceRequest(t, endpoint.method, endpoint.path, vid, `{}`)
		var view map[string]any
		json.Unmarshal(r.Body.Bytes(), &view)
		if r.Code != 200 || view["qr"] == nil || view["code_url"] == nil || view["status"] != "pending" {
			t.Fatalf("unexpired QR was hidden: HTTP=%d qr=%v code_url=%v status=%v", r.Code, view["qr"] != nil, view["code_url"] != nil, view["status"])
		}
	}
	if requests != 0 {
		t.Fatalf("unexpired QR unnecessarily sent %d channel requests", requests)
	}
}

func TestPaymentR3TerminalOrdersNeverExposeQR(t *testing.T) {
	for _, state := range []string{"paid", "closed", "failed"} {
		t.Run(state, func(t *testing.T) {
			auditPaymentConfig(t, false)
			oid, _, vid := paymentR3Fixture(t, 5*time.Minute, true)
			mustExec(t, "UPDATE orders SET status=$2 WHERE id=$1", oid, state)
			r := commerceRequest(t, "GET", "/0brand/api/pay/checkout/"+oid, vid, "")
			var view map[string]any
			json.Unmarshal(r.Body.Bytes(), &view)
			if r.Code != 200 || view["qr"] != nil || view["code_url"] != nil || view["h5_url"] != nil || view["status"] != state {
				t.Fatalf("terminal order still exposes payment entry: HTTP=%d status=%v qr=%v code_url=%v h5=%v", r.Code, view["status"], view["qr"] != nil, view["code_url"] != nil, view["h5_url"] != nil)
			}
		})
	}
}

// A query outage must not panic an unguarded background goroutine and terminate
// the process. The disposable closed pool uses the already-validated test config;
// the original pool is restored immediately and never closed or modified.
func TestPaymentR3ReconcilerHandlesDatabaseUnavailable(t *testing.T) {
	original := pool
	disposable, err := pgxpool.NewWithConfig(context.Background(), original.Config())
	if err != nil {
		t.Fatal("could not create disposable isolated pool")
	}
	disposable.Close()
	pool = disposable
	defer func() {
		pool = original
		if p := recover(); p != nil {
			t.Errorf("reconcileOnce panicked on unavailable database: %v", p)
		}
	}()
	reconcileOnce()
}
