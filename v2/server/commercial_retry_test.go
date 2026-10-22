package app

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// Uses the actual anonymous-create and choose handlers: trial remains a record
// of acquisition, while the boards stage carries the paid ticket obligation.
func commercialFailedUpgradedTrial(t *testing.T) (*http.Client, string, string, string, string) {
	t.Helper()
	commercialRequireTestDB(t)
	resetCreateLimiter()
	resetRateWindow()
	noteImgSuccess()
	t.Setenv("ZEROBRAND_GEN_DIR", t.TempDir())
	srv, base := v2Server(t)
	cl := v2Client(srv)
	status, out := postJSONv2(t, cl, base+"/api/jobs", `{"cn_name":"试用升级重试","industry":"茶饮"}`)
	if status != 201 || out["trial"] != true {
		t.Fatalf("anonymous job must begin as a trial: status=%d body=%v", status, out)
	}
	jid := out["id"].(string)
	var vid string
	if err := pool.QueryRow(contextBG(), "SELECT visitor FROM jobs WHERE id=$1", jid).Scan(&vid); err != nil {
		t.Fatal(err)
	}
	code := upperCode("COMMRETRY")
	t.Cleanup(func() {
		mustExec(t, "DELETE FROM audit_events WHERE object_id=$1 OR object_id=$2", jid, code)
		mustExec(t, "DELETE FROM job_images WHERE job_id=$1", jid)
		mustExec(t, "DELETE FROM jobs WHERE id=$1", jid)
		mustExec(t, "DELETE FROM invite_codes WHERE code=$1", code)
		mustExec(t, "DELETE FROM visitors WHERE id=$1", vid)
		resetRateWindow()
	})
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("direction_%d.png", i)
		commercialWritePNG(t, jid, name, commercialPNG(t, byte(60+i*40)))
		mustExec(t, `INSERT INTO job_images(job_id,kind,idx,file,tokens)
			VALUES($1,'direction',$2,$3,0)`, jid, i, webPath(jid, name))
	}
	mustExec(t, "UPDATE jobs SET status='ready_to_choose',stage='choose' WHERE id=$1", jid)
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,used,bound_visitor) VALUES($1,1,0,$2)", code, vid)
	if status, out := postJSONv2(t, cl, base+"/api/jobs/"+jid+"/choose", `{"direction":1}`); status != 200 {
		t.Fatalf("actual choose must occupy this visitor's ticket: status=%d body=%v", status, out)
	}
	var trial bool
	var used int
	if err := pool.QueryRow(contextBG(), "SELECT trial FROM jobs WHERE id=$1", jid).Scan(&trial); err != nil {
		t.Fatal(err)
	}
	if !trial {
		t.Fatal("trial acquisition history must remain true after paid upgrade")
	}
	if err := pool.QueryRow(contextBG(), "SELECT used FROM invite_codes WHERE code=$1", code).Scan(&used); err != nil || used != 1 {
		t.Fatalf("choose must consume exactly one ticket: used=%d error=%v", used, err)
	}
	markJobFailed(jid, "commercial retry fixture: mock generation failed")
	return cl, base, jid, vid, code
}

// Catches using trial=true to skip reoccupying the refunded paid-stage ticket.
func TestCommercialUpgradedTrialRetryReclaimsTicket(t *testing.T) {
	cl, base, jid, vid, code := commercialFailedUpgradedTrial(t)
	if status, out := postJSONv2(t, cl, base+"/api/jobs/"+jid+"/retry", `{}`); status != 200 {
		t.Fatalf("retry should resume with the returned ticket: status=%d body=%v", status, out)
	}
	var used int
	var usedBy string
	var hasUsedAt bool
	if err := pool.QueryRow(contextBG(), `SELECT used,COALESCE(used_by,''),used_at IS NOT NULL
		FROM invite_codes WHERE code=$1`, code).Scan(&used, &usedBy, &hasUsedAt); err != nil {
		t.Fatal(err)
	}
	var status, quota string
	var refunded, trial bool
	if err := pool.QueryRow(contextBG(), `SELECT status,quota_code,refunded,trial FROM jobs WHERE id=$1`, jid).
		Scan(&status, &quota, &refunded, &trial); err != nil {
		t.Fatal(err)
	}
	if used != 1 || usedBy != vid || !hasUsedAt || status != "queued" || quota != code || refunded || !trial {
		t.Fatalf("paid retry must reoccupy exactly once and retain origin: used=%d usedBy=%q usedAt=%v status=%s quotaMatches=%v refunded=%v trial=%v",
			used, usedBy, hasUsedAt, status, quota == code, refunded, trial)
	}
	markJobFailed(jid, "commercial retry fixture: second mock failure")
	if err := pool.QueryRow(contextBG(), "SELECT used FROM invite_codes WHERE code=$1", code).Scan(&used); err != nil || used != 0 {
		t.Fatalf("failure after retry must return its occupied ticket once: used=%d error=%v", used, err)
	}
}

