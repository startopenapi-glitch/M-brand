package app

// api.go — 路由装配：用户端 / 管理端 / 静态资源

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

var immutableAssetPattern = regexp.MustCompile(`^/assets/[^/]+-[A-Za-z0-9_-]{8,}\.(?:js|css|woff2|png|webp|jpg|svg)$`)

func buildRouter() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.RedirectTrailingSlash = false
	r.RedirectFixedPath = false
	r.Use(gin.Recovery(), requestSecurity())
	// Caddy 在本机回环反代：仅信任本机代理，保证 ClientIP() 为真实客户 IP（防 XFF 伪造）
	_ = r.SetTrustedProxies([]string{"127.0.0.1", "::1"})
	api := r.Group(basePath + "/api")

	// ---- 公开 ----
	api.GET("/health", func(c *gin.Context) {
		worker := workerHealthSnapshot()
		ready, _ := worker["ready"].(bool)
		status := 200
		if !ready {
			status = 503
		}
		c.JSON(status, gin.H{"ok": ready, "service": "M-brand", "worker": worker,
			"img_api": map[bool]string{true: "configured", false: "unconfigured"}[imgConfigured()]})
	})
	api.GET("/cases", listCases)
	api.POST("/code", submitCode)
	api.POST("/admin/login", adminLogin)
	api.POST("/admin/totp/setup", adminTOTPSetup)
	api.POST("/admin/totp/verify", adminTOTPVerify)
	api.POST("/admin/logout", func(c *gin.Context) {
		c.SetCookie(adminCookie, "", -1, "/", "", false, true) // Path=/(根空间为规范 URL；/0brand 为兼容空间)
		c.JSON(200, gin.H{"ok": true})
	})

	// ---- 用户（公开：身份=匿名会话，归属校验在各 handler 内；进门不验票） ----
	api.POST("/jobs", createJob)
	api.POST("/support", supportCreate)
	api.GET("/support", supportList)
	api.POST("/logo-reference", uploadLogoReference)
	api.GET("/jobs/:id", jobState)
	api.POST("/jobs/:id/choose", chooseDirection)
	api.POST("/jobs/:id/unlock", unlockDirections)
	api.POST("/jobs/:id/unlock-code", unlockDirectionsWithCode)
	api.POST("/jobs/:id/logo/confirm", confirmLogo)
	api.POST("/jobs/:id/logo/revise", reviseLogo)
	api.POST("/jobs/:id/retry", retryJob)
	api.GET("/my/orders", myOrders)
	api.GET("/my/jobs", myJobs)
	api.GET("/plan", planHandler)
	api.GET("/draft", draftGet)
	api.PUT("/draft", draftPut)
	api.POST("/recover", recoverOrder)

	// ---- 分包解锁 ----
	api.GET("/packs", func(c *gin.Context) { c.JSON(410, gin.H{"error": "已统一为按项购买，请使用 /api/items"}) })
	api.GET("/items", listItems)
	api.GET("/jobs/:id/detail", jobDetailAlacarte)
	api.GET("/jobs/:id/download", jobDownload)
	api.POST("/pay/pack", packPayCreate)

	// ---- 支付（公开：匿名可下单；订单 ID 为不可枚举凭证） ----
	// 按项购买：核验付款后记录项目权益；不再创建通用票。
	api.POST("/pay/create", payCreate)
	api.GET("/pay/checkout/:id", getCheckout)
	api.POST("/pay/orders/:id/resume", resumeCheckout)
	api.GET("/pay/status/:id", payOrderStatus)
	api.GET("/pay/order/:id", payOrderDetail)
	api.POST("/pay/orders/:id/confirm", payConfirm)

	// ---- 管理后台 ----
	admin := api.Group("/admin", requireAdmin())
	admin.GET("/session", adminSession)
	admin.GET("/orders/page", adminOrderPage)
	admin.GET("/stats", adminStats)
	admin.GET("/codes", adminListCodes)
	admin.GET("/orders", adminListOrders)
	admin.POST("/codes", adminCreateCode)
	admin.POST("/codes/:code/revoke", adminRevokeCode)
	admin.POST("/codes/:code/bind", adminBindCode)
	admin.GET("/jobs", adminListJobs)
	admin.GET("/jobs/:id", adminJobDetail)
	admin.POST("/jobs/:id/retry", adminRetryJob)
	admin.POST("/cases", adminPublishCase)
	admin.PATCH("/cases/:id", adminUpdateCase)
	admin.DELETE("/cases/:id", adminDeleteCase)
	admin.GET("/usage", adminUsage)
	admin.GET("/img-probe", adminImgProbe)

	// ---- 代理分销 ----
	api.POST("/agent/login", agentLogin)
	api.GET("/agent/invitations/:token", agentInviteInfo)
	api.POST("/agent/invitations/:token/accept", agentInviteAccept)
	api.POST("/agent/logout", agentLogout)
	api.GET("/agents/:code/check", checkAgentCode)
	agent := api.Group("/agent", func(c *gin.Context) {
		if agentOK(c) == nil {
			c.AbortWithStatusJSON(401, gin.H{"error": "请先登录"})
			return
		}
		c.Next()
	})
	agent.GET("/me", agentMe)
	agent.GET("/me/stats", agentStats)
	agent.GET("/me/commissions", agentCommissions)
	agent.PUT("/me/payout-account", agentPayoutAccount)
	agent.GET("/me/payouts", agentPayoutList)
	agent.POST("/me/payouts", agentPayoutCreate)
	agent.GET("/me/statement.csv", agentStatementCSV)
	agent.GET("/me/analytics", agentAnalytics)
	admin.GET("/agents", adminListAgents)
	admin.POST("/agents", adminCreateAgent)
	admin.POST("/agents/invite", agentInviteCreate)
	admin.GET("/agents/invitations", adminListAgentInvitations)
	admin.PATCH("/agents/invitations/:id", adminUpdateAgentInvitationCommission)
	admin.PATCH("/agents/:id", adminUpdateAgent)
	admin.POST("/agents/:id/payout", adminAgentPayout)
	admin.GET("/agents/stats", adminAgentStats)

	admin.GET("/audit", adminAudit)
	admin.GET("/support", adminSupportList)
	admin.POST("/support/:id", adminSupportReply)
	admin.GET("/analytics", adminAnalytics)
	admin.GET("/agents/reconcile", adminAgentReconcile)
	admin.GET("/agent-payouts", adminAgentPayoutList)
	admin.GET("/mail-status", func(c *gin.Context) { c.JSON(200, mailStatus()) })
	admin.POST("/agent-payouts/:id/review", adminAgentPayoutReview)

	// ---- 支付 ----
	api.GET("/pay/status", payMeta)
	api.POST("/pay/notify", payNotify) // 公开端点：微信回调

	// ---- 生图产物（公开只读） ----
	r.GET(basePath+"/gen/*filepath", serveGenerated)
	r.HEAD(basePath+"/gen/*filepath", serveGenerated)

	// ---- 用户协议（静态） ----
	r.GET(basePath+"/docs/user-agreement", func(c *gin.Context) {
		if data, err := webFS.ReadFile("webdist/docs/user-agreement.html"); err == nil {
			c.Data(200, "text/html; charset=utf-8", data)
			return
		}
		c.String(404, "not found")
	})
	r.GET(basePath+"/docs/refund", func(c *gin.Context) {
		c.Redirect(http.StatusFound, basePath+"/docs/user-agreement#s5")
	})

	// ---- 前端（embed） ----
	dist, _ := fs.Sub(webFS, "webdist")
	r.GET(basePath, serveIndex)
	r.GET(basePath+"/", serveIndex)
	r.NoRoute(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		p := c.Request.URL.Path
		if p != basePath && !strings.HasPrefix(p, basePath+"/") {
			c.String(404, "not found")
			return
		}
		rel := strings.TrimPrefix(p, basePath)
		if strings.HasPrefix(rel, "/api/") || rel == "/api" || rel == "/gen" {
			c.String(404, "not found")
			return
		}
		if rel == "" || rel == "/" || rel == "/index.html" {
			serveIndex(c)
			return
		}
		if f, err := dist.Open(strings.TrimPrefix(rel, "/")); err == nil {
			info, statErr := f.Stat()
			f.Close()
			if statErr != nil || info.IsDir() {
				c.Status(404)
				return
			}
			if immutableAssetPattern.MatchString(rel) {
				c.Header("Cache-Control", "public, max-age=31536000, immutable")
				// Vite content fingerprint is the semantic version of this resource.
				// Weak validator remains valid across gzip/zstd/identity representations.
				c.Header("ETag", `W/"`+strings.TrimPrefix(rel, "/assets/")+`"`)
			}
			if strings.HasPrefix(rel, "/assets/examples/") || strings.HasPrefix(rel, "/assets/brand/") || rel == "/favicon.svg" || strings.HasPrefix(rel, "/apple-touch-icon") {
				c.Header("Cache-Control", "public, max-age=604800, stale-while-revalidate=86400")
			}
			if servePrecompressedStatic(c, dist, rel) {
				return
			}
			c.FileFromFS(rel, http.FS(dist))
			return
		}
		if strings.HasPrefix(rel, "/assets/") || strings.Contains(rel, ".") {
			c.String(404, "not found")
			return
		}
		serveIndex(c) // 仅页面路径回退SPA，不吞API或静态资源错误
	})
	return r
}

