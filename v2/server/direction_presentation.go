package app

// Structured presentation for each art direction. The direction card and the
// selected-direction snapshot both read this data; prompts no longer share one
// "logo + concept + color block" layout.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

type DirectionPresentation struct {
	Format        string `json:"format"`
	MainVisual    string `json:"mainVisual"`
	Composition   string `json:"composition"`
	Density       string `json:"density"`
	LogoTreatment string `json:"logoTreatment"`
	Medium        string `json:"medium"`
}

type directionDetail struct {
	Mechanism string
	P         DirectionPresentation
}

var directionDetails = map[string]directionDetail{
	"A · 系统坐标": {"用坐标、模块和精确层级把技术能力变成可读的品牌系统。", DirectionPresentation{"系统坐标卡", "线框 Symbol、坐标点和模块网格", "严格模块网格与横向基线，信息按系统层级排列", "medium", "Symbol 作为坐标原点，字号克制，靠位置建立层级", "数字界面、金属、玻璃；干净硬光"}},
	"B · 动态字标": {"让字标成为运动中的主视觉，而不是静态 Logo 展示。", DirectionPresentation{"动态字标海报", "被拉伸、错位、重复的首字母或笔画", "超大字号斜切构图，标题参与画面切割", "high", "字标本身就是主视觉，允许大尺度裁切", "动态图形、平面海报；硬边、速度线"}},
	"C · 温和科技": {"把精密感降噪，用柔和几何和留白表达可信的技术。", DirectionPresentation{"温和科技产品卡", "圆角连续路径、柔和负空间、产品细节", "悬浮留白、轻量信息层级", "low", "Logo 中小尺寸，放在安静的产品场景里", "磨砂材质、柔光摄影；漫射光"}},

	"A · 时装编辑": {"用时装杂志的裁切和层级建立品牌的当代感。", DirectionPresentation{"时装编辑跨页", "织物、金属配件、人物局部或产品细节", "非对称裁切，纵向标题压住画面一侧", "medium", "Logo 小尺寸但位置精准，像杂志刊头", "棚拍硬光、织物与金属；真实阴影"}},
	"B · 粗野构成": {"用字号、黑块和切断制造海报式冲击。", DirectionPresentation{"粗野主义海报", "超大粗体字、黑白色块、几何切口", "强对比、错位、边缘被切断", "high", "Logo 作为巨型字标或几何块出现", "丝网印刷、纸张颗粒；黑/白/一色"}},
	"C · 浪漫有机": {"用手绘比例和柔和曲线表达有温度的个性。", DirectionPresentation{"手写样本页", "铅笔曲线、植物轮廓、纸面留白", "偏心构图、疏密不均，像私人手稿", "low", "Logo 小尺寸、低视觉权重，与手写文字混排", "铅笔、水彩、再生纸；自然光"}},

	"A · 材料档案": {"用真实材料、微距和标签系统表达品牌来源。", DirectionPresentation{"材料样本档案", "材质微距、纸/陶/织物细节与标签文字", "材料微距占主要面积，边缘放少量标签式信息", "medium", "小尺寸置于角落，像档案编号而非广告 Logo", "摄影、纸、陶、织物；自然侧光"}},
	"B · 印章几何": {"把 Symbol 变成可压印的几何印记，强调尺度与边界。", DirectionPresentation{"印章/压印卡", "超大 Symbol、负空间与粗边界", "居中或徽章式构图，明确裁切与尺度对比", "high", "标志本身成为主视觉，可占画面 60% 以上", "印章、包装压印、木质或石材；硬光"}},
	"C · 植物手绘": {"让植物生长路径变成品牌的可触摸语言。", DirectionPresentation{"植物手绘档案", "铅笔/墨线植物线稿与手写注释", "白纸上展开，局部手写，疏密不均", "low", "Logo 小尺寸、低视觉权重，像页脚署名", "铅笔、墨线、纸张；柔光"}},

	"A · 空间摄影": {"用空间、光和材料比例建立品牌的空间感。", DirectionPresentation{"空间摄影内页", "建筑空间、家具或材质在自然光下的局部", "大面积空间留白，横向构图，像建筑杂志", "low", "Logo 小尺寸放在页边或画面底部", "建筑摄影、室内空间；自然光长阴影"}},
	"B · 构成主义": {"用斜线、色块和粗字把空间语言变成海报结构。", DirectionPresentation{"构成主义海报", "斜向色块、重复模块、粗体标题", "强方向性、对角线和高对比", "high", "Logo 进入色块或字标系统，成为结构的一部分", "印刷、色块、纸；硬边无渐变"}},
	"C · 手作自然": {"用不完美的比例和手作痕迹表达材料温度。", DirectionPresentation{"手作材料卡", "织物、陶土、木、纸的自然纹理", "不规则留白、柔和比例、非完美圆", "low", "Logo 小尺寸，像手作品牌署名", "织物、陶土、木；柔光与真实阴影"}},

	"A · 柔光极简": {"用柔光、留白和产品细节表达轻盈的护理感。", DirectionPresentation{"柔光产品卡", "玻璃、霜体、皮肤/材质细节", "大量留白、产品居中、柔和不对称", "low", "Logo 中等偏小，居中但不形成硬边界", "玻璃、霜体、柔光摄影；浅阴影"}},
	"B · 实验字体": {"把配方与实验感转成字号、网格和透明材质。", DirectionPresentation{"实验标签", "超大字号、透明材质、分子/字母结构", "网格错位、裁切、实验室标签式信息", "high", "Logo 作为字标实验的一部分，允许被裁切", "印刷、透明材质、网格；冷光硬边"}},
	"C · 植物配方": {"用植物标本和配方编号建立自然与功效的联想。", DirectionPresentation{"植物配方标本页", "植物标本、配方编号、手写结构", "标本式排版、自然留白、手写标注", "medium", "Logo 小尺寸，与配方编号并置", "植物标本、纸、玻璃；自然光"}},

	"A · 编辑档案": {"用档案编号、栏线和图文混排建立研究感。", DirectionPresentation{"编辑档案页", "档案照片、编号、栏线与注释", "图文混排、栏线、页码和编号构成秩序", "medium", "Logo 作为刊头或档案标记，不占据主视觉", "纸张、印刷、档案照片；自然光"}},
	"B · 字体海报": {"让文字本身成为主视觉，压缩信息密度。", DirectionPresentation{"字体海报", "超大文字、裁切、错位和留白", "文字即构图，标题占据大部分画面", "high", "Logo 与标题融合或成为落款", "丝网印刷、海报；硬边颗粒"}},
	"C · 手绘叙事": {"用插图与手写讲述品牌故事，降低广告感。", DirectionPresentation{"手绘叙事页", "手绘插图、人物/物件线稿与手写字", "非规则版式、图文穿插、留白随叙事变化", "low", "Logo 小尺寸，像故事署名", "铅笔、水彩、拼贴；柔光纸纹"}},

	"A · 临床极简": {"用清晰网格和低刺激视觉建立可信与安全感。", DirectionPresentation{"临床信息卡", "几何路径、圆点、柔和图表", "清晰网格、充足留白、信息优先", "low", "Logo 中等尺寸，放在信息层级顶端", "纸张、哑光表面；漫射光"}},
	"B · 有机路径": {"用连续曲线表达身体、动作或自然的流动。", DirectionPresentation{"有机路径卡", "连续曲线、柔和负空间、动作轨迹", "呼吸感留白、非对称、路径引导视线", "low", "Logo 与路径连接，尺寸偏小", "织物、木、柔光摄影；自然光"}},
	"C · 温暖人文": {"用生活场景与手写细节建立亲近感。", DirectionPresentation{"生活场景卡", "日常物件、手写标注、柔和轮廓", "亲近比例、生活化构图、轻手写", "medium", "Logo 小尺寸，像生活品牌签名", "纸张、布料、日常物件；自然光"}},

	"A · 材质编辑": {"用材料微距与标签式排版建立真实质感。", DirectionPresentation{"材料编辑样本", "材质微距、标签文字、纸/织物", "材料占主画面，标签信息在边缘", "medium", "Logo 小尺寸置于角落或页脚", "摄影、纸、织物；自然侧光"}},
	"B · 几何系统": {"用模块、网格和高对比建立可扩展的视觉系统。", DirectionPresentation{"几何系统海报", "模块化几何、负空间切口、粗字", "严格网格、强对比、明确对齐", "high", "Logo 与几何模块共用结构，可大尺度出现", "印刷、数字、金属；硬光"}},
	"C · 有机叙事": {"用曲线、手绘和非规则留白讲述品牌个性。", DirectionPresentation{"有机叙事页", "自然路径、手绘轮廓、纸面留白", "偏心构图、疏密不均、像一页手稿", "low", "Logo 小尺寸、低视觉权重，像手写署名", "铅笔、水彩、拼贴；柔光纸纹"}},
}

