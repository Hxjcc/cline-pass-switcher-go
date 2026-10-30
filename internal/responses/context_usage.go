package responses

import "github.com/Hxjcc/cline-pass-switcher-go/internal/jsonx"

// Only explicitly indexed per-provider usage can establish final context size.
func finalLegUsage(root map[string]any) (map[string]any, int) {
	var result map[string]any
	best := -1
	visit := func(metadata map[string]any) {
		gateway := jsonx.Map(metadata["gateway"])
		routing := jsonx.Map(gateway["routing"])
		if routing == nil {
			routing = jsonx.Map(metadata["routing"])
		}
		for _, raw := range jsonx.Slice(routing["modelAttempts"]) {
			for _, rawAttempt := range jsonx.Slice(jsonx.Map(raw)["providerAttempts"]) {
				a := jsonx.Map(rawAttempt)
				index, present := a["toolLoopLegIndex"]
				usage := jsonx.Map(a["usage"])
				if !present || usage["prompt_tokens"] == nil || a["success"] == false {
					continue
				}
				n := int(intValue(index))
				if n >= best {
					best = n
					result = usage
				}
			}
		}
	}
	visit(jsonx.Map(root["provider_metadata"]))
	for _, raw := range jsonx.Slice(root["choices"]) {
		c := jsonx.Map(raw)
		visit(jsonx.Map(c["provider_metadata"]))
		visit(jsonx.Map(jsonx.Map(c["delta"])["provider_metadata"]))
		visit(jsonx.Map(jsonx.Map(c["message"])["provider_metadata"]))
	}
	return result, best
}

func (s *StreamState) reportedUsage() any {
	usage := jsonx.Map(reportedUsage(s.rawUsage, s.usageLegs(), s.context.InputTokenCap))
	if usage == nil {
		return nil
	}
	if s.finalLegUsage != nil && s.finalLegUsageIndex >= s.gatewayLegIndex {
		actual := jsonx.Map(reportedUsage(s.finalLegUsage, 1, s.context.InputTokenCap))
		usage["input_tokens"] = actual["input_tokens"]
		usage["input_tokens_details"] = actual["input_tokens_details"]
		usage["total_tokens"] = intValue(usage["input_tokens"]) + intValue(usage["output_tokens"])
	} else if s.usageLegs() > 1 {
		jsonx.Map(usage["input_tokens_details"])["context_usage_estimated"] = true
	}
	return usage
}
