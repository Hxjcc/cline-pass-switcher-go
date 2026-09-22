package responses

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
)

// The Chat backend has no native compaction endpoint. Explicitly ask for a
// handoff summary, and disable ordinary task execution for this generation.
const compactionInstructions = `Create a concise handoff summary of the preceding conversation for another model to continue from.
This is a compaction task. Do not answer the last user question, continue the original task, or call tools.
Return only the complete summary as visible plain text, using exactly these four sections and their headings:

## Objective
The user's goal, constraints and acceptance criteria.

## Work State
What is done, what was verified, what failed and which decisions were taken. Keep exact paths, commands, identifiers and error text.

## Next Move
The single next action to take, plus pending work and open questions.

## Relevant Files
Files, directories, endpoints or config keys that matter, one line each.

Keep every fact another model needs to continue accurately; drop small talk. Thinking alone is not a summary.`

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
		// The history and response headers should show the level compaction
		// actually ran at, while RequestedReasoningEffort keeps the session's
		// original choice.
		context.MappedReasoningEffort = effort
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
// DegradedCompactionResponse is the standalone-endpoint fallback used when no
// summary could be produced: the client still receives a valid compaction
// object, so the session keeps moving, at the cost of the older context.
func DegradedCompactionResponse(context *Context, reason, partial string) map[string]any {
	output := append([]any(nil), context.compactionUsers...)
	output = append(output, degradedCompactionItem(context, reason, partial))
	return map[string]any{
		"id": newID("resp"), "object": "response.compaction", "created_at": time.Now().Unix(),
		"output": output, "usage": nil,
	}
}

// DegradedCompactionTriggerResponse is the remote-compaction v2 fallback: one
// normal response whose single output item is the degraded compaction item.
func DegradedCompactionTriggerResponse(context *Context, reason, partial string) map[string]any {
	return context.responseBase(
		newID("resp"), time.Now().Unix(), context.Model, "completed",
		[]any{degradedCompactionItem(context, reason, partial)}, nil, nil, "",
	)
}

func degradedCompactionItem(context *Context, reason, partial string) map[string]any {
	return map[string]any{
		"id":                newID("cmp"),
		"type":              "compaction",
		"encrypted_content": CompactionEnvelope(degradedCompactionSummary(context, reason, partial)),
	}
}

// PartialCompactionSummary returns whatever visible text a failed summary
// attempt produced, so a degraded item can keep the usable part.
func PartialCompactionSummary(chat map[string]any) string {
	choices := jsonx.Slice(chat["choices"])
	if len(choices) == 0 {
		// Error bodies (500/503, rate limits) carry no completion to salvage.
		return ""
	}
	choice := jsonx.Map(choices[0])
	return strings.TrimSpace(collectPartText(jsonx.Map(choice["message"])["content"]))
}

// degradedCompactionSummary keeps the compaction shape (the four headings) so
// the next model still gets a usable handoff, and preserves the most recent
// user requests verbatim because everything older is gone.
func degradedCompactionSummary(context *Context, reason, partial string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "summary generation failed"
	}
	recent := make([]string, 0, 4)
	for _, raw := range context.compactionUsers {
		message := jsonx.Map(raw)
		text := strings.TrimSpace(collectPartText(message["content"]))
		if text == "" {
			text = strings.TrimSpace(jsonx.String(message["text"]))
		}
		if text == "" {
			continue
		}
		recent = append(recent, truncateText(text, 400))
	}
	if len(recent) > 3 {
		recent = recent[len(recent)-3:]
	}
	var builder strings.Builder
	builder.WriteString("[compaction degraded] The earlier conversation could not be summarized: ")
	builder.WriteString(truncateText(reason, 200))
	builder.WriteString("\n\n## Objective\n")
	if len(recent) > 0 {
		builder.WriteString("Unknown beyond the recent user requests preserved below.\n")
	} else {
		builder.WriteString("Unknown: the earlier conversation is no longer available.\n")
	}
	builder.WriteString("\n## Work State\nUnavailable: summarization failed, so completed work and tool results from earlier turns were dropped.\n")
	if partial = strings.TrimSpace(partial); partial != "" {
		builder.WriteString("\nPartial summary produced before the failure (may be cut off):\n")
		builder.WriteString(partial)
		builder.WriteString("\n")
	}
	builder.WriteString("\n## Next Move\nRe-read the recent user requests below and continue from the last one; ask the user to restate the goal if it is unclear.\n")
	builder.WriteString("\n## Relevant Files\nUnknown: recover paths from the recent requests below.\n")
	if len(recent) > 0 {
		builder.WriteString("\n---\nRecent user requests (verbatim, oldest first):\n")
		for _, text := range recent {
			builder.WriteString("- ")
			builder.WriteString(strings.Join(strings.Fields(text), " "))
			builder.WriteString("\n")
		}
	}
	return strings.TrimSpace(builder.String())
}

func truncateText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
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
