package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

func TestToolSummaryDoesNotStoreArgumentsOrOutput(t *testing.T) {
	st, server := newTestServer(t)
	calls := []any{
		map[string]any{"index": 0, "function": map[string]any{"name": "agent__exec_command", "arguments": "PRIVATE_ARGUMENT"}},
		map[string]any{"index": 1, "function": map[string]any{"name": "apply_patch", "arguments": "PRIVATE_PATCH"}},
	}
	body := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "old"}, map[string]any{"role": "tool", "content": `{"is_error":true,"text":"PRIVATE_RESULT"}`}}}
	ctx := context.WithValue(withToolFailures(context.Background(), body), clientToolNamesKey{}, map[string]string{"agent__exec_command": "agent.exec_command"})
	for _, stream := range []bool{false, true} {
		e := model.HistoryEntry{Model: "test", Stream: stream}
		if stream {
			stats := newStreamStats(time.Now())
			raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": calls}}}})
			stats.Observe(append(append([]byte("data: "), raw...), []byte("\n\n")...))
			applyStreamStats(&e, stats)
		} else {
			applyChatStats(&e, map[string]any{"choices": []any{map[string]any{"message": map[string]any{"tool_calls": calls}}}}, 1)
		}
		server.record(ctx, e)
	}
	for _, e := range st.History() {
		raw, _ := json.Marshal(e)
		if strings.Contains(string(raw), "PRIVATE_") || e.Tools == nil || e.Tools.Calls != 2 || e.Tools.ReportedFailures != 1 || e.Tools.Names["agent.exec_command"] != 1 {
			t.Fatalf("bad tool summary: %s", raw)
		}
	}
}

func TestDynamicResponseHeadersAreSanitized(t *testing.T) {
	r := httptest.NewRecorder()
	result := chainResult{}
	result.Routing.ResolvedProvider = "provider\r\nInjected: yes"
	result.Routing.CanonicalSlug = strings.Repeat("x", 500) + "\n"
	setResponsesHeaders(r, []string{"one\r\ntwo"}, result, "")
	for _, name := range []string{"X-Cline-Actual-Upstream", "X-Cline-Target-Upstream", "X-Cline-Canonical-Model"} {
		v := r.Header().Get(name)
		if strings.ContainsAny(v, "\r\n") || len(v) > 80 {
			t.Fatalf("unsafe %s: %q", name, v)
		}
	}
}

func TestPublicSecuritySaveRequiresIsolationAndValidOrigin(t *testing.T) {
	_, server := newTestServer(t)
	for _, body := range []string{`{"publicBaseUrl":"https://example.com"}`, `{"adminKey":"same","proxyKey":"same","publicBaseUrl":"https://example.com"}`, `{"adminKey":"console","publicBaseUrl":"https://example.com/path"}`} {
		w := httptest.NewRecorder()
		server.ServeHTTP(w, localRequest("POST", "/api/security", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("bad security settings accepted: %d %s", w.Code, w.Body)
		}
	}
	if server.store.Config().PublicBaseURL != "" {
		t.Fatal("failed validation changed the config")
	}
}

func TestIssuedKeyCannotBePromotedByNormalizingACollision(t *testing.T) {
	st, _ := newTestServer(t)
	if err := st.UpdateConfig(func(c *model.Config) {
		c.AdminKey = "console"
		c.ProxyKeys = []model.ProxyKeyGrant{{ID: "issued", Key: "client", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateConfig(func(c *model.Config) { c.AdminKey = "client" }); err == nil {
		t.Fatal("client key became an admin key")
	}
	if cfg := st.Config(); cfg.AdminKey != "console" || len(cfg.ProxyKeys) != 1 {
		t.Fatal("rejected update changed credentials")
	}
}
