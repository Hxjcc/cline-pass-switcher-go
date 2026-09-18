package responses

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCustomToolNamespacesRoundTrip(t *testing.T) {
	tools := []any{}
	for _, namespace := range []string{"editor", "other"} {
		tools = append(tools, map[string]any{"type": "namespace", "name": namespace,
			"tools": []any{map[string]any{"type": "custom", "name": "apply_patch"}}})
	}
	_, context, err := ToChat(map[string]any{"model": "test", "input": "hi", "tools": tools})
	if err != nil {
		t.Fatal(err)
	}
	for _, namespace := range []string{"editor", "other"} {
		chat := map[string]any{"choices": []any{map[string]any{"message": map[string]any{
			"tool_calls": []any{map[string]any{"id": "call1", "function": map[string]any{
				"name": namespace + "__apply_patch", "arguments": `{"input":"patch"}`,
			}}},
		}, "finish_reason": "tool_calls"}}}
		events, err := EventsFromChat(chat, context)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if event.Type == "response.output_item.added" || event.Type == "response.output_item.done" {
				item := asMap(event.Data["item"])
				if item["namespace"] != namespace || item["name"] != "apply_patch" {
					t.Fatalf("tool identity lost: %#v", item)
				}
			}
		}
		response := asMap(events[len(events)-1].Data["response"])
		input := append(asSlice(response["output"]), map[string]any{"type": "custom_tool_call_output", "call_id": "call1", "output": "done"})
		replay, _, err := ToChat(map[string]any{"model": "test", "tools": tools, "input": input})
		if err != nil {
			t.Fatal(err)
		}
		call := asMap(asSlice(asMap(asSlice(replay["messages"])[0])["tool_calls"])[0])
		if asMap(call["function"])["name"] != namespace+"__apply_patch" {
			t.Fatalf("replay selected wrong tool: %#v", call)
		}
	}
}

func TestRefusalHasDedicatedLifecycleAndReplays(t *testing.T) {
	_, context, _ := ToChat(map[string]any{"model": "test", "input": "hi"})
	state := NewStreamState(context)
	events := []Event{}
	for _, delta := range []map[string]any{
		{"content": "prefix"}, {"refusal": "Cannot"}, {"refusal": " comply"}, {"content": "suffix"},
	} {
		events = append(events, state.HandleChunk(map[string]any{"choices": []any{map[string]any{"delta": delta}}})...)
	}
	events = append(events, state.Finalize(true, nil)...)
	response := asMap(events[len(events)-1].Data["response"])
	output := asSlice(response["output"])
	if response["status"] != "completed" || len(output) != 1 {
		t.Fatalf("invalid result: %#v", response)
	}
	parts := asSlice(asMap(output[0])["content"])
	if len(parts) != 3 || asMap(parts[0])["text"] != "prefix" || asMap(parts[1])["type"] != "refusal" || asMap(parts[1])["refusal"] != "Cannot comply" || asMap(parts[2])["text"] != "suffix" {
		t.Fatalf("part order or types lost: %#v", parts)
	}
	var refusal strings.Builder
	seenDone := false
	for _, event := range events {
		if event.Type == "response.refusal.delta" {
			if event.Data["content_index"] != 1 {
				t.Fatalf("wrong refusal index: %#v", event)
			}
			refusal.WriteString(asString(event.Data["delta"]))
		}
		if event.Type == "response.refusal.done" {
			seenDone = event.Data["refusal"] == "Cannot comply" && event.Data["content_index"] == 1
		}
		if event.Type == "response.output_text.delta" && strings.Contains(asString(event.Data["delta"]), "Cannot") {
			t.Fatal("refusal became plain text")
		}
	}
	if refusal.String() != "Cannot comply" || !seenDone {
		t.Fatal("refusal lifecycle incomplete")
	}
	replay, _, err := ToChat(map[string]any{"model": "test", "input": output})
	if err != nil {
		t.Fatal(err)
	}
	message := asMap(asSlice(replay["messages"])[0])
	if message["refusal"] != "Cannot comply" || message["content"] != "prefixsuffix" {
		t.Fatalf("refusal replay flattened: %#v", message)
	}
}

