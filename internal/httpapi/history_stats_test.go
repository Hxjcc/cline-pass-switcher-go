package httpapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

func TestUsageFromChatAndResponsesShapes(t *testing.T) {
	chat := usageFromValue(map[string]any{
		"prompt_tokens":     25.0,
		"completion_tokens": 42.0,
		"total_tokens":      67.0,
		"cost":              0.000315,
		"prompt_tokens_details": map[string]any{
			"cached_tokens": 8.0,
		},
		"completion_tokens_details": map[string]any{
			"reasoning_tokens": 12.0,
		},
	})
	if chat == nil || chat.PromptTokens != 25 || chat.CompletionTokens != 42 || chat.CachedTokens != 8 || chat.ReasoningTokens != 12 {
		t.Fatalf("chat usage not parsed: %#v", chat)
	}
	if chat.Cost == nil || *chat.Cost != 0.000315 {
		t.Fatalf("cost not parsed: %#v", chat)
	}

	responses := usageFromValue(map[string]any{
		"input_tokens":          10.0,
		"output_tokens":         4.0,
		"input_tokens_details":  map[string]any{"cached_tokens": 2.0},
		"output_tokens_details": map[string]any{"reasoning_tokens": 1.0},
	})
	if responses == nil || responses.PromptTokens != 10 || responses.CompletionTokens != 4 || responses.TotalTokens != 14 {
		t.Fatalf("responses usage not parsed: %#v", responses)
	}
}

func TestStreamStatsCapturesFirstTokenAndUsage(t *testing.T) {
	stats := newStreamStats(time.Now().Add(-1500 * time.Millisecond))
	stats.Observe([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n"))
	if stats.TTFTMs() != 0 {
		t.Fatalf("role-only chunk should not count as first token: %d", stats.TTFTMs())
	}
	stats.Observe([]byte("data: {\"choices\":[{\"delta\":{\"reasoning\":\"think\"},\"finish_reason\":null}]}\n\n"))
	if stats.TTFTMs() < 1 {
		t.Fatal("reasoning delta should count as first token")
	}
	stats.Observe([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":3,\"total_tokens\":14,\"cost\":0.01}}\n\n"))
	if stats.FinishReason() != "stop" {
		t.Fatalf("finish reason: %q", stats.FinishReason())
	}
	usage := stats.Usage()
	if usage == nil || usage.PromptTokens != 11 || usage.CompletionTokens != 3 || usage.Cost == nil {
		t.Fatalf("usage: %#v", usage)
	}
}

func TestChatChunkHasTokenIgnoresEmptyReasoningDetails(t *testing.T) {
	if chatChunkHasToken(map[string]any{
		"choices": []any{map[string]any{"delta": map[string]any{"reasoning_details": []any{}}}},
	}) {
		t.Fatal("empty reasoning_details should not count")
	}
	if !chatChunkHasToken(map[string]any{
		"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"id": "call_1"}}}}},
	}) {
		t.Fatal("tool_calls should count as first token")
	}
}

func TestEffortFromChatBody(t *testing.T) {
	if got := effortFromChatBody(map[string]any{"reasoning_effort": "high"}); got != "high" {
		t.Fatalf("reasoning_effort: %q", got)
	}
	if got := recordedEffort("xhigh", map[string]any{"reasoning": map[string]any{"effort": "high"}}); got != "xhigh" {
		t.Fatalf("mapped effort should win: %q", got)
	}
	if got := effortFromChatBody(map[string]any{"reasoning": map[string]any{"effort": "medium"}}); got != "medium" {
		t.Fatalf("nested effort: %q", got)
	}
	entry := model.HistoryEntry{}
	applyReasoningEffort(&entry, "high", "medium", nil)
	if entry.Effort != "high" || entry.RequestedEffort != "medium" {
		t.Fatalf("applyReasoningEffort: %#v", entry)
	}
}

func TestFriendlyCancelText(t *testing.T) {
	if got := friendlyCancelText("context canceled"); got != "客户端取消" {
		t.Fatalf("got %q", got)
	}
	if got := friendlyStreamError(context.Canceled); got != "客户端取消" {
		t.Fatalf("got %q", got)
	}
	if got := friendlyCancelText("upstream timeout"); got != "upstream timeout" {
		t.Fatalf("got %q", got)
	}
}

func TestSSEDataPayloadJoinsEventLines(t *testing.T) {
	payload := sseDataPayload("event: x\ndata: {\"a\":\ndata: 1}\n")
	if payload != "{\"a\":\n1}" {
		t.Fatalf("payload: %q", payload)
	}
	stats := newStreamStats(time.Now())
	stats.Observe([]byte("data: {\"error\":\ndata: {\"message\":\"boom\"}}\n\n"))
	if !strings.Contains(stats.StreamError(), "boom") {
		t.Fatalf("multi-line error event was ignored: %q", stats.StreamError())
	}
}
