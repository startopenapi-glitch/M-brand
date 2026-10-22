package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDeliveryReviewUnknownFailurePreservesBreaker(t *testing.T) {
	noteImgSuccess()
	t.Cleanup(noteImgSuccess)
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(503) }))
	defer s.Close()
	t.Setenv("ZEROBRAND_IMG_BASE_URL", s.URL)
	t.Setenv("ZEROBRAND_IMG_API_KEY", "fake")
	t.Setenv("ZEROBRAND_IMG_FALLBACK_BASE_URL", "")
	t.Setenv("ZEROBRAND_IMG_FALLBACK_API_KEY", "")
	for i := 0; i < 3; i++ {
		if _, e := Generate("fake", filepath.Join(t.TempDir(), "x.png"), nil); e == nil {
			t.Fatal("fake failure succeeded")
		}
	}
	paused, _ := salesPaused()
	if calls.Load() != 3 || !paused {
		t.Fatalf("one dispatch per attempt and breaker expected, calls=%d paused=%v", calls.Load(), paused)
	}
	if got := imgHealthSnapshot()["fail_streak"]; got != 3 {
		t.Fatalf("failures not recorded: %v", got)
	}
}

func TestDeliveryReviewClockTracksStageAndPurchaseVersion(t *testing.T) {
	paymentR5Schema(t)
	j := commercialJob(t, "queued", "directions", 0)
	first, ok := claimJob()
	if !ok {
		t.Fatal("first claim")
	}
	releaseExecution(first)
	mustExec(t, "UPDATE jobs SET status='queued',stage='boards',execution_started_at=now()-interval '2 days' WHERE id=$1", j.ID)
	second, ok := claimJob()
	if !ok {
		t.Fatal("system card claim")
	}
	releaseExecution(second)
	var began time.Time
	pool.QueryRow(contextBG(), "SELECT execution_started_at FROM jobs WHERE id=$1", j.ID).Scan(&began)
	if time.Since(began) > time.Minute {
		t.Fatal("choosing pause counted as generation time")
	}
	// Restart the same delivery run: retain its clock.
	if e := recoverInterruptedJobs(); e != nil {
		t.Fatal(e)
	}
	third, ok := claimJob()
	if !ok {
		t.Fatal("recovery claim")
	}
	releaseExecution(third)
	var resumed time.Time
	pool.QueryRow(contextBG(), "SELECT execution_started_at FROM jobs WHERE id=$1", j.ID).Scan(&resumed)
	if !began.Equal(resumed) {
		t.Fatal("restart reset current run clock")
	}
	mustExec(t, "UPDATE jobs SET status='queued',stage='boards',delivery_version=delivery_version+1,execution_started_at=now()-interval '2 days' WHERE id=$1", j.ID)
	paid, ok := claimJob()
	if !ok {
		t.Fatal("paid addon claim")
	}
	releaseExecution(paid)
	pool.QueryRow(contextBG(), "SELECT execution_started_at FROM jobs WHERE id=$1", j.ID).Scan(&began)
	if time.Since(began) > time.Minute {
		t.Fatal("purchase pause counted as generation time")
	}
}

func TestDeliveryReviewLateRecoveryScansBeyondFirstHundred(t *testing.T) {
	paymentR5Schema(t)
	var target jobRow
	var candidates []jobRow
	for i := 0; i < 101; i++ {
		j := commercialJob(t, "error", "directions", 0)
		mustExec(t, "INSERT INTO generation_attempts(job_id,kind,idx,state) VALUES($1,'direction',0,'unknown')", j.ID)
		candidates = append(candidates, j)
	}
	rows, e := pool.Query(contextBG(), `SELECT ga.job_id FROM generation_attempts ga JOIN jobs j ON j.id=ga.job_id WHERE j.status='error' AND ga.state IN ('submitted','unknown') LIMIT 100`)
	if e != nil {
		t.Fatal(e)
	}
	seen := map[string]bool{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			t.Fatal(e)
		}
		seen[id] = true
	}
	rows.Close()
	for _, j := range candidates {
		if !seen[j.ID] {
			target = j
			break
		}
	}
	if target.ID == "" {
		t.Fatal("missing beyond-page fixture")
	}
	commercialWritePNG(t, target.ID, "direction_0.png", commercialPNG(t, 80))
	for i := 0; i < 3; i++ {
		if e := recoverInterruptedJobs(); e != nil {
			t.Fatal(e)
		}
	}
	var status string
	pool.QueryRow(contextBG(), "SELECT status FROM jobs WHERE id=$1", target.ID).Scan(&status)
	if status != "queued" {
		t.Fatalf("valid late artifact starved: %s", status)
	}
}

