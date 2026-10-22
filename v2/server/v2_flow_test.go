package app

// v2_flow_test.go — V2（进门不验票 / 正片验票 / 票随码走）回归：
//   试用免费出方向 → 选定需票（402）→ 兑换/付款得票 → 原子占票 → 失败返还
//   票=额度载体（同 SKILL：入账优先充值已有票）→ 找回 / 草稿 / 归属 / 价格单源
// 说明：路由挂在 /0brand 前缀下（与生产一致——会话 cookie 的 Path=basePath），
//       用 TLS 测试服务器（cookie 为 Secure），保证会话身份在测试里真实生效。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
 "net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

const priceCents = int64(6900) // Historical paid-order fixture, never a checkout price.

func v2Router() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	g := r.Group("/0brand")
	g.GET("/api/plan", planHandler)
	g.GET("/api/draft", draftGet)
	g.PUT("/api/draft", draftPut)
	g.POST("/api/recover", recoverOrder)
	g.POST("/api/code", submitCode)
	g.POST("/api/jobs", createJob)
	g.GET("/api/jobs/:id", jobState)
	g.POST("/api/jobs/:id/choose", chooseDirection)
	g.POST("/api/jobs/:id/retry", retryJob)
	g.GET("/api/my/jobs", myJobs)
	g.GET("/api/my/orders", myOrders)
	g.POST("/api/pay/create", payCreate)
	g.GET("/api/pay/status/:id", payOrderStatus)
	return r
}

// v2Client：显式 cookie jar + 服务器 TLS 信任（httptest.Server.Client() 不带 jar）
func v2Client(srv *httptest.Server) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Transport: srv.Client().Transport, Timeout: 10 * time.Second}
}

// v2Server：TLS 测试服务器 + /0brand 基址（会话 cookie 与生产同路径）
func v2Server(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewTLSServer(v2Router())
	t.Cleanup(srv.Close)
	return srv, srv.URL + "/0brand"
}

func enablePayMockV2(t *testing.T) {
	wxcfg.mu.Lock()
	wxcfg.mock = true
	wxcfg.mu.Unlock()
	t.Cleanup(func() { wxcfg.mu.Lock(); wxcfg.mock = false; wxcfg.mu.Unlock() })
}

