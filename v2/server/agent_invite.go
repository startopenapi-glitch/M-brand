package app

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/startopenapi-glitch/M-brand/v2/server/internal/platform"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

func inviteHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func agentInviteCreate(c *gin.Context) {
	var in struct { Email string; CommissionPct *int }
	if c.BindJSON(&in) != nil || strings.TrimSpace(in.Email) == "" {
		c.JSON(400, gin.H{"error": "请填写代理邮箱"})
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if !strings.Contains(email, "@") || len(email) > 320 {
		c.JSON(400, gin.H{"error": "邮箱格式不正确"})
		return
	}
	name := strings.Split(email, "@")[0]
	if name == "" {
		name = "代理"
	}
	commissionPct := 40
	if in.CommissionPct != nil {
		if !validAgentCommission(*in.CommissionPct) { c.JSON(400, gin.H{"error":"佣金比例需为 1–90 之间的整数"}); return }
		commissionPct = *in.CommissionPct
	}
	var exists int
	_ = pool.QueryRow(c, `SELECT COUNT(*) FROM agents WHERE lower(email)=$1`, email).Scan(&exists)
	if exists > 0 {
		c.JSON(409, gin.H{"error": "该邮箱已经是代理"})
		return
	}
	token := randHex(32)
	id := "INV" + strings.ToUpper(newID(10))
	expires := platform.Now().Add(7 * 24 * time.Hour)
	if _, err := pool.Exec(c, `INSERT INTO agent_invitations(id,email,name,commission_pct,token_hash,invited_by,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, email, name, commissionPct, inviteHash(token), adminName(c), expires); err != nil {
		c.JSON(409, gin.H{"error": "邀请创建失败（邮箱可能已存在）"})
		return
	}
	link := strings.TrimRight(platform.LoadConfig().PublicOrigin, "/")
	link += "/agent/invite/" + token
	sent, sendErr := sendAgentInviteEmail(email, name, link, commissionPct)
	if sendErr != nil {
		c.JSON(201, gin.H{"ok": true, "id": id, "commission_pct": commissionPct, "email_sent": false, "invite_url": link, "warning": "邮件服务未配置或发送失败，请复制邀请链接发送给代理"})
		return
	}
	if !sent {
		c.JSON(201, gin.H{"ok": true, "id": id, "commission_pct": commissionPct, "email_sent": false, "invite_url": link})
		return
	}
	c.JSON(201, gin.H{"ok": true, "id": id, "commission_pct": commissionPct, "email_sent": true})
	_ = sendErr
}

func agentInviteInfo(c *gin.Context) {
	var name, email string
	var pct int
	var expires time.Time
	err := pool.QueryRow(c, `SELECT name,email,commission_pct,expires_at FROM agent_invitations WHERE token_hash=$1 AND accepted_at IS NULL`, inviteHash(c.Param("token"))).Scan(&name, &email, &pct, &expires)
	if err != nil || expires.Before(platform.Now()) {
		c.JSON(404, gin.H{"error": "邀请不存在或已过期"})
		return
	}
	parts := strings.Split(email, "@")
	masked := email
	if len(parts) == 2 && len(parts[0]) > 1 {
		masked = parts[0][:1] + "***@" + parts[1]
	}
	c.JSON(200, gin.H{"name": name, "email": masked, "commission_pct": pct})
}

func agentInviteAccept(c *gin.Context) {
	var in struct{ Password string }
	if c.BindJSON(&in) != nil || len(in.Password) < 8 {
		c.JSON(400, gin.H{"error": "密码至少 8 位"})
		return
	}
	if len(in.Password) > 72 {
		c.JSON(400, gin.H{"error": "密码过长，请缩短后重试"})
		return
	}
	tx, err := pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(503, gin.H{"error": "系统错误"})
		return
	}
	defer tx.Rollback(c.Request.Context())
	var id, email, name string
	var pct int
	var expires time.Time
	err = tx.QueryRow(c, `SELECT id,email,name,commission_pct,expires_at FROM agent_invitations WHERE token_hash=$1 AND accepted_at IS NULL FOR UPDATE`, inviteHash(c.Param("token"))).Scan(&id, &email, &name, &pct, &expires)
	if err != nil || expires.Before(time.Now()) {
		c.JSON(404, gin.H{"error": "邀请不存在或已过期"})
		return
	}
	hash, hashErr := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if hashErr != nil {
		c.JSON(503, gin.H{"error": "密码处理失败，请稍后重试"})
		return
	}
	agentID := "AG" + randHex(6)
	code := "AG" + strings.ToUpper(randHex(4))
	if _, err = tx.Exec(c, `INSERT INTO agents(id,name,email,password_hash,code,commission_pct) VALUES($1,$2,$3,$4,$5,$6)`, agentID, name, email, string(hash), code, pct); err != nil {
		c.JSON(409, gin.H{"error": "该邮箱已经是代理"})
		return
	}
	if _, err = tx.Exec(c, `UPDATE agent_invitations SET accepted_at=now() WHERE id=$1`, id); err != nil {
		c.JSON(503, gin.H{"error": "开通失败"})
		return
	}
	if err = tx.Commit(c.Request.Context()); err != nil {
		c.JSON(503, gin.H{"error": "开通结果暂不确定，请重新打开邀请链接"})
		return
	}
	setAgentLoginCookie(c, agentID, 0)
	audit("agent:"+agentID, "invite_accepted", "agent", agentID, `{"email":"`+email+`"}`)
	c.JSON(201, gin.H{"ok": true, "id": agentID, "code": code})
}

func setAgentLoginCookie(c *gin.Context, id string, version int64) {
	p := gatePayload{Admin: 0, Code: id, Expiry: time.Now().Add(7 * 24 * time.Hour).Unix(), CSRF: randHex(8), Purpose: "agent", SessionVersion: version}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(agentCookie, makeCookieValue(p), 7*24*3600, "/", "", true, true)
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// mailStatus reports presence only; never return addresses, usernames or keys.
func mailStatus() map[string]bool {
	host := firstEnv("ZEROBRAND_SMTP_HOST", "RESEND_SMTP_HOST", "SMTP_HOST")
	from := firstEnv("ZEROBRAND_SMTP_FROM", "RESEND_FROM", "SMTP_FROM")
	key := firstEnv("ZEROBRAND_RESEND_API_KEY", "RESEND_API_KEY")
	user := firstEnv("ZEROBRAND_SMTP_USER", "RESEND_SMTP_USER", "SMTP_USER")
	pass := firstEnv("ZEROBRAND_SMTP_PASS", "RESEND_SMTP_PASS", "SMTP_PASS")
	return map[string]bool{"smtp": host != "" && from != "", "resend_api": key != "" && from != "", "user": user != "", "password": pass != "" || key != ""}
}

func sendAgentInviteEmail(to, name, link string, commissionPct int) (bool, error) {
	from := firstEnv("ZEROBRAND_SMTP_FROM", "RESEND_FROM", "SMTP_FROM")
	if from == "" {
		return false, errors.New("邮件 From 未配置")
	}
	apiKey := firstEnv("ZEROBRAND_RESEND_API_KEY", "RESEND_API_KEY")
	if apiKey != "" {
		return sendResendAPI(apiKey, from, to, name, link, commissionPct)
	}
	host := firstEnv("ZEROBRAND_SMTP_HOST", "RESEND_SMTP_HOST", "SMTP_HOST")
	port := firstEnv("ZEROBRAND_SMTP_PORT", "RESEND_SMTP_PORT", "SMTP_PORT")
	if host == "" || port == "" {
		return false, errors.New("SMTP 未配置")
	}
	user := firstEnv("ZEROBRAND_SMTP_USER", "RESEND_SMTP_USER", "SMTP_USER")
	pass := firstEnv("ZEROBRAND_SMTP_PASS", "RESEND_SMTP_PASS", "SMTP_PASS")
	if user == "" {
		user = "resend"
	}
	subject := "M-brand 代理邀请"
	body := fmt.Sprintf("你好 %s，\n\n你被邀请开通 M-brand 一级代理工作台。\n佣金比例：%d%%，开通后可在工作台查看当前比例。\n点击下面的链接设置密码并直接开通：\n%s\n\n链接 7 天内有效，仅可使用一次。\n", name, commissionPct, link)
	msg := []byte("From: " + from + "\r\nTo: " + to + "\r\nSubject: " + subject + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + body)
	// Resend SMTP uses 465 (implicit TLS); standard providers often use 587.
	if port == "465" || strings.EqualFold(firstEnv("ZEROBRAND_SMTP_TLS"), "ssl") {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", net.JoinHostPort(host, port), &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if err != nil {
			return false, err
		}
		client, err := smtp.NewClient(conn, host)
		if err != nil {
			conn.Close()
			return false, err
		}
		defer client.Close()
		if user != "" && pass != "" {
			if err = client.Auth(smtp.PlainAuth("", user, pass, host)); err != nil {
				return false, err
			}
		}
		if err = client.Mail(from); err != nil {
			return false, err
		}
		if err = client.Rcpt(to); err != nil {
			return false, err
		}
		w, err := client.Data()
		if err != nil {
			return false, err
		}
		if _, err = w.Write(msg); err != nil {
			return false, err
		}
		if err = w.Close(); err != nil {
			return false, err
		}
		if err = client.Quit(); err != nil {
			return false, err
		}
		return true, nil
	}
	auth := smtp.PlainAuth("", user, pass, host)
	if err := smtp.SendMail(net.JoinHostPort(host, port), auth, from, []string{to}, msg); err != nil {
		return false, err
	}
	return true, nil
}

func sendResendAPI(key, from, to, name, link string, commissionPct int) (bool, error) {
	subject := "M-brand 代理邀请"
	body := fmt.Sprintf("你好 %s，\n\n你被邀请开通 M-brand 一级代理工作台。\n佣金比例：%d%%，开通后可在工作台查看当前比例。\n点击下面的链接设置密码并直接开通：\n%s\n\n链接 7 天内有效，仅可使用一次。\n", name, commissionPct, link)
	payload, _ := json.Marshal(map[string]any{"from": from, "to": []string{to}, "subject": subject, "text": body})
	req, err := http.NewRequest("POST", "https://api.resend.com/emails", bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("resend HTTP %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	return true, nil
}

// Keep pgx and strconv imports used by adjacent invitation helpers.
var _ = pgx.ErrNoRows
var _ = strconv.Itoa

// Keep pgx imported in this file's build when invitation queries change.
var _ = pgx.ErrNoRows
