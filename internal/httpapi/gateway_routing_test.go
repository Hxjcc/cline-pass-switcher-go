package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/jsonx"
	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

func cachedGatewayCatalog() *model.GatewayCatalog {
	return &model.GatewayCatalog{Model: "deepseek/test", Source: "https://example.test/endpoints", FetchedAt: time.Now().UnixMilli(), Providers: []model.GatewayProvider{{Slug: "deepseek", Name: "DeepSeek", Has: []string{"vision", "implicit-caching", "tool-use"}}, {Slug: "other", Name: "Other", Has: []string{"tool-use"}}}}
}

func TestGatewayRoutingApplyAndEveryGenerationPath(t *testing.T) {
	var calls atomic.Int32
	var ignore atomic.Bool
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body["model"] != "cline-pass/test" {
			t.Errorf("model prefix lost: %v", body["model"])
		}
		gateway := jsonx.Map(jsonx.Map(body["providerOptions"])["gateway"])
		has := jsonx.Slice(gateway["has"])
		only := jsonx.Slice(gateway["only"])
		if len(has) == 0 || len(only) != 1 {
			t.Errorf("routing extensions lost: %+v", body)
		}
		target := ""
		if len(only) > 0 {
			target, _ = only[0].(string)
		}
		if strings.HasPrefix(target, "zzz-") {
			w.Header().Set("Content-Type", "text/event-stream")
			raw, _ := json.Marshal(map[string]any{"error": map[string]any{"message": "No available providers match the 'only' filter: " + target + ". Available providers are: deepseek."}})
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
			return
		}
		provider := "deepseek"
		if ignore.Load() {
			provider = "openai-compatible-private"
		}
		content := "OK"
		if strings.Contains(fmt.Sprint(body["messages"]), "compaction task") {
			content = "## Objective\nTest\n## Work State\nDone\n## Next Move\nContinue\n## Relevant Files\nNone"
		}
		if body["response_format"] != nil {
			content = "not JSON"
			if strings.Contains(fmt.Sprint(body["messages"]), "Rewrite the content below") {
				content = `{"number":1}`
			}
		}
		metadata := map[string]any{"gateway": map[string]any{"routing": map[string]any{"finalProvider": provider, "resolvedProvider": provider, "canonicalSlug": "deepseek/test"}}}
		part := map[string]any{"role": "assistant", "content": content, "provider_metadata": metadata}
		choice := map[string]any{"index": 0, "finish_reason": "stop"}
		stream, _ := body["stream"].(bool)
		if stream {
			choice["delta"] = part
		} else {
			choice["message"] = part
		}
		root := map[string]any{"id": "test", "model": "cline-pass/test", "choices": []any{choice}, "usage": map[string]any{"prompt_tokens": 7, "completion_tokens": 1, "total_tokens": 8, "cost": 0.000001}}
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			raw, _ := json.Marshal(root)
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
		} else {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(root)
		}
	}))
	defer upstreamServer.Close()
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(cfg *model.Config) {
		cfg.UpstreamBase = upstreamServer.URL
		cfg.Accounts = []model.Account{{Name: "one", Key: "placeholder", Enabled: true}}
		cfg.KnownModels = []string{"cline-pass/test"}
	}); err != nil {
		t.Fatal(err)
	}
	_, err := st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) {
		value := false
		meta.Pinnable = &value
		meta.PinReason = "single_provider"
		meta.Pipeline = "planner"
		meta.CanonicalSlug = "private/test"
		meta.Upstreams = []string{"openai-compatible-private"}
		meta.GatewayCatalog = cachedGatewayCatalog()
	})
	if err != nil {
		t.Fatal(err)
	}
	send := func(path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		request := localRequest(http.MethodPost, path, bytes.NewReader(raw))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response
	}
	cfg := model.PerModelConfig{Upstreams: []string{"deepseek"}, GatewayHas: []string{"vision", "implicit-caching"}}
	response := send("/api/verify-routing", map[string]any{"model": "cline-pass/test", "config": cfg, "apply": true})
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"applied":true`) {
		t.Fatalf("verification=%d %s", response.Code, response.Body.String())
	}
	if !slices.Equal(st.ModelConfig("cline-pass/test").GatewayHas, []string{"implicit-caching", "vision"}) {
		t.Fatal("verified filter was not applied")
	}
	metaResponse := httptest.NewRecorder()
	server.ServeHTTP(metaResponse, localRequest(http.MethodGet, "/api/models", nil))
	if !strings.Contains(metaResponse.Body.String(), `"pinnable":true`) {
		t.Fatalf("single filtered provider stayed disabled: %s", metaResponse.Body.String())
	}
	clientOptions := map[string]any{"gateway": map[string]any{"has": []string{"tool-use"}}}
	for _, test := range []struct {
		name, path string
		body       map[string]any
	}{
		{"chat", "/v1/chat/completions", map[string]any{"model": "cline-pass/test", "messages": []any{map[string]any{"role": "user", "content": "hi"}}}},
		{"chat-stream", "/v1/chat/completions", map[string]any{"model": "cline-pass/test", "messages": []any{map[string]any{"role": "user", "content": "hi"}}, "stream": true}},
		{"responses", "/v1/responses", map[string]any{"model": "cline-pass/test", "input": "hi"}},
		{"responses-stream", "/v1/responses", map[string]any{"model": "cline-pass/test", "input": "hi", "stream": true}},
		{"compact", "/v1/responses/compact", map[string]any{"model": "cline-pass/test", "input": "hi"}},
		{"compact-trigger", "/v1/responses", map[string]any{"model": "cline-pass/test", "input": []any{map[string]any{"type": "compaction_trigger"}, map[string]any{"role": "user", "content": "hi"}}}},
		{"repair", "/v1/responses", map[string]any{"model": "cline-pass/test", "input": "hi", "text": map[string]any{"format": map[string]any{"type": "json_schema", "name": "number", "schema": map[string]any{"type": "object", "properties": map[string]any{"number": map[string]any{"type": "number"}}, "required": []string{"number"}, "additionalProperties": false}}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.body["providerOptions"] = clientOptions
			response := send(test.path, test.body)
			if response.Code != 200 {
				t.Fatalf("generation=%d %s", response.Code, response.Body.String())
			}
		})
	}
	before := calls.Load()
	conflict := send("/v1/chat/completions", map[string]any{"model": "cline-pass/test", "messages": []any{}, "providerOptions": map[string]any{"gateway": map[string]any{"only": []string{"not-allowed"}}}})
	if conflict.Code != 400 || calls.Load() != before {
		t.Fatalf("conflict reached upstream: %d %s", conflict.Code, conflict.Body.String())
	}
	ignore.Store(true)
	draft := cfg
	draft.GatewayHas = []string{"vision"}
	failed := send("/api/verify-routing", map[string]any{"model": "cline-pass/test", "config": draft, "apply": true})
	if failed.Code != 200 || !strings.Contains(failed.Body.String(), `"applied":false`) {
		t.Fatalf("ignored pin applied: %s", failed.Body.String())
	}
	if !slices.Equal(st.ModelConfig("cline-pass/test").GatewayHas, []string{"implicit-caching", "vision"}) {
		t.Fatal("failed verification replaced working configuration")
	}
	unsafelySaved := send("/api/config", map[string]any{"perModel": map[string]any{"cline-pass/test": draft}})
	if unsafelySaved.Code != 400 {
		t.Fatalf("unverified configuration saved: %s", unsafelySaved.Body.String())
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	for _, entry := range st.History() {
		if entry.Kind == "routing-check" || entry.Error != nil {
			continue
		}
		if !slices.Contains(entry.GatewayHas, "tool-use") || !slices.Contains(entry.GatewayHas, "vision") || !slices.Contains(entry.TargetProviders, "deepseek") {
			t.Fatalf("history lost effective routing snapshot: %+v", entry)
		}
	}
}
