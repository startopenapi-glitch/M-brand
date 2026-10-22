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
	"testing"
)

func TestLogoReferenceUploadAndAttach(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZEROBRAND_GEN_DIR", dir)
	srv := httptest.NewTLSServer(buildRouter())
	defer srv.Close()
	cl := v2Client(srv)

	im := image.NewRGBA(image.Rect(0, 0, 128, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			im.Set(x, y, color.RGBA{R: byte(x), G: byte(y), B: 80, A: 255})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, im); err != nil { t.Fatal(err) }
	rawPNG := pngBuf.Bytes()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "logo.png")
	if err != nil { t.Fatal(err) }
	_, _ = part.Write(rawPNG)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/0brand/api/logo-reference", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := cl.Do(req)
	if err != nil { t.Fatal(err) }
	defer resp.Body.Close()
	if resp.StatusCode != 200 { t.Fatalf("upload HTTP %d", resp.StatusCode) }
	var out struct{ Token string `json:"token"` }
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Token == "" { t.Fatalf("token=%q err=%v", out.Token, err) }
	parts := bytes.Split([]byte(out.Token), []byte("/"))
	if len(parts) != 2 { t.Fatalf("token shape %q", out.Token) }
	vid, rel := string(parts[0]), string(parts[1])
	jid := "LOGOREFJOB"
	if err := os.MkdirAll(filepath.Join(dir, jid), 0o700); err != nil { t.Fatal(err) }
	if err := attachLogoReference(vid, jid, out.Token); err != nil { t.Fatal(err) }
	if st, err := os.Stat(filepath.Join(dir, jid, "logo_ref.png")); err != nil || st.Size() == 0 { t.Fatalf("copied ref missing: %v", err) }
	if rel == "" { t.Fatal("empty rel") }
}
