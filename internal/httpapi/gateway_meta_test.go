package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/upstream"
)

func float64Pointer(value float64) *float64 { return &value }

func TestGatewayRerouteClassifiesTheReason(t *testing.T) {
	deepseekFailed := []model.GatewayAttempt{{Provider: "deepseek", Status: 429}}
	deepseekServed := []model.GatewayAttempt{{Provider: "deepseek", Status: 200, Success: true}}
	basetenServed := []model.GatewayAttempt{{Provider: "baseten", Status: 200, Success: true}}
	cases := []struct {
		name       string
		meta       upstream.GatewayMeta
		cfg        model.PerModelConfig
		want       bool
		wantReason string
	}{
		{
			"pin failed over",
			upstream.GatewayMeta{ResolvedProvider: "baseten", Attempts: deepseekFailed},
			model.PerModelConfig{Upstreams: []string{"deepseek"}},
			true, model.FallbackRetry,
		},
		{
			"pin ignored",
			upstream.GatewayMeta{ResolvedProvider: "deepseek", Attempts: deepseekServed},
			model.PerModelConfig{Upstreams: []string{"baseten"}},
			true, model.FallbackIgnored,
		},
		{
			"pin honoured",
			upstream.GatewayMeta{ResolvedProvider: "deepseek", Attempts: deepseekFailed},
			model.PerModelConfig{Upstreams: []string{"deepseek"}},
			false, "",
		},
		{
			"user pin outranks affinity",
			upstream.GatewayMeta{AffinityPinned: "deepseek", ResolvedProvider: "baseten", Attempts: basetenServed},
			model.PerModelConfig{Upstreams: []string{"baseten"}},
			false, "",
		},
		{
			"affinity failed over",
			upstream.GatewayMeta{AffinityPinned: "deepseek", ResolvedProvider: "baseten", Attempts: deepseekFailed},
			model.PerModelConfig{},
			true, model.FallbackRetry,
		},
		{
			"affinity ignored",
			upstream.GatewayMeta{AffinityPinned: "deepseek", ResolvedProvider: "baseten", Attempts: basetenServed},
			model.PerModelConfig{},
			true, model.FallbackIgnored,
		},
		{
			"affinity honoured",
			upstream.GatewayMeta{AffinityPinned: "deepseek", ResolvedProvider: "deepseek"},
			model.PerModelConfig{},
			false, "",
		},
		{
			"single upstream field",
			upstream.GatewayMeta{FinalProvider: "baseten", Attempts: deepseekFailed},
			model.PerModelConfig{Upstream: "deepseek"},
			true, model.FallbackRetry,
		},
		{
			"unpinned request",
			upstream.GatewayMeta{FinalProvider: "baseten", Attempts: basetenServed},
			model.PerModelConfig{},
			false, "",
		},
		{
			"no routing at all",
			upstream.GatewayMeta{},
			model.PerModelConfig{Upstreams: []string{"deepseek"}},
			false, "",
		},
		{
			"no attempt detail",
			upstream.GatewayMeta{ResolvedProvider: "baseten"},
			model.PerModelConfig{Upstreams: []string{"deepseek"}},
			true, "",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, reason := gatewayReroute(testCase.meta, testCase.cfg)
			if got != testCase.want || reason != testCase.wantReason {
				t.Fatalf("gatewayReroute = (%v, %q), want (%v, %q)", got, reason, testCase.want, testCase.wantReason)
			}
		})
	}
}

func TestApplyGatewayMetaKeepsTheLedgerCost(t *testing.T) {
	entry := model.HistoryEntry{Usage: &model.UsageStats{Cost: float64Pointer(0.0017)}}
	meta := upstream.GatewayMeta{
		ResolvedProvider: "baseten",
		AffinityPinned:   "deepseek",
		GenerationID:     "gen_test",
		InputCost:        float64Pointer(0.0006),
		OutputCost:       float64Pointer(0.0011),
		SurchargeCost:    float64Pointer(0.0068),
		MarketCost:       float64Pointer(0.0085),
		CacheHitTokens:   111,
		CacheMissTokens:  22,
		Attempts: []model.GatewayAttempt{
			{Provider: "deepseek", Status: 429},
			{Provider: "baseten", Status: 200, Success: true},
		},
	}
	applyGatewayMeta(&entry, meta, model.PerModelConfig{Upstreams: []string{"deepseek"}})

	if entry.Resolved != "baseten" || entry.Provider != "baseten" {
		t.Fatalf("resolved provider: %#v", entry)
	}
	if !entry.Fallback {
		t.Fatalf("a rerouted request must be flagged: %#v", entry)
	}
	if entry.FallbackReason != model.FallbackRetry {
		t.Fatalf("a failed-over request should say why: %#v", entry)
	}
	if entry.GenerationID != "gen_test" || len(entry.GatewayAttempts) != 2 {
		t.Fatalf("gateway metadata: %#v", entry)
	}
	usage := entry.Usage
	if usage == nil || usage.Cost == nil || *usage.Cost != 0.0017 {
		t.Fatalf("ledger cost must survive: %#v", usage)
	}
	if usage.GatewayCost == nil || *usage.GatewayCost != 0.0085 ||
		usage.InputCost == nil || *usage.InputCost != 0.0006 ||
		usage.OutputCost == nil || *usage.OutputCost != 0.0011 ||
		usage.SurchargeCost == nil || *usage.SurchargeCost != 0.0068 {
		t.Fatalf("cost split was not stored: %#v", usage)
	}
	if usage.CacheHitTokens != 111 || usage.CacheMissTokens != 22 {
		t.Fatalf("cache counters were not stored: %#v", usage)
	}
}

