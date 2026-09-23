package responses

import (
	"strings"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
)

// ShouldReplayReasoning reports whether reasoning_content can be replayed in
// assistant history for the model. Moonshot/Kimi Chat endpoints are known to
// reject or corrupt replayed reasoning, so they opt out.
func ShouldReplayReasoning(modelID string) bool {
	model := strings.ToLower(strings.TrimSpace(modelID))
	return !strings.Contains(model, "kimi") && !strings.Contains(model, "moonshot")
}

// ShouldUseRawReasoning reports whether the upstream exposes readable
// chain-of-thought that Codex Desktop can also render through the raw
// reasoning lifecycle. DeepSeek-family Cline models use this shape.
// ChatGPT Desktop still needs the summary events; those are always emitted.
func ShouldUseRawReasoning(modelID string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(modelID)), "deepseek")
}

func reasoningEffortDisabled(effort string) bool {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "none", "off", "disabled":
		return true
	default:
		return false
	}
}

func pickReasoningEffort(allowed []string, preference ...string) string {
	for _, wanted := range preference {
		for _, value := range allowed {
			if value == wanted {
				return value
			}
		}
	}
	return ""
}

func mapReasoningEffort(effort string, supported []string) string {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" {
		return ""
	}
	allowed := make([]string, 0, len(supported))
	for _, value := range supported {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			allowed = append(allowed, value)
		}
	}
	if len(allowed) == 0 {
		return effort
	}
	for _, value := range allowed {
		if value == effort {
			return effort
		}
	}
	switch effort {
	case "none", "off", "disabled":
		return pickReasoningEffort(allowed, "none")
	case "minimal":
		return pickReasoningEffort(allowed, "minimal", "low", "medium", "high", "max", "xhigh")
	case "low":
		return pickReasoningEffort(allowed, "low", "minimal", "medium", "high", "max", "xhigh")
	case "medium":
		// Prefer a stronger supported tier over a weaker one. DeepSeek's
		// none/low/high/max list has no medium, and dropping to low made
		// ChatGPT's middle/high slider stops look like "最低".
		return pickReasoningEffort(allowed, "medium", "high", "max", "xhigh", "low", "minimal")
	case "high":
		return pickReasoningEffort(allowed, "high", "max", "xhigh", "medium", "low", "minimal")
	case "xhigh", "max", "ultra":
		return pickReasoningEffort(allowed, "xhigh", "max", "ultra", "high", "medium", "low", "minimal")
	default:
		return ""
	}
}

func extractReasoningDetailsText(value any) string {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) != "" {
			return typed
		}
		return ""
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := extractReasoningDetailPartText(item); text != "" {
				parts = append(parts, text)
			}
		}
		joined := strings.Join(parts, "\n\n")
		if strings.TrimSpace(joined) != "" {
			return joined
		}
		return ""
	default:
		return extractReasoningDetailPartText(value)
	}
}

func extractReasoningDetailPartText(value any) string {
	object := jsonx.Map(value)
	if object == nil {
		return ""
	}
	for _, key := range []string{"text", "content", "summary"} {
		raw := jsonx.String(object[key])
		if strings.TrimSpace(raw) != "" {
			return raw
		}
	}
	if parts := object["parts"]; parts != nil {
		return extractReasoningDetailsText(parts)
	}
	return ""
}

// extractReasoningFieldText pulls readable reasoning from the various shapes
// used by OpenAI-compatible Chat upstreams.
func extractReasoningFieldText(value any) string {
	object := jsonx.Map(value)
	if object == nil {
		return ""
	}
	for _, key := range []string{"reasoning_content", "reasoning"} {
		raw := jsonx.String(object[key])
		if strings.TrimSpace(raw) != "" {
			return raw
		}
	}
	if reasoning := jsonx.Map(object["reasoning"]); reasoning != nil {
		for _, key := range []string{"content", "text", "summary"} {
			raw := jsonx.String(reasoning[key])
			if strings.TrimSpace(raw) != "" {
				return raw
			}
		}
	}
	return extractReasoningDetailsText(object["reasoning_details"])
}

// AliasChatReasoning copies Cline/OpenRouter `reasoning` onto OpenAI-style
// `reasoning_content` (and the reverse) so Chat Completions clients can render
// thinking without protocol conversion.
func AliasChatReasoning(chunk map[string]any) bool {
	if chunk == nil {
		return false
	}
	changed := false
	for _, raw := range jsonx.Slice(chunk["choices"]) {
		choice := jsonx.Map(raw)
		if choice == nil {
			continue
		}
		for _, key := range []string{"delta", "message"} {
			object := jsonx.Map(choice[key])
			if object == nil {
				continue
			}
			if aliasReasoningFields(object) {
				changed = true
			}
		}
	}
	return changed
}

func aliasReasoningFields(object map[string]any) bool {
	reasoningText := jsonx.String(object["reasoning"])
	if reasoningText == "" {
		if nested := jsonx.Map(object["reasoning"]); nested != nil {
			reasoningText = firstString(jsonx.String(nested["content"]), jsonx.String(nested["text"]), jsonx.String(nested["summary"]))
		}
	}
	if reasoningText == "" {
		reasoningText = extractReasoningDetailsText(object["reasoning_details"])
	}
	contentText := jsonx.String(object["reasoning_content"])
	changed := false
	if reasoningText != "" && strings.TrimSpace(contentText) == "" {
		object["reasoning_content"] = reasoningText
		changed = true
	}
	if contentText != "" && jsonx.String(object["reasoning"]) == "" && jsonx.Map(object["reasoning"]) == nil {
		object["reasoning"] = contentText
		changed = true
	}
	return changed
}

func extractReasoningSummaryText(value any) string {
	object := jsonx.Map(value)
	if object == nil {
		return ""
	}
	for _, key := range []string{"content", "summary"} {
		raw := textFromParts(object[key])
		if strings.TrimSpace(raw) != "" {
			return raw
		}
	}
	for _, key := range []string{"reasoning_content", "text"} {
		raw := jsonx.String(object[key])
		if strings.TrimSpace(raw) != "" {
			return raw
		}
	}
	return ""
}

func reasoningTextFromItem(item map[string]any) string {
	if text := extractReasoningSummaryText(item); text != "" {
		return text
	}
	return extractReasoningFieldText(item)
}