func TestBufferedAndStreamedRefusalMatch(t *testing.T) {
	_, context, _ := ToChat(map[string]any{"model": "test", "input": "hi"})
	for _, message := range []map[string]any{
		{"content": nil, "refusal": "No"},
		{"content": []any{map[string]any{"type": "refusal", "refusal": "No"}}},
	} {
		buffered, err := FromChat(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": "stop"}}}, context)
		if err != nil {
			t.Fatal(err)
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": message, "finish_reason": "stop"}}})
		adapter := NewStreamAdapter(context)
		events := adapter.Feed([]byte("data: " + string(chunk) + "\n\ndata: [DONE]\n\n"))
		streamed := asMap(events[len(events)-1].Data["response"])
		left := asMap(asSlice(buffered["output"])[0])["content"]
		right := asMap(asSlice(streamed["output"])[0])["content"]
		if !reflect.DeepEqual(left, right) || asMap(asSlice(left)[0])["type"] != "refusal" {
			t.Fatalf("refusal changed: %#v %#v", left, right)
		}
	}
}

func TestUnsupportedCapabilitiesReturnPreciseErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  map[string]any
		param string
	}{
		{"conversation", map[string]any{"conversation": "conv_123"}, "conversation"},
		{"conversation object", map[string]any{"conversation": map[string]any{"id": "conv_123"}}, "conversation"},
		{"background", map[string]any{"background": true}, "background"},
		{"web search", map[string]any{"tools": []any{map[string]any{"type": "web_search"}}}, "tools[0].type"},
		{"nested server tool", map[string]any{"tools": []any{map[string]any{"type": "namespace", "tools": []any{map[string]any{"type": "file_search"}}}}}, "tools[0].tools[0].type"},
		{"server tool choice", map[string]any{"tool_choice": map[string]any{"type": "web_search"}}, "tool_choice.type"},
		{"hosted tool search", map[string]any{"tools": []any{map[string]any{"type": "tool_search", "execution": "server"}}}, "tools[0].execution"},
		{"dynamic server tool", map[string]any{"input": []any{map[string]any{"type": "additional_tools", "tools": []any{map[string]any{"type": "mcp"}}}}}, "input[0].tools[0].type"},
		{"file", map[string]any{"input": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_file", "file_data": "abc"}}}}}, "input[0].content[0].type"},
		{"audio", map[string]any{"input": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_audio"}}}}}, "input[0].content[0].type"},
		{"image file reference", map[string]any{"input": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_image", "file_id": "file_123"}}}}}, "input[0].content[0]"},
		{"unknown item", map[string]any{"input": []any{map[string]any{"type": "item_reference", "id": "msg_123"}}}, "input[0].type"},
		{"foreign compaction", map[string]any{"input": []any{map[string]any{"type": "compaction", "encrypted_content": "unreadable"}}}, "input[0].encrypted_content"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.body["model"] = "test"
			if tc.body["input"] == nil {
				tc.body["input"] = "hi"
			}
			chat, _, err := ToChat(tc.body)
			var unsupported *RequestError
			if chat != nil || !errors.As(err, &unsupported) || unsupported.Param != tc.param {
				t.Fatalf("missing capability error: %#v %v", chat, err)
			}
		})
	}
}

func TestToolBusinessJSONIsNotValidatedAsProtocol(t *testing.T) {
	for _, output := range []any{
		`{"type":"input_file","content":"ordinary business data"}`,
		map[string]any{"record": map[string]any{"type": "input_file", "content": "ordinary business data"}},
	} {
		_, _, err := ToChat(map[string]any{"model": "test", "input": []any{
			map[string]any{"type": "function_call", "name": "read", "call_id": "c", "arguments": "{}"},
			map[string]any{"type": "function_call_output", "call_id": "c", "output": output},
		}})
		if err != nil {
			t.Fatalf("business data misclassified: %v", err)
		}
	}
}
