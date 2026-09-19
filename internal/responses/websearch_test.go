package responses

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
)

func directSearchContext(t *testing.T) *Context {
	t.Helper()
	chat, context, err := ToChatWithOptions(map[string]any{
		"model": "cline-pass/test",
		"input": "搜索一下今天的新闻",
		"tools": []any{map[string]any{"type": "web_search"}},
	}, Options{WebSearchDirect: true, ReplayReasoning: true})
	if err != nil {
		t.Fatal(err)
	}
	tools := jsonx.Slice(chat["tools"])
	if len(tools) == 0 {
		t.Fatalf("direct search must declare a tool: %#v", chat)
	}
	first := jsonx.Map(tools[0])
	function := jsonx.Map(first["function"])
	if first["type"] != "function" || jsonx.String(function["name"]) == "" {
		t.Fatalf("hosted web_search must become a proxy function tool: %#v", tools[0])
	}
	if !strings.Contains(jsonx.String(function["description"]), "Search the live web") {
		t.Fatalf("search tool description is missing: %#v", function)
	}
	return context
}

// sseChunk renders one Chat Completions delta as an SSE frame. Building the
// payload from Go data keeps the JSON on a single data line.
func sseChunk(t *testing.T, chunk map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(chunk)
	if err != nil {
		t.Fatal(err)
	}
	return []byte("data: " + string(raw) + "\n\n")
}

func searchToolCallChunk(t *testing.T, query string, usage map[string]any) []byte {
	t.Helper()
	arguments, err := json.Marshal(map[string]any{"query": query})
	if err != nil {
		t.Fatal(err)
	}
	chunk := map[string]any{
		"choices": []any{map[string]any{
			"index": 0,
			"delta": map[string]any{
				"role": "assistant",
				"tool_calls": []any{map[string]any{
					"index": 0, "id": "call_1", "type": "function",
					"function": map[string]any{
						"name":      "web_search",
						"arguments": string(arguments),
					},
				}},
			},
			"finish_reason": "tool_calls",
		}},
	}
	if usage != nil {
		chunk["usage"] = usage
	}
	return sseChunk(t, chunk)
}

func itemTypes(events []Event) []string {
	types := make([]string, 0, len(events))
	for _, value := range events {
		if value.Type != "response.output_item.done" {
			continue
		}
		types = append(types, jsonx.String(jsonx.Map(value.Data["item"])["type"]))
	}
	return types
}

func terminalEvent(t *testing.T, events []Event) Event {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("no events were produced")
	}
	last := events[len(events)-1]
	switch last.Type {
	case "response.completed", "response.incomplete", "response.failed":
		return last
	default:
		t.Fatalf("stream did not end in a terminal event: %#v", last)
		return Event{}
	}
}

func TestProxyWebSearchCallContinuesTheTurn(t *testing.T) {
	context := directSearchContext(t)
	adapter := NewStreamAdapter(context)
	events := adapter.Feed(searchToolCallChunk(t, "今日新闻", map[string]any{
		"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12,
	}))
	events = append(events, adapter.Finish(nil)...)

	for _, value := range events {
		if value.Type == "response.output_item.done" {
			t.Fatalf("the search call leaked to the client: %#v", value.Data)
		}
	}
	if len(adapter.state.searches) != 1 {
		t.Fatalf("search call was not captured: %#v", adapter.state.searches)
	}
	request := adapter.state.searches[0]
	if request.Query != "今日新闻" || request.ChatName == "" || request.CallID != "call_1" {
		t.Fatalf("unexpected search request: %#v", request)
	}

	// The proxy runs the search and reports it as the official item.
	searchEvents := adapter.PushWebSearchCall(WebSearchCall{Query: request.Query})
	if types := itemTypes(searchEvents); len(types) != 1 || types[0] != "web_search_call" {
		t.Fatalf("search item was not emitted: %#v", types)
	}
	item := jsonx.Map(searchEvents[len(searchEvents)-1].Data["item"])
	action := jsonx.Map(item["action"])
	id := jsonx.String(item["id"])
	if !strings.HasPrefix(id, "ws_") {
		t.Fatalf("search item needs a ws_ id: %#v", item)
	}
	if item["status"] != "completed" || action["type"] != "search" || action["query"] != "今日新闻" {
		t.Fatalf("unexpected search item: %#v", item)
	}
	queries := jsonx.Slice(action["queries"])
	if len(queries) != 1 || jsonx.String(queries[0]) != "今日新闻" {
		t.Fatalf("search item should carry the query list: %#v", action)
	}

	assistant := adapter.SearchAssistantMessage()
	toolCalls := jsonx.Slice(assistant["tool_calls"])
	if len(toolCalls) != 1 {
		t.Fatalf("assistant replay is missing the tool call: %#v", assistant)
	}
	if function := jsonx.Map(jsonx.Map(toolCalls[0])["function"]); jsonx.String(function["name"]) != request.ChatName {
		t.Fatalf("assistant replay lost the tool name: %#v", toolCalls[0])
	}

	// The next leg answers with the pages the proxy fetched.
	next := adapter.Next()
	answer := sseChunk(t, map[string]any{
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         map[string]any{"role": "assistant", "content": "今天的要闻是……"},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{"prompt_tokens": 20, "completion_tokens": 8, "total_tokens": 28},
	})
	nextEvents := next.Feed(answer)
	nextEvents = append(nextEvents, next.Finish(nil)...)
	terminal := terminalEvent(t, nextEvents)
	response := jsonx.Map(terminal.Data["response"])
	output := jsonx.Slice(response["output"])
	if len(output) != 2 {
		t.Fatalf("final output should hold the search and the answer: %#v", output)
	}
	if first := jsonx.Map(output[0]); first["type"] != "web_search_call" || jsonx.String(first["id"]) != id {
		t.Fatalf("search item must stay ahead of the answer: %#v", output)
	}
	if second := jsonx.Map(output[1]); second["type"] != "message" {
		t.Fatalf("missing final message: %#v", output)
	}
	usage := jsonx.Map(response["usage"])
	if intValue(usage["input_tokens"]) != 30 || intValue(usage["output_tokens"]) != 10 || intValue(usage["total_tokens"]) != 40 {
		t.Fatalf("usage should be summed over legs: %#v", usage)
	}
}

