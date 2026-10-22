package app

// Copy this file into the isolated snapshot's server/ directory and run only
// TestAuditPayment*. Existing TestMain enforces the local zerobrand_test database.
// These are desired-behavior assertions: the reviewed snapshot should FAIL them.
// No workers are started. Every WeChat HTTP request uses an in-memory transport.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type auditPaymentTransport func(*http.Request) (*http.Response, error)

func (f auditPaymentTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func auditPaymentConfig(t *testing.T, mock bool) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil { t.Fatal(err) }
	wxcfg.mu.Lock()
	oldApp, oldMerchant, oldSerial := wxcfg.appid, wxcfg.mchid, wxcfg.serial
	oldPrivate, oldMock := wxcfg.privateKey, wxcfg.mock
	oldPlatformSerial, oldPlatformKey := wxcfg.platformSerialOverride, wxcfg.platformKeyOverride
	wxcfg.appid, wxcfg.mchid, wxcfg.serial = "audit-app", "audit-merchant", "audit-merchant-serial"
	wxcfg.privateKey, wxcfg.mock = key, mock
	wxcfg.platformSerialOverride, wxcfg.platformKeyOverride = "audit-platform-serial", &key.PublicKey
	wxcfg.mu.Unlock()
	oldHTTP := wxHTTP
	wxHTTP = &http.Client{Timeout: time.Second, Transport: auditPaymentTransport(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("audit blocked all unexpected payment HTTP requests")
	})}
	t.Cleanup(func() {
		wxHTTP = oldHTTP
		wxcfg.mu.Lock()
		wxcfg.appid, wxcfg.mchid, wxcfg.serial = oldApp, oldMerchant, oldSerial
		wxcfg.privateKey, wxcfg.mock = oldPrivate, oldMock
		wxcfg.platformSerialOverride, wxcfg.platformKeyOverride = oldPlatformSerial, oldPlatformKey
		wxcfg.mu.Unlock()
	})
	return key
}

func auditPaymentExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil { t.Fatal(err) }
}

func auditPaymentJob(t *testing.T) (string, string) {
	t.Helper()
	id, vid := "audit-job-"+randHex(6), "audit-visitor-"+randHex(6)
	auditPaymentExec(t, "INSERT INTO visitors(id) VALUES($1)", vid)
	auditPaymentExec(t, `INSERT INTO jobs(id,code,visitor,form,status,stage,unlocked_packs)
		VALUES($1,'',$2,'{"cn_name":"Audit fixture","industry":"test"}'::jsonb,'done','done','{}')`, id, vid)
	t.Cleanup(func() {
		auditPaymentExec(t, "DELETE FROM job_images WHERE job_id=$1", id)
		auditPaymentExec(t, "DELETE FROM usage_events WHERE job_id=$1", id)
		auditPaymentExec(t, "DELETE FROM jobs WHERE id=$1", id)
		auditPaymentExec(t, "DELETE FROM visitors WHERE id=$1", vid)
	})
	return id, vid
}

func auditPaymentOrder(t *testing.T, jobID, pack, agentCode string, cents int64) string {
	t.Helper()
	id := "audit-order-" + randHex(6)
	auditPaymentExec(t, `INSERT INTO orders(id,code,amount_cents,channel,status,mock,job_id,pack,agent_code)
		VALUES($1,'',$2,'native','pending',false,$3,$4,$5)`, id, cents, jobID, pack, agentCode)
	t.Cleanup(func() {
		auditPaymentExec(t, "DELETE FROM agent_commissions WHERE order_id=$1", id)
		auditPaymentExec(t, "DELETE FROM order_items WHERE order_id=$1", id)
		auditPaymentExec(t, "DELETE FROM invite_codes WHERE order_id=$1", id)
		auditPaymentExec(t, "DELETE FROM audit_events WHERE object_type='order' AND object_id=$1", id)
		auditPaymentExec(t, "DELETE FROM orders WHERE id=$1", id)
	})
	return id
}

func auditPaymentAgent(t *testing.T, earned int64) (string, string) {
	t.Helper()
	id, code := "audit-agent-"+randHex(6), "AG"+strings.ToUpper(randHex(4))
	auditPaymentExec(t, `INSERT INTO agents(id,name,email,password_hash,code,commission_pct,total_earned_cents)
		VALUES($1,'Audit fixture',$2,'unused-test-hash',$3,40,$4)`, id, id+"@audit.invalid", code, earned)
	t.Cleanup(func() {
		auditPaymentExec(t, "DELETE FROM agent_payouts WHERE agent_id=$1", id)
		auditPaymentExec(t, "DELETE FROM agent_commissions WHERE agent_id=$1", id)
		auditPaymentExec(t, "DELETE FROM agents WHERE id=$1", id)
	})
	return id, code
}

