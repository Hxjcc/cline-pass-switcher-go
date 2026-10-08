package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

// Schema rejection does not undo the upstream spend. One client request must
// retain the usage of the initial generation and any attempted repair.
func TestStructuredResponsesAccountForEveryPass(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name, first, second string
			secondStatus, calls int
			microUSD            int64
			ok                  bool
		}{
			{"valid", `{"color":"red"}`, "", 200, 1, 400_000, true},
			{"local-wrap", "red", "", 200, 1, 400_000, true},
			{"repair-success", `{"wrong":"red"}`, `{"color":"red"}`, 200, 2, 500_000, true},
			{"repair-invalid", `{"wrong":"red"}`, `{"wrong":"blue"}`, 200, 2, 500_000, false},
			{"repair-unavailable", `{"wrong":"red"}`, "", 503, 2, 500_000, false},
			{"empty-output", "", "", 200, 1, 400_000, false},
		} {
			t.Run(fmt.Sprintf("stream=%v/%s", stream, tc.name), func(t *testing.T) {
				var calls atomic.Int32
				us := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(r.URL.Path, "/users/me/plan") {
						http.NotFound(w, r)
						return
					}
					call := calls.Add(1)
					if call == 1 && tc.name == "repair-success" {
						time.Sleep(15 * time.Millisecond)
					}
					content, cost, status := tc.first, 0.4, http.StatusOK
					if call > 1 {
						content, cost, status = tc.second, 0.1, tc.secondStatus
					}
					payload := map[string]any{
						"model":    "cline-pass/test",
						"provider": fmt.Sprintf("provider-%d", call),
						"usage":    map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15, "cost": cost},
					}
					if status == http.StatusOK {
						payload["choices"] = []any{map[string]any{
							"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop",
						}}
					} else {
						payload["error"] = map[string]any{"message": "repair unavailable"}
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					_ = json.NewEncoder(w).Encode(payload)
				}))
				defer us.Close()
				st, server := newTestServer(t)
				configureKeys(t, st, us.URL, func(c *model.Config) {
					c.ProxyKey = "master"
					c.ProxyKeys = []model.ProxyKeyGrant{{ID: "k", Key: "issued", Enabled: true, SpendLimitUSD: 0.35}}
				})
				body := fmt.Sprintf(`{"model":"cline-pass/test","input":"hi","stream":%v,%s}`, stream, strictOutputFormat)
				send := func() *httptest.ResponseRecorder {
					r := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
					r.Header.Set("Authorization", "Bearer issued")
					w := httptest.NewRecorder()
					server.ServeHTTP(w, r)
					return w
				}
				w := send()
				wantStatus := http.StatusOK
				if !tc.ok && !stream {
					wantStatus = http.StatusBadGateway
				}
				if w.Code != wantStatus {
					t.Fatalf("wrong response: %d %s", w.Code, w.Body)
				}
				if stream {
					terminal := "response.failed"
					if tc.ok {
						terminal = "response.completed"
					}
					if !strings.Contains(w.Body.String(), "event: "+terminal) {
						t.Fatalf("missing %s: %s", terminal, w.Body)
					}
				}
				if int(calls.Load()) != tc.calls || w.Header().Get("X-Cline-Attempts") != strconv.Itoa(tc.calls) {
					t.Fatalf("calls=%d header=%q, want %d", calls.Load(), w.Header().Get("X-Cline-Attempts"), tc.calls)
				}
				if tc.ok && w.Header().Get("X-Cline-Actual-Upstream") != fmt.Sprintf("provider-%d", tc.calls) {
					t.Fatalf("header kept the first generation's provider: %v", w.Header())
				}
				history := st.Metadata().History
				if len(history) != 1 || len(history[0].Trace) != tc.calls || (history[0].Error == nil) != tc.ok {
					t.Fatalf("wrong history: %+v", history)
				}
				if tc.name == "repair-success" && history[0].MS < history[0].Trace[0].MS+history[0].Trace[1].MS {
					t.Fatalf("total latency omitted the initial generation: %+v", history[0])
				}
				usage := history[0].Usage
				if usage == nil || usage.Cost == nil || int64(*usage.Cost*1e6) != tc.microUSD || usage.PromptTokens != int64(10*tc.calls) {
					t.Fatalf("lost upstream usage: %+v, want %d microUSD", usage, tc.microUSD)
				}
				if keyUsage := st.KeyUsage()["k"]; keyUsage.Requests != 1 || keyUsage.SpentMicroUSD != tc.microUSD {
					t.Fatalf("wrong key usage: %+v", keyUsage)
				}
				if refused := send(); refused.Code != http.StatusTooManyRequests || int(calls.Load()) != tc.calls {
					t.Fatalf("spent-out key called upstream again: %d %s; calls=%d", refused.Code, refused.Body, calls.Load())
				}
			})
		}
	}
}

func TestStructuredResponsesKeepUnknownCostsUnknown(t *testing.T) {
	var calls atomic.Int32
	us := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content := `{"wrong":"red"}`
		if calls.Add(1) > 1 {
			content = `{"color":"red"}`
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": content}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}))
	defer us.Close()
	st, server := newTestServer(t)
	configureKeys(t, st, us.URL, func(c *model.Config) {
		c.ProxyKey = "master"
		c.ProxyKeys = []model.ProxyKeyGrant{{ID: "k", Key: "issued", Enabled: true, SpendLimitUSD: 1}}
	})
	r := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"cline-pass/test","input":"hi",`+strictOutputFormat+`}`))
	r.Header.Set("Authorization", "Bearer issued")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		body, _ := io.ReadAll(w.Result().Body)
		t.Fatalf("repair failed: %d %s", w.Code, body)
	}
	usage := st.Metadata().History[0].Usage
	if calls.Load() != 2 || usage == nil || usage.PromptTokens != 20 || usage.Cost != nil || st.KeyUsage()["k"].SpentMicroUSD != 0 {
		t.Fatalf("unreported cost was invented or usage lost: %+v", usage)
	}
}
