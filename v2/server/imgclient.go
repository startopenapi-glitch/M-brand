package app

// imgclient.go — 中转站生图客户端：generations + edits（参考图一致性），失败重试一次

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "golang.org/x/image/webp"
)

const maxImageBytes = 32 << 20
const maxImagePixels = 16 << 20

// A local failure before the HTTP request cannot have produced an upstream
// image. Keep it retryable after the missing/corrupt reference is repaired.
var errGenerationPreflight = errors.New("生成请求尚未发送")

type imgConfig struct {
	base, key, model, size string
	timeout                time.Duration
}

func imgConf() imgConfig {
	size := os.Getenv("ZEROBRAND_IMG_SIZE")
	if size == "" {
		size = "1024x1280"
	}
	model := os.Getenv("ZEROBRAND_IMG_MODEL")
	if model == "" {
		model = "gpt-image-2.5-sunburst"
	}
	return imgConfig{
		base:    os.Getenv("ZEROBRAND_IMG_BASE_URL"),
		key:     os.Getenv("ZEROBRAND_IMG_API_KEY"),
		model:   model,
		size:    size,
		timeout: 240 * time.Second,
	}
}

func imgConfigured() bool { c := imgConf(); return c.base != "" && c.key != "" }

// imgFallbackConf：备用生图通道（ZEROBRAND_IMG_FALLBACK_BASE_URL / _API_KEY / _MODEL）
func imgFallbackConf() imgConfig {
	primary := imgConf()
	model := os.Getenv("ZEROBRAND_IMG_FALLBACK_MODEL")
	if model == "" {
		model = primary.model
	}
	return imgConfig{
		base:    os.Getenv("ZEROBRAND_IMG_FALLBACK_BASE_URL"),
		key:     os.Getenv("ZEROBRAND_IMG_FALLBACK_API_KEY"),
		model:   model,
		size:    primary.size,
		timeout: primary.timeout,
	}
}

func fallbackConfigured() bool {
	c := imgFallbackConf()
	return c.base != "" && c.key != ""
}

type imgResult struct {
	Tokens        int
	RequestID     string
	RevisedPrompt string
	Model         string
}

