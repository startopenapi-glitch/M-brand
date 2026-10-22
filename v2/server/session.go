package app

// session.go — HMAC 签名 Cookie（用户邀请码门 + 管理员会话）+ CSRF

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
 "errors"
 "github.com/jackc/pgx/v5"
	"net/http"
	"os"
	"time"

	"github.com/startopenapi-glitch/M-brand/v2/server/internal/platform"
	"github.com/gin-gonic/gin"
)

var sessionSecret []byte

func loadSecret() {
	p := filepathJoin("secret.key")
	if b, err := os.ReadFile(p); err == nil {
		sessionSecret = b
		return
	}
	b := make([]byte, 32)
	rand.Read(b)
	sessionSecret = b
	_ = os.WriteFile(p, b, 0o600)
}

func filepathJoin(name string) string { return dirOf(os.Args[0]) + "/" + name }

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}

func sign(payload string) string {
	m := hmac.New(sha256.New, sessionSecret)
	m.Write([]byte(payload))
	return hex.EncodeToString(m.Sum(nil))
}

type gatePayload struct {
	SessionVersion int64  `json:"v,omitempty"`
	Purpose        string `json:"p,omitempty"`
	Code           string `json:"c"`
	Admin          int    `json:"a,omitempty"`
	Expiry         int64  `json:"e"`
	CSRF           string `json:"t"`
}

func makeCookieValue(p gatePayload) string {
	b, _ := json.Marshal(p)
	payload := hex.EncodeToString(b)
	return payload + "." + sign(payload)
}

func readCookie(c *gin.Context, name string) *gatePayload {
	v, err := c.Cookie(name)
	if err != nil || v == "" {
		return nil
	}
	var payload, sig string
	for i := len(v) - 1; i >= 0; i-- {
		if v[i] == '.' {
			payload, sig = v[:i], v[i+1:]
			break
		}
	}
	if payload == "" || !hmac.Equal([]byte(sig), []byte(sign(payload))) {
		return nil
	}
	raw, err := hex.DecodeString(payload)
	if err != nil {
		return nil
	}
	var p gatePayload
	if json.Unmarshal(raw, &p) != nil || p.Expiry < time.Now().Unix() {
		return nil
	}
	return &p
}

const gateCookie = "zb_gate_v2"
const adminCookie = "zb_admin_v2"

func setGateCookie(c *gin.Context, code string) {
	p := gatePayload{Code: code, Expiry: time.Now().Add(14 * 24 * time.Hour).Unix(),
		CSRF: randHex(8)}
	c.SetCookie(gateCookie, makeCookieValue(p), 14*24*3600, "/", "", true, true) // Path=/(根空间为规范 URL；/0brand 为兼容空间)
}

func setAdminCookie(c *gin.Context, adminID int, username string, version ...int64) {
	v := int64(0)
	if len(version) > 0 {
		v = version[0]
	}
	p := gatePayload{Admin: adminID, Code: username, SessionVersion: v,
		Expiry: time.Now().Add(12 * time.Hour).Unix(), CSRF: randHex(8)}
	c.SetCookie(adminCookie, makeCookieValue(p), 12*3600, "/", "", true, true) // Path=/(根空间为规范 URL；/0brand 为兼容空间)
}

func randHex(n int) string { return platform.RandHex(n) }

const basePath = "/0brand"

func gateOK(c *gin.Context) *gatePayload {
	p := readCookie(c, gateCookie)
	if p == nil || p.Admin != 0 || p.Code == "" {
		return nil
	}
	vid := visitorID(c)
	if vid == "" {
		return nil
	}
	var one int
	if pool.QueryRow(c, "SELECT 1 FROM invite_codes WHERE code=$1 AND bound_visitor=$2 AND revoked=false", p.Code, vid).Scan(&one) != nil {
		return nil
	}
	return p
}

// adminIdentity distinguishes an invalid session from an unavailable identity store.
func adminIdentity(c *gin.Context) (*gatePayload,error) {
 p:=readCookie(c,adminCookie)
 if p==nil||p.Admin<=0{return nil,nil}
 if admin2FAEnabled(){
  var version int64;var enabled bool
  err:=pool.QueryRow(c.Request.Context(),"SELECT session_version,totp_enabled FROM admins WHERE id=$1",p.Admin).Scan(&version,&enabled)
  if errors.Is(err,pgx.ErrNoRows){return nil,nil};if err!=nil{return nil,err}
  if !enabled||version!=p.SessionVersion{return nil,nil}
 }
 return p,nil
}
func adminOK(c *gin.Context) *gatePayload {p,_:=adminIdentity(c);return p}

func requireGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		if gateOK(c) == nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "需要邀请码"})
			return
		}
		c.Next()
	}
}

func requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		p,err := adminIdentity(c)
 if err!=nil{c.AbortWithStatusJSON(http.StatusServiceUnavailable,gin.H{"error":"身份服务暂不可用，请稍后重试"});return}
		if p == nil || p.Admin == 0 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "未登录管理员"})
			return
		}
		c.Set("adminName", p.Code)
		c.Next()
	}
}