func postJSONv2(t *testing.T, cl *http.Client, url, body string) (int, map[string]any) {
	t.Helper()
	resp, err := cl.Post(url, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func putJSONv2(t *testing.T, cl *http.Client, url, body string) int {
	t.Helper()
	req, _ := http.NewRequest("PUT", url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func mustExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(contextBG(), sql, args...); err != nil {
		t.Fatalf("SQL 失败: %v (%s)", err, sql)
	}
}

// 大写测试码（接口对输入做 ToUpper，入库须同为大写）
func upperCode(prefix string) string { return prefix + strings.ToUpper(randHex(2)) }

// New brands are free; counters remain analytics rather than eligibility.
func TestTrialEachNewBrandFree(t *testing.T) {
 resetCreateLimiter();t.Cleanup(resetCreateLimiter)
 t.Setenv("ZEROBRAND_TRIAL_PER_IP","100000");t.Setenv("ZEROBRAND_TRIAL_DAILY_CAP","100000")
 srv,base:=v2Server(t);cl:=v2Client(srv);var vid string;ids:=[]string{}
 t.Cleanup(func(){for _,id:=range ids{mustExec(t,"DELETE FROM audit_events WHERE object_id=$1",id);mustExec(t,"DELETE FROM jobs WHERE id=$1",id)};if vid!=""{mustExec(t,"DELETE FROM visitors WHERE id=$1",vid)}})
 for i:=0;i<3;i++{st,o:=postJSONv2(t,cl,base+"/api/jobs",`{"cn_name":"新品牌","industry":"茶饮"}`);if st!=201{t.Fatalf("brand %d: %d %v",i,st,o)};ids=append(ids,o["id"].(string))}
 if e:=pool.QueryRow(contextBG(),"SELECT visitor FROM jobs WHERE id=$1",ids[0]).Scan(&vid);e!=nil{t.Fatal(e)}
 var used int;pool.QueryRow(contextBG(),"SELECT trial_used FROM visitors WHERE id=$1",vid).Scan(&used);if used!=3{t.Fatalf("analytics count %d",used)}
}

// 正片验票：无票 402 → 兑换得票 → choose 占票 → 幂等 → 票尽再 402
func TestUnlockByTicketAndAtomicConsume(t *testing.T) {
	resetCreateLimiter()
	srv, base := v2Server(t)
	cl := v2Client(srv)

	form := `{"cn_name":"验票测试","industry":"精品烘焙"}`
	var jobs []string
	var vid string
	// 免费试用默认仅 1 次：首个任务走试用；第二个任务在兑换票之后再建（持票路径）
	st0, o0 := postJSONv2(t, cl, base+"/api/jobs", form)
	if st0 != 201 {
		t.Fatalf("建任务失败: %d %v", st0, o0)
	}
	jobs = append(jobs, o0["id"].(string))
	mustExec(t, "UPDATE jobs SET stage='choose', status='ready_to_choose' WHERE id=$1", jobs[0])
	pool.QueryRow(contextBG(), "SELECT COALESCE(visitor,'') FROM jobs WHERE id=$1", jobs[0]).Scan(&vid)
	defer func() {
		for _, jid := range jobs {
			mustExec(t, "DELETE FROM jobs WHERE id=$1", jid)
		}
		if vid != "" {
			mustExec(t, "DELETE FROM visitors WHERE id=$1", vid)
		}
	}()

	// New selection is free and does not invent paid rights.
 if st,o:=postJSONv2(t,cl,base+"/api/jobs/"+jobs[0]+"/choose",`{"direction":1}`);st!=200{t.Fatalf("free choose: %d %v",st,o)}
 if _,js:=getJSON(t,cl,base+"/api/jobs/"+jobs[0]);js["need_unlock"]!=false{t.Fatal("free system card must not request a ticket")}
 mustExec(t,"UPDATE jobs SET status='ready_to_choose',stage='choose' WHERE id=$1",jobs[0])
	// 2) 兑换一张 max_uses=1 的票
	code := upperCode("TESTTK")
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,used,note) VALUES($1,1,0,'test')", code)
	defer mustExec(t, "DELETE FROM invite_codes WHERE code=$1", code)
	if st, _ := postJSONv2(t, cl, base+"/api/code", `{"code":"`+code+`"}`); st != 200 {
		t.Fatalf("兑换应 200: %d", st)
	}
	var bound string
	pool.QueryRow(contextBG(), "SELECT COALESCE(bound_visitor,'') FROM invite_codes WHERE code=$1", code).Scan(&bound)
	if bound == "" || bound != vid {
		t.Fatalf("兑换应把票绑定本会话: bound=%q vid=%q", bound, vid)
	}
	// 3) choose 成功并占票
	if st, o := postJSONv2(t, cl, base+"/api/jobs/"+jobs[0]+"/choose", `{"direction":1}`); st != 200 {
		t.Fatalf("有票 choose 应 200: %d %v", st, o)
	}
	var used int
	pool.QueryRow(contextBG(), "SELECT used FROM invite_codes WHERE code=$1", code).Scan(&used)
	if used != 1 {
		t.Fatalf("占票后 used 应=1: %d", used)
	}
	// 幂等：重复 choose 不再扣
	if st, _ := postJSONv2(t, cl, base+"/api/jobs/"+jobs[0]+"/choose", `{"direction":1}`); st != 200 {
		t.Fatal("重复 choose 应幂等 200")
	}
	pool.QueryRow(contextBG(), "SELECT used FROM invite_codes WHERE code=$1", code).Scan(&used)
	if used != 1 {
		t.Fatalf("重复 choose 不应再扣: %d", used)
	}
	// 4) 票已用尽：第二个任务（持票会话建，不受试用上限影响）→ 402
	st1, o1 := postJSONv2(t, cl, base+"/api/jobs", form)
	if st1 != 201 {
		t.Fatalf("持票建任务失败: %d %v", st1, o1)
	}
	jobs = append(jobs, o1["id"].(string))
	mustExec(t, "UPDATE jobs SET stage='choose', status='ready_to_choose' WHERE id=$1", jobs[1])
	if st, _ := postJSONv2(t, cl, base+"/api/jobs/"+jobs[1]+"/choose", `{"direction":0}`); st != 200 {
		t.Fatalf("免费系统卡不占已用完票: %d", st)
	}
}

// 失败自动返还：只返一次
func TestFailureRefundsQuotaOnce(t *testing.T) {
	code := upperCode("TESTRF")
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,used,note) VALUES($1,1,1,'test')", code)
	jid := newID(12)
	mustExec(t, `INSERT INTO jobs(id, code, form, status, stage, quota_code, visitor)
		VALUES ($1,'','{}','boards_running','boards',$2,'')`, jid, code)
	defer func() {
		mustExec(t, "DELETE FROM jobs WHERE id=$1", jid)
		mustExec(t, "DELETE FROM invite_codes WHERE code=$1", code)
	}()
	markJobFailed(jid, "生成失败测试")
	var status string
	var refunded bool
	var used int
	pool.QueryRow(contextBG(), "SELECT status, refunded FROM jobs WHERE id=$1", jid).Scan(&status, &refunded)
	pool.QueryRow(contextBG(), "SELECT used FROM invite_codes WHERE code=$1", code).Scan(&used)
	if status != "error" || !refunded || used != 0 {
		t.Fatalf("失败应返还: status=%s refunded=%v used=%d", status, refunded, used)
	}
	markJobFailed(jid, "再次失败")
	pool.QueryRow(contextBG(), "SELECT used FROM invite_codes WHERE code=$1", code).Scan(&used)
	if used != 0 {
		t.Fatalf("不得重复返还: used=%d", used)
	}
}

// 额度用尽的票：原持有人仍可进入（身份与额度解耦）；他人不可（一码一人）
func TestExhaustedCodeStillEnters(t *testing.T) {
	resetCreateLimiter()
	resetRateWindow()
	defer resetRateWindow()
	srv, base := v2Server(t)
	cl := v2Client(srv)
	stJ, oJ := postJSONv2(t, cl, base+"/api/jobs", `{"cn_name":"用尽验证","industry":"精制茶饮"}`)
	if stJ != 201 {
		t.Fatalf("建任务失败: %d %v", stJ, oJ)
	}
	jid := oJ["id"].(string)
	var vid string
	pool.QueryRow(contextBG(), "SELECT COALESCE(visitor,'') FROM jobs WHERE id=$1", jid).Scan(&vid)
	code := upperCode("TESTEX")
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,used,bound_visitor,note) VALUES($1,1,1,$2,'test')", code, vid)
	defer func() {
		mustExec(t, "DELETE FROM invite_codes WHERE code=$1", code)
		mustExec(t, "DELETE FROM jobs WHERE id=$1", jid)
		if vid != "" {
			mustExec(t, "DELETE FROM visitors WHERE id=$1", vid)
		}
	}()
	if st, o := postJSONv2(t, cl, base+"/api/code", `{"code":"`+code+`"}`); st != 200 {
		t.Fatalf("原持有人进入用尽票应 200: %d %v", st, o)
	}
	srv2, base2 := v2Server(t)
	if st2, _ := postJSONv2(t, v2Client(srv2), base2+"/api/code", `{"code":"`+code+`"}`); st2 != 403 {
		t.Fatalf("他人兑换用尽票应 403: %d", st2)
	}
}

