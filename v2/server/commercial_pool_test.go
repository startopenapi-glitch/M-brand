package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 客服重绑后靠邀请码归属进入的用户：事务已经占用连接，归属复验不得再向池借连接。
func TestCommercialChooseOwnershipUsesTransactionConnection(t *testing.T) {
	original := pool
	config := original.Config()
	config.MaxConns, config.MinConns = 2, 0
	limited, err := pgxpool.NewWithConfig(contextBG(), config)
	if err != nil {
		t.Fatal(err)
	}
	pool = limited
	t.Cleanup(func() { pool = original; limited.Close() })
	jid, _ := commercialFixture(t, "ready_to_choose", "choose")
	visitor, code := randHex(16), upperCode("POOLBOUND")
	mustExec(t, "INSERT INTO visitors(id) VALUES($1)", visitor)
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,bound_visitor) VALUES($1,1,$2)", code, visitor)
	mustExec(t, "UPDATE jobs SET code=$2,trial=false WHERE id=$1", jid, code)
	t.Cleanup(func() {
		mustExec(t, "DELETE FROM invite_codes WHERE code=$1", code)
		mustExec(t, "DELETE FROM visitors WHERE id=$1", visitor)
	})
	cl, base := commercialServer(t)
	cl.Timeout = 5 * time.Second
	// 保留一个连接模拟其他正常请求；留给 choose 的另一个连接足够完成全部 SQL。
	ctx, cancel := context.WithTimeout(contextBG(), 5*time.Second)
	defer cancel()
	reserved, err := limited.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reserved.Release)
	req, err := http.NewRequest("POST", base+"/api/jobs/"+jid+"/choose", strings.NewReader(`{"direction":1}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(commercialCookie(visitorCookie, visitor, 0))
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
	var got result
	select {
	case got = <-done:
	case <-time.After(time.Second):
		t.Error("选择方向在仅需一个事务连接时被阻塞：可能持事务后再次向池借连接")
		// 给旧实现释放连接完成请求，避免 RED 用例把整个套件永久挂住。
		reserved.Release()
		select {
		case got = <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("释放保留连接后请求仍未结束")
		}
	}
	if got.err != nil || got.status != 200 {
		t.Fatalf("合法重绑持有人应可选择方向: status=%d err=%v", got.status, got.err)
	}
	var used int
	if err := pool.QueryRow(contextBG(), "SELECT used FROM invite_codes WHERE code=$1", code).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used != 1 {
		t.Fatalf("解锁应准确扣票一次，得到%d", used)
	}
}
