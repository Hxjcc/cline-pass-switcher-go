package httpapi

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

// streamStats is fed by the goroutine pumping the upstream stream and read
// by request handlers that may leave at any time, so it guards its own state.
type streamStats struct {
	mu           sync.Mutex
	started      time.Time
	buf          string
	firstTokenAt time.Time
	usage        *model.UsageStats
	finishReason string
}

func newStreamStats(started time.Time) *streamStats {
	return &streamStats{started: started}
}

func (stats *streamStats) Observe(data []byte) {
	if stats == nil || len(data) == 0 {
		return
	}
	stats.mu.Lock()
	defer stats.mu.Unlock()
	stats.buf += string(data)
	stats.buf = strings.ReplaceAll(stats.buf, "\r\n", "\n")
	for {
		index := strings.Index(stats.buf, "\n\n")
		if index < 0 {
			break
		}
		block := stats.buf[:index]
		stats.buf = stats.buf[index+2:]
		stats.consumeBlock(block)
	}
}

func (stats *streamStats) TTFTMs() int64 {
	if stats == nil {
		return 0
	}
	stats.mu.Lock()
	defer stats.mu.Unlock()
	if stats.firstTokenAt.IsZero() || stats.started.IsZero() {
		return 0
	}
	elapsed := stats.firstTokenAt.Sub(stats.started).Milliseconds()
	if elapsed < 1 {
		return 1
	}
	return elapsed
}

func (stats *streamStats) Usage() *model.UsageStats {
	if stats == nil {
		return nil
	}
	stats.mu.Lock()
	defer stats.mu.Unlock()
	return stats.usage
}

func (stats *streamStats) FinishReason() string {
	if stats == nil {
		return ""
	}
	stats.mu.Lock()
	defer stats.mu.Unlock()
	return stats.finishReason
}

func (stats *streamStats) consumeBlock(block string) {
	payloads := sseDataPayloads(block)
	for _, payload := range payloads {
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		stats.consumeChunk(chunk)
	}
}

func (stats *streamStats) consumeChunk(chunk map[string]any) {
	if usage := usageFromValue(chunk["usage"]); usage != nil {
		stats.usage = usage
	}
	if nested := statsMap(chunk["response"]); nested != nil {
		if usage := usageFromValue(nested["usage"]); usage != nil {
			stats.usage = usage
		}
	}
	if stats.firstTokenAt.IsZero() && chatChunkHasToken(chunk) {
		stats.firstTokenAt = time.Now()
	}
	if reason := finishReasonFromChat(chunk); reason != "" {
		stats.finishReason = reason
	}
}

