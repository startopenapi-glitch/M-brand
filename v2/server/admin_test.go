package app

// admin_test.go — 管理端列表接口回归：
// 曾因把 timestamptz 直接扫进 string（pgx 二进制格式下失败 → 空串 → created[:19] 越界 panic → 500）
// 本测试锁定：三个列表接口必须 200 且包含格式化的 created_at。

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAdminListEndpointsReturn200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	admin := r.Group("/0brand/api/admin", func(c *gin.Context) { c.Set("adminName", "unittest"); c.Next() })
	admin.GET("/jobs", adminListJobs)
	admin.GET("/codes", adminListCodes)
	admin.GET("/audit", adminAudit)
	srv := httptest.NewServer(r)
	defer srv.Close()

	// 造数据，确保列表非空（时间戳路径一定被走到）
	code := upperCode("ADMT")
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,note) VALUES($1,1,'admin test')", code)
	defer mustExec(t, "DELETE FROM invite_codes WHERE code=$1", code)
	mustExec(t, "INSERT INTO audit_events(actor,action,object_type,object_id) VALUES('unittest','unit','invite_code',$1)", code)
	defer mustExec(t, "DELETE FROM audit_events WHERE actor='unittest'")
	jid := newID(12)
	mustExec(t, `INSERT INTO jobs(id, code, form, status, stage) VALUES($1,'','{}','queued','directions')`, jid)
	defer mustExec(t, "DELETE FROM jobs WHERE id=$1", jid)

	cl := srv.Client()
	for _, ep := range []string{"/0brand/api/admin/jobs", "/0brand/api/admin/codes", "/0brand/api/admin/audit"} {
		resp, err := cl.Get(srv.URL + ep)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s 应 200（回归：曾 500）: %d %s", ep, resp.StatusCode, truncate(string(body), 160))
		}
		if !strings.Contains(string(body), "created_at") {
			t.Fatalf("%s 响应应含 created_at: %s", ep, truncate(string(body), 160))
		}
		if strings.Contains(string(body), `"created_at":""`) {
			t.Fatalf("%s created_at 不得为空串（时间戳扫描失败的症状）", ep)
		}
	}
}
