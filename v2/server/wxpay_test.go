package app

// wxpay_test.go — 支付安全测试：签名串拼装 / 金额核验 / 验签拒绝 / GCM 解密 / 并发幂等 / Mock 全流程 / 注入攻击

import (
	"bytes"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// ---------- 测试基建 ----------
func safeTestDatabaseURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Fragment != "" ||
		u.Path != "/zerobrand_test" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
		return false
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil { return false }
	// pgx 的查询参数可覆盖 URL 路径和主机；先解码键名，再拒绝所有连接目标覆盖。
	for key := range query {
		switch strings.ToLower(key) {
		case "dbname", "database", "host", "hostaddr", "port", "service", "servicefile":
			return false
		}
	}
	return true
}

func TestMain(m *testing.M) {
	if !safeTestDatabaseURL(os.Getenv("ZEROBRAND_PG_URL")) {
		fmt.Println("拒绝运行：必须显式配置本机 zerobrand_test 隔离测试库，且不允许查询参数覆盖连接目标")
		os.Exit(1)
	}
	loadSecret()
	if err := initDB(); err != nil {
		fmt.Println("测试库初始化失败（连接配置已隐藏）")
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func testKeys(t *testing.T) (*rsa.PrivateKey, string) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key, "TESTPLATSERIAL001"
}

func signWithPlatform(key *rsa.PrivateKey, msg string) string {
	sum := sha256Sum([]byte(msg))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum)
	return base64.StdEncoding.EncodeToString(sig)
}

func encryptGCM(t *testing.T, key, nonce, aad string, plaintext []byte) string {
	block, _ := aes.NewCipher([]byte(key))
	gcm, _ := cipher.NewGCM(block)
	ct := gcm.Seal(nil, []byte(nonce), plaintext, []byte(aad))
	return base64.StdEncoding.EncodeToString(ct)
}

func setupTestOrder(t *testing.T, id string, cents int64) string {
	pool.Exec(contextBG(), "DELETE FROM orders WHERE id LIKE '0BTEST%'")
	pool.Exec(contextBG(), "DELETE FROM invite_codes WHERE code IN ('PAYTEST','MOCKFLOW')")
	pool.Exec(contextBG(), "INSERT INTO invite_codes(code,max_uses) VALUES ('PAYTEST',10) ON CONFLICT DO NOTHING")
	pool.Exec(contextBG(),
		`INSERT INTO orders(id, code, amount_cents, channel, status, mock) VALUES ($1,'PAYTEST',$2,'native','pending',true)`,
		id, cents)
	return id
}

func orderStatus(t *testing.T, id string) string {
	var s string
	if err := pool.QueryRow(contextBG(), "SELECT status FROM orders WHERE id=$1", id).Scan(&s); err != nil {
		return ""
	}
	return s
}

func codeMaxUses(t *testing.T) int {
	var n int
	pool.QueryRow(contextBG(), "SELECT max_uses FROM invite_codes WHERE code='PAYTEST'").Scan(&n)
	return n
}

// ---------- 1. 签名串拼装 ----------
func TestBuildSignMessage(t *testing.T) {
	got := buildSignMessage("POST", "/v3/pay/transactions/native?x=1", "1700000000", "abc123", `{"k":"v"}`)
	want := "POST\n/v3/pay/transactions/native?x=1\n1700000000\nabc123\n" + `{"k":"v"}` + "\n"
	if got != want {
		t.Fatalf("签名串不匹配:\n got=%q\nwant=%q", got, want)
	}
	// GET 的 body 为空串
	got2 := buildSignMessage("GET", "/v3/certificates", "1", "n", "")
	if got2 != "GET\n/v3/certificates\n1\nn\n\n" {
		t.Fatalf("GET 签名串错误: %q", got2)
	}
}