func auditPaymentPOST(router http.Handler, path string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func auditPaymentPayoutRouter() http.Handler {
	r := gin.New()
	r.Use(gin.Recovery())
	// Test the accounting handler, not auth; production mounts this under requireAdmin.
	r.POST("/agents/:id/payout", adminAgentPayout)
	return r
}

func TestAuditPaymentItemCheckoutContract(t *testing.T) {
	auditPaymentConfig(t, true)
	for _, tc := range []struct{name string; all bool; items []string; cents int64; count int}{
		{"one_item", false, []string{"logo_h"}, 1900, 1},
		{"all_items", true, nil, 9900, 17},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jid, vid := auditPaymentJob(t)
			body, _ := json.Marshal(map[string]any{"job_id": jid, "items": tc.items, "all": tc.all})
			req := httptest.NewRequest(http.MethodPost, "/0brand/api/pay/pack", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: visitorCookie, Value: makeCookieValue(gatePayload{Code: vid, Expiry: time.Now().Add(time.Hour).Unix()})})
			rec := httptest.NewRecorder()
			buildRouter().ServeHTTP(rec, req)
			if rec.Code != http.StatusCreated {
				t.Fatalf("valid UI item payload must create checkout: got HTTP %d body=%s; want 201", rec.Code, rec.Body.String())
			}
			var got struct {ID string `json:"id"`; Cents int64 `json:"amount_cents"`}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil { t.Fatal(err) }
			t.Cleanup(func() {
				auditPaymentExec(t, "DELETE FROM order_items WHERE order_id=$1", got.ID)
				auditPaymentExec(t, "DELETE FROM orders WHERE id=$1", got.ID)
			})
			var n int
			if err := pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM order_items WHERE order_id=$1", got.ID).Scan(&n); err != nil { t.Fatal(err) }
			if got.Cents != tc.cents || n != tc.count { t.Fatalf("checkout snapshot cents=%d items=%d; want cents=%d items=%d",got.Cents,n,tc.cents,tc.count) }
		})
	}
}

func TestAuditPaymentSettlementSchedulesSelectedItem(t *testing.T) {
	auditPaymentConfig(t, false)
	jid, _ := auditPaymentJob(t)
	// brand_story is deliberately selected: even a synchronous implementation of
	// afterPaymentGenerate does not call Generate for this kind. Workers never run.
	t.Setenv("ZEROBRAND_GEN_DIR", t.TempDir())
	if err := os.MkdirAll(filepath.Join(genDir(), jid), 0755); err != nil { t.Fatal(err) }
	oid := auditPaymentOrder(t, jid, "brand_story", "", 90)
	auditPaymentExec(t, "INSERT INTO order_items(order_id,item_kind,item_name,price_cents) VALUES($1,'brand_story','Audit story',90)", oid)
	if err := settleOrder(oid, "audit-merchant", "audit-app", 90, "AUDIT-TX-"+oid); err != nil { t.Fatal(err) }
	var generated int
	var status, stage string
	if err := pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM job_images WHERE job_id=$1 AND kind='brand_story' AND COALESCE(file,'')<>''", jid).Scan(&generated); err != nil { t.Fatal(err) }
	if err := pool.QueryRow(context.Background(), "SELECT status,stage FROM jobs WHERE id=$1", jid).Scan(&status,&stage); err != nil { t.Fatal(err) }
	queued := status == "queued" || status == "running" || status == "boards_running"
	if generated == 0 && !queued {
		t.Fatalf("paid selected item must be delivered or durably queued: generated=%d job_status=%s stage=%s; payment alone created no work", generated,status,stage)
	}
}

func TestAuditPaymentPackageSettlementGrantsPurchasedPackage(t *testing.T) {
	auditPaymentConfig(t, false)
	jid, _ := auditPaymentJob(t)
	oid := auditPaymentOrder(t, jid, "logo", "", 990)
	if err := settleOrder(oid,"audit-merchant","audit-app",990,"AUDIT-TX-"+oid); err != nil { t.Fatal(err) }
	var unlocked []string
	if err := pool.QueryRow(context.Background(), "SELECT unlocked_packs FROM jobs WHERE id=$1",jid).Scan(&unlocked); err != nil { t.Fatal(err) }
	if !contains(unlocked,"logo") && !contains(unlocked,"all") { t.Fatalf("paid logo package was not granted: unlocked_packs=%v", unlocked) }
}

