package app

import (
	"github.com/gin-gonic/gin"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// 浏览器写请求校验真实Origin；服务端通知/CLI无Origin仍由现有签名和会话验证。
func requestSecurity() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("X-Frame-Options", "DENY")
		if strings.HasPrefix(c.Request.URL.Path, basePath+"/api/") {
			c.Header("Cache-Control", "private, no-store")
			if c.Request.Method != "GET" && c.Request.Method != "HEAD" && c.Request.Method != "OPTIONS" {
				origin := c.GetHeader("Origin")
				if origin != "" {
					u, err := url.Parse(origin)
					scheme := requestOriginScheme(c.Request)
					if err != nil || (scheme == "" || u.Scheme != scheme) || u.User != nil || !strings.EqualFold(u.Host, c.Request.Host) || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
						c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "请求来源无效，请从本站重试"})
						return
					}
				}
				if c.Request.ContentLength > 256<<10 {
					c.AbortWithStatusJSON(413, gin.H{"error": "请求数据过大"})
					return
				}
				c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
			}
		}
		c.Next()
	}
}

// HTTPS ends at the loopback Caddy proxy; never trust remote forwarding headers.
func requestOriginScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && net.ParseIP(host).IsLoopback() {
		values := r.Header.Values("X-Forwarded-Proto")
		if len(values) > 0 {
			if len(values) == 1 && (values[0] == "https" || values[0] == "http") {
				return values[0]
			}
			return ""
		}
	}
	return "http"
}
