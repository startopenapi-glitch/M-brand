package app

import (
	"encoding/json"
	"fmt"
	"strings"
)

type SelectedDirectionSnapshot struct {
	Constraints *DesignConstraints `json:"designConstraints,omitempty"`
	DirectionID      string                `json:"directionId"`
	DirectionVersion string                `json:"directionVersion"`
	Title            string                `json:"title"`
	VisualMechanism  string                `json:"visualMechanism"`
	Presentation     DirectionPresentation `json:"presentation"`
	Typography       string                `json:"typography,omitempty"`
	Colors           []string              `json:"colors,omitempty"`
	Summary          string                `json:"summary"`
	// StructuredDirection is the source of truth for text-model generated
	// directions. Older snapshots omit it and continue through fallback.
	StructuredDirection *LogoDirection `json:"structuredDirection,omitempty"`
}

func splitColors(s string) []string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '、' || r == '，' || r == ',' })
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func directionSummary(d Direction, idx int) string {
	p := presentationFor(d)
	return fmt.Sprintf("【已选方向】%c · %s\n视觉机制：%s\n构图：%s\n字体：%s\n色彩：%s\n媒介：%s",
		'A'+idx, strings.TrimPrefix(d.Name, string(rune('A'+idx))+" · "), mechanismFor(d), p.Composition, d.Typo, d.Color, p.Medium)
}

func buildDirectionSnapshot(f BrandForm, idx int) (SelectedDirectionSnapshot, bool) {
	dirs := directionsFor(f)
	if idx < 0 || idx >= len(dirs) {
		return SelectedDirectionSnapshot{}, false
	}
	d := dirs[idx]
	return SelectedDirectionSnapshot{
		DirectionID:      string(rune('A' + idx)),
		DirectionVersion: directionConfigVersion,
		Title:            d.Name,
		VisualMechanism:  mechanismFor(d),
		Presentation:     presentationFor(d),
		Typography:       d.Typo,
		Colors:           splitColors(d.Color),
		Summary:          directionSummary(d, idx),
	}, true
}

func logoDirectionSummary(d LogoDirection) string {
	return fmt.Sprintf("【已选方向】%s · %s\n视觉机制：%s\n构图：%s\n字体：%s\n色彩：%s\n媒介：%s",
		d.ID, d.Title, d.DesignHypothesis, d.Presentation.Composition, d.Typography, strings.Join(d.Palette, "、"), d.Presentation.Medium)
}

func buildLogoDirectionSnapshot(d LogoDirection) SelectedDirectionSnapshot {
	return SelectedDirectionSnapshot{
		DirectionID: d.ID, DirectionVersion: d.Version, Title: d.Title,
		VisualMechanism: d.DesignHypothesis, Presentation: d.Presentation,
		Typography: d.Typography, Colors: d.Palette, Summary: logoDirectionSummary(d),
		StructuredDirection: &d, Constraints: d.Constraints,
	}
}

func decodeLogoDirections(raw []byte) []LogoDirection {
	if len(raw) == 0 {
		return nil
	}
	var ds []LogoDirection
	if json.Unmarshal(raw, &ds) != nil || validateLogoDirections(ds) != nil {
		return nil
	}
	return ds
}

func decodeDirectionSnapshot(raw []byte) *SelectedDirectionSnapshot {
	if len(raw) == 0 {
		return nil
	}
	var snap SelectedDirectionSnapshot
	if json.Unmarshal(raw, &snap) != nil || snap.Title == "" {
		return nil
	}
	return &snap
}

func selectedSnapshot(j jobRow) (SelectedDirectionSnapshot, bool) {
	if j.DirectionSnapshot != nil {
		return *j.DirectionSnapshot, true
	}
	if j.Chosen != nil {
		// Old jobs: derive read-only fallback from existing chosen index + form.
		return buildDirectionSnapshot(j.Form, *j.Chosen)
	}
	return SelectedDirectionSnapshot{}, false
}
