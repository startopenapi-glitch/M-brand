package app

import (
 "context"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminUpgradePagedSearchBeyond300(t *testing.T) {
	commercialRequireTestDB(t)
	prefix := "ADMINPAGE" + strings.ToUpper(randHex(5))
	t.Cleanup(func() { pool.Exec(contextBG(), "DELETE FROM orders WHERE left(id,length($1))=$1", prefix) })
	for i := 0; i < 310; i++ {
		mustExec(t, `INSERT INTO orders(id,amount_cents,channel,status,mock,code,created_at) VALUES($1,100,'native','paid',$2,$3,now()-($4::int * interval '1 second'))`, fmt.Sprintf("%s%03d", prefix, i), i%2 == 0, prefix+"CODE", i)
	}
	router := gin.New()
	router.GET("/orders", adminOrderPage)
	get := func(query string) map[string]any {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/orders?"+query, nil))
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		var v map[string]any
		json.Unmarshal(w.Body.Bytes(), &v)
		return v
	}
	v := get("q=" + prefix + "&include_mock=1&page=2&page_size=50")
	if v["total"] != float64(310) || len(v["items"].([]any)) != 50 {
		t.Fatalf("pagination: %v", v)
	}
	summary := v["summary"].(map[string]any)
	if summary["revenue_cents"] != float64(15500) {
		t.Fatal(summary)
	}
	v = get("q=" + prefix + "309&include_mock=1")
	if v["total"] != float64(1) {
		t.Fatal("old order missing")
	}
	v = get("q=" + prefix + "CODE&include_mock=1")
	if v["total"] != float64(310) {
		t.Fatal("code search missing")
	}
	v = get("q=" + prefix)
	if v["total"] != float64(155) {
		t.Fatal("mock leak")
	}
	for _, q := range []string{"page=-1", "page_size=1000", "sort=drop", "from=bad", "from=2026-10-22&to=2026-01-01"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/orders?"+q, nil))
		if w.Code != 400 {
			t.Fatalf("invalid %s => %d", q, w.Code)
		}
	}
}
func TestAdminUpgradeSessionRequiresExistingAdmin(t *testing.T) {
	r := gin.New()
	r.GET("/session", requireAdmin(), adminSession)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/session", nil))
	if w.Code != 403 {
		t.Fatalf("unauth %d", w.Code)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("adminName", "test")
	adminSession(c)
}

func TestAdminUpgradeTaskLinkQueriesBeforeLimit(t *testing.T) {
	vid := reviewFlowVisitor(t)
	prefix := "ADMINLINK" + randHex(5)
	for i := 0; i < 105; i++ {
		mustExec(t, `INSERT INTO jobs(id,visitor,status,stage,form,created_at) VALUES($1,$2,'done','done','{}',now()-($3::int*interval '1 day'))`, fmt.Sprintf("%s%03d", prefix, i), vid, i)
	}
	r := gin.New()
	r.GET("/jobs", adminListJobs)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/jobs?id="+prefix+"104", nil))
	var rows []map[string]any
	json.Unmarshal(w.Body.Bytes(), &rows)
	if len(rows) != 1 || rows[0]["id"] != prefix+"104" {
		t.Fatalf("related old task missing: count %d", len(rows))
	}
}

func TestAdminUpgradeAuthStoreFailureIsNotLogout(t *testing.T){
 commercialRequireTestDB(t);t.Setenv("ZEROBRAND_ADMIN_2FA","1")
 ctx,cancel:=context.WithCancel(context.Background());cancel()
 req:=httptest.NewRequest("GET","/session",nil).WithContext(ctx);req.AddCookie(commercialCookie(adminCookie,"acceptance",1))
 r:=gin.New();r.GET("/session",requireAdmin(),adminSession);w:=httptest.NewRecorder();r.ServeHTTP(w,req)
 if w.Code!=503{t.Fatalf("unavailable auth store must not report logged out: %d %s",w.Code,w.Body.String())}
}
