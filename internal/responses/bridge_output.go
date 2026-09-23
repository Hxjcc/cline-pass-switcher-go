package responses

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
)

func responseOutputText(response map[string]any) string {
	parts := make([]string, 0, 2)
	for _, raw := range jsonx.Slice(response["output"]) {
		item := jsonx.Map(raw)
		switch jsonx.String(item["type"]) {
		case "message":
			if text := strings.TrimSpace(textFromParts(item["content"])); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func newID(prefix string) string {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(raw)
}

func responseIDFromChatID(value string) string {
	if strings.HasPrefix(value, "resp_") {
		return value
	}
	value = strings.TrimPrefix(value, "chatcmpl-")
	value = strings.TrimPrefix(value, "chatcmpl_")
	if value == "" {
		return newID("resp")
	}
	return "resp_" + value
}

func textFromParts(content any) string {
	if text, ok := content.(string); ok {
		return text
	}
	var builder strings.Builder
	for _, raw := range jsonx.Slice(content) {
		part := jsonx.Map(raw)
		if part == nil {
			continue
		}
		if text := jsonx.String(part["text"]); text != "" {
			builder.WriteString(text)
		} else if refusal := jsonx.String(part["refusal"]); refusal != "" {
			builder.WriteString(refusal)
		}
	}
	return builder.String()
}

func usageToResponses(value any) any {
	return reportedUsage(value, 1, 0)
}

// reportedUsage is the usage object Codex uses to decide whether the context
// window is full. Each gateway server tool (a search, for example) runs its own
// internal leg and adds that leg's prompt tokens onto the final usage, so the
// client-visible input is the reported sum divided by the number of legs.
// Output tokens stay as reported: they are not what trips compaction. History
// keeps the raw sum, because that is what the gateway bills.
func reportedUsage(value any, legs int, inputCap int64) any {
	usage := jsonx.Map(value)
	if usage == nil {
		return nil
	}
	if legs < 1 {
		legs = 1
	}
	input := intValue(usage["prompt_tokens"]) / int64(legs)
	output := intValue(usage["completion_tokens"])
	promptDetails := jsonx.Map(usage["prompt_tokens_details"])
	completionDetails := jsonx.Map(usage["completion_tokens_details"])
	cached := intValue(promptDetails["cached_tokens"]) / int64(legs)
	if inputCap > 0 && input > inputCap {
		input = inputCap
	}
	if cached > input {
		cached = input
	}
	return map[string]any{
		"input_tokens":          input,
		"input_tokens_details":  map[string]any{"cached_tokens": cached},
		"output_tokens":         output,
		"output_tokens_details": map[string]any{"reasoning_tokens": intValue(completionDetails["reasoning_tokens"])},
		"total_tokens":          input + output,
	}
}

// gatewayToolCallCounts reads provider_metadata.gateway.gatewayToolCalls.
// The gateway reports a count per server tool it ran inside its own loop, as
// {"exa_search": 2}. Each of those calls is an extra model leg whose prompt
// tokens are included in the same usage object, so the client-visible input has
// to be divided by calls+1. Counts are cumulative, so per-tool maxima are what
// merge across chunks. An array is counted by length and a bare number as
// itself, so a payload change cannot silently zero the count.
func gatewayToolCallCounts(root map[string]any) (map[string]int, int) {
	byName := map[string]int{}
	loose := 0
	merge := func(metadata map[string]any) {
		names, plain := toolCallsInMetadata(metadata)
		for name, count := range names {
			byName[name] = max(byName[name], count)
		}
		loose = max(loose, plain)
	}
	merge(jsonx.Map(root["provider_metadata"]))
	for _, raw := range jsonx.Slice(root["choices"]) {
		choice := jsonx.Map(raw)
		merge(jsonx.Map(choice["provider_metadata"]))
		merge(jsonx.Map(jsonx.Map(choice["delta"])["provider_metadata"]))
		merge(jsonx.Map(jsonx.Map(choice["message"])["provider_metadata"]))
	}
	return byName, loose
}

// toolCallsInMetadata splits one metadata payload into per-tool counts and a
// count the gateway reported without tool names.
func toolCallsInMetadata(metadata map[string]any) (map[string]int, int) {
	gateway := jsonx.Map(metadata["gateway"])
	if gateway == nil {
		return nil, 0
	}
	calls := gateway["gatewayToolCalls"]
	if named := jsonx.Map(calls); named != nil {
		counts := make(map[string]int, len(named))
		for name, raw := range named {
			if count := int(intValue(raw)); count > 0 {
				counts[name] = count
			}
		}
		return counts, 0
	}
	if slice := jsonx.Slice(calls); slice != nil {
		return nil, len(slice)
	}
	if count := int(intValue(calls)); count > 0 {
		return nil, count
	}
	return nil, 0
}

// gatewayLegIndex reads routing.modelAttempts[*].providerAttempts[*].toolLoopLegIndex,
// the loop iteration each provider call belonged to. The gateway runs one model
// call per leg, so the highest index pins the leg count exactly instead of
// inferring it from how many tools ran. Missing indexes report -1.
func gatewayLegIndex(root map[string]any) int {
	best := -1
	visit := func(metadata map[string]any) {
		gateway := jsonx.Map(metadata["gateway"])
		routing := jsonx.Map(gateway["routing"])
		if routing == nil {
			routing = jsonx.Map(metadata["routing"])
		}
		for _, rawModel := range jsonx.Slice(routing["modelAttempts"]) {
			attempts := jsonx.Slice(jsonx.Map(rawModel)["providerAttempts"])
			for _, rawAttempt := range attempts {
				attempt := jsonx.Map(rawAttempt)
				index, found := attempt["toolLoopLegIndex"]
				if !found {
					continue
				}
				if value := int(intValue(index)); value > best {
					best = value
				}
			}
		}
	}
	visit(jsonx.Map(root["provider_metadata"]))
	for _, raw := range jsonx.Slice(root["choices"]) {
		choice := jsonx.Map(raw)
		visit(jsonx.Map(choice["provider_metadata"]))
		visit(jsonx.Map(jsonx.Map(choice["delta"])["provider_metadata"]))
		visit(jsonx.Map(jsonx.Map(choice["message"])["provider_metadata"]))
	}
	return best
}

func intValue(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		result, _ := typed.Int64()
		return result
	default:
		return 0
	}
}

func intValueWithFallback(value any, fallback int64) int64 {
	if result := intValue(value); result != 0 {
		return result
	}
	return fallback
}

func (context *Context) responseOutputItemFromTool(toolCall map[string]any, status string) map[string]any {
	callID := jsonx.String(toolCall["id"])
	if callID == "" {
		callID = newID("call")
	}
	function := jsonx.Map(toolCall["function"])
	chatName := jsonx.String(function["name"])
	arguments := jsonx.String(function["arguments"])
	binding := context.bindings[chatName]
	switch binding.Kind {
	case "custom":
		item := map[string]any{
			"id": newID("ctc"), "type": "custom_tool_call", "status": status,
			"call_id": callID, "name": binding.Name, "input": customInputFromArguments(arguments),
		}
		if binding.Namespace != "" {
			item["namespace"] = binding.Namespace
		}
		return item
	case "tool_search":
		return map[string]any{
			"id": newID("tsc"), "type": "tool_search_call", "status": status,
			"call_id": callID, "execution": "client", "arguments": parseArgumentsObject(arguments),
		}
	default:
		name := binding.Name
		if name == "" {
			name = chatName
		}
		item := map[string]any{
			"id": newID("fc"), "type": "function_call", "status": status,
			"call_id": callID, "name": name, "arguments": arguments,
		}
		if binding.Namespace != "" {
			item["namespace"] = binding.Namespace
		}
		return item
	}
}

func (context *Context) responseBase(id string, createdAt int64, modelID, status string, output []any, usage, responseError any, incompleteReason string) map[string]any {
	var incomplete any
	if incompleteReason != "" {
		incomplete = map[string]any{"reason": incompleteReason}
	}
	return map[string]any{
		"id": id, "object": "response", "created_at": createdAt, "status": status,
		"error": responseError, "incomplete_details": incomplete,
		"instructions": context.Instructions, "max_output_tokens": context.MaxOutputTokens,
		"model": firstString(modelID, context.Model), "output": output,
		"parallel_tool_calls": context.ParallelToolCalls, "previous_response_id": nil,
		"reasoning": context.Reasoning, "store": false, "temperature": context.Temperature,
		"text": context.ResponseText, "tool_choice": context.ResponseToolChoice,
		"tools": context.ResponseTools, "top_p": context.TopP, "truncation": "disabled",
		"usage": usage, "metadata": context.Metadata,
	}
}

func firstString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// ChatFailure reports a buffered Chat response that the Responses state
// machine refused to commit (no final output, malformed tool call, unknown
// finish_reason, ...). Response is the failed Response object, so callers can
// still inspect whatever partial output was salvaged.
type ChatFailure struct {
	Code     string
	Type     string
	Message  string
	Response map[string]any
}

func (failure *ChatFailure) Error() string {
	return failure.Message
}

// deltaFromChatMessage reshapes a completed Chat message into the delta form
// used by streaming chunks, so buffered and streamed responses go through one
// state machine. Array position is authoritative for tool call ordering.
func deltaFromChatMessage(message map[string]any) map[string]any {
	delta := make(map[string]any, len(message))
	for key, value := range message {
		delta[key] = value
	}
	calls := jsonx.Slice(message["tool_calls"])
	if len(calls) == 0 {
		return delta
	}
	indexed := make([]any, 0, len(calls))
	for position, raw := range calls {
		call := jsonx.Map(raw)
		if call == nil {
			continue
		}
		cloned := make(map[string]any, len(call)+1)
		for key, value := range call {
			cloned[key] = value
		}
		cloned["index"] = position
		indexed = append(indexed, cloned)
	}
	delta["tool_calls"] = indexed
	return delta
}

// EventsFromChat replays a buffered Chat Completions response through the
// streaming state machine and returns the complete Responses event lifecycle,
// ending in response.completed, response.incomplete or response.failed.
func EventsFromChat(chat map[string]any, context *Context) ([]Event, error) {
	choices := jsonx.Slice(chat["choices"])
	if len(choices) == 0 {
		return nil, errors.New("upstream returned no choices")
	}
	choice := jsonx.Map(choices[0])
	chunk := map[string]any{
		"id": chat["id"], "model": chat["model"], "created": chat["created"], "usage": chat["usage"],
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         deltaFromChatMessage(jsonx.Map(choice["message"])),
			"finish_reason": choice["finish_reason"],
		}},
	}
	if message := jsonx.Map(choice["message"]); message["provider_metadata"] != nil {
		chunk["provider_metadata"] = message["provider_metadata"]
	}
	state := NewStreamState(context)
	events := state.HandleChunk(chunk)
	return append(events, state.Finalize(true, nil)...), nil
}

