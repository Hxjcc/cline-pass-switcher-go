package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/store"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/upstream"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/webassets"
)

// Production only permits loopback hosts before a proxy key is configured.
func localRequest(method, target string, body io.Reader) *http.Request {
	if strings.HasPrefix(target, "/") {
		target = "http://localhost" + target
	}
	request := httptest.NewRequest(method, target, body)
	// httptest.NewRequest defaults RemoteAddr to TEST-NET-1. The access guard
	// classifies the connection source, so a local test has to look local.
	request.RemoteAddr = "127.0.0.1:54321"
	return request
}

func newTestServer(t *testing.T) (*store.Store, *Server) {
	t.Helper()
	for _, name := range []string{"CLINE_PASS_KEY", "PROXY_KEY", "PUBLIC_BASE_URL", "PORT"} {
		t.Setenv(name, "")
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	service := upstream.New(st)
	assets := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>test</title>"), Mode: 0o644},
	}
	server, err := New(st, service, fs.FS(assets))
	if err != nil {
		t.Fatal(err)
	}
	return st, server
}

// accountStats reads the per-account counters by account name, resolving the
// stable identity the store now keys them by.
func accountStats(t *testing.T, st *store.Store, name string) model.AccountStats {
	t.Helper()
	for _, account := range st.Config().Accounts {
		if account.Name == name {
			return st.Metadata().Stats[account.ID]
		}
	}
	return st.Metadata().Stats[name]
}

func TestMetaIsPublicAndProtectedRoutesRequireKey(t *testing.T) {
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.ProxyKey = "secret"
	}); err != nil {
		t.Fatal(err)
	}

	metaRequest := localRequest(http.MethodGet, "/api/meta", nil)
	metaResponse := httptest.NewRecorder()
	server.ServeHTTP(metaResponse, metaRequest)
	if metaResponse.Code != http.StatusOK {
		t.Fatalf("meta should be public, got %d", metaResponse.Code)
	}

	unauthorizedRequest := localRequest(http.MethodGet, "/api/models", nil)
	unauthorizedResponse := httptest.NewRecorder()
	server.ServeHTTP(unauthorizedResponse, unauthorizedRequest)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("protected route should reject missing key, got %d", unauthorizedResponse.Code)
	}

	authorizedRequest := localRequest(http.MethodGet, "/api/models", nil)
	authorizedRequest.Header.Set("X-Admin-Key", "secret")
	authorizedResponse := httptest.NewRecorder()
	server.ServeHTTP(authorizedResponse, authorizedRequest)
	if authorizedResponse.Code != http.StatusOK {
		t.Fatalf("protected route should accept key, got %d", authorizedResponse.Code)
	}
}

func TestServerFallsBackAcrossUpstreamsAndRecordsTrace(t *testing.T) {
	var requests atomic.Int32
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		count := requests.Add(1)
		if request.URL.Path != "/chat/completions" {
			http.NotFound(writer, request)
			return
		}
		if count == 1 {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"error":"temporarily rate-limited"}`)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{
		  "id":"chatcmpl-test",
		  "object":"chat.completion",
		  "choices":[{
		    "index":0,
		    "message":{
		      "role":"assistant",
		      "content":"OK",
		      "provider_metadata":{"gateway":{"routing":{
		        "canonicalSlug":"vendor/model",
		        "finalProvider":"second"
		      }}}
		    },
		    "finish_reason":"stop"
		  }]
		}`)
	}))
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.Accounts = []model.Account{{Name: "main", Key: "sk_test", Enabled: true}}
		config.KnownModels = []string{"cline-pass/test"}
		config.PerModel["cline-pass/test"] = model.PerModelConfig{
			Upstreams: []string{"first", "second"},
			Exclude:   []string{},
			PinMode:   "strict",
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) {
		meta.Pipeline = "planner"
		meta.Upstreams = []string{"first", "second"}
	}); err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader(`{"model":"cline-pass/test","messages":[{"role":"user","content":"hi"}]}`)
	request := localRequest(http.MethodPost, "/v1/chat/completions", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected fallback success, got %d: %s", response.Code, response.Body.String())
	}
	if response.Header().Get("X-Cline-Target-Upstream") != "first>second" {
		t.Fatalf("unexpected target header: %q", response.Header().Get("X-Cline-Target-Upstream"))
	}
	if response.Header().Get("X-Cline-Actual-Upstream") != "second" {
		t.Fatalf("unexpected actual upstream: %q", response.Header().Get("X-Cline-Actual-Upstream"))
	}
	if response.Header().Get("X-Cline-Attempts") != "2" {
		t.Fatalf("unexpected attempt count: %q", response.Header().Get("X-Cline-Attempts"))
	}
	if requests.Load() != 2 {
		t.Fatalf("expected two upstream attempts, got %d", requests.Load())
	}

	history := st.Metadata().History
	if len(history) != 1 {
		t.Fatalf("expected one history entry, got %d", len(history))
	}
	if len(history[0].Trace) != 2 || history[0].Trace[1].Status != http.StatusOK {
		t.Fatalf("unexpected history trace: %#v", history[0].Trace)
	}
}

