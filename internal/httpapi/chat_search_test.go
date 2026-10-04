package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

func forwardedToolTypes(t *testing.T, body map[string]any) []string {
	t.Helper()
	types := make([]string, 0, 4)
	for _, raw := range jsonx.Slice(body["tools"]) {
		if tool, ok := raw.(map[string]any); ok {
			if name, ok := tool["type"].(string); ok {
				types = append(types, name)
			}
		}
	}
	return types
}

// Chat clients like DeepSeek Harness cannot run the gateway's private search
// tool themselves, so the proxy declares it on their behalf for planner
// routes; the gateway executes the search inside its own loop.
func TestChatPathDeclaresGatewaySearch(t *testing.T) {
	received := make(chan map[string]any, 4)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		received <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-1","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer up.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(c *model.Config) {
		c.UpstreamBase = up.URL
		c.Accounts = []model.Account{{Name: "main", Key: "key", Enabled: true}}
		c.WebSearchUpstream = "exa"
	}); err != nil {
		t.Fatal(err)
	}
	const clientBody = `{"model":"cline-pass/test","messages":[{"role":"user","content":"hi"}],` +
		`"tools":[{"type":"function","function":{"name":"shell","parameters":{"type":"object"}}}]}`

	w := httptest.NewRecorder()
	server.ServeHTTP(w, localRequest("POST", "/v1/chat/completions", strings.NewReader(clientBody)))
	if w.Code != http.StatusOK {
		t.Fatalf("chat request failed: %d %s", w.Code, w.Body.String())
	}
	found := false
	for _, tool := range forwardedToolTypes(t, <-received) {
		if tool == "vercel:exa_search" {
			found = true
		}
	}
	if !found {
		t.Fatal("planner chat request must carry the gateway search tool")
	}

	// Direct routes forward the tool list to the provider, which rejects the
	// gateway's private tool ids, so nothing may be declared there.
	if _, err := st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) {
		meta.Pipeline = "direct"
	}); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	server.ServeHTTP(w, localRequest("POST", "/v1/chat/completions", strings.NewReader(clientBody)))
	if w.Code != http.StatusOK {
		t.Fatalf("direct chat request failed: %d %s", w.Code, w.Body.String())
	}
	for _, tool := range forwardedToolTypes(t, <-received) {
		if tool == "vercel:exa_search" {
			t.Fatal("direct route must not declare the gateway search tool")
		}
	}

	// With the search upstream switched off nothing is added, whatever the
	// route is.
	if err := st.UpdateConfig(func(c *model.Config) { c.WebSearchUpstream = "" }); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) {
		meta.Pipeline = ""
	}); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	server.ServeHTTP(w, localRequest("POST", "/v1/chat/completions", strings.NewReader(clientBody)))
	if w.Code != http.StatusOK {
		t.Fatalf("chat request failed: %d %s", w.Code, w.Body.String())
	}
	for _, tool := range forwardedToolTypes(t, <-received) {
		if tool == "vercel:exa_search" {
			t.Fatal("disabled search upstream must stay off")
		}
	}
}
