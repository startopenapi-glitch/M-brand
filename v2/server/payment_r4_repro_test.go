package app

// Real handlers and transactions, isolated zerobrand_test only. Channel replies
// are signed using an ephemeral test key; no external payment or worker calls.
import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A verified CLOSED result must not be discarded by a second read that times
// out. Otherwise a successfully closed channel order still blocks the customer.
func TestPaymentR4ResumeUsesVerifiedTerminalFact(t *testing.T) {
	for _, tc := range []struct{ channel, want string }{
		{"CLOSED", "closed"}, {"PAYERROR", "failed"}, {"REFUND", "failed"},
	} {
		t.Run(tc.channel, func(t *testing.T) {
			key := auditPaymentConfig(t, false)
			oid, _, vid := paymentR3Fixture(t, 45*time.Minute, true)
			var queries, writes atomic.Int32
			wxHTTP = &http.Client{Transport: auditPaymentTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method == "GET" && strings.Contains(req.URL.Path, oid) {
					if queries.Add(1) == 1 {
						body := fmt.Sprintf(`{"out_trade_no":%q,"trade_state":%q,"mchid":"audit-merchant","appid":"audit-app","amount":{"total":390,"currency":"CNY"}}`, oid, tc.channel)
						return paymentR3SignedResponse(key, 200, body), nil
					}
					return nil, context.DeadlineExceeded
				}
				writes.Add(1)
				return nil, fmt.Errorf("unexpected channel mutation")
			})}
			r := commerceRequest(t, "POST", "/0brand/api/pay/orders/"+oid+"/resume", vid, `{}`)
			status := orderStatus(t, oid)
			t.Logf("channel=%s resume=%d local=%s queries=%d mutations=%d", tc.channel, r.Code, status, queries.Load(), writes.Load())
			if r.Code != 200 || status != tc.want || queries.Load() != 1 || writes.Load() != 0 {
				t.Fatalf("verified terminal fact was discarded: want HTTP200 %s from one channel query with no mutation", tc.want)
			}
			var credited bool
			if err := pool.QueryRow(contextBG(), "SELECT credited FROM orders WHERE id=$1", oid).Scan(&credited); err != nil || credited {
				t.Fatalf("non-successful payment must not grant rights: credited=%v err=%v", credited, err)
			}
		})
	}
}

// Advance only the persisted lease clock while the fourth bounded channel call
// is outstanding. Four 10s calls can exceed 30s; another tab must still wait.
func TestPaymentR4SlowRecoveryLeaseCoversRemainingChannelRead(t *testing.T) {
	key := auditPaymentConfig(t, false)
	oid, _, vid := paymentR3Fixture(t, 45*time.Minute, false)
	reached, release := make(chan struct{}), make(chan struct{})
	var queries, creates, closes atomic.Int32
	wxHTTP = &http.Client{Transport: auditPaymentTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method == "GET" {
			n := queries.Add(1)
			if n == 1 {
				return paymentR3SignedResponse(key, 404, `{"code":"ORDER_NOT_EXIST"}`), nil
			}
			if n == 2 {
				close(reached)
				<-release
			}
			body := fmt.Sprintf(`{"out_trade_no":%q,"trade_state":"NOTPAY","mchid":"audit-merchant","appid":"audit-app","amount":{"total":390,"currency":"CNY"}}`, oid)
			return paymentR3SignedResponse(key, 200, body), nil
		}
		if req.URL.Path == "/v3/pay/transactions/native" {
			creates.Add(1)
			return paymentR3SignedResponse(key, 200, `{"code_url":"weixin://r4/lease"}`), nil
		}
		if strings.HasSuffix(req.URL.Path, "/"+oid+"/close") {
			closes.Add(1)
			return nil, context.DeadlineExceeded
		}
		return nil, fmt.Errorf("unexpected payment request")
	})}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- commerceRequest(t, "POST", "/0brand/api/pay/orders/"+oid+"/resume", vid, `{}`) }()
	select {
	case <-reached:
	case r := <-done:
		close(release)
		t.Fatalf("same-order recovery stopped before close reconciliation: HTTP=%d creates=%d closes=%d", r.Code, creates.Load(), closes.Load())
	case <-time.After(3 * time.Second):
		close(release)
		<-done
		t.Fatal("recovery did not reach fourth channel operation")
	}
	mustExec(t, "UPDATE orders SET checkout_lease_until=checkout_lease_until-interval '31 seconds' WHERE id=$1", oid)
	rateMu.Lock()
	delete(confirmAt, oid)
	rateMu.Unlock()
	second := commerceRequest(t, "POST", "/0brand/api/pay/orders/"+oid+"/resume", vid, `{}`)
	close(release)
	first := <-done
	if second.Code != 409 || queries.Load() != 2 || creates.Load() != 1 || closes.Load() != 1 || orderStatus(t, oid) != "pending" {
		t.Fatalf("late tab entered an active recovery: first=%d second=%d queries=%d creates=%d closes=%d", first.Code, second.Code, queries.Load(), creates.Load(), closes.Load())
	}
}

