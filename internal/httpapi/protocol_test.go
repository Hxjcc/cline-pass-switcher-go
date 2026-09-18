package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

func TestUnsupportedResponsesNeverReachUpstream(t *testing.T) {
	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(500) }))
	defer up.Close()
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(c *model.Config) {
		c.UpstreamBase = up.URL
		c.Accounts = []model.Account{{Name: "test", Key: "test", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/responses", "/v1/responses/compact"} {
		for _, body := range []string{
			`{"model":"test","input":"hi","conversation":"conv_123","stream":true}`,
			`{"model":"test","input":"hi","tools":[{"type":"web_search"}],"tool_choice":{"type":"web_search"}}`,
			`{"model":"test","input":"hi","tools":[{"type":"web_search"}],"tool_choice":"required"}`,
			`{"model":"test","input":[{"role":"user","content":[{"type":"input_file","file_id":"file_123"}]}]}`,
		} {
			w := httptest.NewRecorder()
			server.ServeHTTP(w, localRequest("POST", path, strings.NewReader(body)))
			var payload struct {
				Error struct{ Type, Code, Param string }
			}
			if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if w.Code != 400 || payload.Error.Type != "invalid_request_error" || payload.Error.Code != "unsupported_feature" || payload.Error.Param == "" {
				t.Fatalf("incorrect error: %d %s", w.Code, w.Body.String())
			}
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("unsupported requests reached upstream %d times", hits.Load())
	}
}

func TestGreetingWithAutomaticallyAdvertisedWebSearch(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			var hits atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				tools, _ := body["tools"].([]any)
				if len(tools) != 14 {
					t.Errorf("expected 14 client functions, got %d", len(tools))
				}
				for _, raw := range tools {
					if raw.(map[string]any)["type"] != "function" {
						t.Error("hosted tool reached Chat")
					}
				}
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"你好！\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				} else {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"choices":[{"message":{"content":"你好！"},"finish_reason":"stop"}]}`)
				}
			}))
			defer up.Close()
			st, server := newTestServer(t)
			if err := st.UpdateConfig(func(c *model.Config) {
				c.UpstreamBase = up.URL
				c.Accounts = []model.Account{{Name: "test", Key: "test", Enabled: true}}
			}); err != nil {
				t.Fatal(err)
			}
			tools := []any{}
			for i := range 14 {
				tools = append(tools, map[string]any{"type": "function", "name": fmt.Sprintf("tool_%d", i)})
			}
			tools = append(tools, map[string]any{"type": "web_search"})
			body, _ := json.Marshal(map[string]any{"model": "test", "input": "你好", "tools": tools, "tool_choice": "auto", "stream": stream})
			w := httptest.NewRecorder()
			server.ServeHTTP(w, localRequest("POST", "/v1/responses", strings.NewReader(string(body))))
			if w.Code != 200 || !strings.Contains(w.Body.String(), "你好！") || hits.Load() != 1 {
				t.Fatalf("greeting blocked: %d %s hits=%d", w.Code, w.Body.String(), hits.Load())
			}
			if stream && !strings.Contains(w.Body.String(), "event: response.completed") {
				t.Fatal("stream did not complete")
			}
		})
	}
}