// 入账：一单一码 —— 每笔订单发一张新单次码并绑定会话；旧码不动
func TestSettleCreatesNewSingleUseCode(t *testing.T) {
	enablePayMockV2(t)
	resetCreateLimiter()
	srv, base := v2Server(t)
	cl := v2Client(srv)

	st, o := recordedLegacyOrder(t, cl, base)
	if st != 201 {
		t.Fatalf("下单失败: %d %v", st, o)
	}
	id1 := o["id"].(string)
	var codeBefore, vid string
	pool.QueryRow(contextBG(), "SELECT COALESCE(code,''), COALESCE(visitor,'') FROM orders WHERE id=$1", id1).Scan(&codeBefore, &vid)
	if codeBefore != "" {
		t.Fatalf("V2 下单时不应预建码: %q", codeBefore)
	}
	if vid == "" {
		t.Fatal("订单应记录归属会话")
	}
	if err := settleOrder(id1, "", "", int64(priceCents), "TXV2A"); err != nil {
		t.Fatal(err)
	}
	var code1 string
	var max1 int
	if err := pool.QueryRow(contextBG(),
		"SELECT c.code, c.max_uses FROM orders o JOIN invite_codes c ON c.code=o.code WHERE o.id=$1", id1).
		Scan(&code1, &max1); err != nil {
		t.Fatalf("入账后应可 JOIN 到票: %v", err)
	}
	if code1 == "" || max1 != 1 {
		t.Fatalf("首购应得单次票 max=1: code=%q max=%d", code1, max1)
	}
	var bound, oid string
	pool.QueryRow(contextBG(),
		"SELECT COALESCE(bound_visitor,''), COALESCE(order_id,'') FROM invite_codes WHERE code=$1", code1).Scan(&bound, &oid)
	if bound != vid {
		t.Fatalf("票应绑定购买会话: bound=%q vid=%q", bound, vid)
	}
	if oid != id1 {
		t.Fatalf("票应记录来源订单: %q vs %q", oid, id1)
	}
	if _, v := getJSON(t, cl, base+"/api/pay/status/"+id1); v["invite_code"] != code1 {
		t.Fatalf("查单应返回票号: %v", v["invite_code"])
	}
	// 二次购买 → 新的一张单次票（不再往旧票加次数）
	st2, o2 := recordedLegacyOrder(t, cl, base)
	if st2 != 201 {
		t.Fatalf("二次下单失败: %d %v", st2, o2)
	}
	id2 := o2["id"].(string)
	var code2 string
	defer func() {
		mustExec(t, "DELETE FROM orders WHERE id IN ($1,$2)", id1, id2)
		mustExec(t, "DELETE FROM invite_codes WHERE code IN ($1,$2)", code1, code2)
		mustExec(t, "DELETE FROM visitors WHERE id=$1", vid)
	}()
	var vid2 string
	pool.QueryRow(contextBG(), "SELECT COALESCE(visitor,'') FROM orders WHERE id=$1", id2).Scan(&vid2)
	if vid2 != vid {
		t.Fatalf("同一会话二次购买 visitor 应一致: %q vs %q", vid2, vid)
	}
	if err := settleOrder(id2, "", "", int64(priceCents), "TXV2B"); err != nil {
		t.Fatal(err)
	}
	pool.QueryRow(contextBG(), "SELECT COALESCE(code,'') FROM orders WHERE id=$1", id2).Scan(&code2)
	if code2 == "" || code2 == code1 {
		t.Fatalf("二次购买应发新码: code2=%q code1=%q", code2, code1)
	}
	var maxOld, maxNew int
	pool.QueryRow(contextBG(), "SELECT max_uses FROM invite_codes WHERE code=$1", code1).Scan(&maxOld)
	pool.QueryRow(contextBG(), "SELECT max_uses FROM invite_codes WHERE code=$1", code2).Scan(&maxNew)
	if maxOld != 1 || maxNew != 1 {
		t.Fatalf("两张都应为单次票: %d / %d", maxOld, maxNew)
	}
	var n int
	pool.QueryRow(contextBG(), "SELECT COUNT(*) FROM invite_codes WHERE bound_visitor=$1", vid).Scan(&n)
	if n != 2 {
		t.Fatalf("买两套应得两张独立票: %d", n)
	}
}

