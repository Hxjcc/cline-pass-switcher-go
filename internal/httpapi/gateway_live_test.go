package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

// Opt in against a loopback credential-injecting proxy. The test stores only a
// placeholder credential; the real Cline key remains in the proxy's memory.
func TestLiveGatewayRoutingViaProxy(t *testing.T) {
	base := os.Getenv("CLINE_ROUTING_TEST_PROXY")
	if base == "" {
		t.Skip("set CLINE_ROUTING_TEST_PROXY to a loopback credential-injecting proxy for paid live validation")
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || net.ParseIP(parsed.Hostname()) == nil || !net.ParseIP(parsed.Hostname()).IsLoopback() {
		t.Fatal("live routing proxy must be a loopback HTTP endpoint")
	}
	const modelID = "cline-pass/deepseek-v4.1-flash-1017"
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(cfg *model.Config) {
		cfg.UpstreamBase = strings.TrimRight(base, "/")
		cfg.Accounts = []model.Account{{Name: "live-test", Key: "placeholder", Enabled: true}}
		cfg.KnownModels = []string{modelID}
	}); err != nil {
		t.Fatal(err)
	}
	catalog := cachedGatewayCatalog()
	catalog.Model = "deepseek/deepseek-v4.1-flash-1017"
	_, err = st.UpdateModelMeta(modelID, func(meta *model.ModelMeta) {
		disabled := false
		meta.Pipeline = "planner"
		meta.Pinnable = &disabled
		meta.PinReason = "single_provider"
		meta.CanonicalSlug = "private/deepseek-v4p1-flash-1017-contributor"
		meta.Upstreams = []string{"openai-compatible-private"}
		meta.GatewayCatalog = catalog
		meta.ReasoningEfforts = []string{"none", "low", "high", "max"}
	})
	if err != nil {
		t.Fatal(err)
	}
	send := func(path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := localRequest(http.MethodPost, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		server.ServeHTTP(out, req)
		if out.Code != 200 {
			t.Fatalf("%s returned HTTP %d: %.1200s", path, out.Code, out.Body.String())
		}
		return out
	}
	verified := send("/api/verify-routing", map[string]any{"model": modelID, "config": model.PerModelConfig{Upstreams: []string{"deepseek"}, GatewayHas: []string{"vision", "implicit-caching"}}, "apply": true})
	if !strings.Contains(verified.Body.String(), `"applied":true`) {
		t.Fatalf("live routing verification failed: %.1600s", verified.Body.String())
	}
	send("/v1/chat/completions", map[string]any{"model": modelID, "messages": []any{map[string]any{"role": "user", "content": "Reply only OK"}}, "max_tokens": 256, "reasoning_effort": "none"})
	stream := send("/v1/responses", map[string]any{"model": modelID, "input": "Reply only OK", "stream": true, "max_output_tokens": 256, "reasoning": map[string]any{"effort": "none"}})
	if !strings.Contains(stream.Body.String(), "response.completed") {
		t.Fatalf("live Responses stream did not complete: %.1200s", stream.Body.String())
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	for _, entry := range st.History() {
		if entry.Kind == "routing-check" {
			continue
		}
		if entry.Error != nil || entry.Resolved != "deepseek" || entry.RoutingStatus != "matched" {
			t.Fatalf("live request did not honor routing: %+v", entry)
		}
		var cost any = "unknown"
		if entry.Usage != nil && entry.Usage.Cost != nil {
			cost = *entry.Usage.Cost
		}
		t.Logf("kind=%s provider=%s has=%v cost=%v", entry.Kind, entry.Resolved, entry.GatewayHas, cost)
	}
}
