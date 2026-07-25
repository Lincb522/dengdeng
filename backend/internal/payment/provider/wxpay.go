package provider

import (
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"dengdeng/internal/payment"
)

const wxPayAPIBase = "https://api.mch.weixin.qq.com"

// WxPay is a native implementation of the official WeChat Pay API v3. It
// verifies the platform signature and decrypts the AEAD resource before an
// order can be credited.
type WxPay struct {
	appID, mchID, serialNo, platformSerial, apiV3Key, apiBase, mode string
	privateKey                                                      *rsa.PrivateKey
	platformKey                                                     *rsa.PublicKey
	client                                                          *http.Client
}

func NewWxPay(config map[string]string) (*WxPay, error) {
	appID, mchID, serialNo := strings.TrimSpace(config["appId"]), strings.TrimSpace(config["mchId"]), strings.TrimSpace(config["serialNo"])
	if appID == "" || mchID == "" || serialNo == "" {
		return nil, fmt.Errorf("wxpay requires appId, mchId and serialNo")
	}
	privateKey, err := parsePrivateKey(config["privateKey"])
	if err != nil {
		return nil, fmt.Errorf("wxpay privateKey: %w", err)
	}
	publicValue := config["platformCert"]
	if strings.TrimSpace(publicValue) == "" {
		publicValue = config["platformPublicKey"]
	}
	platformKey, err := parsePublicKey(publicValue)
	if err != nil {
		return nil, fmt.Errorf("wxpay platform certificate: %w", err)
	}
	apiKey := config["apiV3Key"]
	if len(apiKey) != 32 {
		return nil, fmt.Errorf("wxpay apiV3Key must be 32 bytes")
	}
	base := strings.TrimSpace(config["apiBase"])
	if base == "" {
		base = wxPayAPIBase
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, fmt.Errorf("wxpay apiBase must be an HTTPS URL")
	}
	platformSerial := strings.TrimSpace(config["platformPublicKeyId"])
	if platformSerial == "" {
		platformSerial = strings.TrimSpace(config["publicKeyId"])
	}
	if platformSerial == "" {
		platformSerial = strings.TrimSpace(config["platformSerialNo"])
	}
	return &WxPay{appID: appID, mchID: mchID, serialNo: serialNo, platformSerial: platformSerial, apiV3Key: apiKey, apiBase: strings.TrimRight(base, "/"), mode: strings.TrimSpace(config["paymentMode"]), privateKey: privateKey, platformKey: platformKey, client: &http.Client{Timeout: 15 * time.Second}}, nil
}
func (w *WxPay) Key() string { return payment.ProviderWxPay }

