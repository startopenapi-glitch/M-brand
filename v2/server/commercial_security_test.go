package app

// 商用安全回归：均使用测试库与临时图片目录，不启动生图/支付 worker。
import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func commercialFixture(t *testing.T, status, stage string) (string, string) {
	t.Helper()
	jid, vid := newID(12), randHex(16)
	t.Cleanup(func() {
		for _, q := range []string{
			"DELETE FROM audit_events WHERE object_id=$1",
			"DELETE FROM usage_events WHERE job_id=$1",
			"DELETE FROM job_images WHERE job_id=$1",
			"DELETE FROM cases WHERE job_id=$1",
			"DELETE FROM jobs WHERE id=$1",
		} {
			if _, err := pool.Exec(contextBG(), q, jid); err != nil {
				t.Errorf("清理安全测试任务失败: %v", err)
			}
		}
		mustExec(t, "DELETE FROM drafts WHERE visitor_id=$1", vid)
		mustExec(t, "DELETE FROM visitors WHERE id=$1", vid)
	})
	mustExec(t, "INSERT INTO visitors(id) VALUES($1)", vid)
	mustExec(t, `INSERT INTO jobs(id,code,form,status,stage,visitor,trial) VALUES($1,'','{}',$2,$3,$4,true)`, jid, status, stage, vid)
	return jid, vid
}

// 保留旧版 Cookie 格式：权限修复不能让既有合法用户会话失效。
func commercialCookie(name, code string, admin int) *http.Cookie {
	return &http.Cookie{Name: name, Value: makeCookieValue(gatePayload{Code: code, Admin: admin, Expiry: time.Now().Add(time.Hour).Unix()})}
}

func commercialServer(t *testing.T) (*http.Client, string) {
	t.Helper()
	srv := httptest.NewTLSServer(buildRouter())
	t.Cleanup(srv.Close)
	return v2Client(srv), srv.URL + basePath
}

func commercialRequest(t *testing.T, cl *http.Client, method, url, body string, cookies ...*http.Cookie) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	resp, err := cl.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(raw)
}

// 把普通访客 Cookie 改名为管理员 Cookie，不得获得他人任务的读写权限。
func TestCommercialVisitorCookieCannotAuthorizeJobs(t *testing.T) {
	cl, base := commercialServer(t)
	for _, tc := range []struct{ name, method, suffix, body, status, stage string }{
		{"read", "GET", "", "", "error", "directions"},
		{"choose", "POST", "/choose", `{"direction":0}`, "ready_to_choose", "choose"},
		{"retry", "POST", "/retry", `{}`, "error", "directions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jid, _ := commercialFixture(t, tc.status, tc.stage)
			spoof := commercialCookie(adminCookie, randHex(16), 0)
			st, _ := commercialRequest(t, cl, tc.method, base+"/api/jobs/"+jid+tc.suffix, tc.body, spoof)
			if st != 404 {
				t.Errorf("普通访客令牌不能授权他人任务，应404，得到%d", st)
			}
			var status string
			if err := pool.QueryRow(contextBG(), "SELECT status FROM jobs WHERE id=$1", jid).Scan(&status); err != nil {
				t.Fatal(err)
			}
			if status != tc.status {
				t.Errorf("越权请求改变了任务状态: %s -> %s", tc.status, status)
			}
		})
	}
}

func TestCommercialExistingOwnerAndAdminCookiesStillWork(t *testing.T) {
	jid, vid := commercialFixture(t, "done", "done")
	cl, base := commercialServer(t)
	for _, ck := range []*http.Cookie{commercialCookie(visitorCookie, vid, 0), commercialCookie(adminCookie, "test-admin", 1)} {
		st, _ := commercialRequest(t, cl, "GET", base+"/api/jobs/"+jid, "", ck)
		if st != 200 {
			t.Errorf("既有合法%s会话应能读取任务，得到%d", ck.Name, st)
		}
	}
}