// The gateway attaches its routing metadata to one delta near the end of a
// stream; the history row has to carry it even though the client never sees a
// provider field.
func TestStreamingChatRecordsGatewayMetadata(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer,
			"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n"+
				"data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":null}]}\n\n"+
				"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":40,\"completion_tokens\":2,\"total_tokens\":42,\"cost\":0.0017}}\n\n"+
				gatewayMetaDeltaChunk()+
				"data: [DONE]\n\n")
	}))
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.Accounts = []model.Account{{Name: "main", Key: "cline-key", Enabled: true}}
		config.KnownModels = []string{"cline-pass/test"}
		config.PerModel = map[string]model.PerModelConfig{
			"cline-pass/test": {Upstreams: []string{"deepseek"}, PinMode: "strict"},
		}
	}); err != nil {
		t.Fatal(err)
	}

	request := localRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
	  "model":"cline-pass/test","messages":[{"role":"user","content":"hello"}],"stream":true
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("streaming chat failed: %d %s", response.Code, response.Body.String())
	}

	history := st.Metadata().History
	if len(history) != 1 || history[0].Error != nil {
		t.Fatalf("expected one successful row: %#v", history)
	}
	entry := history[0]
	if entry.Resolved != "baseten" || entry.Provider != "baseten" {
		t.Fatalf("resolved provider missing: %#v", entry)
	}
	if !entry.Fallback {
		t.Fatalf("rerouted request must be marked: %#v", entry)
	}
	if entry.FallbackReason != model.FallbackRetry {
		t.Fatalf("failed-over request must carry the reason: %#v", entry)
	}
	if entry.GenerationID != "gen_01TEST" {
		t.Fatalf("generation id missing: %#v", entry)
	}
	if len(entry.GatewayAttempts) != 2 || entry.GatewayAttempts[0].Provider != "deepseek" ||
		entry.GatewayAttempts[1].Status != 200 {
		t.Fatalf("gateway attempts missing: %#v", entry.GatewayAttempts)
	}
	usage := entry.Usage
	if usage == nil || usage.Cost == nil || *usage.Cost != 0.0017 {
		t.Fatalf("ledger cost must stay the recorded cost: %#v", usage)
	}
	if usage.GatewayCost == nil || *usage.GatewayCost != 0.0085 {
		t.Fatalf("gateway total missing: %#v", usage)
	}
	if usage.InputCost == nil || *usage.InputCost != 0.0006 {
		t.Fatalf("input cost missing: %#v", usage)
	}
	if usage.CacheHitTokens != 111 || usage.CacheMissTokens != 22 {
		t.Fatalf("cache counters missing: %#v", usage)
	}
}

// gatewayMetaDeltaChunk builds the SSE event the gateway uses to report how a
// streamed request actually ran.
func gatewayMetaDeltaChunk() string {
	payload := map[string]any{
		"choices": []any{map[string]any{"delta": map[string]any{"provider_metadata": map[string]any{
			"deepseek": map[string]any{"promptCacheHitTokens": 0, "promptCacheMissTokens": 0},
			"baseten":  map[string]any{"promptCacheHitTokens": 111, "promptCacheMissTokens": 22},
			"gateway": map[string]any{
				"generationId":        "gen_01TEST",
				"marketCost":          "0.0085",
				"inputInferenceCost":  "0.0006",
				"outputInferenceCost": "0.0011",
				"surchargeCost":       "0.0068",
				"routing": map[string]any{
					"affinity":         map[string]any{"outcome": "confirmed", "pinnedProvider": "deepseek"},
					"finalProvider":    "baseten",
					"resolvedProvider": "baseten",
					"modelAttempts": []any{map[string]any{"providerAttempts": []any{
						map[string]any{"provider": "deepseek", "statusCode": 429, "success": false, "startTime": 1000, "endTime": 1500},
						map[string]any{"provider": "baseten", "statusCode": 200, "success": true, "startTime": 1600, "endTime": 2600},
					}}},
				},
			},
		}}}},
	}
	raw, _ := json.Marshal(payload)
	return "data: " + string(raw) + "\n\n"
}