// 草稿：写入/读回（属于会话）
func TestDraftRoundTrip(t *testing.T) {
	srv, base := v2Server(t)
	cl := v2Client(srv)
	if st := putJSONv2(t, cl, base+"/api/draft", `{"cn_name":"草稿品牌","industry":"咖啡"}`); st != 200 {
		t.Fatalf("保存草稿应 200: %d", st)
	}
	resp, err := cl.Get(base + "/api/draft")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got map[string]string
	json.NewDecoder(resp.Body).Decode(&got)
	if got["cn_name"] != "草稿品牌" || got["industry"] != "咖啡" {
		t.Fatalf("草稿应完整读回: %v", got)
	}
	// 另一会话（新 cookie jar）看不到
	srv2, base2 := v2Server(t)
	resp2, err2 := v2Client(srv2).Get(base2 + "/api/draft")
	if err2 != nil {
		t.Fatal(err2)
	}
	defer resp2.Body.Close()
	var other map[string]string
	json.NewDecoder(resp2.Body).Decode(&other)
	if len(other) != 0 {
		t.Fatalf("他人不应读到草稿: %v", other)
	}
}

// 归属：另一个会话看不到他人任务
func TestJobOwnershipIsolation(t *testing.T) {
	resetCreateLimiter()
	srv, base := v2Server(t)
	clA := v2Client(srv)
	st, o := postJSONv2(t, clA, base+"/api/jobs", `{"cn_name":"归属测试","industry":"茶饮"}`)
	if st != 201 {
		t.Fatalf("建任务失败: %d %v", st, o)
	}
	jid := o["id"].(string)
	var vid string
	pool.QueryRow(contextBG(), "SELECT COALESCE(visitor,'') FROM jobs WHERE id=$1", jid).Scan(&vid)
	defer func() {
		mustExec(t, "DELETE FROM jobs WHERE id=$1", jid)
		if vid != "" {
			mustExec(t, "DELETE FROM visitors WHERE id=$1", vid)
		}
	}()
	if st, _ := getJSON(t, clA, base+"/api/jobs/"+jid); st != 200 {
		t.Fatalf("本会话应可见: %d", st)
	}
	// 新会话（独立 cookie jar，同一 TLS 信任）→ 404
	jar, _ := cookiejar.New(nil)
	clB := &http.Client{Jar: jar, Transport: srv.Client().Transport, Timeout: 5 * time.Second}
	if st, _ := getJSON(t, clB, base+"/api/jobs/"+jid); st != 404 {
		t.Fatalf("他人任务应 404: %d", st)
	}
}