func TestDeliveryReviewRecoveryReadsDoNotInventEmptyData(t *testing.T) {
	_, vid := auditPaymentJob(t)
	original := pool
	dead, e := pgxpool.New(contextBG(), dbURL())
	if e != nil {
		t.Fatal(e)
	}
	dead.Close()
	pool = dead
	defer func() { pool = original }()
	for _, path := range []string{"/api/draft", "/api/my/jobs"} {
		r := commerceRequest(t, "GET", "/0brand"+path, vid, "")
		if r.Code != 503 {
			t.Errorf("%s failure became HTTP %d %s", path, r.Code, r.Body.String())
		}
	}
}

func TestDeliveryReviewListScanFailureIsNotPartialSuccess(t *testing.T) {
	paymentR5Schema(t)
	j, vid := auditPaymentJob(t)
	second, _ := auditPaymentJob(t)
	mustExec(t, "UPDATE jobs SET visitor=$2 WHERE id=$1", second, vid)
	mustExec(t, "UPDATE jobs SET created_at=now()-interval '1 minute' WHERE id=$1", j)
	// A test-only view supplies an invalid second row, simulating a decoder failure.
	mustExec(t, "ALTER TABLE jobs RENAME TO review_jobs_source")
	defer mustExec(t, "DROP VIEW jobs; ALTER TABLE review_jobs_source RENAME TO jobs")
	mustExec(t, fmt.Sprintf(`CREATE VIEW jobs AS SELECT id,visitor,CASE WHEN id='%s' THEN NULL::text ELSE status END status,stage,chosen,trial,created_at FROM review_jobs_source`, strings.ReplaceAll(j, "'", "''")))
	r := commerceRequest(t, "GET", "/0brand/api/my/jobs", vid, "")
	if r.Code != 503 {
		t.Fatalf("invalid row became successful partial list: %d %s", r.Code, r.Body.String())
	}
	mustExec(t, `CREATE FUNCTION review_raise(value text,bad boolean) RETURNS text LANGUAGE plpgsql AS $$BEGIN IF bad THEN RAISE EXCEPTION 'test stream failure'; END IF; RETURN value; END$$`)
	mustExec(t, fmt.Sprintf(`CREATE OR REPLACE VIEW jobs AS SELECT id,visitor,review_raise(status,id='%s') status,stage,chosen,trial,created_at FROM review_jobs_source`, strings.ReplaceAll(j, "'", "''")))
	r = commerceRequest(t, "GET", "/0brand/api/my/jobs", vid, "")
	if r.Code != 503 {
		t.Fatalf("stream error became success: %d %s", r.Code, r.Body.String())
	}
}

func TestDeliveryReviewExecutionLocksDoNotReserveApplicationPool(t *testing.T) {
	paymentR5Schema(t)
	commercialJob(t, "queued", "directions", 0)
	j, ok := claimJob()
	if !ok {
		t.Fatal("claim failed")
	}
	if n := pool.Stat().AcquiredConns(); n != 0 {
		releaseExecution(j)
		t.Fatalf("long generation reserves %d application connections", n)
	}
	releaseExecution(j)
	// The smallest supported app pool must still claim without deadlock.
	original := pool
	cfg := original.Config()
	cfg.MaxConns = 1
	cfg.MinConns = 0
	p, e := pgxpool.NewWithConfig(context.Background(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	pool = p
	defer func() { pool = original; p.Close() }()
	mustExec(t, "UPDATE jobs SET status='queued' WHERE id=$1", j.ID)
	j, ok = claimJob()
	if !ok {
		t.Fatal("single-connection app pool could not claim")
	}
	releaseExecution(j)
}
