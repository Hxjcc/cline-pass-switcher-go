package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	responsesbridge "github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/responses"
)

func TestResponsesShareKeySeparatesConversionAndRouting(t *testing.T) {
	left := map[string]any{"model": "test", "input": "hi", "tools": []any{
		map[string]any{"type": "namespace", "name": "editor", "tools": []any{map[string]any{"type": "function", "name": "read"}}},
	}}
	right := map[string]any{"model": "test", "input": "hi", "tools": []any{map[string]any{"type": "function", "name": "editor__read"}}}
	leftChat, _, err := responsesbridge.ToChat(left)
	if err != nil {
		t.Fatal(err)
	}
	rightChat, _, err := responsesbridge.ToChat(right)
	if err != nil {
		t.Fatal(err)
	}
	cfg := model.PerModelConfig{}
	base := responsesShareKey(context.Background(), "test", left, leftChat, cfg)
	if base == responsesShareKey(context.Background(), "test", right, rightChat, cfg) {
		t.Fatal("different tool bindings share a key")
	}
	if base == responsesShareKey(context.Background(), "test", left, leftChat, model.PerModelConfig{Upstreams: []string{"other"}}) {
		t.Fatal("changed routing shares a key")
	}
	leftChat["reasoning_effort"] = "high"
	if base == responsesShareKey(context.Background(), "test", left, leftChat, cfg) {
		t.Fatal("changed effective generation shares a key")
	}
}

func TestConcurrentResponsesPreserveEachClientsContext(t *testing.T) {
	for _, tc := range []struct {
		name       string
		requests   [2]string
		completion string
		expected   [2]string
	}{
		{
			name: "metadata",
			requests: [2]string{
				`{"model":"test","input":"hi","stream":true,"metadata":{"owner":"first"}}`,
				`{"model":"test","input":"hi","stream":true,"metadata":{"owner":"second"}}`,
			},
			completion: `{"choices":[{"delta":{"content":"OK"},"finish_reason":"stop"}]}`,
			expected:   [2]string{`"owner":"first"`, `"owner":"second"`},
		},
		{
			name: "tool namespace",
			requests: [2]string{
				`{"model":"test","input":"hi","stream":true,"tools":[{"type":"namespace","name":"editor","tools":[{"type":"function","name":"read"}]}]}`,
				`{"model":"test","input":"hi","stream":true,"tools":[{"type":"function","name":"editor__read"}]}`,
			},
			completion: `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call1","function":{"name":"editor__read","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
			expected:   [2]string{`"namespace":"editor"`, `"name":"editor__read"`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started, release := make(chan struct{}, 2), make(chan struct{})
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				started <- struct{}{}
				<-release
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", tc.completion)
			}))
			defer up.Close()
			st, server := newTestServer(t)
			if err := st.UpdateConfig(func(c *model.Config) {
				c.UpstreamBase = up.URL
				c.Accounts = []model.Account{{Name: "main", Key: "test", Enabled: true}}
			}); err != nil {
				t.Fatal(err)
			}
			results := [2]chan *httptest.ResponseRecorder{make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)}
			for i := range 2 {
				go func() {
					w := httptest.NewRecorder()
					server.ServeHTTP(w, localRequest("POST", "/v1/responses", strings.NewReader(tc.requests[i])))
					results[i] <- w
				}()
			}
			for range 2 {
				select {
				case <-started:
				case <-time.After(2 * time.Second):
					t.Error("different clients were coalesced into one upstream request")
				}
			}
			close(release)
			for i := range 2 {
				w := <-results[i]
				wire := w.Body.String()
				if w.Code != 200 || !strings.Contains(wire, "event: response.completed") || !strings.Contains(wire, tc.expected[i]) {
					t.Fatalf("client %d lost its context: %d %s", i, w.Code, wire)
				}
				if tc.name == "metadata" && strings.Contains(wire, tc.expected[1-i]) {
					t.Fatalf("client %d received the other client's metadata", i)
				}
				if tc.name == "tool namespace" && i == 1 && strings.Contains(wire, `"namespace":"editor"`) {
					t.Fatal("unscoped tool acquired the other client's namespace")
				}
			}
		})
	}
}

func TestResponsesHistoryTracksProtocolOutcome(t *testing.T) {
	first := "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"
	for _, tc := range []struct {
		name, contentType, wire, terminal, reason string
	}{
		{"bare EOF", "text/event-stream", first, "failed", "stream_truncated"},
		{"malformed JSON", "text/event-stream", first + "data: {broken}\n\ndata: [DONE]\n\n", "failed", "stream_invalid_json"},
		{"upstream error", "text/event-stream", first + "data: {\"error\":{\"message\":\"overloaded\",\"code\":\"server_error\"}}\n\n", "failed", "server_error"},
		{"length", "text/event-stream", first + "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n", "incomplete", "max_output_tokens"},
		{"content filter", "text/event-stream", first + "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"content_filter\"}]}\n\ndata: [DONE]\n\n", "incomplete", "content_filter"},
		{"buffered bad tools", "application/json", `{"choices":[{"message":{"tool_calls":[{"id":"call1","function":{"name":"exec","arguments":"{"}}]},"finish_reason":"tool_calls"}]}`, "failed", "upstream_tool_call_dropped"},
		{"buffered length", "application/json", `{"choices":[{"message":{"content":"partial"},"finish_reason":"length"}]}`, "incomplete", "max_output_tokens"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				io.WriteString(w, tc.wire)
			}))
			defer up.Close()
			st, server := newTestServer(t)
			if err := st.UpdateConfig(func(c *model.Config) {
				c.UpstreamBase = up.URL
				c.Accounts = []model.Account{{Name: "main", Key: "test", Enabled: true}}
			}); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			server.ServeHTTP(w, localRequest("POST", "/v1/responses", strings.NewReader(`{"model":"test","input":"hi","stream":true}`)))
			if !strings.Contains(w.Body.String(), "event: response."+tc.terminal) {
				t.Fatalf("wrong terminal: %s", w.Body.String())
			}
			meta := st.Metadata()
			if len(meta.History) != 1 || meta.History[0].Error == nil || !strings.Contains(*meta.History[0].Error, tc.reason) {
				t.Fatalf("protocol failure not recorded: %#v", meta.History)
			}
			if accountStats(t, st, "main").LastError == nil {
				t.Fatal("account statistics still report success")
			}
		})
	}
}

func TestSuccessfulResponsesFallbackClearsPreviousError(t *testing.T) {
	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(503)
			io.WriteString(w, `{"error":{"message":"temporarily unavailable"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer up.Close()
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(c *model.Config) {
		c.UpstreamBase = up.URL
		c.Accounts = []model.Account{{Name: "main", Key: "test", Enabled: true}}
		c.PerModel["test"] = model.PerModelConfig{Upstreams: []string{"first", "second"}, PinMode: "strict"}
	}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, localRequest("POST", "/v1/responses", strings.NewReader(`{"model":"test","input":"hi","stream":true}`)))
	history := st.Metadata().History
	if !strings.Contains(w.Body.String(), "response.completed") || len(history) != 1 || history[0].Error != nil || len(history[0].Trace) != 2 {
		t.Fatalf("successful fallback marked failed: %#v", history)
	}
}

// A truncated summary is never presented as a complete handoff, but it no
// longer strands the client either: the compaction turn degrades, keeps the
// partial text and states why.
func TestCompactionDegradesTruncatedSummary(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"id":"chatcmpl-trunc","created":1,"choices":[{"index":0,"message":{"role":"assistant","content":"partial summary"},"finish_reason":"length"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
			}))
			defer up.Close()
			st, server := newTestServer(t)
			if err := st.UpdateConfig(func(c *model.Config) {
				c.UpstreamBase = up.URL
				c.Accounts = []model.Account{{Name: "main", Key: "test", Enabled: true}}
			}); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			server.ServeHTTP(w, localRequest("POST", "/v1/responses/compact", strings.NewReader(fmt.Sprintf(`{"model":"test","input":"hi","stream":%v}`, stream))))
			if w.Code != http.StatusOK {
				t.Fatalf("degraded compact should succeed: %d %s", w.Code, w.Body.String())
			}
			envelope := ""
			if stream {
				envelope = extractCompactionEnvelope(t, w.Body.String())
			} else {
				var payload map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
					t.Fatal(err)
				}
				output, _ := payload["output"].([]any)
				item, _ := output[len(output)-1].(map[string]any)
				envelope, _ = item["encrypted_content"].(string)
			}
			payload := decodeCompactionEnvelope(t, envelope)
			summary := payload.Summary
			for _, expected := range []string{"compaction degraded", "max_output_tokens", "partial summary"} {
				if !strings.Contains(summary, expected) {
					t.Fatalf("degraded summary is missing %q:\n%s", expected, summary)
				}
			}
			// The truncation escalated once, then degraded: one entry, both passes traced.
			if calls.Load() != 2 {
				t.Fatalf("expected one escalated retry before degrading, got %d calls", calls.Load())
			}
			if history := st.Metadata().History; len(history) != 1 || history[0].Kind != "compact" || history[0].Error != nil || len(history[0].Trace) != 2 {
				t.Fatalf("degraded compaction not recorded correctly: %#v", history)
			}
		})
	}
}

