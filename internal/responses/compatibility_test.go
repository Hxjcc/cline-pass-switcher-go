package responses

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
)

func TestToChatPreservesOpaqueToolOutput(t *testing.T) {
	for _, original := range []string{
		`{"content":"partial output","exit_code":1,"next_cursor":"page2"}`,
		`{"output":{"text":"failed"},"isError":true}`,
		`{"text":"result","count":9007199254740993}`,
		`[1,2,{"content":"nested"}]`,
		"  {\n  \"content\": \"indented\"\n}\n",
		"    indented source code\n\n",
		`{"content":[{"type":"text","text":"hello"}],"isError":true}`,
	} {
		t.Run(original, func(t *testing.T) {
			chat, _, err := ToChat(map[string]any{"model": "test", "input": []any{
				map[string]any{"type": "function_call", "name": "exec", "call_id": "call1", "arguments": "{}"},
				map[string]any{"type": "function_call_output", "call_id": "call1", "output": original},
			}})
			if err != nil {
				t.Fatal(err)
			}
			got := jsonx.Map(jsonx.Slice(chat["messages"])[1])["content"]
			if got != original {
				t.Fatalf("tool output changed: want %q, got %q", original, got)
			}
		})
	}
}

func TestToolOutputPreservesStructuredBusinessData(t *testing.T) {
	for _, value := range []any{
		map[string]any{"content": "result", "exit_code": 1, "next_cursor": "page2"},
		map[string]any{"output": []any{1, 2}, "text": "important"},
		[]any{map[string]any{"content": "one"}, map[string]any{"content": "two"}},
		[]any{},
	} {
		parts := parseToolOutput(value)
		expected, _ := json.Marshal(value)
		if len(parts.Images) != 0 || strings.Join(parts.Text, "\n") != string(expected) {
			t.Fatalf("structured data changed: want %s, got %#v", expected, parts)
		}
	}
}

func TestToolMediaRetainsWrapperMetadata(t *testing.T) {
	encoded := `{"content":[{"type":"text","text":" image loaded\n"},{"type":"image","data":"QUJD","mimeType":"image/png"}],"isError":true,"next_cursor":"page2","count":9007199254740993}`
	var structured any
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&structured); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{encoded, structured} {
		parts := parseToolOutput(value)
		if len(parts.Images) != 1 || jsonx.Map(jsonx.Map(parts.Images[0])["image_url"])["url"] != "data:image/png;base64,QUJD" {
			t.Fatalf("image lost: %#v", parts)
		}
		if len(parts.Text) != 2 || parts.Text[0] != " image loaded\n" {
			t.Fatalf("text or metadata lost: %#v", parts)
		}
		for _, expected := range []string{`"isError":true`, `"next_cursor":"page2"`, `9007199254740993`} {
			if !strings.Contains(parts.Text[1], expected) {
				t.Fatalf("missing %s in %s", expected, parts.Text[1])
			}
		}
	}
}

func TestMalformedSSEFailsAtEveryChunkBoundary(t *testing.T) {
	_, context, _ := ToChat(map[string]any{"model": "test", "input": "hi"})
	for _, malformed := range []string{`{broken}`, `null`, `[]`, `{"choices":`} {
		wire := ": ping\r\n\r\ndata: {\"choices\":[{\"delta\":{\"content\":\"before\"}}]}\r\n\r\n" +
			"data: " + malformed + "\r\n\r\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"after\"},\"finish_reason\":\"stop\"}]}\r\n\r\ndata: [DONE]\r\n\r\n"
		for split := 0; split <= len(wire); split++ {
			adapter := NewStreamAdapter(context)
			events := adapter.Feed([]byte(wire[:split]))
			events = append(events, adapter.Feed([]byte(wire[split:]))...)
			events = append(events, adapter.Finish(nil)...)
			outcome := OutcomeFromEvents(events)
			if outcome.Status != "failed" || outcome.Code != "stream_invalid_json" || !adapter.Completed() {
				t.Fatalf("malformed=%q split=%d outcome=%#v", malformed, split, outcome)
			}
			terminals := 0
			for _, event := range events {
				if event.Type == "response.failed" || event.Type == "response.completed" {
					terminals++
				}
				if event.Type == "response.output_text.delta" && event.Data["delta"] == "after" {
					t.Fatalf("continued after malformed data at split %d", split)
				}
			}
			if terminals != 1 {
				t.Fatalf("expected one terminal, got %d", terminals)
			}
		}
	}
}

func TestSSECommentsAndMultilineDataRemainValid(t *testing.T) {
	_, context, _ := ToChat(map[string]any{"model": "test", "input": "hi"})
	adapter := NewStreamAdapter(context)
	wire := ": ping\n\nevent: message\ndata: {\"choices\":\ndata: [{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\n: ping\n\ndata: [DONE]\n\n"
	events := adapter.Feed([]byte(wire))
	if outcome := OutcomeFromEvents(events); outcome.Status != "completed" {
		t.Fatalf("valid multiline SSE failed: %#v", outcome)
	}
}

