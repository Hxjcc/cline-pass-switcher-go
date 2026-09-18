package responses

import (
	"strings"
	"testing"
)

var benchmarkStreamEvents []Event

func TestLongStreamRetainsContentAndPublishedDeltas(t *testing.T) {
	state := NewStreamState(&Context{Model: "test"})
	piece := strings.Repeat("x", 128)
	var firstText, firstArguments Event
	for i := range 1024 {
		for _, event := range state.pushReasoning(piece) {
			if i == 0 && event.Type == "response.reasoning_summary_text.delta" {
				firstText = event
			}
		}
	}
	for range 1024 {
		state.emitText(piece)
	}
	for i := range 1024 {
		arguments := piece
		if i == 0 {
			arguments = `{"input":"` + arguments
		}
		if i == 1023 {
			arguments += `"}`
		}
		for _, event := range state.pushToolCall(map[string]any{"index": 0, "id": "call1", "function": map[string]any{"name": "exec", "arguments": arguments}}) {
			if i == 0 && event.Type == "response.function_call_arguments.delta" {
				firstArguments = event
			}
		}
	}
	events := state.Finalize(true, nil)
	final := asMap(events[len(events)-1].Data["response"])
	output := asSlice(final["output"])
	want := strings.Repeat(piece, 1024)
	if final["status"] != "completed" || len(output) != 3 {
		t.Fatalf("unexpected final response: status=%v items=%d", final["status"], len(output))
	}
	if asMap(asSlice(asMap(output[0])["summary"])[0])["text"] != want || textFromParts(asMap(output[1])["content"]) != want || asMap(output[2])["arguments"] != `{"input":"`+want+`"}` {
		t.Fatal("long stream lost text or arguments")
	}
	if firstText.Data["delta"] != piece || firstArguments.Data["delta"] != `{"input":"`+piece {
		t.Fatal("growing buffers mutated an already published delta")
	}
}

func BenchmarkStreamAccumulation(b *testing.B) {
	for _, field := range []string{"content", "reasoning_content", "tool_arguments"} {
		b.Run(field, func(b *testing.B) {
			const chunks = 2048
			piece := strings.Repeat("x", 128)
			b.ReportAllocs()
			b.SetBytes(chunks * int64(len(piece)))
			for b.Loop() {
				state := NewStreamState(&Context{Model: "test"})
				for i := range chunks {
					delta := map[string]any{field: piece}
					if field == "tool_arguments" {
						arguments := piece
						if i == 0 {
							arguments = `{"input":"` + arguments
						}
						if i == chunks-1 {
							arguments += `"}`
						}
						delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call1", "function": map[string]any{"name": "exec", "arguments": arguments}}}}
					}
					benchmarkStreamEvents = state.HandleChunk(map[string]any{"choices": []any{map[string]any{"delta": delta}}})
				}
				benchmarkStreamEvents = state.Finalize(true, nil)
			}
		})
	}
}