// ---------- 2. 金额核验：不符拒绝 ----------
func TestSettleAmountMismatch(t *testing.T) {
	id := setupTestOrder(t, "0BTEST001", 6900)
	err := settleOrder(id, "", "", 6901, "TX1") // 差 1 分
	if err == nil || !strings.Contains(err.Error(), "金额不符") {
		t.Fatalf("应拒绝金额不符: %v", err)
	}
	if orderStatus(t, id) != "pending" {
		t.Fatalf("订单不应被入账")
	}
	// 正确金额 → 入账
	if err := settleOrder(id, "", "", 6900, "TX1"); err != nil {
		t.Fatal(err)
	}
	if orderStatus(t, id) != "paid" {
		t.Fatalf("应已入账")
	}
}

// ---------- 3. 验签拒绝路径 ----------
func TestVerifyNotifyRejections(t *testing.T) {
	platKey, serial := testKeys(t)
	wxcfg.mu.Lock()
	oldOverride := wxcfg.platformSerialOverride
	oldKey := wxcfg.platformKeyOverride
	oldReady := wxcfg.apiV3Key
	wxcfg.platformSerialOverride = serial
	wxcfg.platformKeyOverride = &platKey.PublicKey
	wxcfg.apiV3Key = "0123456789abcdef0123456789abcdef" // 32B 测试密钥
	wxcfg.mu.Unlock()
	defer func() {
		wxcfg.mu.Lock()
		wxcfg.platformSerialOverride = oldOverride
		wxcfg.platformKeyOverride = oldKey
		wxcfg.apiV3Key = oldReady
		wxcfg.mu.Unlock()
	}()
	body := `{"event_type":"TRANSACTION.SUCCESS"}`
	ts := fmt.Sprintf("%d", time.Now().Unix())
	nonce := "testnonce01"
	sig := signWithPlatform(platKey, ts+"\n"+nonce+"\n"+body+"\n")

	// 3a. 正确签名 → 通过
	if err := verifyNotifyHeaders(serial, ts, nonce, body, sig); err != nil {
		t.Fatalf("正确签名应通过: %v", err)
	}
	// 3b. 错误序列号 → 拒绝
	if err := verifyNotifyHeaders("WRONGSERIAL", ts, nonce, body, sig); err == nil {
		t.Fatal("错误序列号应拒绝")
	}
	// 3c. 过期时间戳 → 拒绝
	oldTs := fmt.Sprintf("%d", time.Now().Unix()-600)
	if err := verifyNotifyHeaders(serial, oldTs, nonce, body, sig); err == nil {
		t.Fatal("过期时间戳应拒绝")
	}
	// 3d. 篡改 body（签名不匹配）→ 拒绝
	if err := verifyNotifyHeaders(serial, ts, nonce, body+"x", sig); err == nil {
		t.Fatal("篡改 body 应拒绝")
	}
	// 3e. 伪造签名 → 拒绝
	if err := verifyNotifyHeaders(serial, ts, nonce, body, base64.StdEncoding.EncodeToString([]byte("garbage"))); err == nil {
		t.Fatal("伪造签名应拒绝")
	}
	// 3f. 未配置（清空 override）→ 报错（503 路径）
	wxcfg.mu.Lock()
	wxcfg.platformSerialOverride = ""
	wxcfg.platformKeyOverride = nil
	wxcfg.mu.Unlock()
	if err := verifyNotifyHeaders(serial, ts, nonce, body, sig); err == nil {
		t.Fatal("未配平台证书应报错")
	}
	wxcfg.mu.Lock()
	wxcfg.platformSerialOverride = serial
	wxcfg.platformKeyOverride = &platKey.PublicKey
	wxcfg.mu.Unlock()
}

