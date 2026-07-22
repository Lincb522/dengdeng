package service

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAgentIdentityAssertionMatchesSignedEnvelope(t *testing.T) {
	material, err := generateAgentIdentityKeyMaterial()
	if err != nil {
		t.Fatal(err)
	}
	record := AgentIdentityRecord{
		AgentRuntimeID: "runtime-1", AgentPrivateKey: material.privateKeyPKCS8Base64,
		TaskID: "task-1", AccountID: "acct-1", ChatGPTUserID: "user-1",
	}
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	header, err := OpenAIAgentIdentityAuthorization(record, now)
	if err != nil {
		t.Fatal(err)
	}
	encoded := strings.TrimPrefix(header, "AgentAssertion ")
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]string
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["agent_runtime_id"] != record.AgentRuntimeID || envelope["task_id"] != record.TaskID || envelope["timestamp"] != now.Format(time.RFC3339) {
		t.Fatalf("unexpected assertion: %#v", envelope)
	}
	signature, err := base64.StdEncoding.DecodeString(envelope["signature"])
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := parseAgentIdentityPrivateKey(record.AgentPrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	message := []byte(record.AgentRuntimeID + ":" + record.TaskID + ":" + envelope["timestamp"])
	if !ed25519.Verify(privateKey.Public().(ed25519.PublicKey), message, signature) {
		t.Fatal("assertion signature is invalid")
	}
}