func TestAuditPaymentSettlementRecordsFortyPercentCommission(t *testing.T) {
	auditPaymentConfig(t, false)
	agentID, agentCode := auditPaymentAgent(t,0)
	oid := auditPaymentOrder(t,"","",agentCode,9900)
	if err := settleOrder(oid,"audit-merchant","audit-app",9900,"AUDIT-TX-"+oid); err != nil { t.Fatal(err) }
	// A repeat must remain idempotent AND recover or retain the commission.
	if err := settleOrder(oid,"audit-merchant","audit-app",9900,"AUDIT-TX-"+oid); err != nil { t.Fatal(err) }
	var count int
	var ledger, earned int64
	if err := pool.QueryRow(context.Background(),"SELECT COUNT(*),COALESCE(SUM(commission_cents),0) FROM agent_commissions WHERE order_id=$1",oid).Scan(&count,&ledger); err != nil { t.Fatal(err) }
	if err := pool.QueryRow(context.Background(),"SELECT total_earned_cents FROM agents WHERE id=$1",agentID).Scan(&earned); err != nil { t.Fatal(err) }
	if count != 1 || ledger != 3960 || earned != 3960 { t.Fatalf("9900-cent payment at 40%% must credit exactly 3960 once: rows=%d ledger=%d balance=%d",count,ledger,earned) }
}

func TestAuditPaymentPartialPayoutLeavesUnpaidCommission(t *testing.T) {
	agentID,_ := auditPaymentAgent(t,300)
	for i:=0;i<3;i++ {
		auditPaymentExec(t,"INSERT INTO agent_commissions(agent_id,order_id,order_amount_cents,commission_cents) VALUES($1,$2,250,100)",agentID,fmt.Sprintf("%s-order-%d",agentID,i))
	}
	rec := auditPaymentPOST(auditPaymentPayoutRouter(),"/agents/"+agentID+"/payout",[]byte(`{"AmountCents":1,"RequestKey":"audit-partial-payout","Note":"audit only; no money transfer"}`))
	if rec.Code != 200 { t.Fatalf("fixture payout rejected: status=%d body=%s",rec.Code,rec.Body.String()) }
	var paidRows int
	var pending, totalPaid int64
	if err := pool.QueryRow(context.Background(),"SELECT COUNT(*) FILTER (WHERE status='paid'),COALESCE(SUM(commission_cents) FILTER (WHERE status='pending'),0) FROM agent_commissions WHERE agent_id=$1",agentID).Scan(&paidRows,&pending); err != nil { t.Fatal(err) }
	if err := pool.QueryRow(context.Background(),"SELECT total_paid_cents FROM agents WHERE id=$1",agentID).Scan(&totalPaid); err != nil { t.Fatal(err) }
	if totalPaid != 1 || paidRows != 0 || pending == 0 { t.Fatalf("1-cent partial payout cannot fully pay any 100-cent commission: total_paid=%d fully_paid_rows=%d pending=%d; want total_paid=1, fully_paid_rows=0, positive pending",totalPaid,paidRows,pending) }
}