// ---------- 4. GCM 解密往返 ----------
func TestGCMRoundTrip(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef"
	pt := []byte(`{"out_trade_no":"0BTEST","amount":{"total":6900}}`)
	ct := encryptGCM(t, key, "123456789012", "transaction", pt)
	got, err := decryptAESGCM(key, "123456789012", "transaction", ct)
	if err != nil || string(got) != string(pt) {
		t.Fatalf("GCM 往返失败: %v", err)
	}
	// 错误密钥 → 拒绝
	if _, err := decryptAESGCM("8234567890abcdef0123456789abcdef", "123456789012", "transaction", ct); err == nil {
		t.Fatal("错误密钥应解密失败")
	}
	// 错误 AAD → 拒绝
	if _, err := decryptAESGCM(key, "123456789012", "wrong", ct); err == nil {
		t.Fatal("错误 AAD 应解密失败")
	}
	// 非 32 字节密钥 → 拒绝
	if _, err := decryptAESGCM("short", "123456789012", "", ct); err == nil {
		t.Fatal("短密钥应拒绝")
	}
}

// ---------- 5. 并发双入账只发一张单次码 ----------
func TestConcurrentSettleSingleCredit(t *testing.T) {
	setupTestOrder(t, "0BTEST002", 6900)
	pool.Exec(contextBG(), "DELETE FROM invite_codes WHERE order_id='0BTEST002'")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			settleOrder("0BTEST002", "", "", 6900, "TX2")
		}()
	}
	wg.Wait()
	if orderStatus(t, "0BTEST002") != "paid" {
		t.Fatal("应入账")
	}
	// 一单一码：并发入账只发一张码，且为单次码；已有码不被充值
	var n int
	pool.QueryRow(contextBG(), "SELECT COUNT(*) FROM invite_codes WHERE order_id='0BTEST002'").Scan(&n)
	if n != 1 {
		t.Fatalf("并发入账应只发一张码：%d", n)
	}
	var maxU int
	pool.QueryRow(contextBG(), "SELECT max_uses FROM invite_codes WHERE order_id='0BTEST002'").Scan(&maxU)
	if maxU != 1 {
		t.Fatalf("新码应为单次码：%d", maxU)
	}
	if codeMaxUses(t) != 10 {
		t.Fatalf("PAYTEST 不应被充值：%d", codeMaxUses(t))
	}
	pool.Exec(contextBG(), "DELETE FROM orders WHERE id='0BTEST002'")
	pool.Exec(contextBG(), "DELETE FROM invite_codes WHERE order_id='0BTEST002'")
}

// ---------- 6. Mock 全流程（HTTP 层：下单→轮询→入账→交付）----------
func TestMockFullFlow(t *testing.T) {
 enablePayMock(t);resetCreateLimiter();srv,base:=v2Server(t);cl:=v2Client(srv);jid:=itemCheckoutFixture(t,cl,base)
 st,o:=postJSON(t,cl,base+"/api/pay/create",`{"job_id":"`+jid+`","items":["logo_h"]}`);if st!=201{t.Fatalf("checkout %d %v",st,o)}
 id:=o["id"].(string);mustExec(t,"UPDATE orders SET created_at=now()-interval '3 seconds' WHERE id=$1",id)
 st,p:=getJSON(t,cl,base+"/api/pay/status/"+id);if st!=200||p["status"]!="paid"{t.Fatalf("mock polling %d %v",st,p)}
 var status string;pool.QueryRow(contextBG(),"SELECT status FROM jobs WHERE id=$1",jid).Scan(&status);if status!="queued"{t.Fatalf("not durably queued: %s",status)}
}