func (c imgConfig) doGenerate(prompt, dest string, refs []string) (*imgResult, error) {
	var resp *http.Response
	var err error
	if len(refs) > 0 {
		resp, err = c.postEdits(prompt, refs)
	} else {
		resp, err = c.postGenerations(prompt)
	}
	if err != nil {
		if errors.Is(err, errGenerationPreflight) {
			return nil, err
		}
		return nil, unknownGenerationError(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*maxImageBytes+1))
	if err != nil {
		return nil, unknownGenerationError(err)
	}
	if len(body) > 2*maxImageBytes {
		return nil, unknownGenerationError(errors.New("生图响应超过大小上限"))
	}
	if resp.StatusCode >= 500 {
		return nil, unknownGenerationError(fmt.Errorf("生图接口 HTTP %d", resp.StatusCode))
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("生图接口 HTTP %d: %s", resp.StatusCode, truncate(string(body), 160))
	}
	requestID := resp.Header.Get("x-request-id")
	if requestID == "" {
		requestID = resp.Header.Get("openai-request-id")
	}
	var payload struct {
		ID   string `json:"id"`
		Data []struct {
			B64JSON       string `json:"b64_json"`
			URL           string `json:"url"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
		Error *struct{ Message string } `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, unknownGenerationError(fmt.Errorf("响应解析失败: %v", err))
	}
	if payload.Error != nil {
		return nil, unknownGenerationError(errors.New("生图接口错误: " + payload.Error.Message))
	}
	if len(payload.Data) == 0 {
		return nil, unknownGenerationError(errors.New("生图接口未返回图片"))
	}
	var raw []byte
	if payload.Data[0].B64JSON != "" {
		raw, err = base64.StdEncoding.DecodeString(payload.Data[0].B64JSON)
	}
	if (len(raw) == 0 || err != nil) && payload.Data[0].URL != "" {
		raw, err = c.download(payload.Data[0].URL)
	}
	if err != nil {
		return nil, unknownGenerationError(fmt.Errorf("读取图片失败: %w", err))
	}
	raw, err = imagePNG(raw)
	if err != nil {
		return nil, unknownGenerationError(err)
	}
	if err := writeImageFile(dest, raw); err != nil {
		return nil, unknownGenerationError(err)
	}
	if requestID == "" {
		requestID = payload.ID
	}
	return &imgResult{Tokens: payload.Usage.TotalTokens, RequestID: requestID, RevisedPrompt: payload.Data[0].RevisedPrompt, Model: c.model}, nil
}

// imagePNG 校验完整图像后统一 PNG，确保后续参考图的后缀和 MIME 一致。
// 先读尺寸再解码，避免压缩体积很小但像素数极大的响应耗尽内存。
func imagePNG(raw []byte) ([]byte, error) {
	if len(raw) == 0 || len(raw) > maxImageBytes {
		return nil, errors.New("图片为空或超过大小上限")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("图片格式无效: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxImagePixels/cfg.Height {
		return nil, errors.New("图片尺寸超过上限")
	}
	im, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("图片内容损坏: %w", err)
	}
	if format == "png" {
		return raw, nil // 保留原 PNG 位深、透明度与元数据，不做有损变换
	}
	var out bytes.Buffer
	if err := png.Encode(&out, im); err != nil {
		return nil, fmt.Errorf("图片转 PNG 失败: %w", err)
	}
	if out.Len() > maxImageBytes {
		return nil, errors.New("PNG 图片超过大小上限")
	}
	return out.Bytes(), nil
}

func writeImageFile(dest string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(dest), ".image-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0o644); err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), dest)
}

// Generate 失败自动重试一次；主通道失败自动切换备用通道；最终失败上报通道健康（额度告警→熔断）
func Generate(prompt, dest string, refs []string) (*imgResult, error) {
	return GenerateSize(prompt, dest, refs, "")
}
func GenerateSize(prompt, dest string, refs []string, size string) (*imgResult, error) {
	if !imgConfigured() {
		return nil, errors.New("生图 API 未配置（ZEROBRAND_IMG_BASE_URL / API_KEY）")
	}
	conf := imgConf()
	if size != "" {
		conf.size = size
	}
	isFallback := false
	var lastErr error
	for round := 0; round < 2; round++ {
		for attempt := 0; attempt < 2; attempt++ {
			r, err := conf.doGenerate(prompt, dest, refs)
			if err == nil {
				noteImgSuccess()
				if isFallback {
					slog.Warn("生图已使用备用通道成功", "err_before", truncate(lastErr.Error(), 120))
				}
				return r, nil
			}
			lastErr = err
			if errors.Is(err, errGenerationUnknown) {
				noteImgFailure(err.Error())
				return nil, err
			}
			if errors.Is(err, errGenerationPreflight) {
				return nil, err
			}
			if strings.Contains(err.Error(), "QUOTA_EXHAUSTED") {
				break // 额度告警：不浪费第二次尝试，直接切换
			}
			time.Sleep(3 * time.Second)
		}
		if isFallback || !fallbackConfigured() {
			break
		}
		slog.Warn("生图主通道失败，切换备用通道", "err", truncate(lastErr.Error(), 120))
		conf = imgFallbackConf()
		if size != "" {
			conf.size = size
		}
		isFallback = true
	}
	noteImgFailure(lastErr.Error())
	return nil, lastErr
}

func (c imgConfig) postGenerations(prompt string) (*http.Response, error) {
	body, _ := json.Marshal(map[string]any{
		"model": c.model, "prompt": prompt, "n": 1, "size": c.size,
	})
	req, err := http.NewRequest("POST", c.base+"/images/generations", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errGenerationPreflight, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	return (&http.Client{Timeout: c.timeout}).Do(req)
}

func (c imgConfig) postEdits(prompt string, refs []string) (*http.Response, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("model", c.model)
	_ = w.WriteField("prompt", prompt)
	_ = w.WriteField("size", c.size)
	_ = w.WriteField("n", "1")
	for i, ref := range refs {
		raw, err := os.ReadFile(ref)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errGenerationPreflight, err)
		}
		raw, err = imagePNG(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: 参考图无效: %v", errGenerationPreflight, err)
		}
		// 必须显式 image/* 内容类型：CreateFormFile 默认 octet-stream 会被中转站 400
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition",
			fmt.Sprintf(`form-data; name="image"; filename="ref%d.png"`, i))
		h.Set("Content-Type", "image/png")
		fw, err := w.CreatePart(h)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errGenerationPreflight, err)
		}
		if _, err := fw.Write(raw); err != nil {
			return nil, fmt.Errorf("%w: %v", errGenerationPreflight, err)
		}
	}
	w.Close()
	req, err := http.NewRequest("POST", c.base+"/images/edits", &buf)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errGenerationPreflight, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return (&http.Client{Timeout: c.timeout}).Do(req)
}

func (c imgConfig) download(url string) ([]byte, error) {
	resp, err := (&http.Client{Timeout: c.timeout}).Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("下载图片 HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxImageBytes {
		return nil, errors.New("下载图片超过大小上限")
	}
	return raw, nil
}