// ---------- 用户端 ----------
type codeReq struct {
	Code string `json:"code"`
}

func submitCode(c *gin.Context) {
	var req codeReq
	if c.BindJSON(&req) != nil || req.Code == "" {
		c.JSON(400, gin.H{"error": "请输入邀请码"})
		return
	}
	code := strings.ToUpper(strings.TrimSpace(req.Code))
	// 失败计数限速（10 次/分/IP）：只对「兑换失败」计数，正常用户不受影响
	ip := c.ClientIP()
	if rateBlocked("code:"+ip, 10) {
		c.JSON(429, gin.H{"error": "尝试过于频繁，请 1 分钟后再试"})
		return
	}
	fail := func(msg string) {
		if !rateOK("code:"+ip, 10) {
			c.JSON(429, gin.H{"error": "尝试过于频繁，请 1 分钟后再试"})
			return
		}
		c.JSON(403, gin.H{"error": msg})
	}
	var max, used int
	var revoked bool
	var bound string
	if err := pool.QueryRow(c,
		"SELECT max_uses, used, revoked, COALESCE(bound_visitor,'') FROM invite_codes WHERE code=$1", code).
		Scan(&max, &used, &revoked, &bound); err != nil || revoked {
		fail("邀请码无效或已停用")
		return
	}
	vid := ensureVisitor(c)
	// 一码一人：已绑定他人 / 已被他人使用的码不可兑换；本人重复进入放行（用于查看历史）
	if bound != "" && bound != vid {
		fail("该邀请码已被使用或已绑定其他设备；站内购买请用订单号找回，其他渠道请联系站点管理员")
		return
	}
	if used >= max && bound != vid {
		fail("该邀请码已被使用或已绑定其他设备；站内购买请用订单号找回，其他渠道请联系站点管理员")
		return
	}
	// 首次兑换即绑定（条件更新，防并发双抢）
	tag, _ := pool.Exec(c, `UPDATE invite_codes SET bound_visitor=$2
		WHERE code=$1 AND (bound_visitor IS NULL OR bound_visitor='')`, code, vid)
	if tag.RowsAffected() == 0 && bound != vid {
		var b2 string
		pool.QueryRow(c, "SELECT COALESCE(bound_visitor,'') FROM invite_codes WHERE code=$1", code).Scan(&b2)
		if b2 != "" && b2 != vid {
			fail("该邀请码已被使用或已绑定其他设备；站内购买请用订单号找回，其他渠道请联系站点管理员")
			return
		}
	}
	setGateCookie(c, code)
	audit("visitor:"+vid, "code_redeem", "invite_code", code, "")
	c.JSON(200, gin.H{"ok": true, "code": code, "remaining": max - used})
}

