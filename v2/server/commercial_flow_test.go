package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestCommercialRecoverMovesPurchasedDelivery(t *testing.T) {
	resetRateWindow()
	t.Cleanup(resetRateWindow)
	jid, oldVID := commercialFixture(t, "done", "done")
	code, oid, newVID := upperCode("CMRC"), "0BTEST"+randHex(10), randHex(16)
	mustExec(t, "INSERT INTO visitors(id) VALUES($1)", newVID)
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,used,bound_visitor) VALUES($1,1,1,$2)", code, oldVID)
	mustExec(t, "INSERT INTO orders(id,code,amount_cents,channel,status,credited) VALUES($1,$2,6900,'native','paid',true)", oid, code)
	mustExec(t, "UPDATE jobs SET quota_code=$2 WHERE id=$1", jid, code)
	t.Cleanup(func() {
		mustExec(t, "DELETE FROM orders WHERE id=$1", oid)
		mustExec(t, "DELETE FROM audit_events WHERE object_id=$1", code)
		mustExec(t, "DELETE FROM invite_codes WHERE code=$1", code)
		mustExec(t, "DELETE FROM visitors WHERE id=$1", newVID)
	})
	cl, base := commercialServer(t)
	ck := commercialCookie(visitorCookie, newVID, 0)
	st, body := commercialRequest(t, cl, "POST", base+"/api/recover", fmt.Sprintf(`{"order_id":%q}`, oid), ck)
	if st != 200 {
		t.Fatalf("找回应成功: %d %s", st, body)
	}
	st, _ = commercialRequest(t, cl, "GET", base+"/api/jobs/"+jid, "", ck)
	if st != 200 {
		t.Errorf("找回后应能读取已购提案，得到%d", st)
	}
	st, body = commercialRequest(t, cl, "GET", base+"/api/my/jobs", "", ck)
	if st != 200 || !strings.Contains(body, jid) {
		t.Errorf("找回后的提案列表缺少已购任务: %d", st)
	}
	oldCL, _ := commercialServer(t)
	st, _ = commercialRequest(t, oldCL, "GET", base+"/api/jobs/"+jid, "", commercialCookie(visitorCookie, oldVID, 0), commercialCookie(gateCookie, code, 0))
	if st != 404 {
		t.Errorf("迁移后的旧设备不应继续读取，得到%d", st)
	}
}

func TestCommercialFreeBrandShortRateLimitIsAtomic(t *testing.T) {
	resetCreateLimiter()
	t.Cleanup(resetCreateLimiter)
	t.Setenv("ZEROBRAND_TRIAL_PER_SESSION", "1")
	t.Setenv("ZEROBRAND_TRIAL_PER_IP", "1")
	t.Setenv("ZEROBRAND_TRIAL_DAILY_CAP", "100000")
	_, vid := commercialFixture(t, "done", "done")
	// 把两个请求固定重叠在插入窗口，旧版查询后插入会重复通过额度检查。
	name := "commercial_trial_" + randHex(5)
	mustExec(t, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.visitor='%s' THEN PERFORM pg_sleep(0.2); END IF; RETURN NEW; END $$`, name, vid))
	mustExec(t, fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON jobs FOR EACH ROW EXECUTE FUNCTION %s()`, name, name))
	t.Cleanup(func() {
		mustExec(t, "DROP TRIGGER "+name+" ON jobs")
		mustExec(t, "DROP FUNCTION "+name+"()")
		mustExec(t, "DELETE FROM audit_events WHERE actor=$1", "visitor:"+vid)
		mustExec(t, "DELETE FROM jobs WHERE visitor=$1", vid)
	})
	cl, base := commercialServer(t)
	ch := make(chan int, 6)
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, _ := commercialRequest(t, cl, "POST", base+"/api/jobs", `{"cn_name":"并发测试","industry":"品牌"}`, commercialCookie(visitorCookie, vid, 0))
			ch <- st
		}()
	}
	wg.Wait()
	close(ch)
	successes := 0
	for st := range ch {
		if st == 201 {
			successes++
		} else if st != 429 {
			t.Errorf("网络并发限速应429，得到%d", st)
		}
	}
	if successes != 3 {
		t.Errorf("网络限速必须原子预约，成功%d次", successes)
	}
}

func TestCommercialAPIAndAssetMissesStayStrict(t *testing.T) {
	r := buildRouter()
	for _, path := range []string{"/0brand/api/no-such-endpoint", "/0brand/assets/nope.js", "/0brand/gen", "/0brandx"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Errorf("%s 应404而非SPA壳，得到%d", path, w.Code)
		}
	}
}

func TestCommercialCrossOriginWritesAreRejected(t *testing.T) {
	r := buildRouter()
	for _, origin := range []string{"https://evil.example", "null", "https://shop.example.attacker.test"} {
		req := httptest.NewRequest("PUT", "https://shop.example/0brand/api/draft", strings.NewReader(`{}`))
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("跨站Origin %s应403，得到%d", origin, w.Code)
		}
	}
	// 真实渠道通知没有浏览器Origin，不应被CSRF入口误拦。
	req := httptest.NewRequest("POST", "https://shop.example/0brand/api/pay/notify", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == 403 {
		t.Error("无Origin渠道通知不应被CSRF拦截")
	}
}

func TestCommercialModelProbeDoesNotProveGenerationRecovery(t *testing.T) {
	t.Cleanup(noteImgSuccess)
	noteImgSuccess()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ZEROBRAND_IMG_BASE_URL", srv.URL)
	t.Setenv("ZEROBRAND_IMG_API_KEY", "test-only")
	noteImgFailure("API_KEY_QUOTA_EXHAUSTED")
	ok, _ := probeImgChannel()
	if !ok {
		t.Fatal("模型列表应可达")
	}
	if paused, _ := salesPaused(); !paused {
		t.Error("可列模型不代表生图额度恢复，不应据此解除收款熔断")
	}
}