func TestUpstreamErrorImmediatelyEndsAdapter(t *testing.T) {
	_, context, _ := ToChat(map[string]any{"model": "test", "input": "hi"})
	adapter := NewStreamAdapter(context)
	events := adapter.Feed([]byte("data: {\"error\":{\"message\":\"overloaded\",\"code\":\"server_error\"}}\n\n"))
	if !adapter.Completed() || OutcomeFromEvents(events).Code != "server_error" {
		t.Fatalf("failed stream is still open: %#v", events)
	}
}

func TestCompactionPreservesUsersAndOnlyVisibleSummary(t *testing.T) {
	users := []any{
		map[string]any{"role": "user", "content": "Build a page"},
		map[string]any{"role": "assistant", "content": "Work done"},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "input_text", "text": "Match this image"},
			map[string]any{"type": "input_image", "image_url": "data:image/png;base64,QUJD"},
		}},
	}
	body := map[string]any{"model": "test", "input": users, "instructions": "Keep names intact",
		"tools": []any{map[string]any{"type": "function", "name": "exec"}}, "tool_choice": "required",
		"text": map[string]any{"format": map[string]any{"type": "json_object"}}, "stream": true}
	before, _ := json.Marshal(body)
	chat, context, err := ToCompactionChatWithOptions(body, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"tools", "tool_choice", "response_format", "stream", "stream_options"} {
		if _, exists := chat[key]; exists {
			t.Fatalf("compaction still has generation option %s", key)
		}
	}
	messages := jsonx.Slice(chat["messages"])
	if !strings.Contains(jsonx.String(jsonx.Map(messages[len(messages)-1])["content"]), "handoff summary") {
		t.Fatal("missing explicit summary task")
	}
	response, err := CompactionResponse(map[string]any{"choices": []any{map[string]any{
		"message": map[string]any{"content": "Completed page skeleton. Styling pending.", "reasoning_content": "I should summarize"}, "finish_reason": "stop",
	}}}, context)
	if err != nil {
		t.Fatal(err)
	}
	output := jsonx.Slice(response["output"])
	if response["object"] != "response.compaction" || len(output) != 3 {
		t.Fatalf("invalid compaction output: %#v", response)
	}
	if textFromParts(jsonx.Map(output[0])["content"]) != "Build a page" || !reflect.DeepEqual(jsonx.Map(output[1])["content"], jsonx.Map(users[2])["content"]) {
		t.Fatalf("user messages changed: %#v", output)
	}
	item := jsonx.Map(output[2])
	summary, ok := compactionSummaryFromEnvelope(jsonx.String(item["encrypted_content"]))
	if !ok || summary != "Completed page skeleton. Styling pending." || jsonx.String(item["id"]) == "" {
		t.Fatalf("invalid summary: %#v", item)
	}
	events := CompactionEvents(response, context)
	completed := jsonx.Map(events[len(events)-1].Data["response"])
	if completed["object"] != "response" || completed["status"] != "completed" || completed["tool_choice"] != "none" || len(jsonx.Slice(completed["tools"])) != 0 || jsonx.Map(jsonx.Map(completed["text"])["format"])["type"] != "text" {
		t.Fatalf("streaming compaction describes the wrong generation: %#v", completed)
	}
	replay, _, err := ToChat(map[string]any{"model": "test", "input": output})
	if err != nil || len(jsonx.Slice(replay["messages"])) != 3 {
		t.Fatalf("compaction cannot be replayed: %#v, %v", replay, err)
	}
	after, _ := json.Marshal(body)
	if string(before) != string(after) {
		t.Fatal("compaction mutated the request")
	}
}

func TestCompactionRejectsUnfinishedOrInvalidSummaries(t *testing.T) {
	_, context, _ := ToCompactionChatWithOptions(map[string]any{"model": "test", "input": "hi"}, Options{})
	for _, reason := range []string{"length", "content_filter", "", "unknown", "tool_calls"} {
		t.Run(reason, func(t *testing.T) {
			out, err := CompactionResponse(map[string]any{"choices": []any{map[string]any{
				"message": map[string]any{"content": "partial summary"}, "finish_reason": reason,
			}}}, context)
			if err == nil || out != nil {
				t.Fatalf("invalid summary accepted: %#v, %v", out, err)
			}
		})
	}
	for _, message := range []map[string]any{
		{"content": "", "refusal": "Cannot summarize"},
		{"content": []any{map[string]any{"type": "refusal", "refusal": "Cannot summarize"}}},
		{"content": "summary", "tool_calls": []any{map[string]any{"id": "call1", "function": map[string]any{"name": "exec", "arguments": "{}"}}}},
		{"content": "summary", "tool_calls": []any{map[string]any{"id": "call1", "function": map[string]any{"name": "exec", "arguments": "{"}}}},
	} {
		if out, err := CompactionResponse(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": "stop"}}}, context); err == nil || out != nil {
			t.Fatalf("non-summary output accepted: %#v, %v", out, err)
		}
	}
}
