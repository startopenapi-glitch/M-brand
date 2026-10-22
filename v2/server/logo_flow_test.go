package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestBrandBriefFallbackIsConservative(t *testing.T) {
	f := normalizeForm(BrandForm{"cn_name": "山物", "industry": "茶饮", "products": "冷泡茶", "keywords": "自然、克制"})
	b := fallbackBrandBrief(f)
	if b.BrandName != "山物" || b.ExactWordmark != "山物" || b.Source != "heuristic" {
		t.Fatalf("%+v", b)
	}
	if len(b.Personality) != 2 {
		t.Fatalf("personality=%v", b.Personality)
	}
	if err := validateBrandBrief(b); err != nil {
		t.Fatal(err)
	}
}

func TestTextAdapterStructuredBriefAndInvalidFallback(t *testing.T) {
	mode := "valid"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		content := `{"brandName":"山物","exactWordmark":"山物","industryAndProduct":"茶饮 · 冷泡茶","audience":"","positioning":"","differentiator":"","personality":["自然"],"mandatoryElements":[],"avoidElements":[],"unknowns":[]}`
		if mode == "invalid" {
			content = `{"brandName":`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": content}}}})
	}))
	defer srv.Close()
	t.Setenv("ZEROBRAND_TEXT_BASE_URL", srv.URL)
	t.Setenv("ZEROBRAND_TEXT_API_KEY", "test-only")
	f := normalizeForm(BrandForm{"cn_name": "山物", "industry": "茶饮"})
	b, err := buildBrandBrief(t.Context(), f)
	if err != nil || b.Source != "text" || b.BrandName != "山物" {
		t.Fatalf("brief=%+v err=%v", b, err)
	}
	mode = "invalid"
	b, err = buildBrandBrief(t.Context(), f)
	if err == nil || b.Source != "heuristic" {
		t.Fatalf("invalid JSON must fall back: %+v %v", b, err)
	}
}

func TestLogoPromptsOmitEmptyFieldsAndSeparateCards(t *testing.T) {
	b := BrandBrief{BrandName: "山物", ExactWordmark: "山物", IndustryAndProduct: "茶饮", Personality: []string{"自然"}}
	d := fallbackLogoDirections(normalizeForm(BrandForm{"cn_name": "山物", "industry": "茶饮"}))[0]
	card := buildDirectionCardPrompt(b, d)
	logo := buildFormalLogoPrompt(b, d)
	if !containsText(card, "方向探索卡") || !containsText(logo, "正式 Logo") {
		t.Fatal("templates not separated")
	}
	if containsText(card, "定位：") || containsText(card, "差异点：") {
		t.Fatal("empty fields leaked into card")
	}
	if containsText(logo, "定位与差异：") {
		t.Fatal("empty positioning leaked into logo")
	}
	if !containsText(logo, "准确字标：山物") {
		t.Fatal("wordmark missing")
	}
	if !containsText(logo, "禁止画茶壶") || !containsText(logo, "几何轮廓") {
		t.Fatal("food/tea logo abstraction guard missing")
	}
	if !containsText(buildLogoRevisionPrompt(d, "山物", "改轮廓", nil, nil), "不得新增、删除或改变任何颜色") {
		t.Fatal("revision color boundary missing")
	}
}

func TestFormalLogoStageAndRevisionKeepVersions(t *testing.T) {
	j := commercialJob(t, "queued", "logo", 0)
	commercialWritePNG(t, j.ID, "direction_0.png", commercialPNG(t, 20))
	commercialGenerator(t, nil)
	if err := stageLogo(j); err != nil {
		t.Fatal(err)
	}
	var status, stage string
	if err := pool.QueryRow(contextBG(), "SELECT status,stage FROM jobs WHERE id=$1", j.ID).Scan(&status, &stage); err != nil {
		t.Fatal(err)
	}
	if status != "ready_to_confirm" || stage != "logo" {
		t.Fatalf("state=%s/%s", status, stage)
	}
	if _, err := os.Stat(filepath.Join(jobDir(j.ID), "system_card.png")); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(contextBG(), "SELECT COUNT(*) FROM generation_records WHERE job_id=$1 AND step='formal_logo'", j.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("records=%d err=%v", n, err)
	}
	mustExec(t, "UPDATE jobs SET logo_revision_instruction='保留符号，字标更紧凑' WHERE id=$1", j.ID)
	if err := stageLogoRevision(j); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(jobDir(j.ID), "logo_v2.png")); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(contextBG(), "SELECT COUNT(*) FROM logo_versions WHERE job_id=$1", j.ID).Scan(&n); err != nil || n < 2 {
		t.Fatalf("versions=%d err=%v", n, err)
	}
}

func TestInlinePNG(t *testing.T) {
	im := image.NewRGBA(image.Rect(0, 0, 8, 8))
	im.Set(0, 0, color.RGBA{1, 2, 3, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	if _, err := imagePNG(b.Bytes()); err != nil {
		t.Fatal(err)
	}
	_ = base64.StdEncoding.EncodeToString(b.Bytes())
	_ = fmt.Sprintf("%d", b.Len())
}
