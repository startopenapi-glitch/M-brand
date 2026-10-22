package app

// Explicit customer requirements are captured before normalization introduces
// defaults. The snapshot lives in existing form/brief JSON, requiring no DB or
// commercial migration. Legacy jobs hydrate conservatively from their saved form.
import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const designConstraintsVersion = "design-constraints-v1"
const constraintsFormKey = "_design_constraints_v1"

type DesignConstraints struct {
	Version          string   `json:"version"`
	Fingerprint      string   `json:"fingerprint"`
	ColorAuthority   string   `json:"colorAuthority,omitempty"`
	ApprovedRevision string   `json:"approvedRevision,omitempty"`
	ChineseWordmark  string   `json:"chineseWordmark,omitempty"`
	EnglishWordmark  string   `json:"englishWordmark,omitempty"`
	Language         string   `json:"language,omitempty"`
	LogoType         string   `json:"logoType,omitempty"`
	PrimaryColor     string   `json:"primaryColor,omitempty"`
	AccentColors     string   `json:"accentColors,omitempty"`
	Materials        string   `json:"materials,omitempty"`
	Mandatory        []string `json:"mandatory,omitempty"`
	Avoid            []string `json:"avoid,omitempty"`
}

func explicitConstraints(f BrandForm) DesignConstraints {
	c := DesignConstraints{Version: designConstraintsVersion, ChineseWordmark: strings.TrimSpace(f["cn_name"]), EnglishWordmark: strings.TrimSpace(f["en_name"]), Language: strings.TrimSpace(f["lang"]), LogoType: strings.TrimSpace(f["logo_type"]), PrimaryColor: strings.TrimSpace(f["primary_color"]), AccentColors: strings.TrimSpace(f["accent_colors"]), Materials: strings.TrimSpace(f["materials"]), Mandatory: cleanList(f["extra"]), Avoid: cleanList(f["avoid"])}
	raw, _ := json.Marshal(c)
	sum := sha256.Sum256(raw)
	c.Fingerprint = hex.EncodeToString(sum[:])[:16]
	return c
}
func freezeDesignConstraints(normalized, raw BrandForm) {
	c := explicitConstraints(raw)
	b, _ := json.Marshal(c)
	normalized[constraintsFormKey] = string(b)
}
func constraintsFor(f BrandForm) DesignConstraints {
	var c DesignConstraints
	if json.Unmarshal([]byte(f[constraintsFormKey]), &c) == nil && c.Version == designConstraintsVersion && c.ChineseWordmark == strings.TrimSpace(f["cn_name"]) {
		return c
	}
	return explicitConstraints(f)
}
func (c DesignConstraints) wordmark() string {
	switch c.Language {
	case "英文", "English", "english":
		if c.EnglishWordmark != "" {
			return c.EnglishWordmark
		}
		return c.ChineseWordmark
	case "中文", "Chinese", "chinese":
		return c.ChineseWordmark
	}
	return strings.Join(nonEmpty(c.ChineseWordmark, c.EnglishWordmark), " / ")
}
func hydrateBriefConstraints(b BrandBrief, f BrandForm) BrandBrief {
	if b.Constraints.Version == "" {
		b.Constraints = constraintsFor(f)
	}
	if w := b.Constraints.wordmark(); w != "" {
		b.ExactWordmark = w
	}
	return b
}
func (c DesignConstraints) prompt() string {
	lines := []string{"【用户明确约束 · " + c.Version + " · " + c.Fingerprint + "】"}
	for _, x := range []struct{ k, v string }{{"准确字标", c.wordmark()}, {"标志语言", c.Language}, {"Logo 类型", c.LogoType}, {"主色", c.PrimaryColor}, {"辅助色", c.AccentColors}, {"材质", c.Materials}, {"必须保留", strings.Join(c.Mandatory, "；")}, {"避免", strings.Join(c.Avoid, "；")}} {
		if x.v != "" {
			lines = append(lines, x.k+"："+x.v)
		}
	}
	if c.ColorAuthority == "confirmed_logo" {
		lines = append(lines, "当前颜色：以已确认的修订 Logo 母版为准，不恢复初始色板")
	}
	if c.ApprovedRevision != "" {
		lines = append(lines, "已确认修订（仅涉及的属性覆盖初始约束）："+c.ApprovedRevision)
	}
	return strings.Join(lines, "\n") + "\n用户明确约束优先于设计建议；未提供英文名不得翻译或编造英文。图标项不放字标；单色及反白项仅按该任务转换标志颜色，其余身份保持一致。"
}
func constrainDirection(d LogoDirection, c DesignConstraints) LogoDirection {
	if c.ColorAuthority == "confirmed_logo" {
		d.Palette = []string{"以已确认的修订 Logo 母版为准"}
	} else if c.PrimaryColor != "" || c.AccentColors != "" {
		d.Palette = splitColors(strings.Join(nonEmpty(c.PrimaryColor, c.AccentColors), "/"))
	}
	if c.Language == "英文" || strings.EqualFold(c.Language, "English") {
		d.Typography = strings.ReplaceAll(d.Typography, "中文", "英文")
		d.Composition = "按本方向的比例与间距组合准确英文字标「" + c.EnglishWordmark + "」，不添加中文"
	}
	if c.Language == "中文" || strings.EqualFold(c.Language, "Chinese") {
		d.Composition = "按本方向的比例与间距组合准确中文字标「" + c.ChineseWordmark + "」，不添加英文"
	}
	return d
}
func buildConstrainedRevisionPrompt(b BrandBrief, d LogoDirection, instruction string, keep, change []string) string {
	// Revision is the only bounded override; it does not mutate the saved brand.
	return b.Constraints.prompt() + "\n" + factLines(b) + "\n" + buildLogoRevisionPrompt(constrainDirection(d, b.Constraints), b.ExactWordmark, instruction, keep, change) + "\n仅本次明确修改的属性可覆盖原约束；其他字标、颜色、材质和避免项继续保留。"
}

func approvedRevisionConstraints(c DesignConstraints, instruction string) DesignConstraints {
	c.ApprovedRevision = instruction
	// Do not guess a new swatch from natural language. The approved image becomes
	// the color master whenever the revision mentions color. Original form remains
	// immutable for audit; paid layouts must not restore its previous swatches.
	if regexp.MustCompile(`(?i)色|颜|#[0-9a-f]{3,8}|\b(red|blue|green|purple|black|white|gold|silver|orange|pink|yellow|color|colour)\b`).MatchString(instruction) {
		c.ColorAuthority = "confirmed_logo"
		c.PrimaryColor = ""
		c.AccentColors = ""
	}
	c.Fingerprint = ""
	raw, _ := json.Marshal(c)
	sum := sha256.Sum256(raw)
	c.Fingerprint = hex.EncodeToString(sum[:])[:16]
	return c
}
func saveApprovedRevisionConstraints(j jobRow, b BrandBrief, d LogoDirection, instruction string) error {
	c := approvedRevisionConstraints(b.Constraints, instruction)
	b.Constraints = c
	snap := buildLogoDirectionSnapshot(d)
	if j.DirectionSnapshot != nil {
		snap = *j.DirectionSnapshot
	}
	snap.Constraints = &c
	tag, err := pool.Exec(contextBG(), `UPDATE jobs SET brand_brief=$2,direction_snapshot=$3 WHERE id=$1 AND ($4='' OR execution_token=$4)`, j.ID, mustJSON(b), mustJSON(snap), j.ExecutionToken)
	if err == nil && tag.RowsAffected() != 1 {
		return fmt.Errorf("修订约束保存失败：执行权已变更")
	}
	return err
}
