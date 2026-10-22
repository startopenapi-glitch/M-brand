package app

// Every test owns a schema in zerobrand_test. Fresh subprocesses run only this
// test helper, never main(), real workers, production payments, or generation.
import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func paymentR5Schema(t *testing.T) {
	t.Helper()
	original := pool
	var db string
	if err := original.QueryRow(contextBG(), "SELECT current_database()").Scan(&db); err != nil || db != "zerobrand_test" {
		t.Fatal("isolated test database required")
	}
	schema := "payment_r5_" + randHex(6)
	if _, err := original.Exec(contextBG(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	cfg := original.Config()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	p, err := pgxpool.NewWithConfig(contextBG(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	pool = p
	t.Cleanup(func() {
		pool = original
		p.Close()
		if _, err := original.Exec(contextBG(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	if err := migrateDB(); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("ZEROBRAND_PG_URL"))
	if err != nil {
		t.Fatal("invalid test URL")
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	t.Setenv("ZEROBRAND_PG_URL", u.String())
}

func paymentR5Once(t *testing.T, oid, jid, aid string) {
	t.Helper()
	var orders, rights, commissions int
	var earned, version int64
	var credited bool
	if err := pool.QueryRow(contextBG(), `SELECT count(*) FROM orders WHERE job_id=$1`, jid).Scan(&orders); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(contextBG(), `SELECT credited FROM orders WHERE id=$1`, oid).Scan(&credited); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(contextBG(), `SELECT count(*) FROM delivery_items WHERE job_id=$1 AND kind='logo_h'`, jid).Scan(&rights); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(contextBG(), `SELECT delivery_version FROM jobs WHERE id=$1`, jid).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(contextBG(), `SELECT count(*) FROM agent_commissions WHERE order_id=$1`, oid).Scan(&commissions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(contextBG(), `SELECT total_earned_cents FROM agents WHERE id=$1`, aid).Scan(&earned); err != nil {
		t.Fatal(err)
	}
	t.Logf("orders=%d credited=%v rights=%d version=%d commissions=%d earned=%d", orders, credited, rights, version, commissions, earned)
	if orders != 1 || !credited || rights != 1 || version != 1 || commissions != 1 || earned != 156 || orderStatus(t, oid) != "paid" {
		t.Fatal("one payment / one entitlement / one 40% commission invariant violated")
	}
}

func paymentR5Child(t *testing.T, mode, oid, vid string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestPaymentR5ProcessHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "R5_CHILD_MODE="+mode, "R5_ORDER="+oid, "R5_VISITOR="+vid)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fresh recovery process failed: %s", out)
	}
	t.Logf("fresh process %s: %s", mode, strings.TrimSpace(string(out)))
}

func TestPaymentR5RestartReconcilesOldExpiredExactlyOnce(t *testing.T) {
	paymentR5Schema(t)
	auditPaymentConfig(t, false)
	oid, jid, _ := paymentR3Fixture(t, 48*time.Hour, true)
	aid, code := auditPaymentAgent(t, 0)
	mustExec(t, `UPDATE orders SET status='expired',updated_at=now()-interval '2 days',agent_id=$2,agent_code=$3,agent_pct=40 WHERE id=$1`, oid, aid, code)
	paymentR5Child(t, "reconcile-paid", oid, "")
	if orderStatus(t, oid) != "paid" {
		t.Fatal("fresh reconciler skipped >24h expired payment; paid rights never recovered")
	}
	paymentR5Child(t, "reconcile-paid", oid, "")
	paymentR5Child(t, "query-paid", oid, "")
	paymentR5Once(t, oid, jid, aid)
}

func TestPaymentR5BackgroundHonorsPersistedRecoveryLease(t *testing.T) {
	paymentR5Schema(t)
	key := auditPaymentConfig(t, false)
	oid, _, _ := paymentR3Fixture(t, 3*time.Hour, true)
	mustExec(t, `UPDATE orders SET checkout_lease_until=now()+interval '1 minute' WHERE id=$1`, oid)
	var closes int
	wxHTTP = &http.Client{Transport: auditPaymentTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method == "GET" {
			return paymentR3SignedResponse(key, 200, fmt.Sprintf(`{"out_trade_no":%q,"trade_state":"NOTPAY","mchid":"audit-merchant","appid":"audit-app","amount":{"total":390,"currency":"CNY"}}`, oid)), nil
		}
		closes++
		return nil, context.DeadlineExceeded
	})}
	reconcileOnce()
	if closes != 0 {
		t.Fatalf("background ignored active persisted manual lease: close_calls=%d", closes)
	}
	mustExec(t, `UPDATE orders SET checkout_lease_until=now()-interval '1 second' WHERE id=$1`, oid)
	reconcileOnce()
	if closes != 1 || orderStatus(t, oid) != "pending" {
		t.Fatalf("expired lease must recover once without fabricated closure: close=%d", closes)
	}
}

func TestPaymentR5BackgroundRejectsUnverifiedOrder(t *testing.T) {
	for _, bad := range []string{"wrong_amount", "unsigned", "timeout"} {
		t.Run(bad, func(t *testing.T) {
			paymentR5Schema(t)
			key := auditPaymentConfig(t, false)
			oid, _, _ := paymentR3Fixture(t, 3*time.Hour, true)
			var writes int
			wxHTTP = &http.Client{Transport: auditPaymentTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method == "GET" {
					if bad == "timeout" {
						return nil, context.DeadlineExceeded
					}
					amount := 390
					if bad == "wrong_amount" {
						amount++
					}
					r := paymentR3SignedResponse(key, 200, fmt.Sprintf(`{"out_trade_no":%q,"trade_state":"NOTPAY","mchid":"audit-merchant","appid":"audit-app","amount":{"total":%d,"currency":"CNY"}}`, oid, amount))
					if bad == "unsigned" {
						r.Header = make(http.Header)
					}
					return r, nil
				}
				writes++
				return paymentR3SignedResponse(key, 204, ""), nil
			})}
			reconcileOnce()
			if writes != 0 || orderStatus(t, oid) != "pending" {
				t.Fatalf("unverified %s caused channel mutation: writes=%d status=%s", bad, writes, orderStatus(t, oid))
			}
		})
	}
}

func TestPaymentR5ConsecutiveCloseTimeoutsRecoverPaidOnce(t *testing.T) {
	paymentR5Schema(t)
	key := auditPaymentConfig(t, false)
	oid, jid, vid := paymentR3Fixture(t, 3*time.Hour, true)
	aid, code := auditPaymentAgent(t, 0)
	mustExec(t, `UPDATE orders SET agent_id=$2,agent_code=$3,agent_pct=40 WHERE id=$1`, oid, aid, code)
	var paid atomic.Bool
	var closes, creates atomic.Int32
	wxHTTP = &http.Client{Transport: auditPaymentTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method == "GET" {
			state, tx := "NOTPAY", ""
			if paid.Load() {
				state = "SUCCESS"
				tx = "R5-" + oid
			}
			return paymentR3SignedResponse(key, 200, fmt.Sprintf(`{"out_trade_no":%q,"trade_state":%q,"transaction_id":%q,"mchid":"audit-merchant","appid":"audit-app","amount":{"total":390,"currency":"CNY"}}`, oid, state, tx)), nil
		}
		if strings.HasSuffix(req.URL.Path, "/close") {
			if closes.Add(1) == 5 {
				paid.Store(true)
			}
			return nil, context.DeadlineExceeded
		}
		creates.Add(1)
		return nil, fmt.Errorf("unexpected create")
	})}
	for i := 0; i < 5; i++ {
		mustExec(t, `UPDATE orders SET checkout_lease_until=now()-interval '1 second',updated_at=now() WHERE id=$1`, oid)
		reconcileOnce()
		if i < 4 && orderStatus(t, oid) != "pending" {
			t.Fatal("timeout fabricated terminal state")
		}
	}
	// The fifth close timed out while payment succeeded. Recover in the same
	// round, without waiting for another page visit or making a fresh Native order.
	if orderStatus(t, oid) != "paid" {
		t.Fatal("close timeout lost racing SUCCESS until a later reconciliation")
	}
	for i := 0; i < 4; i++ {
		queryAndSettle(oid)
		r := commerceRequest(t, "POST", "/0brand/api/pay/create", vid, `{"job_id":"`+jid+`","items":["logo_h"]}`)
		if r.Code != 200 || !strings.Contains(r.Body.String(), `"already_purchased":true`) {
			t.Fatal("paid item could be repurchased")
		}
	}
	if closes.Load() != 5 || creates.Load() != 0 {
		t.Fatal("unexpected channel mutation count")
	}
	paymentR5Once(t, oid, jid, aid)
}

func TestPaymentR5KilledRecoveryRetainsLeaseAcrossProcesses(t *testing.T) {
	paymentR5Schema(t)
	auditPaymentConfig(t, false)
	oid, jid, vid := paymentR3Fixture(t, 3*time.Hour, true)
	aid, code := auditPaymentAgent(t, 0)
	mustExec(t, `UPDATE orders SET agent_id=$2,agent_code=$3,agent_pct=40 WHERE id=$1`, oid, aid, code)
	marker := filepath.Join(t.TempDir(), "close-started")
	cmd := exec.Command(os.Args[0], "-test.run=^TestPaymentR5ProcessHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "R5_CHILD_MODE=hold-close", "R5_ORDER="+oid, "R5_VISITOR="+vid, "R5_MARKER="+marker)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	killed := false
	t.Cleanup(func() {
		if !killed {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	ready := false
	for i := 0; i < 150; i++ {
		if _, err := os.Stat(marker); err == nil {
			ready = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("child never reached signed-query / in-flight close")
	}
	cmd.Process.Kill()
	cmd.Wait()
	killed = true
	paymentR5Child(t, "resume-leased", oid, vid)
	mustExec(t, `UPDATE orders SET checkout_lease_until=now()-interval '1 second' WHERE id=$1`, oid)
	paymentR5Child(t, "resume-paid", oid, vid)
	paymentR5Child(t, "resume-paid", oid, vid)
	paymentR5Child(t, "query-paid", oid, vid)
	paymentR5Once(t, oid, jid, aid)
}

func TestPaymentR5ProcessHelper(t *testing.T) {
	mode := os.Getenv("R5_CHILD_MODE")
	if mode == "" {
		return
	}
	oid, vid := os.Getenv("R5_ORDER"), os.Getenv("R5_VISITOR")
	key := auditPaymentConfig(t, false)
	calls := 0
	wxHTTP = &http.Client{Transport: auditPaymentTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		if mode == "resume-leased" {
			return nil, fmt.Errorf("active lease allowed external request")
		}
		if req.Method == "GET" && strings.Contains(req.URL.Path, oid) {
			state, tx := "SUCCESS", "R5-"+oid
			if mode == "hold-close" {
				state = "NOTPAY"
				tx = ""
			}
			return paymentR3SignedResponse(key, 200, fmt.Sprintf(`{"out_trade_no":%q,"trade_state":%q,"transaction_id":%q,"mchid":"audit-merchant","appid":"audit-app","amount":{"total":390,"currency":"CNY"}}`, oid, state, tx)), nil
		}
		if mode == "hold-close" && strings.HasSuffix(req.URL.Path, "/close") {
			if err := os.WriteFile(os.Getenv("R5_MARKER"), []byte("ready"), 0600); err != nil {
				return nil, err
			}
			time.Sleep(30 * time.Second)
			return nil, context.DeadlineExceeded
		}
		return nil, fmt.Errorf("unexpected channel mutation")
	})}
	switch mode {
	case "reconcile-paid":
		reconcileOnce()
	case "query-paid":
		queryAndSettle(oid)
	default:
		r := commerceRequest(t, "POST", "/0brand/api/pay/orders/"+oid+"/resume", vid, `{}`)
		if mode == "resume-leased" {
			if r.Code != 409 || calls != 0 {
				t.Fatalf("lease lost across restart: HTTP=%d requests=%d", r.Code, calls)
			}
		} else if mode == "resume-paid" && r.Code != 200 {
			t.Fatalf("restarted recovery HTTP=%d", r.Code)
		}
	}
	t.Logf("helper=%s requests=%d status=%s", mode, calls, orderStatus(t, oid))
}

func TestPaymentR5WorkerAndTwoTabsShareOneClose(t *testing.T) {
	paymentR5Schema(t)
	key := auditPaymentConfig(t, false)
	oid, _, vid := paymentR3Fixture(t, 3*time.Hour, true)
	reached, release := make(chan struct{}), make(chan struct{})
	var closes atomic.Int32
	wxHTTP = &http.Client{Transport: auditPaymentTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method == "GET" {
			return paymentR3SignedResponse(key, 200, fmt.Sprintf(`{"out_trade_no":%q,"trade_state":"NOTPAY","mchid":"audit-merchant","appid":"audit-app","amount":{"total":390,"currency":"CNY"}}`, oid)), nil
		}
		if strings.HasSuffix(req.URL.Path, "/close") {
			if closes.Add(1) == 1 {
				close(reached)
				<-release
			}
			return nil, context.DeadlineExceeded
		}
		return nil, fmt.Errorf("unexpected channel create")
	})}
	finished := make(chan struct{})
	go func() { reconcileOnce(); close(finished) }()
	select {
	case <-reached:
	case <-time.After(3 * time.Second):
		close(release)
		<-finished
		t.Fatal("worker close not reached")
	}
	// The second worker and both browser requests see the persisted lease.
	reconcileOnce()
	responses := make(chan *httptest.ResponseRecorder, 2)
	for i := 0; i < 2; i++ {
		go func() { responses <- commerceRequest(t, "POST", "/0brand/api/pay/orders/"+oid+"/resume", vid, `{}`) }()
	}
	a, b := <-responses, <-responses
	close(release)
	<-finished
	if (a.Code != 409 && a.Code != 429) || (b.Code != 409 && b.Code != 429) || closes.Load() != 1 || orderStatus(t, oid) != "pending" {
		t.Fatalf("concurrent worker/tab mutation: a=%d b=%d close=%d status=%s", a.Code, b.Code, closes.Load(), orderStatus(t, oid))
	}
}
