package app

// packs.go — 分包定义（v3：映射到单件 item kinds）

// packForItemKind 根据图片 kind 返回所属包
func packForItemKind(kind string) string {
	switch kind {
	// Logo 资产包
	case "logo_h", "logo_v", "logo_icon", "logo_mono", "logo_white":
		return "logo"

	// 品牌视觉系统
	case "system_card", "color_typo", "photo_style":
		return "system"

	// 品牌提案
	case "worldview", "logo_logic", "app_overview":
		return "boards"

	// 应用落地包
	case "card_front", "card_back", "letterhead",
		"wx_avatar", "wx_cover", "ppt_template", "brand_story":
		return "apps"

	// 方向选择阶段（免费）
	case "direction":
		return ""
	}
	return ""
}

// packItemNames 包含的交付物名称（前端展示用）
var packItemNames = map[string][]string{
	"logo":   {"Logo 横版", "Logo 竖版", "Logo 图标", "Logo 单色黑", "Logo 反白"},
	"system": {"Logo 组合卡", "色彩与字体规范", "摄影与材质风格"},
	"boards": {"品牌世界观", "Logo 概念逻辑", "应用场景总览"},
	"apps":   {"名片正面", "名片背面", "信纸", "微信头像", "公众号封面", "PPT 模板", "品牌故事"},
}
