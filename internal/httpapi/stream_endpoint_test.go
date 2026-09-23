package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

func TestStreamingResponsesEndpointEmitsResponsesEvents(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer,
			"data: {\"id\":\"chatcmpl-stream\",\"model\":\"cline-pass/test\",\"choices\":[{\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n"+
				"data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":null}]}\n\n"+
				"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":3,\"total_tokens\":14,\"cost\":0.0002,\"completion_tokens_details\":{\"reasoning_tokens\":1}}}\n\n"+
				"data: [DONE]\n\n")
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case <-request.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.Accounts = []model.Account{{Name: "main", Key: "cline-key", Enabled: true}}
		config.KnownModels = []string{"cline-pass/test"}
	}); err != nil {
		t.Fatal(err)
	}

	request := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
	  "model":"cline-pass/test","input":"hello","stream":true,"reasoning":{"effort":"high"}
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("streaming responses request failed: %d %s", response.Code, response.Body.String())
	}
	stream := response.Body.String()
	for _, expected := range []string{
		"event: response.created", "event: response.output_text.delta", "event: response.completed",
	} {
		if !strings.Contains(stream, expected) {
			t.Fatalf("missing %q in stream: %s", expected, stream)
		}
	}
	history := st.Metadata().History
	if len(history) != 1 || history[0].Error != nil {
		t.Fatalf("completed stream should not record cancellation as an error: %#v", history)
	}
	if history[0].Kind != "responses" || history[0].FinishReason != "stop" || history[0].Effort != "high" || history[0].RequestedEffort != "high" {
		t.Fatalf("unexpected stream history metadata: %#v", history[0])
	}
	if history[0].TTFTMs < 1 {
		t.Fatalf("expected first-token latency, got %d", history[0].TTFTMs)
	}
	if history[0].Usage == nil || history[0].Usage.PromptTokens != 11 || history[0].Usage.CompletionTokens != 3 || history[0].Usage.ReasoningTokens != 1 {
		t.Fatalf("usage was not recorded: %#v", history[0].Usage)
	}
}

func TestStreamingResponsesReplaysBufferedCompletion(t *testing.T) {
	upstreamServer := newBufferedCompletionUpstream(t)
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.Accounts = []model.Account{{Name: "main", Key: "cline-key", Enabled: true}}
		config.KnownModels = []string{"cline-pass/test"}
	}); err != nil {
		t.Fatal(err)
	}

	request := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
	  "model":"cline-pass/test","input":"hello","stream":true
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("buffered upstream should not fail the stream: %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("client asked for SSE: %#v", response.Header())
	}
	if response.Header().Get("X-Cline-Actual-Upstream") != "vendor" {
		t.Fatalf("routing from the buffered body should be exposed: %#v", response.Header())
	}
	stream := response.Body.String()
	for _, expected := range []string{
		"event: response.created", "event: response.reasoning_summary_text.delta",
		"\"delta\":\"Buffered OK\"", "event: response.completed", "\"id\":\"resp_buffered\"",
	} {
		if !strings.Contains(stream, expected) {
			t.Fatalf("missing %q in synthesized stream: %s", expected, stream)
		}
	}
	history := st.Metadata().History
	if len(history) != 1 || history[0].Error != nil {
		t.Fatalf("buffered completion should be recorded as a success: %#v", history)
	}
	if history[0].Kind != "responses" || !history[0].Stream || history[0].FinishReason != "stop" || history[0].Provider != "vendor" {
		t.Fatalf("unexpected history metadata: %#v", history[0])
	}
	if history[0].Usage == nil || history[0].Usage.PromptTokens != 7 || history[0].Usage.CompletionTokens != 2 {
		t.Fatalf("usage was not recorded: %#v", history[0].Usage)
	}
	if len(history[0].Trace) != 1 || history[0].Trace[0].Note != "buffered completion" {
		t.Fatalf("trace should mark the buffered fallback: %#v", history[0].Trace)
	}
}

