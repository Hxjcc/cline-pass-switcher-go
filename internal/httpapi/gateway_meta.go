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
	if rerouted, reason := gatewayReroute(meta, cfg); rerouted {
		entry.Fallback = true
		entry.FallbackReason = reason
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

// gatewayReroute reports whether a request landed on a provider other than the
// one the model's pin (or, without a pin, the session affinity) asked for, and
// why. A model whose channel preference the gateway ignores looks like the
// second case: the wanted channel never appears in the attempt list, so the
// request was not failed over — the preference simply had no effect.
func gatewayReroute(meta upstream.GatewayMeta, cfg model.PerModelConfig) (bool, string) {
	actual := firstNonEmpty(meta.ResolvedProvider, meta.FinalProvider, meta.Provider)
	if actual == "" {
		return false, ""
	}
	wanted := cfg.Upstreams
	if len(wanted) == 0 && cfg.Upstream != "" {
		wanted = []string{cfg.Upstream}
	}
	if len(wanted) == 0 {
		// Without a pin the only signal left is the affinity the gateway
		// itself reported.
		if meta.AffinityPinned == "" || upstream.SameProvider(meta.AffinityPinned, actual) {
			return false, ""
		}
		return true, rerouteReason(meta, meta.AffinityPinned)
	}
	for _, slug := range wanted {
		if upstream.SameProvider(slug, actual) {
			return false, ""
		}
	}
	return true, rerouteReason(meta, wanted...)
}

// rerouteReason separates a real failover from an ignored preference: only a
// wanted channel that shows up in the gateway's attempt list was actually
// tried. Without attempt details the reason stays empty and the console keeps
// the generic wording.
func rerouteReason(meta upstream.GatewayMeta, wanted ...string) string {
	if len(meta.Attempts) == 0 {
		return ""
	}
	for _, attempt := range meta.Attempts {
		for _, slug := range wanted {
			if slug != "" && upstream.SameProvider(slug, attempt.Provider) {
				return model.FallbackRetry
			}
		}
	}
	return model.FallbackIgnored
}
