package app

// items.go — 单件输出规格：每个交付物 = 一张独立图片
// Logo 组合卡是免费的前置交付，不是付费项。

import ("fmt";"strings")

// ItemSpec 单个交付物规格
type ItemSpec struct {
	Kind        string // 唯一标识（DB job_images.kind）
	Name        string // 中文名
	Pack        string // 所属包
	Prompt      string // 生成提示词（不含 commonRule/ctx，运行时拼接）
	SizeHint    string // 比例提示
	Priority    int    // 生成顺序（越小越先生成）
	IsSquare    bool   // true = 1:1 方形；false = 4:5 竖版
	NeedsRef    bool   // 是否需要参考图（Logo 组合卡+方向图）
}

// itemSpecs 返回全部 18 项交付物规格（1 项免费 Logo 组合卡 + 17 项付费内容）
func itemSpecs(f BrandForm) []ItemSpec {
	cn := f["cn_name"]
	en := f["en_name"]
	primary := f["primary_color"]
	slogan := f["slogan"]
	_ = slogan // 部分项使用

	enLogo := ""
	if en != "" {
		enLogo = fmt.Sprintf(" + 英文标准字「%s」", en)
	}

	return []ItemSpec{
		// ===== 免费前置：Logo 组合卡 =====
		{"system_card", "Logo 组合卡", "system",
			fmt.Sprintf("免费 Logo 组合卡：只展示一个完整的主 Logo 组合，包含 Symbol + 中文标准字「%s」%s。三者按统一比例组合，居中，浅色纯净背景，留白充足。不要色板、色值、字体规范、材质小样、辅助纹样或任何应用场景。", cn, enLogo),
			"4:5", 1, false, false},

		// ===== 第一批：核心 Logo 组合 =====
		{"logo_h", "Logo 横版标准组合", "logo",
			fmt.Sprintf("Logo 横版标准组合：Symbol 在左，中文品牌名「%s」和英文品牌名「%s」在右，水平居中排列。大量留白（≥60%%）。适合网站页头、名片顶部。", cn, en),
			"1:1", 2, true, true},

		{"logo_v", "Logo 竖版标准组合", "logo",
			fmt.Sprintf("Logo 竖版标准组合：Symbol 在上方居中，中文品牌名「%s」在中间，英文品牌名「%s」在下方。垂直排列，大量留白。适合竖版海报、包装侧面。", cn, en),
			"1:1", 3, true, true},

		{"logo_icon", "Logo 图标", "logo",
			fmt.Sprintf("仅 Symbol 图标，无任何文字。居中于画面，大量留白（≥70%%）。适合做 App 图标、favicon、水印。保持 Symbol 的完整轮廓和负空间结构。"),
			"1:1", 4, true, true},

		{"logo_mono", "Logo 单色黑版", "logo",
			fmt.Sprintf("完整 Logo 的纯黑色版本，纯白色背景。保持所有比例和结构不变，仅颜色改为 #000000。适合单色印刷、文件盖章。"),
			"1:1", 5, true, true},

		{"logo_white", "Logo 反白版", "logo",
			fmt.Sprintf("完整 Logo 的纯白色版本，背景使用品牌主色 %s。保持所有比例和结构不变，颜色改为 #FFFFFF。适合深色背景、包装内衬。", primary),
			"1:1", 6, true, true},

		{"card_front", "名片正面", "apps",
			fmt.Sprintf("名片正面设计，标准横版比例（90:54mm）。包含：品牌名「%s」（使用品牌字体和主色）、姓名占位符、职位占位符、电话和邮箱占位符。排版简洁专业。", cn),
			"4:5", 7, false, true},

		{"card_back", "名片背面", "apps",
			fmt.Sprintf("名片背面设计，标准横版比例（90:54mm）。大面积使用品牌主色 %s 或辅助图形，Logo Symbol 居中或角部放置。极简设计，不放文字信息。", primary),
			"4:5", 8, false, true},

		// ===== 第二批：视觉系统 + 提案 =====
		{"worldview", "品牌世界观封面", "boards",
			fmt.Sprintf("品牌世界观主视觉：大标题「%s」+ 大型品牌主视觉 + 完整 Logo + 3-5 张情绪/材质拼贴 + 品牌宣言「%s」。Editorial 杂志风格，Logo 可见但不压主标题。", cn, slogan),
			"4:5", 9, false, true},

		{"color_typo", "品牌色彩与字体规范", "system",
			fmt.Sprintf("品牌色彩与字体规范：主色 %s 的 HEX/RGB 值、辅助色和点缀色色值、色彩使用比例（60/30/10）、标题字体样张、正文字体样张、中英文混排示例。", primary),
			"4:5", 10, false, false},

		{"letterhead", "信纸设计", "apps",
			fmt.Sprintf("A4 信纸设计：页眉放品牌名「%s」和 Logo，页脚放联系方式占位符。大面积留白供正文书写。使用品牌色和字体；最终交付为提案展示图，不是印刷出血文件。", cn),
			"4:5", 11, false, true},

		{"wx_avatar", "微信头像", "apps",
			fmt.Sprintf("微信头像设计：正方形，Logo Symbol 居中，背景使用品牌主色 %s 的浅色调或渐变。在圆形裁切安全区内完整展示。", primary),
			"1:1", 12, true, true},

		{"wx_cover", "公众号封面", "apps",
			fmt.Sprintf("微信公众号封面横幅：横版 2.35:1 比例。品牌主视觉 + 品牌标语「%s」+ Logo。使用品牌色系；最终交付为提案展示图，不是公众号后台直接上传的成品尺寸。", slogan),
			"4:5", 13, false, true},

		{"ppt_cover", "PPT 模板封面", "apps",
			fmt.Sprintf("演示文稿封面页模板：横版 16:9 版式。品牌主色 %s 背景 + Logo + 大标题占位区域 + 简洁几何装饰。最终交付为 4:5 提案展示图，不是可编辑 PPT 源文件。", primary),
			"4:5", 14, false, true},

		{"photo_style", "品牌摄影与材质指南", "system",
			fmt.Sprintf("品牌摄影与材质指南：展示品牌指定材质（%s）的实拍样片、品牌色调的摄影氛围参考、光线和构图说明。2-3 张风格样片排列；这是方向指南，不包含真实拍摄执行。", f["materials"]),
			"4:5", 15, false, false},

		{"logo_logic", "Logo 概念逻辑", "boards",
			fmt.Sprintf("Logo 设计逻辑展示：超大 Symbol 居中 + 概念来源草图 + 负空间结构分析 + 手绘箭头和圈选标注。研究感版面，解释「为什么这样设计」。"),
			"4:5", 16, false, true},

		{"app_overview", "品牌应用场景总览", "boards",
			fmt.Sprintf("品牌应用场景总览：一张大图展示品牌在 5-7 个触点上的应用效果：%s。每个触点独立展示但整体风格统一。", f["scenarios"]),
			"4:5", 17, false, true},

		// 品牌故事（非图片，Priority 最低）
		{"brand_story", "品牌故事文档", "apps",
			"", // 空提示词 = 不走图像 API，直接写 .md 文件
			"", 18, false, false},
	}
}

