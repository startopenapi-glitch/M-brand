package app

import (
	"fmt"
	"strings"
)

// Central reference policy. Do not rely on array order implicitly: the prompt
// labels are generated from the actual, ordered reference list.
type ReferencePolicy string

const (
	ReferenceDirectionFirst ReferencePolicy = "direction_first"
	ReferenceLogoFirst      ReferencePolicy = "logo_first"
	ReferenceNone           ReferencePolicy = "none"
)

// Explicit mapping for all free/paid kinds. Reasons:
//
//	direction_first: composition, scene, material, photography, application
//	logo_first: formal symbol shape, lockup, icon and logo logic
var referencePolicies = map[string]ReferencePolicy{
	"system_card":  ReferenceDirectionFirst, // free Logo card derives from the chosen direction
	"logo_h":       ReferenceLogoFirst,
	"logo_v":       ReferenceLogoFirst,
	"logo_icon":    ReferenceLogoFirst,
	"logo_mono":    ReferenceLogoFirst,
	"logo_white":   ReferenceLogoFirst,
	"color_typo":   ReferenceDirectionFirst, // palette/type must follow the art direction
	"photo_style":  ReferenceDirectionFirst,
	"worldview":    ReferenceDirectionFirst,
	"logo_logic":   ReferenceLogoFirst, // explains the formal symbol's structure
	"app_overview": ReferenceDirectionFirst,
	"card_front":   ReferenceDirectionFirst,
	"card_back":    ReferenceDirectionFirst,
	"letterhead":   ReferenceDirectionFirst,
	"wx_avatar":    ReferenceLogoFirst, // symbol must stay recognisable when cropped
	"wx_cover":     ReferenceDirectionFirst,
	"ppt_cover":    ReferenceDirectionFirst,
	"brand_story":  ReferenceNone,
}

type referenceImage struct {
	Path  string
	Role  string
	Label string
}

func referencePolicyFor(kind string) ReferencePolicy {
	if p, ok := referencePolicies[kind]; ok {
		return p
	}
	return ReferenceDirectionFirst
}

func referenceInputs(j jobRow, kind string) []referenceImage {
	if kind == "brand_story" || referencePolicyFor(kind) == ReferenceNone {
		return nil
	}
	dirPath := ""
	if j.Chosen != nil {
		dirPath = jobDir(j.ID) + "/" + fmt.Sprintf("direction_%d.png", *j.Chosen)
	}
	logoPath := jobDir(j.ID) + "/system_card.png"
	dirRef := referenceImage{Path: dirPath, Role: "direction", Label: "方向参考图"}
	logoRef := referenceImage{Path: logoPath, Role: "logo", Label: "正式 Logo 参考图"}
	if !fileExists(dirPath) {
		dirRef = referenceImage{}
	}
	if !fileExists(logoPath) {
		logoRef = referenceImage{}
	}
	out := []referenceImage{}
	appendRef := func(r referenceImage) {
		if r.Path != "" {
			out = append(out, r)
		}
	}
	switch referencePolicyFor(kind) {
	case ReferenceLogoFirst:
		appendRef(logoRef)
		appendRef(dirRef)
	case ReferenceDirectionFirst:
		appendRef(dirRef)
		appendRef(logoRef)
	}
	return out
}

func referenceRoleBlock(refs []referenceImage) string {
	if len(refs) == 0 {
		return ""
	}
	lines := []string{"【参考职责】"}
	for i, r := range refs {
		if r.Role == "direction" {
			lines = append(lines, fmt.Sprintf("参考图 %d｜方向参考：提供构图语言、色彩关系、材质、媒介与氛围。", i+1))
		} else {
			lines = append(lines, fmt.Sprintf("参考图 %d｜正式 Logo 参考：提供标志形状、比例、字标与组合关系；不要复制它的背景、居中布局或留白。", i+1))
		}
	}
	if len(refs) >= 2 {
		lines = append(lines, "出现冲突时：标志本体以正式 Logo 参考图为准；场景、材质与版式以选定方向为准。不要把方向卡里的示意 Logo 当成正式标志。")
	}
	return strings.Join(lines, "\n")
}