func TestStaticFallbackServesSPAIndex(t *testing.T) {
	_, server := newTestServer(t)
	request := localRequest(http.MethodGet, "/models/cline-pass/test", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected SPA fallback, got %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "<!doctype html>") {
		t.Fatalf("unexpected static response: %s", response.Body.String())
	}
}

func TestConfigEndpointNormalizesExclude(t *testing.T) {
	st, server := newTestServer(t)
	body := strings.NewReader(`{
	  "perModel":{
	    "cline-pass/test":{
	      "upstreams":["a","b","a"],
	      "exclude":["b"],
	      "pinMode":"preferred",
	      "sort":"cost"
	    }
	  }
	}`)
	request := localRequest(http.MethodPost, "/api/config", body)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("save config failed: %d %s", response.Code, response.Body.String())
	}
	config := st.Config().PerModel["cline-pass/test"]
	if len(config.Upstreams) != 1 || config.Upstreams[0] != "a" {
		raw, _ := json.Marshal(config)
		t.Fatalf("unexpected normalized config: %s", raw)
	}
}

func TestRemoveModelDropsSubscriptionConfigAndMeta(t *testing.T) {
	st, server := newTestServer(t)
	const modelID = "cline-pass/glm-5.3-flash"
	if err := st.UpdateConfig(func(config *model.Config) {
		config.PerModel[modelID] = model.PerModelConfig{Upstreams: []string{"z-ai"}, PinMode: "strict"}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateModelMeta(modelID, func(meta *model.ModelMeta) {
		meta.Upstreams = []string{"z-ai"}
	}); err != nil {
		t.Fatal(err)
	}

	request := localRequest(http.MethodPost, "/api/models/remove", strings.NewReader(`{"model":"`+modelID+`"}`))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("remove failed: %d %s", response.Code, response.Body.String())
	}

	config := st.Config()
	if slices.Contains(config.KnownModels, modelID) {
		t.Fatal("model should leave the subscription list")
	}
	if _, found := config.PerModel[modelID]; found {
		t.Fatal("pin configuration should be dropped with the model")
	}
	if !slices.Contains(config.RemovedModels, modelID) {
		t.Fatal("removal should be remembered for the official sync")
	}
	if _, found := st.Metadata().Models[modelID]; found {
		t.Fatal("probe data should be dropped with the model")
	}

	listRequest := localRequest(http.MethodGet, "/v1/models", nil)
	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, listRequest)
	if strings.Contains(listResponse.Body.String(), modelID) {
		t.Fatal("a removed model should not be advertised to clients")
	}

	// A failed request does not resurrect the model, a successful one does
	// (streamed or not, every path records the entry).
	failure := "boom"
	if err := st.Record(model.HistoryEntry{TS: 1, Model: modelID, MS: 5, Error: &failure}); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(st.Config().KnownModels, modelID) {
		t.Fatal("a failed request should not re-subscribe a removed model")
	}
	if err := st.Record(model.HistoryEntry{TS: 2, Model: modelID, MS: 5, Stream: true}); err != nil {
		t.Fatal(err)
	}
	config = st.Config()
	if !slices.Contains(config.KnownModels, modelID) {
		t.Fatal("a successful request should re-subscribe the model")
	}
	if slices.Contains(config.RemovedModels, modelID) {
		t.Fatal("re-subscribing should clear the removal")
	}
}

