package provider

import "testing"

func TestValidateWxOutTradeNo(t *testing.T) {
	valid := []string{
		"ddp_1234567890123456789012345678",
		"ABCdef_-|*123",
	}
	for _, value := range valid {
		if err := validateWxOutTradeNo(value); err != nil {
			t.Fatalf("%q rejected: %v", value, err)
		}
	}
	for _, value := range []string{"short", "ddp_12345678901234567890123456789", "ddp_bad.order"} {
		if err := validateWxOutTradeNo(value); err == nil {
			t.Fatalf("%q should be rejected", value)
		}
	}
}