// FromChat converts a completed Chat Completions response to a Responses
// object using the same rules as the streaming adapter, so inline <think>
// blocks, content_filter, malformed tool calls and missing output are treated
// identically on both paths. A rejected turn is returned as *ChatFailure.
func FromChat(chat map[string]any, context *Context) (map[string]any, error) {
	events, err := EventsFromChat(chat, context)
	if err != nil {
		return nil, err
	}
	terminal := events[len(events)-1]
	response := jsonx.Map(terminal.Data["response"])
	if terminal.Type != "response.failed" {
		return response, nil
	}
	failure := &ChatFailure{Response: response, Message: "upstream response could not be converted"}
	if responseError := jsonx.Map(response["error"]); responseError != nil {
		failure.Code = jsonx.String(responseError["code"])
		failure.Type = jsonx.String(responseError["type"])
		if message := jsonx.String(responseError["message"]); message != "" {
			failure.Message = message
		}
	}
	return nil, failure
}

// ChatCompletionAsChunk rewrites a buffered Chat Completions object as a
// single streaming chunk, for clients that asked for SSE from an upstream
// that answered with plain JSON.
func ChatCompletionAsChunk(chat map[string]any) map[string]any {
	chunk := make(map[string]any, len(chat)+1)
	for key, value := range chat {
		if key != "choices" {
			chunk[key] = value
		}
	}
	chunk["object"] = "chat.completion.chunk"
	choices := make([]any, 0, 1)
	for position, raw := range jsonx.Slice(chat["choices"]) {
		choice := jsonx.Map(raw)
		if choice == nil {
			continue
		}
		index := choice["index"]
		if index == nil {
			index = position
		}
		choices = append(choices, map[string]any{
			"index":         index,
			"delta":         deltaFromChatMessage(jsonx.Map(choice["message"])),
			"finish_reason": choice["finish_reason"],
			"logprobs":      choice["logprobs"],
		})
	}
	chunk["choices"] = choices
	return chunk
}

func boolValue(value any, fallback bool) bool {
	if result, ok := value.(bool); ok {
		return result
	}
	return fallback
}
