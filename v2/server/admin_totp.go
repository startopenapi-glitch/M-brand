package app

// Admin TOTP: password is step one; a short-lived signed-in-memory challenge
// gates the second step. The challenge never becomes a session cookie.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

const admin2FAEnv = "ZEROBRAND_ADMIN_2FA"

func admin2FAEnabled() bool { return osGetenv(admin2FAEnv) == "1" }

type adminChallenge struct {
	AdminID  int
	Username string
	Purpose  string // setup | verify
	Secret   string
	IP       string
	Expires  time.Time
	Attempts int
}

var (
	adminChallengeMu sync.Mutex
	adminChallenges  = map[string]*adminChallenge{}
)

func osGetenv(k string) string { return os.Getenv(k) }

func newTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}

func validTOTPSecret(s string) bool {
	if len(s) != 32 || strings.ToUpper(s) != s {
		return false
	}
	_, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
	return err == nil
}

func totpCode(secret string, at time.Time) string {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return ""
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(at.Unix()/30))
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 15
	n := (uint32(sum[off])&0x7f)<<24 | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])
	return fmt.Sprintf("%06d", n%1000000)
}

func verifyTOTP(secret, code string, at time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false
	}
	for _, delta := range []int64{0, -1, 1} {
		if hmac.Equal([]byte(totpCode(secret, at.Add(time.Duration(delta)*30*time.Second))), []byte(code)) {
			return true
		}
	}
	return false
}

func totpURL(secret, username string) string {
	label := url.PathEscape("M-brand:" + username)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", "M-brand")
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	return "otpauth://totp/" + label + "?" + q.Encode()
}

func issueAdminChallenge(adminID int, username, purpose, secret, ip string) string {
	token := randHex(24)
	adminChallengeMu.Lock()
	defer adminChallengeMu.Unlock()
	now := time.Now()
	for k, v := range adminChallenges {
		if v.Expires.Before(now) || v.IP != ip {
			delete(adminChallenges, k)
		}
	}
	adminChallenges[token] = &adminChallenge{AdminID: adminID, Username: username, Purpose: purpose, Secret: secret, IP: ip, Expires: now.Add(5 * time.Minute)}
	return token
}

func takeAdminChallenge(token, ip string) *adminChallenge {
	adminChallengeMu.Lock()
	defer adminChallengeMu.Unlock()
	c := adminChallenges[token]
	if c == nil || c.Expires.Before(time.Now()) || c.IP != ip {
		return nil
	}
	delete(adminChallenges, token)
	return c
}

func adminLoginPasswordOK(c *gin.Context) (int, string, string, bool) {
	var req struct{ Username, Password string }
	if c.BindJSON(&req) != nil {
		c.JSON(400, gin.H{"error": "参数无效"})
		return 0, "", "", false
	}
	var id int
	var hash string
	if err := pool.QueryRow(c, "SELECT id,password_hash FROM admins WHERE username=$1", req.Username).Scan(&id, &hash); err != nil || bcryptCompare(hash, req.Password) != nil {
		audit(req.Username, "admin_login_fail", "admin", "", "")
		c.JSON(401, gin.H{"error": "账号或密码不正确"})
		return 0, "", "", false
	}
	return id, req.Username, req.Password, true
}

func adminTOTPSetup(c *gin.Context) {
	var in struct{ Token, Code, Secret string }
	if c.BindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "参数无效"})
		return
	}
	ch := takeAdminChallenge(in.Token, c.ClientIP())
	if ch == nil || ch.Purpose != "setup" {
		c.JSON(400, gin.H{"error": "设置已过期，请重新登录"})
		return
	}
	secret := strings.ToUpper(strings.TrimSpace(ch.Secret))
	if in.Secret != "" {
		secret = strings.ToUpper(strings.TrimSpace(in.Secret))
	}
	if !validTOTPSecret(secret) || !verifyTOTP(secret, in.Code, time.Now()) {
		c.JSON(401, gin.H{"error": "动态验证码不正确"})
		return
	}
	var version int64
	if err := pool.QueryRow(c, `UPDATE admins SET totp_secret=$1,totp_enabled=true,session_version=session_version+1
		WHERE id=$2 RETURNING session_version`, secret, ch.AdminID).Scan(&version); err != nil {
		c.JSON(500, gin.H{"error": "系统错误"})
		return
	}
	setAdminCookie(c, ch.AdminID, ch.Username, version)
	audit(ch.Username, "admin_totp_enabled", "admin", strconv.Itoa(ch.AdminID), "")
	c.JSON(200, gin.H{"ok": true})
}

func adminTOTPVerify(c *gin.Context) {
	var in struct{ Challenge, Code string }
	if c.BindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "参数无效"})
		return
	}
	ch := takeAdminChallenge(in.Challenge, c.ClientIP())
	if ch == nil || ch.Purpose != "verify" {
		c.JSON(401, gin.H{"error": "登录验证已过期，请重新输入密码"})
		return
	}
	var secret string
	var version int64
	var enabled bool
	if err := pool.QueryRow(c, "SELECT totp_secret,totp_enabled,session_version FROM admins WHERE id=$1", ch.AdminID).Scan(&secret, &enabled, &version); err != nil || !enabled || !verifyTOTP(secret, in.Code, time.Now()) {
		c.JSON(401, gin.H{"error": "动态验证码不正确"})
		return
	}
	setAdminCookie(c, ch.AdminID, ch.Username, version)
	audit(ch.Username, "admin_login_totp", "admin", strconv.Itoa(ch.AdminID), "")
	c.JSON(200, gin.H{"ok": true})
}

// Kept here so the login flow has one password comparison boundary.
func bcryptCompare(hash, pass string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pass))
}
