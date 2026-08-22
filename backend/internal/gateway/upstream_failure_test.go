package gateway

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestClassifyUpstreamFailureSeparatesAccountModelAndRequest(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		retry  bool
		scope  upstreamFailureScope
	}{
		{name: "credential", status: 401, body: `{"error":"invalid token"}`, retry: true, scope: failureAccount},
		{name: "balance", status: 402, body: `{"error":"insufficient balance"}`, retry: true, scope: failureAccount},
		{name: "model permission", status: 403, body: `{"error":"model is not supported"}`, retry: true, scope: failureModel},
		{name: "account permission", status: 403, body: `{"error":"workspace forbidden"}`, retry: true, scope: failureAccount},
		{name: "client payload", status: 400, body: `{"error":"invalid input_text"}`, retry: false, scope: failureModel},
		{name: "capability", status: 400, body: `{"error":"unsupported model"}`, retry: true, scope: failureModel},
		{name: "rate limit", status: 429, body: `{"error":"rate limited"}`, retry: true, scope: failureAccount},
		{name: "transient", status: 502, body: `bad gateway`, retry: true, scope: failureModel},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classifyUpstreamFailure(test.status, []byte(test.body))
			if got.Retry != test.retry || got.Scope != test.scope {
				t.Fatalf("decision = %#v, want retry=%v scope=%d", got, test.retry, test.scope)
			}
		})
	}
}

func TestPreflightSuccessfulJSONPromotesEncodedOverload(t *testing.T) {
	response := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"server_is_overloaded"}}`)),
	}
	got, err := preflightSuccessfulJSON(response)
	if err != nil {
		t.Fatal(err)
	}
	if got.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", got.StatusCode)
	}
	body, _ := io.ReadAll(got.Body)
	if !strings.Contains(string(body), "server_is_overloaded") {
		t.Fatalf("body was not restored: %s", body)
	}
}

func TestPreflightSuccessfulJSONKeepsNormalPayload(t *testing.T) {
	response := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`)),
	}
	got, err := preflightSuccessfulJSON(response)
	if err != nil || got.StatusCode != http.StatusOK {
		t.Fatalf("response = %#v, %v", got, err)
	}
}

func TestPreflightSuccessfulSSEPromotesErrorBeforeOutput(t *testing.T) {
	response := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.created\"}\n\n" +
				"data: {\"type\":\"error\",\"error\":{\"message\":\"server_is_overloaded\"}}\n\n",
		)),
	}
	got, err := preflightSuccessfulResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	if got.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", got.StatusCode)
	}
}

func TestPreflightSuccessfulSSERestoresFirstOutput(t *testing.T) {
	stream := "data: {\"type\":\"response.created\"}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"
	response := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(stream)),
	}
	got, err := preflightSuccessfulResponse(response)
	if err != nil || got.StatusCode != http.StatusOK {
		t.Fatalf("response = %#v, %v", got, err)
	}
	body, _ := io.ReadAll(got.Body)
	if string(body) != stream {
		t.Fatalf("stream was not restored: %q", body)
	}
}
