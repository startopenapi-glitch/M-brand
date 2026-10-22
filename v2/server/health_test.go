package app

// health_test.go — V5：
//  1) 熔断：额度告警 → 暂停收款与新建任务；恢复后放行（熔断优先于收钱）
//  2) 试用默认 1 次（合同）
//  3) 找回也接受邀请码（票随码走）
//  4) 上架自动 WebP（cwebp 可用时；失败退回原图不阻断）

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCommerceFreeBrandHasNoSessionQuota(t *testing.T) {
	if os.Getenv("ZEROBRAND_TRIAL_PER_SESSION") == "" && trialPerSession() != 0 {
		t.Fatalf("新品牌不应限制每会话免费次数: %d", trialPerSession())
	}
}

func TestV5BreakerPausesSales(t *testing.T) {
	resetCreateLimiter()
	enablePayMockV2(t)
	srv, base := v2Server(t)
	cl := v2Client(srv)
	noteImgSuccess() // 起点干净
 t.Cleanup(noteImgSuccess)
 jid:=itemCheckoutFixture(t,cl,base)

	if _, p := getJSON(t, cl, base+"/api/plan"); p["sales_paused"] != false {
		t.Fatalf("初始不应熔断: %v", p["sales_paused"])
	}
	noteImgFailure(`方向 1 生成失败: 生图接口 HTTP 429: {"code":"API_KEY_QUOTA_EXHAUSTED","message":"额度已用完"}`)
	if paused, _ := salesPaused(); !paused {
		t.Fatal("额度告警应立即熔断")
	}
	if _, p := getJSON(t, cl, base+"/api/plan"); p["sales_paused"] != true {
		t.Fatalf("plan 应体现熔断: %v", p)
	}
	if st, o := postJSONv2(t, cl, base+"/api/jobs", `{"cn_name":"熔断测试","industry":"精制茶饮"}`); st != 503 {
		t.Fatalf("熔断中建任务应 503: %d %v", st, o)
	}
	if st, o := postJSONv2(t, cl, base+"/api/pay/create", `{"job_id":"`+jid+`","items":["logo_h"]}`); st != 503 {
		t.Fatalf("熔断中收款应 503: %d %v", st, o)
	}

	noteImgSuccess() // 通道恢复
	if _, p := getJSON(t, cl, base+"/api/plan"); p["sales_paused"] != false {
		t.Fatal("恢复后不应熔断")
	}
	st, o := postJSONv2(t, cl, base+"/api/pay/create", `{"job_id":"`+jid+`","items":["logo_h"]}`)
	if st != 200 && st != 201 {
		t.Fatalf("恢复后收款应可用: %d %v", st, o)
	}
	if oid, _ := o["id"].(string); oid != "" {
		mustExec(t, "DELETE FROM orders WHERE id=$1", oid)
	}
	noteImgSuccess()
}

func TestV5RecoverByInviteCode(t *testing.T) {
	resetCreateLimiter()
	srv, base := v2Server(t)
	cl := v2Client(srv)
	// 未绑定且未使用的码：可凭码找回（等价兑换，绑定本会话）
	code := upperCode("RCV5")
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,used) VALUES($1,1,0)", code)
	defer mustExec(t, "DELETE FROM invite_codes WHERE code=$1", code)
	st, o := postJSONv2(t, cl, base+"/api/recover", `{"order_id":"`+code+`"}`)
	if st != 200 || o["code"] != code {
		t.Fatalf("凭未使用码找回失败: %d %v", st, o)
	}
	if o["remaining"] != float64(1) {
		t.Fatalf("剩余次数应为 1: %v", o["remaining"])
	}
	var bound string
	pool.QueryRow(contextBG(), "SELECT COALESCE(bound_visitor,'') FROM invite_codes WHERE code=$1", code).Scan(&bound)
	if bound == "" {
		t.Fatal("找回应把码绑定到当前会话")
	}
	mustExec(t, "DELETE FROM visitors WHERE id=$1", bound)

	// 已使用的码：不可凭码找回（一码一人，防抢绑）
	code2 := upperCode("RCV5B")
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,used) VALUES($1,1,1)", code2)
	defer mustExec(t, "DELETE FROM invite_codes WHERE code=$1", code2)
	if st2, _ := postJSONv2(t, cl, base+"/api/recover", `{"order_id":"`+code2+`"}`); st2 != 403 {
		t.Fatalf("已使用码凭码找回应 403: %d", st2)
	}
	if st3, _ := postJSONv2(t, cl, base+"/api/recover", `{"order_id":"NOSUCHCODE1"}`); st3 != 404 {
		t.Fatalf("无效码应 404: %d", st3)
	}
}

func TestV5PublishCaseAutoWebP(t *testing.T) {
	if _, err := os.Stat("/usr/bin/cwebp"); err != nil {
		if _, err2 := os.Stat("/usr/local/bin/cwebp"); err2 != nil {
			t.Skip("cwebp 不可用，跳过")
		}
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	admin := r.Group("/0brand/api/admin", func(c *gin.Context) { c.Set("adminName", "unittest"); c.Next() })
	admin.POST("/cases", adminPublishCase)
	srv := httptest.NewServer(r)
	defer srv.Close()

	jid := newID(12)
	dir := filepath.Join(genDir(), jid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	mustExec(t, `INSERT INTO jobs(id, code, form, status, stage,unlocked_packs) VALUES($1,'','{}','done','done',ARRAY['boards'])`, jid)
	defer mustExec(t, "DELETE FROM jobs WHERE id=$1", jid)
	defer mustExec(t, "DELETE FROM job_images WHERE job_id=$1", jid)
	for i := 1; i <= 4; i++ {
		mkTestPNG(t, filepath.Join(dir, fmt.Sprintf("board_%d.png", i)))
		mustExec(t, `INSERT INTO job_images(job_id,kind,idx,file,tokens) VALUES($1,'board',$2,$3,0)`,
			jid, i, fmt.Sprintf("gen/%s/board_%d.png", jid, i))
	}
	payload := fmt.Sprintf(`{"job_id":"%s","title":"V5测试案例","tagline":"t"}`, jid)
	resp, err := srv.Client().Post(srv.URL+"/0brand/api/admin/cases", "application/json", bytes.NewReader([]byte(payload)))
	if err != nil {
		t.Fatal(err)
	}
	rb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("上架应 201: %d %s", resp.StatusCode, string(rb))
	}
	defer mustExec(t, "DELETE FROM cases WHERE job_id=$1", jid)
	var data []byte
	if err := pool.QueryRow(contextBG(), "SELECT images FROM cases WHERE job_id=$1 ORDER BY id DESC LIMIT 1", jid).Scan(&data); err != nil {
		t.Fatal(err)
	}
	var list []struct{ File string }
	if err := json.Unmarshal(data, &list); err != nil || len(list) != 4 {
		t.Fatalf("案例图片解析失败: %v %s", err, string(data))
	}
	for _, it := range list {
		if filepath.Ext(it.File) != ".webp" {
			t.Fatalf("上架应指向 webp: %s", it.File)
		}
		full := filepath.Join(genDir(), filepath.Base(filepath.Dir(it.File)), filepath.Base(it.File))
		if _, err := os.Stat(full); err != nil {
			t.Fatalf("webp 文件不存在: %s (%v)", it.File, err)
		}
	}
}

func mkTestPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for x := 0; x < 64; x++ {
		for y := 0; y < 64; y++ {
			img.Set(x, y, color.RGBA{uint8(x * 4), uint8(y * 4), 120, 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