func TestCommercialGateRequiresBoundVisitor(t *testing.T) {
	_, vid := commercialFixture(t, "done", "done")
	code, rebound, revoked := upperCode("SECBOUND"), upperCode("SECREBIND"), upperCode("SECREVOKE")
	t.Cleanup(func() { mustExec(t, "DELETE FROM invite_codes WHERE code=ANY($1)", []string{code, rebound, revoked}) })
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,bound_visitor) VALUES($1,1,$2),($3,1,'other-device')", code, vid, rebound)
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,bound_visitor,revoked) VALUES($1,1,$2,true)", revoked, vid)
	for _, tc := range []struct {
		name    string
		gate    *http.Cookie
		visitor *http.Cookie
		want    bool
	}{
		{"valid_existing_gate", commercialCookie(gateCookie, code, 0), commercialCookie(visitorCookie, vid, 0), true},
		{"renamed_visitor", commercialCookie(gateCookie, vid, 0), commercialCookie(visitorCookie, vid, 0), false},
		{"renamed_admin", commercialCookie(gateCookie, code, 1), commercialCookie(visitorCookie, vid, 0), false},
		{"missing_code", commercialCookie(gateCookie, "NOT_A_REAL_CODE", 0), commercialCookie(visitorCookie, vid, 0), false},
		{"empty_code", commercialCookie(gateCookie, "", 0), commercialCookie(visitorCookie, vid, 0), false},
		{"wrong_visitor", commercialCookie(gateCookie, code, 0), commercialCookie(visitorCookie, randHex(16), 0), false},
		{"no_visitor", commercialCookie(gateCookie, code, 0), nil, false},
		{"revoked", commercialCookie(gateCookie, revoked, 0), commercialCookie(visitorCookie, vid, 0), false},
		{"rebound_to_other_device", commercialCookie(gateCookie, rebound, 0), commercialCookie(visitorCookie, vid, 0), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("GET", "https://example.test"+basePath+"/api/my/orders", nil)
			c.Request.AddCookie(tc.gate)
			if tc.visitor != nil {
				c.Request.AddCookie(tc.visitor)
			}
			if got := gateOK(c) != nil; got != tc.want {
				t.Errorf("gate归属校验应%v，得到%v", tc.want, got)
			}
		})
	}
}

func TestCommercialVisitorRejectsAdminCookie(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		admin      int
		want       string
	}{
		{"valid_existing_visitor", "legacy-visitor", 0, "legacy-visitor"},
		{"renamed_admin", "test-admin", 1, ""},
		{"negative_admin", "test-admin", -1, ""},
		{"empty_visitor", "", 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("GET", "https://example.test"+basePath+"/api/draft", nil)
			c.Request.AddCookie(commercialCookie(visitorCookie, tc.code, tc.admin))
			if visitorID(c) != tc.want {
				t.Error("visitor Cookie角色校验错误")
			}
		})
	}
}

func TestCommercialPrivateMediaAccess(t *testing.T) {
	jid, vid := commercialFixture(t, "done", "done")
 mustExec(t,"UPDATE jobs SET unlocked_packs=ARRAY['all'] WHERE id=$1",jid)
	dir := t.TempDir()
	t.Setenv("ZEROBRAND_GEN_DIR", dir)
	if err := os.MkdirAll(filepath.Join(dir, jid), 0o755); err != nil {
		t.Fatal(err)
	}
	mkTestPNG(t, filepath.Join(dir, jid, "board_1.png"))
	file := "gen/" + jid + "/board_1.png"
	mustExec(t, `INSERT INTO job_images(job_id,kind,idx,file,tokens) VALUES($1,'board',1,$2,0)`, jid, file)
	cl, base := commercialServer(t)
	for _, tc := range []struct {
		name, path string
		cookie     *http.Cookie
		want       int
	}{
		{"root_listing", "/gen/", nil, 404},
		{"job_listing", "/gen/" + jid + "/", nil, 404},
		{"anonymous", "/" + file, nil, 404},
		{"other_visitor", "/" + file, commercialCookie(visitorCookie, randHex(16), 0), 404},
		{"renamed_visitor", "/" + file, commercialCookie(adminCookie, randHex(16), 0), 404},
		{"owner", "/" + file, commercialCookie(visitorCookie, vid, 0), 200},
		{"admin", "/" + file, commercialCookie(adminCookie, "test-admin", 1), 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cookies []*http.Cookie
			if tc.cookie != nil {
				cookies = append(cookies, tc.cookie)
			}
			st, body := commercialRequest(t, cl, "GET", base+tc.path, "", cookies...)
			if st != tc.want {
				t.Errorf("图片访问应%d，得到%d", tc.want, st)
			}
			if tc.want == 404 && (strings.Contains(body, jid) || strings.Contains(body, "board_1.png")) {
				t.Error("拒绝响应不能泄露文件目录")
			}
		})
	}
}

