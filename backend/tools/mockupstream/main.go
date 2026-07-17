// mockupstream simulates Anthropic / OpenAI / Gemini endpoints for local
// end-to-end testing of the relay (streaming + usage fields included).
//
//	go run ./tools/mockupstream -port 9200
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

func main() {
	port := flag.Int("port", 9200, "listen port")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", anthropicMessages)
	mux.HandleFunc("/v1/chat/completions", openaiChat)
	mux.HandleFunc("/v1beta/models/", gemini)

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	log.Printf("mock upstream listening on http://%s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func readReq(r *http.Request) map[string]any {
	var m map[string]any
	_ = json.NewDecoder(r.Body).Decode(&m)
	return m
}

func anthropicMessages(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("x-api-key") == "" {
		http.Error(w, `{"error":{"type":"authentication_error","message":"missing x-api-key"}}`, 401)
		return
	}
	req := readReq(r)
	modelName, _ := req["model"].(string)
	stream, _ := req["stream"].(bool)

	if !stream {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"msg_mock","type":"message","role":"assistant","model":%q,"content":[{"type":"text","text":"mock reply from anthropic"}],"stop_reason":"end_turn","usage":{"input_tokens":120,"output_tokens":45,"cache_creation_input_tokens":10,"cache_read_input_tokens":30}}`, modelName)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	f := w.(http.Flusher)
	send := func(event, data string) {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		f.Flush()
		time.Sleep(20 * time.Millisecond)
	}
	send("message_start", fmt.Sprintf(`{"type":"message_start","message":{"id":"msg_mock","model":%q,"usage":{"input_tokens":120,"output_tokens":1,"cache_creation_input_tokens":10,"cache_read_input_tokens":30}}}`, modelName))
	send("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
	for _, chunk := range []string{"mock ", "stream ", "reply"} {
		send("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, chunk))
	}
	send("content_block_stop", `{"type":"content_block_stop","index":0}`)
	send("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":45}}`)
	send("message_stop", `{"type":"message_stop"}`)
}

func openaiChat(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		http.Error(w, `{"error":{"message":"missing bearer token"}}`, 401)
		return
	}
	req := readReq(r)
	modelName, _ := req["model"].(string)
	stream, _ := req["stream"].(bool)

	if !stream {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"chatcmpl-mock","object":"chat.completion","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"mock reply from openai"},"finish_reason":"stop"}],"usage":{"prompt_tokens":200,"completion_tokens":80,"prompt_tokens_details":{"cached_tokens":50}}}`, modelName)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	f := w.(http.Flusher)
	send := func(data string) {
		fmt.Fprintf(w, "data: %s\n\n", data)
		f.Flush()
		time.Sleep(20 * time.Millisecond)
	}
	for _, chunk := range []string{"mock ", "openai ", "stream"} {
		send(fmt.Sprintf(`{"id":"chatcmpl-mock","object":"chat.completion.chunk","model":%q,"choices":[{"index":0,"delta":{"content":%q}}]}`, modelName, chunk))
	}
	send(fmt.Sprintf(`{"id":"chatcmpl-mock","object":"chat.completion.chunk","model":%q,"choices":[],"usage":{"prompt_tokens":200,"completion_tokens":80,"prompt_tokens_details":{"cached_tokens":50}}}`, modelName))
	send("[DONE]")
}

func gemini(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("x-goog-api-key") == "" {
		http.Error(w, `{"error":{"code":401,"message":"missing api key"}}`, 401)
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/v1beta/models/")
	stream := strings.Contains(action, ":streamGenerateContent")

	if !stream {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"candidates":[{"content":{"parts":[{"text":"mock reply from gemini"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":150,"candidatesTokenCount":60,"thoughtsTokenCount":15,"cachedContentTokenCount":40,"totalTokenCount":225}}`)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	f := w.(http.Flusher)
	send := func(data string) {
		fmt.Fprintf(w, "data: %s\n\n", data)
		f.Flush()
		time.Sleep(20 * time.Millisecond)
	}
	send(`{"candidates":[{"content":{"parts":[{"text":"mock "}],"role":"model"}}],"usageMetadata":{"promptTokenCount":150,"candidatesTokenCount":2,"totalTokenCount":152}}`)
	send(`{"candidates":[{"content":{"parts":[{"text":"gemini stream"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":150,"candidatesTokenCount":60,"thoughtsTokenCount":15,"cachedContentTokenCount":40,"totalTokenCount":225}}`)
}