func TestPaymentR4ExpiredPaymentCloseAndConcurrentCallbacksSettleOnce(t *testing.T) {
	key := auditPaymentConfig(t, false)
	oid, jid, vid := paymentR3Fixture(t, 45*time.Minute, true)
	aid, code := auditPaymentAgent(t, 0)
	mustExec(t, "UPDATE orders SET agent_id=$2,agent_code=$3,agent_pct=40 WHERE id=$1", oid, aid, code)
	started, release := make(chan struct{}), make(chan struct{})
	var paid atomic.Bool
	var closes, creates atomic.Int32
	wxHTTP = &http.Client{Transport: auditPaymentTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method == "GET" {
			state, txid := "NOTPAY", ""
			if paid.Load() {
				state = "SUCCESS"
				txid = "R4-TX-" + oid
			}
			body := fmt.Sprintf(`{"out_trade_no":%q,"trade_state":%q,"transaction_id":%q,"mchid":"audit-merchant","appid":"audit-app","amount":{"total":390,"currency":"CNY"}}`, oid, state, txid)
			return paymentR3SignedResponse(key, 200, body), nil
		}
		if strings.HasSuffix(req.URL.Path, "/"+oid+"/close") {
			if closes.Add(1) == 1 {
				close(started)
				<-release
			}
			return paymentR3SignedResponse(key, 400, `{"code":"ORDERPAID"}`), nil
		}
		creates.Add(1)
		return nil, fmt.Errorf("unexpected new checkout")
	})}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- commerceRequest(t, "POST", "/0brand/api/pay/orders/"+oid+"/resume", vid, `{}`) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		<-done
		t.Fatal("close not reached")
	}
	paid.Store(true)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- settleOrder(oid, "audit-merchant", "audit-app", 390, "R4-TX-"+oid) }()
	}
	wg.Wait()
	close(errs)
	close(release)
	r := <-done
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	// Other tabs have the original selection and cannot buy it again.
	for i := 0; i < 4; i++ {
		r := commerceRequest(t, "POST", "/0brand/api/pay/create", vid, `{"job_id":"`+jid+`","items":["logo_h"]}`)
		if r.Code != 200 || !strings.Contains(r.Body.String(), `"already_purchased":true`) {
			t.Fatalf("paid selection remained payable: %d %s", r.Code, r.Body)
		}
	}
	var orders, commissions, deliveries int
	var earned, version int64
	var credited bool
	if err := pool.QueryRow(contextBG(), "SELECT count(*) FROM orders WHERE job_id=$1", jid).Scan(&orders); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(contextBG(), "SELECT credited FROM orders WHERE id=$1", oid).Scan(&credited); err != nil {
		t.Fatal(err)
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
	if err := pool.QueryRow(contextBG(), "SELECT count(*) FROM delivery_items WHERE job_id=$1 AND kind='logo_h'", jid).Scan(&deliveries); err != nil {
		t.Fatal(err)
	}
	if orderStatus(t, oid) != "paid" || !credited || orders != 1 || commissions != 1 || earned != 156 || version != 1 || deliveries != 1 || closes.Load() != 1 || creates.Load() != 0 {
		t.Fatalf("payment race violated one-order/one-delivery/one-commission: HTTP=%d orders=%d credited=%v commission=%d earned=%d version=%d deliveries=%d closes=%d creates=%d", r.Code, orders, credited, commissions, earned, version, deliveries, closes.Load(), creates.Load())
	}
}
