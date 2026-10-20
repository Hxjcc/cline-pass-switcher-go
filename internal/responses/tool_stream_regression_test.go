package responses

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/jsonx"
)

func toolDelta(call map[string]any) map[string]any {
	return map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{call}}}}}
}

func TestToolCallIdentitySurvivesMissingAndLateIDs(t *testing.T) {
	for _, indexed := range []bool{false, true} {
		for _, lateID := range []string{"", "late-id"} {
			t.Run(fmt.Sprintf("indexed=%v/late=%s", indexed, lateID), func(t *testing.T) {
				_, ctx, err := ToChat(map[string]any{"model": "test", "input": "hi", "tools": []any{map[string]any{"type": "function", "name": "read"}}})
				if err != nil {
					t.Fatal(err)
				}
				state := NewStreamState(ctx)
				first := map[string]any{"function": map[string]any{"name": "read", "arguments": "{"}}
				second := map[string]any{"function": map[string]any{"arguments": "}"}}
				if indexed {
					first["index"], second["index"] = 0, 0
				}
				events := state.HandleChunk(toolDelta(first))
				if lateID != "" {
					second["id"] = lateID
				}
				events = append(events, state.HandleChunk(toolDelta(second))...)
				events = append(events, state.HandleChunk(map[string]any{"choices": []any{map[string]any{"finish_reason": "tool_calls"}}})...)
				events = append(events, state.Finalize(true, nil)...)
				added, done := "", ""
				for _, event := range events {
					item := jsonx.Map(event.Data["item"])
					if event.Type == "response.output_item.added" {
						added = jsonx.String(item["call_id"])
					}
					if event.Type == "response.output_item.done" {
						done = jsonx.String(item["call_id"])
					}
				}
				if added == "" || added != done || OutcomeFromEvents(events).Status != "completed" {
					t.Fatalf("identity changed: added=%q done=%q outcome=%+v", added, done, OutcomeFromEvents(events))
				}
			})
		}
	}
}

func TestProviderToolContinuationChunksStayHidden(t *testing.T) {
	for _, identity := range []string{"index", "id", "implicit"} {
		for _, reuseIndex := range []bool{false, true} {
			t.Run(identity+map[bool]string{false: "/text", true: "/client-call"}[reuseIndex], func(t *testing.T) {
				_, ctx, err := ToChatWithOptions(map[string]any{"model": "test", "input": "hi", "tools": []any{map[string]any{"type": "web_search"}, map[string]any{"type": "function", "name": "read"}}}, Options{WebSearchUpstream: "vercel:exa_search"})
				if err != nil {
					t.Fatal(err)
				}
				state := NewStreamState(ctx)
				first := map[string]any{"id": "provider1", "function": map[string]any{"name": "exa_search", "arguments": "{\"q\":\""}}
				second := map[string]any{"function": map[string]any{"arguments": "topic\"}"}}
				if identity == "index" {
					first["index"] = 0
					second["index"] = 0
				}
				if identity == "id" {
					second["id"] = "provider1"
				}
				events := state.HandleChunk(toolDelta(first))
				events = append(events, state.HandleChunk(toolDelta(second))...)
				reason := "stop"
				if reuseIndex {
					reason = "tool_calls"
					events = append(events, state.HandleChunk(toolDelta(map[string]any{"index": 0, "id": "client1", "function": map[string]any{"name": "read", "arguments": "{}"}}))...)
				} else {
					events = append(events, state.HandleChunk(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "search answer"}}}})...)
				}
				events = append(events, state.HandleChunk(map[string]any{"choices": []any{map[string]any{"finish_reason": reason}}})...)
				events = append(events, state.Finalize(true, nil)...)
				if outcome := OutcomeFromEvents(events); outcome.Status != "completed" {
					t.Fatalf("provider continuation broke output: %+v", outcome)
				}
				for _, event := range events {
					if event.Type == "response.output_item.added" {
						item := jsonx.Map(event.Data["item"])
						if event.Data["output_index"] != 0 || item["name"] == "exa_search" {
							t.Fatalf("provider item leaked or consumed output index: %+v", event.Data)
						}
					}
				}
			})
		}
	}
}

func TestObjectToolArgumentsPreserveNumberPrecision(t *testing.T) {
	for _, kind := range []string{"tool_search", "local_shell"} {
		t.Run(kind, func(t *testing.T) {
			_, ctx, err := ToChat(map[string]any{"model": "test", "input": "hi", "tools": []any{map[string]any{"type": kind, "execution": "client"}}})
			if err != nil {
				t.Fatal(err)
			}
			const arguments = `{"resource_id":9007199254740993,"nested":{"count":9223372036854775807,"ratio":1.234567890123456789}}`
			response, err := FromChat(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"tool_calls": []any{map[string]any{"id": "call1", "function": map[string]any{"name": kind, "arguments": arguments}}}}, "finish_reason": "tool_calls"}}}, ctx)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(response["output"])
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range []string{"9007199254740993", "9223372036854775807", "1.234567890123456789"} {
				if !strings.Contains(string(raw), value) {
					t.Fatalf("argument %s rounded: %s", value, raw)
				}
			}
		})
	}
	if got := customInputFromArguments(`{"input":{"id":9007199254740993}}`); got != `{"id":9007199254740993}` {
		t.Fatalf("custom JSON fallback rounded input: %s", got)
	}
}
