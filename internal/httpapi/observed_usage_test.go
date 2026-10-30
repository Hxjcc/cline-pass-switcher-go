package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

// These are reduced, de-identified live Cline responses, not fabricated
// per-leg usage. Replaying them must adjust only the client-facing reference:
// history, the usage ledger and issued-key spend all keep the original bill.
func TestObservedClineUsageSeparatesContextFromBilling(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name        string `json:"name"`
			ContentType string `json:"contentType"`
			Body        string `json:"body"`
			Expected    struct {
				InputTokens     int64   `json:"inputTokens"`
				CachedTokens    int64   `json:"cachedTokens"`
				Estimated       bool    `json:"estimated"`
				OutputTokens    int64   `json:"outputTokens"`
				RawInputTokens  int64   `json:"rawInputTokens"`
				RawCachedTokens int64   `json:"rawCachedTokens"`
				RawTotalTokens  int64   `json:"rawTotalTokens"`
				Cost            float64 `json:"cost"`
				SpentMicroUSD   int64   `json:"spentMicroUSD"`
			} `json:"expected"`
		} `json:"cases"`
	}
	raw, err := os.ReadFile("testdata/cline-usage-observed.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("missing observed usage cases")
	}
	for _, tc := range fixture.Cases {
		for _, clientStream := range []bool{false, true} {
			if !clientStream && tc.ContentType == "text/event-stream" {
				continue // The captured upstream SSE requests were streaming.
			}
			transport := "json"
			if clientStream {
				transport = "sse"
			}
			t.Run(tc.Name+"/"+transport, func(t *testing.T) {
				u := &keyedUpstream{body: tc.Body, contentType: tc.ContentType}
				us := u.server(t)
				st, server := newTestServer(t)
				const modelID = "cline-pass/deepseek-v4.1-flash-1017"
				configureKeys(t, st, us.URL, func(c *model.Config) {
					c.KnownModels = []string{modelID}
					c.ProxyKey, c.AdminKey = "master", "admin"
					c.ProxyKeys = []model.ProxyKeyGrant{{ID: "k", Key: "issued", Enabled: true, SpendLimitUSD: 1}}
				})
				body, err := json.Marshal(map[string]any{"model": modelID, "input": "synthetic usage replay", "stream": clientStream})
				if err != nil {
					t.Fatal(err)
				}
				r := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Authorization", "Bearer issued")
				w := httptest.NewRecorder()
				server.ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("response failed: %d %s", w.Code, w.Body)
				}
				response := w.Body.Bytes()
				if clientStream {
					response = nil
					for _, event := range parseStreamEvents(t, w.Body.String()) {
						if event.Type == "response.completed" {
							response, err = json.Marshal(event.Data["response"])
							if err != nil {
								t.Fatal(err)
							}
						}
					}
				}
				var got struct {
					Status string `json:"status"`
					Usage  struct {
						InputTokens        int64 `json:"input_tokens"`
						OutputTokens       int64 `json:"output_tokens"`
						TotalTokens        int64 `json:"total_tokens"`
						InputTokensDetails struct {
							CachedTokens int64 `json:"cached_tokens"`
							Estimated    bool  `json:"context_usage_estimated"`
						} `json:"input_tokens_details"`
					} `json:"usage"`
				}
				if err := json.Unmarshal(response, &got); err != nil {
					t.Fatalf("missing completed response: %v", err)
				}
				want := tc.Expected
				if got.Status != "completed" || got.Usage.InputTokens != want.InputTokens || got.Usage.OutputTokens != want.OutputTokens ||
					got.Usage.TotalTokens != want.InputTokens+want.OutputTokens || got.Usage.InputTokensDetails.CachedTokens != want.CachedTokens ||
					got.Usage.InputTokensDetails.Estimated != want.Estimated {
					t.Fatalf("client usage differs from capture: %+v", got)
				}
				meta := st.Metadata()
				if len(meta.History) != 1 || meta.History[0].Error != nil || meta.History[0].KeyID != "k" {
					t.Fatalf("wrong request history: %+v", meta.History)
				}
				history := meta.History[0].Usage
				if history == nil || history.PromptTokens != want.RawInputTokens || history.CachedTokens != want.RawCachedTokens ||
					history.CompletionTokens != want.OutputTokens || history.TotalTokens != want.RawTotalTokens ||
					history.Cost == nil || *history.Cost != want.Cost {
					t.Fatalf("history lost cumulative usage or actual cost: %+v", history)
				}
				if spend := st.KeyUsage()["k"]; spend.Requests != 1 || spend.SpentMicroUSD != want.SpentMicroUSD {
					t.Fatalf("client estimate changed the issued-key bill: %+v", spend)
				}
				if meta.UsageLedger == nil || len(meta.UsageLedger.Buckets) != 1 {
					t.Fatal("request was not recorded once in the usage ledger")
				}
				for _, bucket := range meta.UsageLedger.Buckets {
					if bucket.InputTokens != want.RawInputTokens || bucket.CachedTokens != want.RawCachedTokens ||
						bucket.OutputTokens != want.OutputTokens || bucket.TotalTokens != want.RawTotalTokens || bucket.Cost != want.Cost {
						t.Fatalf("usage ledger lost the upstream totals: %+v", bucket)
					}
				}
				if len(u.credentials()) != 1 {
					t.Fatal("replay unexpectedly made more than one model request")
				}
			})
		}
	}
}
