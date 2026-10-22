package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDeliveryR6SuccessfulResponseCannotTriggerAnotherGeneration(t *testing.T) {
	for _, badFile := range []bool{false, true} {
		t.Run(fmt.Sprint("disk_failure_", badFile), func(t *testing.T) {
			var calls atomic.Int32
			image := []byte("invalid image")
			if badFile {
				image = commercialPNG(t, 90)
			}
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"b64_json": base64.StdEncoding.EncodeToString(image)}}})
			}))
			defer s.Close()
			t.Setenv("ZEROBRAND_IMG_BASE_URL", s.URL)
			t.Setenv("ZEROBRAND_IMG_API_KEY", "local-fake")
			t.Setenv("ZEROBRAND_IMG_FALLBACK_BASE_URL", "")
			t.Setenv("ZEROBRAND_IMG_FALLBACK_API_KEY", "")
			t.Cleanup(noteImgSuccess)
			dest := filepath.Join(t.TempDir(), "output.png")
			if badFile {
				if e := os.Mkdir(dest, 0700); e != nil {
					t.Fatal(e)
				}
			}
			_, e := Generate("fake", dest, nil)
			if !errors.Is(e, errGenerationUnknown) || calls.Load() != 1 {
				t.Fatalf("successful upstream response replayed: calls=%d err=%v", calls.Load(), e)
			}
		})
	}
}

func TestDeliveryR6LostWorkerConnectionFencesLateResult(t *testing.T) {
	paymentR5Schema(t)
	j := commercialJob(t, "queued", "directions", 0)
	old, ok := claimJob()
	if !ok || old.ID != j.ID {
		t.Fatal("claim failed")
	}
	defer releaseExecution(old)
	release := make(chan struct{})
	started := make(chan struct{})
	var calls atomic.Int32
	raw := commercialPNG(t, 80)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"b64_json": base64.StdEncoding.EncodeToString(raw)}}})
	}))
	defer s.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	t.Setenv("ZEROBRAND_IMG_BASE_URL", s.URL)
	t.Setenv("ZEROBRAND_IMG_API_KEY", "local-fake")
	t.Cleanup(noteImgSuccess)
	done := make(chan error, 1)
	go func() { done <- ensureJobImage(old, "direction", 0, "direction_0.png", "fake", nil) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("fake upstream not reached")
	}
	var killed bool
	if e := pool.QueryRow(contextBG(), "SELECT pg_terminate_backend($1)", old.executionConn.PgConn().PID()).Scan(&killed); e != nil || !killed {
		t.Fatalf("test connection not terminated: %v", e)
	}
	// Termination is asynchronous in PostgreSQL; recovery must succeed once the
	// old backend has actually released its session lock.
	var newer jobRow
	for i := 0; i < 30; i++ {
		if e := recoverInterruptedJobs(); e != nil {
			t.Fatal(e)
		}
		newer, ok = claimJob()
		if ok {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ok {
		t.Fatal("orphan not reclaimed")
	}
	defer releaseExecution(newer)
	if e := ensureJobImage(newer, "direction", 0, "direction_0.png", "fake", nil); !errors.Is(e, errGenerationUnknown) {
		t.Fatalf("pending attempt was not fenced: %v", e)
	}
	close(release)
	if e := <-done; !errors.Is(e, errExecutionLost) {
		t.Fatalf("stale worker registered result: %v", e)
	}
	if e := ensureJobImage(newer, "direction", 0, "direction_0.png", "fake", nil); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 1 {
		t.Fatalf("duplicate image calls: %d", calls.Load())
	}
	var n int
	pool.QueryRow(contextBG(), "SELECT count(*) FROM job_images WHERE job_id=$1", j.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("images=%d", n)
	}
}

func TestDeliveryR6DatabaseOutageIsRetryable(t *testing.T) {
	jid, vid := auditPaymentJob(t)
	oid := auditPaymentOrder(t, jid, "items:logo_h", "", 390)
	original := pool
	dead, err := pgxpool.New(contextBG(), dbURL())
	if err != nil {
		t.Fatal(err)
	}
	dead.Close()
	pool = dead
	defer func() { pool = original }()
	for _, p := range []string{"/api/jobs/" + jid, "/api/jobs/" + jid + "/detail", "/api/pay/checkout/" + oid} {
		r := commerceRequest(t, "GET", "/0brand"+p, vid, "")
		if r.Code != 503 {
			t.Errorf("DB failure must remain retryable for %s: status=%d", p, r.Code)
		}
	}
}

func TestDeliveryR6RecoveryDoesNotStealLiveExecutor(t *testing.T) {
	j := commercialJob(t, "running", "directions", 0)
	c, e := pool.Acquire(contextBG())
	if e != nil {
		t.Fatal(e)
	}
	defer c.Release()
	var locked bool
	if e = c.QueryRow(contextBG(), "SELECT pg_try_advisory_lock(197705,hashtext($1))", j.ID).Scan(&locked); e != nil || !locked {
		t.Fatal(e)
	}
	defer c.Exec(context.Background(), "SELECT pg_advisory_unlock(197705,hashtext($1))", j.ID)
	if e = recoverInterruptedJobs(); e != nil {
		t.Fatal(e)
	}
	var status string
	pool.QueryRow(contextBG(), "SELECT status FROM jobs WHERE id=$1", j.ID).Scan(&status)
	if status != "running" {
		t.Fatalf("live executor stolen: %s", status)
	}
	c.Exec(contextBG(), "SELECT pg_advisory_unlock(197705,hashtext($1))", j.ID)
	if e = recoverInterruptedJobs(); e != nil {
		t.Fatal(e)
	}
	pool.QueryRow(contextBG(), "SELECT status FROM jobs WHERE id=$1", j.ID).Scan(&status)
	if status != "queued" {
		t.Fatalf("orphan was not recovered without restart: %s", status)
	}
}

func TestDeliveryR6AmbiguousGenerationIsNotReplayed(t *testing.T) {
	j := commercialJob(t, "running", "directions", 0)
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		c, _, e := w.(http.Hijacker).Hijack()
		if e == nil {
			c.Close()
		}
	}))
	defer s.Close()
	t.Setenv("ZEROBRAND_IMG_BASE_URL", s.URL)
	t.Setenv("ZEROBRAND_IMG_API_KEY", "local-fake")
	t.Setenv("ZEROBRAND_IMG_FALLBACK_BASE_URL", "")
	t.Setenv("ZEROBRAND_IMG_FALLBACK_API_KEY", "")
	t.Cleanup(noteImgSuccess)
	for i := 0; i < 2; i++ {
		if e := ensureJobImage(j, "direction", 0, "direction_0.png", "fake prompt", nil); e == nil {
			t.Fatal("unknown result reported successful")
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("unknown generation was replayed %d times", n)
	}
	// A complete late artifact is recoverable without another upstream request.
	commercialWritePNG(t, j.ID, "direction_0.png", commercialPNG(t, 100))
	if e := ensureJobImage(j, "direction", 0, "direction_0.png", "fake prompt", nil); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 1 {
		t.Fatal("saved output regenerated")
	}
	var f string
	if e := pool.QueryRow(contextBG(), "SELECT file FROM job_images WHERE job_id=$1", j.ID).Scan(&f); e != nil || !strings.Contains(f, filepath.Base(j.ID)) {
		t.Fatalf("late artifact not registered: %s %v", f, e)
	}
}

