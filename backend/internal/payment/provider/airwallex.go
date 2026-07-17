package provider

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"dengdeng/internal/payment"
)

// Airwallex implements its Payment Intent API and signed webhook contract.
// It uses a per-instance token cache: credentials from different merchants
// never share an access token.
type Airwallex struct {
	clientID, apiKey, accountID, webhookSecret, apiBase, countryCode string
	client                                                           *http.Client
	mu                                                               sync.Mutex
	token                                                            string
	tokenExpiry                                                      time.Time
}

func NewAirwallex(config map[string]string) (*Airwallex, error) {
	clientID, apiKey, webhook := strings.TrimSpace(config["clientId"]), strings.TrimSpace(config["apiKey"]), strings.TrimSpace(config["webhookSecret"])
	if clientID == "" || apiKey == "" || webhook == "" {
		return nil, fmt.Errorf("airwallex requires clientId, apiKey and webhookSecret")
	}
	base := strings.TrimRight(strings.TrimSpace(config["apiBase"]), "/")
	if base == "" {
		return nil, fmt.Errorf("airwallex requires apiBase (sandbox or production)")
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "/api/v1" {
		return nil, fmt.Errorf("airwallex apiBase must be an HTTPS /api/v1 URL")
	}
	country := strings.ToUpper(strings.TrimSpace(config["countryCode"]))
	if country == "" {
		country = "CN"
	}
	if len(country) != 2 {
		return nil, fmt.Errorf("airwallex countryCode must be an ISO two-letter code")
	}
	return &Airwallex{clientID: clientID, apiKey: apiKey, accountID: strings.TrimSpace(config["accountId"]), webhookSecret: webhook, apiBase: base, countryCode: country, client: &http.Client{Timeout: 15 * time.Second}}, nil
}
func (a *Airwallex) Key() string { return payment.ProviderAirwallex }

func (a *Airwallex) Create(ctx context.Context, req payment.CreateRequest) (*payment.CreateResponse, error) {
	amount, err := payment.FormatAmount(req.AmountMinor, req.Currency)
	if err != nil {
		return nil, err
	}
	token, err := a.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"request_id": stableRequestID("intent", req.OrderID), "amount": json.RawMessage(amount), "currency": strings.ToUpper(req.Currency), "merchant_order_id": req.OrderID, "return_url": req.ReturnURL, "metadata": map[string]string{"order_id": req.OrderID}}
	var response airwallexIntent
	if err := a.doJSON(ctx, http.MethodPost, "/pa/payment_intents/create", token, payload, &response); err != nil {
		return nil, fmt.Errorf("airwallex create: %w", err)
	}
	if response.ID == "" || response.ClientSecret == "" {
		return nil, fmt.Errorf("airwallex create returned no client secret")
	}
	env := "demo"
	if strings.Contains(strings.ToLower(a.apiBase), "api.airwallex.com") && !strings.Contains(strings.ToLower(a.apiBase), "api-demo.") {
		env = "prod"
	}
	return &payment.CreateResponse{TradeNo: response.ID, IntentID: response.ID, ClientSecret: response.ClientSecret, PaymentEnv: env, CountryCode: a.countryCode}, nil
}
func (a *Airwallex) Query(ctx context.Context, tradeNo string) (*payment.QueryResponse, error) {
	token, err := a.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	var response airwallexIntent
	if err := a.doJSON(ctx, http.MethodGet, "/pa/payment_intents/"+url.PathEscape(tradeNo), token, nil, &response); err != nil {
		return nil, err
	}
	amount, err := payment.ParseAmount(response.Amount.String(), response.Currency)
	if err != nil {
		return nil, err
	}
	status := payment.StatusPending
	if response.Status == "SUCCEEDED" {
		status = payment.StatusPaid
	} else if response.Status == "CANCELLED" || response.Status == "FAILED" {
		status = payment.StatusFailed
	}
	return &payment.QueryResponse{TradeNo: response.ID, AmountMinor: amount, Currency: response.Currency, Status: status}, nil
}
func (a *Airwallex) Verify(_ context.Context, raw []byte, headers map[string]string) (*payment.Notification, error) {
	if a.webhookSecret == "" {
		return nil, fmt.Errorf("airwallex webhookSecret is required")
	}
	timestamp, signature := strings.TrimSpace(headers["x-timestamp"]), strings.TrimSpace(headers["x-signature"])
	if timestamp == "" || signature == "" {
		return nil, fmt.Errorf("airwallex webhook missing signature headers")
	}
	millis, err := parseMillis(timestamp)
	if err != nil || time.Since(time.UnixMilli(millis)).Abs() > 5*time.Minute {
		return nil, fmt.Errorf("invalid airwallex webhook timestamp")
	}
	mac := hmac.New(sha256.New, []byte(a.webhookSecret))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write(raw)
	if !hmac.Equal([]byte(strings.ToLower(signature)), []byte(hex.EncodeToString(mac.Sum(nil)))) {
		return nil, fmt.Errorf("invalid airwallex webhook signature")
	}
	var event struct {
		Name string `json:"name"`
		Data struct {
			Object airwallexIntent `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil, err
	}
	if event.Name != "payment_intent.succeeded" && event.Name != "payment_intent.cancelled" {
		return nil, nil
	}
	amount, err := payment.ParseAmount(event.Data.Object.Amount.String(), event.Data.Object.Currency)
	if err != nil {
		return nil, err
	}
	status := payment.StatusFailed
	if event.Name == "payment_intent.succeeded" && event.Data.Object.Status == "SUCCEEDED" {
		status = payment.StatusPaid
	}
	return &payment.Notification{OrderID: event.Data.Object.MerchantOrderID, TradeNo: event.Data.Object.ID, AmountMinor: amount, Currency: event.Data.Object.Currency, Status: status}, nil
}
func (a *Airwallex) Refund(ctx context.Context, tradeNo, _ string, amountMinor int64, currency, _ string) (*payment.RefundResponse, error) {
	amount, err := payment.FormatAmount(amountMinor, currency)
	if err != nil {
		return nil, err
	}
	token, err := a.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"request_id": stableRequestID("refund", tradeNo), "payment_intent_id": tradeNo, "amount": json.RawMessage(amount), "reason": "requested_by_customer"}
	var response struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := a.doJSON(ctx, http.MethodPost, "/pa/refunds/create", token, payload, &response); err != nil {
		return nil, err
	}
	if response.ID == "" {
		return nil, fmt.Errorf("airwallex refund returned no id")
	}
	status := payment.StatusPending
	if response.Status == "SETTLED" {
		status = payment.StatusRefunded
	} else if response.Status == "FAILED" {
		status = payment.StatusFailed
	}
	return &payment.RefundResponse{RefundID: response.ID, Status: status}, nil
}
func (a *Airwallex) QueryRefund(ctx context.Context, _ string, _ string, refundID string) (*payment.RefundResponse, error) {
	token, err := a.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	var response struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := a.doJSON(ctx, http.MethodGet, "/pa/refunds/"+url.PathEscape(refundID), token, nil, &response); err != nil {
		return nil, err
	}
	if response.ID == "" {
		response.ID = refundID
	}
	status := payment.StatusPending
	if response.Status == "SETTLED" {
		status = payment.StatusRefunded
	} else if response.Status == "FAILED" {
		status = payment.StatusFailed
	}
	return &payment.RefundResponse{RefundID: response.ID, Status: status}, nil
}
func (a *Airwallex) Cancel(ctx context.Context, tradeNo string) error {
	token, err := a.accessToken(ctx)
	if err != nil {
		return err
	}
	return a.doJSON(ctx, http.MethodPost, "/pa/payment_intents/"+url.PathEscape(tradeNo)+"/cancel", token, nil, nil)
}

func (a *Airwallex) accessToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && time.Now().Add(2*time.Minute).Before(a.tokenExpiry) {
		return a.token, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.apiBase+"/authentication/login", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("x-client-id", a.clientID)
	req.Header.Set("x-api-key", a.apiKey)
	if a.accountID != "" {
		req.Header.Set("x-login-as", a.accountID)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, easyPayMaxBody))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("airwallex authentication HTTP %d: %s", resp.StatusCode, sanitizeRemoteMessage(string(body)))
	}
	var response struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.Token == "" {
		return "", fmt.Errorf("invalid airwallex authentication response")
	}
	expiry, err := time.Parse(time.RFC3339, response.ExpiresAt)
	if err != nil {
		expiry = time.Now().Add(25 * time.Minute)
	}
	a.token, a.tokenExpiry = response.Token, expiry
	return a.token, nil
}
func (a *Airwallex) doJSON(ctx context.Context, method, path, token string, payload any, target any) error {
	var reader io.Reader
	if payload != nil {
		body, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.apiBase+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if a.accountID != "" {
		req.Header.Set("x-on-behalf-of", a.accountID)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, easyPayMaxBody))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("airwallex HTTP %d: %s", resp.StatusCode, sanitizeRemoteMessage(string(body)))
	}
	if target != nil && len(bytes.TrimSpace(body)) > 0 {
		return json.Unmarshal(body, target)
	}
	return nil
}
func stableRequestID(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	raw := hex.EncodeToString(hash[:16])
	return raw[0:8] + "-" + raw[8:12] + "-4" + raw[13:16] + "-a" + raw[17:20] + "-" + raw[20:32]
}
func parseMillis(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	var n int64
	if _, err := fmt.Sscan(raw, &n); err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid timestamp")
	}
	return n, nil
}

type rawAmount json.RawMessage

func (r rawAmount) String() string { return strings.Trim(string(r), `"`) }

type airwallexIntent struct {
	ID              string    `json:"id"`
	ClientSecret    string    `json:"client_secret"`
	MerchantOrderID string    `json:"merchant_order_id"`
	Amount          rawAmount `json:"amount"`
	Currency        string    `json:"currency"`
	Status          string    `json:"status"`
}