func TestAuditPaymentConcurrentPayoutCannotOverdraw(t *testing.T) {
	if pool.Config().MaxConns < 4 { t.Fatal("deterministic payout barrier requires >=4 isolated database connections") }
	agentID,_ := auditPaymentAgent(t,1000)
 auditPaymentExec(t,"INSERT INTO agent_commissions(agent_id,order_id,order_amount_cents,commission_cents) VALUES($1,$2,2500,1000)",agentID,agentID+"-order")
	guard,err := pool.Begin(context.Background())
	if err != nil { t.Fatal(err) }
	defer guard.Rollback(context.Background())
	var lockedID string
	if err := guard.QueryRow(context.Background(),"SELECT id FROM agents WHERE id=$1 FOR UPDATE",agentID).Scan(&lockedID); err != nil { t.Fatal(err) }
	router := auditPaymentPayoutRouter()
	results := make(chan *httptest.ResponseRecorder,2)
	for i:=0;i<2;i++ {
		go func(){ results <- auditPaymentPOST(router,"/agents/"+agentID+"/payout",[]byte(`{"AmountCents":1000,"RequestKey":"`+randHex(16)+`","Note":"audit only; no money transfer"}`)) }()
	}
	// FOR UPDATE blocks the FK checks on payout INSERTs but not their earlier
	// plain balance reads. Seeing two blocked INSERTs proves both read 1000/0.
	blocked := 0
	deadline := time.Now().Add(4*time.Second)
	for time.Now().Before(deadline) {
		err = pool.QueryRow(context.Background(),`SELECT COUNT(*) FROM pg_stat_activity
			WHERE datname=current_database() AND wait_event_type='Lock'
			AND cardinality(pg_blocking_pids(pid))>0
			AND (query LIKE 'INSERT INTO agent_payouts%' OR query LIKE 'SELECT total_earned_cents,total_paid_cents FROM agents%')`).Scan(&blocked)
		if err != nil || blocked >= 2 { break }
		time.Sleep(10*time.Millisecond)
	}
	unlockErr := guard.Rollback(context.Background())
	responses := make([]*httptest.ResponseRecorder,0,2)
	for i:=0;i<2;i++ {
		select { case r:=<-results: responses=append(responses,r)
		case <-time.After(4*time.Second): t.Fatal("payout handler did not finish after releasing fixture lock") }
	}
	if err != nil { t.Fatal(err) }
	if unlockErr != nil { t.Fatal(unlockErr) }
	if blocked < 2 { t.Fatalf("barrier did not observe both concurrent payout requests waiting for the agent lock: blocked=%d",blocked) }
	var earned,paid,payouts int64
	if err := pool.QueryRow(context.Background(),"SELECT total_earned_cents,total_paid_cents FROM agents WHERE id=$1",agentID).Scan(&earned,&paid); err != nil { t.Fatal(err) }
	if err := pool.QueryRow(context.Background(),"SELECT COALESCE(SUM(amount_cents),0) FROM agent_payouts WHERE agent_id=$1",agentID).Scan(&payouts); err != nil { t.Fatal(err) }
	succeeded:=0
	for _,r:=range responses { if r.Code==200 { succeeded++ } }
	if paid>earned || payouts>earned || succeeded!=1 { t.Fatalf("concurrent payouts must reserve balance once: response_codes=%d,%d earned=%d paid=%d ledger=%d; want one success and no overdraw",responses[0].Code,responses[1].Code,earned,paid,payouts) }
}

func TestAuditPaymentRejectsUnsignedWeChatResponse(t *testing.T) {
	auditPaymentConfig(t,false)
	body:=`{"out_trade_no":"audit-response","trade_state":"SUCCESS","transaction_id":"AUDIT-TX","mchid":"audit-merchant","appid":"audit-app","amount":{"total":9900,"currency":"CNY"}}`
	wxHTTP=&http.Client{Transport:auditPaymentTransport(func(req *http.Request)(*http.Response,error){
		return &http.Response{StatusCode:200,Header:make(http.Header),Body:io.NopCloser(strings.NewReader(body)),Request:req},nil
	})}
	var result wxQueryResp
	if err:=wxRequest("GET","/v3/pay/transactions/out-trade-no/audit-response?mchid=audit-merchant",nil,&result);err==nil {
		t.Fatalf("unsigned payment response must be rejected before trusting SUCCESS; accepted trade_state=%q",result.TradeState)
	}
}

func TestAuditPaymentQueryRejectsDifferentOrder(t *testing.T) {
	key:=auditPaymentConfig(t,false)
	oid:=auditPaymentOrder(t,"","","",9900)
	body:=`{"out_trade_no":"audit-other-order","trade_state":"SUCCESS","transaction_id":"AUDIT-OTHER-TX","mchid":"audit-merchant","appid":"audit-app","amount":{"total":9900,"currency":"CNY"}}`
	wxHTTP=&http.Client{Transport:auditPaymentTransport(func(req *http.Request)(*http.Response,error){
		ts,nonce:=fmt.Sprint(time.Now().Unix()),"audit-response-nonce"
		h:=make(http.Header)
		h.Set("Wechatpay-Serial","audit-platform-serial")
		h.Set("Wechatpay-Timestamp",ts)
		h.Set("Wechatpay-Nonce",nonce)
		h.Set("Wechatpay-Signature",signWithPlatform(key,ts+"\n"+nonce+"\n"+body+"\n"))
		return &http.Response{StatusCode:200,Header:h,Body:io.NopCloser(strings.NewReader(body)),Request:req},nil
	})}
	queryAndSettle(oid)
	var status string
	if err:=pool.QueryRow(context.Background(),"SELECT status FROM orders WHERE id=$1",oid).Scan(&status);err!=nil {t.Fatal(err)}
	if status!="pending" { t.Fatalf("response for different out_trade_no must not settle requested order: status=%s; want pending",status) }
}