// Real PostgreSQL transaction, with the connection severed at the COMMIT
// boundary. The after mode commits on the wire, then loses the acknowledgement.
type deliveryR6CommitFault struct {
	after bool
	fired atomic.Bool
}

func (f *deliveryR6CommitFault) TraceQueryStart(ctx context.Context, c *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.EqualFold(d.SQL, "commit") && f.fired.CompareAndSwap(false, true) {
		if f.after {
			_, _ = c.PgConn().Exec(ctx, "COMMIT").ReadAll()
		}
		_ = c.Close(ctx)
	}
	return ctx
}
func (f *deliveryR6CommitFault) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestDeliveryR6SettlementConnectionLossAndConcurrentRecovery(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprint("commit_applied_", after), func(t *testing.T) {
			paymentR5Schema(t)
			key := auditPaymentConfig(t, false)
			oid, jid, vid := paymentR3Fixture(t, time.Hour, true)
			aid, code := auditPaymentAgent(t, 0)
			mustExec(t, "UPDATE orders SET agent_id=$2,agent_code=$3,agent_pct=40 WHERE id=$1", oid, aid, code)
			original := pool
			cfg := original.Config()
			fault := &deliveryR6CommitFault{after: after}
			cfg.ConnConfig.Tracer = fault
			p, e := pgxpool.NewWithConfig(contextBG(), cfg)
			if e != nil {
				t.Fatal(e)
			}
			pool = p
			e = settleOrder(oid, "audit-merchant", "audit-app", 390, "TX"+oid)
			pool = original
			p.Close()
			if e == nil || !fault.fired.Load() {
				t.Fatal("connection fault not exercised")
			}
			want := "pending"
			if after {
				want = "paid"
			}
			if got := orderStatus(t, oid); got != want {
				t.Fatalf("wrong commit boundary: %s", got)
			}
			var writes atomic.Int32
			wxHTTP = &http.Client{Transport: auditPaymentTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" {
					writes.Add(1)
					return nil, fmt.Errorf("unexpected payment mutation")
				}
				return paymentR3SignedResponse(key, 200, fmt.Sprintf(`{"out_trade_no":%q,"trade_state":"SUCCESS","mchid":"audit-merchant","appid":"audit-app","amount":{"total":390,"currency":"CNY"},"transaction_id":%q}`, oid, "TX"+oid)), nil
			})}
			var wg sync.WaitGroup
			for i := 0; i < 3; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					queryAndSettle(oid)
					r := commerceRequest(t, "GET", "/0brand/api/pay/checkout/"+oid, vid, "")
					if r.Code != 200 {
						t.Errorf("recovery read %d", r.Code)
					}
				}()
			}
			wg.Wait()
			for i := 0; i < 3; i++ {
				paymentR5Child(t, "query-paid", oid, "")
			}
			paymentR5Once(t, oid, jid, aid)
			if writes.Load() != 0 {
				t.Fatal("recovery created a new payment")
			}
		})
	}
}

