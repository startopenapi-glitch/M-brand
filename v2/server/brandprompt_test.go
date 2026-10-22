package app

import (
	"strings"
	"testing"
)

func TestNormalizeFillsDefaults(t *testing.T) {
	f := normalizeForm(BrandForm{"cn_name": "山物", "industry": "精制茶饮"})
	if !strings.HasPrefix(f["ratio"], "4:5") || f["style"] == "" || f["materials"] == "" {
		t.Fatalf("缺省补全失败: %v", f)
	}
	if f["lang"] != "中英结合" {
		t.Fatalf("lang 默认错误")
	}
}

func TestDirectionPromptsDistinct(t *testing.T) {
	f := normalizeForm(BrandForm{"cn_name": "山物", "industry": "精制茶饮"})
	ps := DirectionPrompts(f)
	if len(ps) != 3 || ps[0] == ps[1] || ps[1] == ps[2] {
		t.Fatalf("三方向必须互异")
	}
	for _, p := range ps {
		if !strings.Contains(p, "山物") || !strings.Contains(p, "方向卡") || !strings.Contains(p, "4:5") || !strings.Contains(p, "【唯一方向】") || !strings.Contains(p, "禁止") {
			t.Fatalf("方向卡提示词缺关键要素")
		}
	}
}

func TestDirectionSetsDifferByIndustry(t *testing.T) {
	tea := DirectionPrompts(normalizeForm(BrandForm{"cn_name": "山物", "industry": "精制茶饮"}))
	tech := DirectionPrompts(normalizeForm(BrandForm{"cn_name": "矩阵", "industry": "AI 软件"}))
	if tea[0] == tech[0] || tea[1] == tech[1] || tea[2] == tech[2] {
		t.Fatalf("不同行业不应共用同一组三方向\ntea=%v\ntech=%v", tea, tech)
	}
	if strings.Contains(tea[0], "系统坐标") || strings.Contains(tech[0], "材料档案") {
		t.Fatal("行业方向集选错")
	}
}

func TestEveryDirectionHasPresentation(t *testing.T) {
	for _, set := range directionSets {
		for _, d := range set.dirs {
			p := presentationFor(d)
			if d.Name == "" || mechanismFor(d) == "" || p.Format == "" || p.MainVisual == "" || p.Composition == "" || p.Density == "" || p.LogoTreatment == "" || p.Medium == "" {
				t.Fatalf("incomplete presentation for %s: %+v", d.Name, p)
			}
		}
	}
	for _, d := range defaultDirections {
		p := presentationFor(d)
		if p.Format == "" || p.MainVisual == "" || p.Composition == "" || p.Medium == "" { t.Fatalf("incomplete default presentation %s", d.Name) }
	}
}

func TestDefaultStyleDoesNotForceEditorial(t *testing.T) {
	f := normalizeForm(BrandForm{"cn_name": "未知品牌", "industry": "特种传感器"})
	if strings.Contains(strings.ToLower(f["style"]), "editorial") {
		t.Fatalf("未命中行业时不应默认 editorial: %s", f["style"])
	}
}

func TestBoardPromptsOrderAndRules(t *testing.T) {
	f := normalizeForm(BrandForm{"cn_name": "SEEDLAB", "industry": "烘焙"})
	bps := BoardPrompts(f)
	order := []int{}
	for _, s := range bps {
		order = append(order, s.N)
	}
	if order[0] != 2 || order[1] != 1 || order[2] != 3 || order[3] != 4 {
		t.Fatalf("生成顺序必须 2→1→3→4，实际 %v", order)
	}
	for _, s := range bps {
		if s.N == 2 && !strings.Contains(s.Body, "锚点") {
			t.Fatalf("图2 应包含锚点规则")
		}
		if s.N != 2 && !strings.Contains(s.Body, "不得重新设计") {
			t.Fatalf("图%d 应包含一致性规则", s.N)
		}
		if !strings.Contains(s.Body, "留白 25%-35%") {
			t.Fatalf("图%d 缺固定要素", s.N)
		}
	}
	if !strings.Contains(bps[3].Body, "分层使用") {
		t.Fatalf("图4 缺分层使用规则")
	}
}
