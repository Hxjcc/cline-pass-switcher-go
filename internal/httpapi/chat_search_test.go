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

func forwardedToolTypes(body map[string]any) []string {
	types := make([]string, 0, 4)
	for _, raw := range jsonx.Slice(body["tools"]) {
		if tool := jsonx.Map(raw); tool != nil {
			types = append(types, jsonx.String(tool["type"]))
		}
	}
	return types
}

func forwardedFunctionNames(body map[string]any) []string {
	names := make([]string, 0, 4)
	for _, raw := range jsonx.Slice(body["tools"]) {
		tool := jsonx.Map(raw)
		if tool == nil || jsonx.String(tool["type"]) != "function" {
			continue
		}
		names = append(names, jsonx.String(jsonx.Map(tool["function"])["name"]))
	}
	return names
}

func firstMessageContent(body map[string]any) string {
	messages := jsonx.Slice(body["messages"])
	if len(messages) == 0 {
		return ""
	}
	return jsonx.String(jsonx.Map(messages[0])["content"])
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

// Chat clients declare their own search as a function tool (DeepSeek Harness
// does). The proxy replaces that declaration with the gateway search on
// planner models, and never adds one the client did not ask for.
func TestChatSearchFollowsClientDeclaration(t *testing.T) {
	const webSearchTool = `{"type":"function","function":{"name":"web_search","parameters":{"type":"object","properties":{"queries":{"type":"array","items":{"type":"string"}}}}}}`
	const shellTool = `{"type":"function","function":{"name":"shell","parameters":{"type":"object"}}}`
	for _, tc := range []struct {
		name        string
		pipeline    string
		upstream    string
		tools       string
		wantGateway bool
		wantPolicy  bool
		wantFuncs   []string
	}{
		{"declared/planner", "", "exa", webSearchTool, true, true, nil},
		{"undeclared/planner", "", "exa", shellTool, false, false, []string{"shell"}},
		{"declared/search-off", "", "", webSearchTool, false, false, []string{"web_search"}},
		{"declared/direct", "direct", "exa", webSearchTool, false, false, []string{"web_search"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			received := make(chan map[string]any, 1)
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
				c.WebSearchUpstream = tc.upstream
			}); err != nil {
				t.Fatal(err)
			}
			if tc.pipeline != "" {
				if _, err := st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) {
					meta.Pipeline = tc.pipeline
				}); err != nil {
					t.Fatal(err)
				}
			}

			body := `{"model":"cline-pass/test","messages":[{"role":"user","content":"hi"}],"tools":[` + tc.tools + `]}`
			w := httptest.NewRecorder()
			server.ServeHTTP(w, localRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
			if w.Code != http.StatusOK {
				t.Fatalf("chat request failed: %d %s", w.Code, w.Body.String())
			}
			forwarded := <-received

			hasGateway := false
			for _, toolType := range forwardedToolTypes(forwarded) {
				if toolType == "vercel:exa_search" {
					hasGateway = true
				}
			}
			if hasGateway != tc.wantGateway {
				t.Fatalf("gateway tool=%v want %v: %#v", hasGateway, tc.wantGateway, forwarded["tools"])
			}
			if policy := strings.Contains(firstMessageContent(forwarded), "Web search policy"); policy != tc.wantPolicy {
				t.Fatalf("policy=%v want %v: %q", policy, tc.wantPolicy, firstMessageContent(forwarded))
			}
			if got := forwardedFunctionNames(forwarded); !sameStrings(got, tc.wantFuncs) {
				t.Fatalf("function tools=%v want %v", got, tc.wantFuncs)
			}
		})
	}
}