// Catches a stale quota_code bypassing the current ticket-owner requirement.
func TestCommercialRetryCannotConsumeTransferredTicket(t *testing.T) {
	cl, base, jid, _, code := commercialFailedUpgradedTrial(t)
	mustExec(t, "UPDATE invite_codes SET bound_visitor=$2 WHERE code=$1", code, "COMMNEWOWNER"+newID(12))
	if status, _ := postJSONv2(t, cl, base+"/api/jobs/"+jid+"/retry", `{}`); status != 402 {
		t.Errorf("a transferred original ticket is unavailable to this visitor: status=%d", status)
	}
	var used int
	var status string
	var refunded bool
	if err := pool.QueryRow(contextBG(), "SELECT used FROM invite_codes WHERE code=$1", code).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(contextBG(), "SELECT status,refunded FROM jobs WHERE id=$1", jid).Scan(&status, &refunded); err != nil {
		t.Fatal(err)
	}
	if used != 0 || status != "error" || !refunded {
		t.Fatalf("denied retry must not charge or requeue: used=%d status=%s refunded=%v", used, status, refunded)
	}
}

// Catches double consumption/requeue when two browser requests race.
func TestCommercialConcurrentRetryConsumesOneTicket(t *testing.T) {
	cl, base, jid, vid, code := commercialFailedUpgradedTrial(t)
	extra := upperCode("COMMEXTRA")
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,used,bound_visitor) VALUES($1,1,0,$2)", extra, vid)
	t.Cleanup(func() { mustExec(t, "DELETE FROM invite_codes WHERE code=$1", extra) })
	start := make(chan struct{})
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			resp, err := cl.Post(base+"/api/jobs/"+jid+"/retry", "application/json", strings.NewReader(`{}`))
			if err != nil {
				codes <- 0
				return
			}
			resp.Body.Close()
			codes <- resp.StatusCode
		}()
	}
	close(start)
	wg.Wait()
	close(codes)
	success, conflict := 0, 0
	for status := range codes {
		if status == 200 {
			success++
		}
		if status == 409 {
			conflict++
		}
	}
	var total int
	if err := pool.QueryRow(contextBG(), "SELECT SUM(used) FROM invite_codes WHERE code IN ($1,$2)", code, extra).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if success != 1 || conflict != 1 || total != 1 {
		t.Fatalf("concurrent retry must have one winner and one consumed ticket: success=%d conflict=%d used=%d", success, conflict, total)
	}
}