func TestCompactionConstructsSummaryUpstreamRequest(t *testing.T) {
	requests := make(chan map[string]any, 1)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"Full summary"},"finish_reason":"stop"}]}`)
	}))
	defer up.Close()
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(c *model.Config) {
		c.UpstreamBase = up.URL
		c.Accounts = []model.Account{{Name: "main", Key: "test", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, localRequest("POST", "/v1/responses/compact", strings.NewReader(`{
		"model":"test","input":[{"role":"user","content":"Continue implementing the app"}],
		"tools":[{"type":"function","name":"exec"}],"tool_choice":"required",
		"text":{"format":{"type":"json_object"}}
	}`)))
	if w.Code != 200 {
		t.Fatalf("compaction failed: %d %s", w.Code, w.Body.String())
	}
	request := <-requests
	messages, _ := request["messages"].([]any)
	last, _ := messages[len(messages)-1].(map[string]any)
	if content, _ := last["content"].(string); !strings.Contains(content, "handoff summary") {
		t.Fatalf("no summary task sent upstream: %#v", request)
	}
	for _, key := range []string{"tools", "tool_choice", "response_format", "stream"} {
		if _, found := request[key]; found {
			t.Fatalf("compaction has task-generation option %s", key)
		}
	}
}

func TestCancelledResponsesRunIsRecordedAsClientCancellation(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer up.Close()
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(c *model.Config) {
		c.UpstreamBase = up.URL
		c.Accounts = []model.Account{{Name: "main", Key: "test", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	chat, bridge, err := responsesbridge.ToChat(map[string]any{"model": "test", "input": "hi", "stream": true})
	if err != nil {
		t.Fatal(err)
	}
	job := server.shares.join(context.Background(), "cancel-test", func(job *sharedResponsesStream) {
		server.runSharedResponses(job, chat, bridge, "test", model.PerModelConfig{})
	})
	defer job.release()
	<-job.ready
	job.cancel()
	sub := job.subscribe()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		_, ok, err := sub.next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
	}
	history := st.Metadata().History
	if len(history) != 1 || history[0].Error == nil || *history[0].Error != "客户端取消" {
		t.Fatalf("cancellation recorded incorrectly: %#v", history)
	}
}
