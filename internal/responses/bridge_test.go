package responses

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
)

func TestToChatPreservesReasoningAndUserImage(t *testing.T) {
	body := map[string]any{
		"model": "cline-pass/deepseek-v4.1-flash",
		"input": []any{map[string]any{
			"type": "message", "role": "user",
			"content": []any{
				map[string]any{"type": "input_text", "text": "describe"},
				map[string]any{"type": "input_image", "image_url": "data:image/png;base64,AAAA", "detail": "original"},
			},
		}},
		"reasoning": map[string]any{"effort": "xhigh", "summary": "auto"},
	}
	chat, _, err := ToChat(body)
	if err != nil {
		t.Fatal(err)
	}
	if chat["reasoning_effort"] != "xhigh" {
		t.Fatalf("reasoning effort not forwarded: %#v", chat["reasoning_effort"])
	}
	if chat["include_reasoning"] != true {
		t.Fatalf("include_reasoning was not requested: %#v", chat["include_reasoning"])
	}
	if jsonx.Map(chat["reasoning"])["effort"] != "xhigh" {
		t.Fatalf("native reasoning object was not forwarded: %#v", chat["reasoning"])
	}
	messages := jsonx.Slice(chat["messages"])
	content := jsonx.Slice(jsonx.Map(messages[0])["content"])
	if len(content) != 2 {
		t.Fatalf("expected text and image content, got %#v", content)
	}
	image := jsonx.Map(jsonx.Map(content[1])["image_url"])
	if image["url"] != "data:image/png;base64,AAAA" || image["detail"] != "high" {
		t.Fatalf("unexpected image conversion: %#v", image)
	}
}

func TestToChatMovesViewImageAfterCompleteToolGroup(t *testing.T) {
	body := map[string]any{
		"model": "cline-pass/deepseek-v4.1-flash",
		"tools": []any{
			map[string]any{"type": "function", "name": "shell", "parameters": map[string]any{"type": "object"}},
			map[string]any{"type": "custom", "name": "view_image"},
		},
		"input": []any{
			map[string]any{"type": "function_call", "call_id": "call_shell", "name": "shell", "arguments": `{"cmd":"ok"}`},
			map[string]any{"type": "custom_tool_call", "call_id": "call_image", "name": "view_image", "input": `C:\\tmp\\a.png`},
			map[string]any{"type": "function_call_output", "call_id": "call_shell", "output": "shell ok"},
			map[string]any{
				"type": "custom_tool_call_output", "call_id": "call_image",
				"output": map[string]any{"content": []any{
					map[string]any{"type": "text", "text": "image loaded"},
					map[string]any{"type": "image", "data": "QUJD", "mimeType": "image/png"},
				}},
			},
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "continue"}}},
		},
	}
	chat, _, err := ToChat(body)
	if err != nil {
		t.Fatal(err)
	}
	messages := jsonx.Slice(chat["messages"])
	if len(messages) != 5 {
		raw, _ := json.Marshal(messages)
		t.Fatalf("expected assistant, two tools, image user, final user; got %s", raw)
	}
	if jsonx.Map(messages[0])["role"] != "assistant" || len(jsonx.Slice(jsonx.Map(messages[0])["tool_calls"])) != 2 {
		t.Fatalf("tool calls were not grouped: %#v", messages[0])
	}
	if jsonx.Map(messages[1])["role"] != "tool" || jsonx.Map(messages[2])["role"] != "tool" {
		t.Fatalf("tool results must precede images: %#v", messages)
	}
	if !strings.Contains(jsonx.String(jsonx.Map(messages[2])["content"]), "image loaded") {
		t.Fatalf("text alongside image was lost: %#v", messages[2])
	}
	imageMessage := jsonx.Map(messages[3])
	if imageMessage["role"] != "user" {
		t.Fatalf("image should be moved to a user message: %#v", imageMessage)
	}
	parts := jsonx.Slice(imageMessage["content"])
	imageURL := ""
	for _, raw := range parts {
		part := jsonx.Map(raw)
		if part["type"] == "image_url" {
			imageURL = jsonx.String(jsonx.Map(part["image_url"])["url"])
		}
	}
	if imageURL != "data:image/png;base64,QUJD" {
		t.Fatalf("view_image data was not forwarded: %q", imageURL)
	}
}

func TestToChatKeepsBatchedViewImageOutputsAcrossResizeNotices(t *testing.T) {
	body := map[string]any{
		"model": "cline-pass/glm-5.3-flash",
		"tools": []any{map[string]any{"type": "function", "name": "view_image", "parameters": map[string]any{"type": "object"}}},
		"input": []any{
			map[string]any{"type": "function_call", "call_id": "call_00_FcvzTdT13OubzpZTEKKs2202", "name": "view_image", "arguments": `{"path":"desktop.png"}`},
			map[string]any{"type": "function_call", "call_id": "call_01_IX7JwYr2fqHzBsB6ygcH6127", "name": "view_image", "arguments": `{"path":"mobile.png"}`},
			map[string]any{
				"type": "function_call_output", "call_id": "call_00_FcvzTdT13OubzpZTEKKs2202",
				"output": []any{map[string]any{"type": "input_image", "image_url": "data:image/png;base64,AAAA"}},
			},
			map[string]any{
				"type": "message", "role": "developer",
				"content": []any{map[string]any{"type": "input_text", "text": "<image_resize_notice>\nImage 1 of 1 in the preceding tool output was resized.\n</image_resize_notice>"}},
			},
			map[string]any{
				"type": "function_call_output", "call_id": "call_01_IX7JwYr2fqHzBsB6ygcH6127",
				"output": []any{map[string]any{"type": "input_image", "image_url": "data:image/png;base64,BBBB"}},
			},
			map[string]any{
				"type": "message", "role": "developer",
				"content": []any{map[string]any{"type": "input_text", "text": "<image_resize_notice>\nImage 1 of 1 in the preceding tool output was resized.\n</image_resize_notice>"}},
			},
		},
	}
	chat, _, err := ToChat(body)
	if err != nil {
		t.Fatalf("batched view_image history should convert: %v", err)
	}
	messages := jsonx.Slice(chat["messages"])
	if len(messages) < 5 {
		raw, _ := json.Marshal(messages)
		t.Fatalf("expected assistant, two tools, notices, images; got %s", raw)
	}
	assistant := jsonx.Map(messages[0])
	if len(jsonx.Slice(assistant["tool_calls"])) != 2 {
		t.Fatalf("both view_image calls should share one assistant message: %#v", assistant)
	}
	if jsonx.Map(messages[1])["tool_call_id"] != "call_00_FcvzTdT13OubzpZTEKKs2202" || jsonx.Map(messages[1])["content"] != toolMediaPlaceholder {
		t.Fatalf("first image tool output was not placeholder-paired: %#v", messages[1])
	}
	if jsonx.Map(messages[2])["tool_call_id"] != "call_01_IX7JwYr2fqHzBsB6ygcH6127" || jsonx.Map(messages[2])["content"] != toolMediaPlaceholder {
		t.Fatalf("second image tool output was not placeholder-paired: %#v", messages[2])
	}
	var imageCount int
	var sawNotice bool
	for _, raw := range messages[3:] {
		message := jsonx.Map(raw)
		// Mid-history developer notices are demoted to user turns; a system
		// message after the tool group would be rejected by some providers.
		if jsonx.String(message["role"]) != "user" {
			t.Fatalf("unexpected role after tool group: %#v", message)
		}
		if strings.Contains(textFromParts(message["content"]), "image_resize_notice") {
			sawNotice = true
		}
		for _, part := range jsonx.Slice(message["content"]) {
			if jsonx.Map(part)["type"] == "image_url" {
				imageCount++
			}
		}
	}
	if !sawNotice {
		t.Fatal("image resize notices were dropped")
	}
	if imageCount != 2 {
		t.Fatalf("expected 2 forwarded images, got %d: %#v", imageCount, messages)
	}
}

func TestToChatReplaysReasoningOnAssistantToolCall(t *testing.T) {
	body := map[string]any{
		"model": "cline-pass/qwen3.8-max",
		"tools": []any{map[string]any{"type": "function", "name": "shell", "parameters": map[string]any{"type": "object"}}},
		"input": []any{
			map[string]any{
				"type":    "reasoning",
				"summary": []any{map[string]any{"type": "summary_text", "text": "Need to inspect the file."}},
			},
			map[string]any{"type": "function_call", "call_id": "call_1", "name": "shell", "arguments": `{"cmd":"dir"}`},
			map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "ok"},
		},
	}
	chat, _, err := ToChat(body)
	if err != nil {
		t.Fatal(err)
	}
	messages := jsonx.Slice(chat["messages"])
	if len(messages) != 2 {
		raw, _ := json.Marshal(messages)
		t.Fatalf("expected assistant tool call and tool output, got %s", raw)
	}
	assistant := jsonx.Map(messages[0])
	if assistant["role"] != "assistant" || assistant["reasoning_content"] != "Need to inspect the file." {
		t.Fatalf("reasoning was not replayed on the assistant tool call: %#v", assistant)
	}
}

func TestToChatAttachesTrailingReasoningToPreviousAssistant(t *testing.T) {
	body := map[string]any{
		"model": "cline-pass/qwen3.8-max",
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": "question"},
			map[string]any{"type": "message", "role": "assistant", "content": "answer"},
			map[string]any{"type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": "trailing thought"}}},
		},
	}
	chat, _, err := ToChat(body)
	if err != nil {
		t.Fatal(err)
	}
	messages := jsonx.Slice(chat["messages"])
	assistant := jsonx.Map(messages[1])
	if assistant["role"] != "assistant" || assistant["reasoning_content"] != "trailing thought" {
		t.Fatalf("trailing reasoning was not attached to the assistant reply: %#v", assistant)
	}
}

