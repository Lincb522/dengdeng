// Package payment defines provider-neutral payment primitives. Provider code
// never touches the user balance; it only creates, verifies and refunds an
// external charge. The service package owns all state transitions and credit.
package payment

import "context"

const (
	ProviderEasyPay   = "easypay"
	ProviderAlipay    = "alipay"
	ProviderWxPay     = "wxpay"
	ProviderStripe    = "stripe"
	ProviderAirwallex = "airwallex"

	MethodAlipay = "alipay"
	MethodWxPay  = "wxpay"
	MethodCard   = "card"
	MethodLink   = "link"

	StatusPending  = "pending"
	StatusPaid     = "paid"
	StatusFailed   = "failed"
	StatusRefunded = "refunded"
)

var SupportedProviders = map[string]struct{}{
	ProviderEasyPay: {}, ProviderAlipay: {}, ProviderWxPay: {}, ProviderStripe: {}, ProviderAirwallex: {},
}

type CreateRequest struct {
	OrderID       string
	AmountMinor   int64
	Currency      string
	PaymentMethod string
	Subject       string
	NotifyURL     string
	ReturnURL     string
	ClientIP      string
	IsMobile      bool
}

type CreateResponse struct {
	TradeNo      string `json:"trade_no,omitempty"`
	PayURL       string `json:"pay_url,omitempty"`
	QRCode       string `json:"qr_code,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	IntentID     string `json:"intent_id,omitempty"`
	// These fields are intentionally public client-side identifiers. They are
	// returned only with the authenticated owner's order and never expose a
	// merchant secret.
	PublishableKey string `json:"publishable_key,omitempty"`
	PaymentEnv     string `json:"payment_env,omitempty"`
	CountryCode    string `json:"country_code,omitempty"`
}

type Notification struct {
	OrderID     string
	TradeNo     string
	AmountMinor int64
	Currency    string
	Status      string
}

type QueryResponse struct {
	TradeNo     string
	AmountMinor int64
	Currency    string
	Status      string
}

type RefundResponse struct {
	RefundID string
	Status   string
}

// Provider is intentionally small. Its config is decrypted only for the
// lifetime of one operation and is not returned through any console API.
type Provider interface {
	Key() string
	Create(context.Context, CreateRequest) (*CreateResponse, error)
	Query(context.Context, string) (*QueryResponse, error)
	Verify(context.Context, []byte, map[string]string) (*Notification, error)
	Refund(context.Context, string, string, int64, string, string) (*RefundResponse, error)
	Cancel(context.Context, string) error
}

// RefundQueryProvider is implemented by channels that expose a durable refund
// status API. The service uses it to finish a previously pending refund
// without ever re-crediting the user's held balance twice.
type RefundQueryProvider interface {
	Provider
	QueryRefund(context.Context, string, string, string) (*RefundResponse, error)
}
