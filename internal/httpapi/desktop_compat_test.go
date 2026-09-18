package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

// Replays the history ChatGPT Desktop stores at the head of a delegated
// thread: a function_call_output whose call item never entered that thread.
func TestDelegatedThreadHistoryReachesUpstream(t *testing.T) {
	received := make(chan map[string]any, 1)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		received <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{\"id\":\"chatcmpl-1\",\"choices\":[{\"message\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}")
	}))
	defer up.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(c *model.Config) {
		c.UpstreamBase = up.URL
		c.Accounts = []model.Account{{Name: "main", Key: "key", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}

	body := "{\"model\":\"cline-pass/glm-5.3-flash\",\"input\":[{\"type\":\"function_call_output\",\"call_id\":\"fco_1\",\"name\":\"create_thread\",\"namespace\":\"codex_app\",\"output\":\"<codex_delegation><input>写一个测试</input></codex_delegation>\"}]}"
	w := httptest.NewRecorder()
	server.ServeHTTP(w, localRequest("POST", "/v1/responses", strings.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("delegated history rejected: %d %s", w.Code, w.Body.String())
	}
	upstreamBody := <-received
	messages, _ := upstreamBody["messages"].([]any)
	if len(messages) == 0 {
		t.Fatalf("upstream received no messages: %#v", upstreamBody)
	}
	first, _ := messages[0].(map[string]any)
	if first["role"] != "user" || !strings.Contains(first["content"].(string), "写一个测试") {
		t.Fatalf("delegated prompt was not forwarded as user text: %#v", first)
	}

	// The strict switch keeps the strict behavior available.
	if err := st.UpdateConfig(func(c *model.Config) { c.StrictToolHistory = true }); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	server.ServeHTTP(w, localRequest("POST", "/v1/responses", strings.NewReader(body)))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "orphan tool output") {
		t.Fatalf("strict mode should reject the request: %d %s", w.Code, w.Body.String())
	}
}
