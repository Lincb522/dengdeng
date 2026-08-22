package handler

import (
	"testing"

	"dengdeng/internal/model"
)

func TestNormalizeAccountProtocol(t *testing.T) {
	protocol, mode, err := normalizeAccountProtocol(model.PlatformKimi, "", "")
	if err != nil || protocol != model.APIProtocolAdaptive || mode != model.AccountModePayG {
		t.Fatalf("defaults = %q/%q, err=%v", protocol, mode, err)
	}
	if _, _, err := normalizeAccountProtocol(model.PlatformZhipu, model.APIProtocolResponses, model.AccountModePayG); err == nil {
		t.Fatal("zhipu responses protocol should be rejected")
	}
	if _, _, err := normalizeAccountProtocol(model.PlatformDeepSeek, model.APIProtocolResponses, model.AccountModeCoding); err != nil {
		t.Fatalf("deepseek responses should be accepted: %v", err)
	}
}
