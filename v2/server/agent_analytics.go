package app

import (
	"time"

	"github.com/gin-gonic/gin"
)

func agentAnalytics(c *gin.Context) {
	p := agentOK(c)
	if p == nil {
		c.JSON(401, gin.H{"error": "请先登录"})
		return
	}
	days := 30
	if c.Query("days") == "7" {
		days = 7
	}
	since := time.Now().AddDate(0, 0, -days)
	rows, err := pool.Query(c, `SELECT event,COUNT(*) FROM analytics_events WHERE agent_code=$1 AND created_at>=$2 GROUP BY event`, p.Code, since)
	if err != nil {
		c.JSON(503, gin.H{"error": "分析数据暂不可用"})
		return
	}
	defer rows.Close()
	counts := map[string]int64{}
	for rows.Next() {
		var e string
		var n int64
		if rows.Scan(&e, &n) == nil {
			counts[e] = n
		}
	}
	var visitors int64
	_ = pool.QueryRow(c, `SELECT COUNT(DISTINCT visitor_hash) FROM analytics_events WHERE agent_code=$1 AND created_at>=$2 AND visitor_hash<>''`, p.Code, since).Scan(&visitors)
	c.JSON(200, gin.H{"days": days, "visitors": visitors, "events": counts})
}