func TestCommercialRecoveryCannotTakeDifferentPaidTicket(t *testing.T) {
	resetRateWindow()
	t.Cleanup(resetRateWindow)
	jid, owner := commercialFixture(t, "done", "done")
	oldCode, newCode, oid, claimant := upperCode("CMOLD"), upperCode("CMNEW"), "0BTEST"+randHex(10), randHex(16)
	mustExec(t, "INSERT INTO visitors(id) VALUES($1)", claimant)
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,used,bound_visitor) VALUES($1,1,1,$3),($2,1,1,$3)", oldCode, newCode, owner)
	mustExec(t, "INSERT INTO orders(id,code,amount_cents,channel,status,credited) VALUES($1,$2,6900,'native','paid',true)", oid, oldCode)
	mustExec(t, "UPDATE jobs SET code=$2,quota_code=$3 WHERE id=$1", jid, oldCode, newCode)
	t.Cleanup(func() {
		mustExec(t, "DELETE FROM orders WHERE id=$1", oid)
		mustExec(t, "DELETE FROM audit_events WHERE object_id=$1", oldCode)
		mustExec(t, "DELETE FROM invite_codes WHERE code IN ($1,$2)", oldCode, newCode)
		mustExec(t, "DELETE FROM visitors WHERE id=$1", claimant)
	})
	cl, base := commercialServer(t)
	ck := commercialCookie(visitorCookie, claimant, 0)
	st, _ := commercialRequest(t, cl, "POST", base+"/api/recover", fmt.Sprintf(`{"order_id":%q}`, oid), ck)
	if st != 200 {
		t.Fatalf("合法旧订单找回失败:%d", st)
	}
	st, _ = commercialRequest(t, cl, "GET", base+"/api/jobs/"+jid, "", ck)
	if st != 404 {
		t.Errorf("旧订单凭证不能接管另一张票支付的提案:%d", st)
	}
	var actual string
	mustQueryErr := pool.QueryRow(contextBG(), "SELECT visitor FROM jobs WHERE id=$1", jid).Scan(&actual)
	if mustQueryErr != nil || actual != owner {
		t.Error("不同付款票的提案归属被错误迁移")
	}
}

func TestCommercialMalformedPaymentCreatesNoOrder(t *testing.T) {
	enablePayMockV2(t)
	resetCreateLimiter()
	t.Cleanup(resetCreateLimiter)
	cl, base := commercialServer(t)
	var before, after int
	pool.QueryRow(contextBG(), "SELECT count(*) FROM orders").Scan(&before)
	st, _ := commercialRequest(t, cl, "POST", base+"/api/pay/create", `{"channel":`)
	pool.QueryRow(contextBG(), "SELECT count(*) FROM orders").Scan(&after)
	if st != 400 || before != after {
		t.Errorf("无效JSON不得创建订单: status=%d before=%d after=%d", st, before, after)
	}
}

func TestCommercialInvalidAdminPayloadIsReadOnly(t *testing.T) {
	code := upperCode("CMBAD")
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,bound_visitor) VALUES($1,1,'original-owner')", code)
	t.Cleanup(func() {
		mustExec(t, "DELETE FROM invite_codes WHERE code=$1", code)
		mustExec(t, "DELETE FROM audit_events WHERE object_id=$1", code)
	})
	cl, base := commercialServer(t)
	cookie := commercialCookie(adminCookie, "test-admin", 1)
	var before, after int
	pool.QueryRow(contextBG(), "SELECT count(*) FROM invite_codes").Scan(&before)
	st, _ := commercialRequest(t, cl, "POST", base+"/api/admin/codes", `{"count":`, cookie)
	pool.QueryRow(contextBG(), "SELECT count(*) FROM invite_codes").Scan(&after)
	if st != 400 || before != after {
		t.Errorf("坏JSON不得批量发码: status=%d delta=%d", st, after-before)
	}
	st, _ = commercialRequest(t, cl, "POST", base+"/api/admin/codes/"+code+"/bind", `{"visitor":`, cookie)
	var bound string
	pool.QueryRow(contextBG(), "SELECT COALESCE(bound_visitor,'') FROM invite_codes WHERE code=$1", code).Scan(&bound)
	if st != 400 || bound != "original-owner" {
		t.Errorf("坏JSON不得解绑已有票: status=%d bound=%q", st, bound)
	}
}

func TestCommercialOriginSchemeAndProxyTrust(t *testing.T) {
	cases := []struct {
		name, target, origin, remote, proto string
		want                                int
	}{
		{"tls-same", "https://shop.example", "https://shop.example", "198.51.100.1:1234", "", 404},
		{"tls-cross-scheme", "https://shop.example", "http://shop.example", "198.51.100.1:1234", "", 403},
		{"trusted-proxy", "http://shop.example", "https://shop.example", "127.0.0.1:1234", "https", 404},
		{"untrusted-proxy", "http://shop.example", "https://shop.example", "198.51.100.1:1234", "https", 403},
		{"invalid-proto", "http://shop.example", "http://shop.example", "127.0.0.1:1234", "https,http", 403},
		{"no-origin", "https://shop.example", "", "198.51.100.1:1234", "", 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", tc.target+"/0brand/api/absent", strings.NewReader(`{}`))
			req.RemoteAddr = tc.remote
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("X-Forwarded-Proto", tc.proto)
			w := httptest.NewRecorder()
			buildRouter().ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("Origin/proxy contract: got %d want %d", w.Code, tc.want)
			}
		})
	}
}
