package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

// Both compaction protocols record one request, but must charge every summary
// pass even when the final output is a fallback with no usage of its own.
func TestCompactionAccountsForEveryPass(t *testing.T) {
	starved := strings.Replace(costedCompletion, `"finish_reason":"stop"`, `"finish_reason":"length"`, 1)
	for _, protocol := range []string{"compact", "trigger"} {
		for _, stream := range []bool{false, true} {
			for _, tc := range []struct {
				name, first, second string
				secondStatus, calls int
				cost                *float64
				degraded            bool
			}{
				{"success", costedCompletion, "", 200, 1, floatPtr(0.25), false},
				{"retry-success", starved, costedCompletion, 200, 2, floatPtr(0.50), false},
				{"retry-degraded", starved, starved, 200, 2, floatPtr(0.50), true},
				{"retry-unavailable", starved, `{"error":{"message":"unavailable"}}`, 503, 2, floatPtr(0.25), true},
				{"unknown-cost", strings.Replace(starved, `,"cost":0.25`, "", 1), strings.Replace(costedCompletion, `,"cost":0.25`, "", 1), 200, 2, nil, false},
			} {
				transport := "json"
				if stream {
					transport = "sse"
				}
				t.Run(protocol+"/"+transport+"/"+tc.name, func(t *testing.T) {
					var calls atomic.Int32
					us := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if strings.HasPrefix(r.URL.Path, "/users/me/plan") {
							http.NotFound(w, r)
							return
						}
						w.Header().Set("Content-Type", "application/json")
						if calls.Add(1) == 1 {
							_, _ = io.WriteString(w, tc.first)
						} else {
							w.WriteHeader(tc.secondStatus)
							_, _ = io.WriteString(w, tc.second)
						}
					}))
					defer us.Close()
					st, server := newTestServer(t)
					configureKeys(t, st, us.URL, func(c *model.Config) {
						c.ProxyKey = "master"
						c.ProxyKeys = []model.ProxyKeyGrant{{ID: "k", Key: "issued", Enabled: true, SpendLimitUSD: 0.20}}
					})
					path := "/v1/responses/compact"
					input := []any{map[string]any{"role": "user", "content": "remember this"}}
					if protocol == "trigger" {
						path = "/v1/responses"
						input = append(input, map[string]any{"type": "compaction_trigger"})
					}
					body, err := json.Marshal(map[string]any{"model": "cline-pass/test", "input": input, "stream": stream})
					if err != nil {
						t.Fatal(err)
					}
					send := func() *httptest.ResponseRecorder {
						r := localRequest(http.MethodPost, path, strings.NewReader(string(body)))
						r.Header.Set("Content-Type", "application/json")
						r.Header.Set("Authorization", "Bearer issued")
						w := httptest.NewRecorder()
						server.ServeHTTP(w, r)
						return w
					}
					if w := send(); w.Code != 200 || (stream && !strings.Contains(w.Body.String(), "response.completed")) {
						t.Fatalf("compaction failed: %d %s", w.Code, w.Body)
					}
					if int(calls.Load()) != tc.calls {
						t.Fatalf("got %d upstream calls, want %d", calls.Load(), tc.calls)
					}
					history := st.Metadata().History
					if len(history) != 1 || history[0].KeyID != "k" || history[0].Degraded != tc.degraded || len(history[0].Trace) != tc.calls {
						t.Fatalf("wrong compaction history: %+v", history)
					}
					usage := history[0].Usage
					if usage == nil {
						t.Fatal("lost upstream usage")
					}
					if tc.cost == nil {
						if usage.Cost != nil || st.KeyUsage()["k"].SpentMicroUSD != 0 {
							t.Fatal("invented an unreported cost")
						}
						return
					}
					if usage.Cost == nil || *usage.Cost != *tc.cost {
						t.Fatalf("wrong total cost: %+v, want %v", usage, *tc.cost)
					}
					if keyUsage := st.KeyUsage()["k"]; keyUsage.Requests != 1 || keyUsage.SpentMicroUSD != int64(*tc.cost*1e6) {
						t.Fatalf("wrong key charge: %+v", keyUsage)
					}
					if w := send(); w.Code != 429 {
						t.Fatalf("spent key must be refused: %d %s", w.Code, w.Body)
					}
					if int(calls.Load()) != tc.calls {
						t.Fatal("spent key reached the upstream again")
					}
				})
			}
		}
	}
}
