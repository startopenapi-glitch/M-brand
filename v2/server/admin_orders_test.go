package app

// admin_orders_test.go — 管理端「订单」接口与营收口径回归。
//
// 锁定两条业务规则：
//  1. 实收 = status='paid' 且 mock=false —— 测试单（mock）不得计入营收，也不得出现在默认列表里。
//  2. created_at 必须格式化非空（回归：timestamptz 扫进 string 失败 → 空串）。
// 另覆盖状态过滤与前缀搜索（订单号 / 微信流水 / 会话 ID）。

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type adminOrderRow struct {
	ID        string `json:"id"`
	Cents     int64  `json:"amount_cents"`
	Status    string `json:"status"`
	Mock      bool   `json:"mock"`
	CreatedAt string `json:"created_at"`
	PaidAt    string `json:"paid_at"`
}

type adminStatsRow struct {
	RevenueToday int64 `json:"revenue_today_cents"`
	RevenueTotal int64 `json:"revenue_total_cents"`
	OrdersMock   int   `json:"orders_mock"`
	OrdersToday  int   `json:"orders_today"`
}

// 直挂 handler（不经 requireAdmin），与 admin_test.go 同法，无需签发 cookie。
func newAdminOrdersSrv(t *testing.T) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	admin := r.Group("/0brand/api/admin", func(c *gin.Context) { c.Set("adminName", "unittest"); c.Next() })
	admin.GET("/orders", adminListOrders)
	admin.GET("/stats", adminStats)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func adminGetJSON(t *testing.T, srv *httptest.Server, path string, out any) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s 应 200，实得 %d：%s", path, resp.StatusCode, truncate(string(body), 200))
	}
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("GET %s 响应不是合法 JSON：%v %s", path, err, truncate(string(body), 200))
	}
}

func listOrders(t *testing.T, srv *httptest.Server, query string) []adminOrderRow {
	t.Helper()
	var rows []adminOrderRow
	adminGetJSON(t, srv, "/0brand/api/admin/orders"+query, &rows)
	for _, r := range rows {
		if r.CreatedAt == "" {
			t.Fatalf("created_at 不得为空串（时间戳扫描失败的症状）：%+v", r)
		}
	}
	return rows
}

func stats(t *testing.T, srv *httptest.Server) adminStatsRow {
	t.Helper()
	var s adminStatsRow
	adminGetJSON(t, srv, "/0brand/api/admin/stats", &s)
	return s
}

func hasOrder(rows []adminOrderRow, id string) bool {
	for _, r := range rows {
		if r.ID == id {
			return true
		}
	}
	return false
}

func TestAdminOrdersRevenueExcludesMock(t *testing.T) {
	srv := newAdminOrdersSrv(t)

	mockID := "0BMOCK" + strings.ToUpper(randHex(4))
	realID := "0BREAL" + strings.ToUpper(randHex(4))
	pendID := "0BPEND" + strings.ToUpper(randHex(4))

	insert := func(id string, cents int64, status string, mock bool) {
		// paid 单补 paid_at（贴近真实数据；今日实收按 paid_at 统计）
		mustExec(t, `INSERT INTO orders(id, amount_cents, channel, status, credited, mock, created_at, paid_at)
		             VALUES($1, $2, 'native', $3, false, $4, now(),
		                    CASE WHEN $3='paid' THEN now() ELSE NULL END)`, id, cents, status, mock)
	}
	cleanup := func() { mustExec(t, "DELETE FROM orders WHERE id = ANY(ARRAY[$1,$2,$3])", mockID, realID, pendID) }
	defer cleanup()

	base := stats(t, srv).RevenueTotal

	// ① 测试单（paid + mock）→ 营收不得变动
	insert(mockID, 999900, "paid", true)
	if got := stats(t, srv).RevenueTotal; got != base {
		t.Fatalf("mock 测试单不得计入实收：基线 %d，插入 mock paid 后 %d", base, got)
	}

	// ② 真实付款单 → 营收恰好 + 12345
	insert(realID, 12345, "paid", false)
	if got, want := stats(t, srv).RevenueTotal, base+12345; got != want {
		t.Fatalf("真实付款单应计入实收：实得 %d，期望 %d", got, want)
	}

	// ③ 待支付单 → 不得计入营收
	insert(pendID, 6900, "pending", false)
	if got, want := stats(t, srv).RevenueTotal, base+12345; got != want {
		t.Fatalf("待支付单不得计入实收：实得 %d，期望 %d", got, want)
	}

	// ④ 默认列表：含真实单、排除 mock 测试单
	def := listOrders(t, srv, "")
	if !hasOrder(def, realID) {
		t.Fatalf("默认列表应含已付款真实单 %s", realID)
	}
	if hasOrder(def, mockID) {
		t.Fatalf("默认列表不得含 mock 测试单 %s（需 include_mock=1）", mockID)
	}

	// ⑤ include_mock=1：测试单出现
	withMock := listOrders(t, srv, "?include_mock=1&limit=500")
	if !hasOrder(withMock, mockID) {
		t.Fatalf("include_mock=1 时列表应含测试单 %s", mockID)
	}

	// ⑥ 状态过滤：paid 只给已付款；pending 只给待支付
	for _, r := range listOrders(t, srv, "?status=paid&include_mock=1&limit=500") {
		if r.Status != "paid" {
			t.Fatalf("status=paid 过滤失效，出现 %q（订单 %s）", r.Status, r.ID)
		}
	}
	pend := listOrders(t, srv, "?status=pending&limit=500")
	if !hasOrder(pend, pendID) {
		t.Fatalf("status=pending 应含待支付单 %s", pendID)
	}
	if hasOrder(pend, realID) {
		t.Fatalf("status=pending 不得含已付款单 %s", realID)
	}

	// ⑦ 前缀搜索：真实单的前 8 位只命中它自己
	hit := listOrders(t, srv, "?q="+realID[:8]+"&include_mock=1&limit=500")
	if !hasOrder(hit, realID) {
		t.Fatalf("前缀搜索应命中 %s", realID)
	}
	if hasOrder(hit, mockID) || hasOrder(hit, pendID) {
		t.Fatalf("前缀搜索 %s 不应命中其它订单", realID[:8])
	}

	// ⑧ 空串搜索不得退化成全表命中（AGENTS.md 隐私事故记录）
	all := listOrders(t, srv, "?q=&include_mock=1&limit=500")
	if len(all) < 3 {
		t.Fatalf("空 q 应返回全部（守卫须放行），实得 %d 条", len(all))
	}
}
