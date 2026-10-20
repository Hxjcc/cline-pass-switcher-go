package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

func TestStreamingAttemptsRetainBilledFailures(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/v1/responses"} {
		for _, firstSSE := range []bool{false, true} {
			for _, ending := range []string{"failure", "buffered", "stream"} {
				t.Run(fmt.Sprintf("%s/firstSSE=%v/%s", path, firstSSE, ending), func(t *testing.T) {
					var calls atomic.Int32
					us := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if strings.Contains(r.URL.Path, "/users/me/plan") {
							http.NotFound(w, r)
							return
						}
						call := calls.Add(1)
						if call == 1 {
							payload := `{"error":{"message":"billed failure","type":"server_error","code":503},"usage":{"prompt_tokens":100,"completion_tokens":20,"cost":0.4}}`
							if firstSSE {
								w.Header().Set("Content-Type", "text/event-stream")
								fmt.Fprintf(w, "data: %s\n\n", payload)
							} else {
								w.Header().Set("Content-Type", "application/json")
								w.WriteHeader(503)
								fmt.Fprint(w, payload)
							}
							return
						}
						if ending == "buffered" {
							w.Header().Set("Content-Type", "application/json")
							fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"cost":0.1}}`)
						} else {
							w.Header().Set("Content-Type", "text/event-stream")
							fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"cost\":0.1}}\n\ndata: [DONE]\n\n")
						}
					}))
					defer us.Close()
					st, server := newTestServer(t)
					configureKeys(t, st, us.URL, func(c *model.Config) {
						c.ProxyKey = "master"
						c.ProxyKeys = []model.ProxyKeyGrant{{ID: "limited", Key: "issued", Enabled: true, SpendLimitUSD: 0.35}}
						if ending != "failure" {
							c.PerModel["test"] = model.PerModelConfig{Upstreams: []string{"first", "second"}}
						}
					})
					input := `"input":"hi"`
					if strings.Contains(path, "chat/completions") {
						input = `"messages":[{"role":"user","content":"hi"}]`
					}
					send := func() *httptest.ResponseRecorder {
						r := localRequest("POST", path, strings.NewReader(`{"model":"test","stream":true,`+input+`}`))
						r.Header.Set("Authorization", "Bearer issued")
						w := httptest.NewRecorder()
						server.ServeHTTP(w, r)
						return w
					}
					w := send()
					server.shares.running.Wait()
					wantCalls, wantCost, wantPrompt := int32(1), int64(400000), int64(100)
					if ending != "failure" {
						wantCalls, wantCost, wantPrompt = 2, 500000, 110
						if w.Code != 200 {
							t.Fatalf("failover failed: %d %s", w.Code, w.Body)
						}
					} else if w.Code != 503 {
						t.Fatalf("wrong failure status: %d %s", w.Code, w.Body)
					}
					history := st.History()
					if len(history) != 1 || history[0].Usage == nil || history[0].Usage.PromptTokens != wantPrompt {
						t.Fatalf("failed-attempt usage lost: %+v", history)
					}
					if u := st.KeyUsage()["limited"]; u.SpentMicroUSD != wantCost || u.Requests != 1 {
						t.Fatalf("wrong billed usage: %+v, want %d", u, wantCost)
					}
					if refused := send(); refused.Code != 429 || calls.Load() != wantCalls {
						t.Fatalf("spent key reached upstream: status=%d calls=%d", refused.Code, calls.Load())
					}
				})
			}
		}
	}
}

func TestStructuredRepairRetainsClientOutputCap(t *testing.T) {
	for _, effort := range []string{"none", "max"} {
		t.Run(effort, func(t *testing.T) {
			var calls atomic.Int32
			us := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				decoder := json.NewDecoder(r.Body)
				decoder.UseNumber()
				if err := decoder.Decode(&body); err != nil {
					t.Error(err)
					http.Error(w, "invalid body", 400)
					return
				}
				if body["max_tokens"] != json.Number("16") || body["reasoning_effort"] != effort {
					t.Errorf("pass %d exceeded budget or enabled reasoning: cap=%v effort=%v", calls.Load()+1, body["max_tokens"], body["reasoning_effort"])
				}
				text := `{"wrong":"red"}`
				if calls.Add(1) > 1 {
					text = `{"color":"red"}`
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": text}, "finish_reason": "stop"}}})
			}))
			defer us.Close()
			st, server := newTestServer(t)
			configureKeys(t, st, us.URL, func(*model.Config) {})
			if _, err := st.UpdateModelMeta("test", func(m *model.ModelMeta) { m.ReasoningEfforts = []string{"none", "max"} }); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			server.ServeHTTP(w, localRequest("POST", "/v1/responses", strings.NewReader(fmt.Sprintf(`{"model":"test","input":"hi","max_output_tokens":16,"reasoning":{"effort":%q},`+strictOutputFormat+`}`, effort))))
			if w.Code != 200 || calls.Load() != 2 {
				t.Fatalf("repair did not complete within the cap: %d calls=%d %s", w.Code, calls.Load(), w.Body)
			}
		})
	}
}
