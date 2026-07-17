package provider

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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

const stripeAPIBase = "https://api.stripe.com/v1"

// Stripe uses the public HTTPS API directly rather than keeping a global SDK
// client, so multiple merchant instances cannot share credentials by mistake.
type Stripe struct {
	secretKey, publishableKey, webhookSecret string
	client                                   *http.Client
}

func NewStripe(config map[string]string) (*Stripe, error) {
	secret, publishable, webhook := strings.TrimSpace(config["secretKey"]), strings.TrimSpace(config["publishableKey"]), strings.TrimSpace(config["webhookSecret"])
	if secret == "" || publishable == "" || webhook == "" {
		return nil, fmt.Errorf("stripe requires secretKey, publishableKey and webhookSecret")
	}
	return &Stripe{secretKey: secret, publishableKey: publishable, webhookSecret: webhook, client: &http.Client{Timeout: 15 * time.Second}}, nil
}
func (s *Stripe) Key() string { return payment.ProviderStripe }

func (s *Stripe) Create(ctx context.Context, req payment.CreateRequest) (*payment.CreateResponse, error) {
	currency, err := payment.NormalizeCurrency(req.Currency)
	if err != nil {
		return nil, err
	}
	method := strings.TrimSpace(req.PaymentMethod)
	if method == "" {
		method = payment.MethodCard
	}
	if method == payment.MethodWxPay {
		method = "wechat_pay"
	}
	form := url.Values{}
	form.Set("amount", strconv.FormatInt(req.AmountMinor, 10))
	form.Set("currency", strings.ToLower(currency))
	form.Set("description", req.Subject)
	form.Set("metadata[order_id]", req.OrderID)
	form.Add("payment_method_types[]", method)
	body, err := s.do(ctx, http.MethodPost, "/payment_intents", form)
	if err != nil {
		return nil, fmt.Errorf("stripe create: %w", err)
	}
	var out struct {
		ID           string `json:"id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.ID == "" || out.ClientSecret == "" {
		return nil, fmt.Errorf("stripe create: invalid payment intent response")
	}
	return &payment.CreateResponse{TradeNo: out.ID, IntentID: out.ID, ClientSecret: out.ClientSecret, PublishableKey: s.publishableKey}, nil
}

func (s *Stripe) Query(ctx context.Context, tradeNo string) (*payment.QueryResponse, error) {
	body, err := s.do(ctx, http.MethodGet, "/payment_intents/"+url.PathEscape(tradeNo), nil)
	if err != nil {
		return nil, fmt.Errorf("stripe query: %w", err)
	}
	var out stripeIntent
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	status := payment.StatusPending
	if out.Status == "succeeded" {
		status = payment.StatusPaid
	} else if out.Status == "canceled" || out.Status == "requires_payment_method" {
		status = payment.StatusFailed
	}
	return &payment.QueryResponse{TradeNo: out.ID, AmountMinor: out.Amount, Currency: strings.ToUpper(out.Currency), Status: status}, nil
}

func (s *Stripe) Verify(_ context.Context, raw []byte, headers map[string]string) (*payment.Notification, error) {
	if s.webhookSecret == "" {
		return nil, fmt.Errorf("stripe webhookSecret is required")
	}
	if !verifyStripeSignature(headers["stripe-signature"], raw, s.webhookSecret, time.Now()) {
		return nil, fmt.Errorf("invalid stripe webhook signature")
	}
	var event struct {
		Type string `json:"type"`
		Data struct {
			Object stripeIntent `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil, fmt.Errorf("stripe webhook parse: %w", err)
	}
	if event.Type != "payment_intent.succeeded" && event.Type != "payment_intent.payment_failed" {
		return nil, nil
	}
	status := payment.StatusFailed
	if event.Type == "payment_intent.succeeded" {
		status = payment.StatusPaid
	}
	return &payment.Notification{OrderID: event.Data.Object.Metadata["order_id"], TradeNo: event.Data.Object.ID, AmountMinor: event.Data.Object.Amount, Currency: strings.ToUpper(event.Data.Object.Currency), Status: status}, nil
}

func (s *Stripe) Refund(ctx context.Context, tradeNo, _ string, amountMinor int64, _ string, _ string) (*payment.RefundResponse, error) {
	form := url.Values{"payment_intent": {tradeNo}, "amount": {strconv.FormatInt(amountMinor, 10)}}
	body, err := s.do(ctx, http.MethodPost, "/refunds", form)
	if err != nil {
		return nil, fmt.Errorf("stripe refund: %w", err)
	}
	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.ID == "" {
		return nil, fmt.Errorf("stripe refund: invalid response")
	}
	status := payment.StatusPending
	if out.Status == "succeeded" {
		status = payment.StatusRefunded
	} else if out.Status == "failed" || out.Status == "canceled" {
		status = payment.StatusFailed
	}
	return &payment.RefundResponse{RefundID: out.ID, Status: status}, nil
}
func (s *Stripe) QueryRefund(ctx context.Context, _ string, _ string, refundID string) (*payment.RefundResponse, error) {
	body, err := s.do(ctx, http.MethodGet, "/refunds/"+url.PathEscape(refundID), nil)
	if err != nil {
		return nil, fmt.Errorf("stripe refund query: %w", err)
	}
	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.ID == "" {
		return nil, fmt.Errorf("stripe refund query: invalid response")
	}
	status := payment.StatusPending
	if out.Status == "succeeded" {
		status = payment.StatusRefunded
	} else if out.Status == "failed" || out.Status == "canceled" {
		status = payment.StatusFailed
	}
	return &payment.RefundResponse{RefundID: out.ID, Status: status}, nil
}
func (s *Stripe) Cancel(ctx context.Context, tradeNo string) error {
	_, err := s.do(ctx, http.MethodPost, "/payment_intents/"+url.PathEscape(tradeNo)+"/cancel", url.Values{})
	return err
}

func (s *Stripe) do(ctx context.Context, method, path string, form url.Values) ([]byte, error) {
	var reader io.Reader
	if form != nil {
		reader = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, stripeAPIBase+path, reader)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(s.secretKey, "")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, easyPayMaxBody))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("remote HTTP %d: %s", resp.StatusCode, sanitizeRemoteMessage(string(body)))
	}
	return body, nil
}

type stripeIntent struct {
	ID       string            `json:"id"`
	Amount   int64             `json:"amount"`
	Currency string            `json:"currency"`
	Status   string            `json:"status"`
	Metadata map[string]string `json:"metadata"`
}

func verifyStripeSignature(raw string, body []byte, secret string, now time.Time) bool {
	var timestamp string
	var signatures []string
	for _, part := range strings.Split(raw, ",") {
		pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(pair) != 2 {
			continue
		}
		if pair[0] == "t" {
			timestamp = pair[1]
		}
		if pair[0] == "v1" {
			signatures = append(signatures, pair[1])
		}
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || now.Sub(time.Unix(seconds, 0)).Abs() > 5*time.Minute {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	for _, value := range signatures {
		if hmac.Equal([]byte(expected), []byte(value)) {
			return true
		}
	}
	return false
}
