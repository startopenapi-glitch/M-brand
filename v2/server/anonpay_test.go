package app

// anonpay_test.go — 匿名支付闭环回归：无邀请码下单 → 入账自动发码 → 公开查单拿码 → 码可兑换
// 依赖 TestMain 的 ZEROBRAND_PG_URL 隔离库守卫（与 wxpay_test.go 共用）。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
 "net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func payTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.POST("/api/code", submitCode)
	r.POST("/api/pay/create", payCreate)
	r.POST("/api/pay/create/gated", func(c *gin.Context) { fakeGate(c, "ANONGATE"); payCreate(c) })
	r.GET("/api/pay/status/:id", payOrderStatus)
	r.GET("/api/pay/order/:id", payOrderDetail)
	r.POST("/api/pay/orders/:id/confirm", payConfirm)
	return r
}

func resetCreateLimiter() {
	createMu.Lock()
	createAt = map[string][]time.Time{}
	createMu.Unlock()
}

func enablePayMock(t *testing.T) {
	wxcfg.mu.Lock()
	wxcfg.mock = true
	wxcfg.mu.Unlock()
	t.Cleanup(func() { wxcfg.mu.Lock(); wxcfg.mock = false; wxcfg.mu.Unlock() })
}

func postJSON(t *testing.T, cl *http.Client, url, body string) (int, map[string]any) {
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

func getJSON(t *testing.T, cl *http.Client, url string) (int, map[string]any) {
	t.Helper()
	resp, err := cl.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// Anonymous visitor checkout is scoped to their project; no purchase creates a voucher.
func itemCheckoutFixture(t *testing.T,cl *http.Client,base string) string {
 t.Helper();putJSONv2(t,cl,base+"/api/draft",`{}`);u,_:=url.Parse(base);vid:=""
 for _,ck:=range cl.Jar.Cookies(u){if ck.Name==visitorCookie{if p:=testParseVisitorCookie(ck);p!=nil{vid=p.Code}}}
 if vid==""{t.Fatal("visitor missing")}
 jid:=newID(12);mustExec(t,`INSERT INTO jobs(id,code,form,status,stage,visitor,chosen) VALUES($1,'','{}','done','done',$2,0)`,jid,vid)
 t.Cleanup(func(){mustExec(t,"DELETE FROM order_items WHERE order_id IN (SELECT id FROM orders WHERE job_id=$1)",jid);mustExec(t,"DELETE FROM orders WHERE job_id=$1",jid);mustExec(t,"DELETE FROM jobs WHERE id=$1",jid);mustExec(t,"DELETE FROM drafts WHERE visitor_id=$1",vid);mustExec(t,"DELETE FROM visitors WHERE id=$1",vid)})
 return jid
}
func TestAnonPayFullLoop(t *testing.T) {
 enablePayMock(t);resetCreateLimiter();srv,base:=v2Server(t);cl:=v2Client(srv);jid:=itemCheckoutFixture(t,cl,base)
 st,o:=postJSON(t,cl,base+"/api/pay/create",`{"job_id":"`+jid+`","items":["logo_h"]}`)
 if st!=201||o["qr"]==nil{t.Fatalf("checkout %d %v",st,o)};id:=o["id"].(string)
 if len(id)>32{t.Fatal("merchant id too long")}
 cents:=int64(o["amount_cents"].(float64));if err:=settleOrder(id,"","",cents,"TX"+id);err!=nil{t.Fatal(err)}
 if st,p:=getJSON(t,cl,base+"/api/pay/status/"+id);st!=200||p["status"]!="paid"||p["invite_code"]!=nil{t.Fatalf("paid view %d %v",st,p)}
 rights,e:=jobEntitlements(contextBG(),pool,jid);if e!=nil||!rights["logo_h"]||rights["business_card"]{t.Fatalf("wrong rights %v %v",rights,e)}
 var n int;pool.QueryRow(contextBG(),"SELECT COUNT(*) FROM invite_codes WHERE order_id=$1",id).Scan(&n);if n!=0{t.Fatal("item purchase minted legacy voucher")}
}
func TestAnonCreateRateLimit(t *testing.T) {
 enablePayMock(t);resetCreateLimiter();srv,base:=v2Server(t);cl:=v2Client(srv)
 for i:=0;i<4;i++{jid:=itemCheckoutFixture(t,cl,base);st,o:=postJSON(t,cl,base+"/api/pay/create",`{"job_id":"`+jid+`","items":["logo_h"]}`);want:=201;if i==3{want=429};if st!=want{t.Fatalf("order %d: %d %v",i,st,o)}}
}
func TestGatedReuseUnaffected(t *testing.T) {
 enablePayMock(t);resetCreateLimiter();srv,base:=v2Server(t);cl:=v2Client(srv);jid:=itemCheckoutFixture(t,cl,base)
 body:=`{"job_id":"`+jid+`","items":["logo_h"]}`
 st,a:=postJSON(t,cl,base+"/api/pay/create",body);if st!=201{t.Fatalf("create %d %v",st,a)}
 for i:=0;i<5;i++{st,b:=postJSON(t,cl,base+"/api/pay/create",body);if st!=200||b["id"]!=a["id"]{t.Fatalf("reuse %d %v",st,b)}}
 var n int;pool.QueryRow(contextBG(),"SELECT COUNT(*) FROM orders WHERE job_id=$1",jid).Scan(&n);if n!=1{t.Fatalf("orders %d",n)}
 if st,_:=postJSON(t,cl,base+"/api/pay/create",`{"channel":"native"}`);st!=400{t.Fatalf("obsolete checkout must reject, got %d",st)}
}

// 未知订单：公开端点一律 404
func TestPublicOrderNotFound(t *testing.T) {
	resetCreateLimiter()
	srv := httptest.NewServer(payTestRouter())
	defer srv.Close()
	cl := srv.Client()
	for _, path := range []string{"/api/pay/status/NOSUCHORDER0001", "/api/pay/order/NOSUCHORDER0001"} {
		if st, _ := getJSON(t, cl, srv.URL+path); st != 404 {
			t.Fatalf("%s 应 404: %d", path, st)
		}
	}
	if st, _ := postJSON(t, cl, srv.URL+"/api/pay/orders/NOSUCHORDER0001/confirm", `{}`); st != 404 {
		t.Fatalf("confirm 未知订单应 404: %d", st)
	}
}

func testParseVisitorCookie(ck *http.Cookie)*gatePayload {c,_:=gin.CreateTestContext(httptest.NewRecorder());c.Request=httptest.NewRequest("GET","https://test.invalid",nil);c.Request.AddCookie(ck);return readCookie(c,visitorCookie)}