func sseDataPayloads(block string) []string {
	payloads := make([]string, 0, 1)
	for _, line := range strings.Split(block, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payloads = append(payloads, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
	}
	return payloads
}

func applyChatStats(entry *model.HistoryEntry, chatOut map[string]any, ttftMs int64) {
	if entry == nil {
		return
	}
	if chatOut != nil {
		entry.Usage = usageFromValue(chatOut["usage"])
		entry.FinishReason = finishReasonFromChat(chatOut)
	}
	if ttftMs > 0 {
		entry.TTFTMs = ttftMs
	}
}

func applyStreamStats(entry *model.HistoryEntry, stats *streamStats) {
	if entry == nil || stats == nil {
		return
	}
	entry.Usage = stats.Usage()
	entry.FinishReason = stats.FinishReason()
	if ttft := stats.TTFTMs(); ttft > 0 {
		entry.TTFTMs = ttft
	}
}

func recordedEffort(mapped string, body map[string]any) string {
	if value := strings.TrimSpace(mapped); value != "" {
		return value
	}
	return effortFromChatBody(body)
}

func applyReasoningEffort(entry *model.HistoryEntry, mapped, requested string, body map[string]any) {
	entry.Effort = recordedEffort(mapped, body)
	if value := strings.TrimSpace(requested); value != "" {
		entry.RequestedEffort = value
	}
}

func friendlyStreamError(err error) string {
	if err == nil {
		return ""
	}
	return friendlyCancelText(err.Error())
}

func friendlyCancelText(message string) string {
	if strings.Contains(strings.ToLower(message), "context canceled") {
		return "客户端取消"
	}
	return message
}

func effortFromChatBody(body map[string]any) string {
	if body == nil {
		return ""
	}
	if value := strings.TrimSpace(statsString(body["reasoning_effort"])); value != "" {
		return value
	}
	return strings.TrimSpace(statsString(statsMap(body["reasoning"])["effort"]))
}

func usageFromValue(value any) *model.UsageStats {
	usage := statsMap(value)
	if usage == nil {
		return nil
	}
	prompt := firstInt64(usage["prompt_tokens"], usage["input_tokens"])
	completion := firstInt64(usage["completion_tokens"], usage["output_tokens"])
	cached := firstInt64(
		statsMap(usage["prompt_tokens_details"])["cached_tokens"],
		statsMap(usage["input_tokens_details"])["cached_tokens"],
	)
	reasoning := firstInt64(
		statsMap(usage["completion_tokens_details"])["reasoning_tokens"],
		statsMap(usage["output_tokens_details"])["reasoning_tokens"],
	)
	total := int64Value(usage["total_tokens"])
	if total == 0 {
		total = prompt + completion
	}
	stats := &model.UsageStats{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		ReasoningTokens:  reasoning,
		CachedTokens:     cached,
		TotalTokens:      total,
	}
	if _, found := usage["cost"]; found {
		if cost, ok := floatValue(usage["cost"]); ok {
			stats.Cost = &cost
		}
	}
	if stats.PromptTokens == 0 && stats.CompletionTokens == 0 && stats.TotalTokens == 0 &&
		stats.ReasoningTokens == 0 && stats.CachedTokens == 0 && stats.Cost == nil {
		return nil
	}
	return stats
}

func finishReasonFromChat(value map[string]any) string {
	for _, raw := range statsSlice(value["choices"]) {
		choice := statsMap(raw)
		reason := strings.TrimSpace(statsString(choice["finish_reason"]))
		if reason != "" && reason != "null" {
			return reason
		}
	}
	return ""
}

func chatChunkHasToken(chunk map[string]any) bool {
	if hasOutputToken(statsMap(chunk["delta"])) || hasOutputToken(statsMap(chunk["message"])) {
		return true
	}
	for _, raw := range statsSlice(chunk["choices"]) {
		choice := statsMap(raw)
		if hasOutputToken(statsMap(choice["delta"])) || hasOutputToken(statsMap(choice["message"])) {
			return true
		}
	}
	return false
}

func hasOutputToken(object map[string]any) bool {
	if object == nil {
		return false
	}
	if strings.TrimSpace(statsString(object["content"])) != "" {
		return true
	}
	if strings.TrimSpace(statsString(object["reasoning"])) != "" {
		return true
	}
	if strings.TrimSpace(statsString(object["reasoning_content"])) != "" {
		return true
	}
	if strings.TrimSpace(statsString(object["refusal"])) != "" {
		return true
	}
	if len(statsSlice(object["tool_calls"])) > 0 {
		return true
	}
	switch typed := object["content"].(type) {
	case []any:
		if len(typed) > 0 {
			return true
		}
	case map[string]any:
		if len(typed) > 0 {
			return true
		}
	}
	switch typed := object["reasoning_details"].(type) {
	case []any:
		return len(typed) > 0
	case string:
		return strings.TrimSpace(typed) != ""
	}
	return false
}

func statsMap(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func statsSlice(value any) []any {
	result, _ := value.([]any)
	return result
}

func statsString(value any) string {
	result, _ := value.(string)
	return result
}

func int64Value(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		result, _ := typed.Int64()
		return result
	default:
		return 0
	}
}

func firstInt64(values ...any) int64 {
	for _, value := range values {
		if result := int64Value(value); result != 0 {
			return result
		}
	}
	return 0
}

func floatValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		result, err := typed.Float64()
		return result, err == nil
	default:
		return 0, false
	}
}