func TestDeliveryR6ClaimCommitLossBecomesRecoverable(t *testing.T) {
	paymentR5Schema(t)
	j := commercialJob(t, "queued", "directions", 0)
	original := pool
	cfg := original.Config()
	fault := &deliveryR6CommitFault{after: true}
	cfg.ConnConfig.Tracer = fault
	p, e := pgxpool.NewWithConfig(contextBG(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	pool = p
	_, ok := claimJob()
	pool = original
	p.Close()
	if ok || !fault.fired.Load() {
		t.Fatal("lost claim acknowledgement not injected")
	}
	if e = recoverInterruptedJobs(); e != nil {
		t.Fatal(e)
	}
	var status string
	pool.QueryRow(contextBG(), "SELECT status FROM jobs WHERE id=$1", j.ID).Scan(&status)
	if status != "queued" {
		t.Fatalf("orphan after lost claim commit: %s", status)
	}
}

func TestDeliveryR6LateArtifactRecoversWithoutAnotherGeneration(t *testing.T) {
	j := commercialJob(t, "error", "directions", 0)
	mustExec(t, "INSERT INTO generation_attempts(job_id,kind,idx,state) VALUES($1,'direction',0,'unknown')", j.ID)
	commercialWritePNG(t, j.ID, "direction_0.png", commercialPNG(t, 120))
	if e := recoverInterruptedJobs(); e != nil {
		t.Fatal(e)
	}
	var s string
	pool.QueryRow(contextBG(), "SELECT status FROM jobs WHERE id=$1", j.ID).Scan(&s)
	if s != "queued" {
		t.Fatalf("complete late artifact must be reconciled: %s", s)
	}
}

func TestDeliveryR6ServiceProcessHelper(t *testing.T) {
	if os.Getenv("R6_SERVICE_CHILD") != "1" {
		return
	}
	// TestMain enforces zerobrand_test. Real main + workers, exclusively fake APIs.
	Main()
}
func deliveryR6Wait(t *testing.T, description string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(18 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("timeout: " + description)
}
func TestDeliveryR6ActualServiceThreeRestartsAndLateDelivery(t *testing.T) {
	paymentR5Schema(t)
	auditPaymentConfig(t, false)
	j := commercialJob(t, "queued", "boards", 0)
	aid, code := auditPaymentAgent(t, 0)
	oid := auditPaymentOrder(t, j.ID, "items:logo_h", code, 390)
	mustExec(t, "INSERT INTO order_items(order_id,item_kind,item_name,price_cents) VALUES($1,'logo_h','fixture',390)", oid)
	if e := settleOrder(oid, "audit-merchant", "audit-app", 390, "TX"+oid); e != nil {
		t.Fatal(e)
	}
	commercialWritePNG(t, j.ID, "direction_0.png", commercialPNG(t, 20))
	commercialWritePNG(t, j.ID, "system_card.png", commercialPNG(t, 40))
	var calls atomic.Int32
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer upstream.Close()
	defer close(release)
	launch := func() *exec.Cmd {
		l, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		port := l.Addr().(*net.TCPAddr).Port
		l.Close()
		cmd := exec.Command(os.Args[0], "-test.run=^TestDeliveryR6ServiceProcessHelper$")
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), "R6_SERVICE_CHILD=1", "MBRAND_DEPLOYMENT_ID=m-brand", "ZEROBRAND_PORT="+fmt.Sprint(port), "ZEROBRAND_GEN_DIR="+genDir(), "ZEROBRAND_IMG_BASE_URL="+upstream.URL, "ZEROBRAND_IMG_API_KEY=fake-local-only", "ZEROBRAND_IMG_FALLBACK_BASE_URL=", "ZEROBRAND_IMG_FALLBACK_API_KEY=")
		if e = cmd.Start(); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			if cmd.ProcessState == nil {
				cmd.Process.Kill()
				cmd.Wait()
			}
		})
		client := &http.Client{Timeout: time.Second}
		deliveryR6Wait(t, "candidate service health", func() bool {
			r, e := client.Get(fmt.Sprintf("http://127.0.0.1:%d/0brand/api/health", port))
			if e != nil {
				return false
			}
			r.Body.Close()
			return r.StatusCode == 200
		})
		return cmd
	}
	state := func() string {
		var s string
		pool.QueryRow(contextBG(), "SELECT status FROM jobs WHERE id=$1", j.ID).Scan(&s)
		return s
	}
	for i := 0; i < 3; i++ {
		cmd := launch()
		if i == 0 {
			deliveryR6Wait(t, "initial fake request submitted", func() bool { return calls.Load() == 1 })
		} else {
			deliveryR6Wait(t, "unknown generation made actionable", func() bool { return state() == "error" })
		}
		if e := cmd.Process.Kill(); e != nil {
			t.Fatal(e)
		}
		cmd.Wait()
		t.Logf("service kill %d: fake generation calls=%d", i+1, calls.Load())
	}
	if calls.Load() != 1 {
		t.Fatalf("restarts repeated unknown generation: %d", calls.Load())
	}
	// Complete result arriving from the previous attempt. Periodic recovery must
	// register it even without another restart or user clicking retry.
	cmd := launch()
	deliveryR6Wait(t, "review state", func() bool { return state() == "error" })
	commercialWritePNG(t, j.ID, "logo_h.png", commercialPNG(t, 99))
	deliveryR6Wait(t, "late artifact automatic completion", func() bool { return state() == "done" })
	cmd.Process.Kill()
	cmd.Wait()
	if calls.Load() != 1 {
		t.Fatal("late artifact triggered another generation")
	}
	paymentR5Once(t, oid, j.ID, aid)
	var n int
	pool.QueryRow(contextBG(), "SELECT count(*) FROM job_images WHERE job_id=$1 AND kind='logo_h'", j.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("late artifact count %d", n)
	}
}

