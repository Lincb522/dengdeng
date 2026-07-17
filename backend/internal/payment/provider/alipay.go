package provider

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"dengdeng/internal/payment"
)

const alipayProductionGateway = "https://openapi.alipay.com/gateway.do"

// Alipay implements the official OpenAPI RSA2 flow with QR (precreate) and
// browser redirect modes. Signature verification happens before the service
// can see a payment result.
type Alipay struct {
	appID      string
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
	gateway    string
	mode       string
	client     *http.Client
}

func NewAlipay(config map[string]string) (*Alipay, error) {
	appID := strings.TrimSpace(config["appId"])
	if appID == "" {
		return nil, fmt.Errorf("alipay requires appId")
	}
	privateKey, err := parsePrivateKey(config["privateKey"])
	if err != nil {
		return nil, fmt.Errorf("alipay privateKey: %w", err)
	}
	publicValue := config["alipayPublicKey"]
	if strings.TrimSpace(publicValue) == "" {
		publicValue = config["publicKey"]
	}
	publicKey, err := parsePublicKey(publicValue)
	if err != nil {
		return nil, fmt.Errorf("alipay public key: %w", err)
	}
	gateway := strings.TrimSpace(config["gateway"])
	if gateway == "" {
		gateway = alipayProductionGateway
	}
	parsed, err := url.Parse(gateway)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, fmt.Errorf("alipay gateway must be an HTTPS URL")
	}
	return &Alipay{appID: appID, privateKey: privateKey, publicKey: publicKey, gateway: gateway, mode: strings.TrimSpace(config["paymentMode"]), client: &http.Client{Timeout: 15 * time.Second}}, nil
}
func (a *Alipay) Key() string { return payment.ProviderAlipay }