func TestClearHistoryKeepsAccountStats(t *testing.T) {
	st, server := newTestServer(t)
	if err := st.Record(model.HistoryEntry{TS: 1, Model: "cline-pass/test", MS: 5, Account: "账号1"}); err != nil {
		t.Fatal(err)
	}
	request := localRequest(http.MethodPost, "/api/history/clear", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("clear failed: %d %s", response.Code, response.Body.String())
	}
	meta := st.Metadata()
	if len(meta.History) != 0 {
		t.Fatalf("history should be empty, got %d entries", len(meta.History))
	}
	if meta.Stats["账号1"].Requests != 1 {
		t.Fatalf("account counters should survive a history clear: %#v", meta.Stats)
	}
}

func TestModelsEndpointDoesNotLimitSubscriptionModels(t *testing.T) {
	_, server := newTestServer(t)
	request := localRequest(http.MethodGet, "/v1/models", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("models request failed: %d", response.Code)
	}
	var payload struct {
		Data   []map[string]any `json:"data"`
		Models []any            `json:"models"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data) != len(model.DefaultKnownModels) {
		t.Fatalf("expected %d default models, got %d", len(model.DefaultKnownModels), len(payload.Data))
	}
	if payload.Models == nil {
		t.Fatal("expected Codex-compatible models field")
	}
}

func TestResponsesEndpointConvertsRequestAndResponse(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/chat/completions" {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer cline-key" {
			t.Fatalf("unexpected upstream authorization header")
		}
		var payload map[string]any
		decoder := json.NewDecoder(request.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["reasoning_effort"] != "high" {
			t.Fatalf("reasoning effort was not forwarded: %#v", payload["reasoning_effort"])
		}
		if payload["include_reasoning"] != true {
			t.Fatalf("include_reasoning was not forwarded: %#v", payload["include_reasoning"])
		}
		gateway := payload["providerOptions"].(map[string]any)["gateway"].(map[string]any)
		only := gateway["only"].([]any)
		if len(only) != 1 || only[0] != "deepseek" {
			t.Fatalf("strict route was not injected: %#v", gateway)
		}
		messages := payload["messages"].([]any)
		content := messages[0].(map[string]any)["content"].([]any)
		if len(content) != 2 || content[1].(map[string]any)["type"] != "image_url" {
			t.Fatalf("input image was not converted: %#v", messages)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{
		  "id":"chatcmpl-responses",
		  "model":"cline-pass/deepseek-v4.1-flash",
		  "choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],
		  "usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}
		}`)
	}))
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.Accounts = []model.Account{{Name: "main", Key: "cline-key", Enabled: true}}
		config.KnownModels = []string{"cline-pass/deepseek-v4.1-flash"}
		config.PerModel["cline-pass/deepseek-v4.1-flash"] = model.PerModelConfig{
			Upstreams: []string{"deepseek"}, Exclude: []string{}, PinMode: "strict",
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateModelMeta("cline-pass/deepseek-v4.1-flash", func(meta *model.ModelMeta) {
		meta.Pipeline = "planner"
		meta.Upstreams = []string{"deepseek"}
	}); err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader(`{
	  "model":"cline-pass/deepseek-v4.1-flash",
	  "reasoning":{"effort":"high","summary":"auto"},
	  "input":[{"type":"message","role":"user","content":[
	    {"type":"input_text","text":"describe"},
	    {"type":"input_image","image_url":"data:image/png;base64,AAAA","detail":"high"}
	  ]}]
	}`)
	request := localRequest(http.MethodPost, "/v1/responses", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("responses request failed: %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("X-Cline-Reasoning-Effort") != "high" {
		t.Fatalf("missing reasoning header")
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["object"] != "response" || payload["status"] != "completed" {
		t.Fatalf("unexpected Responses payload: %#v", payload)
	}
	output := payload["output"].([]any)
	content := output[0].(map[string]any)["content"].([]any)
	if content[0].(map[string]any)["text"] != "OK" {
		t.Fatalf("unexpected response text: %#v", output)
	}
}

func TestSingularResponsePathIsNotRouted(t *testing.T) {
	_, server := newTestServer(t)
	for _, path := range []string{"/response", "/v1/response", "/api/v1/response"} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, localRequest(http.MethodPost, path, strings.NewReader(`{"model":"test","input":"hi"}`)))
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s should not be a Responses alias: %d %s", path, response.Code, response.Body.String())
		}
	}
}

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

