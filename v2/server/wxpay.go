package app

// wxpay.go — 微信支付 v3 核心：手写签名/验签/AES-GCM，仅调 4 个支付 API（+平台证书自举）
// 安全铁律：金额全程整数分；私钥/APIv3 密钥不进日志；下单类超时不重试；不跳过 TLS。

import (
	"bytes"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/startopenapi-glitch/M-brand/v2/server/internal/platform"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const wxGateway = "https://api.mch.weixin.qq.com" // 生产锁死，不许改

type wxConfig struct {
	appid, mchid, serial, apiV3Key string
	notifyURL                      string
	privateKey                     *rsa.PrivateKey
	platformSerialOverride         string // WXPAY_PLATFORM_SERIAL：设置后回调序列号必须严格相等
	platformKeyOverride            *rsa.PublicKey
	mock, h5Enabled, envProduction bool
	mu                             sync.RWMutex
	platformCerts                  map[string]*rsa.PublicKey // serial -> key（自举+定期刷新）
}

var wxcfg = wxConfig{platformCerts: map[string]*rsa.PublicKey{}}

func loadWxConfig() {
	wxcfg.mu.Lock()
	defer wxcfg.mu.Unlock()
	wxcfg.appid = os.Getenv("WXPAY_APPID")
	wxcfg.mchid = os.Getenv("WXPAY_MCHID")
	wxcfg.serial = os.Getenv("WXPAY_SERIAL")
	wxcfg.apiV3Key = os.Getenv("WXPAY_APIV3_KEY")
	wxcfg.notifyURL = os.Getenv("WXPAY_NOTIFY_URL")
	if wxcfg.notifyURL == "" {
		wxcfg.notifyURL = platform.LoadConfig().PublicOrigin + "/api/pay/notify"
	}
	keyPEM := ""
	if p := os.Getenv("WXPAY_KEY_PATH"); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			keyPEM = string(b)
		}
	}
	if keyPEM == "" {
		keyPEM = os.Getenv("WXPAY_PRIVATE_KEY")
	}
	if keyPEM != "" {
		if blk, _ := pem.Decode([]byte(keyPEM)); blk != nil {
			if k, err := x509.ParsePKCS1PrivateKey(blk.Bytes); err == nil {
				wxcfg.privateKey = k
			} else if k8, err8 := x509.ParsePKCS8PrivateKey(blk.Bytes); err8 == nil {
				if rk, ok := k8.(*rsa.PrivateKey); ok {
					wxcfg.privateKey = rk
				}
			}
		}
	}
	if p := os.Getenv("WXPAY_PLATFORM_KEY_PATH"); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			if blk, _ := pem.Decode(b); blk != nil {
				if crt, err := x509.ParseCertificate(blk.Bytes); err == nil {
					if pk, ok := crt.PublicKey.(*rsa.PublicKey); ok {
						wxcfg.platformKeyOverride = pk
					}
				} else if pk, err := x509.ParsePKIXPublicKey(blk.Bytes); err == nil {
					if rpk, ok := pk.(*rsa.PublicKey); ok {
						wxcfg.platformKeyOverride = rpk
					}
				}
			}
		}
	}
	wxcfg.platformSerialOverride = os.Getenv("WXPAY_PLATFORM_SERIAL")
	wxcfg.h5Enabled = os.Getenv("WXPAY_H5_ENABLED") == "1"
	wxcfg.mock = os.Getenv("WXPAY_MOCK") == "1" && os.Getenv("ZEROBRAND_ENV") != "production"
	wxcfg.envProduction = os.Getenv("ZEROBRAND_ENV") == "production"
}

// wxReady 签名凭据齐备（可下单/查单/关单——主动查单模式不需要 APIv3 密钥）
func wxReady() bool {
	wxcfg.mu.RLock()
	defer wxcfg.mu.RUnlock()
	return wxcfg.appid != "" && wxcfg.mchid != "" && wxcfg.serial != "" && wxcfg.privateKey != nil
}

// wxCallbackReady 回调验签齐备（需要 APIv3 密钥 + 平台证书）
func wxCallbackReady() bool {
	wxcfg.mu.RLock()
	defer wxcfg.mu.RUnlock()
	return wxcfg.apiV3Key != "" && len(wxcfg.apiV3Key) == 32 &&
		(wxcfg.platformKeyOverride != nil || len(wxcfg.platformCerts) > 0)
}

func wxMock() bool { wxcfg.mu.RLock(); defer wxcfg.mu.RUnlock(); return wxcfg.mock }

