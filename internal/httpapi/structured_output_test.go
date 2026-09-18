package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

const strictOutputFormat = `"text":{"format":{"type":"json_schema","name":"state","strict":true,"schema":{"type":"object","properties":{"color":{"type":"string"}},"required":["color"],"additionalProperties":false}}}`

func TestResponsesStrictSchemaEndToEnd(t *testing.T) {
	for _, mode := range []string{"buffered", "sse", "buffered-to-sse"} {
		for _, valid := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/valid=%v", mode, valid), func(t *testing.T) {
				var hits atomic.Int32
				up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					hits.Add(1)
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						return
					}
					format, _ := body["response_format"].(map[string]any)
					definition, _ := format["json_schema"].(map[string]any)
					if format["type"] != "json_schema" || definition["strict"] != true {
						t.Error("strict output format not forwarded")
					}
					content := `{"image_color":"red","status":"done"}`
					if valid {
						content = `{"color":"red"}`
					}
					field := "message"
					if mode == "sse" {
						field = "delta"
					}
					result := map[string]any{"choices": []any{map[string]any{field: map[string]any{"content": content}, "finish_reason": "stop"}}}
					if mode == "sse" {
						w.Header().Set("Content-Type", "text/event-stream")
						encoded, _ := json.Marshal(result)
						fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", encoded)
					} else {
						w.Header().Set("Content-Type", "application/json")
						json.NewEncoder(w).Encode(result)
					}
				}))
				defer up.Close()
				st, server := newTestServer(t)
				if err := st.UpdateConfig(func(c *model.Config) {
					c.UpstreamBase = up.URL
					c.Accounts = []model.Account{{Name: "test", Key: "test", Enabled: true}}
					c.PerModel["test"] = model.PerModelConfig{Upstreams: []string{"first", "second"}, PinMode: "strict"}
				}); err != nil {
					t.Fatal(err)
				}
				body := fmt.Sprintf(`{"model":"test","input":"hi","stream":%v,%s}`, mode != "buffered", strictOutputFormat)
				w := httptest.NewRecorder()
				server.ServeHTTP(w, httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body)))
				if mode == "buffered" {
					var result map[string]any
					if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					if valid {
						if w.Code != 200 || result["status"] != "completed" {
							t.Fatalf("valid response failed: %d %s", w.Code, w.Body.String())
						}
					} else {
						errorBody, _ := result["error"].(map[string]any)
						if w.Code != 502 || errorBody["code"] != "upstream_schema_validation_failed" || errorBody["type"] != "upstream_error" {
							t.Fatalf("schema failure hidden: %d %s", w.Code, w.Body.String())
						}
					}
				} else {
					want, forbidden := "event: response.failed", "event: response.completed"
					if valid {
						want, forbidden = forbidden, want
					}
					if w.Code != 200 || !strings.Contains(w.Body.String(), want) || strings.Contains(w.Body.String(), forbidden) {
						t.Fatalf("wrong stream terminal: %d %s", w.Code, w.Body.String())
					}
					if !valid && !strings.Contains(w.Body.String(), "upstream_schema_validation_failed") {
						t.Fatal("stream failure has no schema error code")
					}
				}
				if hits.Load() != 1 {
					t.Fatalf("validation retried generation %d times", hits.Load())
				}
				history := st.Metadata().History
				if len(history) != 1 || (history[0].Error == nil) != valid {
					t.Fatalf("history disagrees with schema outcome: %#v", history)
				}
			})
		}
	}
}

func TestInvalidStrictSchemaIsClientError(t *testing.T) {
	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.WriteString(w, "unexpected upstream call")
	}))
	defer up.Close()
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(c *model.Config) {
		c.UpstreamBase = up.URL
		c.Accounts = []model.Account{{Name: "test", Key: "test", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"test","input":"hi","text":{"format":{"type":"json_schema","strict":true,"schema":{"type":"invalid"}}}}`)))
	var result struct {
		Error struct{ Code, Type, Param string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != 400 || result.Error.Code != "invalid_json_schema" || result.Error.Type != "invalid_request_error" || result.Error.Param != "text.format.schema" || hits.Load() != 0 {
		t.Fatalf("wrong client error: %d %s; hits=%d", w.Code, w.Body.String(), hits.Load())
	}
}