// 找回：凭订单号取票并绑定；未支付/不存在分别 403/404
func TestRecoverByOrderID(t *testing.T) {
	enablePayMockV2(t)
	resetCreateLimiter()
	srv, base := v2Server(t)
	cl := v2Client(srv)
	st, o := recordedLegacyOrder(t, cl, base)
	if st != 201 {
		t.Fatalf("下单失败: %d %v", st, o)
	}
	oid := o["id"].(string)
	defer mustExec(t, "DELETE FROM orders WHERE id=$1", oid)
	if st, _ := postJSONv2(t, cl, base+"/api/recover", `{"order_id":"`+oid+`"}`); st != 403 {
		t.Fatalf("未支付订单找回应 403: %d", st)
	}
	if err := settleOrder(oid, "", "", int64(priceCents), "TXREC1"); err != nil {
		t.Fatal(err)
	}
	var code, vid string
	pool.QueryRow(contextBG(), "SELECT COALESCE(code,''), COALESCE(visitor,'') FROM orders WHERE id=$1", oid).Scan(&code, &vid)
	defer func() {
		mustExec(t, "DELETE FROM invite_codes WHERE code=$1", code)
		if vid != "" {
			mustExec(t, "DELETE FROM visitors WHERE id=$1", vid)
		}
	}()
	// 另一个会话找回
	srv2, base2 := v2Server(t)
	st2, o2 := postJSONv2(t, v2Client(srv2), base2+"/api/recover", `{"order_id":"`+oid+`"}`)
	if st2 != 200 || o2["code"] != code {
		t.Fatalf("找回应返回票号: %d %v", st2, o2)
	}
	if st3, _ := postJSONv2(t, v2Client(srv2), base2+"/api/recover", `{"order_id":"0BNOSUCHORDER000"}`); st3 != 404 {
		t.Fatalf("不存在订单应 404: %d", st3)
	}
}

// 价格/权益单源
func TestPlanSingleSource(t *testing.T) {
	srv, base := v2Server(t)
	st, p := getJSON(t, v2Client(srv), base+"/api/plan")
	if st != 200 || p["price_label"] != "¥99.00" {
		t.Fatalf("plan 应返回全选 ¥99.00: %d %v", st, p)
	}
	if p["price_fen"] != float64(bundlePriceCents) {
		t.Fatalf("plan price_fen 应与后端一致: %v vs %d", p["price_fen"], bundlePriceCents)
	}
	if _, ok := p["includes"]; !ok {
		t.Fatal("plan 应包含权益清单")
	}
}

// Existing sold orders still settle/recover; fixtures are not a new legacy purchase API.
func recordedLegacyOrder(t *testing.T, cl *http.Client, base string) (int,map[string]any) {
 t.Helper();putJSONv2(t,cl,base+"/api/draft",`{}`)
 u,_:=url.Parse(base);vid:=""
 for _,ck:=range cl.Jar.Cookies(u){if ck.Name==visitorCookie{if p:=testParseVisitorCookie(ck);p!=nil{vid=p.Code}}}
 if vid==""{t.Fatal("recorded-order fixture requires a visitor")}
 id:="0BOLD"+randHex(10)
 mustExec(t,`INSERT INTO orders(id,code,amount_cents,channel,status,mock,visitor,code_url) VALUES($1,'',6900,'native','pending',true,$2,'wxp://mock')`,id,vid)
 t.Cleanup(func(){mustExec(t,"DELETE FROM orders WHERE id=$1",id);mustExec(t,"DELETE FROM invite_codes WHERE order_id=$1",id)})
 return 201,map[string]any{"id":id,"amount_cents":float64(6900)}
}