func TestCommercialPublishedCaseOnlyExposesListedImage(t *testing.T) {
	jid, _ := commercialFixture(t, "done", "done")
	dir := t.TempDir()
	t.Setenv("ZEROBRAND_GEN_DIR", dir)
	if err := os.MkdirAll(filepath.Join(dir, jid), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"board_1.png", "system_card.png"} {
		mkTestPNG(t, filepath.Join(dir, jid, name))
	}
	publicFile := "gen/" + jid + "/board_1.png"
	images, err := json.Marshal([]map[string]string{{"file": publicFile, "label": "公开案例"}})
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, `INSERT INTO cases(job_id,title,tagline,images,published) VALUES($1,'安全测试','',$2,true)`, jid, images)
	cl, base := commercialServer(t)
	st, body := commercialRequest(t, cl, "GET", base+"/"+publicFile, "")
	if st != 200 || !strings.HasPrefix(body, "\x89PNG") {
		t.Fatalf("公开案例图应可匿名读取，状态%d", st)
	}
	st, _ = commercialRequest(t, cl, "HEAD", base+"/"+publicFile, "")
	if st != 200 {
		t.Errorf("公开案例图HEAD应200，得到%d", st)
	}
	st, _ = commercialRequest(t, cl, "GET", base+"/gen/"+jid+"/system_card.png", "")
	if st != 404 {
		t.Errorf("发布案例不应公开同任务其他图片，得到%d", st)
	}
	mustExec(t, "UPDATE cases SET published=false WHERE job_id=$1", jid)
	st, _ = commercialRequest(t, cl, "GET", base+"/"+publicFile, "")
	if st != 404 {
		t.Errorf("案例下架后匿名读取应404，得到%d", st)
	}
}

func TestCommercialMediaRejectsTraversalAndSymlinks(t *testing.T) {
	jid, vid := commercialFixture(t, "done", "done")
	otherJob, _ := commercialFixture(t, "done", "done")
	dir, outside := t.TempDir(), t.TempDir()
	t.Setenv("ZEROBRAND_GEN_DIR", dir)
	for _, id := range []string{jid, otherJob} {
		if err := os.MkdirAll(filepath.Join(dir, id), 0o755); err != nil {
			t.Fatal(err)
		}
		mkTestPNG(t, filepath.Join(dir, id, "board_1.png"))
	}
	mkTestPNG(t, filepath.Join(outside, "board_1.png"))
	for _, link := range []struct{ target, name string }{
		{filepath.Join(outside, "board_1.png"), filepath.Join(dir, jid, "outside.png")},
		{filepath.Join(dir, otherJob, "board_1.png"), filepath.Join(dir, jid, "other.png")},
		{outside, filepath.Join(dir, "linked-job")},
	} {
		if err := os.Symlink(link.target, link.name); err != nil {
			t.Fatal(err)
		}
	}
	cl, base := commercialServer(t)
	cl.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	for _, path := range []string{
		"/gen/" + jid + "/../" + otherJob + "/board_1.png",
		"/gen/" + jid + "/%2e%2e/" + otherJob + "/board_1.png",
		"/gen/" + jid + "/outside.png",
		"/gen/" + jid + "/other.png",
		"/gen/linked-job/board_1.png",
	} {
		t.Run(path, func(t *testing.T) {
			st, body := commercialRequest(t, cl, "GET", base+path, "", commercialCookie(visitorCookie, vid, 0))
			if st != 404 || strings.HasPrefix(body, "\x89PNG") {
				t.Errorf("非规范路径或符号链接必须404且不返回图片，得到%d", st)
			}
		})
	}
}