func TestStreamingChatSynthesizesChunkFromBufferedCompletion(t *testing.T) {
	upstreamServer := newBufferedCompletionUpstream(t)
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.Accounts = []model.Account{{Name: "main", Key: "cline-key", Enabled: true}}
		config.KnownModels = []string{"cline-pass/test"}
	}); err != nil {
		t.Fatal(err)
	}

	request := localRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
	  "model":"cline-pass/test","messages":[{"role":"user","content":"hello"}],"stream":true
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("buffered upstream should not fail the stream: %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("client asked for SSE: %#v", response.Header())
	}
	stream := response.Body.String()
	if !strings.HasSuffix(stream, "data: [DONE]\n\n") {
		t.Fatalf("synthesized stream must terminate with [DONE]: %q", stream)
	}
	var chunk map[string]any
	payload := strings.TrimPrefix(strings.SplitN(stream, "\n\n", 2)[0], "data: ")
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		t.Fatalf("first SSE block is not JSON: %v: %q", err, payload)
	}
	if chunk["object"] != "chat.completion.chunk" || chunk["id"] != "chatcmpl-buffered" {
		t.Fatalf("completion was not reshaped into a chunk: %#v", chunk)
	}
	choice := chunk["choices"].([]any)[0].(map[string]any)
	delta := choice["delta"].(map[string]any)
	if choice["finish_reason"] != "stop" || delta["content"] != "Buffered OK" || delta["reasoning_content"] != "quick check" {
		t.Fatalf("delta lost content: %#v", choice)
	}
	history := st.Metadata().History
	if len(history) != 1 || history[0].Error != nil || history[0].Kind != "chat" || !history[0].Stream {
		t.Fatalf("buffered chat completion should be recorded as a successful stream: %#v", history)
	}
	if history[0].FinishReason != "stop" || history[0].Usage == nil || history[0].Usage.PromptTokens != 7 {
		t.Fatalf("stats were not recorded: %#v", history[0])
	}
}

func TestResponsesShareKeyCoversSamplingParameters(t *testing.T) {
	key := func(modelID string, body map[string]any) string {
		return responsesShareKey(context.Background(), modelID, body, body, model.PerModelConfig{})
	}
	base := map[string]any{"messages": []any{"x"}, "tools": nil, "reasoning_effort": "low", "temperature": 0.2, "stream": true}
	same := key("m", map[string]any{"messages": []any{"x"}, "tools": nil, "reasoning_effort": "low", "temperature": 0.2, "stream": true, "stream_options": map[string]any{"include_usage": true}})
	if key("m", base) != same {
		t.Fatal("transport flags must not split a reconnecting client from the running stream")
	}
	for name, variant := range map[string]map[string]any{
		"effort":      {"messages": []any{"x"}, "tools": nil, "reasoning_effort": "max", "temperature": 0.2},
		"temperature": {"messages": []any{"x"}, "tools": nil, "reasoning_effort": "low", "temperature": 0.9},
		"messages":    {"messages": []any{"y"}, "tools": nil, "reasoning_effort": "low", "temperature": 0.2},
	} {
		if key("m", variant) == key("m", base) {
			t.Fatalf("requests differing in %s must not share one upstream stream", name)
		}
	}
	if key("other-model", base) == key("m", base) {
		t.Fatal("different models must not share")
	}
}

func TestSharedResponsesStreamCoalescesDuplicateClients(t *testing.T) {
	var hits atomic.Int32
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := writer.(http.Flusher)
		_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"reasoning\":\"think\"},\"finish_reason\":null}]}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(250 * time.Millisecond)
		_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2,\"total_tokens\":6}}\n\n")
		_, _ = io.WriteString(writer, "data: [DONE]\n\n")
	}))
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.Accounts = []model.Account{{Name: "main", Key: "cline-key", Enabled: true}}
		config.KnownModels = []string{"cline-pass/test"}
	}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan string, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			request := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
			  "model":"cline-pass/test","input":"share-me","stream":true,"reasoning":{"effort":"low"}
			}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				errs <- response.Body.String()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("shared stream client failed: %s", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("duplicate clients should share one upstream stream, got %d", hits.Load())
	}
	history := st.Metadata().History
	if len(history) != 1 {
		t.Fatalf("one upstream stream must produce one history entry, got %d: %#v", len(history), history)
	}
	if history[0].Error != nil || history[0].Usage == nil || history[0].Usage.CompletionTokens != 2 {
		t.Fatalf("shared stream usage should be counted once: %#v", history[0])
	}
	if stats := accountStats(t, st, "main"); stats.Requests != 1 {
		t.Fatalf("account request count should not be inflated by attached clients: %#v", stats)
	}
}

