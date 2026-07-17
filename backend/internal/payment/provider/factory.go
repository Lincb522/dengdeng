package provider

import (
	"fmt"

	"dengdeng/internal/payment"
)

// New creates a concrete adapter from decrypted instance configuration.
func New(key string, config map[string]string) (payment.Provider, error) {
	switch key {
	case payment.ProviderEasyPay:
		return NewEasyPay(config)
	case payment.ProviderStripe:
		return NewStripe(config)
	case payment.ProviderAlipay:
		return NewAlipay(config)
	case payment.ProviderWxPay:
		return NewWxPay(config)
	case payment.ProviderAirwallex:
		return NewAirwallex(config)
	default:
		return nil, fmt.Errorf("unsupported payment provider %q", key)
	}
}