func (a *Alipay) Create(ctx context.Context, req payment.CreateRequest) (*payment.CreateResponse, error) {
	amount, err := payment.FormatAmount(req.AmountMinor, req.Currency)
	if err != nil {
		return nil, err
	}
	biz, _ := json.Marshal(map[string]string{"out_trade_no": req.OrderID, "total_amount": amount, "subject": req.Subject})
	method := "alipay.trade.precreate"
	if a.mode == "redirect" || a.mode == "wap" {
		method = "alipay.trade.wap.pay"
	}
	params := a.common(method, string(biz), req.NotifyURL, req.ReturnURL)
	if method == "alipay.trade.wap.pay" {
		params["product_code"] = "QUICK_WAP_WAY"
		signed, err := a.signedValues(params)
		if err != nil {
			return nil, err
		}
		return &payment.CreateResponse{PayURL: a.gateway + "?" + signed.Encode()}, nil
	}
	body, err := a.call(ctx, params)
	if err != nil {
		return nil, err
	}
	var response struct {
		Data struct {
			Code       string `json:"code"`
			Msg        string `json:"msg"`
			OutTradeNo string `json:"out_trade_no"`
			QRCode     string `json:"qr_code"`
		} `json:"alipay_trade_precreate_response"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("alipay create response: %w", err)
	}
	if response.Data.Code != "10000" || response.Data.QRCode == "" {
		return nil, fmt.Errorf("alipay create rejected: %s", sanitizeRemoteMessage(response.Data.Msg))
	}
	return &payment.CreateResponse{TradeNo: response.Data.OutTradeNo, QRCode: response.Data.QRCode}, nil
}

func (a *Alipay) Query(ctx context.Context, tradeNo string) (*payment.QueryResponse, error) {
	body, err := a.call(ctx, a.common("alipay.trade.query", mustJSON(map[string]string{"out_trade_no": tradeNo}), "", ""))
	if err != nil {
		return nil, err
	}
	var response struct {
		Data struct {
			Code        string `json:"code"`
			Msg         string `json:"msg"`
			OutTradeNo  string `json:"out_trade_no"`
			TradeNo     string `json:"trade_no"`
			TotalAmount string `json:"total_amount"`
			TradeStatus string `json:"trade_status"`
		} `json:"alipay_trade_query_response"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	if response.Data.Code != "10000" {
		return nil, fmt.Errorf("alipay query: %s", sanitizeRemoteMessage(response.Data.Msg))
	}
	amount, err := payment.ParseAmount(response.Data.TotalAmount, "CNY")
	if err != nil {
		return nil, err
	}
	status := payment.StatusPending
	if response.Data.TradeStatus == "TRADE_SUCCESS" || response.Data.TradeStatus == "TRADE_FINISHED" {
		status = payment.StatusPaid
	} else if response.Data.TradeStatus == "TRADE_CLOSED" {
		status = payment.StatusFailed
	}
	return &payment.QueryResponse{TradeNo: response.Data.TradeNo, AmountMinor: amount, Currency: "CNY", Status: status}, nil
}

func (a *Alipay) Verify(_ context.Context, raw []byte, _ map[string]string) (*payment.Notification, error) {
	values, err := url.ParseQuery(string(raw))
	if err != nil {
		return nil, err
	}
	sign := values.Get("sign")
	if sign == "" {
		return nil, fmt.Errorf("alipay webhook missing signature")
	}
	params := make(map[string]string, len(values))
	for key := range values {
		params[key] = values.Get(key)
	}
	if params["app_id"] != a.appID {
		return nil, fmt.Errorf("alipay webhook app id mismatch")
	}
	if !a.verify(params, sign) {
		return nil, fmt.Errorf("invalid alipay webhook signature")
	}
	amount, err := payment.ParseAmount(params["total_amount"], "CNY")
	if err != nil {
		return nil, err
	}
	status := payment.StatusFailed
	if params["trade_status"] == "TRADE_SUCCESS" || params["trade_status"] == "TRADE_FINISHED" {
		status = payment.StatusPaid
	}
	return &payment.Notification{OrderID: params["out_trade_no"], TradeNo: params["trade_no"], AmountMinor: amount, Currency: "CNY", Status: status}, nil
}

func (a *Alipay) Refund(ctx context.Context, tradeNo, orderID string, amountMinor int64, currency, _ string) (*payment.RefundResponse, error) {
	amount, err := payment.FormatAmount(amountMinor, currency)
	if err != nil {
		return nil, err
	}
	biz := map[string]string{"refund_amount": amount, "out_request_no": orderID + "-refund"}
	if tradeNo != "" {
		biz["trade_no"] = tradeNo
	} else {
		biz["out_trade_no"] = orderID
	}
	body, err := a.call(ctx, a.common("alipay.trade.refund", mustJSON(biz), "", ""))
	if err != nil {
		return nil, err
	}
	var response struct {
		Data struct {
			Code         string `json:"code"`
			Msg          string `json:"msg"`
			OutRequestNo string `json:"out_request_no"`
		} `json:"alipay_trade_refund_response"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	if response.Data.Code != "10000" {
		return nil, fmt.Errorf("alipay refund: %s", sanitizeRemoteMessage(response.Data.Msg))
	}
	return &payment.RefundResponse{RefundID: response.Data.OutRequestNo, Status: payment.StatusRefunded}, nil
}
func (a *Alipay) QueryRefund(ctx context.Context, _ string, orderID, refundID string) (*payment.RefundResponse, error) {
	body, err := a.call(ctx, a.common("alipay.trade.fastpay.refund.query", mustJSON(map[string]string{"out_trade_no": orderID, "out_request_no": refundID}), "", ""))
	if err != nil {
		return nil, err
	}
	var response struct {
		Data struct {
			Code         string `json:"code"`
			Msg          string `json:"msg"`
			RefundStatus string `json:"refund_status"`
			OutRequestNo string `json:"out_request_no"`
		} `json:"alipay_trade_fastpay_refund_query_response"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	if response.Data.Code != "10000" {
		return nil, fmt.Errorf("alipay refund query: %s", sanitizeRemoteMessage(response.Data.Msg))
	}
	status := payment.StatusPending
	if response.Data.RefundStatus == "REFUND_SUCCESS" {
		status = payment.StatusRefunded
	} else if response.Data.RefundStatus == "REFUND_FAIL" {
		status = payment.StatusFailed
	}
	if response.Data.OutRequestNo == "" {
		response.Data.OutRequestNo = refundID
	}
	return &payment.RefundResponse{RefundID: response.Data.OutRequestNo, Status: status}, nil
}
func (a *Alipay) Cancel(ctx context.Context, tradeNo string) error {
	_, err := a.call(ctx, a.common("alipay.trade.close", mustJSON(map[string]string{"out_trade_no": tradeNo}), "", ""))
	return err
}

func (a *Alipay) common(method, biz, notifyURL, returnURL string) map[string]string {
	params := map[string]string{"app_id": a.appID, "method": method, "format": "JSON", "charset": "utf-8", "sign_type": "RSA2", "timestamp": time.Now().Format("2006-01-02 15:04:05"), "version": "1.0", "biz_content": biz}
	if notifyURL != "" {
		params["notify_url"] = notifyURL
	}
	if returnURL != "" {
		params["return_url"] = returnURL
	}
	return params
}
func (a *Alipay) call(ctx context.Context, params map[string]string) ([]byte, error) {
	values, err := a.signedValues(params)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.gateway, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, easyPayMaxBody))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("alipay remote HTTP %d", resp.StatusCode)
	}
	return body, nil
}
func (a *Alipay) signedValues(params map[string]string) (url.Values, error) {
	sign, err := a.sign(params)
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	for key, value := range params {
		values.Set(key, value)
	}
	values.Set("sign", sign)
	return values, nil
}
func (a *Alipay) sign(params map[string]string) (string, error) {
	digest := sha256.Sum256([]byte(canonicalAlipay(params)))
	signature, err := rsa.SignPKCS1v15(rand.Reader, a.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(signature), nil
}
func (a *Alipay) verify(params map[string]string, signature string) bool {
	raw, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	digest := sha256.Sum256([]byte(canonicalAlipay(params)))
	return rsa.VerifyPKCS1v15(a.publicKey, crypto.SHA256, digest[:], raw) == nil
}
func canonicalAlipay(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		if key != "sign" && key != "sign_type" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+params[key])
	}
	return strings.Join(parts, "&")
}
func mustJSON(v map[string]string) string { raw, _ := json.Marshal(v); return string(raw) }
func parsePrivateKey(raw string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(raw)))
	if block == nil {
		return nil, fmt.Errorf("invalid PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("not RSA private key")
	}
	return key, nil
}
func parsePublicKey(raw string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(raw)))
	if block == nil {
		return nil, fmt.Errorf("invalid PEM")
	}
	if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
		if key, ok := cert.PublicKey.(*rsa.PublicKey); ok {
			return key, nil
		}
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("not RSA public key")
	}
	return key, nil
}
