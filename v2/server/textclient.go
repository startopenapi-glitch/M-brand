package app

// Optional text-model adapter for structured brand briefs and directions.
// Only an OpenAI-compatible /chat/completions JSON response is used. If the
// configured provider does not return valid JSON, callers fall back to the
// deterministic path; we never feed a truncated JSON string into the image
// pipeline. Secrets and raw request headers are never logged.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type textConfig struct {
	base, key, model string
	timeout          time.Duration
}

func textConf() textConfig {
	return textConfig{
		base: strings.TrimRight(os.Getenv("ZEROBRAND_TEXT_BASE_URL"), "/"),
		key:  os.Getenv("ZEROBRAND_TEXT_API_KEY"),
		model: func() string {
			if v := os.Getenv("ZEROBRAND_TEXT_MODEL"); v != "" {
				return v
			}
			return "gpt-4o-mini"
		}(),
		timeout: 60 * time.Second,
	}
}

func textConfigured() bool { c := textConf(); return c.base != "" && c.key != "" }

func textJSON(ctx context.Context, system, user string, out any) error {
	c := textConf()
	if c.base == "" || c.key == "" {
		return errors.New("文本模型未配置")
	}
	body, _ := json.Marshal(map[string]any{
		"model":           c.model,
		"messages":        []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": user}},
		"temperature":     0.4,
		"response_format": map[string]string{"type": "json_object"},
	})
	req, err := http.NewRequestWithContext(ctx, "POST", c.base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: c.timeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("文本接口 HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || len(payload.Choices) == 0 {
		return errors.New("文本接口返回格式无效")
	}
	content := strings.TrimSpace(payload.Choices[0].Message.Content)
	if strings.HasPrefix(content, "```") {
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimPrefix(content, "json")
		content = strings.TrimSuffix(content, "```")
		content = strings.TrimSpace(content)
	}
	if len(content) == 0 || len(content) > 1<<20 || !json.Valid([]byte(content)) {
		return errors.New("文本接口未返回完整 JSON")
	}
	if err := json.Unmarshal([]byte(content), out); err != nil {
		return fmt.Errorf("文本 JSON schema 不匹配: %w", err)
	}
	return nil
}