// newBufferedCompletionUpstream ignores stream:true and answers with a plain
// JSON completion, as some OpenAI-compatible gateways do.
func newBufferedCompletionUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode upstream body: %v", err)
		}
		if body["stream"] != true {
			t.Errorf("expected stream:true to reach upstream: %#v", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{
		  "id":"chatcmpl-buffered","object":"chat.completion","model":"vendor/test","provider":"Vendor",
		  "choices":[{"index":0,"message":{"role":"assistant","reasoning_content":"quick check","content":"Buffered OK"},"finish_reason":"stop"}],
		  "usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}
		}`)
	}))
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
		return responsesShareKey(modelID, body, body, model.PerModelConfig{})
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

func TestNonStreamChainStopsOnAuthFailureWithoutAlternateAccount(t *testing.T) {
	var hits atomic.Int32
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(writer, `{"error":{"message":"Invalid API key","type":"authentication_error"}}`)
	}))
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.Accounts = []model.Account{{Name: "only", Key: "bad-key", Enabled: true}}
		config.KnownModels = []string{"cline-pass/test"}
		config.PerModel["cline-pass/test"] = model.PerModelConfig{Upstreams: []string{"first", "second", "third"}, PinMode: "strict"}
	}); err != nil {
		t.Fatal(err)
	}

	request := localRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
	  "model":"cline-pass/test","messages":[{"role":"user","content":"hi"}]
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("auth failure should be surfaced as-is: %d %s", response.Code, response.Body.String())
	}
	if hits.Load() != 1 {
		t.Fatalf("switching provider channels cannot fix a bad key; expected 1 upstream call, got %d", hits.Load())
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

func newCompactionUpstream(t *testing.T, summary string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/chat/completions" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{
		  "id":"chatcmpl-compact",
		  "model":"cline-pass/test",
		  "choices":[{"index":0,"message":{"role":"assistant","content":"`+summary+`"},"finish_reason":"stop"}],
		  "usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}
		}`)
	}))
}

func TestResponsesCompactEndpointWrapsChatSummary(t *testing.T) {
	upstreamServer := newCompactionUpstream(t, "condensed history")
	defer upstreamServer.Close()
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.Accounts = []model.Account{{Name: "main", Key: "cline-key", Enabled: true}}
		config.KnownModels = []string{"cline-pass/test"}
	}); err != nil {
		t.Fatal(err)
	}

	request := localRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(`{
	  "model":"cline-pass/test","input":"summarize the conversation"
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("compaction request failed: %d %s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	output, _ := payload["output"].([]any)
	if payload["object"] != "response.compaction" || len(output) != 2 {
		t.Fatalf("expected preserved user message and compaction item: %#v", payload)
	}
	if user, _ := output[0].(map[string]any); user["role"] != "user" {
		t.Fatalf("original user message was not retained: %#v", output[0])
	}
	item, _ := output[1].(map[string]any)
	if item["type"] != "compaction" {
		t.Fatalf("unexpected compaction item: %#v", item)
	}
	envelope, _ := item["encrypted_content"].(string)
	encoded := strings.TrimPrefix(envelope, "ocx1:")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || string(decoded) != "condensed history" {
		t.Fatalf("compaction envelope did not round-trip: %q %v", envelope, err)
	}
}

func TestResponsesCompactStreamEmitsCompactionEvents(t *testing.T) {
	upstreamServer := newCompactionUpstream(t, "streamed summary")
	defer upstreamServer.Close()
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.Accounts = []model.Account{{Name: "main", Key: "cline-key", Enabled: true}}
		config.KnownModels = []string{"cline-pass/test"}
	}); err != nil {
		t.Fatal(err)
	}

	request := localRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(`{
	  "model":"cline-pass/test","input":"summarize the conversation","stream":true
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("streaming compaction failed: %d %s", response.Code, response.Body.String())
	}
	stream := response.Body.String()
	for _, expected := range []string{
		"event: response.output_item.done",
		"event: response.completed",
		`"type":"compaction"`,
	} {
		if !strings.Contains(stream, expected) {
			t.Fatalf("missing %q in compaction stream: %s", expected, stream)
		}
	}
}