func TestNotifyInjectionAttacks(t *testing.T) {
	platKey, serial := testKeys(t)
	merchKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	wxcfg.mu.Lock()
	wxcfg.platformSerialOverride = serial
	wxcfg.platformKeyOverride = &platKey.PublicKey
	wxcfg.apiV3Key = "0123456789abcdef0123456789abcdef"
	wxcfg.mchid = "TESTMCH"
	wxcfg.appid = "TESTAPP"
	wxcfg.serial = "TESTMERCHSERIAL"
	wxcfg.privateKey = merchKey
	wxcfg.mu.Unlock()
	defer func() {
		wxcfg.mu.Lock()
		wxcfg.platformSerialOverride = ""
		wxcfg.platformKeyOverride = nil
		wxcfg.apiV3Key = ""
		wxcfg.mchid = ""
		wxcfg.appid = ""
		wxcfg.serial = ""
		wxcfg.privateKey = nil
		wxcfg.mu.Unlock()
	}()
	setupTestOrder(t, "0BTEST003", 6900)

	buildEvent := func(cents int64) string {
		res := map[string]any{
			"out_trade_no": "0BTEST003", "transaction_id": "FAKETX",
			"mchid": "TESTMCH", "appid": "TESTAPP",
			"amount": map[string]any{"total": cents, "currency": "CNY"},
		}
		pt, _ := json.Marshal(res)
		return fmt.Sprintf(`{"event_type":"TRANSACTION.SUCCESS","resource":{"algorithm":"AEAD_AES_256_GCM","ciphertext":"%s","nonce":"%s","associated_data":"transaction"}}`,
			encryptGCM(t, "0123456789abcdef0123456789abcdef", "123456789012", "transaction", pt),
			"123456789012")
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/notify", payNotify)
	srv := httptest.NewServer(r)
	defer srv.Close()

	doNotifySigned := func(serialHdr, sendBody, signBody string) int {
		ts := fmt.Sprintf("%d", time.Now().Unix())
		sig := signWithPlatform(platKey, ts+"\ntestnonce\n"+signBody+"\n")
		req, _ := http.NewRequest("POST", srv.URL+"/notify", bytes.NewBufferString(sendBody))
		req.Header.Set("Wechatpay-Serial", serialHdr)
		req.Header.Set("Wechatpay-Timestamp", ts)
		req.Header.Set("Wechatpay-Nonce", "testnonce")
		req.Header.Set("Wechatpay-Signature", sig)
		resp, err := srv.Client().Do(req)
		if err != nil {
			return 0
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	doNotify := func(serialHdr, body string) int {
		return doNotifySigned(serialHdr, body, body)
	}

	// 7a. 错误序列号 → 401
	if code := doNotify("EVILSERIAL", buildEvent(6900)); code != 401 {
		t.Fatalf("错误序列号应 401: %d", code)
	}
	// 7b. 错误金额（100 分）→ 403
	if code := doNotify(serial, buildEvent(100)); code != 403 {
		t.Fatalf("错金额应 403: %d", code)
	}
	// 7c. 正确金额+签名 → 200 入账
	if code := doNotify(serial, buildEvent(6900)); code != 200 {
		t.Fatalf("正确回调应 200: %d", code)
	}
	if orderStatus(t, "0BTEST003") != "paid" {
		t.Fatal("回调后应入账")
	}
	// 7d. 签原 body 发篡改 body（签名失效）→ 401
	if code := doNotifySigned(serial, buildEvent(6900)+"x", buildEvent(6900)); code != 401 {
		t.Fatalf("篡改应 401: %d", code)
	}
}

// ---------- 辅助 ----------
func fakeGate(c *gin.Context, code string) {
	vid := "fixture-gate-"+code
	pool.Exec(contextBG(),"INSERT INTO visitors(id) VALUES($1) ON CONFLICT DO NOTHING",vid)
	pool.Exec(contextBG(),"UPDATE invite_codes SET bound_visitor=$2 WHERE code=$1",code,vid)
	c.Request.AddCookie(&http.Cookie{Name:visitorCookie,Value:makeCookieValue(gatePayload{Code:vid,Expiry:time.Now().Add(time.Hour).Unix()})})
	val := makeCookieValue(gatePayload{
		Code: code, Expiry: time.Now().Add(time.Hour).Unix(), CSRF: "t"})
	c.Request.AddCookie(&http.Cookie{Name: gateCookie, Value: val})
}

func sha256Sum(b []byte) []byte {
	h := sha256.New()
	h.Write(b)
	return h.Sum(nil)
}
