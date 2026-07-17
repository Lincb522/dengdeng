package provider

import (
	"context"
	"crypto/hmac"
	"crypto/md5" // EasyPay's signed protocol is MD5; this is not used for password storage.
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"dengdeng/internal/payment"
)

const easyPayMaxBody = 1 << 20

// EasyPay supports the common submit.php/mapi.php aggregator protocol. Each
// merchant account remains an independent encrypted provider instance.
type EasyPay struct {
	pid      string
	pkey     string
	apiBase  string
	mode     string
	currency string
	client   *http.Client
}

func NewEasyPay(config map[string]string) (*EasyPay, error) {
	pid := strings.TrimSpace(config["pid"])
	pkey := strings.TrimSpace(config["pkey"])
	base := normalizeBase(config["apiBase"])
	if pid == "" || pkey == "" || base == "" {
		return nil, fmt.Errorf("easypay requires pid, pkey and apiBase")
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, fmt.Errorf("easypay apiBase must be an HTTPS URL")
	}
	currency := strings.ToUpper(strings.TrimSpace(config["currency"]))
	if currency == "" {
		currency = "CNY"
	}
	if _, err := payment.NormalizeCurrency(currency); err != nil {
		return nil, err
	}
	return &EasyPay{pid: pid, pkey: pkey, apiBase: base, mode: strings.TrimSpace(config["paymentMode"]), currency: currency, client: &http.Client{Timeout: 12 * time.Second}}, nil
}

func (e *EasyPay) Key() string { return payment.ProviderEasyPay }

func (e *EasyPay) Create(ctx context.Context, req payment.CreateRequest) (*payment.CreateResponse, error) {
	amount, err := payment.FormatAmount(req.AmountMinor, req.Currency)
	if err != nil {
		return nil, err
	}
	params := map[string]string{
		"pid": e.pid, "type": req.PaymentMethod, "out_trade_no": req.OrderID,
		"notify_url": req.NotifyURL, "return_url": req.ReturnURL, "name": req.Subject, "money": amount,
	}
	if req.ClientIP != "" {
		params["clientip"] = req.ClientIP
	}
	if req.IsMobile {
		params["device"] = "mobile"
	}
	params["sign"] = easyPaySign(params, e.pkey)
	params["sign_type"] = "MD5"
	if e.mode == "popup" {
		return &payment.CreateResponse{PayURL: e.apiBase + "/submit.php?" + encode(params)}, nil
	}
	body, err := e.post(ctx, "/mapi.php", params)
	if err != nil {
		return nil, fmt.Errorf("easypay create: %w", err)
	}
	var out struct {
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
		TradeNo string `json:"trade_no"`
		PayURL  string `json:"payurl"`
		PayURL2 string `json:"payurl2"`
		QRCode  string `json:"qrcode"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("easypay create response: %w", err)
	}
	if out.Code != 1 {
		return nil, fmt.Errorf("easypay rejected payment: %s", sanitizeRemoteMessage(out.Msg))
	}
	payURL := out.PayURL
	if req.IsMobile && out.PayURL2 != "" {
		payURL = out.PayURL2
	}
	return &payment.CreateResponse{TradeNo: out.TradeNo, PayURL: payURL, QRCode: out.QRCode}, nil
}

func (e *EasyPay) Query(ctx context.Context, tradeNo string) (*payment.QueryResponse, error) {
	body, err := e.post(ctx, "/api.php", map[string]string{"act": "order", "pid": e.pid, "key": e.pkey, "trade_no": tradeNo})
	if err != nil {
		return nil, fmt.Errorf("easypay query: %w", err)
	}
	var out struct {
		TradeStatus string `json:"trade_status"`
		Status      int    `json:"status"`
		Money       string `json:"money"`
		TradeNo     string `json:"trade_no"`
		Data        struct {
			TradeStatus string `json:"trade_status"`
			Status      int    `json:"status"`
			Money       string `json:"money"`
			TradeNo     string `json:"trade_no"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("easypay query response: %w", err)
	}
	if out.Money == "" {
		out.Money = out.Data.Money
	}
	if out.TradeNo == "" {
		out.TradeNo = out.Data.TradeNo
	}
	status := payment.StatusPending
	if out.TradeStatus == "TRADE_SUCCESS" || out.Data.TradeStatus == "TRADE_SUCCESS" || out.Status == 1 || out.Data.Status == 1 {
		status = payment.StatusPaid
	}
	amount, err := payment.ParseAmount(out.Money, e.currency)
	if err != nil {
		return nil, err
	}
	return &payment.QueryResponse{TradeNo: out.TradeNo, AmountMinor: amount, Currency: e.currency, Status: status}, nil
}