func (w *WxPay) Create(ctx context.Context, req payment.CreateRequest) (*payment.CreateResponse, error) {
	if !strings.EqualFold(req.Currency, "CNY") {
		return nil, fmt.Errorf("wxpay only supports CNY")
	}
	if err := validateWxOutTradeNo(req.OrderID); err != nil {
		return nil, err
	}
	payload := map[string]any{"appid": w.appID, "mchid": w.mchID, "description": req.Subject, "out_trade_no": req.OrderID, "notify_url": req.NotifyURL, "amount": map[string]any{"total": req.AmountMinor, "currency": "CNY"}}
	path := "/v3/pay/transactions/native"
	if w.mode == "h5" {
		if req.ClientIP == "" {
			return nil, fmt.Errorf("wxpay H5 requires client IP")
		}
		payload["scene_info"] = map[string]any{"payer_client_ip": req.ClientIP}
		path = "/v3/pay/transactions/h5"
	}
	body, err := w.request(ctx, http.MethodPost, path, payload)
	if err != nil {
		return nil, fmt.Errorf("wxpay create: %w", err)
	}
	var response struct {
		CodeURL  string `json:"code_url"`
		H5URL    string `json:"h5_url"`
		PrepayID string `json:"prepay_id"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	if response.CodeURL == "" && response.H5URL == "" && response.PrepayID == "" {
		return nil, fmt.Errorf("wxpay create returned no checkout data")
	}
	return &payment.CreateResponse{TradeNo: req.OrderID, QRCode: response.CodeURL, PayURL: response.H5URL, IntentID: response.PrepayID}, nil
}

// validateWxOutTradeNo mirrors the API v3 merchant-order requirement so an
// invalid identifier fails locally instead of becoming an opaque remote 400.
func validateWxOutTradeNo(value string) error {
	if len(value) < 6 || len(value) > 32 {
		return fmt.Errorf("wxpay out_trade_no must be 6-32 characters")
	}
	for _, char := range value {
		if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || char == '_' || char == '-' || char == '|' || char == '*' {
			continue
		}
		return fmt.Errorf("wxpay out_trade_no contains unsupported characters")
	}
	return nil
}

func (w *WxPay) Query(ctx context.Context, tradeNo string) (*payment.QueryResponse, error) {
	body, err := w.request(ctx, http.MethodGet, "/v3/pay/transactions/out-trade-no/"+url.PathEscape(tradeNo)+"?mchid="+url.QueryEscape(w.mchID), nil)
	if err != nil {
		return nil, fmt.Errorf("wxpay query: %w", err)
	}
	var response wxPayTransaction
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	status := payment.StatusPending
	if response.TradeState == "SUCCESS" {
		status = payment.StatusPaid
	} else if response.TradeState == "CLOSED" || response.TradeState == "PAYERROR" {
		status = payment.StatusFailed
	}
	return &payment.QueryResponse{TradeNo: response.TransactionID, AmountMinor: response.Amount.Total, Currency: response.Amount.Currency, Status: status}, nil
}

func (w *WxPay) Verify(_ context.Context, raw []byte, headers map[string]string) (*payment.Notification, error) {
	timestamp, nonce, signature := headers["wechatpay-timestamp"], headers["wechatpay-nonce"], headers["wechatpay-signature"]
	if timestamp == "" || nonce == "" || signature == "" {
		return nil, fmt.Errorf("wxpay webhook missing signature headers")
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || time.Since(time.Unix(seconds, 0)).Abs() > 5*time.Minute {
		return nil, fmt.Errorf("wxpay webhook timestamp outside tolerance")
	}
	if !verifyWxSignature(w.platformKey, timestamp+"\n"+nonce+"\n"+string(raw)+"\n", signature) {
		return nil, fmt.Errorf("invalid wxpay webhook signature")
	}
	var envelope struct {
		Resource struct {
			Algorithm      string `json:"algorithm"`
			Nonce          string `json:"nonce"`
			AssociatedData string `json:"associated_data"`
			Ciphertext     string `json:"ciphertext"`
		} `json:"resource"`
		EventType string `json:"event_type"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	if envelope.EventType != "TRANSACTION.SUCCESS" {
		return nil, nil
	}
	plain, err := decryptWxResource(w.apiV3Key, envelope.Resource.Nonce, envelope.Resource.AssociatedData, envelope.Resource.Ciphertext)
	if err != nil {
		return nil, err
	}
	var transaction wxPayTransaction
	if err := json.Unmarshal(plain, &transaction); err != nil {
		return nil, err
	}
	if transaction.MchID != "" && transaction.MchID != w.mchID {
		return nil, fmt.Errorf("wxpay webhook merchant mismatch")
	}
	status := payment.StatusFailed
	if transaction.TradeState == "SUCCESS" {
		status = payment.StatusPaid
	}
	return &payment.Notification{OrderID: transaction.OutTradeNo, TradeNo: transaction.TransactionID, AmountMinor: transaction.Amount.Total, Currency: transaction.Amount.Currency, Status: status}, nil
}

func (w *WxPay) Refund(ctx context.Context, tradeNo, orderID string, amountMinor int64, currency, _ string) (*payment.RefundResponse, error) {
	if !strings.EqualFold(currency, "CNY") {
		return nil, fmt.Errorf("wxpay only supports CNY")
	}
	payload := map[string]any{"out_refund_no": orderID + "-refund", "amount": map[string]any{"refund": amountMinor, "total": amountMinor, "currency": "CNY"}}
	if tradeNo != "" {
		payload["transaction_id"] = tradeNo
	} else {
		payload["out_trade_no"] = orderID
	}
	body, err := w.request(ctx, http.MethodPost, "/v3/refund/domestic/refunds", payload)
	if err != nil {
		return nil, fmt.Errorf("wxpay refund: %w", err)
	}
	var response struct {
		RefundID string `json:"refund_id"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.RefundID == "" {
		return nil, fmt.Errorf("wxpay refund invalid response")
	}
	status := payment.StatusPending
	if response.Status == "SUCCESS" {
		status = payment.StatusRefunded
	} else if response.Status == "ABNORMAL" || response.Status == "CLOSED" {
		status = payment.StatusFailed
	}
	return &payment.RefundResponse{RefundID: response.RefundID, Status: status}, nil
}
func (w *WxPay) QueryRefund(ctx context.Context, _ string, _ string, refundID string) (*payment.RefundResponse, error) {
	body, err := w.request(ctx, http.MethodGet, "/v3/refund/domestic/refunds/"+url.PathEscape(refundID), nil)
	if err != nil {
		return nil, err
	}
	var response struct {
		RefundID string `json:"refund_id"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	if response.RefundID == "" {
		response.RefundID = refundID
	}
	status := payment.StatusPending
	if response.Status == "SUCCESS" {
		status = payment.StatusRefunded
	} else if response.Status == "ABNORMAL" || response.Status == "CLOSED" {
		status = payment.StatusFailed
	}
	return &payment.RefundResponse{RefundID: response.RefundID, Status: status}, nil
}
func (w *WxPay) Cancel(ctx context.Context, tradeNo string) error {
	_, err := w.request(ctx, http.MethodPost, "/v3/pay/transactions/out-trade-no/"+url.PathEscape(tradeNo)+"/close", map[string]string{"mchid": w.mchID})
	return err
}

// CreateMerchantTransfer implements the current WeChat Pay "merchant
// transfer" API. The caller owns idempotency and must query the same out bill
// number after a transport/API error instead of creating a new one.
func (w *WxPay) CreateMerchantTransfer(ctx context.Context, req payment.MerchantTransferRequest) (*payment.MerchantTransferResponse, error) {
	if len(req.OutBillNo) < 6 || len(req.OutBillNo) > 32 {
		return nil, fmt.Errorf("wxpay out_bill_no must be 6-32 characters")
	}
	for _, char := range req.OutBillNo {
		if (char < '0' || char > '9') && (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') {
			return nil, fmt.Errorf("wxpay out_bill_no must be alphanumeric")
		}
	}
	if strings.TrimSpace(req.OpenID) == "" || req.AmountMinor <= 0 || strings.TrimSpace(req.SceneID) == "" || strings.TrimSpace(req.Remark) == "" || len(req.SceneReportInfo) == 0 {
		return nil, fmt.Errorf("wxpay transfer requires openid, amount, scene, remark and scene report info")
	}
	payload := map[string]any{
		"appid": w.appID, "out_bill_no": req.OutBillNo, "transfer_scene_id": req.SceneID,
		"openid": req.OpenID, "transfer_amount": req.AmountMinor, "transfer_remark": req.Remark,
		"transfer_scene_report_infos": req.SceneReportInfo,
	}
	if req.NotifyURL != "" {
		payload["notify_url"] = req.NotifyURL
	}
	if req.UserPerception != "" {
		payload["user_recv_perception"] = req.UserPerception
	}
	body, err := w.request(ctx, http.MethodPost, "/v3/fund-app/mch-transfer/transfer-bills", payload)
	if err != nil {
		return nil, fmt.Errorf("wxpay merchant transfer: %w", err)
	}
	var response struct {
		OutBillNo      string `json:"out_bill_no"`
		TransferBillNo string `json:"transfer_bill_no"`
		State          string `json:"state"`
		PackageInfo    string `json:"package_info"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("wxpay merchant transfer response: %w", err)
	}
	return &payment.MerchantTransferResponse{OutBillNo: response.OutBillNo, ProviderBillNo: response.TransferBillNo, State: response.State, PackageInfo: response.PackageInfo, AmountMinor: req.AmountMinor, OpenID: req.OpenID, AppID: w.appID, MerchantID: w.mchID}, nil
}

func (w *WxPay) QueryMerchantTransfer(ctx context.Context, outBillNo string) (*payment.MerchantTransferResponse, error) {
	body, err := w.request(ctx, http.MethodGet, "/v3/fund-app/mch-transfer/transfer-bills/out-bill-no/"+url.PathEscape(outBillNo), nil)
	if err != nil {
		return nil, fmt.Errorf("wxpay merchant transfer query: %w", err)
	}
	var response struct {
		OutBillNo      string `json:"out_bill_no"`
		TransferBillNo string `json:"transfer_bill_no"`
		State          string `json:"state"`
		TransferAmount int64  `json:"transfer_amount"`
		FailReason     string `json:"fail_reason"`
		OpenID         string `json:"openid"`
		AppID          string `json:"appid"`
		MchID          string `json:"mch_id"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("wxpay merchant transfer query response: %w", err)
	}
	return &payment.MerchantTransferResponse{OutBillNo: response.OutBillNo, ProviderBillNo: response.TransferBillNo, State: response.State, FailureReason: response.FailReason, AmountMinor: response.TransferAmount, OpenID: response.OpenID, AppID: response.AppID, MerchantID: response.MchID}, nil
}

func (w *WxPay) VerifyMerchantTransfer(_ context.Context, raw []byte, headers map[string]string) (*payment.MerchantTransferResponse, error) {
	timestamp, nonce, signature := headers["wechatpay-timestamp"], headers["wechatpay-nonce"], headers["wechatpay-signature"]
	if timestamp == "" || nonce == "" || signature == "" {
		return nil, fmt.Errorf("wxpay transfer webhook missing signature headers")
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || time.Since(time.Unix(seconds, 0)).Abs() > 5*time.Minute {
		return nil, fmt.Errorf("wxpay transfer webhook timestamp outside tolerance")
	}
	if !verifyWxSignature(w.platformKey, timestamp+"\n"+nonce+"\n"+string(raw)+"\n", signature) {
		return nil, fmt.Errorf("invalid wxpay transfer webhook signature")
	}
	var envelope struct {
		EventType string `json:"event_type"`
		Resource  struct {
			Nonce          string `json:"nonce"`
			AssociatedData string `json:"associated_data"`
			Ciphertext     string `json:"ciphertext"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	if envelope.EventType != "MCHTRANSFER.BILL.FINISHED" {
		return nil, nil
	}
	plain, err := decryptWxResource(w.apiV3Key, envelope.Resource.Nonce, envelope.Resource.AssociatedData, envelope.Resource.Ciphertext)
	if err != nil {
		return nil, err
	}
	var result struct {
		OutBillNo      string `json:"out_bill_no"`
		TransferBillNo string `json:"transfer_bill_no"`
		State          string `json:"state"`
		MchID          string `json:"mch_id"`
		TransferAmount int64  `json:"transfer_amount"`
		OpenID         string `json:"openid"`
		FailReason     string `json:"fail_reason"`
	}
	if err := json.Unmarshal(plain, &result); err != nil {
		return nil, err
	}
	if result.MchID != "" && result.MchID != w.mchID {
		return nil, fmt.Errorf("wxpay transfer webhook merchant mismatch")
	}
	return &payment.MerchantTransferResponse{OutBillNo: result.OutBillNo, ProviderBillNo: result.TransferBillNo, State: result.State, AmountMinor: result.TransferAmount, OpenID: result.OpenID, FailureReason: result.FailReason, MerchantID: result.MchID}, nil
}

func (w *WxPay) request(ctx context.Context, method, path string, payload any) ([]byte, error) {
	body := []byte{}
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return nil, err
		}
	}
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	nonce := randomNonce()
	message := method + "\n" + path + "\n" + timestamp + "\n" + nonce + "\n" + string(body) + "\n"
	digest := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, w.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return nil, err
	}
	authorization := fmt.Sprintf("WECHATPAY2-SHA256-RSA2048 mchid=\"%s\",nonce_str=\"%s\",timestamp=\"%s\",serial_no=\"%s\",signature=\"%s\"", w.mchID, nonce, timestamp, w.serialNo, base64.StdEncoding.EncodeToString(signature))
	req, err := http.NewRequestWithContext(ctx, method, w.apiBase+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if w.platformSerial != "" {
		req.Header.Set("Wechatpay-Serial", w.platformSerial)
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	result, err := io.ReadAll(io.LimitReader(resp.Body, easyPayMaxBody))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("remote HTTP %d: %s", resp.StatusCode, sanitizeRemoteMessage(string(result)))
	}
	responseTimestamp := resp.Header.Get("Wechatpay-Timestamp")
	responseNonce := resp.Header.Get("Wechatpay-Nonce")
	responseSignature := resp.Header.Get("Wechatpay-Signature")
	if responseTimestamp == "" || responseNonce == "" || responseSignature == "" {
		return nil, fmt.Errorf("wxpay response missing signature headers")
	}
	if !verifyWxSignature(w.platformKey, responseTimestamp+"\n"+responseNonce+"\n"+string(result)+"\n", responseSignature) {
		return nil, fmt.Errorf("invalid wxpay response signature")
	}
	return result, nil
}
func verifyWxSignature(key *rsa.PublicKey, message, signature string) bool {
	raw, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	digest := sha256.Sum256([]byte(message))
	return rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], raw) == nil
}
func decryptWxResource(key, nonce, associated, ciphertext string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, []byte(nonce), raw, []byte(associated))
}
func randomNonce() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

type wxPayTransaction struct {
	MchID         string `json:"mchid"`
	OutTradeNo    string `json:"out_trade_no"`
	TransactionID string `json:"transaction_id"`
	TradeState    string `json:"trade_state"`
	Amount        struct {
		Total    int64  `json:"total"`
		Currency string `json:"currency"`
	} `json:"amount"`
}
