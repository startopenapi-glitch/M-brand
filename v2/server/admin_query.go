package app

import (
	"github.com/startopenapi-glitch/M-brand/v2/server/internal/adminorders"
	"github.com/gin-gonic/gin"
)

func adminOrderPage(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	f, e := adminorders.Parse(c.Request.URL.Query())
	if e != nil {
		c.JSON(400, gin.H{"error": "筛选参数无效"})
		return
	}
	out, e := (adminorders.Service{DB: pool}).Query(c.Request.Context(), f)
	if e != nil {
		c.JSON(503, gin.H{"error": "订单暂不可用，请稍后重试"})
		return
	}
	c.JSON(200, out)
}
func adminSession(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.JSON(200, gin.H{"name": adminName(c), "capabilities": []string{"overview", "orders", "jobs", "support", "codes", "cases", "agents", "usage", "audit"}})
}