func (e *EasyPay) Verify(_ context.Context, raw []byte, _ map[string]string) (*payment.Notification, error) {
	values, err := url.ParseQuery(string(raw))
	if err != nil {
		return nil, err
	}
	params := make(map[string]string, len(values))
	for key := range values {
		params[key] = values.Get(key)
	}
	if params["sign"] == "" || !hmac.Equal([]byte(easyPaySign(params, e.pkey)), []byte(params["sign"])) {
		return nil, fmt.Errorf("invalid easypay webhook signature")
	}
	amount, err := payment.ParseAmount(params["money"], e.currency)
	if err != nil {
		return nil, err
	}
	status := payment.StatusFailed
	if params["trade_status"] == "TRADE_SUCCESS" {
		status = payment.StatusPaid
	}
	return &payment.Notification{OrderID: params["out_trade_no"], TradeNo: params["trade_no"], AmountMinor: amount, Currency: e.currency, Status: status}, nil
}

func (e *EasyPay) Refund(ctx context.Context, tradeNo, orderID string, amountMinor int64, currency, _ string) (*payment.RefundResponse, error) {
	amount, err := payment.FormatAmount(amountMinor, currency)
	if err != nil {
		return nil, err
	}
	params := map[string]string{"pid": e.pid, "key": e.pkey, "money": amount}
	if orderID != "" {
		params["out_trade_no"] = orderID
	} else {
		params["trade_no"] = tradeNo
	}
	body, err := e.post(ctx, "/api.php?act=refund", params)
	if err != nil {
		return nil, fmt.Errorf("easypay refund: %w", err)
	}
	var out struct {
		Code any    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("easypay refund response: %w", err)
	}
	ok := false
	switch code := out.Code.(type) {
	case float64:
		ok = int(code) == 1
	case string:
		ok = strings.TrimSpace(code) == "1"
	}
	if !ok {
		return nil, fmt.Errorf("easypay refund rejected: %s", sanitizeRemoteMessage(out.Msg))
	}
	if orderID == "" {
		orderID = tradeNo
	}
	return &payment.RefundResponse{RefundID: orderID, Status: payment.StatusRefunded}, nil
}

func (e *EasyPay) Cancel(context.Context, string) error { return nil } // aggregator protocol has no portable cancel endpoint.

// EasyPay's refund status endpoint is not standardized across aggregators;
// operators can still settle a successful synchronous refund immediately.
func (e *EasyPay) QueryRefund(context.Context, string, string, string) (*payment.RefundResponse, error) {
	return nil, fmt.Errorf("easypay does not expose a portable refund query API")
}

func (e *EasyPay) post(ctx context.Context, endpoint string, params map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.apiBase+endpoint, strings.NewReader(encode(params)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, easyPayMaxBody))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("remote HTTP %d", resp.StatusCode)
	}
	return body, nil
}

func normalizeBase(raw string) string {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	for _, suffix := range []string{"/submit.php", "/mapi.php", "/api.php"} {
		if strings.HasSuffix(strings.ToLower(base), suffix) {
			return strings.TrimSuffix(base, base[len(base)-len(suffix):])
		}
	}
	return base
}

func encode(params map[string]string) string {
	values := url.Values{}
	for key, value := range params {
		values.Set(key, value)
	}
	return values.Encode()
}
func easyPaySign(params map[string]string, key string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k != "sign" && k != "sign_type" && v != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+params[k])
	}
	sum := md5.Sum([]byte(strings.Join(parts, "&") + key))
	return hex.EncodeToString(sum[:])
}
func sanitizeRemoteMessage(v string) string {
	v = strings.Join(strings.Fields(v), " ")
	if len(v) > 200 {
		return v[:200]
	}
	return v
}

var _ = strconv.IntSize