func TestToChatSkipsReasoningReplayForMoonshotModels(t *testing.T) {
	body := map[string]any{
		"model": "cline-pass/kimi-k3",
		"input": []any{
			map[string]any{"type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": "private thought"}}},
			map[string]any{"type": "message", "role": "assistant", "content": "answer"},
		},
	}
	chat, _, err := ToChatWithOptions(body, Options{
		ReplayReasoning: ShouldReplayReasoning(jsonx.String(body["model"])),
	})
	if err != nil {
		t.Fatal(err)
	}
	assistant := jsonx.Map(jsonx.Slice(chat["messages"])[0])
	if assistant["reasoning_content"] != nil {
		t.Fatalf("Moonshot reasoning must not be replayed: %#v", assistant)
	}
}

func TestToChatRestoresCompactionEnvelopeAndIgnoresAdditionalTools(t *testing.T) {
	body := map[string]any{
		"model": "cline-pass/qwen3.8-max",
		"input": []any{
			map[string]any{"type": "compaction", "encrypted_content": CompactionEnvelope("condensed history")},
			map[string]any{"type": "additional_tools", "role": "developer", "tools": []any{}},
			map[string]any{"type": "message", "role": "user", "content": "continue"},
		},
	}
	chat, _, err := ToChat(body)
	if err != nil {
		t.Fatal(err)
	}
	messages := jsonx.Slice(chat["messages"])
	if len(messages) != 2 {
		raw, _ := json.Marshal(messages)
		t.Fatalf("expected compaction summary and user message, got %s", raw)
	}
	if jsonx.Map(messages[0])["role"] != "system" || !strings.Contains(jsonx.String(jsonx.Map(messages[0])["content"]), "condensed history") {
		t.Fatalf("compaction summary was not restored: %#v", messages[0])
	}
	if jsonx.Map(messages[1])["role"] != "user" || jsonx.Map(messages[1])["content"] != "continue" {
		t.Fatalf("additional_tools should not become a user message: %#v", messages)
	}
}

func TestToChatDropsOrphanedToolChoice(t *testing.T) {
	body := map[string]any{
		"model":               "cline-pass/qwen3.8-max",
		"tools":               []any{},
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
		"input":               "search",
	}
	chat, _, err := ToChat(body)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := chat["tool_choice"]; found {
		t.Fatalf("tool_choice must be dropped when no Chat tools are forwarded: %#v", chat)
	}
	if _, found := chat["parallel_tool_calls"]; found {
		t.Fatalf("parallel_tool_calls must be dropped when no Chat tools are forwarded: %#v", chat)
	}
}

func chatFunctionByName(t *testing.T, chat map[string]any, name string) map[string]any {
	t.Helper()
	for _, raw := range jsonx.Slice(chat["tools"]) {
		function := jsonx.Map(jsonx.Map(raw)["function"])
		if jsonx.String(function["name"]) == name {
			return function
		}
	}
	raw, _ := json.Marshal(chat["tools"])
	t.Fatalf("missing Chat tool %q in %s", name, raw)
	return nil
}

func TestToChatPreservesClientToolSearchSchema(t *testing.T) {
	body := map[string]any{
		"model": "cline-pass/test",
		"tools": []any{map[string]any{
			"type":        "tool_search",
			"execution":   "client",
			"description": "Find the project-specific tools needed to continue the task.",
			"parameters": map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"goal": map[string]any{"type": "string"}},
				"required":             []any{"goal"},
				"additionalProperties": false,
			},
		}},
		"input": "find the shipping tool",
	}
	chat, _, err := ToChat(body)
	if err != nil {
		t.Fatal(err)
	}
	function := chatFunctionByName(t, chat, "tool_search")
	if jsonx.String(function["description"]) != "Find the project-specific tools needed to continue the task." {
		t.Fatalf("tool_search description was overwritten: %#v", function["description"])
	}
	if jsonx.Map(jsonx.Map(function["parameters"])["properties"])["goal"] == nil {
		t.Fatalf("client tool_search parameters were not forwarded: %#v", function["parameters"])
	}
}

func TestToChatCollectsAdditionalToolsAndSearchOutput(t *testing.T) {
	body := map[string]any{
		"model": "cline-pass/test",
		"input": []any{
			map[string]any{
				"type": "additional_tools", "role": "developer",
				"tools": []any{
					map[string]any{"type": "custom", "name": "exec"},
					map[string]any{
						"type": "tool_search", "execution": "client",
						"description": "Search MCP namespaces.\n- calendar",
						"parameters": map[string]any{
							"type":       "object",
							"properties": map[string]any{"query": map[string]any{"type": "string"}},
							"required":   []any{"query"},
						},
					},
				},
			},
			map[string]any{"type": "message", "role": "user", "content": "schedule a meeting"},
			map[string]any{
				"type": "tool_search_call", "call_id": "call_search", "execution": "client",
				"arguments": map[string]any{"query": "calendar"},
			},
			map[string]any{
				"type": "tool_search_output", "call_id": "call_search", "execution": "client", "status": "completed",
				"tools": []any{map[string]any{
					"type": "namespace", "name": "calendar", "description": "Plan events.",
					"tools": []any{map[string]any{
						"type": "function", "name": "create_event", "defer_loading": true,
						"parameters": map[string]any{"type": "object", "properties": map[string]any{"title": map[string]any{"type": "string"}}},
					}},
				}},
			},
		},
	}
	chat, context, err := ToChat(body)
	if err != nil {
		t.Fatal(err)
	}
	chatFunctionByName(t, chat, "exec")
	chatFunctionByName(t, chat, "tool_search")
	chatFunctionByName(t, chat, "calendar__create_event")
	if context.ResponseTools == nil || jsonx.String(jsonx.Map(context.ResponseTools[0])["name"]) != "exec" {
		t.Fatalf("Responses Lite tools were not echoed: %#v", context.ResponseTools)
	}
	foundSearchOutput := false
	for _, raw := range jsonx.Slice(chat["messages"]) {
		message := jsonx.Map(raw)
		if jsonx.String(message["role"]) != "tool" || jsonx.String(message["tool_call_id"]) != "call_search" {
			continue
		}
		foundSearchOutput = true
		if !strings.Contains(jsonx.String(message["content"]), "create_event") {
			t.Fatalf("tool_search_output tools were not forwarded as Chat tool content: %#v", message["content"])
		}
	}
	if !foundSearchOutput {
		raw, _ := json.Marshal(chat["messages"])
		t.Fatalf("missing tool_search_output Chat message: %s", raw)
	}
}

func TestToChatSkipsHostedToolSearchHistory(t *testing.T) {
	body := map[string]any{
		"model": "cline-pass/test",
		"tools": []any{
			map[string]any{"type": "tool_search"},
			map[string]any{
				"type": "namespace", "name": "crm",
				"tools": []any{map[string]any{"type": "function", "name": "list_open_orders", "parameters": map[string]any{"type": "object"}}},
			},
		},
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": "list orders"},
			map[string]any{
				"type": "tool_search_call", "execution": "server", "call_id": nil,
				"arguments": map[string]any{"paths": []any{"crm"}},
			},
			map[string]any{
				"type": "tool_search_output", "execution": "server", "call_id": nil,
				"tools": []any{map[string]any{"type": "function", "name": "list_open_orders"}},
			},
		},
	}
	chat, _, err := ToChat(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range jsonx.Slice(chat["messages"]) {
		message := jsonx.Map(raw)
		if jsonx.String(message["role"]) == "tool" || jsonx.Slice(message["tool_calls"]) != nil {
			rawMessages, _ := json.Marshal(chat["messages"])
			t.Fatalf("hosted tool_search items must not become Chat tool turns: %s", rawMessages)
		}
	}
}

func TestFromChatProducesResponsesToolCall(t *testing.T) {
	body := map[string]any{
		"model": "cline-pass/test",
		"tools": []any{map[string]any{"type": "custom", "name": "apply_patch"}},
		"input": "fix it",
	}
	_, context, err := ToChat(body)
	if err != nil {
		t.Fatal(err)
	}
	response, err := FromChat(map[string]any{
		"id": "chatcmpl-test", "model": "cline-pass/test", "created": json.Number("12"),
		"choices": []any{map[string]any{
			"finish_reason": "tool_calls",
			"message": map[string]any{
				"role":              "assistant",
				"reasoning_content": "plan",
				"tool_calls": []any{map[string]any{
					"id": "call_1", "type": "function",
					"function": map[string]any{"name": "apply_patch", "arguments": `{"input":"*** Begin Patch"}`},
				}},
			},
		}},
	}, context)
	if err != nil {
		t.Fatal(err)
	}
	output := jsonx.Slice(response["output"])
	if len(output) != 2 || jsonx.Map(output[0])["type"] != "reasoning" || jsonx.Map(output[1])["type"] != "custom_tool_call" {
		t.Fatalf("unexpected response output: %#v", output)
	}
	if jsonx.Map(output[1])["input"] != "*** Begin Patch" {
		t.Fatalf("custom tool input was not restored: %#v", output[1])
	}
}

func TestStreamAdapterAcceptsCleanDoneWithoutFinishReason(t *testing.T) {
	_, context, err := ToChat(map[string]any{"model": "cline-pass/test", "input": "hi", "stream": true})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewStreamAdapter(context)
	events := adapter.Feed([]byte(
		"data: {\"id\":\"chatcmpl-x\",\"model\":\"cline-pass/test\",\"choices\":[{\"delta\":{\"reasoning_content\":\"think\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":null}]}\n\n" +
			"data: [DONE]\n\n",
	))
	if !adapter.Completed() {
		t.Fatal("adapter should stop reading after [DONE]")
	}
	types := make([]string, 0, len(events))
	for _, value := range events {
		types = append(types, value.Type)
	}
	joined := strings.Join(types, ",")
	for _, expected := range []string{"response.created", "response.reasoning_summary_text.delta", "response.output_text.delta", "response.completed"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing %s in %s", expected, joined)
		}
	}
}