func TestDeliveryR6AdminShowsUncertainWorkAndNewDeliverables(t *testing.T) {
	j := commercialJob(t, "error", "boards", 0)
	mustExec(t, "UPDATE jobs SET execution_started_at=now()-interval '2 minutes' WHERE id=$1", j.ID)
	mustExec(t, "INSERT INTO generation_attempts(job_id,kind,idx,state) VALUES($1,'logo_h',2,'unknown')", j.ID)
	mustExec(t, "INSERT INTO job_images(job_id,kind,idx,file,tokens) VALUES($1,'logo_icon',4,$2,0)", j.ID, webPath(j.ID, "logo_icon.png"))
	r := gin.New()
	r.GET("/jobs", adminListJobs)
	r.GET("/jobs/:id", adminJobDetail)
	list := httptest.NewRecorder()
	r.ServeHTTP(list, httptest.NewRequest("GET", "/jobs", nil))
	var data []struct {
		ID          string
		NeedsReview bool  `json:"needs_review"`
		Elapsed     int64 `json:"elapsed_seconds"`
	}
	if e := json.Unmarshal(list.Body.Bytes(), &data); e != nil {
		t.Fatal(e)
	}
	found := false
	for _, d := range data {
		if d.ID == j.ID {
			found = true
			if !d.NeedsReview || d.Elapsed < 119 {
				t.Fatalf("missing operational status: %+v", d)
			}
		}
	}
	if !found {
		t.Fatal("pending job missing")
	}
	detail := httptest.NewRecorder()
	r.ServeHTTP(detail, httptest.NewRequest("GET", "/jobs/"+j.ID, nil))
	if !strings.Contains(detail.Body.String(), "logo_icon") || !strings.Contains(detail.Body.String(), "unknown") {
		t.Fatalf("operator cannot inspect delivery: %s", detail.Body)
	}
}
