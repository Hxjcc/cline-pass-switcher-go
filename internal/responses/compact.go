package responses

import (
	"strings"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
)

// The Chat backend has no native compaction endpoint. Explicitly ask for a
// handoff summary, and disable ordinary task execution for this generation.
const compactionInstructions = `Create a concise handoff summary of the preceding conversation for another model to continue from.
This is a compaction task. Do not answer the last user question, continue the original task, or call tools.
Preserve the user's goals and constraints, decisions, completed work, important tool results and errors, exact paths and identifiers, and pending work.
User messages will be retained verbatim alongside the summary. Focus on the assistant/tool context needed to continue accurately.
Return only the complete summary as visible plain text. Thinking alone is not a summary.`

func ToCompactionChatWithOptions(body map[string]any, options Options) (map[string]any, *Context, error) {
	chat, context, err := ToChatWithOptions(body, options)
	if err != nil {
		return nil, nil, err
	}
	context.compactionUsers = compactionUserMessages(body["input"])
	messages := jsonx.Slice(chat["messages"])
	leading := 0
	for leading < len(messages) && jsonx.Map(messages[leading])["role"] == "system" {
		leading++
	}
	prepared := make([]any, 0, len(messages)+2)
	prepared = append(prepared, messages[:leading]...)
	prepared = append(prepared, map[string]any{"role": "system", "content": compactionInstructions})
	prepared = append(prepared, messages[leading:]...)
	prepared = append(prepared, map[string]any{"role": "user", "content": compactionInstructions})
	chat["messages"] = prepared
	// Compaction is mechanical summarization. If the session runs at a high
	// reasoning effort the model can spend the whole output budget on hidden
	// thinking and the gateway answers "empty response content"; ask for the
	// cheapest level the model advertises so the budget goes to the summary.
	if effort := minimalReasoningEffort(options.ReasoningEfforts); effort != "" {
		chat["reasoning_effort"] = effort
		if reasoningEffortOff(effort) {
			delete(chat, "reasoning")
		} else {
			chat["reasoning"] = map[string]any{"effort": effort}
		}
	}
	for _, key := range []string{"stream", "stream_options", "tools", "tool_choice", "parallel_tool_calls", "response_format"} {
		delete(chat, key)
	}
	context.ResponseTools = []any{}
	context.ResponseToolChoice = "none"
	context.ResponseText = map[string]any{"format": map[string]any{"type": "text"}}
	context.outputSchema = nil
	context.ParallelToolCalls = false
	return chat, context, nil
}

// minimalReasoningEffort picks the cheapest advertised level so a compaction
// turn cannot burn its token budget on hidden reasoning.
func minimalReasoningEffort(efforts []string) string {
	for _, preferred := range []string{"none", "minimal", "low"} {
		for _, effort := range efforts {
			if strings.EqualFold(strings.TrimSpace(effort), preferred) {
				return preferred
			}
		}
	}
	for _, effort := range efforts {
		if value := strings.TrimSpace(effort); value != "" {
			return value
		}
	}
	return ""
}

func reasoningEffortOff(effort string) bool {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "none", "off", "disabled":
		return true
	default:
		return false
	}
}

// Keep original user turns, including images, outside the summary. Construct
// output message objects without changing the request maps or content blocks.
func compactionUserMessages(input any) []any {
	items := jsonx.Slice(input)
	if text, ok := input.(string); ok {
		items = []any{text}
	}
	users := make([]any, 0)
	for _, raw := range items {
		var source map[string]any
		if text, ok := raw.(string); ok {
			source = map[string]any{"role": "user", "content": text}
		} else {
			source = jsonx.Map(raw)
		}
		if jsonx.String(source["role"]) != "user" {
			continue
		}
		message := make(map[string]any, len(source)+3)
		for key, value := range source {
			message[key] = value
		}
		message["type"], message["status"] = "message", "completed"
		if jsonx.String(message["id"]) == "" {
			message["id"] = newID("msg")
		}
		if text, ok := message["content"].(string); ok {
			message["content"] = []any{map[string]any{"type": "input_text", "text": text}}
		}
		users = append(users, message)
	}
	return users
}
