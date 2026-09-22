package responses

import (
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
)

func TestResponsesUsageSplitsGatewaySearchLegs(t *testing.T) {
	chat := map[string]any{
		"id": "chatcmpl-search",
		"choices": []any{map[string]any{
			"finish_reason": "stop",
			"message": map[string]any{
				"role":    "assistant",
				"content": "five headlines",
				"provider_metadata": map[string]any{
					"gateway": map[string]any{
						"gatewayToolCalls": []any{map[string]any{"type": "vercel:exa_search"}},
					},
				},
			},
		}},
		"usage": map[string]any{
			"prompt_tokens":             1_295_586,
			"completion_tokens":         120,
			"total_tokens":              1_295_706,
			"prompt_tokens_details":     map[string]any{"cached_tokens": 1_292_800},
			"completion_tokens_details": map[string]any{"reasoning_tokens": 40},
		},
	}
	response, err := FromChat(chat, &Context{Model: "cline-pass/deepseek-v4.1-flash"})
	if err != nil {
		t.Fatal(err)
	}
	usage := jsonx.Map(response["usage"])
	if usage["input_tokens"] != int64(647_793) || jsonx.Map(usage["input_tokens_details"])["cached_tokens"] != int64(646_400) {
		t.Fatalf("input tokens should be one leg of the summed usage, got %#v", usage)
	}
	if usage["output_tokens"] != int64(120) || jsonx.Map(usage["output_tokens_details"])["reasoning_tokens"] != int64(40) {
		t.Fatalf("output tokens should stay on the raw count, got %#v", usage)
	}
	if usage["total_tokens"] != int64(647_793+120) {
		t.Fatalf("total should be the adjusted input plus output, got %#v", usage)
	}
}

func TestResponsesUsageWithoutGatewayToolsStaysRaw(t *testing.T) {
	chat := map[string]any{
		"id": "chatcmpl-plain",
		"choices": []any{map[string]any{
			"finish_reason": "stop",
			"message":       map[string]any{"role": "assistant", "content": "ok"},
		}},
		"usage": map[string]any{
			"prompt_tokens":         35,
			"completion_tokens":     2,
			"prompt_tokens_details": map[string]any{"cached_tokens": 0},
		},
	}
	response, err := FromChat(chat, &Context{Model: "cline-pass/test", InputTokenCap: 1_048_576})
	if err != nil {
		t.Fatal(err)
	}
	usage := jsonx.Map(response["usage"])
	if usage["input_tokens"] != int64(35) || usage["output_tokens"] != int64(2) {
		t.Fatalf("a single leg should be unchanged, got %#v", usage)
	}
}

func TestResponsesUsageCapsInputAtTheContextWindow(t *testing.T) {
	chat := map[string]any{
		"id": "chatcmpl-cap",
		"choices": []any{map[string]any{
			"finish_reason": "stop",
			"message":       map[string]any{"role": "assistant", "content": "ok"},
		}},
		"usage": map[string]any{
			"prompt_tokens":         3_000_000,
			"completion_tokens":     10,
			"prompt_tokens_details": map[string]any{"cached_tokens": 2_900_000},
		},
	}
	response, err := FromChat(chat, &Context{Model: "cline-pass/test", InputTokenCap: 1_048_576})
	if err != nil {
		t.Fatal(err)
	}
	usage := jsonx.Map(response["usage"])
	if usage["input_tokens"] != int64(1_048_576) {
		t.Fatalf("input should clamp to the context window, got %#v", usage)
	}
	if jsonx.Map(usage["input_tokens_details"])["cached_tokens"] != int64(1_048_576) {
		t.Fatalf("cached tokens cannot exceed the reported input, got %#v", usage)
	}
}

func TestStreamUsageLearnsSearchLegsAfterTheUsageChunk(t *testing.T) {
	adapter := NewStreamAdapter(&Context{Model: "cline-pass/test"})
	events := adapter.Feed([]byte("data: {\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":4,\"prompt_tokens_details\":{\"cached_tokens\":80}}}\n\n"))
	events = append(events, adapter.Feed([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\",\"provider_metadata\":{\"gateway\":{\"gatewayToolCalls\":[{}]}}},\"finish_reason\":\"stop\"}]}\n\n"))...)
	events = append(events, adapter.Finish(nil)...)
	var usage map[string]any
	for _, event := range events {
		if event.Type != "response.completed" {
			continue
		}
		usage = jsonx.Map(jsonx.Map(event.Data["response"])["usage"])
	}
	if usage["input_tokens"] != int64(50) || jsonx.Map(usage["input_tokens_details"])["cached_tokens"] != int64(40) {
		t.Fatalf("a later gateway tool call should still split the earlier usage, got %#v", usage)
	}
}

// The gateway reports gatewayToolCalls as a per-tool count object, as seen in
// production: {"exa_search": 2}. Two searches plus the final answer are three
// legs, so the summed prompt has to be divided by three.
func TestResponsesUsageSplitsCountedGatewayToolCalls(t *testing.T) {
	chat := map[string]any{
		"id": "chatcmpl-counts",
		"choices": []any{map[string]any{
			"finish_reason": "stop",
			"message": map[string]any{
				"role":    "assistant",
				"content": "eight degrees",
				"provider_metadata": map[string]any{
					"gateway": map[string]any{
						"gatewayToolCalls": map[string]any{"exa_search": float64(2)},
					},
				},
			},
		}},
		"usage": map[string]any{
			"prompt_tokens":         8_536,
			"completion_tokens":     272,
			"prompt_tokens_details": map[string]any{"cached_tokens": 900},
		},
	}
	response, err := FromChat(chat, &Context{Model: "cline-pass/deepseek-v4.1-flash"})
	if err != nil {
		t.Fatal(err)
	}
	usage := jsonx.Map(response["usage"])
	if usage["input_tokens"] != int64(2_845) {
		t.Fatalf("counted gateway calls should split the summed prompt, got %#v", usage)
	}
	if jsonx.Map(usage["input_tokens_details"])["cached_tokens"] != int64(300) {
		t.Fatalf("cached tokens should split by the same leg count, got %#v", usage)
	}
}

// Counts are cumulative and per tool, so legs from different chunks and
// different tools must add up instead of replacing each other.
func TestStreamUsageAddsLegsPerToolAcrossChunks(t *testing.T) {
	adapter := NewStreamAdapter(&Context{Model: "cline-pass/test"})
	events := adapter.Feed([]byte("data: {\"usage\":{\"prompt_tokens\":900,\"completion_tokens\":6,\"prompt_tokens_details\":{\"cached_tokens\":600}}}\n\n"))
	events = append(events, adapter.Feed([]byte("data: {\"choices\":[{\"delta\":{\"provider_metadata\":{\"gateway\":{\"gatewayToolCalls\":{\"exa_search\":1}}}}}]}\n\n"))...)
	events = append(events, adapter.Feed([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\",\"provider_metadata\":{\"gateway\":{\"gatewayToolCalls\":{\"browserbase_fetch\":1}}}},\"finish_reason\":\"stop\"}]}\n\n"))...)
	events = append(events, adapter.Finish(nil)...)
	var usage map[string]any
	for _, event := range events {
		if event.Type != "response.completed" {
			continue
		}
		usage = jsonx.Map(jsonx.Map(event.Data["response"])["usage"])
	}
	// search leg + fetch leg + the final answer = three legs
	if usage["input_tokens"] != int64(300) || jsonx.Map(usage["input_tokens_details"])["cached_tokens"] != int64(200) {
		t.Fatalf("separate tool counts should add up to the same leg count, got %#v", usage)
	}
}