type commercialCountingBody struct {
	io.Reader
	read int
}

func (b *commercialCountingBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}
func (b *commercialCountingBody) Close() error { return nil }

func TestCommercialRequestBodiesAreLimitedBeforeReading(t *testing.T) {
	_, vid := commercialFixture(t, "done", "done")
	resetRateWindow()
	t.Cleanup(resetRateWindow)
	r := buildRouter()
	// cn_name 留空避免旧代码产生生成任务；正文仍合法且足够大来证明流式上限。
	payload := `{"cn_name":"","industry":"咖啡","extra":"` + strings.Repeat("x", 2<<20) + `"}`
	for _, tc := range []struct {
		name, method, path string
		limit              int
	}{
		{"draft", "PUT", "/api/draft", 16 << 10},
		{"create", "POST", "/api/jobs", 64 << 10},
	} {
		for _, knownLength := range []bool{true, false} {
			name := tc.name + "_chunked"
			if knownLength {
				name = tc.name + "_content_length"
			}
			t.Run(name, func(t *testing.T) {
				body := &commercialCountingBody{Reader: strings.NewReader(payload)}
				req := httptest.NewRequest(tc.method, "https://example.test"+basePath+tc.path, body)
				req.Header.Set("Content-Type", "application/json")
				req.AddCookie(commercialCookie(visitorCookie, vid, 0))
				req.ContentLength = -1
				if knownLength {
					req.ContentLength = int64(len(payload))
				}
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if w.Code != 413 {
					t.Errorf("超限正文应413，得到%d", w.Code)
				}
				if body.read > tc.limit+1 {
					t.Errorf("拒绝前已读取%d字节，超过安全上限%d+1", body.read, tc.limit)
				}
			})
		}
	}
	var drafts int
	if err := pool.QueryRow(contextBG(), "SELECT COUNT(*) FROM drafts WHERE visitor_id=$1", vid).Scan(&drafts); err != nil {
		t.Fatal(err)
	}
	if drafts != 0 {
		t.Error("超限正文不能写入草稿")
	}
}

func TestCommercialJobErrorDoesNotExposeUpstreamDetails(t *testing.T) {
	jid, vid := commercialFixture(t, "error", "directions")
	internal := `生图接口 HTTP 502: provider-internal-marker-91 https://private-upstream.invalid/debug`
	mustExec(t, "UPDATE jobs SET error=$2 WHERE id=$1", jid, internal)
	cl, base := commercialServer(t)
	st, body := commercialRequest(t, cl, "GET", base+"/api/jobs/"+jid, "", commercialCookie(visitorCookie, vid, 0))
	if st != 200 {
		t.Fatalf("任务状态应200，得到%d", st)
	}
	if strings.Contains(body, "provider-internal-marker-91") || strings.Contains(body, "private-upstream.invalid") {
		t.Error("用户接口泄露原始上游错误")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if msg, _ := out["user_error"].(string); msg == "" {
		t.Error("隐藏技术细节后仍应提供用户可读的失败说明")
	}
	if out["can_retry"] != true {
		t.Error("失败重试入口不应随错误清理消失")
	}
	st, adminBody := commercialRequest(t, cl, "GET", base+"/api/admin/jobs", "", commercialCookie(adminCookie, "test-admin", 1))
	if st != 200 || !strings.Contains(adminBody, "provider-internal-marker-91") {
		t.Error("受保护管理端应保留排障信息")
	}
}
