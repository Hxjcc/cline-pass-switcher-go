package httpapi

import (
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/upstream"
)

// applyGatewayMeta copies the gateway's own view of a request onto the history
// entry: the provider it really ran, the attempts it made inside the gateway,
// the generation id, and the cost/cache split of the response. Spend limits
// keep using usage.cost; the split is display-only.
func applyGatewayMeta(entry *model.HistoryEntry, meta upstream.GatewayMeta, cfg model.PerModelConfig) {
	if entry == nil || meta.Empty() {
		return
	}
	actual := firstNonEmpty(meta.ResolvedProvider, meta.FinalProvider, meta.Provider)
	if entry.Resolved == "" {
		entry.Resolved = actual
	}
	if entry.Provider == "" {
		entry.Provider = actual
	}
	if entry.Canonical == "" {
		entry.Canonical = meta.CanonicalSlug
	}
	if len(meta.Attempts) > 0 {
		entry.GatewayAttempts = meta.Attempts
	}
	if meta.GenerationID != "" {
		entry.GenerationID = meta.GenerationID
	}
	if gatewayFallback(meta, cfg) {
		entry.Fallback = true
	}
	if entry.Usage == nil {
		if meta.InputCost == nil && meta.OutputCost == nil && meta.SurchargeCost == nil && meta.MarketCost == nil {
			return
		}
		entry.Usage = &model.UsageStats{}
	}
	if meta.InputCost != nil {
		entry.Usage.InputCost = meta.InputCost
	}
	if meta.OutputCost != nil {
		entry.Usage.OutputCost = meta.OutputCost
	}
	if meta.SurchargeCost != nil {
		entry.Usage.SurchargeCost = meta.SurchargeCost
	}
	if meta.MarketCost != nil {
		entry.Usage.GatewayCost = meta.MarketCost
	}
	if meta.CacheHitTokens != 0 || meta.CacheMissTokens != 0 {
		entry.Usage.CacheHitTokens = meta.CacheHitTokens
		entry.Usage.CacheMissTokens = meta.CacheMissTokens
	}
}

// applyGatewayMetaForModel resolves the stored per-model pin itself, for paths
// that only carry the model id.
func (s *Server) applyGatewayMetaForModel(entry *model.HistoryEntry, meta upstream.GatewayMeta, modelID string) {
	applyGatewayMeta(entry, meta, s.store.ModelConfig(modelID))
}

// gatewayFallback reports whether a request landed on a provider other than
// the one the session affinity or the model's pin asked for. The gateway
// silently ignores channel preferences for some models, so this is the only
// trustworthy signal that a request was rerouted.
func gatewayFallback(meta upstream.GatewayMeta, cfg model.PerModelConfig) bool {
	actual := firstNonEmpty(meta.ResolvedProvider, meta.FinalProvider, meta.Provider)
	if actual == "" {
		return false
	}
	if meta.AffinityPinned != "" && !upstream.SameProvider(meta.AffinityPinned, actual) {
		return true
	}
	pinned := cfg.Upstreams
	if len(pinned) == 0 && cfg.Upstream != "" {
		pinned = []string{cfg.Upstream}
	}
	if len(pinned) == 0 {
		return false
	}
	for _, slug := range pinned {
		if upstream.SameProvider(slug, actual) {
			return false
		}
	}
	return true
}
