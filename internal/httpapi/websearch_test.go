package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/store"
)

const (
	searchLegSSE = "data: {\"id\":\"chatcmpl-1\",\"model\":\"cline-pass/test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"web_search\",\"arguments\":\"{\\\"query\\\":\\\"今日新闻\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
	answerLegSSE = "data: {\"id\":\"chatcmpl-2\",\"model\":\"cline-pass/test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"今天的要闻是……\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":8,\"total_tokens\":28}}\n\ndata: [DONE]\n\n"
)

func newSearchExa(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"results":[{"title":"人民网","url":"https://news.example/a","text":"今日要闻"}]}`)
	}))
}

func configureDirectSearch(t *testing.T, st *store.Store, upstreamURL, searchURL string) {
	t.Helper()
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamURL
		config.Accounts = []model.Account{{Name: "main", Key: "key", Enabled: true}}
		config.WebSearchDirectAPIKey = "exa-key"
		config.WebSearchDirectBaseURL = searchURL
	}); err != nil {
		t.Fatal(err)
	}
}

func TestProxyWebSearchStreamsSearchItemsAndContinues(t *testing.T) {
	var upstreamCalls atomic.Int32
	var firstRequest, secondRequest atomic.Value
	up := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		raw, _ := io.ReadAll(request.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		writer.Header().Set("Content-Type", "text/event-stream")
		if upstreamCalls.Add(1) == 1 {
			firstRequest.Store(body)
			_, _ = io.WriteString(writer, searchLegSSE)
			return
		}
		secondRequest.Store(body)
		_, _ = io.WriteString(writer, answerLegSSE)
	}))
	defer up.Close()

	var searchHits atomic.Int32
	exa := newSearchExa(t, &searchHits)
	defer exa.Close()

	st, server := newTestServer(t)
	configureDirectSearch(t, st, up.URL, exa.URL)

	request := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(
		`{"model":"cline-pass/test","input":"搜索今天的新闻","tools":[{"type":"web_search"}],"stream":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("streaming request failed: %d %s", response.Code, response.Body.String())
	}
	stream := response.Body.String()
	for _, want := range []string{`"type":"web_search_call"`, `"query":"今日新闻"`, "event: response.completed"} {
		if !strings.Contains(stream, want) {
			t.Fatalf("stream is missing %q: %s", want, stream)
		}
	}
	if strings.Contains(stream, `"type":"function_call"`) {
		t.Fatalf("the proxy-side search call must not reach the client: %s", stream)
	}
	if searchHits.Load() != 1 {
		t.Fatalf("expected exactly one search API call, got %d", searchHits.Load())
	}

	first, _ := firstRequest.Load().(map[string]any)
	tools, _ := first["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("direct mode must declare one proxy tool: %#v", first["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	function, _ := tool["function"].(map[string]any)
	if tool["type"] != "function" || function["name"] != "web_search" {
		t.Fatalf("hosted web_search was not replaced by the proxy tool: %#v", tools[0])
	}

	second, _ := secondRequest.Load().(map[string]any)
	messages, _ := second["messages"].([]any)
	var sawAssistantCall, sawToolResult bool
	for _, raw := range messages {
		message, _ := raw.(map[string]any)
		if message["role"] == "assistant" {
			if calls, ok := message["tool_calls"].([]any); ok && len(calls) == 1 {
				entry, _ := calls[0].(map[string]any)
				callFunction, _ := entry["function"].(map[string]any)
				if callFunction["name"] == "web_search" && entry["id"] == "call_1" {
					sawAssistantCall = true
				}
			}
		}
		if message["role"] == "tool" && message["tool_call_id"] == "call_1" {
			content, _ := message["content"].(string)
			if strings.Contains(content, "https://news.example/a") && strings.Contains(content, "人民网") {
				sawToolResult = true
			}
		}
	}
	if !sawAssistantCall || !sawToolResult {
		t.Fatalf("search continuation was not replayed upstream: %#v", messages)
	}
}

func TestProxyWebSearchBufferedResponseCarriesSearchItem(t *testing.T) {
	var upstreamCalls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if upstreamCalls.Add(1) == 1 {
			_, _ = io.WriteString(writer, `{"id":"chatcmpl-1","model":"cline-pass/test","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"今日新闻\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`)
			return
		}
		_, _ = io.WriteString(writer, `{"id":"chatcmpl-2","model":"cline-pass/test","choices":[{"index":0,"message":{"role":"assistant","content":"今天的要闻是……"},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":8,"total_tokens":28}}`)
	}))
	defer up.Close()

	var searchHits atomic.Int32
	exa := newSearchExa(t, &searchHits)
	defer exa.Close()

	st, server := newTestServer(t)
	configureDirectSearch(t, st, up.URL, exa.URL)

	request := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(
		`{"model":"cline-pass/test","input":"搜索今天的新闻","tools":[{"type":"web_search"}]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("buffered request failed: %d %s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	output, _ := payload["output"].([]any)
	if len(output) != 2 {
		t.Fatalf("expected a search item and the answer: %#v", payload["output"])
	}
	item, _ := output[0].(map[string]any)
	action, _ := item["action"].(map[string]any)
	if item["type"] != "web_search_call" || item["status"] != "completed" || action["query"] != "今日新闻" {
		t.Fatalf("unexpected search item: %#v", item)
	}
	message, _ := output[1].(map[string]any)
	if message["type"] != "message" {
		t.Fatalf("missing final message: %#v", output[1])
	}
	usage, _ := payload["usage"].(map[string]any)
	if usage["total_tokens"] != float64(40) {
		t.Fatalf("usage should include every leg: %#v", usage)
	}
	if searchHits.Load() != 1 {
		t.Fatalf("expected exactly one search API call, got %d", searchHits.Load())
	}
}
