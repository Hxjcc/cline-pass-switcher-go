package responses

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
)

// PushWebSearchCall records one executed search on the client-visible output
// and returns its item lifecycle events. Clients that understand the hosted
// web_search tool turn this item into a "searched the web" activity.
func (adapter *StreamAdapter) PushWebSearchCall(call WebSearchCall) []Event {
	if call.ID == "" {
		call.ID = newID("ws")
	}
	state := adapter.state
	state.context.RecordWebSearch(call)
	index := state.nextOutputIndex
	state.nextOutputIndex++
	item := WebSearchCallItem(call)
	state.addOutput(index, item)
	pending := WebSearchCallItem(WebSearchCall{ID: call.ID, Query: call.Query, Status: "in_progress"})
	events := state.ensureStarted()
	events = append(events, state.closeReasoning()...)
	events = append(events, state.closeMessage()...)
	return append(events,
		event("response.output_item.added", map[string]any{"output_index": index, "item": pending}),
		event("response.output_item.done", map[string]any{"output_index": index, "item": item}),
	)
}

// PendingSearches returns the proxy-executed searches captured in this leg. A
// non-empty result means the leg ended by asking the proxy to search.
func (adapter *StreamAdapter) PendingSearches() []SearchRequest {
	return adapter.state.searches
}

// SearchAssistantMessage renders the assistant turn that requested the
// captured searches; it must be replayed upstream ahead of the tool results.
func (adapter *StreamAdapter) SearchAssistantMessage() map[string]any {
	state := adapter.state
	message := map[string]any{"role": "assistant"}
	toolCalls := make([]any, 0, len(state.searches))
	for _, call := range state.searches {
		toolCalls = append(toolCalls, map[string]any{
			"id": call.CallID, "type": "function",
			"function": map[string]any{"name": call.ChatName, "arguments": call.Arguments},
		})
	}
	message["tool_calls"] = toolCalls
	if content := state.legText.String(); strings.TrimSpace(content) != "" {
		message["content"] = content
	}
	// DeepSeek-style endpoints expect the thinking that produced a tool call
	// to be replayed with it; other providers reject the field.
	if reasoning := state.legReasoning.String(); reasoning != "" && state.context.ReplayReasoning {
		message["reasoning_content"] = reasoning
	}
	return message
}

// Next starts a fresh upstream leg for the same client-visible response.
// Response identity, output items and accumulated usage carry over, while
// per-leg stream bookkeeping starts clean.
func (adapter *StreamAdapter) Next() *StreamAdapter {
	state := adapter.state
	next := NewStreamState(state.context)
	next.responseID = state.responseID
	next.model = state.model
	next.createdAt = state.createdAt
	next.identityLocked = state.identityLocked
	next.started = state.started
	next.output = append([]outputEntry(nil), state.output...)
	next.nextOutputIndex = state.nextOutputIndex
	next.carriedUsage = mergeResponsesUsage(state.carriedUsage, state.usage)
	return &StreamAdapter{state: next}
}

// Fail publishes a terminal failure for the response.
func (adapter *StreamAdapter) Fail(message, code string) []Event {
	return adapter.state.Fail(message, code)
}

// FeedCompletion replays one complete Chat Completions response through this
// adapter, as if it had arrived as a single SSE chunk, and finalizes it.
func (adapter *StreamAdapter) FeedCompletion(chat map[string]any) ([]Event, error) {
	raw, err := json.Marshal(ChatCompletionAsChunk(chat))
	if err != nil {
		return nil, err
	}
	events := adapter.Feed([]byte("data: " + string(raw) + "\n\n"))
	if adapter.done {
		return events, nil
	}
	adapter.done = true
	// A buffered completion is complete by definition; treat it like a stream
	// that ended with its sentinel instead of demanding a finish_reason.
	return append(events, adapter.state.Finalize(true, nil)...), nil
}

// WebSearchToolContent renders proxy-side search results as the upstream tool
// message that answers the model's search call.
func WebSearchToolContent(query string, results []WebSearchResult) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Web search results for %q:", query)
	if len(results) == 0 {
		builder.WriteString("\nNo pages were returned.")
		return builder.String()
	}
	for index, result := range results {
		title := strings.TrimSpace(result.Title)
		if title == "" {
			title = result.URL
		}
		fmt.Fprintf(&builder, "\n\n%d. %s\n%s", index+1, title, result.URL)
		if text := strings.TrimSpace(result.Text); text != "" {
			builder.WriteString("\n")
			builder.WriteString(text)
		}
	}
	return builder.String()
}

// WebSearchFailureContent tells the model that a proxy-side search failed.
func WebSearchFailureContent(err error) string {
	return fmt.Sprintf("Web search failed: %v. Answer without live web data and tell the user the search did not run.", err)
}

// AppendMessages returns a copy of a Chat body with extra messages appended.
func AppendMessages(body map[string]any, extra ...any) map[string]any {
	next := make(map[string]any, len(body)+1)
	for key, value := range body {
		next[key] = value
	}
	messages := append([]any{}, jsonx.Slice(body["messages"])...)
	next["messages"] = append(messages, extra...)
	return next
}