func createJob(c *gin.Context) {
	raw := map[string]string{}
	if !readJSONBodyLimit(c, &raw, 64<<10) {
		return
	}
	f := normalizeForm(BrandForm(raw))
	freezeDesignConstraints(f, BrandForm(raw))
	if f["cn_name"] == "" || f["industry"] == "" {
		c.JSON(400, gin.H{"error": "中文品牌名与行业为必填"})
		return
	}
	creationKey := c.GetHeader("Idempotency-Key")
	if creationKey != "" && !creationKeyPattern.MatchString(creationKey) {
		c.JSON(400, gin.H{"error": "创建请求编号无效"})
		return
	}
	// Establish the anonymous session before submitting a durable intention.
	// Otherwise a lost first response can lose both the job ID and its cookie.
	if creationKey != "" && visitorID(c) == "" {
		c.JSON(428, gin.H{"error": "请先保存品牌资料，再继续提交"})
		return
	}
	vid := ensureVisitor(c)
	ip := c.ClientIP()
	code := ""
	if gate := gateOK(c); gate != nil {
		code = gate.Code
	}
	// 支付成功已经将票绑定会话，不要求客户再手动兑换一次。
	if code == "" {
		if tickets := ticketCandidates(vid, "", ""); len(tickets) > 0 {
			code = tickets[0]
		}
	}
	trial := code == ""
	paywall := directionsPaywallEnabled()
	unlocked := true
	initialStage := "directions"
	initialStatus := "queued"
	quotaCode := ""
	jid := newID(12)
	logoSource := ""
	if token := strings.TrimSpace(raw["logo_ref"]); token != "" {
		src, err := validateLogoReference(vid, token)
		if err != nil {
			c.JSON(400, gin.H{"error": "Logo 参考图不可用，请重新上传"})
			return
		}
		logoSource = src
		f["logo_ref"] = "logo_ref.png"
	} else {
		delete(f, "logo_ref")
	}
	formJSON, _ := json.Marshal(f)
	ctx := c.Request.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		c.JSON(500, gin.H{"error": "系统错误"})
		return
	}
	defer tx.Rollback(ctx)
	// 旧权益归属在事务中复核；固定锁仅串行极短的SQL，生成不持锁。
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(197704,1)"); err != nil {
		c.JSON(500, gin.H{"error": "系统错误"})
		return
	}
	if replayCreation(c, tx, vid, creationKey, formJSON) {
		return
	}
	if paused, reason := salesPausedQuery(ctx, tx); paused {
		c.JSON(503, gin.H{"error": reason, "paused": true})
		return
	}
	if !allowCreate(ip) {
		c.JSON(429, gin.H{"error": "操作过于频繁，请 1 分钟后再试"})
		return
	}
	if code != "" {
		var verified string
		if err = tx.QueryRow(ctx, "SELECT code FROM invite_codes WHERE code=$1 AND bound_visitor=$2 AND revoked=false FOR SHARE", code, vid).Scan(&verified); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// A changed historical ticket cannot block a new free proposal or
				// confer its former rights. Keep the new job independent of it.
				code = ""
				trial = true
			} else {
				c.JSON(500, gin.H{"error": "系统错误"})
				return
			}
		}
	}
	if paywall {
		// 9.9 paywall: no free trial. A bound, unused code unlocks immediately;
		// otherwise the job stays locked until the 9.9 checkout settles.
		trial = false
		unlocked = false
		initialStage = "unlock"
		initialStatus = "waiting_unlock"
		if code != "" {
			tag, claimErr := tx.Exec(ctx, `UPDATE invite_codes SET used=used+1,
				used_by=COALESCE(NULLIF(used_by,''),$2),used_at=COALESCE(used_at,now())
				WHERE code=$1 AND revoked=false AND used<max_uses AND bound_visitor=$2`, code, vid)
			if claimErr == nil && tag.RowsAffected() == 1 {
				unlocked = true
				quotaCode = code
				initialStage = "directions"
				initialStatus = "queued"
			} else {
				code = ""
			}
		}
	} else {
		if ok, why := allowTrialQuery(ctx, tx, vid, ip); !ok {
			c.JSON(503, gin.H{"error": why, "need_ticket": false})
			return
		}
	}
	if logoSource != "" {
		if err := copyLogoReference(logoSource, jid); err != nil {
			c.JSON(503, gin.H{"error": "Logo 参考图保存失败，请重试"})
			return
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO jobs(id, code, form, status, stage, visitor, ip, trial, quota_code, directions_unlocked, legacy_code_rights, creation_owner, creation_key)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10,false,$6,NULLIF($11,''))`, jid, code, formJSON, initialStatus, initialStage, vid, ip, trial, quotaCode, unlocked, creationKey); err != nil {
		c.JSON(500, gin.H{"error": "系统错误"})
		return
	}
	if trial {
		if _, err = tx.Exec(ctx, "UPDATE visitors SET trial_used=trial_used+1 WHERE id=$1", vid); err != nil {
			c.JSON(500, gin.H{"error": "系统错误"})
			return
		}
	}
	if err = tx.Commit(ctx); err != nil {
		c.JSON(500, gin.H{"error": "系统错误"})
		return
	}
	recordAnalytics(c, "job_created", jid)
	if trial {
		audit("visitor:"+vid, "trial_start", "job", jid, "")
	}
	c.JSON(201, gin.H{"id": jid, "trial": trial, "stage": initialStage, "directions_unlocked": unlocked})
}

func jobState(c *gin.Context) {
	jid := c.Param("id")
	vid := visitorID(c)
	gateCode := ""
	if gate := gateOK(c); gate != nil {
		gateCode = gate.Code
	}
	var status, stage, errText, owner, jobCode string
	var chosen *int
	var formJSON []byte
	var trial, refunded, logoConfirmed, directionsUnlocked bool
	if err := pool.QueryRow(c,
		`SELECT status, stage, chosen, COALESCE(error,''), form,
		        COALESCE(visitor,''), COALESCE(NULLIF(quota_code,''),code,''), trial, refunded, logo_confirmed, directions_unlocked
		 FROM jobs WHERE id=$1`, jid).
		Scan(&status, &stage, &chosen, &errText, &formJSON, &owner, &jobCode, &trial, &refunded, &logoConfirmed, &directionsUnlocked); err != nil {
		readFailure(c, err, "任务不存在")
		return
	}
	if !jobOwned(c, vid, gateCode, owner, jobCode) {
		c.JSON(404, gin.H{"error": "任务不存在"})
		return
	}
	rights, e := jobEntitlements(c, pool, jid)
	if e != nil {
		c.JSON(503, gin.H{"error": "权益暂不可用"})
		return
	}
	imgs := []imgRecord{}
	rows, queryErr := pool.Query(c,
		"SELECT kind, idx, COALESCE(file,''), tokens FROM job_images WHERE job_id=$1 ORDER BY id", jid)
	if queryErr != nil {
		readFailure(c, queryErr, "任务不存在")
		return
	}
	for rows.Next() {
		var r imgRecord
		if e := rows.Scan(&r.Kind, &r.Idx, &r.File, &r.Tokens); e != nil {
			rows.Close()
			readFailure(c, e, "任务不存在")
			return
		}
		r.Kind = artifactKind(r.Kind, r.Idx)
		if !rights[r.Kind] {
			r.File = ""
		}
		imgs = append(imgs, r)
	}
	queryErr = rows.Err()
	rows.Close()
	if queryErr != nil {
		readFailure(c, queryErr, "任务不存在")
		return
	}
	countKind := func(k string) int {
		n := 0
		for _, im := range imgs {
			if im.Kind == k && im.File != "" {
				n++
			}
		}
		return n
	}
	progress := fmt.Sprintf("方向 %d/3", countKind("direction"))
	if stage == "boards" || stage == "done" {
		done, total := 0, 0
		for _, sp := range itemSpecs(BrandForm{}) {
			if rights[sp.Kind] {
				total++
				done += countKind(sp.Kind)
			}
		}
		progress = fmt.Sprintf("已完成 %d/%d 项", done, total)
	}
	// 解锁信息：到达「选定方向」而名下无票 → 前端弹解锁卡（扫码直付 / 拿码兑换）
	needUnlock := false
	// 失败安置：人话文案（原始细节只给管理端/日志）+ 是否可自助重试
	userErr := ""
	if errText != "" {
		userErr = userMessage(errText, trial && stage != "boards" && stage != "done", refunded)
	}
	delivery, e := readDeliveryProgress(c, jid)
	if e != nil {
		readFailure(c, e, "任务不存在")
		return
	}
	if status == "error" {
		pending, e := generationNeedsReview(jid)
		if e != nil {
			readFailure(c, e, "任务不存在")
			return
		}
		if pending {
			delivery.NeedsReview = true
			userErr = userMessage(errGenerationUnknown.Error(), false, false)
		}
	}
	c.JSON(200, gin.H{"delivery": delivery, "id": jid, "status": status, "stage": stage, "chosen": chosen,
		"error": userErr, "images": imgs, "progress": progress,
		"trial": trial && stage != "boards" && stage != "done", "refunded": refunded, "logo_confirmed": logoConfirmed,
		"directions_unlocked": directionsUnlocked, "unlock_price_cents": directionUnlockPriceCents,
		"user_error": userErr, "can_retry": status == "error" && !delivery.NeedsReview,
		"need_unlock": needUnlock, "plan": planPayload()})
}

func chooseDirection(c *gin.Context) {
	jid := c.Param("id")
	var req struct {
		Direction int `json:"direction"`
	}
	if c.BindJSON(&req) != nil || req.Direction < 0 || req.Direction > 2 {
		c.JSON(400, gin.H{"error": "参数无效"})
		return
	}
	vid := visitorID(c)
	gateCode := ""
	if gate := gateOK(c); gate != nil {
		gateCode = gate.Code
	}
	var status, stage, owner, jobCode string
	var formRaw, directionsRaw []byte
	if err := pool.QueryRow(c,
		"SELECT status, stage, COALESCE(visitor,''), COALESCE(NULLIF(quota_code,''),code,''), form, logo_directions FROM jobs WHERE id=$1", jid).
		Scan(&status, &stage, &owner, &jobCode, &formRaw, &directionsRaw); err != nil {
		c.JSON(404, gin.H{"error": "任务不存在"})
		return
	}
	if !jobOwned(c, vid, gateCode, owner, jobCode) {
		c.JSON(404, gin.H{"error": "任务不存在"})
		return
	}
	if stage == "boards" || stage == "done" || stage == "logo" || stage == "logo_revision" {
		c.JSON(200, gin.H{"ok": true, "status": status, "reused": true})
		return
	}
	if status != "ready_to_choose" {
		c.JSON(409, gin.H{"error": "当前状态不可选择方向"})
		return
	}
	// 验票：进门不验票，正片验票
	cands := ticketCandidates(vid, jobCode, gateCode)

	ctx := c.Request.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		c.JSON(500, gin.H{"error": "系统错误"})
		return
	}
	defer tx.Rollback(ctx)
	// 与重试/找回采用相同锁顺序：先锁任务，再占票，避免并发时使用过期归属。
	if err = tx.QueryRow(ctx, "SELECT status,stage,COALESCE(visitor,''),COALESCE(NULLIF(quota_code,''),code,''),form,logo_directions FROM jobs WHERE id=$1 FOR UPDATE", jid).Scan(&status, &stage, &owner, &jobCode, &formRaw, &directionsRaw); err != nil {
		c.JSON(404, gin.H{"error": "任务不存在"})
		return
	}
	if !jobOwnedQuery(c, tx, vid, gateCode, owner, jobCode) {
		c.JSON(404, gin.H{"error": "任务不存在"})
		return
	}
	if stage == "boards" || stage == "done" || stage == "logo" || stage == "logo_revision" {
		c.JSON(200, gin.H{"ok": true, "status": status, "reused": true})
		return
	}
	if status != "ready_to_choose" {
		c.JSON(409, gin.H{"error": "当前状态不可选择方向"})
		return
	}
	claimed := ""
	for _, cand := range cands {
		// 原子占票：条件更新，防并发双花
		tag, e := tx.Exec(ctx,
			`UPDATE invite_codes SET used=used+1,
			   used_by=COALESCE(NULLIF(used_by,''), $2), used_at=COALESCE(used_at, now())
			 WHERE code=$1 AND revoked=false AND used<max_uses AND bound_visitor=$2`, cand, vid)
		if e == nil && tag.RowsAffected() == 1 {
			claimed = cand
			break
		}
	}
	if len(cands) > 0 && claimed == "" {
		c.JSON(402, gin.H{"error": "可用额度不足，请购买或兑换后再试", "need_ticket": true, "plan": planPayload()})
		return
	}
	var form BrandForm
	_ = json.Unmarshal(formRaw, &form)
	var snap SelectedDirectionSnapshot
	ok := false
	if dirs := decodeLogoDirections(directionsRaw); len(dirs) == 3 {
		snap, ok = buildLogoDirectionSnapshot(dirs[req.Direction]), true
	}
	if !ok {
		snap, ok = buildDirectionSnapshot(normalizeForm(form), req.Direction)
	}
	if !ok {
		c.JSON(409, gin.H{"error": "方向数据不可用"})
		return
	}
	constraints := constraintsFor(form)
	snap.Constraints = &constraints
	snapJSON, _ := json.Marshal(snap)
	tag, err2 := tx.Exec(ctx,
		`UPDATE jobs SET chosen=$2, stage='logo', status='queued', error='', quota_code=$3, direction_snapshot=$4, logo_confirmed=false, updated_at=now()
		 WHERE id=$1 AND status='ready_to_choose'`, jid, req.Direction, claimed, string(snapJSON))
	if err2 != nil || tag.RowsAffected() == 0 {
		c.JSON(409, gin.H{"error": "当前状态不可选择方向"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		c.JSON(500, gin.H{"error": "系统错误"})
		return
	}
	audit("visitor:"+vid, "unlock_consume", "job", jid,
		fmt.Sprintf(`{"code":"%s","direction":%d}`, claimed, req.Direction))
	c.JSON(200, gin.H{"ok": true, "status": "queued", "quota_code": claimed})
}

// ---------- 画廊（公开） ----------
func listCases(c *gin.Context) {
	rows, _ := pool.Query(c,
		"SELECT id, title, tagline, images FROM cases WHERE published=true ORDER BY sort DESC, id DESC")
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id int
		var title, tagline string
		var images []byte
		rows.Scan(&id, &title, &tagline, &images)
		var imgs []map[string]any
		json.Unmarshal(images, &imgs)
		out = append(out, gin.H{"id": id, "title": title, "tagline": tagline, "images": imgs})
	}
	c.JSON(200, out)
}

// ---------- 管理后台 ----------
var adminLoginAt = map[string]time.Time{}
var adminLoginMu sync.Mutex

func adminLogin(c *gin.Context) {
	ip := c.ClientIP()
	adminLoginMu.Lock()
	if t, ok := adminLoginAt[ip]; ok && time.Since(t) < 15*time.Second {
		adminLoginMu.Unlock()
		c.JSON(429, gin.H{"error": "尝试过于频繁，请 15 秒后再试"})
		return
	}
	adminLoginAt[ip] = time.Now()
	adminLoginMu.Unlock()
	var req struct{ Username, Password string }
	if c.BindJSON(&req) != nil {
		c.JSON(400, gin.H{"error": "参数无效"})
		return
	}
	var id int
	var hash, secret string
	var enabled bool
	var version int64
	err := pool.QueryRow(c, "SELECT id,password_hash,COALESCE(totp_secret,''),totp_enabled,session_version FROM admins WHERE username=$1",
		req.Username).Scan(&id, &hash, &secret, &enabled, &version)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		audit(req.Username, "admin_login_fail", "admin", "", "")
		c.JSON(401, gin.H{"error": "账号或密码不正确"})
		return
	}
	if !admin2FAEnabled() {
		setAdminCookie(c, id, req.Username, version)
		audit(req.Username, "admin_login", "admin", strconv.Itoa(id), "")
		c.JSON(200, gin.H{"ok": true})
		return
	}
	if !enabled {
		newSecret, e := newTOTPSecret()
		if e != nil {
			c.JSON(500, gin.H{"error": "系统错误"})
			return
		}
		challenge := issueAdminChallenge(id, req.Username, "setup", newSecret, ip)
		audit(req.Username, "admin_totp_setup_required", "admin", strconv.Itoa(id), "")
		c.JSON(200, gin.H{"totp_setup_required": true, "setup_token": challenge, "secret": newSecret, "otpauth_url": totpURL(newSecret, req.Username)})
		return
	}
	challenge := issueAdminChallenge(id, req.Username, "verify", "", ip)
	audit(req.Username, "admin_login_password_ok", "admin", strconv.Itoa(id), "")
	c.JSON(200, gin.H{"totp_required": true, "challenge": challenge})
}

func initAdmin() {
	var n int
	pool.QueryRow(contextBG(), "SELECT COUNT(*) FROM admins").Scan(&n)
	if n > 0 {
		return
	}
	user := os.Getenv("ZEROBRAND_ADMIN_USER")
	pass := os.Getenv("ZEROBRAND_ADMIN_PASS")
	if user == "" {
		user = "admin"
	}
	if pass == "" {
		pass = randHex(6)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	pool.Exec(contextBG(), "INSERT INTO admins(username,password_hash) VALUES ($1,$2)", user, string(hash))
	fmt.Println("初始管理员已创建，请使用已配置的凭据登录")
}

func adminStats(c *gin.Context) {
	var jobsTotal, jobsRunning, jobsFailed, codesActive int
	var todayImgs int
	pool.QueryRow(contextBG(), "SELECT COUNT(*) FROM jobs").Scan(&jobsTotal)
	pool.QueryRow(contextBG(), "SELECT COUNT(*) FROM jobs WHERE status IN ('queued','running','boards_running')").Scan(&jobsRunning)
	pool.QueryRow(contextBG(), "SELECT COUNT(*) FROM jobs WHERE status='error'").Scan(&jobsFailed)
	pool.QueryRow(contextBG(), "SELECT COUNT(*) FROM invite_codes WHERE revoked=false AND used<max_uses").Scan(&codesActive)
	pool.QueryRow(contextBG(), "SELECT COUNT(*) FROM job_images WHERE created_at>current_date").Scan(&todayImgs)
	var tokens30 int
	pool.QueryRow(contextBG(), "SELECT COALESCE(SUM(tokens),0) FROM usage_events WHERE created_at>now()-interval '30 days'").Scan(&tokens30)
	var trialToday, trialTotal int
	pool.QueryRow(contextBG(), "SELECT COUNT(*) FROM jobs WHERE trial=true AND created_at>current_date").Scan(&trialToday)
	pool.QueryRow(contextBG(), "SELECT COUNT(*) FROM jobs WHERE trial=true").Scan(&trialTotal)
	// 运营首屏：今日任务量与失败率（成品交付口径：done vs error）
	var jobsToday, jobsFailToday, jobsDoneToday int
	pool.QueryRow(contextBG(), "SELECT COUNT(*) FROM jobs WHERE created_at>current_date").Scan(&jobsToday)
	pool.QueryRow(contextBG(), "SELECT COUNT(*) FROM jobs WHERE status='error' AND created_at>current_date").Scan(&jobsFailToday)
	pool.QueryRow(contextBG(), "SELECT COUNT(*) FROM jobs WHERE status='done' AND created_at>current_date").Scan(&jobsDoneToday)
	failRate := 0.0
	if n := jobsFailToday + jobsDoneToday; n > 0 {
		failRate = float64(jobsFailToday) / float64(n) * 100
	}
	// 试用 → 付费转化（按会话去重，全时段）
	var trialVisitors, trialConverted int
	pool.QueryRow(contextBG(),
		"SELECT COUNT(DISTINCT visitor) FROM jobs WHERE trial=true AND COALESCE(visitor,'')<>''").Scan(&trialVisitors)
	pool.QueryRow(contextBG(), `SELECT COUNT(DISTINCT o.visitor) FROM orders o
		WHERE o.status='paid' AND COALESCE(o.visitor,'')<>''
		  AND EXISTS (SELECT 1 FROM jobs j WHERE j.visitor=o.visitor AND j.trial=true)`).Scan(&trialConverted)
	// 营收：口径 = status='paid' AND mock=false（测试单不计入，另给计数便于核对）
	var revToday, revTotal int64
	var ordersToday, ordersPaidToday, ordersPending, ordersExpired, ordersMock int
	pool.QueryRow(contextBG(), `
		SELECT COALESCE(SUM(amount_cents) FILTER (WHERE status='paid' AND mock=false AND paid_at>current_date),0),
		       COALESCE(SUM(amount_cents) FILTER (WHERE status='paid' AND mock=false),0),
		       COUNT(*) FILTER (WHERE created_at>current_date),
		       COUNT(*) FILTER (WHERE status='paid' AND paid_at>current_date),
		       COUNT(*) FILTER (WHERE status='pending'),
		       COUNT(*) FILTER (WHERE status='expired'),
		       COUNT(*) FILTER (WHERE mock=true)
		FROM orders`).Scan(&revToday, &revTotal, &ordersToday, &ordersPaidToday,
		&ordersPending, &ordersExpired, &ordersMock)
	paused, pausedReason := salesPaused()
	worker := workerHealthSnapshot()
	funnel := gin.H{}
	if rows, e := pool.Query(contextBG(), `SELECT event,COUNT(*) FROM analytics_events WHERE created_at>now()-interval '30 days' GROUP BY event`); e == nil {
		for rows.Next() {
			var ev string
			var n int64
			if rows.Scan(&ev, &n) == nil {
				funnel[ev] = n
			}
		}
		rows.Close()
	}
	c.JSON(200, gin.H{"worker": worker, "funnel": funnel, "jobs_total": jobsTotal, "jobs_running": jobsRunning,
		"jobs_failed": jobsFailed, "codes_active": codesActive,
		"today_images": todayImgs, "tokens_30d": tokens30,
		"trial_today": trialToday, "trial_total": trialTotal,
		"jobs_today": jobsToday, "fail_rate_today": failRate,
		"trial_visitors": trialVisitors, "trial_converted": trialConverted,
		"revenue_today_cents": revToday, "revenue_total_cents": revTotal,
		"orders_today": ordersToday, "orders_paid_today": ordersPaidToday,
		"orders_pending": ordersPending, "orders_expired": ordersExpired,
		"orders_mock":  ordersMock,
		"sales_paused": paused, "paused_reason": pausedReason,
		"img_health": imgHealthSnapshot()})
}

func adminListCodes(c *gin.Context) {
	rows, _ := pool.Query(contextBG(),
		`SELECT code, max_uses, used, revoked, COALESCE(note,''),
		        COALESCE(to_char(created_at,'YYYY-MM-DD HH24:MI:SS'),''),
		        COALESCE(bound_visitor,''), COALESCE(order_id,''),
		        COALESCE(to_char(used_at,'MM-DD HH24:MI'),''), COALESCE(used_by,'')
		 FROM invite_codes ORDER BY created_at DESC LIMIT 200`)
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var code, note, created, bound, oid, usedAt, usedBy string
		var max, used int
		var revoked bool
		if rows.Scan(&code, &max, &used, &revoked, &note, &created, &bound, &oid, &usedAt, &usedBy) != nil {
			continue
		}
		out = append(out, gin.H{"code": code, "max_uses": max, "used": used,
			"revoked": revoked, "note": note, "created_at": created,
			"bound_visitor": bound, "order_id": oid, "used_at": usedAt, "used_by": usedBy})
	}
	c.JSON(200, out)
}

// adminListOrders —— 管理端订单列表（只读）。
// 口径：默认剔除 mock 测试单（include_mock=1 才显示）；金额单位为分（amount_cents）。
func adminListOrders(c *gin.Context) {
	status := strings.TrimSpace(c.Query("status"))
	if status == "all" {
		status = ""
	}
	channel := strings.TrimSpace(c.Query("channel"))
	if channel == "all" {
		channel = ""
	}
	q := strings.TrimSpace(c.Query("q"))
	includeMock := c.Query("include_mock") == "1" || strings.EqualFold(c.Query("include_mock"), "true")
	limit := 200
	if n, err := strconv.Atoi(c.Query("limit")); err == nil && n > 0 {
		if n > 500 {
			n = 500
		}
		limit = n
	}
	// 过滤条件一律带 $n='' 守卫：空串参数直接比较会全表命中（见 AGENTS.md）。
	// 搜索用 left() 前缀比较而非 LIKE，避免用户输入的 % _ 被当通配符。
	rows, err := pool.Query(contextBG(), `
		SELECT o.id, o.amount_cents, o.channel, o.status, o.credited, o.mock,
		       COALESCE(o.wx_transaction_id,''), COALESCE(o.code,''), COALESCE(o.visitor,''),
		       COALESCE(to_char(o.created_at,'YYYY-MM-DD HH24:MI:SS'),''),
		       COALESCE(to_char(o.paid_at,'YYYY-MM-DD HH24:MI:SS'),'')
		FROM orders o
		WHERE ($1='' OR o.status=$1)
		  AND ($2='' OR o.channel=$2)
		  AND ($3='' OR left(o.id,length($3))=$3
		              OR left(COALESCE(o.wx_transaction_id,''),length($3))=$3
		              OR left(COALESCE(o.visitor,''),length($3))=$3)
		  AND ($4 OR o.mock=false)
		ORDER BY o.created_at DESC LIMIT $5`, status, channel, q, includeMock, limit)
	if err != nil {
		slog.Warn("adminListOrders 查询失败", "err", err)
		c.JSON(500, gin.H{"error": "查询失败"})
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id, ch, st, wx, ocode, visitor, created, paid string
		var cents int64
		var credited, mock bool
		if rows.Scan(&id, &cents, &ch, &st, &credited, &mock, &wx, &ocode, &visitor, &created, &paid) != nil {
			continue // 坏行跳过，不 panic（见 AGENTS.md）
		}
		out = append(out, gin.H{"id": id, "amount_cents": cents, "channel": ch, "status": st,
			"credited": credited, "mock": mock, "wx_transaction_id": wx, "code": ocode,
			"visitor": visitor, "created_at": created, "paid_at": paid})
	}
	c.JSON(200, out)
}

// adminCreateCode：一码一次 —— 只发单次码；要发 N 次用 count 批量发 N 张
func adminCreateCode(c *gin.Context) {
	var req struct {
		Count int    `json:"count"`
		Note  string `json:"note"`
	}
	if !readJSONBodyLimit(c, &req, 64<<10) {
		return
	}
	if req.Count <= 0 {
		req.Count = 1
	}
	if req.Count > 50 {
		c.JSON(400, gin.H{"error": "一次最多 50 张单次码"})
		return
	}
	codes := []string{}
	for i := 0; i < req.Count; i++ {
		code := strings.ToUpper(randHex(8))
		if _, err := pool.Exec(contextBG(),
			"INSERT INTO invite_codes(code,max_uses,note,item_kind) VALUES ($1,1,$2,'logo_icon')", code, req.Note); err != nil {
			continue
		}
		codes = append(codes, code)
	}
	audit(adminName(c), "code_create", "invite_code", strings.Join(codes, ","),
		fmt.Sprintf(`{"count":%d,"single_use":true,"item_kind":"logo_icon"}`, len(codes)))
	c.JSON(201, gin.H{"codes": codes, "count": len(codes)})
}

// adminBindCode：客服解绑/重绑（一码一人下的换设备兜底）
func adminBindCode(c *gin.Context) {
	code := strings.ToUpper(c.Param("code"))
	var req struct {
		Visitor string `json:"visitor"`
	}
	if !readJSONBodyLimit(c, &req, 64<<10) {
		return
	}
	tag, err := pool.Exec(contextBG(),
		"UPDATE invite_codes SET bound_visitor=NULLIF($2,'') WHERE code=$1", code, strings.TrimSpace(req.Visitor))
	if err != nil || tag.RowsAffected() == 0 {
		c.JSON(404, gin.H{"error": "邀请码不存在"})
		return
	}
	audit(adminName(c), "code_rebind", "invite_code", code, fmt.Sprintf(`{"visitor":"%s"}`, req.Visitor))
	c.JSON(200, gin.H{"ok": true, "bound": req.Visitor})
}

func adminRevokeCode(c *gin.Context) {
	code := c.Param("code")
	pool.Exec(contextBG(), "UPDATE invite_codes SET revoked=true WHERE code=$1", code)
	audit(adminName(c), "code_revoke", "invite_code", code, "")
	c.JSON(200, gin.H{"ok": true})
}

func adminListJobs(c *gin.Context) {
	rows, e := pool.Query(c, `SELECT j.id,COALESCE(j.code,''),j.status,j.stage,COALESCE(j.error,''),
 to_char(j.created_at,'YYYY-MM-DD HH24:MI:SS'),(SELECT COUNT(*) FROM job_images i WHERE i.job_id=j.id),
 EXISTS(SELECT 1 FROM generation_attempts a WHERE a.job_id=j.id AND (a.state='unknown' OR (a.state='submitted' AND j.status='error'))),
 COALESCE(GREATEST(0,extract(epoch FROM (CASE WHEN j.status IN ('done','ready_to_choose','error') THEN j.updated_at ELSE now() END-j.execution_started_at)))::bigint,0)
 FROM jobs j WHERE ($1='' OR j.id=$1) ORDER BY (j.status IN ('error','running','boards_running')) DESC,j.created_at DESC LIMIT 100`, strings.TrimSpace(c.Query("id")))
	if e != nil {
		readFailure(c, e, "任务不存在")
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id, code, status, stage, errText, created string
		var imgs int
		var review bool
		var elapsed int64
		if e = rows.Scan(&id, &code, &status, &stage, &errText, &created, &imgs, &review, &elapsed); e != nil {
			readFailure(c, e, "任务不存在")
			return
		}
		out = append(out, gin.H{"id": id, "code": code, "status": status, "stage": stage, "error": errText, "created_at": created, "images": imgs, "needs_review": review, "elapsed_seconds": elapsed})
	}
	if e = rows.Err(); e != nil {
		readFailure(c, e, "任务不存在")
		return
	}
	c.JSON(200, out)
}

func adminJobDetail(c *gin.Context) {
	jid := c.Param("id")
	var code, status, stage string
	var formJSON []byte
	if e := pool.QueryRow(c, "SELECT COALESCE(code,''),status,stage,form FROM jobs WHERE id=$1", jid).Scan(&code, &status, &stage, &formJSON); e != nil {
		readFailure(c, e, "不存在")
		return
	}
	var form BrandForm
	json.Unmarshal(formJSON, &form)
	rows, e := pool.Query(c, "SELECT kind,idx,COALESCE(file,''),COALESCE(tokens,0) FROM job_images WHERE job_id=$1 ORDER BY kind,idx", jid)
	if e != nil {
		readFailure(c, e, "不存在")
		return
	}
	var imgs = []imgRecord{}
	for rows.Next() {
		var im imgRecord
		if e = rows.Scan(&im.Kind, &im.Idx, &im.File, &im.Tokens); e != nil {
			break
		}
		imgs = append(imgs, im)
	}
	re := rows.Err()
	rows.Close()
	if e != nil || re != nil {
		c.JSON(503, gin.H{"error": "交付物暂不可用"})
		return
	}
	delivery, e := readDeliveryProgress(c, jid)
	if e != nil {
		readFailure(c, e, "不存在")
		return
	}
	c.JSON(200, gin.H{"id": jid, "code": code, "status": status, "stage": stage, "form": form, "images": imgs, "delivery": delivery})
}

func adminRetryJob(c *gin.Context) {
	jid := c.Param("id")
	_, err := retryFailedJob(c.Request.Context(), jid, "", "", true)
	if err != nil {
		if errors.Is(err, errRetryNotFound) || errors.Is(err, errRetryNotFailed) || errors.Is(err, errRetryNoTicket) || errors.Is(err, errGenerationUnknown) {
			c.JSON(409, gin.H{"error": err.Error()})
		} else {
			c.JSON(500, gin.H{"error": "系统错误"})
		}
		return
	}
	audit(adminName(c), "job_retry", "job", jid, "")
	c.JSON(200, gin.H{"ok": true})
}

func adminPublishCase(c *gin.Context) {
	var req struct {
		JobID   string `json:"job_id"`
		Title   string `json:"title"`
		Tagline string `json:"tagline"`
	}
	if c.BindJSON(&req) != nil || req.JobID == "" {
		c.JSON(400, gin.H{"error": "参数无效"})
		return
	}
	if req.Title == "" {
		req.Title = "未命名案例"
	}
	rights, err := jobEntitlements(c, pool, req.JobID)
	if err != nil {
		c.JSON(404, gin.H{"error": "任务不存在或权益不可读取"})
		return
	}
	list := []gin.H{}
	names := []string{"01 · 世界观封面", "02 · 符号生成逻辑", "03 · 视觉设计系统", "04 · 应用与体验"}
	for i := 1; i <= 4; i++ {
		kind := legacyBoardKind(i)
		if !rights[kind] {
			c.JSON(400, gin.H{"error": "该任务四图未全部解锁，无法上架"})
			return
		}
		var file string
		err = pool.QueryRow(c, "SELECT file FROM job_images WHERE job_id=$1 AND (kind=$2 OR (kind='board' AND idx=$3)) AND COALESCE(file,'')<>'' ORDER BY id DESC LIMIT 1", req.JobID, kind, i).Scan(&file)
		if err != nil {
			c.JSON(400, gin.H{"error": "该任务四图不完整，无法上架"})
			return
		}
		f, e := openArtifact(req.JobID, file)
		if e != nil {
			c.JSON(400, gin.H{"error": "案例原图不可用，请先修复交付"})
			return
		}
		f.Close()
		list = append(list, gin.H{"label": names[i-1], "file": webpify(file)})
	}
	data, _ := json.Marshal(list)
	if _, err = pool.Exec(c, "INSERT INTO cases(job_id,title,tagline,images) VALUES ($1,$2,$3,$4::jsonb)", req.JobID, req.Title, req.Tagline, string(data)); err != nil {
		c.JSON(503, gin.H{"error": "案例保存失败，请稍后重试"})
		return
	}
	audit(adminName(c), "case_publish", "case", req.JobID, "")
	c.JSON(201, gin.H{"ok": true})
}

func adminUpdateCase(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Title     *string `json:"title"`
		Tagline   *string `json:"tagline"`
		Published *bool   `json:"published"`
		Sort      *int    `json:"sort"`
	}
	if c.BindJSON(&req) != nil {
		c.JSON(400, gin.H{"error": "参数无效"})
		return
	}
	if req.Title != nil {
		pool.Exec(contextBG(), "UPDATE cases SET title=$2 WHERE id=$1", id, *req.Title)
	}
	if req.Tagline != nil {
		pool.Exec(contextBG(), "UPDATE cases SET tagline=$2 WHERE id=$1", id, *req.Tagline)
	}
	if req.Published != nil {
		pool.Exec(contextBG(), "UPDATE cases SET published=$2 WHERE id=$1", id, *req.Published)
	}
	if req.Sort != nil {
		pool.Exec(contextBG(), "UPDATE cases SET sort=$2 WHERE id=$1", id, *req.Sort)
	}
	audit(adminName(c), "case_update", "case", id, "")
	c.JSON(200, gin.H{"ok": true})
}

func adminDeleteCase(c *gin.Context) {
	id := c.Param("id")
	pool.Exec(contextBG(), "DELETE FROM cases WHERE id=$1", id)
	audit(adminName(c), "case_delete", "case", id, "")
	c.JSON(200, gin.H{"ok": true})
}

func adminUsage(c *gin.Context) {
	days := 30
	if d, err := strconv.Atoi(c.Query("days")); err == nil && d > 0 {
		days = d
	}
	rows, _ := pool.Query(contextBG(), fmt.Sprintf(
		`SELECT to_char(date_trunc('day', created_at), 'MM-DD') d, COUNT(*), COALESCE(SUM(tokens),0)
		 FROM usage_events WHERE created_at > now() - interval '%d days'
		 GROUP BY 1 ORDER BY 1`, days))
	defer rows.Close()
	type row struct {
		Day    string `json:"day"`
		Imgs   int    `json:"images"`
		Tokens int    `json:"tokens"`
	}
	out := []row{}
	for rows.Next() {
		var r row
		rows.Scan(&r.Day, &r.Imgs, &r.Tokens)
		out = append(out, r)
	}
	c.JSON(200, out)
}

func adminAudit(c *gin.Context) {
	rows, _ := pool.Query(contextBG(),
		`SELECT id, COALESCE(actor,''), action, object_type, COALESCE(object_id,''),
		        COALESCE(to_char(created_at,'YYYY-MM-DD HH24:MI:SS'),'')
		 FROM audit_events ORDER BY id DESC LIMIT 100`)
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id int
		var actor, action, objType, objID, created string
		if rows.Scan(&id, &actor, &action, &objType, &objID, &created) != nil {
			continue
		}
		out = append(out, gin.H{"id": id, "actor": actor, "action": action,
			"object_type": objType, "object_id": objID, "created_at": created})
	}
	c.JSON(200, out)
}

// ---------- 支付占位 ----------
func payWebhook(c *gin.Context) {
	// 渠道确定后实现验签与发码；先落库原始报文
	body, _ := c.GetRawData()
	pool.Exec(contextBG(),
		"INSERT INTO payments(provider, order_no, status, raw) VALUES ('pending',$1,'received',$2::jsonb) ON CONFLICT DO NOTHING",
		fmt.Sprintf("hook-%d", time.Now().UnixNano()), string(body))
	c.JSON(200, gin.H{"ok": true, "note": "支付渠道未接入，报文已暂存"})
}

func adminName(c *gin.Context) string { return c.GetString("adminName") }

// encodingQuality honors explicit exclusions before wildcard preferences.
func encodingQuality(header, encoding string) float64 {
	explicit, wildcard := -1.0, -1.0
	for _, item := range strings.Split(strings.ToLower(header), ",") {
		parts := strings.Split(item, ";")
		token := strings.TrimSpace(parts[0])
		q := 1.0
		for _, param := range parts[1:] {
			kv := strings.SplitN(strings.TrimSpace(param), "=", 2)
			if len(kv) == 2 && strings.TrimSpace(kv[0]) == "q" {
				v, e := strconv.ParseFloat(strings.TrimSpace(kv[1]), 64)
				if e != nil || !(v >= 0 && v <= 1) {
					q = 0
				} else {
					q = v
				}
			}
		}
		if token == encoding {
			explicit = q
		}
		if token == "*" {
			wildcard = q
		}
	}
	if explicit >= 0 {
		return explicit
	}
	if wildcard >= 0 {
		return wildcard
	}
	return 0
}

// Build-time encodings avoid per-request compression. Open the embedded file
// without ReadFile's full-body copy; ServeContent handles HEAD and byte ranges.
func servePrecompressedStatic(c *gin.Context, dist fs.FS, rel string) bool {
	ext := ""
	for _, candidate := range []string{".js", ".css", ".svg"} {
		if strings.HasSuffix(rel, candidate) {
			ext = candidate
			break
		}
	}
	if ext == "" {
		return false
	}
	c.Header("Vary", "Accept-Encoding")
	header := c.GetHeader("Accept-Encoding")
	encodings := []string{"zstd", "gzip"}
	if encodingQuality(header, "gzip") > encodingQuality(header, "zstd") {
		encodings = []string{"gzip", "zstd"}
	}
	for _, encoding := range encodings {
		if encodingQuality(header, encoding) <= 0 {
			continue
		}
		suffix := ".zst"
		if encoding == "gzip" {
			suffix = ".gz"
		}
		file, err := dist.Open(strings.TrimPrefix(rel, "/") + suffix)
		if err != nil {
			continue
		}
		reader, ok := file.(io.ReadSeeker)
		info, statErr := file.Stat()
		if !ok || statErr != nil {
			file.Close()
			continue
		}
		contentType := mime.TypeByExtension(ext)
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		c.Header("Content-Encoding", encoding)
		c.Header("Content-Type", contentType)
		http.ServeContent(c.Writer, c.Request, rel, info.ModTime(), reader)
		file.Close()
		return true
	}
	return false
}

// serveIndex 直接输出 index.html 字节（http.FileServer 会把 /index.html 重定向到 ./）
func serveIndex(c *gin.Context) {
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	data, err := webFS.ReadFile("webdist/index.html")
	if err != nil {
		c.String(500, "frontend not embedded")
		return
	}
	c.Data(200, "text/html; charset=utf-8", data)
}

func contextBG() context.Context { return context.Background() }
