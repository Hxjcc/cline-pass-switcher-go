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

const titleOutputFormat = `"text":{"format":{"type":"json_schema","name":"title","strict":true,"schema":{"type":"object","properties":{"title":{"type":"string","maxLength":36,"minLength":1}},"required":["title"],"additionalProperties":false}}}`

func responseOutputTextFromBody(t *testing.T, body []byte) string {
	t.Helper()
	var result struct {
		Output []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Output) == 0 || len(result.Output[0].Content) == 0 {
		return ""
	}
	return result.Output[0].Content[0].Text
}

func TestResponsesStrictSchemaEndToEnd(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, valid := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%v/valid=%v", stream, valid), func(t *testing.T) {
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
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
						"message": map[string]any{"content": content}, "finish_reason": "stop",
					}}})
				}))
				defer up.Close()
				st, server := newTestServer(t)
				if err := st.UpdateConfig(func(c *model.Config) {
					c.UpstreamBase = up.URL
					c.Accounts = []model.Account{{Name: "test", Key: "test", Enabled: true}}
					c.PerModel["test"] = model.PerModelConfig{Upstreams: []string{"first", "second"}}
				}); err != nil {
					t.Fatal(err)
				}
				body := fmt.Sprintf(`{"model":"test","input":"hi","stream":%v,%s}`, stream, strictOutputFormat)
				w := httptest.NewRecorder()
				server.ServeHTTP(w, localRequest("POST", "/v1/responses", strings.NewReader(body)))
				if !stream {
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
				wantHits := int32(1)
				if !valid {
					wantHits = 2
				}
				if hits.Load() != wantHits {
					t.Fatalf("upstream called %d times, want %d", hits.Load(), wantHits)
				}
				history := st.Metadata().History
				if len(history) != 1 || (history[0].Error == nil) != valid {
					t.Fatalf("history disagrees with schema outcome: %#v", history)
				}
				if history[0].Stream != stream {
					t.Fatalf("history stream flag = %v, want %v", history[0].Stream, stream)
				}
			})
		}
	}
}

func TestResponsesStrictSchemaWrapsPlainTitle(t *testing.T) {
	const title = "生成可享高级权益的115安装包"
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			var hits atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
					"message": map[string]any{"content": title}, "finish_reason": "stop",
				}}})
			}))
			defer up.Close()
			st, server := newTestServer(t)
			if err := st.UpdateConfig(func(c *model.Config) {
				c.UpstreamBase = up.URL
				c.Accounts = []model.Account{{Name: "test", Key: "test", Enabled: true}}
				c.PerModel["test"] = model.PerModelConfig{Upstreams: []string{"first"}}
			}); err != nil {
				t.Fatal(err)
			}
			body := fmt.Sprintf(`{"model":"test","input":"hi","stream":%v,%s}`, stream, titleOutputFormat)
			w := httptest.NewRecorder()
			server.ServeHTTP(w, localRequest("POST", "/v1/responses", strings.NewReader(body)))
			if w.Code != 200 {
				t.Fatalf("wrapped title rejected: %d %s", w.Code, w.Body.String())
			}
			if hits.Load() != 1 {
				t.Fatalf("plain title needed %d upstream calls, want 1", hits.Load())
			}
			want := `{"title":"` + title + `"}`
			if stream {
				if !strings.Contains(w.Body.String(), "response.completed") || strings.Contains(w.Body.String(), "response.failed") {
					t.Fatalf("wrong stream terminal: %s", w.Body.String())
				}
				if !strings.Contains(w.Body.String(), strings.ReplaceAll(want, `"`, `\"`)) {
					t.Fatalf("wrapped title missing from stream: %s", w.Body.String())
				}
				return
			}
			if got := responseOutputTextFromBody(t, w.Body.Bytes()); got != want {
				t.Fatalf("wrapped title missing: %s", w.Body.String())
			}
		})
	}
}

func TestResponsesStrictSchemaRepairsRefusalText(t *testing.T) {
	const refusal = "抱歉，我无法协助这个请求。修改安装程序以绕过付费验证、解锁高级权益属于破解软件的行为，这涉及侵犯知识产权和服务盗用，建议通过官方渠道购买会员。"
	const repairedTitle = `{"title":"无法协助破解请求"}`
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			var hits atomic.Int32
			var repairRequest map[string]any
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempt := hits.Add(1)
				payload, _ := io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				if attempt == 1 {
					json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
						"message": map[string]any{"content": refusal}, "finish_reason": "stop",
					}}})
					return
				}
				_ = json.Unmarshal(payload, &repairRequest)
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
					"message": map[string]any{"content": repairedTitle}, "finish_reason": "stop",
				}}})
			}))
			defer up.Close()
			st, server := newTestServer(t)
			if err := st.UpdateConfig(func(c *model.Config) {
				c.UpstreamBase = up.URL
				c.Accounts = []model.Account{{Name: "test", Key: "test", Enabled: true}}
				c.PerModel["test"] = model.PerModelConfig{Upstreams: []string{"first"}}
			}); err != nil {
				t.Fatal(err)
			}
			body := fmt.Sprintf(`{"model":"test","input":"hi","stream":%v,%s}`, stream, titleOutputFormat)
			w := httptest.NewRecorder()
			server.ServeHTTP(w, localRequest("POST", "/v1/responses", strings.NewReader(body)))
			if w.Code != 200 {
				t.Fatalf("repaired title rejected: %d %s", w.Code, w.Body.String())
			}
			if hits.Load() != 2 {
				t.Fatalf("repair used %d upstream calls, want 2", hits.Load())
			}
			messages, _ := repairRequest["messages"].([]any)
			if len(messages) != 1 {
				t.Fatalf("repair request carried conversation history: %#v", repairRequest["messages"])
			}
			prompt, _ := messages[0].(map[string]any)["content"].(string)
			if !strings.Contains(prompt, "JSON Schema") || !strings.Contains(prompt, "无法协助") {
				t.Fatalf("repair prompt missing schema or text: %q", prompt)
			}
			if effort, _ := repairRequest["reasoning_effort"].(string); effort != "low" {
				t.Fatalf("repair effort = %q, want low", effort)
			}
			if stream {
				if !strings.Contains(w.Body.String(), "response.completed") || strings.Contains(w.Body.String(), "response.failed") {
					t.Fatalf("wrong stream terminal: %s", w.Body.String())
				}
				if !strings.Contains(w.Body.String(), strings.ReplaceAll(repairedTitle, `"`, `\"`)) {
					t.Fatalf("repaired title missing from stream: %s", w.Body.String())
				}
				return
			}
			if got := responseOutputTextFromBody(t, w.Body.Bytes()); got != repairedTitle {
				t.Fatalf("repaired title missing: %s", w.Body.String())
			}
		})
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
	server.ServeHTTP(w, localRequest("POST", "/v1/responses", strings.NewReader(`{"model":"test","input":"hi","text":{"format":{"type":"json_schema","strict":true,"schema":{"type":"invalid"}}}}`)))
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
