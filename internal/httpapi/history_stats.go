package httpapi

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/sse"
)

// streamStats is fed by the goroutine pumping the upstream stream and read
// by request handlers that may leave at any time, so it guards its own state.
type streamStats struct {
	mu           sync.Mutex
	started      time.Time
	parser       sse.Parser
	firstTokenAt time.Time
	usage        *model.UsageStats
	finishReason string
	done         bool
	streamError  string
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
	// The protocol parser owns reporting oversize errors. Statistics stop
	// collecting after the same bound is exceeded, without retaining the input.
	_ = stats.parser.Feed(data, func(block string) bool {
		stats.consumeBlock(block)
		return true
	})
}

// ObserveBlock consumes a trailing partial block that never got its
// blank-line delimiter. A truncated upstream can still have delivered the
// final finish_reason payload, and the stats must see it.
func (stats *streamStats) ObserveBlock(block string) {
	if stats == nil || strings.TrimSpace(block) == "" {
		return
	}
	stats.mu.Lock()
	defer stats.mu.Unlock()
	stats.consumeBlock(block)
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

// Terminal reports whether the upstream protocol reached a terminal state: a
// [DONE] sentinel or a chunk carrying finish_reason. Without one the stream
// was cut short even though the HTTP body ended cleanly.
func (stats *streamStats) Terminal() bool {
	if stats == nil {
		return false
	}
	stats.mu.Lock()
	defer stats.mu.Unlock()
	return stats.done || stats.finishReason != ""
}

// StreamError returns the message of the first in-stream error event, if any.
func (stats *streamStats) StreamError() string {
	if stats == nil {
		return ""
	}
	stats.mu.Lock()
	defer stats.mu.Unlock()
	return stats.streamError
}

func (stats *streamStats) consumeBlock(block string) {
	payload := sseDataPayload(block)
	if payload == "" {
		return
	}
	if payload == "[DONE]" {
		stats.done = true
		return
	}
	var chunk map[string]any
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		// Refusing to guess is safer than reporting success: an event that is
		// there but unreadable may well be the error the stream ended with.
		if stats.streamError == "" {
			stats.streamError = "上游流包含无法解析的 SSE 事件"
		}
		return
	}
	stats.consumeChunk(chunk)
}

func (stats *streamStats) consumeChunk(chunk map[string]any) {
	if stats.streamError == "" {
		stats.streamError = chatStreamErrorText(chunk)
	}
	if usage := usageFromValue(chunk["usage"]); usage != nil {
		stats.usage = usage
	}
	if nested := jsonx.Map(chunk["response"]); nested != nil {
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

// chatStreamErrorText extracts an error carried inside a Chat Completions SSE
// event. Upstreams report overloads and mid-stream failures this way, and the
// HTTP response itself still ends with 200.
func chatStreamErrorText(chunk map[string]any) string {
	if chunk == nil || len(jsonx.Slice(chunk["choices"])) > 0 {
		return ""
	}
	if _, found := chunk["error"]; found {
		if message := strings.TrimSpace(extractAttemptError(chunk)); message != "" {
			return message
		}
		return "upstream error"
	}
	if strings.EqualFold(strings.TrimSpace(jsonx.String(chunk["type"])), "error") {
		if message := strings.TrimSpace(jsonx.String(chunk["message"])); message != "" {
			return message
		}
		if code := strings.TrimSpace(jsonx.String(chunk["code"])); code != "" {
			return code
		}
		return "upstream error"
	}
	return ""
}

// sseDataPayload joins every data: line of one event. The SSE spec defines the
// event payload as the data lines joined with newlines, so decoding each line
// on its own both mis-parses valid multi-line JSON and silently drops the
// error events that report a mid-stream failure.
func sseDataPayload(block string) string {
	lines := make([]string, 0, 1)
	for _, line := range strings.Split(block, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		lines = append(lines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
	}
	return strings.Join(lines, "\n")
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

// addUsage accumulates independent upstream passes. Missing cost stays unknown;
// only amounts actually reported by the upstream are added.
func addUsage(total, next *model.UsageStats) *model.UsageStats {
	if next == nil {
		return total
	}
	if total == nil {
		total = &model.UsageStats{}
	}
	total.PromptTokens += next.PromptTokens
	total.CompletionTokens += next.CompletionTokens
	total.ReasoningTokens += next.ReasoningTokens
	total.CachedTokens += next.CachedTokens
	total.TotalTokens += next.TotalTokens
	if next.Cost != nil {
		cost := *next.Cost
		if total.Cost != nil {
			cost += *total.Cost
		}
		total.Cost = &cost
	}
	return total
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
	if value := strings.TrimSpace(jsonx.String(body["reasoning_effort"])); value != "" {
		return value
	}
	return strings.TrimSpace(jsonx.String(jsonx.Map(body["reasoning"])["effort"]))
}

func usageFromValue(value any) *model.UsageStats {
	usage := jsonx.Map(value)
	if usage == nil {
		return nil
	}
	prompt := firstInt64(usage["prompt_tokens"], usage["input_tokens"])
	completion := firstInt64(usage["completion_tokens"], usage["output_tokens"])
	cached := firstInt64(
		jsonx.Map(usage["prompt_tokens_details"])["cached_tokens"],
		jsonx.Map(usage["input_tokens_details"])["cached_tokens"],
	)
	reasoning := firstInt64(
		jsonx.Map(usage["completion_tokens_details"])["reasoning_tokens"],
		jsonx.Map(usage["output_tokens_details"])["reasoning_tokens"],
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
	// The gateway splits the bill: "cost" covers the model legs only, while
	// "market_cost"/"gateway_cost" also include per-call tool fees such as web
	// search. Prefer the billed total so history and key spend limits count the
	// search fee too; fall back to the model-only cost when no total is given.
	for _, key := range []string{"market_cost", "gateway_cost", "cost"} {
		if cost, ok := floatValue(usage[key]); ok {
			stats.Cost = &cost
			break
		}
	}
	if stats.PromptTokens == 0 && stats.CompletionTokens == 0 && stats.TotalTokens == 0 &&
		stats.ReasoningTokens == 0 && stats.CachedTokens == 0 && stats.Cost == nil {
		return nil
	}
	return stats
}

func finishReasonFromChat(value map[string]any) string {
	for _, raw := range jsonx.Slice(value["choices"]) {
		choice := jsonx.Map(raw)
		reason := strings.TrimSpace(jsonx.String(choice["finish_reason"]))
		if reason != "" && reason != "null" {
			return reason
		}
	}
	return ""
}

func chatChunkHasToken(chunk map[string]any) bool {
	if hasOutputToken(jsonx.Map(chunk["delta"])) || hasOutputToken(jsonx.Map(chunk["message"])) {
		return true
	}
	for _, raw := range jsonx.Slice(chunk["choices"]) {
		choice := jsonx.Map(raw)
		if hasOutputToken(jsonx.Map(choice["delta"])) || hasOutputToken(jsonx.Map(choice["message"])) {
			return true
		}
	}
	return false
}

func hasOutputToken(object map[string]any) bool {
	if object == nil {
		return false
	}
	if strings.TrimSpace(jsonx.String(object["content"])) != "" {
		return true
	}
	if strings.TrimSpace(jsonx.String(object["reasoning"])) != "" {
		return true
	}
	if strings.TrimSpace(jsonx.String(object["reasoning_content"])) != "" {
		return true
	}
	if strings.TrimSpace(jsonx.String(object["refusal"])) != "" {
		return true
	}
	if len(jsonx.Slice(object["tool_calls"])) > 0 {
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
