package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func commercialRequireTestDB(t *testing.T) {
	t.Helper()
	var name string
	if err := pool.QueryRow(contextBG(), "SELECT current_database()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "zerobrand_test" {
		t.Fatalf("commercial regressions require zerobrand_test, got %q", name)
	}
}

func commercialJob(t *testing.T, status, stage string, chosen int) jobRow {
	t.Helper()
	commercialRequireTestDB(t)
	t.Setenv("ZEROBRAND_GEN_DIR", t.TempDir())
	j := jobRow{ID: "COMM" + newID(16), Status: status, Stage: stage, Chosen: &chosen,
		Form: normalizeForm(BrandForm{"cn_name": "商用回归", "industry": "茶饮"})}
	raw, err := json.Marshal(j.Form)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, `INSERT INTO jobs(id,code,form,status,stage,chosen)
		VALUES($1,'',$2,$3,$4,$5)`, j.ID, raw, status, stage, chosen)
	t.Cleanup(func() {
		mustExec(t, "DELETE FROM usage_events WHERE job_id=$1", j.ID)
		mustExec(t, "DELETE FROM job_images WHERE job_id=$1", j.ID)
		mustExec(t, "DELETE FROM jobs WHERE id=$1", j.ID)
	})
	return j
}

func commercialPNG(t *testing.T, red byte) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 4, 5))
	for y := 0; y < 5; y++ {
		for x := 0; x < 4; x++ {
			im.Set(x, y, color.RGBA{R: red, G: byte(x * 40), B: byte(y * 30), A: 255})
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, im); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func commercialWritePNG(t *testing.T, jid, name string, raw []byte) {
	t.Helper()
	if err := os.MkdirAll(jobDir(jid), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir(jid), name), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func commercialGenerator(t *testing.T, inspect func(*http.Request)) {
	t.Helper()
	raw := commercialPNG(t, 77)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if inspect != nil {
			inspect(r)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(raw)}},
			"usage": map[string]int{"total_tokens": 1},
		})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ZEROBRAND_IMG_BASE_URL", srv.URL)
	t.Setenv("ZEROBRAND_IMG_API_KEY", "commercial-test-only")
	t.Setenv("ZEROBRAND_IMG_MODEL", "commercial-test-only")
	t.Setenv("ZEROBRAND_IMG_FALLBACK_BASE_URL", "")
	t.Setenv("ZEROBRAND_IMG_FALLBACK_API_KEY", "")
	t.Cleanup(noteImgSuccess)
}

// Catches a restart abandoning the paid boards_running execution state.
func TestCommercialWorkersRecoverBoardGeneration(t *testing.T) {
	j := commercialJob(t, "boards_running", "boards", 1)
	if err := recoverInterruptedJobs(); err != nil {
		t.Fatal(err)
	}
	var status, stage string
	if err := pool.QueryRow(contextBG(), "SELECT status,stage FROM jobs WHERE id=$1", j.ID).
		Scan(&status, &stage); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || stage != "boards" {
		t.Fatalf("restart must resume paid boards work, got status=%s stage=%s", status, stage)
	}
}

// Catches creating a fresh system-card logo instead of preserving the selected
// direction. DB display paths may be WebP, while the edit reference remains PNG.
func TestCommercialSystemCardUsesSelectedDirection(t *testing.T) {
	j := commercialJob(t, "running", "boards", 2)
	var selected []byte
	for i := 0; i < 3; i++ {
		raw := commercialPNG(t, byte(40+i*70))
		commercialWritePNG(t, j.ID, fmt.Sprintf("direction_%d.png", i), raw)
		mustExec(t, `INSERT INTO job_images(job_id,kind,idx,file,tokens)
			VALUES($1,'direction',$2,$3,0)`, j.ID, i, webPath(j.ID, fmt.Sprintf("direction_%d.webp", i)))
		if i == 2 {
			selected = raw
		}
	}
	var mu sync.Mutex
	var firstPath string
	var firstImages [][]byte
	var inspectErr error
	commercialGenerator(t, func(r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if firstPath != "" {
			return
		}
		firstPath = r.URL.Path
		if firstPath != "/images/edits" {
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			inspectErr = err
			return
		}
		defer r.MultipartForm.RemoveAll()
		for _, headers := range r.MultipartForm.File {
			for _, header := range headers {
				f, err := header.Open()
				if err != nil {
					inspectErr = err
					return
				}
				raw, err := io.ReadAll(f)
				f.Close()
				if err != nil {
					inspectErr = err
					return
				}
				firstImages = append(firstImages, raw)
			}
		}
	})
	if err := stageBoardsV3(j); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if inspectErr != nil {
		t.Fatal(inspectErr)
	}
	if firstPath != "/images/edits" || len(firstImages) != 1 || !bytes.Equal(firstImages[0], selected) {
		t.Fatalf("system card must edit selected direction 2 PNG; path=%s references=%d", firstPath, len(firstImages))
	}
}

// Catches DecodeString("") succeeding and silently creating an empty PNG even
// though the upstream response supplied a usable URL.
func TestCommercialURLImageResponseDownloadsActualImage(t *testing.T) {
	raw := commercialPNG(t, 131)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/asset.png" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(raw)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  []map[string]string{{"url": srv.URL + "/asset.png"}},
			"usage": map[string]int{"total_tokens": 9},
		})
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "image.png")
	conf := imgConfig{base: srv.URL, key: "test", model: "test", size: "1024x1280", timeout: time.Second}
	result, err := conf.doGenerate("test", dest, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Tokens != 9 || !bytes.Equal(got, raw) {
		t.Fatalf("URL-only response must persist its real image, got %d bytes, want %d", len(got), len(raw))
	}
}