func TestResponsesCompactUsesMinimumOutputBudget(t *testing.T) {
	var invalidBudget atomic.Bool
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		if tokens, ok := payload["max_tokens"].(float64); !ok || tokens < 2048 {
			invalidBudget.Store(true)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{
		  "id":"chatcmpl-compact",
		  "model":"cline-pass/test",
		  "choices":[{"index":0,"message":{"role":"assistant","content":"summary"},"finish_reason":"stop"}]
		}`)
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

	request := localRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(`{
	  "model":"cline-pass/test","input":"summarize"
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("compaction request failed: %d %s", response.Code, response.Body.String())
	}
	if invalidBudget.Load() {
		t.Fatal("compaction should send at least 2048 max_tokens for reasoning models")
	}
}

func TestResponsesReasoningEffortMappingIsForwardedAndVisible(t *testing.T) {
	var forwarded string
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		decoder := json.NewDecoder(request.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil {
			t.Errorf("decode upstream payload: %v", err)
			return
		}
		forwarded, _ = payload["reasoning_effort"].(string)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{
		  "id":"chatcmpl-effort",
		  "model":"cline-pass/test",
		  "choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]
		}`)
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
	if _, err := st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) {
		meta.ReasoningEfforts = []string{"low", "high"}
	}); err != nil {
		t.Fatal(err)
	}

	request := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
	  "model":"cline-pass/test","input":"hi","reasoning":{"effort":"max"},"max_output_tokens":32
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("responses request failed: %d %s", response.Code, response.Body.String())
	}
	if forwarded != "high" {
		t.Fatalf("max should be clamped to the highest supported effort, got %q", forwarded)
	}
	if header := response.Header().Get("X-Cline-Reasoning-Effort"); header != "high" {
		t.Fatalf("response header should expose the effective effort, got %q", header)
	}
}

func TestResponsesPreservesUpstreamErrorDetails(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		body        string
		wantType    string
		wantCode    string
		wantMessage string
	}{
		{
			name: "rate limit", status: http.StatusTooManyRequests,
			body:     `{"error":{"message":"rate limited","type":"rate_limit_error","code":"rate_limit_exceeded"}}`,
			wantType: "rate_limit_error", wantCode: "rate_limit_exceeded", wantMessage: "rate limited",
		},
		{
			name: "context length", status: http.StatusBadRequest,
			body:     `{"error":{"message":"maximum context length exceeded"}}`,
			wantType: "context_length_exceeded", wantMessage: "maximum context length exceeded",
		},
		{
			name: "authentication", status: http.StatusUnauthorized,
			body:     `{"error":{"message":"invalid api key","code":"invalid_api_key"}}`,
			wantType: "authentication_error", wantCode: "invalid_api_key", wantMessage: "invalid api key",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(testCase.status)
				_, _ = io.WriteString(writer, testCase.body)
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
			  "model":"cline-pass/test","input":"hi"
			}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != testCase.status {
				t.Fatalf("expected HTTP %d, got %d: %s", testCase.status, response.Code, response.Body.String())
			}
			var payload map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			errorObject, _ := payload["error"].(map[string]any)
			if errorObject["type"] != testCase.wantType || errorObject["message"] != testCase.wantMessage {
				t.Fatalf("error details were not preserved: %#v", payload)
			}
			if testCase.wantCode != "" && errorObject["code"] != testCase.wantCode {
				t.Fatalf("error code was not preserved: %#v", payload)
			}
		})
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

// The console holds the proxy key in browser storage, so a script injected
// into the page could read it. The policy allows exactly the inline boot
// scripts that ship with index.html and nothing else.
func TestContentSecurityPolicyHashesInlineBootScripts(t *testing.T) {
	index := []byte(`<!doctype html><html><head>` +
		`<script>window.theme="dark"</script>` +
		`<script type="module" src="/assets/app.js"></script>` +
		`</head><body><script>window.boot=1</script></body></html>`)
	policy := contentSecurityPolicy(index)

	directive := ""
	for _, part := range strings.Split(policy, "; ") {
		if strings.HasPrefix(part, "script-src ") {
			directive = part
		}
	}
	if directive == "" {
		t.Fatalf("policy has no script-src: %s", policy)
	}
	for _, inline := range []string{`window.theme="dark"`, "window.boot=1"} {
		sum := sha256.Sum256([]byte(inline))
		want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
		if !strings.Contains(directive, want) {
			t.Fatalf("inline script %q is not allowed by %s", inline, directive)
		}
	}
	// The external module is covered by 'self'; only the two inline scripts
	// may be hashed.
	if count := strings.Count(directive, "'sha256-"); count != 2 {
		t.Fatalf("expected two hashed scripts, got %d in %s", count, directive)
	}
	for _, want := range []string{"default-src 'self'", "object-src 'none'", "frame-ancestors 'none'"} {
		if !strings.Contains(policy, want) {
			t.Fatalf("policy is missing %q: %s", want, policy)
		}
	}
}

func TestResponsesCarryFramingAndReferrerGuards(t *testing.T) {
	_, server := newTestServer(t)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, localRequest(http.MethodGet, "/", nil))
	for header, want := range map[string]string{
		"X-Frame-Options":            "DENY",
		"Referrer-Policy":            "no-referrer",
		"Cross-Origin-Opener-Policy": "same-origin",
	} {
		if got := response.Header().Get(header); got != want {
			t.Fatalf("%s = %q, want %q", header, got, want)
		}
	}
	if response.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("console responses must carry a content security policy")
	}
}

// The policy is derived from the shipped HTML, so a regexp that stops matching
// a future index.html would silently drop the boot scripts from the policy and
// blank the console. Check the real embedded file, not a fixture.
func TestShippedConsolePolicyAllowsItsBootScripts(t *testing.T) {
	index, err := fs.ReadFile(webassets.FS(), "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	policy := contentSecurityPolicy(index)
	for _, inline := range []string{"cline-pass-switcher-theme", "cline-pass-switcher-root-snapshot-v1"} {
		if !strings.Contains(string(index), inline) {
			t.Fatalf("shipped index.html no longer contains %q; update this test", inline)
		}
	}
	if count := strings.Count(policy, "'sha256-"); count < 2 {
		t.Fatalf("shipped console should hash its inline boot scripts, got %d: %s", count, policy)
	}
}

// A model with no saved routing preferences used to serialize upstreams and
// exclude as null, which forced every consumer to handle two shapes.
func TestModelsEndpointReportsEmptyListsInsteadOfNull(t *testing.T) {
	_, server := newTestServer(t)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, localRequest(http.MethodGet, "/api/models", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/models: %d", response.Code)
	}
	var payload struct {
		Subscription []struct {
			ID     string `json:"id"`
			Config struct {
				Upstreams []string `json:"upstreams"`
				Exclude   []string `json:"exclude"`
			} `json:"config"`
		} `json:"subscription"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Subscription) == 0 {
		t.Fatal("expected the default subscription list")
	}
	for _, entry := range payload.Subscription {
		if entry.Config.Upstreams == nil || entry.Config.Exclude == nil {
			t.Fatalf("%s: lists must be empty arrays, got %s", entry.ID, response.Body.String())
		}
	}
}
