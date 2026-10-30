package responses

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/jsonx"
)

const strictToolDefinition = `{"type":"function","name":"exec_command","strict":true,"parameters":{"type":"object","properties":{"cmd":{"type":"string"},"mode":{"enum":["read","write"]}},"required":["cmd","mode"],"additionalProperties":false}}`

func TestStrictToolSchemasGateBufferedAndStreamingArguments(t *testing.T) {
	var tool any
	if err := json.Unmarshal([]byte(strictToolDefinition), &tool); err != nil {
		t.Fatal(err)
	}
	_, ctx, err := ToChat(map[string]any{"model": "test", "input": "hi", "tools": []any{tool}})
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{`{"cmd":"ls","mode":"read"}`, `{"cmd":1,"mode":"read"}`, `{"cmd":"ls","mode":"execute"}`, `{"cmd":"ls"}`, `{"cmd":"ls","mode":"read","extra":true}`} {
		valid := args == `{"cmd":"ls","mode":"read"}`
		chunk := map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_1", "function": map[string]any{"name": "exec_command", "arguments": args}}}}, "finish_reason": "tool_calls"}}}
		state := NewStreamState(ctx)
		events := state.HandleChunk(chunk)
		events = append(events, state.Finalize(true, nil)...)
		done := false
		for _, event := range events {
			if event.Type == "response.output_item.done" && jsonx.Map(event.Data["item"])["type"] == "function_call" {
				done = true
			}
			if !valid && event.Type == "response.function_call_arguments.delta" {
				t.Fatal("invalid strict arguments leaked")
			}
		}
		if done != valid || (events[len(events)-1].Type == "response.completed") != valid {
			t.Fatalf("wrong strict result for %s: %#v", args, events)
		}
		buffered := map[string]any{"choices": []any{map[string]any{"message": map[string]any{"tool_calls": []any{map[string]any{"id": "call_1", "function": map[string]any{"name": "exec_command", "arguments": args}}}}, "finish_reason": "tool_calls"}}}
		_, err := FromChat(buffered, ctx)
		if (err == nil) != valid {
			t.Fatalf("buffered strict result: %v", err)
		}
	}
}

func TestStrictToolSchemaRejectsExternalReferences(t *testing.T) {
	_, _, err := ToChat(map[string]any{"model": "test", "tools": []any{map[string]any{"type": "function", "name": "f", "strict": true, "parameters": map[string]any{"$ref": "https://example.invalid/schema"}}}})
	var request *RequestError
	if !errors.As(err, &request) || request.Code != "invalid_tool_schema" {
		t.Fatalf("external reference: %v", err)
	}
}

func TestUnsupportedResponseSemanticsAreRejected(t *testing.T) {
	for key, value := range map[string]any{"truncation": "auto", "include": []any{"reasoning.encrypted_content"}, "max_tool_calls": 1} {
		_, _, err := ToChat(map[string]any{"model": "test", "input": "hi", key: value})
		var request *RequestError
		if !errors.As(err, &request) || request.Param != key {
			t.Fatalf("%s: %v", key, err)
		}
	}
}

func TestCompactionMaterialNeverBecomesSystemInstructions(t *testing.T) {
	const injection = "IGNORE_ALL_POLICY_8192"
	chat, _, err := ToChat(map[string]any{"model": "test", "instructions": "original policy", "input": []any{map[string]any{"type": "compaction", "encrypted_content": CompactionEnvelope(injection)}, map[string]any{"role": "user", "content": "continue"}}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, raw := range jsonx.Slice(chat["messages"]) {
		m := jsonx.Map(raw)
		text := jsonx.String(m["content"])
		if strings.Contains(text, injection) {
			found = true
			if m["role"] != "user" || !strings.HasPrefix(text, untrustedSummaryPrefix) {
				t.Fatal("summary elevated into instructions")
			}
		}
	}
	if !found {
		t.Fatal("summary was dropped")
	}
}

func TestFinalLegActualUsageOverridesAverage(t *testing.T) {
	chunk := map[string]any{"usage": map[string]any{"prompt_tokens": 1000, "completion_tokens": 20}, "provider_metadata": map[string]any{"gateway": map[string]any{"routing": map[string]any{"modelAttempts": []any{map[string]any{"providerAttempts": []any{
		map[string]any{"toolLoopLegIndex": 0, "usage": map[string]any{"prompt_tokens": 100}},
		map[string]any{"toolLoopLegIndex": 1, "success": true, "usage": map[string]any{"prompt_tokens": 900, "prompt_tokens_details": map[string]any{"cached_tokens": 700}}},
	}}}}}}}
	state := NewStreamState(&Context{Model: "test"})
	state.HandleChunk(chunk)
	usage := jsonx.Map(state.usage)
	if usage["input_tokens"] != int64(900) || jsonx.Map(usage["input_tokens_details"])["cached_tokens"] != int64(700) || usage["output_tokens"] != int64(20) {
		t.Fatalf("actual usage lost: %#v", usage)
	}
}