func TestProxyWebSearchKeepsReasoningForReplay(t *testing.T) {
	context := directSearchContext(t)
	adapter := NewStreamAdapter(context)
	events := adapter.Feed(sseChunk(t, map[string]any{
		"choices": []any{map[string]any{
			"index": 0,
			"delta": map[string]any{
				"reasoning_content": "先想想",
				"tool_calls": []any{map[string]any{
					"index": 0, "id": "call_1", "type": "function",
					"function": map[string]any{"name": "web_search", "arguments": `{"query":"a"}`},
				}},
			},
			"finish_reason": "tool_calls",
		}},
	}))
	events = append(events, adapter.Finish(nil)...)
	if len(adapter.state.searches) != 1 {
		t.Fatalf("search call was not captured: %#v", adapter.state.searches)
	}
	assistant := adapter.SearchAssistantMessage()
	if jsonx.String(assistant["reasoning_content"]) != "先想想" {
		t.Fatalf("DeepSeek-style reasoning must be replayed with the call: %#v", assistant)
	}
}

func TestProxyWebSearchAccumulatesStreamedArguments(t *testing.T) {
	context := directSearchContext(t)
	adapter := NewStreamAdapter(context)
	// Real upstreams stream tool calls in fragments: the name arrives with the
	// first delta and the JSON arguments are split across later ones.
	events := adapter.Feed(sseChunk(t, map[string]any{
		"choices": []any{map[string]any{
			"index": 0,
			"delta": map[string]any{
				"role": "assistant",
				"tool_calls": []any{
					map[string]any{
						"index": 0, "id": "call_1", "type": "function",
						"function": map[string]any{"name": "web_search", "arguments": `{"que`},
					},
					map[string]any{
						"index": 1, "id": "call_2", "type": "function",
						"function": map[string]any{"name": "web_search", "arguments": `{"query":"`},
					},
				},
			},
		}},
	}))
	events = append(events, adapter.Feed(sseChunk(t, map[string]any{
		"choices": []any{map[string]any{
			"index": 0,
			"delta": map[string]any{
				"tool_calls": []any{
					map[string]any{"index": 0, "function": map[string]any{"arguments": `ry":"first"}`}},
					map[string]any{"index": 1, "function": map[string]any{"arguments": `second"}`}},
				},
			},
			"finish_reason": "tool_calls",
		}},
	}))...)
	events = append(events, adapter.Finish(nil)...)
	_ = events

	requests := adapter.PendingSearches()
	if len(requests) != 2 {
		t.Fatalf("both searches should be captured: %#v", requests)
	}
	if requests[0].Query != "first" || requests[1].Query != "second" {
		t.Fatalf("streamed arguments were not accumulated: %#v", requests)
	}
	if requests[0].CallID != "call_1" || requests[1].CallID != "call_2" {
		t.Fatalf("call ids were not preserved: %#v", requests)
	}
}

func TestHostedWebSearchHistoryIsNotReplayedAsUserText(t *testing.T) {
	chat, _, err := ToChatWithOptions(map[string]any{
		"model": "cline-pass/test",
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "你好"}}},
			map[string]any{"type": "web_search_call", "id": "ws_1", "status": "completed", "action": map[string]any{"type": "search", "query": "secret query"}},
			map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "answer"}}},
		},
	}, Options{WebSearchDirect: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(chat["messages"])
	if strings.Contains(string(raw), "secret query") || strings.Contains(string(raw), "ws_1") {
		t.Fatalf("stored web_search_call items must not leak into user text: %s", raw)
	}
}