func TestStreamingResponsesFailsOverToHealthyAccountOn401(t *testing.T) {
	var badKeyHits, goodKeyHits atomic.Int32
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/users/me/plan") {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") == "Bearer bad-key" {
			badKeyHits.Add(1)
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(writer, `{"error":{"message":"Invalid API key","type":"authentication_error"}}`)
			return
		}
		goodKeyHits.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer,
			"data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\n"+
				"data: [DONE]\n\n")
	}))
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.AccountMode = "single"
		config.ActiveAccount = 0
		config.Accounts = []model.Account{
			{Name: "expired", Key: "bad-key", Enabled: true},
			{Name: "backup", Key: "good-key", Enabled: true},
		}
		config.KnownModels = []string{"cline-pass/test"}
		config.PerModel["cline-pass/test"] = model.PerModelConfig{Upstreams: []string{"first", "second"}, PinMode: "strict"}
	}); err != nil {
		t.Fatal(err)
	}

	send := func() *httptest.ResponseRecorder {
		request := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
		  "model":"cline-pass/test","input":"hello","stream":true
		}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response
	}
	response := send()
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "response.completed") {
		t.Fatalf("the backup account should have served the request: %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("X-Cline-Account") != "backup" {
		t.Fatalf("response should be attributed to the backup account: %#v", response.Header())
	}
	if badKeyHits.Load() != 1 || goodKeyHits.Load() != 1 {
		t.Fatalf("expected one rejected and one successful upstream call, got bad=%d good=%d", badKeyHits.Load(), goodKeyHits.Load())
	}

	// The rejected account is cooling down: the next request skips it.
	if response = send(); response.Code != http.StatusOK {
		t.Fatalf("second request failed: %d %s", response.Code, response.Body.String())
	}
	if badKeyHits.Load() != 1 || goodKeyHits.Load() != 2 {
		t.Fatalf("cooling account should not be retried, got bad=%d good=%d", badKeyHits.Load(), goodKeyHits.Load())
	}
	meta := st.Metadata().Models["cline-pass/test"]
	if status, found := meta.UpstreamStatus["first"]; found && status.Status == "auth" {
		t.Fatalf("an account failure must not be blamed on the provider channel: %#v", meta.UpstreamStatus)
	}
}

func TestStreamingResponsesOutlivesIdleWindowWhileDataFlows(t *testing.T) {
	// The stream runs for longer than the configured idle window, but each gap
	// is shorter than it. This guards the original 120s regression: a fixed
	// total deadline must not cut off a long, healthy reasoning stream.
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := writer.(http.Flusher)
		write := func(value string) bool {
			if _, err := io.WriteString(writer, value); err != nil {
				return false
			}
			if flusher != nil {
				flusher.Flush()
			}
			return true
		}
		if !write("data: {\"id\":\"chatcmpl-slow\",\"model\":\"cline-pass/test\",\"choices\":[{\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n") {
			return
		}
		for index := 0; index < 8; index++ {
			select {
			case <-request.Context().Done():
				return
			case <-time.After(40 * time.Millisecond):
			}
			if !write("data: {\"choices\":[{\"delta\":{\"content\":\"x\"},\"finish_reason\":null}]}\n\n") {
				return
			}
		}
		write("data: [DONE]\n\n")
	}))
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.Accounts = []model.Account{{Name: "main", Key: "cline-key", Enabled: true}}
		config.KnownModels = []string{"cline-pass/test"}
	}); err != nil {
		t.Fatal(err)
	}
	server.upstream.SetStreamHeadTimeout(200 * time.Millisecond)
	server.upstream.SetStreamIdleTimeout(120 * time.Millisecond)

	request := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
	  "model":"cline-pass/test","input":"think for a while","stream":true
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("streaming responses request failed: %d %s", response.Code, response.Body.String())
	}
	stream := response.Body.String()
	if !strings.Contains(stream, "event: response.completed") {
		t.Fatalf("stream should complete after outliving the idle window: %s", stream)
	}
	if strings.Contains(stream, "event: response.failed") {
		t.Fatalf("healthy stream should not fail: %s", stream)
	}
	history := st.Metadata().History
	if len(history) != 1 || history[0].Error != nil {
		t.Fatalf("long stream should complete without an error: %#v", history)
	}
	if history[0].MS < 300 {
		t.Fatalf("stream ended before outliving the idle window: %dms", history[0].MS)
	}
}