// itemsByPack 按包分组
func itemsByPack(f BrandForm, pack string) []ItemSpec {
	var out []ItemSpec
	for _, s := range itemSpecs(f) {
		if s.Pack == pack {
			out = append(out, s)
		}
	}
	return out
}

// buildItemPrompt 拼接完整提示词
func buildItemPrompt(f BrandForm, spec ItemSpec) string {
	return buildItemPromptWithContext(f, spec, nil, nil)
}

func buildItemPromptWithContext(f BrandForm, spec ItemSpec, snap *SelectedDirectionSnapshot, refs []referenceImage) string {
	if spec.Prompt == "" {
		return "" // 品牌故事等非图片项
	}
	sizeNote := "4:5 竖版（1024×1280）"
	if spec.IsSquare {
		sizeNote = "1:1 方形（1024×1024）"
	}
	summary := ""
	if snap != nil && snap.Summary != "" {
		summary = "\n\n" + snap.Summary
	}
	roles := referenceRoleBlock(refs)
	if roles != "" { roles = "\n\n" + roles }
	c := constraintsFor(f)
 brief := fallbackBrandBrief(f)
 if snap != nil && snap.Constraints != nil { c = *snap.Constraints; brief.Constraints = c; brief.ExactWordmark = c.wordmark() }
 if snap != nil && snap.Title!="" {
  d := LogoDirection{ID:snap.DirectionID,Title:snap.Title,DesignHypothesis:snap.VisualMechanism,Typography:snap.Typography,Palette:snap.Colors,Presentation:snap.Presentation}
  if snap.StructuredDirection != nil {d=*snap.StructuredDirection}
  d=constrainDirection(d,c)
  if c.Materials!="" {d.Presentation.Medium=c.Materials}
  summary="\n\n"+logoDirectionSummary(d)
 }
 effective := BrandForm{}
 for k,v := range f { effective[k]=v }
 for k,v := range map[string]string{"cn_name":c.ChineseWordmark,"en_name":c.EnglishWordmark,"lang":c.Language,"logo_type":c.LogoType,"primary_color":c.PrimaryColor,"accent_colors":c.AccentColors,"materials":c.Materials} { if v!="" || k=="en_name" {effective[k]=v} }
 if c.PrimaryColor=="" && snap!=nil && len(snap.Colors)>0 { effective["primary_color"]=snap.Colors[0] }
 if c.Materials=="" && snap!=nil { effective["materials"]=snap.Presentation.Medium }
 if c.Language=="英文" || strings.EqualFold(c.Language,"English") { effective["cn_name"]=c.wordmark() }
 if c.ColorAuthority=="confirmed_logo" {effective["primary_color"]="以已确认 Logo 母版为准";effective["accent_colors"]="";roles=strings.ReplaceAll(roles,"构图语言、色彩关系、材质","构图语言、材质");roles+="\n修订后的标志及应用颜色以已确认 Logo 母版为准，方向卡只提供构图、媒介与氛围。"}
 task := spec.Prompt
 for _,candidate := range itemSpecs(effective) { if candidate.Kind==spec.Kind { task=candidate.Prompt; break } }
 if spec.Kind == "system_card" || spec.Kind == "logo_h" || spec.Kind == "logo_v" {
  layout := map[string]string{"system_card":"一个主 Logo 组合，单一底色", "logo_h":"横向组合", "logo_v":"纵向组合"}[spec.Kind]
  task = layout + "；按指定 Logo 类型排列准确字标「"+c.wordmark()+"」；保持已确认标志的比例与身份。"
 }
 task = strings.ReplaceAll(task,"Editorial 杂志风格，", "按已选方向的构图与媒介，")
 return fmt.Sprintf("%s\n\n%s%s\n\n【任务】%s\n%s%s\n\n输出 %s 独立成图。保持唯一已选方向；用户明确约束优先，已确认 Logo 的身份一致。",
  c.prompt(), factLines(brief), summary, spec.Name, task, roles, sizeNote)
}
