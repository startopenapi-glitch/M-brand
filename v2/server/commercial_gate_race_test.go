package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCommercialCreateRechecksGateAfterQuotaLock(t *testing.T) {
	t.Run("transferred-ticket", func(t *testing.T) { checkFreeCreateDuringTicketChange(t, false) })
	t.Run("revoked-ticket", func(t *testing.T) { checkFreeCreateDuringTicketChange(t, true) })
}

func checkFreeCreateDuringTicketChange(t *testing.T, revoke bool) {
	resetCreateLimiter()
	t.Cleanup(resetCreateLimiter)
	noteImgSuccess()
	t.Cleanup(noteImgSuccess)
	fixtureJob, oldVisitor := commercialFixture(t, "done", "done")
	newVisitor, code := randHex(16), upperCode("RACEGATE")
	mustExec(t, "INSERT INTO visitors(id) VALUES($1)", newVisitor)
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,bound_visitor) VALUES($1,1,$2)", code, oldVisitor)
	t.Cleanup(func() {
		mustExec(t, "DELETE FROM audit_events WHERE actor=$1", "visitor:"+oldVisitor)
		mustExec(t, "DELETE FROM jobs WHERE visitor=$1 AND id<>$2", oldVisitor, fixtureJob)
		mustExec(t, "DELETE FROM invite_codes WHERE code=$1", code)
		mustExec(t, "DELETE FROM visitors WHERE id=$1", newVisitor)
	})
	cl, base := commercialServer(t)
	cl.Timeout = 6 * time.Second
	ctx, cancel := context.WithTimeout(contextBG(), 5*time.Second)
	defer cancel()
	lockTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockTx.Rollback(contextBG())
	var holderPID int
	if err := lockTx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&holderPID); err != nil {
		t.Fatal(err)
	}
	if _, err := lockTx.Exec(ctx, "SELECT pg_advisory_xact_lock(197704,1)"); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/api/jobs", strings.NewReader(`{"cn_name":"转绑竞态","industry":"咖啡"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "192.0.2.61")
	req.AddCookie(commercialCookie(visitorCookie, oldVisitor, 0))
	req.AddCookie(commercialCookie(gateCookie, code, 0))
	type result struct {
		status int
		err    error
	}
	done := make(chan result, 1)
	go func() {
		resp, e := cl.Do(req)
		if e != nil {
			done <- result{err: e}
			return
		}
		_, e = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		done <- result{status: resp.StatusCode, err: e}
	}()
	// pg_blocking_pids 确认请求已通过初始 gate 检查，正等待本测试持有的额度锁。
	deadline := time.Now().Add(2 * time.Second)
	for {
		var blocked bool
		if err := lockTx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted
			AND $1::int=ANY(pg_blocking_pids(pid)))`, holderPID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case got := <-done:
			t.Fatalf("请求未到达额度锁就返回: status=%d err=%v", got.status, got.err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("未观察到请求等待额度锁，无法建立转绑竞态")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// 同一次提交让转绑生效并放开锁，接下来的事务校验必须读取新的归属。
	if revoke {
		_, err = lockTx.Exec(ctx, "UPDATE invite_codes SET revoked=true WHERE code=$1", code)
	} else {
		_, err = lockTx.Exec(ctx, "UPDATE invite_codes SET bound_visitor=$2 WHERE code=$1", code, newVisitor)
	}
	if err != nil { t.Fatal(err) }
	if err := lockTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.status != http.StatusCreated {
			t.Errorf("旧票变化不能阻断新免费品牌，得到%d", got.status)
		}
	case <-ctx.Done():
		t.Fatal("释放额度锁后请求未结束")
	}
	var created int
	if err := pool.QueryRow(contextBG(), "SELECT COUNT(*) FROM jobs WHERE visitor=$1 AND id<>$2", oldVisitor, fixtureJob).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if created != 1 {
		t.Fatalf("应该只创建一个免费品牌，得到%d", created)
	}
	var jobCode, quota string; var trial bool
	if err := pool.QueryRow(contextBG(), "SELECT code,COALESCE(quota_code,''),trial FROM jobs WHERE visitor=$1 AND id<>$2", oldVisitor, fixtureJob).Scan(&jobCode,&quota,&trial); err != nil { t.Fatal(err) }
	if jobCode!="" || quota!="" || !trial { t.Fatalf("免费品牌误关联旧票: code=%q quota=%q trial=%v", jobCode,quota,trial) }
	var bound string; var used,max int; var revoked bool
	if err := pool.QueryRow(contextBG(), "SELECT bound_visitor,used,max_uses,revoked FROM invite_codes WHERE code=$1",code).Scan(&bound,&used,&max,&revoked); err != nil { t.Fatal(err) }
	wantBound:=newVisitor; if revoke { wantBound=oldVisitor }
	if bound!=wantBound || used!=0 || max!=1 || revoked!=revoke { t.Fatalf("创建免费品牌改变原票权益: bound=%q used=%d max=%d revoked=%v",bound,used,max,revoked) }
}
