package app

// Optional customer-supplied Logo reference for the free direction stage.
// Files are private (under generated/refs, never served by serveGenerated),
// validated by decoding and re-encoding, and copied into the job directory.
import (
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const maxLogoReferenceBytes = 8 << 20

func uploadLogoReference(c *gin.Context) {
	vid := ensureVisitor(c)
	if !rateOK("logo-ref:"+vid, 5) || !rateOK("logo-ref-ip:"+c.ClientIP(), 20) {
		c.JSON(429, gin.H{"error": "上传过于频繁，请稍后再试"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxLogoReferenceBytes+1)
	file, err := c.FormFile("file")
	if err != nil || file.Size <= 0 || file.Size > maxLogoReferenceBytes {
		c.JSON(400, gin.H{"error": "请上传 8MB 以内的 PNG/JPG Logo 参考图"})
		return
	}
	f, err := file.Open()
	if err != nil { c.JSON(400, gin.H{"error":"参考图读取失败"}); return }
	defer f.Close()
	// Check dimensions before decoding pixels: a small compressed file can
	// otherwise expand into a very large bitmap.
	cfg, _, err := image.DecodeConfig(f)
	if err != nil || cfg.Width < 64 || cfg.Height < 64 || cfg.Width > 4096 || cfg.Height > 4096 {
		c.JSON(400, gin.H{"error":"参考图尺寸需在 64–4096px 之间"}); return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil { c.JSON(400, gin.H{"error":"参考图读取失败"}); return }
	img, _, err := image.Decode(f)
	if err != nil { c.JSON(400, gin.H{"error":"参考图不是有效图片"}); return }
	token := newID(24)
	dir := filepath.Join(genDir(), "refs", vid)
	if err := os.MkdirAll(dir, 0o700); err != nil { c.JSON(503, gin.H{"error":"参考图暂存失败"}); return }
	dest := filepath.Join(dir, token+".png")
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil { c.JSON(503, gin.H{"error":"参考图暂存失败"}); return }
	if err := png.Encode(out, img); err != nil { out.Close(); os.Remove(dest); c.JSON(400, gin.H{"error":"参考图转码失败"}); return }
	if err := out.Close(); err != nil { os.Remove(dest); c.JSON(503, gin.H{"error":"参考图暂存失败"}); return }
	c.JSON(200, gin.H{"token": vid + "/" + token + ".png"})
}

// validateLogoReference resolves an upload token to its private source path
// without copying it. This lets createJob replay an existing request without
// creating another orphan copy.
func validateLogoReference(vid, token string) (string, error) {
	prefix := vid + "/"
	if vid == "" || !strings.HasPrefix(token, prefix) {
		return "", fmt.Errorf("参考图不属于当前会话")
	}
	rel := strings.TrimPrefix(token, prefix)
	if rel == "" || strings.ContainsAny(rel, "/\\\x00") || !strings.HasSuffix(rel, ".png") {
		return "", fmt.Errorf("参考图路径无效")
	}
	src := filepath.Join(genDir(), "refs", vid, rel)
	st, err := os.Stat(src)
	if err != nil || st.Size() <= 0 || st.Size() > maxLogoReferenceBytes {
		return "", fmt.Errorf("参考图不存在或不可用")
	}
	raw, err := os.ReadFile(src)
	if err != nil { return "", err }
	if _, err := imagePNG(raw); err != nil { return "", fmt.Errorf("参考图损坏") }
	return src, nil
}

func copyLogoReference(src, jid string) error {
	raw, err := os.ReadFile(src)
	if err != nil { return err }
	dest := filepath.Join(jobDir(jid), "logo_ref.png")
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil { return err }
	return os.WriteFile(dest, raw, 0o600)
}

// attachLogoReference is kept for callers/tests that need both steps.
func attachLogoReference(vid, jid, token string) error {
	src, err := validateLogoReference(vid, token)
	if err != nil { return err }
	return copyLogoReference(src, jid)
}

// cleanStaleLogoReferences removes abandoned temporary uploads. Job copies live
// under jobs/<id>/ and are not touched.
func cleanStaleLogoReferences() {
	root := filepath.Join(genDir(), "refs")
	entries, err := os.ReadDir(root)
	if err != nil { return }
	cutoff := time.Now().Add(-48 * time.Hour)
	for _, e := range entries {
		if !e.IsDir() { continue }
		dir := filepath.Join(root, e.Name())
		files, err := os.ReadDir(dir)
		if err != nil { continue }
		remaining := 0
		for _, f := range files {
			if f.IsDir() { remaining++; continue }
			info, err := f.Info()
			if err != nil { continue }
			if info.ModTime().Before(cutoff) {
				_ = os.Remove(filepath.Join(dir, f.Name()))
			} else { remaining++ }
		}
		if remaining == 0 { _ = os.Remove(dir) }
	}
}

func startLogoReferenceCleanup() {
	cleanStaleLogoReferences()
	go func() {
		for range time.Tick(time.Hour) { cleanStaleLogoReferences() }
	}()
}
