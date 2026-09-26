package upstream

import (
	"encoding/json"
	"testing"
)

// A fallback turn: deepseek was rate limited, the gateway retried on baseten
// and reported its own routing, cache and billing metadata on the message.
const gatewayMetaCompletion = `{
  "choices": [{"message": {"content": "OK", "provider_metadata": {
    "deepseek": {"choiceIndex": 0, "promptCacheHitTokens": 0, "promptCacheMissTokens": 0},
    "baseten": {"choiceIndex": 0, "promptCacheHitTokens": 111, "promptCacheMissTokens": 22},
    "gateway": {
      "cost": "0.0017", "gatewayCost": "0.0085", "marketCost": "0.0085",
      "inferenceCost": "0.0017", "inputInferenceCost": "0.0006",
      "outputInferenceCost": "0.0011", "surchargeCost": "0.0068",
      "generationId": "gen_01TEST",
      "routing": {
        "affinity": {"outcome": "confirmed", "pinnedProvider": "deepseek"},
        "canonicalSlug": "deepseek/deepseek-v4.1-flash",
        "finalProvider": "baseten",
        "resolvedProvider": "baseten",
        "fallbacksAvailable": ["alibaba", "baseten", "fireworks"],
        "planningReasoning": "System credentials planned for: deepseek, baseten.",
        "modelAttemptCount": 1,
        "modelAttempts": [{"canonicalSlug": "deepseek/deepseek-v4.1-flash", "providerAttemptCount": 2, "success": true, "providerAttempts": [
          {"provider": "deepseek", "statusCode": 429, "success": false, "startTime": 1000, "endTime": 1500, "providerRequestId": "req-1", "providerResponseId": "resp-1"},
          {"provider": "baseten", "statusCode": 200, "success": true, "startTime": 1600, "endTime": 2600, "providerRequestId": "req-2"}
        ]}]
      }
    }
  }}}]
}`

// A stream carries the same metadata in one delta chunk.
const gatewayMetaDelta = `{"choices":[{"delta":{"provider_metadata":{
  "deepseek":{"promptCacheHitTokens":182400,"promptCacheMissTokens":1234},
  "gateway":{"generationId":"gen_02TEST","routing":{
    "affinity":{"outcome":"confirmed","pinnedProvider":"deepseek"},
    "finalProvider":"deepseek",
    "resolvedProvider":"deepseek"
  }}
}}}]}`

func decodeMetaFixture(t *testing.T, raw string) map[string]any {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	return root
}

func TestParseMetaReadsGatewayRouting(t *testing.T) {
	meta := ParseMeta(decodeMetaFixture(t, gatewayMetaCompletion))
	if meta.FinalProvider != "baseten" || meta.ResolvedProvider != "baseten" {
		t.Fatalf("final/resolved provider: %#v", meta)
	}
	if meta.AffinityPinned != "deepseek" {
		t.Fatalf("affinity should name the pinned provider: %#v", meta)
	}
	if meta.CanonicalSlug != "deepseek/deepseek-v4.1-flash" || meta.GenerationID != "gen_01TEST" {
		t.Fatalf("slug/generation: %#v", meta)
	}
	if meta.InputCost == nil || *meta.InputCost != 0.0006 ||
		meta.OutputCost == nil || *meta.OutputCost != 0.0011 ||
		meta.SurchargeCost == nil || *meta.SurchargeCost != 0.0068 ||
		meta.MarketCost == nil || *meta.MarketCost != 0.0085 {
		t.Fatalf("cost split: %#v", meta)
	}
	// The provider that actually ran owns the cache counters.
	if meta.CacheHitTokens != 111 || meta.CacheMissTokens != 22 {
		t.Fatalf("cache counters: %#v", meta)
	}
	if len(meta.Attempts) != 2 {
		t.Fatalf("expected both gateway attempts: %#v", meta.Attempts)
	}
	first, second := meta.Attempts[0], meta.Attempts[1]
	if first.Provider != "deepseek" || first.Status != 429 || first.Success || first.MS != 500 {
		t.Fatalf("failed attempt: %#v", first)
	}
	if second.Provider != "baseten" || second.Status != 200 || !second.Success || second.MS != 1000 {
		t.Fatalf("successful attempt: %#v", second)
	}
	if second.RequestID != "req-2" || first.ResponseID != "resp-1" {
		t.Fatalf("attempt ids: %#v", meta.Attempts)
	}
}

func TestParseMetaReadsStreamDelta(t *testing.T) {
	meta := ParseMeta(decodeMetaFixture(t, gatewayMetaDelta))
	if meta.ResolvedProvider != "deepseek" || meta.AffinityPinned != "deepseek" || meta.GenerationID != "gen_02TEST" {
		t.Fatalf("delta metadata: %#v", meta)
	}
	if meta.CacheHitTokens != 182400 || meta.CacheMissTokens != 1234 {
		t.Fatalf("delta cache counters: %#v", meta)
	}
}

func TestParseMetaReadsDirectPipelineProvider(t *testing.T) {
	meta := ParseMeta(decodeMetaFixture(t, `{"provider":"Z.AI","model":"z-ai/glm-5.3-flash"}`))
	if meta.Provider != "z-ai" || meta.CanonicalSlug != "z-ai/glm-5.3-flash" {
		t.Fatalf("direct pipeline shape: %#v", meta)
	}
}

func TestParseMetaEmptyForPlainChunks(t *testing.T) {
	if meta := ParseMeta(decodeMetaFixture(t, `{"choices":[{"delta":{"content":"hi"}}]}`)); !meta.Empty() {
		t.Fatalf("plain content chunk must not look like metadata: %#v", meta)
	}
}

func TestSameProviderFoldsNames(t *testing.T) {
	if !SameProvider("z.ai", "Z-AI") {
		t.Fatal("provider names should compare through their folded key")
	}
	if SameProvider("baseten", "deepseek") {
		t.Fatal("different providers must not match")
	}
	if SameProvider("", "") {
		t.Fatal("empty names must not match")
	}
}