func TestStreamingResponsesEmitsKeepaliveDuringUpstreamSilence(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := writer.(http.Flusher)
		write := func(value string) {
			_, _ = io.WriteString(writer, value)
			if flusher != nil {
				flusher.Flush()
			}
		}
		write("data: {\"id\":\"chatcmpl-keepalive\",\"model\":\"cline-pass/test\",\"choices\":[{\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n")
		select {
		case <-request.Context().Done():
			return
		case <-time.After(450 * time.Millisecond):
		}
		write("data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\n")
		write("data: [DONE]\n\n")
	}))
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.Accounts = []model.Account{{Name: "main", Key: "cline-key", Enabled: true}}
		config.KnownModels = []string{"cline-pass/test"}
	}); err != nil {
		t.Fatal(err)
	}
	server.upstream.SetStreamHeadTimeout(2 * time.Second)
	server.upstream.SetStreamIdleTimeout(900 * time.Millisecond)

	request := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
	  "model":"cline-pass/test","input":"think silently","stream":true
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("streaming request failed: %d %s", response.Code, response.Body.String())
	}
	stream := response.Body.String()
	if !strings.Contains(stream, ": ping") {
		t.Fatalf("expected an SSE keepalive during silent reasoning: %s", stream)
	}
	if !strings.Contains(stream, "event: response.completed") {
		t.Fatalf("stream should complete after the silent period: %s", stream)
	}
}

func TestStreamingChatEmitsKeepaliveDuringUpstreamSilence(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := writer.(http.Flusher)
		write := func(value string) {
			_, _ = io.WriteString(writer, value)
			if flusher != nil {
				flusher.Flush()
			}
		}
		write("data: {\"id\":\"chatcmpl-keepalive\",\"choices\":[{\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n")
		select {
		case <-request.Context().Done():
			return
		case <-time.After(450 * time.Millisecond):
		}
		write("data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\n")
		write("data: [DONE]\n\n")
	}))
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.Accounts = []model.Account{{Name: "main", Key: "cline-key", Enabled: true}}
		config.KnownModels = []string{"cline-pass/test"}
	}); err != nil {
		t.Fatal(err)
	}
	server.upstream.SetStreamHeadTimeout(2 * time.Second)
	server.upstream.SetStreamIdleTimeout(900 * time.Millisecond)

	request := localRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
	  "model":"cline-pass/test","messages":[{"role":"user","content":"think silently"}],"stream":true
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("streaming request failed: %d %s", response.Code, response.Body.String())
	}
	stream := response.Body.String()
	if !strings.Contains(stream, ": ping") {
		t.Fatalf("expected an SSE keepalive during silent reasoning: %s", stream)
	}
	if !strings.Contains(stream, "\"content\":\"OK\"") {
		t.Fatalf("stream should preserve upstream content: %s", stream)
	}
}

func TestStreamingResponsesPreservesUpstreamErrorDetails(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(writer, `{"error":{"message":"rate limited","type":"rate_limit_error","code":"rate_limit_exceeded"}}`)
	}))
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.Accounts = []model.Account{{Name: "main", Key: "cline-key", Enabled: true}}
		config.KnownModels = []string{"cline-pass/test"}
	}); err != nil {
		t.Fatal(err)
	}

	request := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
	  "model":"cline-pass/test","input":"hi","stream":true
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("expected HTTP 429, got %d: %s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	errorObject, _ := payload["error"].(map[string]any)
	if errorObject["type"] != "rate_limit_error" || errorObject["code"] != "rate_limit_exceeded" {
		t.Fatalf("streaming error details were not preserved: %#v", payload)
	}
}
