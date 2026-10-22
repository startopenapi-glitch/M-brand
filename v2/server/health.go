package app

// health.go — 生图通道健康：熔断（额度告警/连败 → 暂停收款与新建任务）+ 备用通道探测
//
// 第一性原理：生成能力＝产品本身。通道不可用时"熔断优先于收钱"——
// 已付费未生成的部分自动返还；新任务与收款在通道恢复前直接拒绝。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	breakerWindow   = 15 * time.Minute // 熔断自愈窗口：窗口内无成功生成则自动恢复（下次失败会立刻再熔断）
	failStreakLimit = 3                // 普通失败连败阈值（额度告警则立即熔断）
)

type imgHealthState struct {
	mu         sync.Mutex
	trippedAt  time.Time
	tripReason string
	lastOK     time.Time
	lastErr    string
	failStreak int
}

var imgHealth = &imgHealthState{}

// noteImgFailure：生图失败上报（额度告警立即熔断；其他错误连败 3 次熔断）
func noteImgFailure(errText string) {
	imgHealth.mu.Lock()
	imgHealth.lastErr = truncate(errText, 200)
	imgHealth.failStreak++
	if strings.Contains(errText, "QUOTA_EXHAUSTED") || imgHealth.failStreak >= failStreakLimit {
		imgHealth.trippedAt = time.Now()
		imgHealth.tripReason = truncate(errText, 200)
	}
	imgHealth.mu.Unlock()
	persistImgHealth()
}

// noteImgSuccess：成功生成 → 解除熔断
func noteImgSuccess() {
	imgHealth.mu.Lock()
	imgHealth.lastOK = time.Now()
	imgHealth.failStreak = 0
	imgHealth.trippedAt = time.Time{}
	imgHealth.tripReason = ""
	imgHealth.mu.Unlock()
	persistImgHealth()
}

// salesPaused：是否处于熔断（生成不可用 → 停止收款与新建任务）
func salesPaused() (bool, string) {
	return salesPausedQuery(contextBG(), pool)
}

// Reuse an existing transaction instead of waiting for a second pool connection.
func salesPausedQuery(ctx context.Context, q trialQuerier) (bool, string) {
	refreshImgHealthQuery(ctx, q)
	imgHealth.mu.Lock()
	defer imgHealth.mu.Unlock()
	if imgHealth.trippedAt.IsZero() {
		return false, ""
	}
	if time.Since(imgHealth.trippedAt) > breakerWindow {
		imgHealth.trippedAt = time.Time{} // 窗口自愈
		return false, ""
	}
	return true, "生图通道当前不可用，已暂停收款与新建任务（你的额度/试用次数不受影响）"
}

type imgHealthPersist struct {
	TrippedAt  string `json:"tripped_at"`
	Reason     string `json:"reason"`
	LastOK     string `json:"last_ok"`
	FailStreak int    `json:"fail_streak"`
}

func persistImgHealth() {
	if pool == nil {
		return
	}
	imgHealth.mu.Lock()
	p := imgHealthPersist{FailStreak: imgHealth.failStreak}
	if !imgHealth.trippedAt.IsZero() {
		p.TrippedAt = imgHealth.trippedAt.Format(time.RFC3339Nano)
		p.Reason = imgHealth.tripReason
	}
	if !imgHealth.lastOK.IsZero() {
		p.LastOK = imgHealth.lastOK.Format(time.RFC3339Nano)
	}
	imgHealth.mu.Unlock()
	raw, _ := json.Marshal(p)
	_, _ = pool.Exec(contextBG(), `INSERT INTO runtime_health(key,value,updated_at) VALUES('img',$1::jsonb,now())
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`, string(raw))
}

var lastHealthRefresh time.Time

func refreshImgHealth() {
	refreshImgHealthQuery(contextBG(), pool)
}

func refreshImgHealthQuery(ctx context.Context, q trialQuerier) {
	if pool == nil || q == nil {
		return
	}
	now := time.Now()
	imgHealth.mu.Lock()
	fresh := now.Sub(lastHealthRefresh) < 2*time.Second
	imgHealth.mu.Unlock()
	if fresh {
		return
	}
	var raw []byte
	if q.QueryRow(ctx, `SELECT value FROM runtime_health WHERE key='img'`).Scan(&raw) != nil {
		return
	}
	var p imgHealthPersist
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	tripped, _ := time.Parse(time.RFC3339Nano, p.TrippedAt)
	ok, _ := time.Parse(time.RFC3339Nano, p.LastOK)
	imgHealth.mu.Lock()
	imgHealth.trippedAt, imgHealth.tripReason, imgHealth.lastOK, imgHealth.failStreak = tripped, p.Reason, ok, p.FailStreak
	lastHealthRefresh = now
	imgHealth.mu.Unlock()
}

func imgHealthSnapshot() gin.H {
	imgHealth.mu.Lock()
	defer imgHealth.mu.Unlock()
	h := gin.H{
		"tripped":             !imgHealth.trippedAt.IsZero() && time.Since(imgHealth.trippedAt) <= breakerWindow,
		"fail_streak":         imgHealth.failStreak,
		"last_err":            imgHealth.lastErr,
		"fallback_configured": fallbackConfigured(),
	}
	if imgHealth.lastOK.Unix() > 0 {
		h["last_ok"] = imgHealth.lastOK.Format("01-02 15:04:05")
	}
	if !imgHealth.trippedAt.IsZero() {
		h["tripped_at"] = imgHealth.trippedAt.Format("01-02 15:04")
		h["trip_reason"] = imgHealth.tripReason
	}
	return h
}

// probeImgChannel：主动探测生图通道（只读、免费）：GET /models
// 只确认接口可达，不能证明生图额度恢复，因此不解除生成失败熔断
func probeImgChannel() (bool, string) {
	if !imgConfigured() {
		return false, "生图未配置（ZEROBRAND_IMG_BASE_URL / API_KEY）"
	}
	ok, msg := probeImgConfig(imgConf())
	if ok {
		return true, "主通道接口可达（生图能力以实际生成结果为准）"
	}
	if fb := imgFallbackConf(); fb.base != "" && fb.key != "" {
		ok2, msg2 := probeImgConfig(fb)
		if ok2 {
			return true, "备用通道正常（主通道异常：" + truncate(msg, 80) + "）"
		}
		noteImgFailure(msg + " / " + msg2)
		return false, "主/备通道均异常：" + truncate(msg, 60) + " ｜ " + truncate(msg2, 60)
	}
	noteImgFailure(msg)
	return false, truncate(msg, 160)
}

func probeImgConfig(c imgConfig) (bool, string) {
	req, err := http.NewRequest("GET", c.base+"/models", nil)
	if err != nil {
		return false, "探测请求构造失败: " + err.Error()
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	resp, err := (&http.Client{Timeout: 25 * time.Second}).Do(req)
	if err != nil {
		return false, "探测失败: " + err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == 200 {
		return true, "ok"
	}
	return false, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 120))
}

// adminImgProbe：管理端"检测生图通道"按钮（接口可达不等于生成恢复）
func adminImgProbe(c *gin.Context) {
	ok, msg := probeImgChannel()
	paused, reason := salesPaused()
	c.JSON(200, gin.H{"ok": ok, "message": msg, "sales_paused": paused,
		"paused_reason": reason, "health": imgHealthSnapshot()})
}