// wxResponseVerificationReady：能验证微信 POST 响应签名。没有平台证书时
// Native 下单会在渠道已创建订单后被本地验签拒绝，留下“无二维码的 pending”。
func wxResponseVerificationReady() bool {
	wxcfg.mu.RLock()
	defer wxcfg.mu.RUnlock()
	return wxcfg.platformKeyOverride != nil || len(wxcfg.platformCerts) > 0
}

// wxEnabled 对用户暴露的支付可用性（真实或 mock）。
// 真实模式要求平台证书，否则不创建必死订单，直接提示支付暂不可用。
func wxEnabled() bool { return wxMock() || (wxReady() && wxResponseVerificationReady()) }

// ---------- 出站签名 ----------
// 签名串 = METHOD\nPATH(含query)\ntimestamp\nnonce\nbody\n（GET body 为空串）
func buildSignMessage(method, pathWithQuery, timestamp, nonce, body string) string {
	return method + "\n" + pathWithQuery + "\n" + timestamp + "\n" + nonce + "\n" + body + "\n"
}

func signMessage(msg string) (string, error) {
	wxcfg.mu.RLock()
	key := wxcfg.privateKey
	serial, mchid := wxcfg.serial, wxcfg.mchid
	wxcfg.mu.RUnlock()
	if key == nil {
		return "", errors.New("商户私钥未配置")
	}
	sum := sha256.Sum256([]byte(msg))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	_ = serial
	_ = mchid
	return base64.StdEncoding.EncodeToString(sig), nil
}

var wxHTTP = &http.Client{
	Timeout: 10 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return errors.New("redirects disabled") // 禁止跟随重定向
	},
	Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}},
}

