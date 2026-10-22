package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func constraintFixture() BrandForm {
	return BrandForm{"cn_name": "山序", "en_name": "SHAN XU", "industry": "茶饮", "products": "冷泡茶", "positioning": "年轻通勤者", "lang": "英文", "logo_type": "字母图形", "primary_color": "#1357AD", "accent_colors": "#F0C040", "materials": "拉丝铝", "avoid": "禁止叶片；禁止印章", "extra": "保留 X 负形"}
}
func TestConstraintsAllStages(t *testing.T) {
	f := constraintFixture()
	b := fallbackBrandBrief(f)
	d := fallbackLogoDirections(f)[0]
	snap := buildLogoDirectionSnapshot(d)
	prompts := map[string]string{"card": buildDirectionCardPrompt(b, d), "logo": buildFormalLogoPrompt(b, d), "revision": buildConstrainedRevisionPrompt(b, d, "只调整间距", nil, []string{"只调整间距"})}
	for _, s := range itemSpecs(f) {
		if s.Prompt != "" {
			prompts[s.Kind] = buildItemPromptWithContext(f, s, &snap, nil)
		}
	}
	for stage, p := range prompts {
		for _, want := range []string{"SHAN XU", "#1357AD", "#F0C040", "拉丝铝", "禁止叶片", "禁止印章", "字母图形", "年轻通勤者"} {
			if !strings.Contains(p, want) {
				t.Errorf("%s lost %s", stage, want)
			}
		}
		if strings.Contains(p, "准确字标：山序") || strings.Contains(p, "中文标准字「山序」") || strings.Contains(p, "中文品牌名「山序」") || strings.Contains(p, "品牌名「山序」") || strings.Contains(p, "大标题「山序」") {
			t.Errorf("%s contradicts English-only constraint", stage)
		}
	}
}
func TestExplicitConstraintSnapshotSurvivesNormalization(t *testing.T) {
	raw := BrandForm{"cn_name": "留白", "industry": "茶饮", "en_name": "LIU BAI", "lang": "英文"}
	f := normalizeForm(raw)
	freezeDesignConstraints(f, raw)
	b := fallbackBrandBrief(f)
	if b.Constraints.PrimaryColor != "" {
		t.Fatal("inferred color became mandatory")
	}
	f["en_name"] = "MUTATED"
	b = fallbackBrandBrief(f)
	if b.Constraints.EnglishWordmark != "LIU BAI" {
		t.Fatal("frozen constraint changed")
	}
	data, _ := json.Marshal(b)
	var restored BrandBrief
	_ = json.Unmarshal(data, &restored)
	if restored.Constraints.Version != designConstraintsVersion {
		t.Fatal("missing version")
	}
}
func TestLegacyConstraintHydrationPreservesBrief(t *testing.T) {
	f := constraintFixture()
	old := BrandBrief{BrandName: "山序", ExactWordmark: "山序", Positioning: "已保存定位", Source: "text"}
	b := hydrateBriefConstraints(old, f)
	if b.Positioning != "已保存定位" || b.Source != "text" || b.ExactWordmark != "SHAN XU" {
		t.Fatal("legacy hydration damaged facts")
	}
	b.Constraints.PrimaryColor = "#AAAAAA"
	f["primary_color"] = "#BBBBBB"
	if hydrateBriefConstraints(b, f).Constraints.PrimaryColor != "#AAAAAA" {
		t.Fatal("persisted constraint overwritten")
	}
}
func TestRevisionHonorsExplicitColorChangeWithoutContradiction(t *testing.T) {
	b := fallbackBrandBrief(constraintFixture())
	d := fallbackLogoDirections(constraintFixture())[0]
	p := buildConstrainedRevisionPrompt(b, d, "改为紫色", nil, []string{"改为紫色"})
	if strings.Contains(p, "本次没有要求改变颜色") {
		t.Fatal("contradictory no-color-change rule")
	}
	if !strings.Contains(p, "改为紫色") || !strings.Contains(p, "仅本次明确修改") {
		t.Fatal("missing bounded revision precedence")
	}
}

func TestPaidTaskUsesSnapshotInsteadOfChangedForm(t *testing.T) {
	f := constraintFixture()
	d := fallbackLogoDirections(f)[0]
	snap := buildLogoDirectionSnapshot(d)
	f["primary_color"] = "#BADBAD"
	f["en_name"] = "WRONG NAME"
	f["materials"] = "WRONG MATERIAL"
	for _, s := range itemSpecs(f) {
		if s.Prompt != "" {
			p := buildItemPromptWithContext(f, s, &snap, nil)
			for _, bad := range []string{"#BADBAD", "WRONG NAME", "WRONG MATERIAL"} {
				if strings.Contains(p, bad) {
					t.Errorf("%s leaked mutable input %s", s.Kind, bad)
				}
			}
		}
	}
}

func TestApprovedColorRevisionCarriesIntoPaidApplications(t *testing.T) {
	f := constraintFixture()
	d := fallbackLogoDirections(f)[0]
	snap := buildLogoDirectionSnapshot(d)
	c := approvedRevisionConstraints(constraintsFor(f), "改为紫色")
	snap.Constraints = &c
	for _, spec := range itemSpecs(f) {
		if spec.Prompt != "" {
			p := buildItemPromptWithContext(f, spec, &snap, nil)
			if strings.Contains(p, "#1357AD") || strings.Contains(p, "#F0C040") {
				t.Errorf("%s restored initial palette", spec.Kind)
			}
			if !strings.Contains(p, "已确认的修订 Logo 母版") {
				t.Errorf("%s missing revised master", spec.Kind)
			}
		}
	}
	if approvedRevisionConstraints(constraintsFor(f), "只调整间距").PrimaryColor != "#1357AD" {
		t.Fatal("non-color revision erased palette")
	}
}

func TestCreationReplayAcrossConstraintUpgradeKeepsOriginalJob(t *testing.T) {
	vid := reviewFlowVisitor(t)
	key := "constraint-upgrade-" + randHex(12)
	id := reviewFlowCreateID(t, reviewFlowCreate(t, vid, key, "旧任务"))
	mustExec(t, "UPDATE jobs SET form=form-'_design_constraints_v1' WHERE id=$1", id)
	response := reviewFlowCreate(t, vid, key, "旧任务")
	if response.Code != 200 {
		t.Fatalf("legacy replay HTTP %d: %s", response.Code, response.Body)
	}
	if next := reviewFlowCreateID(t, response); next != id {
		t.Fatal("upgrade created a second job")
	}
	if response := reviewFlowCreate(t, vid, key, "已改变品牌"); response.Code != 409 {
		t.Fatalf("changed payload accepted: %d", response.Code)
	}
}

func TestLegacyDirectionSummaryDoesNotReintroduceOldPalette(t *testing.T) {
	f := constraintFixture()
	snap, _ := buildDirectionSnapshot(f, 0)
	snap.Colors = []string{"OLD_PALETTE"}
	snap.Summary += "OLD_PALETTE"
	c := constraintsFor(f)
	snap.Constraints = &c
	for _, spec := range itemSpecs(f) {
		if spec.Kind == "worldview" {
			p := buildItemPromptWithContext(f, spec, &snap, nil)
			if strings.Contains(p, "OLD_PALETTE") {
				t.Fatal("legacy summary overrode explicit color")
			}
		}
	}
}
