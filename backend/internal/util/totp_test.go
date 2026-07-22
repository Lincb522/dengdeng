package util

import (
	"testing"
	"time"
)

func TestValidateTOTPRFC6238SHA1(t *testing.T) {
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	now := time.Unix(59, 0)
	if got := totpCode(secret, now.Unix()/30); got != "287082" {
		t.Fatalf("code = %q, want 287082", got)
	}
	if !ValidateTOTP(secret, "287082", now) {
		t.Fatal("known RFC vector was rejected")
	}
	if ValidateTOTP(secret, "287083", now) {
		t.Fatal("incorrect code was accepted")
	}
}
