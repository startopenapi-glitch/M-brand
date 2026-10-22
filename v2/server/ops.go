package app

// Backend operations: support/after-sales cases, privacy-preserving funnel
// events, and agent commission reconciliation.

import (
	"github.com/startopenapi-glitch/M-brand/v2/server/internal/analytics"
	domainanalytics "github.com/startopenapi-glitch/M-brand/v2/server/internal/domain/analytics"
	supportsvc "github.com/startopenapi-glitch/M-brand/v2/server/internal/support"
	"errors"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
)

func analyticsVisitor(c *gin.Context) string { return domainanalytics.VisitorHash(visitorID(c)) }

func recordAnalytics(c *gin.Context, event, jobID string) {
	v := analyticsVisitor(c)
	if v == "" {
		return
	}
	path := c.FullPath()
	if path == "" {
		path = c.Request.URL.Path
	}
	agentCode := ""
	if code, _, _, err := attribution(c); err == nil {
		agentCode = code
	}
	_ = (analytics.Service{DB: pool}).Record(contextBG(), v, event, jobID, path, agentCode)
}

type supportInput struct {
	RequestKey  string `json:"request_key"`
	JobID       string `json:"job_id"`
	OrderID     string `json:"order_id"`
	Category    string `json:"category"`
	Description string `json:"description"`
}
type supportAdminInput struct {
 Version *int64 `json:"version"`
	Status     string `json:"status"`
	Resolution string `json:"resolution"`
	Note       string `json:"note"`
}

func supportCreate(c *gin.Context) {
	vid := visitorID(c)
	if vid == "" {
		c.JSON(401, gin.H{"error": "请先建立会话"})
		return
	}
	var in supportInput
	if c.BindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "参数无效"})
		return
	}
	row, inserted, err := (supportsvc.Service{DB: pool}).Create(c, supportsvc.CreateInput{RequestKey: in.RequestKey, Visitor: vid, JobID: in.JobID, OrderID: in.OrderID, Category: in.Category, Description: in.Description})
	switch {
	case errors.Is(err, supportsvc.ErrInvalid):
		c.JSON(400, gin.H{"error": "请填写问题和有效请求编号"})
	case errors.Is(err, supportsvc.ErrNotFound):
		c.JSON(404, gin.H{"error": "提案或订单不存在"})
	case errors.Is(err, supportsvc.ErrConflict):
		c.JSON(409, gin.H{"error": "请求编号已被使用"})
	case err != nil:
		c.JSON(500, gin.H{"error": "系统错误"})
	default:
		if inserted {
			recordAnalytics(c, "support_created", in.JobID)
		}
		c.JSON(200, gin.H{"id": row.ID, "status": row.Status})
	}
}
func supportList(c *gin.Context) {
	vid := visitorID(c)
	if vid == "" {
		c.JSON(401, gin.H{"error": "请先建立会话"})
		return
	}
	rows, err := (supportsvc.Service{DB: pool}).List(c, vid)
	if err != nil {
		c.JSON(503, gin.H{"error": "售后记录暂不可用"})
		return
	}
	c.JSON(200, rows)
}
func adminSupportList(c *gin.Context) {
	rows, err := (supportsvc.Service{DB: pool}).AdminList(c, c.Query("order_id"))
	if err != nil {
		c.JSON(503, gin.H{"error": "售后记录暂不可用"})
		return
	}
	c.JSON(200, rows)
}
func adminSupportReply(c *gin.Context) {
	var in supportAdminInput
	if c.BindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "参数无效"})
		return
	}
	row, err := (supportsvc.Service{DB: pool}).Review(c, supportsvc.ReviewInput{ID: c.Param("id"), Status: in.Status, Resolution: in.Resolution, Actor: adminName(c), Note: in.Note, Version: in.Version})
	switch {
	case errors.Is(err, supportsvc.ErrConflict):
 c.JSON(409,gin.H{"error":"工单已被其他操作更新，请刷新后核对"})
 case errors.Is(err, supportsvc.ErrInvalid):
		c.JSON(400, gin.H{"error": "状态无效"})
	case errors.Is(err, supportsvc.ErrNotFound):
		c.JSON(404, gin.H{"error": "工单不存在"})
	case err != nil:
		c.JSON(500, gin.H{"error": "系统错误"})
	default:
		c.JSON(200, gin.H{"ok": true, "id": row.ID, "status": row.Status, "version": row.Version})
	}
}
func adminAnalytics(c *gin.Context) {
	days := 30
	if q := c.Query("days"); q == "7" || q == "30" || q == "90" {
		days = int(q[0]-'0')*10 + int(q[1]-'0')
	}
	since := time.Now().AddDate(0, 0, -days)
	rows, err := pool.Query(c, `SELECT event,COUNT(*) FROM analytics_events WHERE created_at>=$1 GROUP BY event ORDER BY event`, since)
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
	_ = pool.QueryRow(c, `SELECT COUNT(DISTINCT visitor_hash) FROM analytics_events WHERE created_at>=$1 AND visitor_hash<>''`, since).Scan(&visitors)
	c.JSON(200, gin.H{"days": days, "events": counts, "visitors": visitors})
}

func adminAgentReconcile(c *gin.Context) {
	// Detect the three accounting failures that matter: paid order with no
	// commission, duplicated commission, and stored agent totals that drift.
	type anomaly struct{ Kind, OrderID, AgentID, Detail string }
	out := []anomaly{}
	rows, err := pool.Query(c, `SELECT o.id,COALESCE(o.agent_id,'') FROM orders o
		LEFT JOIN agent_commissions ac ON ac.order_id=o.id
		WHERE o.status='paid' AND o.mock=false AND COALESCE(o.agent_id,'')<>'' AND ac.id IS NULL LIMIT 200`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var oid, aid string
			if rows.Scan(&oid, &aid) == nil {
				out = append(out, anomaly{"missing_commission", oid, aid, "paid order has no commission"})
			}
		}
	}
	rows2, err := pool.Query(c, `SELECT order_id,agent_id,COUNT(*) FROM agent_commissions GROUP BY order_id,agent_id HAVING COUNT(*)>1 LIMIT 200`)
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var oid, aid string
			var n int
			if rows2.Scan(&oid, &aid, &n) == nil {
				out = append(out, anomaly{"duplicate_commission", oid, aid, "commission rows=" + itoa(n)})
			}
		}
	}
	rows3, err := pool.Query(c, `SELECT a.id,COALESCE(SUM(ac.commission_cents),0),a.total_earned_cents FROM agents a LEFT JOIN agent_commissions ac ON ac.agent_id=a.id GROUP BY a.id,a.total_earned_cents HAVING COALESCE(SUM(ac.commission_cents),0)<>a.total_earned_cents LIMIT 200`)
	if err == nil {
		defer rows3.Close()
		for rows3.Next() {
			var aid string
			var sum, stored int64
			if rows3.Scan(&aid, &sum, &stored) == nil {
				out = append(out, anomaly{"agent_total_drift", aid, aid, "ledger=" + i64toa(sum) + " stored=" + i64toa(stored)})
			}
		}
	}
	c.JSON(200, gin.H{"ok": len(out) == 0, "anomalies": out})
}

func itoa(n int) string     { return fmt.Sprint(n) }
func i64toa(n int64) string { return fmt.Sprint(n) }