// wxRequest 调微信 API。orderCreate=true 时超时/模糊失败绝不重试（防重复下单）。
// pathWithQuery 必须与签名一致（含 query string）。
func wxRequest(method, pathWithQuery string, body []byte, out any) error {
	wxcfg.mu.RLock()
	mchid, serial := wxcfg.mchid, wxcfg.serial
	wxcfg.mu.RUnlock()
	ts := fmt.Sprintf("%d", time.Now().Unix())
	nonce := randHex(16)
	sig, err := signMessage(buildSignMessage(method, pathWithQuery, ts, nonce, string(body)))
	if err != nil {
		return err
	}
	auth := fmt.Sprintf(`WECHATPAY2-SHA256-RSA2048 mchid="%s",nonce_str="%s",timestamp="%s",serial_no="%s",signature="%s"`,
		mchid, nonce, ts, serial, sig)
	req, err := http.NewRequest(method, wxGateway+pathWithQuery, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "0brand/2.0")
	req.Header.Set("Accept", "application/json")
	resp, err := wxHTTP.Do(req)
	if err != nil {
		return err // 超时/网络错误：调用方决定处理；下单类不重试
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return errors.New("响应超过 1MB 上限")
	}
	if method != "GET" || pathWithQuery != "/v3/certificates" {
		if err := verifyNotifyHeaders(resp.Header.Get("Wechatpay-Serial"), resp.Header.Get("Wechatpay-Timestamp"), resp.Header.Get("Wechatpay-Nonce"), string(raw), resp.Header.Get("Wechatpay-Signature")); err != nil {
			return fmt.Errorf("渠道响应验签失败: %w", err)
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e wxAPIError
		if json.Unmarshal(raw, &e) != nil {
			return fmt.Errorf("微信支付 HTTP %d", resp.StatusCode)
		}
		return &e
	}

	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

type wxAPIError struct {
	Code string `json:"code"`
}

func (e *wxAPIError) Error() string { return "微信支付拒绝请求: " + e.Code }

// ---------- 4 个支付 API ----------
type wxOrderResp struct{ CodeURL, H5URL string }

func wxNativeRequest(outTradeNo string, amountCents int64, description string) []byte {
	wxcfg.mu.RLock()
	appid, mchid, notify := wxcfg.appid, wxcfg.mchid, wxcfg.notifyURL
	wxcfg.mu.RUnlock()
	body, _ := json.Marshal(map[string]any{"appid": appid, "mchid": mchid, "description": description, "out_trade_no": outTradeNo, "notify_url": notify,
		"time_expire": time.Now().Add(30 * time.Minute).Format(time.RFC3339), "amount": map[string]any{"total": amountCents, "currency": "CNY"}})
	return body
}
func wxCreateNative(outTradeNo string, amountCents int64, description string) (string, error) {
	return wxCreateNativeRequest(wxNativeRequest(outTradeNo, amountCents, description))
}
func validNativeSnapshot(body []byte, id string, cents int64) bool {
	var p struct {
		ID          string `json:"out_trade_no"`
		App         string `json:"appid"`
		Merchant    string `json:"mchid"`
		Expiry      string `json:"time_expire"`
		Description string `json:"description"`
		Amount      struct {
			Total    int64  `json:"total"`
			Currency string `json:"currency"`
		} `json:"amount"`
	}
	if json.Unmarshal(body, &p) != nil {
		return false
	}
	wxcfg.mu.RLock()
	a, m := wxcfg.appid, wxcfg.mchid
	wxcfg.mu.RUnlock()
	_, e := time.Parse(time.RFC3339, p.Expiry)
	return e == nil && p.ID == id && p.App == a && p.Merchant == m && p.Amount.Total == cents && p.Amount.Currency == "CNY" && p.Description != ""
}
func wxCreateNativeRequest(body []byte) (string, error) {
	var out struct {
		CodeURL string `json:"code_url"`
	}
	if err := wxRequest("POST", "/v3/pay/transactions/native", body, &out); err != nil {
		return "", err
	}
	if out.CodeURL == "" {
		return "", fmt.Errorf("渠道未返回支付入口")
	}
	return out.CodeURL, nil
}

func wxCreateH5(outTradeNo string, amountCents int64, description, clientIP string) (string, error) {
	wxcfg.mu.RLock()
	appid, mchid, notify := wxcfg.appid, wxcfg.mchid, wxcfg.notifyURL
	wxcfg.mu.RUnlock()
	body, _ := json.Marshal(map[string]any{
		"appid": appid, "mchid": mchid, "description": description,
		"out_trade_no": outTradeNo, "notify_url": notify,
		"time_expire": time.Now().Add(30 * time.Minute).Format(time.RFC3339),
		"amount":      map[string]any{"total": amountCents, "currency": "CNY"},
		"scene_info": map[string]any{
			"payer_client_ip": clientIP,
			"h5_info":         map[string]any{"type": "Wap", "app_name": "M-brand 品牌提案", "app_url": platform.LoadConfig().PublicOrigin},
		},
	})
	var out struct {
		H5URL string `json:"h5_url"`
	}
	if err := wxRequest("POST", "/v3/pay/transactions/h5", body, &out); err != nil {
		return "", err
	}
	if !strings.HasPrefix(out.H5URL, "https://wx.tenpay.com/") { // 防开放重定向
		return "", errors.New("h5_url 域名不在白名单")
	}
	return out.H5URL, nil
}

type wxQueryResp struct {
	OutTradeNo    string `json:"out_trade_no"`
	TradeState    string `json:"trade_state"`
	TransactionID string `json:"transaction_id"`
	Mchid         string `json:"mchid"`
	Appid         string `json:"appid"`
	Amount        struct {
		Total    int64  `json:"total"`
		Currency string `json:"currency"`
	} `json:"amount"`
}

func wxQueryOrder(outTradeNo string) (*wxQueryResp, error) {
	wxcfg.mu.RLock()
	mchid := wxcfg.mchid
	wxcfg.mu.RUnlock()
	path := "/v3/pay/transactions/out-trade-no/" + outTradeNo + "?mchid=" + mchid
	var out wxQueryResp
	if err := wxRequest("GET", path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func wxCloseOrder(outTradeNo string) error {
	wxcfg.mu.RLock()
	mchid := wxcfg.mchid
	wxcfg.mu.RUnlock()
	body, _ := json.Marshal(map[string]any{"mchid": mchid})
	return wxRequest("POST", "/v3/pay/transactions/out-trade-no/"+outTradeNo+"/close", body, nil)
}

// ---------- 平台证书自举（GET /v3/certificates，不计入 4 个支付 API）----------
func refreshPlatformCerts() error {
	if !wxReady() {
		return errors.New("凭据未配齐")
	}
	var out struct {
		Data []struct {
			SerialNo string `json:"serial_no"`
			EncCert  struct {
				Ciphertext     string `json:"ciphertext"`
				Nonce          string `json:"nonce"`
				AssociatedData string `json:"associated_data"`
			} `json:"encrypt_certificate"`
		} `json:"data"`
	}
	if err := wxRequest("GET", "/v3/certificates", nil, &out); err != nil {
		return err
	}
	wxcfg.mu.Lock()
	defer wxcfg.mu.Unlock()
	for _, d := range out.Data {
		pemBytes, err := decryptAESGCM(wxcfg.apiV3Key, d.EncCert.Nonce, d.EncCert.AssociatedData, d.EncCert.Ciphertext)
		if err != nil {
			continue
		}
		blk, _ := pem.Decode(pemBytes)
		if blk == nil {
			continue
		}
		crt, err := x509.ParseCertificate(blk.Bytes)
		if err != nil {
			continue
		}
		if !strings.EqualFold(crt.SerialNumber.Text(16), d.SerialNo) || time.Now().Before(crt.NotBefore) || time.Now().After(crt.NotAfter) {
			continue
		}
		if pk, ok := crt.PublicKey.(*rsa.PublicKey); ok {
			wxcfg.platformCerts[d.SerialNo] = pk
		}
	}
	if len(wxcfg.platformCerts) == 0 {
		return errors.New("未解析到平台证书")
	}
	return nil
}

func startPlatformCertRefresher() {
	go func() {
		for {
			if !wxReady() || wxcfg.apiV3Key == "" {
				time.Sleep(10 * time.Minute)
				continue
			}
			if err := refreshPlatformCerts(); err == nil {
				time.Sleep(12 * time.Hour) // 轮换周期刷新
			} else {
				time.Sleep(10 * time.Minute) // 未成功则退避重试
			}
		}
	}()
}

// ---------- 回调验签 ----------
// 校验：序列号匹配 + 时差±300s + RSA-SHA256 验签（Timestamp\nNonce\nbody\n）
func verifyNotifyHeaders(serialHdr, tsHdr, nonceHdr, body, sigHdr string) error {
	wxcfg.mu.RLock()
	overrideSerial := wxcfg.platformSerialOverride
	overrideKey := wxcfg.platformKeyOverride
	known := map[string]*rsa.PublicKey{}
	for k, v := range wxcfg.platformCerts {
		known[k] = v
	}
	wxcfg.mu.RUnlock()
	var pub *rsa.PublicKey
	if overrideSerial != "" {
		if serialHdr != overrideSerial { // 铁律：必须严格等于配置序列号
			return fmt.Errorf("平台证书序列号不匹配")
		}
		pub = overrideKey
	}
	if pub == nil {
		if k, ok := known[serialHdr]; ok {
			pub = k
		}
	}
	if pub == nil {
		if !wxReady() {
			return errors.New("支付凭据未配齐（503）")
		}
		return fmt.Errorf("未知平台证书序列号 %s", truncate(serialHdr, 20))
	}
	ts, err := strconv.ParseInt(tsHdr, 10, 64)
	if err != nil || nonceHdr == "" {
		return errors.New("时间戳格式无效")
	}
	if diff := time.Now().Unix() - ts; diff > 300 || diff < -300 {
		return errors.New("时间戳超出 ±300 秒")
	}
	sig, err := base64.StdEncoding.DecodeString(sigHdr)
	if err != nil {
		return errors.New("签名 base64 无效")
	}
	sum := sha256.Sum256([]byte(tsHdr + "\n" + nonceHdr + "\n" + body + "\n"))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		return errors.New("签名验证失败")
	}
	return nil
}

// ---------- AES-256-GCM（APIv3 密钥解密 resource / 平台证书）----------
func decryptAESGCM(apiV3Key, nonce, aad, ciphertextB64 string) ([]byte, error) {
	if len(apiV3Key) != 32 {
		return nil, errors.New("APIv3 密钥必须 32 字节")
	}
	ct, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher([]byte(apiV3Key))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != 12 {
		return nil, errors.New("nonce 长度必须 12 字节")
	}
	pt, err := gcm.Open(nil, []byte(nonce), ct, []byte(aad))
	if err != nil {
		return nil, errors.New("GCM 解密失败（密钥或密文不匹配）")
	}
	return pt, nil
}

// ---------- 回调解密后的交易体 ----------
type wxNotifyResource struct {
	OutTradeNo    string `json:"out_trade_no"`
	TransactionID string `json:"transaction_id"`
	Mchid         string `json:"mchid"`
	Appid         string `json:"appid"`
	Amount        struct {
		Total    int64  `json:"total"`
		Currency string `json:"currency"`
	} `json:"amount"`
}

// mock 查单：订单创建 2 秒后视为已支付（金额与本单一致）
func mockQueryOrder(outTradeNo string) *wxQueryResp {
	wxcfg.mu.RLock()
	mchid, appid := wxcfg.mchid, wxcfg.appid
	wxcfg.mu.RUnlock()
	var amountCents int64
	var created time.Time
	var status string
	err := pool.QueryRow(contextBG(),
		"SELECT amount_cents, created_at, status FROM orders WHERE id=$1", outTradeNo).
		Scan(&amountCents, &created, &status)
	if err != nil {
		return nil
	}
	q := &wxQueryResp{OutTradeNo: outTradeNo, Mchid: mchid, Appid: appid}
	q.Amount.Total = amountCents
	q.Amount.Currency = "CNY"
	q.TransactionID = "MOCKTX" + outTradeNo
	if status == "paid" {
		q.TradeState = "SUCCESS"
		return q
	}
	if time.Since(created) >= 2*time.Second {
		q.TradeState = "SUCCESS"
	} else {
		q.TradeState = "NOTPAY"
	}
	return q
}