func TestStreamAdapterRestoresCustomToolCall(t *testing.T) {
	_, context, err := ToChat(map[string]any{
		"model": "cline-pass/test", "input": "fix", "stream": true,
		"tools": []any{map[string]any{"type": "custom", "name": "apply_patch"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewStreamAdapter(context)
	events := adapter.Feed([]byte(
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"apply_patch\",\"arguments\":\"{\\\"input\\\":\\\"*** Begin\"}}]},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\" Patch\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
			"data: [DONE]\n\n",
	))
	types := make([]string, 0, len(events))
	var customInput string
	for _, value := range events {
		types = append(types, value.Type)
		if value.Type == "response.custom_tool_call_input.done" {
			customInput = jsonx.String(value.Data["input"])
		}
	}
	joined := strings.Join(types, ",")
	for _, expected := range []string{"response.output_item.added", "response.custom_tool_call_input.done", "response.output_item.done", "response.completed"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing %s in %s", expected, joined)
		}
	}
	if customInput != "*** Begin Patch" {
		t.Fatalf("unexpected custom tool input: %q", customInput)
	}
}

func TestStreamAdapterRestoresToolSearchCall(t *testing.T) {
	_, context, err := ToChat(map[string]any{
		"model": "cline-pass/test", "input": "find calendar", "stream": true,
		"tools": []any{map[string]any{
			"type": "tool_search", "execution": "client",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{"query": map[string]any{"type": "string"}},
				"required":   []any{"query"},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewStreamAdapter(context)
	events := adapter.Feed([]byte(
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_search\",\"function\":{\"name\":\"tool_search\",\"arguments\":\"{\\\"query\\\":\\\"cal\"}}]},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"endar\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
			"data: [DONE]\n\n",
	))
	var item map[string]any
	var arguments string
	types := make([]string, 0, len(events))
	for _, value := range events {
		types = append(types, value.Type)
		if value.Type == "response.output_item.done" {
			item = jsonx.Map(value.Data["item"])
		}
		if value.Type == "response.function_call_arguments.done" {
			arguments = jsonx.String(value.Data["arguments"])
		}
	}
	joined := strings.Join(types, ",")
	for _, expected := range []string{"response.output_item.added", "response.function_call_arguments.delta", "response.function_call_arguments.done", "response.output_item.done", "response.completed"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing %s in %s", expected, joined)
		}
	}
	if jsonx.String(item["type"]) != "tool_search_call" || jsonx.String(item["execution"]) != "client" || jsonx.String(item["call_id"]) != "call_search" {
		t.Fatalf("unexpected tool_search_call item: %#v", item)
	}
	if jsonx.String(jsonx.Map(item["arguments"])["query"]) != "calendar" {
		t.Fatalf("tool_search arguments were not restored: %#v", item["arguments"])
	}
	if arguments != `{"query":"calendar"}` {
		t.Fatalf("tool_search argument done payload was %q", arguments)
	}
}

func TestStreamAdapterFailsBareEOFWithoutFinishReason(t *testing.T) {
	_, context, err := ToChat(map[string]any{"model": "cline-pass/test", "input": "hi", "stream": true})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewStreamAdapter(context)
	events := adapter.Feed([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n"))
	events = append(events, adapter.Finish(nil)...)
	last := events[len(events)-1]
	if last.Type != "response.failed" {
		t.Fatalf("bare EOF should fail instead of completing: %#v", events)
	}
	response := jsonx.Map(last.Data["response"])
	responseError := jsonx.Map(response["error"])
	if responseError["code"] != "stream_truncated" {
		t.Fatalf("unexpected terminal error: %#v", responseError)
	}
}

func TestStreamAdapterMarksContentFilterIncomplete(t *testing.T) {
	_, context, err := ToChat(map[string]any{"model": "cline-pass/test", "input": "hi", "stream": true})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewStreamAdapter(context)
	events := adapter.Feed([]byte(
		"data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"content_filter\"}]}\n\n" +
			"data: [DONE]\n\n",
	))
	last := events[len(events)-1]
	if last.Type != "response.incomplete" {
		t.Fatalf("content_filter should produce response.incomplete: %#v", events)
	}
	response := jsonx.Map(last.Data["response"])
	if jsonx.Map(response["incomplete_details"])["reason"] != "content_filter" {
		t.Fatalf("unexpected incomplete details: %#v", response)
	}
}

func TestStreamAdapterFailsUnknownFinishReason(t *testing.T) {
	_, context, err := ToChat(map[string]any{"model": "cline-pass/test", "input": "hi", "stream": true})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewStreamAdapter(context)
	events := adapter.Feed([]byte(
		"data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"surprise\"}]}\n\n" +
			"data: [DONE]\n\n",
	))
	last := events[len(events)-1]
	if last.Type != "response.failed" {
		t.Fatalf("unknown finish_reason should fail: %#v", events)
	}
	responseError := jsonx.Map(jsonx.Map(last.Data["response"])["error"])
	if responseError["code"] != "upstream_finish_reason_unknown" {
		t.Fatalf("unexpected terminal error: %#v", responseError)
	}
}

func TestStreamAdapterLocksResponseIdentityAfterFirstChunk(t *testing.T) {
	_, context, err := ToChat(map[string]any{"model": "cline-pass/test", "input": "hi", "stream": true})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewStreamAdapter(context)
	events := adapter.Feed([]byte(
		"data: {\"id\":\"chatcmpl-first\",\"model\":\"model-first\",\"choices\":[{\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"id\":\"chatcmpl-second\",\"model\":\"model-second\",\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n",
	))
	var completed map[string]any
	for _, value := range events {
		if value.Type == "response.completed" {
			completed = jsonx.Map(value.Data["response"])
		}
	}
	if completed == nil {
		t.Fatalf("missing response.completed: %#v", events)
	}
	if completed["id"] != "resp_first" || completed["model"] != "cline-pass/test" {
		t.Fatalf("response identity changed after the first chunk: %#v", completed)
	}
}

func TestStreamAdapterSplitsInlineThinkBlock(t *testing.T) {
	_, context, err := ToChat(map[string]any{"model": "cline-pass/minimax-m3", "input": "hi", "stream": true})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewStreamAdapter(context)
	events := adapter.Feed([]byte(
		"data: {\"choices\":[{\"delta\":{\"content\":\"<thi\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"nk>I should \"},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"think.</thi\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"nk>\\n\\nOK\"},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n",
	))
	var reasoning, visible string
	for _, value := range events {
		switch value.Type {
		case "response.reasoning_summary_text.delta":
			reasoning += jsonx.String(value.Data["delta"])
		case "response.output_text.delta":
			visible += jsonx.String(value.Data["delta"])
		}
	}
	if reasoning != "I should think." {
		t.Fatalf("unexpected inline reasoning: %q", reasoning)
	}
	if visible != "OK" {
		t.Fatalf("think tags leaked into visible output: %q", visible)
	}
}

func TestStreamAdapterReadsReasoningDetails(t *testing.T) {
	_, context, err := ToChat(map[string]any{"model": "cline-pass/glm-5.3", "input": "hi", "stream": true})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewStreamAdapter(context)
	events := adapter.Feed([]byte(
		"data: {\"choices\":[{\"delta\":{\"reasoning_details\":[{\"type\":\"reasoning_text\",\"text\":\"detailed thought\"}]},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n",
	))
	var reasoning string
	for _, value := range events {
		if value.Type == "response.reasoning_summary_text.delta" {
			reasoning += jsonx.String(value.Data["delta"])
		}
	}
	if reasoning != "detailed thought" {
		t.Fatalf("reasoning_details were not converted: %q", reasoning)
	}
}

func TestStreamAdapterDropsMalformedToolArguments(t *testing.T) {
	_, context, err := ToChat(map[string]any{
		"model": "cline-pass/test", "input": "fix", "stream": true,
		"tools": []any{map[string]any{"type": "function", "name": "shell", "parameters": map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"choices": []any{map[string]any{
		"delta": map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "id": "call_1",
			"function": map[string]any{"name": "shell", "arguments": `{"cmd":`},
		}}},
		"finish_reason": "tool_calls",
	}}}
	raw, _ := json.Marshal(payload)
	adapter := NewStreamAdapter(context)
	events := adapter.Feed([]byte("data: " + string(raw) + "\n\ndata: [DONE]\n\n"))
	last := events[len(events)-1]
	if last.Type != "response.failed" {
		t.Fatalf("malformed arguments should fail the turn: %#v", events)
	}
	responseError := jsonx.Map(jsonx.Map(last.Data["response"])["error"])
	if responseError["code"] != "upstream_tool_call_dropped" {
		t.Fatalf("unexpected terminal error: %#v", responseError)
	}
}

func TestStreamAdapterCanonicalizesToolArguments(t *testing.T) {
	_, context, err := ToChat(map[string]any{
		"model": "cline-pass/test", "input": "fix", "stream": true,
		"tools": []any{map[string]any{"type": "function", "name": "shell", "parameters": map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"choices": []any{map[string]any{
		"delta": map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "id": "call_1",
			"function": map[string]any{"name": "shell", "arguments": ` { "cmd" : "dir" } `},
		}}},
		"finish_reason": "tool_calls",
	}}}
	raw, _ := json.Marshal(payload)
	adapter := NewStreamAdapter(context)
	events := adapter.Feed([]byte("data: " + string(raw) + "\n\ndata: [DONE]\n\n"))
	var arguments string
	for _, value := range events {
		if value.Type == "response.function_call_arguments.done" {
			arguments = jsonx.String(value.Data["arguments"])
		}
	}
	if arguments != `{"cmd":"dir"}` {
		t.Fatalf("tool arguments were not canonicalized: %q", arguments)
	}
}

func TestToChatMergesCommentaryWithToolCalls(t *testing.T) {
	body := map[string]any{
		"model": "cline-pass/qwen3.8-max",
		"tools": []any{map[string]any{"type": "function", "name": "shell", "parameters": map[string]any{"type": "object"}}},
		"input": []any{
			map[string]any{"type": "message", "role": "assistant", "content": "I will inspect."},
			map[string]any{"type": "function_call", "call_id": "call_1", "name": "shell", "arguments": `{"cmd":"dir"}`},
			map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "ok"},
		},
	}
	chat, _, err := ToChat(body)
	if err != nil {
		t.Fatal(err)
	}
	messages := jsonx.Slice(chat["messages"])
	if len(messages) != 2 {
		raw, _ := json.Marshal(messages)
		t.Fatalf("commentary and tool calls should share one assistant message: %s", raw)
	}
	assistant := jsonx.Map(messages[0])
	if assistant["content"] != "I will inspect." || len(jsonx.Slice(assistant["tool_calls"])) != 1 {
		t.Fatalf("commentary was not merged with tool calls: %#v", assistant)
	}
}

func TestToChatRejectsIncompleteToolHistory(t *testing.T) {
	body := map[string]any{
		"model": "cline-pass/qwen3.8-max",
		"tools": []any{map[string]any{"type": "function", "name": "shell", "parameters": map[string]any{"type": "object"}}},
		"input": []any{
			map[string]any{"type": "function_call", "call_id": "call_1", "name": "shell", "arguments": `{"cmd":"dir"}`},
		},
	}
	_, _, err := ToChat(body)
	if err == nil || !strings.Contains(err.Error(), "missing outputs") {
		t.Fatalf("expected incomplete tool history error, got %v", err)
	}
}

func TestCustomInputFromArgumentsAcceptsJSONString(t *testing.T) {
	if got := customInputFromArguments(`"ls -la"`); got != "ls -la" {
		t.Fatalf("custom tool JSON string was not unwrapped: %q", got)
	}
}

func TestToChatMapsReasoningEffortToSupportedCapabilities(t *testing.T) {
	cases := []struct {
		effort    string
		supported []string
		want      string
	}{
		{effort: "xhigh", supported: []string{"low", "medium", "high"}, want: "high"},
		{effort: "minimal", supported: []string{"low", "medium", "high"}, want: "low"},
		{effort: "medium", supported: []string{"low", "medium", "high"}, want: "medium"},
		{effort: "medium", supported: []string{"none", "low", "high", "max"}, want: "high"},
		{effort: "xhigh", supported: []string{"none", "low", "high", "max"}, want: "max"},
		{effort: "weird", supported: []string{"low", "medium", "high"}, want: ""},
		{effort: "none", supported: []string{"low", "medium", "high"}, want: ""},
		{effort: "none", supported: []string{"none", "low", "high"}, want: "none"},
		{effort: "xhigh", supported: nil, want: "xhigh"},
	}
	for _, testCase := range cases {
		body := map[string]any{
			"model": "cline-pass/test", "input": "hi",
			"reasoning": map[string]any{"effort": testCase.effort},
		}
		chat, context, err := ToChatWithOptions(body, Options{
			ReplayReasoning:  true,
			ReasoningEfforts: testCase.supported,
		})
		if err != nil {
			t.Fatal(err)
		}
		if context.RequestedReasoningEffort != testCase.effort {
			t.Fatalf("requested effort: got %q want %q", context.RequestedReasoningEffort, testCase.effort)
		}
		got, found := chat["reasoning_effort"]
		if testCase.want == "" {
			if found {
				t.Fatalf("effort %q should be dropped when unsupported, got %v", testCase.effort, got)
			}
			if context.MappedReasoningEffort != "" {
				t.Fatalf("dropped effort should not be reported as mapped: %q", context.MappedReasoningEffort)
			}
			if testCase.effort == "none" {
				if _, included := chat["include_reasoning"]; included {
					t.Fatalf("disabled reasoning should not request reasoning tokens: %#v", chat)
				}
			} else if chat["include_reasoning"] != true {
				t.Fatalf("requested reasoning should still ask for tokens when effort %q is dropped", testCase.effort)
			}
			continue
		}
		if !found || got != testCase.want {
			t.Fatalf("effort %q with %v: got %v, want %q", testCase.effort, testCase.supported, got, testCase.want)
		}
		if context.MappedReasoningEffort != testCase.want {
			t.Fatalf("mapped effort was not exposed on context: %q", context.MappedReasoningEffort)
		}
		if testCase.want == "none" {
			if _, included := chat["include_reasoning"]; included {
				t.Fatalf("disabled reasoning should not request reasoning tokens: %#v", chat)
			}
		} else if chat["include_reasoning"] != true {
			t.Fatalf("expected include_reasoning for effort %q", testCase.effort)
		}
	}
}

func TestStreamAdapterPreservesUpstreamErrorType(t *testing.T) {
	_, context, err := ToChat(map[string]any{"model": "cline-pass/test", "input": "hi", "stream": true})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewStreamAdapter(context)
	events := adapter.Feed([]byte(
		"data: {\"error\":{\"message\":\"rate limited\",\"type\":\"rate_limit_error\",\"code\":\"rate_limit_exceeded\"}}\n\n",
	))
	last := events[len(events)-1]
	if last.Type != "response.failed" {
		t.Fatalf("expected response.failed, got %#v", events)
	}
	responseError := jsonx.Map(jsonx.Map(last.Data["response"])["error"])
	if responseError["type"] != "rate_limit_error" || responseError["code"] != "rate_limit_exceeded" {
		t.Fatalf("stream error details were not preserved: %#v", responseError)
	}
}

func TestShouldUseRawReasoning(t *testing.T) {
	if !ShouldUseRawReasoning("cline-pass/deepseek-v4.1-flash") {
		t.Fatal("DeepSeek should use raw reasoning")
	}
	if ShouldUseRawReasoning("cline-pass/qwen3.8-max") {
		t.Fatal("Qwen should keep the summary projection by default")
	}
}

func TestStreamAdapterEmitsRawReasoningForDeepSeek(t *testing.T) {
	_, context, err := ToChat(map[string]any{
		"model": "cline-pass/deepseek-v4.1-flash", "input": "hi", "stream": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !context.RawReasoning {
		t.Fatal("DeepSeek context should enable raw reasoning")
	}
	adapter := NewStreamAdapter(context)
	events := adapter.Feed([]byte(
		"data: {\"id\":\"chatcmpl-raw\",\"model\":\"deepseek\",\"choices\":[{\"delta\":{\"reasoning\":\"Need \"},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"reasoning\":\"think.\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n",
	))
	rawDeltas := 0
	summaryDeltas := 0
	var reasoningItem map[string]any
	for _, value := range events {
		switch value.Type {
		case "response.reasoning_text.delta":
			rawDeltas++
		case "response.reasoning_summary_text.delta":
			summaryDeltas++
		case "response.output_item.done":
			item := jsonx.Map(value.Data["item"])
			if jsonx.String(item["type"]) == "reasoning" {
				reasoningItem = item
			}
		}
	}
	if rawDeltas == 0 || summaryDeltas == 0 {
		t.Fatalf("DeepSeek should emit raw and summary reasoning deltas: raw=%d summary=%d", rawDeltas, summaryDeltas)
	}
	if reasoningItem == nil {
		t.Fatal("missing completed reasoning item")
	}
	content := jsonx.Slice(reasoningItem["content"])
	if len(content) != 1 || jsonx.String(jsonx.Map(content[0])["type"]) != "reasoning_text" {
		t.Fatalf("raw reasoning item shape is wrong: %#v", reasoningItem)
	}
	summary := jsonx.Slice(reasoningItem["summary"])
	if len(summary) != 1 || jsonx.String(jsonx.Map(summary[0])["text"]) != "Need think." {
		t.Fatalf("ChatGPT summary projection missing: %#v", reasoningItem)
	}
}

func TestStreamAdapterPreservesReasoningSpaces(t *testing.T) {
	_, context, err := ToChat(map[string]any{
		"model": "cline-pass/deepseek-v4.1-flash", "input": "hi", "stream": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewStreamAdapter(context)
	events := adapter.Feed([]byte(
		"data: {\"choices\":[{\"delta\":{\"reasoning\":\"We\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"reasoning\":\" need\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"reasoning\":\" answer\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n",
	))
	var joined string
	for _, value := range events {
		if value.Type == "response.reasoning_text.delta" {
			joined += jsonx.String(value.Data["delta"])
		}
	}
	if joined != "We need answer" {
		t.Fatalf("reasoning spaces were lost: %q", joined)
	}
}

func TestToChatReplaysFullReasoningWithoutTruncation(t *testing.T) {
	longReasoning := strings.Repeat("x", 70<<10)
	body := map[string]any{
		"model": "cline-pass/qwen3.8-max",
		"input": []any{
			map[string]any{
				"type":    "reasoning",
				"summary": []any{map[string]any{"type": "summary_text", "text": longReasoning}},
			},
			map[string]any{"type": "message", "role": "assistant", "content": "answer"},
		},
	}
	chat, _, err := ToChat(body)
	if err != nil {
		t.Fatal(err)
	}
	assistant := jsonx.Map(jsonx.Slice(chat["messages"])[0])
	if got := jsonx.String(assistant["reasoning_content"]); got != longReasoning {
		t.Fatalf("reasoning replay was truncated: got %d bytes, want %d", len(got), len(longReasoning))
	}
}

func TestStreamAdapterKeepsRequestedModel(t *testing.T) {
	_, context, err := ToChat(map[string]any{
		"model": "cline-pass/glm-5.3-flash", "input": "hi", "stream": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewStreamAdapter(context)
	events := adapter.Feed([]byte(
		"data: {\"id\":\"chatcmpl-x\",\"model\":\"glm-5.3-flash\",\"choices\":[{\"delta\":{\"reasoning\":\"plan\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n",
	))
	for _, value := range events {
		if value.Type == "response.created" {
			if jsonx.String(jsonx.Map(value.Data["response"])["model"]) != "cline-pass/glm-5.3-flash" {
				t.Fatalf("streamed model was overwritten: %#v", value.Data["response"])
			}
			return
		}
	}
	t.Fatal("missing response.created")
}

func TestFromChatReadsClineReasoningField(t *testing.T) {
	_, context, err := ToChat(map[string]any{"model": "cline-pass/qwen3.8-max", "input": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := FromChat(map[string]any{
		"id": "chatcmpl-or", "model": "qwen3.8-max", "created": json.Number("1"),
		"choices": []any{map[string]any{
			"finish_reason": "stop",
			"message": map[string]any{
				"role":      "assistant",
				"reasoning": "step by step",
				"content":   "OK",
			},
		}},
	}, context)
	if err != nil {
		t.Fatal(err)
	}
	if jsonx.String(response["model"]) != "cline-pass/qwen3.8-max" {
		t.Fatalf("requested model was not preserved: %#v", response["model"])
	}
	item := jsonx.Map(jsonx.Slice(response["output"])[0])
	if jsonx.String(item["type"]) != "reasoning" {
		t.Fatalf("cline reasoning field was not converted: %#v", response["output"])
	}
	summary := jsonx.Slice(item["summary"])
	if len(summary) != 1 || jsonx.String(jsonx.Map(summary[0])["text"]) != "step by step" {
		t.Fatalf("reasoning summary missing: %#v", item)
	}
}

func TestAliasChatReasoningCopiesOpenRouterField(t *testing.T) {
	chunk := map[string]any{
		"choices": []any{map[string]any{
			"delta": map[string]any{"reasoning": "think aloud"},
		}},
	}
	if !AliasChatReasoning(chunk) {
		t.Fatal("expected reasoning alias")
	}
	delta := jsonx.Map(jsonx.Map(jsonx.Slice(chunk["choices"])[0])["delta"])
	if jsonx.String(delta["reasoning_content"]) != "think aloud" {
		t.Fatalf("reasoning_content was not filled: %#v", delta)
	}
}

// collectStreamText joins reasoning and visible deltas and records which
// output_index each delta targeted after that item was already done.
func collectStreamText(events []Event) (reasoning, visible string, lateDeltas []Event) {
	done := map[any]bool{}
	for _, value := range events {
		switch value.Type {
		case "response.output_item.done":
			done[value.Data["output_index"]] = true
		case "response.reasoning_summary_text.delta":
			reasoning += jsonx.String(value.Data["delta"])
			if done[value.Data["output_index"]] {
				lateDeltas = append(lateDeltas, value)
			}
		case "response.output_text.delta":
			visible += jsonx.String(value.Data["delta"])
			if done[value.Data["output_index"]] {
				lateDeltas = append(lateDeltas, value)
			}
		}
	}
	return reasoning, visible, lateDeltas
}

func completedOutputTypes(t *testing.T, events []Event) []string {
	t.Helper()
	last := events[len(events)-1]
	if last.Type != "response.completed" {
		t.Fatalf("expected response.completed, got %s: %#v", last.Type, last.Data)
	}
	types := []string{}
	for _, raw := range jsonx.Slice(jsonx.Map(last.Data["response"])["output"]) {
		types = append(types, jsonx.String(jsonx.Map(raw)["type"]))
	}
	return types
}

func TestStreamAdapterReopensItemsAfterInterleavedThinking(t *testing.T) {
	_, context, err := ToChat(map[string]any{"model": "cline-pass/test", "input": "hi", "stream": true})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewStreamAdapter(context)
	events := adapter.Feed([]byte(
		"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"plan\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"Step one. \"},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"reconsider\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"Step two.\"},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n",
	))
	reasoning, visible, late := collectStreamText(events)
	if reasoning != "planreconsider" || visible != "Step one. Step two." {
		t.Fatalf("interleaved content was lost: reasoning=%q visible=%q", reasoning, visible)
	}
	if len(late) != 0 {
		t.Fatalf("deltas were emitted for already completed items: %#v", late)
	}
	types := completedOutputTypes(t, events)
	want := []string{"reasoning", "message", "reasoning", "message"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("output should follow wire order, got %v", types)
	}
	response := jsonx.Map(events[len(events)-1].Data["response"])
	second := jsonx.Map(jsonx.Slice(response["output"])[3])
	if textFromParts(second["content"]) != "Step two." {
		t.Fatalf("second message lost its text: %#v", second)
	}
}

func TestStreamAdapterKeepsTextAfterToolCall(t *testing.T) {
	_, context, err := ToChat(map[string]any{
		"model": "cline-pass/test", "input": "go", "stream": true,
		"tools": []any{map[string]any{"type": "function", "name": "shell", "parameters": map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewStreamAdapter(context)
	events := adapter.Feed([]byte(
		"data: {\"choices\":[{\"delta\":{\"content\":\"Running.\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"shell\",\"arguments\":\"{\\\"cmd\\\":\\\"ls\\\"}\"}}]},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"Done.\"},\"finish_reason\":\"tool_calls\"}]}\n\n" +
			"data: [DONE]\n\n",
	))
	_, visible, late := collectStreamText(events)
	if visible != "Running.Done." || len(late) != 0 {
		t.Fatalf("text after a tool call was mishandled: visible=%q late=%#v", visible, late)
	}
	types := completedOutputTypes(t, events)
	if strings.Join(types, ",") != "message,function_call,message" {
		t.Fatalf("unexpected output order: %v", types)
	}
}

func TestStreamAdapterSplitsMultipleInlineThinkBlocks(t *testing.T) {
	_, context, err := ToChat(map[string]any{"model": "cline-pass/minimax-m3", "input": "hi", "stream": true})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewStreamAdapter(context)
	events := adapter.Feed([]byte(
		"data: {\"choices\":[{\"delta\":{\"content\":\"<think>first</think>\\n\\nAlpha \"},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"<thi\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"nk>second</think>\\nBeta\"},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n",
	))
	reasoning, visible, late := collectStreamText(events)
	if reasoning != "firstsecond" {
		t.Fatalf("second think block was not captured: %q", reasoning)
	}
	if visible != "Alpha Beta" {
		t.Fatalf("think tags leaked into visible output: %q", visible)
	}
	if len(late) != 0 {
		t.Fatalf("deltas targeted completed items: %#v", late)
	}
	types := completedOutputTypes(t, events)
	if strings.Join(types, ",") != "reasoning,message,reasoning,message" {
		t.Fatalf("unexpected output order: %v", types)
	}
}

func TestStreamAdapterLeavesLiteralThinkTagWhenResponseDidNotOpenWithOne(t *testing.T) {
	_, context, err := ToChat(map[string]any{"model": "cline-pass/test", "input": "hi", "stream": true})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewStreamAdapter(context)
	events := adapter.Feed([]byte(
		"data: {\"choices\":[{\"delta\":{\"content\":\"Use the tag `<think>` in your prompt.\"},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n",
	))
	reasoning, visible, _ := collectStreamText(events)
	if reasoning != "" || visible != "Use the tag `<think>` in your prompt." {
		t.Fatalf("literal tag mid-text should stay visible: reasoning=%q visible=%q", reasoning, visible)
	}
}

func TestStreamAdapterFlushesHeldTailAtEnd(t *testing.T) {
	_, context, err := ToChat(map[string]any{"model": "cline-pass/minimax-m3", "input": "hi", "stream": true})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewStreamAdapter(context)
	events := adapter.Feed([]byte(
		"data: {\"choices\":[{\"delta\":{\"content\":\"<think>x</think>a < b <\"},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n",
	))
	_, visible, _ := collectStreamText(events)
	if visible != "a < b <" {
		t.Fatalf("held tail was dropped at end of stream: %q", visible)
	}
	completedOutputTypes(t, events)
}

func TestStreamAdapterPreservesWhitespaceReasoningDelta(t *testing.T) {
	_, context, err := ToChat(map[string]any{"model": "cline-pass/test", "input": "hi", "stream": true})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewStreamAdapter(context)
	events := adapter.Feed([]byte(
		"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"First.\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"\\n\\n\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Second.\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n",
	))
	reasoning, _, _ := collectStreamText(events)
	if reasoning != "First.\n\nSecond." {
		t.Fatalf("paragraph break inside reasoning was dropped: %q", reasoning)
	}
}

func TestFromChatSplitsInlineThinkBlock(t *testing.T) {
	_, context, err := ToChat(map[string]any{"model": "cline-pass/minimax-m3", "input": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := FromChat(map[string]any{
		"id": "chatcmpl-1", "choices": []any{map[string]any{
			"message":       map[string]any{"role": "assistant", "content": "<think>I should think.</think>\n\nOK"},
			"finish_reason": "stop",
		}},
	}, context)
	if err != nil {
		t.Fatal(err)
	}
	output := jsonx.Slice(response["output"])
	if len(output) != 2 || jsonx.Map(output[0])["type"] != "reasoning" || jsonx.Map(output[1])["type"] != "message" {
		t.Fatalf("non-stream path should split inline think like the stream path: %#v", output)
	}
	if textFromParts(jsonx.Map(output[1])["content"]) != "OK" {
		t.Fatalf("think tags leaked into the message: %#v", output[1])
	}
	summary := jsonx.Slice(jsonx.Map(output[0])["summary"])
	if len(summary) != 1 || jsonx.String(jsonx.Map(summary[0])["text"]) != "I should think." {
		t.Fatalf("reasoning summary missing: %#v", output[0])
	}
}

func TestFromChatMarksContentFilterIncomplete(t *testing.T) {
	_, context, err := ToChat(map[string]any{"model": "cline-pass/test", "input": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := FromChat(map[string]any{
		"choices": []any{map[string]any{
			"message":       map[string]any{"role": "assistant", "content": "partial"},
			"finish_reason": "content_filter",
		}},
	}, context)
	if err != nil {
		t.Fatal(err)
	}
	if response["status"] != "incomplete" || jsonx.Map(response["incomplete_details"])["reason"] != "content_filter" {
		t.Fatalf("content_filter should be reported as incomplete: %#v", response)
	}
}

func TestFromChatRejectsMalformedToolArguments(t *testing.T) {
	_, context, err := ToChat(map[string]any{
		"model": "cline-pass/test", "input": "fix",
		"tools": []any{map[string]any{"type": "function", "name": "shell", "parameters": map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = FromChat(map[string]any{
		"choices": []any{map[string]any{
			"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
				"id": "call_1", "type": "function",
				"function": map[string]any{"name": "shell", "arguments": "{\"cmd\":"},
			}}},
			"finish_reason": "tool_calls",
		}},
	}, context)
	var failure *ChatFailure
	if !errors.As(err, &failure) {
		t.Fatalf("expected ChatFailure, got %v", err)
	}
	if failure.Code != "upstream_tool_call_dropped" || failure.Response == nil {
		t.Fatalf("unexpected failure details: %#v", failure)
	}
}

func TestFromChatKeepsToolCallOrder(t *testing.T) {
	_, context, err := ToChat(map[string]any{
		"model": "cline-pass/test", "input": "go",
		"tools": []any{
			map[string]any{"type": "function", "name": "read", "parameters": map[string]any{"type": "object"}},
			map[string]any{"type": "function", "name": "write", "parameters": map[string]any{"type": "object"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := FromChat(map[string]any{
		"choices": []any{map[string]any{
			"message": map[string]any{"role": "assistant", "content": "Doing both.", "tool_calls": []any{
				map[string]any{"id": "call_a", "type": "function", "function": map[string]any{"name": "read", "arguments": "{}"}},
				map[string]any{"id": "call_b", "type": "function", "function": map[string]any{"name": "write", "arguments": "{}"}},
			}},
			"finish_reason": "tool_calls",
		}},
	}, context)
	if err != nil {
		t.Fatal(err)
	}
	output := jsonx.Slice(response["output"])
	if len(output) != 3 || jsonx.Map(output[0])["type"] != "message" {
		t.Fatalf("unexpected output: %#v", output)
	}
	if jsonx.Map(output[1])["call_id"] != "call_a" || jsonx.Map(output[2])["call_id"] != "call_b" {
		t.Fatalf("tool call order changed: %#v", output)
	}
}

func TestCompactionResponseRejectsReasoningOnlySummary(t *testing.T) {
	_, context, err := ToChat(map[string]any{"model": "cline-pass/test", "input": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := CompactionResponse(map[string]any{
		"choices": []any{map[string]any{
			"message":       map[string]any{"role": "assistant", "reasoning_content": "summary lives here", "content": ""},
			"finish_reason": "stop",
		}},
	}, context)
	if err == nil || response != nil {
		t.Fatalf("thinking alone must not replace conversation history: %#v, %v", response, err)
	}
}

func TestChatCompletionAsChunkIndexesToolCalls(t *testing.T) {
	chunk := ChatCompletionAsChunk(map[string]any{
		"id": "chatcmpl-1", "object": "chat.completion", "provider": "vendor",
		"usage": map[string]any{"prompt_tokens": 1},
		"choices": []any{map[string]any{
			"index": 0,
			"message": map[string]any{"role": "assistant", "content": "hi", "tool_calls": []any{
				map[string]any{"id": "call_a", "type": "function", "function": map[string]any{"name": "read", "arguments": "{}"}},
			}},
			"finish_reason": "tool_calls",
		}},
	})
	if chunk["object"] != "chat.completion.chunk" || chunk["provider"] != "vendor" || chunk["usage"] == nil {
		t.Fatalf("top-level fields were not carried over: %#v", chunk)
	}
	choice := jsonx.Map(jsonx.Slice(chunk["choices"])[0])
	delta := jsonx.Map(choice["delta"])
	if choice["finish_reason"] != "tool_calls" || jsonx.String(delta["content"]) != "hi" {
		t.Fatalf("choice was not reshaped into a delta: %#v", choice)
	}
	call := jsonx.Map(jsonx.Slice(delta["tool_calls"])[0])
	if call["index"] != 0 {
		t.Fatalf("streaming tool calls need an index: %#v", call)
	}
}

func TestToChatDemotesMidHistoryDeveloperMessages(t *testing.T) {
	chat, _, err := ToChat(map[string]any{
		"model": "cline-pass/test", "instructions": "You are Codex.",
		"input": []any{
			map[string]any{"type": "message", "role": "developer", "content": "<permissions_instructions>sandboxed</permissions_instructions>"},
			map[string]any{"type": "message", "role": "user", "content": "hello"},
			map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "hi"}}},
			map[string]any{"type": "message", "role": "developer", "content": "<environment_context>cwd changed</environment_context>"},
			map[string]any{"type": "message", "role": "user", "content": "continue"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	roles := []string{}
	for _, raw := range jsonx.Slice(chat["messages"]) {
		roles = append(roles, jsonx.String(jsonx.Map(raw)["role"]))
	}
	if strings.Join(roles, ",") != "system,system,user,assistant,user,user" {
		t.Fatalf("leading developer stays system, later ones become user: %v", roles)
	}
	demoted := jsonx.Map(jsonx.Slice(chat["messages"])[4])
	if !strings.Contains(textFromParts(demoted["content"]), "environment_context") {
		t.Fatalf("demoted notice lost its content: %#v", demoted)
	}
}

func TestToChatRejectsPreviousResponseID(t *testing.T) {
	_, _, err := ToChat(map[string]any{
		"model": "cline-pass/test", "input": "next turn", "previous_response_id": "resp_123",
	})
	if err == nil || !strings.Contains(err.Error(), "previous_response_id") {
		t.Fatalf("chaining on server-side state must be refused, got %v", err)
	}
}

func TestToChatForwardsPromptCacheKeyAndPadsEmptyToolOutput(t *testing.T) {
	chat, _, err := ToChat(map[string]any{
		"model": "cline-pass/test", "prompt_cache_key": "session-42",
		"tools": []any{map[string]any{"type": "function", "name": "shell", "parameters": map[string]any{"type": "object"}}},
		"input": []any{
			map[string]any{"type": "function_call", "call_id": "call_1", "name": "shell", "arguments": "{}"},
			map[string]any{"type": "function_call_output", "call_id": "call_1", "output": ""},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if chat["prompt_cache_key"] != "session-42" {
		t.Fatalf("prompt_cache_key should reach the Chat body: %#v", chat)
	}
	messages := jsonx.Slice(chat["messages"])
	tool := jsonx.Map(messages[len(messages)-1])
	if tool["role"] != "tool" || tool["content"] != toolEmptyOutputPlaceholder {
		t.Fatalf("empty tool output should be padded: %#v", tool)
	}
}

func TestCompactionTriggerIsAcceptedAndNeverBecomesAMessage(t *testing.T) {
	body := map[string]any{
		"model": "cline-pass/test",
		"input": []any{
			map[string]any{
				"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "input_text", "text": "hello"}},
			},
			map[string]any{"type": "compaction_trigger"},
		},
	}
	if !RequestTriggersCompaction(body) {
		t.Fatal("compaction trigger was not detected")
	}
	chat, _, err := ToChatWithOptions(body, Options{})
	if err != nil {
		t.Fatalf("remote compaction v2 request must be accepted: %v", err)
	}
	raw, err := json.Marshal(chat["messages"])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "compaction_trigger") {
		t.Fatalf("compaction marker leaked into the chat messages: %s", raw)
	}
	if !strings.Contains(string(raw), "hello") {
		t.Fatalf("user history must still be forwarded: %s", raw)
	}
}

func TestCompactionTriggerResponseHoldsOneCompactionItem(t *testing.T) {
	chat := map[string]any{
		"id": "chatcmpl-1", "created": 123,
		"choices": []any{map[string]any{
			"index": 0, "finish_reason": "stop",
			"message": map[string]any{"role": "assistant", "content": "condensed history"},
		}},
		"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
	}
	_, context, err := ToChat(map[string]any{"model": "cline-pass/test", "input": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := CompactionTriggerResponse(chat, context)
	if err != nil {
		t.Fatal(err)
	}
	output := jsonx.Slice(response["output"])
	if response["object"] != "response" || response["status"] != "completed" || len(output) != 1 {
		t.Fatalf("remote compaction v2 must return one normal response with a single item: %#v", response)
	}
	item := jsonx.Map(output[0])
	if item["type"] != "compaction" || !strings.HasPrefix(jsonx.String(item["encrypted_content"]), "ocx1:") {
		t.Fatalf("unexpected compaction item: %#v", item)
	}
	types := []string{}
	for _, value := range CompactionTriggerEvents(response, context) {
		types = append(types, value.Type)
	}
	want := "response.created,response.in_progress,response.output_item.added,response.output_item.done,response.completed"
	if strings.Join(types, ",") != want {
		t.Fatalf("unexpected remote compaction lifecycle: %v", types)
	}
}

func TestCompactionInstructionsAnchorTheFourSections(t *testing.T) {
	for _, heading := range []string{"## Objective", "## Work State", "## Next Move", "## Relevant Files"} {
		if !strings.Contains(compactionInstructions, heading) {
			t.Fatalf("compaction template lost %q:\n%s", heading, compactionInstructions)
		}
	}
}

func TestCompactionEnvelopeKeepsRecentAndReadsLegacyText(t *testing.T) {
	legacy := "ocx1:" + base64.StdEncoding.EncodeToString([]byte("plain summary"))
	payload, ok := DecodeCompactionEnvelope(legacy)
	if !ok || payload.Summary != "plain summary" || len(payload.Recent) != 0 {
		t.Fatalf("legacy envelope must keep decoding as a plain summary: %#v", payload)
	}

	encoded := encodeCompactionPayload(CompactionPayload{
		Summary: "## Objective\nfix the parser",
		Recent: []CompactionRecentMessage{
			{Role: "user", Text: "first request"},
			{Role: "assistant", Text: "first answer"},
		},
	})
	if !strings.HasPrefix(encoded, "ocx1:") {
		t.Fatalf("envelope prefix changed: %q", encoded)
	}
	payload, ok = DecodeCompactionEnvelope(encoded)
	if !ok || payload.Summary != "## Objective\nfix the parser" || len(payload.Recent) != 2 || payload.Recent[1].Text != "first answer" {
		t.Fatalf("structured envelope did not round-trip: %#v", payload)
	}
}

func TestCompactionTailHonoursBudgetAndTurnBoundary(t *testing.T) {
	messages := []any{
		map[string]any{"role": "user", "content": "old question"},
		map[string]any{"role": "assistant", "content": "old answer"},
		map[string]any{"role": "tool", "tool_call_id": "call_1", "content": strings.Repeat("x", 4000)},
		map[string]any{"role": "user", "content": "recent question"},
		map[string]any{"role": "assistant", "content": "recent answer"},
	}
	tail := selectCompactionTail(messages, 8)
	if len(tail) == 0 || tail[0].Role != "user" {
		t.Fatalf("a tail must start at a user turn: %#v", tail)
	}
	raw, err := json.Marshal(tail)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "old answer") || strings.Contains(string(raw), "xxxx") {
		t.Fatalf("tail ignored its budget or kept tool output: %s", raw)
	}
	full := selectCompactionTail(messages, 8000)
	if len(full) != 4 {
		t.Fatalf("a generous budget should keep every user/assistant message: %#v", full)
	}
	if got := selectCompactionTail(messages, 0); got != nil {
		t.Fatalf("zero disables the verbatim tail: %#v", got)
	}
}

func TestCompactionChatSummarizesOnlyTheOlderPart(t *testing.T) {
	body := map[string]any{
		"model": "cline-pass/test",
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "OLD-HISTORY-MARKER"}}},
			map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "ack"}}},
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "RECENT-MARKER"}}},
			map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "done"}}},
		},
	}
	chat, context, err := ToCompactionChatWithOptions(body, Options{RecentCompactionTokens: 8})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(chat["messages"])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "OLD-HISTORY-MARKER") {
		t.Fatalf("older history must still reach the summarizer: %s", raw)
	}
	if strings.Contains(string(raw), "RECENT-MARKER") {
		t.Fatalf("the verbatim tail must not be summarized again: %s", raw)
	}
	if len(context.compactionRecent) == 0 || context.compactionRecent[0].Role != "user" || context.compactionRecent[0].Text != "RECENT-MARKER" {
		t.Fatalf("verbatim tail was not captured: %#v", context.compactionRecent)
	}
}

func TestDegradedCompactionKeepsShapeAndRecentRequests(t *testing.T) {
	body := map[string]any{
		"model": "cline-pass/test",
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "old request"}}},
			map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "working on it"}}},
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "finish internal/parse.go\nand rerun the tests"}}},
			map[string]any{"type": "compaction_trigger"},
		},
	}
	_, context, err := ToCompactionChatWithOptions(body, Options{})
	if err != nil {
		t.Fatal(err)
	}
	response := DegradedCompactionTriggerResponse(context, "upstream 503: gateway unavailable", "partial handoff text")
	output := jsonx.Slice(response["output"])
	if response["object"] != "response" || len(output) != 1 {
		t.Fatalf("degraded v2 reply must stay one normal response with one item: %#v", response)
	}
	summary, ok := compactionSummaryFromEnvelope(jsonx.String(jsonx.Map(output[0])["encrypted_content"]))
	if !ok {
		t.Fatal("degraded item must still use the ocx1 envelope")
	}
	for _, expected := range []string{
		"compaction degraded",
		"gateway unavailable",
		"partial handoff text",
		"## Objective",
		"## Work State",
		"## Next Move",
		"## Relevant Files",
		"finish internal/parse.go and rerun the tests",
	} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("degraded summary is missing %q:\n%s", expected, summary)
		}
	}
	// The degraded path must not keep the assistant turn: only user requests
	// survive verbatim.
	if strings.Contains(summary, "working on it") {
		t.Fatalf("degraded summary should only preserve user requests:\n%s", summary)
	}
}

func TestToCompactionChatResolvesTheReasoningEffort(t *testing.T) {
	body := map[string]any{
		"model": "cline-pass/test",
		"input": []any{map[string]any{
			"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": "hello"}},
		}},
		"reasoning": map[string]any{"effort": "max"},
	}
	for _, tc := range []struct {
		efforts    []string
		configured string
		want       string
	}{
		// The default runs compaction at the strongest advertised level.
		{efforts: []string{"low", "high", "max"}, want: "max"},
		{efforts: []string{"low", "high"}, want: "high"},
		{efforts: []string{"none", "max"}, want: "max"},
		{efforts: []string{"none"}, want: "none"},
		// An explicit configuration wins when the model advertises it.
		{efforts: []string{"low", "high", "max"}, configured: "high", want: "high"},
		{efforts: []string{"none", "high", "max"}, configured: "low", want: "max"},
		{efforts: []string{"none", "max"}, configured: "auto", want: "max"},
		// No advertised levels: keep whatever the client asked for.
		{efforts: nil, want: "max"},
	} {
		chat, _, err := ToCompactionChatWithOptions(body, Options{
			ReasoningEfforts:          tc.efforts,
			CompactionReasoningEffort: tc.configured,
		})
		if err != nil {
			t.Fatal(err)
		}
		if chat["reasoning_effort"] != tc.want {
			t.Fatalf("efforts %v configured %q: compaction should run at %q, got %#v",
				tc.efforts, tc.configured, tc.want, chat["reasoning_effort"])
		}
		if tc.want == "none" {
			if chat["reasoning"] != nil {
				t.Fatalf("a no-reasoning model must not carry a reasoning map: %#v", chat)
			}
			continue
		}
		if effort := jsonx.String(jsonx.Map(chat["reasoning"])["effort"]); effort != tc.want {
			t.Fatalf("reasoning map must follow the capped effort: %#v", chat["reasoning"])
		}
	}
}

func TestCompactionEventsOpenTheLifecycle(t *testing.T) {
	compaction := map[string]any{
		"id": "resp_c", "object": "response.compaction",
		"output": []any{map[string]any{"type": "compaction", "encrypted_content": "ocx1:AA=="}},
		"usage":  map[string]any{"input_tokens": 1},
	}
	context := &Context{Model: "test"}
	types := []string{}
	for _, value := range CompactionEvents(compaction, context) {
		types = append(types, value.Type)
	}
	want := "response.created,response.in_progress,response.output_item.added,response.output_item.done,response.completed"
	if strings.Join(types, ",") != want {
		t.Fatalf("unexpected compaction lifecycle: %v", types)
	}
	created := jsonx.Map(CompactionEvents(compaction, context)[0].Data["response"])
	if created["status"] != "in_progress" || len(jsonx.Slice(created["output"])) != 0 || created["usage"] != nil {
		t.Fatalf("response.created must describe an in-progress response: %#v", created)
	}
	if compaction["object"] != "response.compaction" || compaction["status"] != nil {
		t.Fatalf("the final object must not be mutated: %#v", compaction)
	}
}

func TestEventWriterNumbersEventsPerConnection(t *testing.T) {
	shared := Event{Type: "response.created", Data: map[string]any{"type": "response.created"}}
	var first, second strings.Builder
	writerA := NewEventWriter(&first)
	writerB := NewEventWriter(&second)
	for range 2 {
		if err := writerA.Write(shared); err != nil {
			t.Fatal(err)
		}
	}
	if err := writerB.Write(shared); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.String(), "\"sequence_number\":0") || !strings.Contains(first.String(), "\"sequence_number\":1") {
		t.Fatalf("events should be numbered from 0 per connection: %s", first.String())
	}
	if !strings.Contains(second.String(), "\"sequence_number\":0") {
		t.Fatalf("a second connection restarts numbering: %s", second.String())
	}
	if _, mutated := shared.Data["sequence_number"]; mutated {
		t.Fatal("shared event payload must not be mutated")
	}
}

func TestToChatLeavesReasoningUntouchedWithoutRequest(t *testing.T) {
	chat, context, err := ToChatWithOptions(map[string]any{
		"model": "cline-pass/deepseek-v4.1-flash", "input": "hi",
	}, Options{ReasoningEfforts: []string{"low", "high", "max"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, found := chat["reasoning_effort"]; found {
		t.Fatalf("no requested effort should leave reasoning untouched: %#v", chat)
	}
	if _, found := chat["include_reasoning"]; found {
		t.Fatalf("include_reasoning should not be forced without reasoning config: %#v", chat)
	}
	if context.RequestedReasoningEffort != "" || context.MappedReasoningEffort != "" {
		t.Fatalf("no effort should be recorded: requested=%q mapped=%q", context.RequestedReasoningEffort, context.MappedReasoningEffort)
	}
}

func TestToChatToleratesOrphanToolOutputs(t *testing.T) {
	// ChatGPT Desktop hands a delegated task to the child thread as a
	// function_call_output whose call item stays in the parent thread.
	delegation := map[string]any{
		"model": "cline-pass/qwen3.8-max",
		"input": []any{
			map[string]any{
				"type":      "function_call_output",
				"call_id":   "fco_1",
				"name":      "create_thread",
				"namespace": "codex_app",
				"output":    "<codex_delegation><source_thread_id>abc</source_thread_id><input>do the thing &amp; report</input></codex_delegation>",
			},
		},
	}
	chat, _, err := ToChat(delegation)
	if err != nil {
		t.Fatalf("delegation payload must not fail the request: %v", err)
	}
	messages := jsonx.Slice(chat["messages"])
	if len(messages) != 1 {
		t.Fatalf("expected a single message, got %#v", messages)
	}
	message := jsonx.Map(messages[0])
	if jsonx.String(message["role"]) != "user" || jsonx.String(message["content"]) != "do the thing & report" {
		t.Fatalf("delegation payload was not replayed as the prompt: %#v", message)
	}

	// Results of host-side tools have no call at all; keep the content and mark
	// it so the model does not read it as a user turn.
	hostTool := map[string]any{
		"model": "cline-pass/qwen3.8-max",
		"input": []any{
			map[string]any{"type": "function_call_output", "call_id": "call_x", "name": "shell", "output": "exit code 1"},
		},
	}
	chat, _, err = ToChat(hostTool)
	if err != nil {
		t.Fatalf("orphan tool output must not fail the request: %v", err)
	}
	content := jsonx.String(jsonx.Map(jsonx.Slice(chat["messages"])[0])["content"])
	if !strings.Contains(content, "exit code 1") || !strings.Contains(content, "shell") {
		t.Fatalf("orphan content or marker lost: %q", content)
	}
}

func TestToChatStrictToolHistoryStillRejectsOrphans(t *testing.T) {
	body := map[string]any{
		"model": "cline-pass/qwen3.8-max",
		"input": []any{
			map[string]any{"type": "function_call_output", "call_id": "orphan", "output": "ok"},
		},
	}
	_, _, err := ToChatWithOptions(body, Options{StrictToolHistory: true})
	if err == nil || !strings.Contains(err.Error(), "orphan tool output") {
		t.Fatalf("expected the orphan error in strict mode, got %v", err)
	}
}

func TestOrphanToolOutputDoesNotSplitToolGroup(t *testing.T) {
	body := map[string]any{
		"model": "cline-pass/qwen3.8-max",
		"tools": []any{map[string]any{"type": "function", "name": "shell", "parameters": map[string]any{"type": "object"}}},
		"input": []any{
			map[string]any{"type": "function_call", "call_id": "call_a", "name": "shell", "arguments": "{\"cmd\":\"a\"}"},
			map[string]any{"type": "function_call", "call_id": "call_b", "name": "shell", "arguments": "{\"cmd\":\"b\"}"},
			map[string]any{"type": "function_call_output", "call_id": "call_a", "output": "a done"},
			map[string]any{"type": "function_call_output", "call_id": "call_orphan", "name": "shell", "output": "orphan"},
			map[string]any{"type": "function_call_output", "call_id": "call_b", "output": "b done"},
		},
	}
	chat, _, err := ToChat(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	roles := make([]string, 0, 4)
	for _, raw := range jsonx.Slice(chat["messages"]) {
		roles = append(roles, jsonx.String(jsonx.Map(raw)["role"]))
	}
	if got := strings.Join(roles, ","); got != "assistant,tool,tool,user" {
		t.Fatalf("orphan output split the tool group: %s (%#v)", got, chat["messages"])
	}
}

func TestWebSearchMapsToGatewayProviderTool(t *testing.T) {
	body := map[string]any{
		"model": "cline-pass/glm-5.3-flash",
		"input": "今天有什么新闻",
		"tools": []any{map[string]any{"type": "web_search"}},
	}
	// Off by default: the hosted declaration stays unsupported.
	chat, _, err := ToChat(body)
	if err != nil {
		t.Fatal(err)
	}
	if jsonx.Slice(chat["tools"]) != nil {
		t.Fatalf("web_search must stay unmapped by default: %#v", chat["tools"])
	}

	chat, _, err = ToChatWithOptions(body, Options{WebSearchUpstream: "exa"})
	if err != nil {
		t.Fatal(err)
	}
	tools := jsonx.Slice(chat["tools"])
	if len(tools) != 1 || jsonx.String(jsonx.Map(tools[0])["type"]) != "vercel:exa_search" {
		t.Fatalf("web_search was not mapped to the gateway tool: %#v", tools)
	}

	// The preview alias maps too, and the provider is configurable.
	preview := map[string]any{
		"model": "cline-pass/glm-5.3-flash", "input": "hi",
		"tools": []any{map[string]any{"type": "web_search_preview"}},
	}
	chat, _, err = ToChatWithOptions(preview, Options{WebSearchUpstream: "perplexity"})
	if err != nil {
		t.Fatal(err)
	}
	tools = jsonx.Slice(chat["tools"])
	if len(tools) != 1 || jsonx.String(jsonx.Map(tools[0])["type"]) != "vercel:perplexity_search" {
		t.Fatalf("web_search_preview was not mapped: %#v", tools)
	}

	// Other hosted tools keep being dropped.
	fileSearch := map[string]any{
		"model": "cline-pass/glm-5.3-flash", "input": "hi",
		"tools": []any{map[string]any{"type": "file_search"}},
	}
	chat, _, err = ToChatWithOptions(fileSearch, Options{WebSearchUpstream: "exa"})
	if err != nil {
		t.Fatal(err)
	}
	if jsonx.Slice(chat["tools"]) != nil {
		t.Fatalf("file_search must stay unmapped: %#v", chat["tools"])
	}
}

func TestWebSearchPolicyIsInjectedOnlyWhenDeclared(t *testing.T) {
	body := map[string]any{
		"model": "cline-pass/deepseek-v4.1-flash", "input": "hi",
		"instructions": "base instructions",
		"tools":        []any{map[string]any{"type": "web_search"}},
	}
	chat, _, err := ToChatWithOptions(body, Options{WebSearchUpstream: "exa"})
	if err != nil {
		t.Fatal(err)
	}
	messages := jsonx.Slice(chat["messages"])
	first := jsonx.Map(messages[0])
	content := jsonx.String(first["content"])
	if jsonx.String(first["role"]) != "system" ||
		!strings.Contains(content, "base instructions") ||
		!strings.Contains(content, "Web search policy") ||
		!strings.Contains(content, "at most one web search call") ||
		!strings.Contains(content, "at most 3 results") ||
		!strings.Contains(content, "When invoking tools") {
		t.Fatalf("web search policy was not injected: %#v", first)
	}

	plain := map[string]any{
		"model": "cline-pass/deepseek-v4.1-flash", "input": "hi",
		"instructions": "base instructions",
	}
	chat, _, err = ToChatWithOptions(plain, Options{WebSearchUpstream: "exa"})
	if err != nil {
		t.Fatal(err)
	}
	if content = jsonx.String(jsonx.Map(jsonx.Slice(chat["messages"])[0])["content"]); strings.Contains(content, "Web search policy") {
		t.Fatalf("policy leaked into a request without web_search: %#v", content)
	}

	chat, _, err = ToChatWithOptions(body, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if content = jsonx.String(jsonx.Map(jsonx.Slice(chat["messages"])[0])["content"]); strings.Contains(content, "Web search policy") {
		t.Fatalf("policy leaked while the provider tool was disabled: %#v", content)
	}
}

func TestProviderToolCallNeverReachesTheClient(t *testing.T) {
	_, context, err := ToChatWithOptions(map[string]any{
		"model": "cline-pass/glm-5.3-flash", "input": "search", "stream": true,
		"tools": []any{map[string]any{"type": "web_search"}},
	}, Options{WebSearchUpstream: "exa"})
	if err != nil {
		t.Fatal(err)
	}
	if !context.isProviderTool("vercel:exa_search") || !context.isProviderTool("exa_search") {
		t.Fatal("gateway provider tool names were not tracked")
	}
	state := NewStreamState(context)
	events := state.HandleChunk(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{
		"tool_calls": []any{map[string]any{
			"index": 0, "id": "call_1",
			"function": map[string]any{"name": "vercel:exa_search", "arguments": "{}"},
		}},
	}}}})
	for _, event := range events {
		item := jsonx.Map(event.Data["item"])
		if item != nil && jsonx.String(item["type"]) == "function_call" {
			t.Fatalf("provider tool call leaked to the client: %#v", item)
		}
	}
}

func TestLinkFetchDeclaredOnlyWhenUserSendsURL(t *testing.T) {
	options := Options{WebFetchUpstream: "browserbase_fetch"}
	withURL := map[string]any{
		"model": "cline-pass/deepseek-v4.1-flash",
		"input": []any{map[string]any{
			"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": "帮我看看 https://example.com/a?b=1 这个页面"}},
		}},
	}
	chat, _, err := ToChatWithOptions(withURL, options)
	if err != nil {
		t.Fatal(err)
	}
	tools := jsonx.Slice(chat["tools"])
	if len(tools) != 1 || jsonx.String(jsonx.Map(tools[0])["type"]) != "vercel:browserbase_fetch" {
		t.Fatalf("fetch tool was not declared for a user link: %#v", tools)
	}

	// A plain string input counts too.
	plain := map[string]any{"model": "cline-pass/deepseek-v4.1-flash", "input": "读一下 https://example.com"}
	chat, _, err = ToChatWithOptions(plain, options)
	if err != nil {
		t.Fatal(err)
	}
	if tools = jsonx.Slice(chat["tools"]); len(tools) != 1 {
		t.Fatalf("string input with a link did not declare the fetch tool: %#v", tools)
	}

	// No link: nothing declared.
	withoutURL := map[string]any{"model": "cline-pass/deepseek-v4.1-flash", "input": "你好"}
	chat, _, err = ToChatWithOptions(withoutURL, options)
	if err != nil {
		t.Fatal(err)
	}
	if tools = jsonx.Slice(chat["tools"]); tools != nil {
		t.Fatalf("fetch tool must stay undeclared without a link: %#v", tools)
	}

	// Disabled in configuration.
	chat, _, err = ToChatWithOptions(withURL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if tools = jsonx.Slice(chat["tools"]); tools != nil {
		t.Fatalf("fetch tool must stay off unless configured: %#v", tools)
	}

	// A link inside a tool result is not user text.
	fromTool := map[string]any{
		"model": "cline-pass/deepseek-v4.1-flash",
		"input": []any{
			map[string]any{"type": "function_call", "call_id": "call_1", "name": "shell", "arguments": "{}"},
			map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "https://example.com"},
		},
	}
	chat, _, err = ToChatWithOptions(fromTool, options)
	if err != nil {
		t.Fatal(err)
	}
	if tools = jsonx.Slice(chat["tools"]); tools != nil {
		t.Fatalf("tool output links must not declare the fetch tool: %#v", tools)
	}
}