// ChatSearchTurn extracts the proxy-side searches from a buffered Chat
// Completions response and returns the assistant message that must be
// replayed before their results. It reports nothing when the turn also calls
// client-executed tools, because that turn has to go back to the client.
func ChatSearchTurn(chat map[string]any, context *Context) (map[string]any, []SearchRequest) {
	choices := jsonx.Slice(chat["choices"])
	if len(choices) == 0 {
		return nil, nil
	}
	message := jsonx.Map(jsonx.Map(choices[0])["message"])
	if message == nil {
		return nil, nil
	}
	toolCalls := jsonx.Slice(message["tool_calls"])
	requests := make([]SearchRequest, 0, len(toolCalls))
	for _, raw := range toolCalls {
		call := jsonx.Map(raw)
		if call == nil {
			return nil, nil
		}
		function := jsonx.Map(call["function"])
		name := jsonx.String(function["name"])
		if !context.isDirectWebSearchTool(name) {
			return nil, nil
		}
		arguments, valid := canonicalToolArguments(jsonx.String(function["arguments"]))
		if !valid {
			return nil, nil
		}
		query := searchQueryFromArguments(arguments)
		if query == "" {
			return nil, nil
		}
		callID := jsonx.String(call["id"])
		if callID == "" {
			callID = newID("call")
		}
		requests = append(requests, SearchRequest{
			CallID: callID, ChatName: name, Arguments: arguments, Query: query,
		})
	}
	if len(requests) == 0 {
		return nil, nil
	}
	assistant := map[string]any{"role": "assistant", "tool_calls": toolCalls}
	if content := jsonx.String(message["content"]); strings.TrimSpace(content) != "" {
		assistant["content"] = content
	}
	if reasoning := jsonx.String(message["reasoning_content"]); reasoning != "" && context.ReplayReasoning {
		assistant["reasoning_content"] = reasoning
	}
	return assistant, requests
}

// MergeChatUsage sums the token counters of two Chat Completions usage
// objects, so a turn assembled from several upstream legs reports its total.
func MergeChatUsage(total, next any) any {
	incoming := jsonx.Map(next)
	if incoming == nil {
		return total
	}
	previous := jsonx.Map(total)
	if previous == nil {
		return incoming
	}
	merged := map[string]any{}
	for key, value := range previous {
		merged[key] = value
	}
	prompt := intValue(previous["prompt_tokens"]) + intValue(incoming["prompt_tokens"])
	completion := intValue(previous["completion_tokens"]) + intValue(incoming["completion_tokens"])
	totalTokens := intValue(previous["total_tokens"]) + intValue(incoming["total_tokens"])
	if totalTokens == 0 {
		totalTokens = prompt + completion
	}
	merged["prompt_tokens"] = prompt
	merged["completion_tokens"] = completion
	merged["total_tokens"] = totalTokens
	if details := mergeUsageDetail(previous["prompt_tokens_details"], incoming["prompt_tokens_details"], "cached_tokens"); details != nil {
		merged["prompt_tokens_details"] = details
	}
	if details := mergeUsageDetail(previous["completion_tokens_details"], incoming["completion_tokens_details"], "reasoning_tokens"); details != nil {
		merged["completion_tokens_details"] = details
	}
	return merged
}

func mergeUsageDetail(previous, incoming any, counter string) map[string]any {
	left, right := jsonx.Map(previous), jsonx.Map(incoming)
	if left == nil && right == nil {
		return nil
	}
	merged := map[string]any{}
	for name, value := range left {
		merged[name] = value
	}
	for name, value := range right {
		merged[name] = value
	}
	merged[counter] = intValue(left[counter]) + intValue(right[counter])
	return merged
}

// mergeResponsesUsage sums two Responses usage objects across upstream legs.
func mergeResponsesUsage(existing, next any) any {
	incoming := jsonx.Map(next)
	if incoming == nil {
		return existing
	}
	previous := jsonx.Map(existing)
	if previous == nil {
		return incoming
	}
	inputDetails, nextInputDetails := jsonx.Map(previous["input_tokens_details"]), jsonx.Map(incoming["input_tokens_details"])
	outputDetails, nextOutputDetails := jsonx.Map(previous["output_tokens_details"]), jsonx.Map(incoming["output_tokens_details"])
	input := intValue(previous["input_tokens"]) + intValue(incoming["input_tokens"])
	output := intValue(previous["output_tokens"]) + intValue(incoming["output_tokens"])
	total := intValue(previous["total_tokens"]) + intValue(incoming["total_tokens"])
	if total == 0 {
		total = input + output
	}
	return map[string]any{
		"input_tokens":          input,
		"input_tokens_details":  map[string]any{"cached_tokens": intValue(inputDetails["cached_tokens"]) + intValue(nextInputDetails["cached_tokens"])},
		"output_tokens":         output,
		"output_tokens_details": map[string]any{"reasoning_tokens": intValue(outputDetails["reasoning_tokens"]) + intValue(nextOutputDetails["reasoning_tokens"])},
		"total_tokens":          total,
	}
}