// Catches successful delivery of empty bytes or base64-encoded non-images.
func TestCommercialInvalidImageResponseCannotSucceed(t *testing.T) {
	for _, tc := range []struct {
		name, body string
	}{
		{"empty_image", `{"data":[{}]}`},
		{"non_image_base64", `{"data":[{"b64_json":"bm90IGFuIGltYWdl"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			dest := filepath.Join(t.TempDir(), "image.png")
			conf := imgConfig{base: srv.URL, key: "test", model: "test", size: "1024x1280", timeout: time.Second}
			if result, err := conf.doGenerate("test", dest, nil); err == nil || result != nil {
				t.Errorf("invalid image must fail before delivery; result=%v error=%v", result, err)
			}
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Errorf("invalid response must not create final artifact, stat error=%v", err)
			}
		})
	}
}

// Catches recordImage swallowing the final board INSERT failure while declaring
// the paid proposal done. The constraint is scoped to this test's unique job.
func TestCommercialImageRecordFailureCannotCompleteJob(t *testing.T) {
	j := commercialJob(t, "running", "boards", 1)
 grantTestItems(t,j.ID,"logo_h")
 commercialWritePNG(t,j.ID,"logo_h.png",commercialPNG(t,20))
	raw := commercialPNG(t, 85)
	commercialWritePNG(t, j.ID, "system_card.png", raw)
	mustExec(t, `INSERT INTO job_images(job_id,kind,idx,file,tokens)
		VALUES($1,'system',0,$2,0)`, j.ID, webPath(j.ID, "system_card.png"))
	for _, n := range []int{1, 2, 3} {
		name := fmt.Sprintf("board_%d.png", n)
		commercialWritePNG(t, j.ID, name, raw)
		mustExec(t, `INSERT INTO job_images(job_id,kind,idx,file,tokens)
			VALUES($1,'board',$2,$3,0)`, j.ID, n, webPath(j.ID, name))
	}
	constraint := "commercial_image_failure_" + newID(12)
	mustExec(t, fmt.Sprintf(`ALTER TABLE job_images ADD CONSTRAINT %s
		CHECK (job_id <> '%s' OR kind <> 'logo_h') NOT VALID`, constraint, j.ID))
	t.Cleanup(func() { mustExec(t, "ALTER TABLE job_images DROP CONSTRAINT IF EXISTS "+constraint) })
	commercialGenerator(t, nil)
	err := stageBoardsV3(j)
	if err == nil {
		t.Error("failed image persistence must propagate an error to the worker")
	}
	var status string
	if scanErr := pool.QueryRow(contextBG(), "SELECT status FROM jobs WHERE id=$1", j.ID).Scan(&status); scanErr != nil {
		t.Fatal(scanErr)
	}
	if status == "done" {
		t.Error("job with a missing persisted board must not be marked done")
	}
}

// Catches spending another provider request after a process died between
// successful disk persistence and recording that same artifact in PostgreSQL.
func TestCommercialExistingPNGsResumeWithoutRegeneration(t *testing.T) {
	j := commercialJob(t, "running", "boards", 2)
	raw := commercialPNG(t, 29)
	commercialWritePNG(t, j.ID, "system_card.png", raw)
 mustExec(t,"UPDATE jobs SET unlocked_packs=ARRAY['all'] WHERE id=$1",j.ID)
 for _,spec:=range itemSpecs(j.Form){if spec.Kind!="brand_story"{commercialWritePNG(t,j.ID,spec.Kind+".png",raw)}}
 if err:=os.WriteFile(filepath.Join(jobDir(j.ID),"brand_story.md"),[]byte("# 已完成的品牌故事\n"),0644);err!=nil{t.Fatal(err)}
	var requests atomic.Int32
	commercialGenerator(t, func(*http.Request) { requests.Add(1) })
	if err := stageBoardsV3(j); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Errorf("already generated PNGs must be registered without new model calls, got %d", requests.Load())
	}
	// Re-entering the stage also must neither regenerate nor duplicate records.
	if err := stageBoardsV3(j); err != nil {
		t.Fatal(err)
	}
	var total, distinct int
	if err := pool.QueryRow(contextBG(), `SELECT COUNT(*),COUNT(DISTINCT (kind,idx))
		FROM job_images WHERE job_id=$1`, j.ID).Scan(&total, &distinct); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 || total != 18 || distinct != 18 {
		t.Fatalf("resume must deliver eighteen distinct persisted images without model calls, calls=%d total=%d distinct=%d", requests.Load(), total, distinct)
	}
}

// This lossless 2x2 WebP fixture has one opaque and one half-transparent row.
// Catches rejecting valid provider WebP or retaining mislabeled WebP bytes in a
// .png file that later gets uploaded as an image/png edit reference.
func TestCommercialWebPResponseBecomesValidPNG(t *testing.T) {
	const fixture = "UklGRiYAAABXRUJQVlA4TBoAAAAvAUAAEA9QohcxX0T1f4BRQSGAAChoIvofAw=="
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"b64_json":"`+fixture+`"}]}`)
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "image.png")
	conf := imgConfig{base: srv.URL, key: "test", model: "test", size: "1024x1280", timeout: time.Second}
	if _, err := conf.doGenerate("test", dest, nil); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	im, err := png.Decode(f)
	if err != nil {
		t.Fatalf("saved .png must be decodable PNG, got %v", err)
	}
	if im.Bounds().Dx() != 2 || im.Bounds().Dy() != 2 {
		t.Fatalf("unexpected image dimensions: %v", im.Bounds())
	}
	_, _, _, alpha := im.At(0, 1).RGBA()
	if alpha != 0x8080 {
		t.Fatalf("WebP transparency must survive PNG conversion, alpha=%#x", alpha)
	}
}
