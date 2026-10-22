package app

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func uploadTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			im.Set(x, y, color.RGBA{R: byte(x), G: byte(y), B: 90, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestLogoReferenceEndToEnd(t *testing.T) {
	commercialRequireTestDB(t)
	resetCreateLimiter()
	t.Setenv("ZEROBRAND_TRIAL_PER_IP", "1000")
	t.Setenv("ZEROBRAND_TRIAL_DAILY_CAP", "1000")
	t.Setenv("ZEROBRAND_GEN_DIR", t.TempDir())
	srv := httptest.NewTLSServer(buildRouter())
	defer srv.Close()
	cl := v2Client(srv)
	base := srv.URL + "/0brand"

	raw := uploadTestPNG(t, 128, 128)
	var uploadBody bytes.Buffer
	mw := multipart.NewWriter(&uploadBody)
	part, _ := mw.CreateFormFile("file", "logo.png")
	_, _ = part.Write(raw)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", base+"/api/logo-reference", &uploadBody)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := cl.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var uploaded struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&uploaded)
	resp.Body.Close()
	if resp.StatusCode != 200 || uploaded.Token == "" {
		t.Fatalf("upload status=%d token=%q", resp.StatusCode, uploaded.Token)
	}

	form := `{"cn_name":"上传链路测试","industry":"茶饮","logo_ref":"` + uploaded.Token + `"}`
	st, out := postJSONv2(t, cl, base+"/api/jobs", form)
	if st != 201 {
		t.Fatalf("create status=%d out=%v", st, out)
	}
	jid, _ := out["id"].(string)
	if jid == "" {
		t.Fatal("missing job id")
	}
	var visitor, code string
	var formRaw []byte
	var chosen *int
	if err := pool.QueryRow(contextBG(), "SELECT COALESCE(visitor,''),COALESCE(code,''),form,chosen FROM jobs WHERE id=$1", jid).Scan(&visitor, &code, &formRaw, &chosen); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(filepath.Join(genDir(), "refs", visitor))
		mustExec(t, "DELETE FROM job_images WHERE job_id=$1", jid)
		mustExec(t, "DELETE FROM usage_events WHERE job_id=$1", jid)
		mustExec(t, "DELETE FROM jobs WHERE id=$1", jid)
		if visitor != "" {
			mustExec(t, "DELETE FROM visitors WHERE id=$1", visitor)
		}
	})
	var formMap BrandForm
	_ = json.Unmarshal(formRaw, &formMap)
	if formMap["logo_ref"] != "logo_ref.png" {
		t.Fatalf("bound ref=%q", formMap["logo_ref"])
	}
	if _, err := os.Stat(filepath.Join(jobDir(jid), "logo_ref.png")); err != nil {
		t.Fatalf("job ref missing: %v", err)
	}

	var mu sync.Mutex
	var imageCalls int
	commercialGenerator(t, func(r *http.Request) {
		mu.Lock()
		imageCalls++
		mu.Unlock()
		if r.URL.Path != "/images/edits" {
			return
		}
		_ = r.ParseMultipartForm(16 << 20)
		if len(r.MultipartForm.File["image"]) == 0 {
			t.Error("customer ref was not sent to image API")
		}
		if !strings.Contains(r.FormValue("prompt"), "客户 Logo 参考") {
			t.Error("customer ref instruction missing")
		}
	})
	j := jobRow{ID: jid, Code: code, Status: "queued", Stage: "directions", Form: formMap}
	if err := stageDirections(j); err != nil {
		t.Fatal(err)
	}
	if imageCalls != 3 {
		t.Fatalf("direction image calls=%d", imageCalls)
	}
	if st, choose := postJSONv2(t, cl, base+"/api/jobs/"+jid+"/choose", `{"direction":1}`); st != 200 {
		t.Fatalf("choose status=%d %v", st, choose)
	}
	var snapRaw []byte
	if err := pool.QueryRow(contextBG(), "SELECT direction_snapshot FROM jobs WHERE id=$1", jid).Scan(&snapRaw); err != nil {
		t.Fatal(err)
	}
	var chosenVal int
	if chosen != nil {
		chosenVal = *chosen
	}
	snap := decodeDirectionSnapshot(snapRaw)
	if snap == nil || snap.DirectionID != "B" || snap.Title == "" || snap.Presentation.LogoTreatment == "" {
		t.Fatalf("snapshot=%+v", snap)
	}
	// The formal Logo is a separate stage; it must be confirmed before the
	// application stage can start.
	j.Chosen = &chosenVal
	j.Stage = "logo"
	if err := stageLogo(j); err != nil {
		t.Fatal(err)
	}
	if st, body := postJSONv2(t, cl, base+"/api/jobs/"+jid+"/logo/confirm", `{}`); st != 200 {
		t.Fatalf("confirm status=%d %v", st, body)
	}
	var confirmed bool
	var nextStage string
	if err := pool.QueryRow(contextBG(), "SELECT logo_confirmed,stage FROM jobs WHERE id=$1", jid).Scan(&confirmed, &nextStage); err != nil {
		t.Fatal(err)
	}
	if !confirmed || nextStage != "boards" {
		t.Fatalf("confirm state=%v/%s", confirmed, nextStage)
	}
}