func presentationFor(d Direction) DirectionPresentation {
	if x, ok := directionDetails[d.Name]; ok {
		return x.P
	}
	return DirectionPresentation{
		Format: "方向探索卡", MainVisual: d.Layout, Composition: d.Layout,
		Density: "medium", LogoTreatment: "按方向决定 Logo 大小与位置",
		Medium: d.Medium,
	}
}

func mechanismFor(d Direction) string {
	if x, ok := directionDetails[d.Name]; ok {
		return x.Mechanism
	}
	return d.Layout + "；以" + d.Medium + "为主要媒介。"
}

// directionConfigVersion makes a snapshot traceable to the direction data used
// when the customer chose it.
var directionConfigVersion = func() string {
	payload, _ := json.Marshal(struct {
		Sets    []directionSet
		Default [3]Direction
		Details map[string]directionDetail
	}{directionSets, defaultDirections, directionDetails})
	sum := sha256.Sum256(payload)
	return "dir-" + hex.EncodeToString(sum[:6])
}()

func compactBrandFacts(f BrandForm) string {
	name := f["cn_name"]
	if f["en_name"] != "" { name += " / " + f["en_name"] }
	lines := []string{"品牌：" + name}
	add := func(label, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			lines = append(lines, label+"："+value)
		}
	}
	add("行业与产品", strings.TrimSpace(f["industry"]+" · "+f["products"]))
	add("定位人群", f["positioning"])
	add("性格关键词", f["keywords"])
	add("视觉风格", f["style"])
	if c := strings.TrimSpace(f["primary_color"] + " / " + f["accent_colors"]); c != "" {
		add("色彩", c)
	}
	return strings.Join(lines, "\n")
}

func limitAvoid(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '、' || r == '，' || r == ',' || r == '；' || r == ';' })
	out := make([]string, 0, 3)
	for _, p := range parts {
		p = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(p), "禁止"))
		if p != "" {
			out = append(out, p)
		}
		if len(out) == 3 {
			break
		}
	}
	return strings.Join(out, "；")
}

func directionPrompt(f BrandForm, d Direction) string {
	p := presentationFor(d)
	neg := limitAvoid(d.Avoid)
	return fmt.Sprintf(`【方向机制】%s

【Presentation】
- 形式：%s
- 主视觉：%s
- 构图：%s
- 信息密度：%s
- Logo 处理：%s
- 媒介：%s

【品牌事实】
%s

【质量底线】%s

【禁止】%s

【输出】生成一张 4:5 竖版方向卡。只展示这一个方向；允许使用摄影、材质或纸张背景；不要套用其他方向的构图。不要 VI 表格、mockup 合集、大段正文。方向卡中的 Logo 只是探索稿，不是正式标志母版。`,
		mechanismFor(d), p.Format, p.MainVisual, p.Composition, p.Density, p.LogoTreatment, p.Medium,
		compactBrandFacts(f), commonRule, neg)
}
