package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReferencePolicyCoversAllImageItems(t *testing.T) {
	seen := 0
	for _, spec := range itemSpecs(normalizeForm(BrandForm{"cn_name": "测试", "industry": "测试"})) {
		seen++
		p := referencePolicyFor(spec.Kind)
		if spec.Kind == "brand_story" {
			if p != ReferenceNone {
				t.Fatalf("brand_story policy=%s", p)
			}
			continue
		}
		if p != ReferenceDirectionFirst && p != ReferenceLogoFirst {
			t.Fatalf("%s policy=%s", spec.Kind, p)
		}
	}
	if seen != 18 {
		t.Fatalf("items=%d", seen)
	}
}

func TestReferenceInputsOrderAndMissingRefs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZEROBRAND_GEN_DIR", dir)
	jid := "REFPOLICY"
	if err := os.MkdirAll(filepath.Join(dir, jid), 0o700); err != nil {
		t.Fatal(err)
	}
	// Minimal valid PNG files are enough for path/order selection.
	raw := commercialPNG(t, 10)
	chosen := 1
	j := jobRow{ID: jid, Chosen: &chosen, Form: normalizeForm(BrandForm{"cn_name": "测试", "industry": "测试"})}
	if err := os.WriteFile(filepath.Join(dir, jid, "direction_1.png"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, jid, "system_card.png"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	d := referenceInputs(j, "worldview")
	if len(d) != 2 || d[0].Role != "direction" || d[1].Role != "logo" {
		t.Fatalf("worldview refs=%v", d)
	}
	l := referenceInputs(j, "logo_h")
	if len(l) != 2 || l[0].Role != "logo" || l[1].Role != "direction" {
		t.Fatalf("logo_h refs=%v", l)
	}

	if err := os.Remove(filepath.Join(dir, jid, "system_card.png")); err != nil {
		t.Fatal(err)
	}
	l = referenceInputs(j, "logo_h")
	if len(l) != 1 || l[0].Role != "direction" {
		t.Fatalf("missing logo must not create a fake reference: %v", l)
	}
	block := referenceRoleBlock([]referenceImage{{Path: "a", Role: "logo", Label: "正式 Logo 参考图"}, {Path: "b", Role: "direction", Label: "方向参考图"}})
	if !containsText(block, "参考图 1｜正式 Logo") || !containsText(block, "参考图 2｜方向参考") {
		t.Fatalf("role numbering mismatch: %s", block)
	}
}

func containsText(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && strings.Contains(s, sub))
}
