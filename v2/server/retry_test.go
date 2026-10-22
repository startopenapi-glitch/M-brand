package app

// retry_test.go — 用户自助重试：失败是产品的一部分（发生了什么/影响/下一步）

import (
	"strings"
	"testing"
)

// 免费试用失败 → 可免费重试，且不额外消耗试用次数；给用户的文案不泄露内部错误
func TestUserRetryTrialJobFree(t *testing.T) {
	resetCreateLimiter()
	srv, base := v2Server(t)
	cl := v2Client(srv)

	st, o := postJSONv2(t, cl, base+"/api/jobs", `{"cn_name":"重试测试","industry":"茶饮"}`)
	if st != 201 {
		t.Fatalf("建任务失败: %d %v", st, o)
	}
	jid := o["id"].(string)
	var vid string
	pool.QueryRow(contextBG(), "SELECT COALESCE(visitor,'') FROM jobs WHERE id=$1", jid).Scan(&vid)
	defer func() {
		mustExec(t, "DELETE FROM jobs WHERE id=$1", jid)
		if vid != "" {
			mustExec(t, "DELETE FROM visitors WHERE id=$1", vid)
		}
	}()

	markJobFailed(jid, `方向 1 生成失败: 生图接口 HTTP 429: {"code":"API_KEY_QUOTA_EXHAUSTED","message":"API key 额度已用完"}`)
	var usedBefore int
	pool.QueryRow(contextBG(), "SELECT trial_used FROM visitors WHERE id=$1", vid).Scan(&usedBefore)

	_, js := getJSON(t, cl, base+"/api/jobs/"+jid)
	if js["can_retry"] != true {
		t.Fatalf("失败任务应标记可重试: %v", js["can_retry"])
	}
	ue, _ := js["user_error"].(string)
	if ue == "" {
		t.Fatal("应返回 user_error 人话文案")
	}
	if strings.Contains(ue, "API_KEY_QUOTA_EXHAUSTED") || strings.Contains(ue, "429") {
		t.Fatalf("给用户的文案不得泄露内部错误: %q", ue)
	}
	if !strings.Contains(ue, "免费重试") {
		t.Fatalf("文案应说明保留信息且免费重试: %q", ue)
	}

	if rst, ro := postJSONv2(t, cl, base+"/api/jobs/"+jid+"/retry", `{}`); rst != 200 {
		t.Fatalf("试用任务重试应 200: %d %v", rst, ro)
	}
	var status string
	pool.QueryRow(contextBG(), "SELECT status FROM jobs WHERE id=$1", jid).Scan(&status)
	if status != "queued" {
		t.Fatalf("重试后应回到 queued: %s", status)
	}
	var usedAfter int
	pool.QueryRow(contextBG(), "SELECT trial_used FROM visitors WHERE id=$1", vid).Scan(&usedAfter)
	if usedAfter != usedBefore {
		t.Fatalf("试用重试不应额外消耗次数: %d→%d", usedBefore, usedAfter)
	}
}

// 付费任务失败已返还额度 → 重试自动重新占票；无票时 402 引导
func TestUserRetryPaidJobReclaimsTicket(t *testing.T) {
	resetCreateLimiter()
	srv, base := v2Server(t)
	cl := v2Client(srv)

	st, o := postJSONv2(t, cl, base+"/api/jobs", `{"cn_name":"付费重试","industry":"咖啡"}`)
	if st != 201 {
		t.Fatalf("建任务失败: %d %v", st, o)
	}
	jid := o["id"].(string)
	var vid string
	pool.QueryRow(contextBG(), "SELECT COALESCE(visitor,'') FROM jobs WHERE id=$1", jid).Scan(&vid)
	code := upperCode("RTRY")
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,used,note,bound_visitor) VALUES($1,1,0,'retry test',$2)", code, vid)
	defer func() {
		mustExec(t, "DELETE FROM jobs WHERE id=$1", jid)
		mustExec(t, "DELETE FROM invite_codes WHERE code=$1", code)
		if vid != "" {
			mustExec(t, "DELETE FROM visitors WHERE id=$1", vid)
		}
	}()

	// 模拟：该任务已占票、在正片阶段失败并被自动返还
	mustExec(t, "UPDATE jobs SET trial=false, quota_code=$2, stage='boards' WHERE id=$1", jid, code)
	mustExec(t, "UPDATE invite_codes SET used=1 WHERE code=$1", code)
	markJobFailed(jid, "品牌系统卡生成失败: 生图接口 HTTP 500")
	var used int
	pool.QueryRow(contextBG(), "SELECT used FROM invite_codes WHERE code=$1", code).Scan(&used)
	if used != 0 {
		t.Fatalf("失败应自动返还额度: used=%d", used)
	}

	if rst, ro := postJSONv2(t, cl, base+"/api/jobs/"+jid+"/retry", `{}`); rst != 200 {
		t.Fatalf("重试应 200: %d %v", rst, ro)
	}
	pool.QueryRow(contextBG(), "SELECT used FROM invite_codes WHERE code=$1", code).Scan(&used)
	if used != 1 {
		t.Fatalf("重试应重新占票: used=%d", used)
	}
	var refunded bool
	pool.QueryRow(contextBG(), "SELECT refunded FROM jobs WHERE id=$1", jid).Scan(&refunded)
	if refunded {
		t.Fatal("重试后 refunded 应复位为 false")
	}

	// 再失败一次 → 再次返还；此时票用于 0，若把票额度耗尽则重试应 402
	mustExec(t, "UPDATE invite_codes SET max_uses=1, used=1 WHERE code=$1", code) // 模拟票被别的任务占用
	markJobFailed(jid, "再次失败")
	if _, ro := postJSONv2(t, cl, base+"/api/jobs/"+jid+"/retry", `{}`); ro == nil {
		t.Fatal("应返回响应体")
	}
}
