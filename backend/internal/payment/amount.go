package payment

import (
	"fmt"
	"strings"
)

// NormalizeCurrency accepts the ISO currencies supported by the initial
// Sub2API-compatible adapters. More can be added without changing orders.
func NormalizeCurrency(raw string) (string, error) {
	currency := strings.ToUpper(strings.TrimSpace(raw))
	switch currency {
	case "CNY", "USD", "HKD", "JPY", "EUR", "GBP", "AUD", "CAD", "SGD":
		return currency, nil
	default:
		return "", fmt.Errorf("unsupported payment currency %q", raw)
	}
}

func MinorDigits(currency string) int {
	if strings.EqualFold(currency, "JPY") {
		return 0
	}
	return 2
}

func FormatAmount(minor int64, currency string) (string, error) {
	if minor <= 0 {
		return "", fmt.Errorf("payment amount must be positive")
	}
	currency, err := NormalizeCurrency(currency)
	if err != nil {
		return "", err
	}
	if MinorDigits(currency) == 0 {
		return fmt.Sprintf("%d", minor), nil
	}
	return fmt.Sprintf("%d.%02d", minor/100, minor%100), nil
}

func ParseAmount(raw, currency string) (int64, error) {
	currency, err := NormalizeCurrency(currency)
	if err != nil {
		return 0, err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "-") {
		return 0, fmt.Errorf("invalid payment amount")
	}
	parts := strings.Split(raw, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, fmt.Errorf("invalid payment amount")
	}
	var whole int64
	if _, err := fmt.Sscan(parts[0], &whole); err != nil || whole < 0 {
		return 0, fmt.Errorf("invalid payment amount")
	}
	if MinorDigits(currency) == 0 {
		if len(parts) != 1 {
			return 0, fmt.Errorf("%s does not use decimal minor units", currency)
		}
		return whole, nil
	}
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	if len(frac) > 2 {
		return 0, fmt.Errorf("amount has more than two decimals")
	}
	for len(frac) < 2 {
		frac += "0"
	}
	var cents int64
	if frac != "" {
		if _, err := fmt.Sscan(frac, &cents); err != nil || cents < 0 || cents > 99 {
			return 0, fmt.Errorf("invalid payment amount")
		}
	}
	return whole*100 + cents, nil
}
