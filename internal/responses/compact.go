package responses

import (
	"encoding/json"
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
	// Compaction is mechanical summarization, so it runs at "high" rather than
	// the session's maximum: a max-effort pass tends to spend the output budget
	// on hidden thinking and the gateway then answers "empty response content".
	// The caller escalates to the model's top level once if that still happens.
	if effort := compactionReasoningEffort(options.ReasoningEfforts); effort != "" {
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

// compactionReasoningEffort picks "high" when the model advertises it, falling
// back to the closest available level. Summaries need faithfulness rather than
// maximum deliberation, and the caller escalates on starvation.
func compactionReasoningEffort(efforts []string) string {
	for _, preferred := range []string{"high", "medium", "low", "minimal", "max", "xhigh"} {
		for _, effort := range efforts {
			if strings.EqualFold(strings.TrimSpace(effort), preferred) {
				return preferred
			}
		}
	}
	// No graded level advertised: prefer any real thinking level over "none"
	// (a none/max toggle resolves to max), and fall back to disabling thinking.
	for _, effort := range efforts {
		if value := strings.TrimSpace(effort); value != "" && !reasoningEffortOff(value) {
			return value
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

// EscalateCompactionBudget rewrites a compaction chat request for the retry
// pass: the model's highest advertised reasoning level plus a doubled output
// budget, so a summary that starved on hidden thinking gets room to land.
func EscalateCompactionBudget(body map[string]any, efforts []string) {
	if body == nil {
		return
	}
	if effort := highestReasoningEffort(efforts); effort != "" {
		body["reasoning_effort"] = effort
		if reasoningEffortOff(effort) {
			delete(body, "reasoning")
		} else {
			body["reasoning"] = map[string]any{"effort": effort}
		}
	}
	tokens := 0
	switch typed := body["max_tokens"].(type) {
	case float64:
		tokens = int(typed)
	case int:
		tokens = typed
	case int64:
		tokens = int(typed)
	case json.Number:
		value, _ := typed.Int64()
		tokens = int(value)
	}
	if tokens < 8192 {
		tokens = 8192
	}
	tokens *= 2
	if tokens > 32768 {
		tokens = 32768
	}
	body["max_tokens"] = tokens
}

func highestReasoningEffort(efforts []string) string {
	for _, preferred := range []string{"max", "xhigh", "high", "medium", "low"} {
		for _, effort := range efforts {
			if strings.EqualFold(strings.TrimSpace(effort), preferred) {
				return preferred
			}
		}
	}
	return compactionReasoningEffort(efforts)
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