// Catches checking ownership before waiting for the task lock, then queuing a
// task that has been recovered to a different visitor while the request waited.
func TestCommercialRetryRechecksOwnershipAfterTaskLock(t *testing.T) {
	cl, base, jid, _, code := commercialFailedUpgradedTrial(t)
	tx, err := pool.Begin(contextBG())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(contextBG())
	var lockedID string
	if err := tx.QueryRow(contextBG(), "SELECT id FROM jobs WHERE id=$1 FOR UPDATE", jid).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}
	var blocker int
	if err := tx.QueryRow(contextBG(), "SELECT pg_backend_pid()").Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	result := make(chan int, 1)
	go func() {
		resp, err := cl.Post(base+"/api/jobs/"+jid+"/retry", "application/json", strings.NewReader(`{}`))
		if err != nil {
			result <- 0
			return
		}
		resp.Body.Close()
		result <- resp.StatusCode
	}()
	blocked := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(contextBG(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
			WHERE datname=current_database() AND $1::int=ANY(pg_blocking_pids(pid)))`, blocker).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("retry did not reach the held task lock")
	}
	newOwner := "COMMNEWOWNER" + newID(12)
	if _, err := tx.Exec(contextBG(), "UPDATE jobs SET visitor=$2 WHERE id=$1", jid, newOwner); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(contextBG(), "UPDATE invite_codes SET bound_visitor=$2 WHERE code=$1", code, newOwner); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(contextBG()); err != nil {
		t.Fatal(err)
	}
	select {
	case status := <-result:
		if status != 404 {
			t.Fatalf("ownership must be checked after acquiring task lock: status=%d", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("retry did not finish after releasing task lock")
	}
}

// A revoked code cannot authorize retry through the legacy job-code ownership
// fallback when the task's visitor belongs to another device.
func TestCommercialRevokedCodeCannotAuthorizeRetry(t *testing.T) {
	commercialRequireTestDB(t)
	resetRateWindow()
	jid, _ := commercialFixture(t, "error", "directions")
	vid, code := randHex(16), upperCode("COMMREVOKED")
	mustExec(t, "INSERT INTO visitors(id) VALUES($1)", vid)
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,bound_visitor,revoked) VALUES($1,1,$2,true)", code, vid)
	mustExec(t, "UPDATE jobs SET code=$2 WHERE id=$1", jid, code)
	t.Cleanup(func() {
		mustExec(t, "DELETE FROM invite_codes WHERE code=$1", code)
		mustExec(t, "DELETE FROM visitors WHERE id=$1", vid)
		resetRateWindow()
	})
	cl, base := commercialServer(t)
	ck := commercialCookie(visitorCookie, vid, 0)
	if status, _ := commercialRequest(t, cl, "GET", base+"/api/jobs/"+jid, "", ck); status != 404 {
		t.Fatalf("revoked holder should not be able to read another visitor's job: status=%d", status)
	}
	if status, _ := commercialRequest(t, cl, "POST", base+"/api/jobs/"+jid+"/retry", `{}`, ck); status != 404 {
		t.Fatalf("revoked holder must not authorize retry when read is forbidden: status=%d", status)
	}
	var status string
	if err := pool.QueryRow(contextBG(), "SELECT status FROM jobs WHERE id=$1", jid).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "error" {
		t.Fatalf("unauthorized retry must preserve task status, got %s", status)
	}
}

// Paying without a gate cookie must activate the ticket already bound to this
// visitor, even when the visitor used their one free trial earlier.
func TestCommercialPaidVisitorWithoutGateCanCreateAfterTrial(t *testing.T) {
	commercialRequireTestDB(t)
	resetCreateLimiter()
	resetRateWindow()
	noteImgSuccess()
	enablePayMockV2(t)
	t.Setenv("ZEROBRAND_TRIAL_PER_SESSION", "1")
	t.Setenv("ZEROBRAND_TRIAL_PER_IP", "100000")
	t.Setenv("ZEROBRAND_TRIAL_DAILY_CAP", "100000")
	cl, base := commercialServer(t)
	st, first := postJSONv2(t, cl, base+"/api/jobs", `{"cn_name":"购票前试用","industry":"茶饮"}`)
	if st != 201 || first["trial"] != true {
		t.Fatalf("initial trial should be created: status=%d body=%v", st, first)
	}
	firstID := first["id"].(string)
	var vid string
	if err := pool.QueryRow(contextBG(), "SELECT visitor FROM jobs WHERE id=$1", firstID).Scan(&vid); err != nil {
		t.Fatal(err)
	}
	jobs := []string{firstID}
	oid := ""
	t.Cleanup(func() {
		for _, jid := range jobs {
			mustExec(t, "DELETE FROM audit_events WHERE object_id=$1", jid)
			mustExec(t, "DELETE FROM job_images WHERE job_id=$1", jid)
			mustExec(t, "DELETE FROM jobs WHERE id=$1", jid)
		}
		if oid != "" {
			mustExec(t, "DELETE FROM audit_events WHERE object_id=$1", oid)
			mustExec(t, "DELETE FROM invite_codes WHERE order_id=$1", oid)
			mustExec(t, "DELETE FROM orders WHERE id=$1", oid)
		}
		mustExec(t, "DELETE FROM visitors WHERE id=$1", vid)
		resetCreateLimiter()
		resetRateWindow()
	})
	st, created := recordedLegacyOrder(t, cl, base)
	if st != 201 {
		t.Fatalf("mock HTTP checkout must create an order: status=%d body=%v", st, created)
	}
	oid = created["id"].(string)
	// Advance only this mock order's local timestamp instead of waiting or
	// invoking a real payment channel; settlement still uses the public handler.
	mustExec(t, "UPDATE orders SET created_at=now()-interval '3 seconds' WHERE id=$1", oid)
	st, paid := getJSON(t, cl, base+"/api/pay/status/"+oid)
	paidCode, _ := paid["invite_code"].(string)
	if st != 200 || paid["status"] != "paid" || paidCode == "" {
		t.Fatalf("mock order should settle and deliver a code: status=%d body=%v", st, paid)
	}
	var bound string
	if err := pool.QueryRow(contextBG(), "SELECT COALESCE(bound_visitor,'') FROM invite_codes WHERE code=$1", paidCode).Scan(&bound); err != nil || bound != vid {
		t.Fatalf("settled ticket must belong to the buying visitor: matches=%v error=%v", bound == vid, err)
	}
	u, err := url.Parse(base + "/api/jobs")
	if err != nil {
		t.Fatal(err)
	}
	for _, ck := range cl.Jar.Cookies(u) {
		if ck.Name == gateCookie {
			t.Fatal("fixture must reproduce paid visitor without a gate cookie")
		}
	}
	st, second := postJSONv2(t, cl, base+"/api/jobs", `{"cn_name":"购票后新提案","industry":"咖啡"}`)
	if id, ok := second["id"].(string); ok {
		jobs = append(jobs, id)
	}
	if st != 201 || second["trial"] != false {
		t.Fatalf("paid bound ticket must allow a non-trial proposal: status=%d body=%v", st, second)
	}
	var trialUsed, ticketUsed int
	if err := pool.QueryRow(contextBG(), "SELECT trial_used FROM visitors WHERE id=$1", vid).Scan(&trialUsed); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(contextBG(), "SELECT used FROM invite_codes WHERE order_id=$1", oid).Scan(&ticketUsed); err != nil {
		t.Fatal(err)
	}
	if trialUsed != 1 || ticketUsed != 0 {
		t.Fatalf("creating directions must not spend another trial or the paid unlock ticket: trialUsed=%d ticketUsed=%d", trialUsed, ticketUsed)
	}
}
