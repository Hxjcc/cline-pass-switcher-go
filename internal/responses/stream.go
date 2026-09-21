package responses

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/apierr"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/sse"
)

const (
	thinkOpenTag  = "<think>"
	thinkCloseTag = "</think>"
)

type inlineThinkMode uint8

const (
	inlineThinkDetecting inlineThinkMode = iota
	inlineThinkReasoning
	inlineThinkText
)

type Event struct {
	Type string
	Data map[string]any
}

// Outcome is the protocol result of a generation, independent of the HTTP
// transport succeeding. Both buffered completions and SSE use these terminals.
type Outcome struct {
	Status           string
	Code             string
	Message          string
	IncompleteReason string
}

func OutcomeFromEvents(events []Event) Outcome {
	for index := len(events) - 1; index >= 0; index-- {
		value := events[index]
		response := jsonx.Map(value.Data["response"])
		switch value.Type {
		case "response.completed":
			return Outcome{Status: "completed"}
		case "response.incomplete":
			return Outcome{Status: "incomplete", IncompleteReason: jsonx.String(jsonx.Map(response["incomplete_details"])["reason"])}
		case "response.failed":
			details := jsonx.Map(response["error"])
			return Outcome{Status: "failed", Code: jsonx.String(details["code"]), Message: jsonx.String(details["message"])}
		}
	}
	return Outcome{}
}

func (outcome Outcome) ErrorMessage() string {
	switch outcome.Status {
	case "failed":
		return firstString(outcome.Code, "response.failed") + ": " + firstString(outcome.Message, "upstream response failed")
	case "incomplete":
		return "response.incomplete: " + firstString(outcome.IncompleteReason, "unknown reason")
	default:
		return ""
	}
}

func WriteEvent(writer io.Writer, event Event) error {
	raw, err := json.Marshal(event.Data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", event.Type, raw)
	return err
}

// EventWriter writes Responses events for one client connection, numbering
// them with sequence_number the way OpenAI does. Shared streams fan the same
// Event values out to several clients, so the counter lives with the writer
// and the payload is copied instead of mutated.
type EventWriter struct {
	writer io.Writer
	next   int
}

func NewEventWriter(writer io.Writer) *EventWriter {
	return &EventWriter{writer: writer}
}

func (w *EventWriter) Write(event Event) error {
	payload := make(map[string]any, len(event.Data)+1)
	for key, value := range event.Data {
		payload[key] = value
	}
	payload["sequence_number"] = w.next
	w.next++
	return WriteEvent(w.writer, Event{Type: event.Type, Data: payload})
}

type textState struct {
	ItemID      string
	OutputIndex int
	Text        strings.Builder
	Done        bool
}

type messageState struct {
	ItemID      string
	OutputIndex int
	Parts       []any
	PartKind    string
	PartText    strings.Builder
	Done        bool
}

type toolState struct {
	Key         int
	OutputIndex int
	CallID      string
	ChatName    string
	Arguments   strings.Builder
	ItemID      string
	Kind        string
	Added       bool
	Done        bool
}

// outputEntry pairs a completed output item with its output_index so the
// final response lists items in wire order even when they were closed out of
// order (interleaved thinking, text after tool calls).
type outputEntry struct {
	index int
	item  map[string]any
}

type StreamState struct {
	context           *Context
	responseID        string
	model             string
	createdAt         int64
	identityLocked    bool
	started           bool
	completed         bool
	finishReason      string
	usage             any
	output            []outputEntry
	nextOutputIndex   int
	reasoning         *textState
	message           *messageState
	inlineThinkMode   inlineThinkMode
	inlineThinkRaw    string
	inlineThinkSeen   bool
	inlineTrimLeading bool
	tools             map[int]*toolState
	lastToolIndex     int
	droppedTools      int
}

func NewStreamState(context *Context) *StreamState {
	return &StreamState{
		context:       context,
		responseID:    newID("resp"),
		model:         context.Model,
		createdAt:     time.Now().Unix(),
		tools:         map[int]*toolState{},
		lastToolIndex: -1,
	}
}

func event(eventType string, payload map[string]any) Event {
	payload["type"] = eventType
	return Event{Type: eventType, Data: payload}
}

func (state *StreamState) addOutput(index int, item map[string]any) {
	state.output = append(state.output, outputEntry{index: index, item: item})
}

func (state *StreamState) outputItems() []any {
	sort.SliceStable(state.output, func(left, right int) bool {
		return state.output[left].index < state.output[right].index
	})
	items := make([]any, 0, len(state.output))
	for _, entry := range state.output {
		items = append(items, entry.item)
	}
	return items
}

func (state *StreamState) baseResponse(status string, responseError any, incompleteReason string) map[string]any {
	usage := state.usage
	output := state.outputItems()
	if status == "in_progress" {
		usage = nil
		output = []any{}
	}
	return state.context.responseBase(
		state.responseID, state.createdAt, state.model, status, output, usage, responseError, incompleteReason,
	)
}

func (state *StreamState) ensureStarted() []Event {
	if state.started {
		return nil
	}
	state.started = true
	response := state.baseResponse("in_progress", nil, "")
	return []Event{
		event("response.created", map[string]any{"response": response}),
		event("response.in_progress", map[string]any{"response": response}),
	}
}

func (state *StreamState) ensureReasoning() []Event {
	if state.reasoning != nil && !state.reasoning.Done {
		return nil
	}
	events := state.ensureStarted()
	// A new thinking segment after visible text (interleaved thinking) ends
	// the current message item so output_index order matches the wire order.
	events = append(events, state.closeMessage()...)
	reasoning := &textState{ItemID: newID("rs"), OutputIndex: state.nextOutputIndex}
	state.nextOutputIndex++
	state.reasoning = reasoning
	item := map[string]any{
		"id": reasoning.ItemID, "type": "reasoning", "status": "in_progress", "summary": []any{},
	}
	if state.context != nil && state.context.RawReasoning {
		item["content"] = []any{}
	}
	events = append(events, event("response.output_item.added", map[string]any{
		"output_index": reasoning.OutputIndex,
		"item":         item,
	}))
	// ChatGPT Desktop / Codex renders the summary lifecycle, not raw
	// reasoning_text. Always emit summary events; DeepSeek still gets the
	// extra raw deltas below for Codex Desktop's raw reasoning path.
	events = append(events, event("response.reasoning_summary_part.added", map[string]any{
		"item_id": reasoning.ItemID, "output_index": reasoning.OutputIndex, "summary_index": 0,
		"part": map[string]any{"type": "summary_text", "text": ""},
	}))
	return events
}

func (state *StreamState) pushReasoning(delta string) []Event {
	if delta == "" {
		return nil
	}
	// Whitespace inside an open item is a paragraph break; whitespace that
	// would open a new item is noise.
	if (state.reasoning == nil || state.reasoning.Done) && strings.TrimSpace(delta) == "" {
		return nil
	}
	events := state.ensureReasoning()
	state.reasoning.Text.WriteString(delta)
	events = append(events, event("response.reasoning_summary_text.delta", map[string]any{
		"item_id": state.reasoning.ItemID, "output_index": state.reasoning.OutputIndex,
		"summary_index": 0, "delta": delta,
	}))
	if state.context != nil && state.context.RawReasoning {
		events = append(events, event("response.reasoning_text.delta", map[string]any{
			"item_id": state.reasoning.ItemID, "output_index": state.reasoning.OutputIndex,
			"content_index": 0, "delta": delta,
		}))
	}
	return events
}

func (state *StreamState) closeReasoning() []Event {
	if state.reasoning == nil || state.reasoning.Done {
		return nil
	}
	state.reasoning.Done = true
	text := state.reasoning.Text.String()
	item := map[string]any{
		"id": state.reasoning.ItemID, "type": "reasoning",
		"summary": []any{map[string]any{"type": "summary_text", "text": text}},
	}
	if state.context != nil && state.context.RawReasoning {
		item["content"] = []any{map[string]any{"type": "reasoning_text", "text": text}}
	}
	if text != "" {
		state.addOutput(state.reasoning.OutputIndex, item)
	}
	events := []Event{
		event("response.reasoning_summary_text.done", map[string]any{
			"item_id": state.reasoning.ItemID, "output_index": state.reasoning.OutputIndex,
			"summary_index": 0, "text": text,
		}),
		event("response.reasoning_summary_part.done", map[string]any{
			"item_id": state.reasoning.ItemID, "output_index": state.reasoning.OutputIndex,
			"summary_index": 0,
			"part":          map[string]any{"type": "summary_text", "text": text},
		}),
	}
	if state.context != nil && state.context.RawReasoning {
		events = append(events, event("response.reasoning_text.done", map[string]any{
			"item_id": state.reasoning.ItemID, "output_index": state.reasoning.OutputIndex,
			"content_index": 0, "text": text,
		}))
	}
	return append(events, event("response.output_item.done", map[string]any{
		"output_index": state.reasoning.OutputIndex, "item": item,
	}))
}

func (state *StreamState) ensureMessage(kind string) []Event {
	events := state.ensureStarted()
	if state.message == nil || state.message.Done {
		state.message = &messageState{ItemID: newID("msg"), OutputIndex: state.nextOutputIndex}
		state.nextOutputIndex++
		message := state.message
		events = append(events, event("response.output_item.added", map[string]any{
			"output_index": message.OutputIndex,
			"item": map[string]any{
				"id": message.ItemID, "type": "message", "status": "in_progress",
				"role": "assistant", "content": []any{},
			},
		}))
	}
	message := state.message
	if message.PartKind == kind {
		return events
	}
	events = append(events, state.closeMessagePart()...)
	message.PartKind = kind
	part := map[string]any{"type": kind, "refusal": ""}
	if kind == "output_text" {
		part = map[string]any{"type": kind, "text": "", "annotations": []any{}}
	}
	return append(events, event("response.content_part.added", map[string]any{
		"item_id": message.ItemID, "output_index": message.OutputIndex, "content_index": len(message.Parts), "part": part,
	}))
}

func (state *StreamState) pushRefusal(delta string) []Event {
	if delta == "" {
		return nil
	}
	events := state.flushInlineThink()
	events = append(events, state.closeReasoning()...)
	events = append(events, state.ensureMessage("refusal")...)
	state.message.PartText.WriteString(delta)
	return append(events, event("response.refusal.delta", map[string]any{
		"item_id": state.message.ItemID, "output_index": state.message.OutputIndex,
		"content_index": len(state.message.Parts), "delta": delta,
	}))
}

func (state *StreamState) emitText(delta string) []Event {
	if delta == "" {
		return nil
	}
	if (state.message == nil || state.message.Done) && strings.TrimSpace(delta) == "" {
		return nil
	}
	events := state.closeReasoning()
	events = append(events, state.ensureMessage("output_text")...)
	state.message.PartText.WriteString(delta)
	return append(events, event("response.output_text.delta", map[string]any{
		"item_id": state.message.ItemID, "output_index": state.message.OutputIndex,
		"content_index": len(state.message.Parts), "delta": delta,
	}))
}

// pushContent routes visible content through the inline <think> splitter.
// Some OpenAI-compatible upstreams (MiniMax, some Qwen/GLM deployments) put
// the chain-of-thought in the content field, and interleaved-thinking models
// alternate several <think> blocks with the visible answer.
func (state *StreamState) pushContent(delta string) []Event {
	if delta == "" {
		return nil
	}
	state.inlineThinkRaw += delta
	return state.drainInline()
}

func (state *StreamState) drainInline() []Event {
	events := []Event{}
	for {
		raw := state.inlineThinkRaw
		switch state.inlineThinkMode {
		case inlineThinkDetecting:
			trimmed := strings.TrimLeft(raw, " \t\r\n")
			if trimmed == "" || (len(trimmed) < len(thinkOpenTag) && strings.HasPrefix(thinkOpenTag, trimmed)) {
				return events
			}
			if strings.HasPrefix(trimmed, thinkOpenTag) {
				state.inlineThinkMode = inlineThinkReasoning
				state.inlineThinkSeen = true
				state.inlineThinkRaw = trimmed[len(thinkOpenTag):]
				continue
			}
			state.inlineThinkMode = inlineThinkText
		case inlineThinkReasoning:
			index := strings.Index(raw, thinkCloseTag)
			if index < 0 {
				// Keep a tail that could still be the start of a split closing tag.
				keep := trailingTagPrefixLen(raw, thinkCloseTag)
				state.inlineThinkRaw = raw[len(raw)-keep:]
				return append(events, state.pushReasoning(raw[:len(raw)-keep])...)
			}
			state.inlineThinkMode = inlineThinkText
			state.inlineTrimLeading = true
			state.inlineThinkRaw = raw[index+len(thinkCloseTag):]
			events = append(events, state.pushReasoning(raw[:index])...)
		default:
			if state.inlineTrimLeading {
				raw = strings.TrimLeft(raw, " \t\r\n")
				state.inlineThinkRaw = raw
				if raw == "" {
					return events
				}
				state.inlineTrimLeading = false
			}
			// Further <think> blocks are only recognised once the response
			// has opened with one, so a model that never uses inline
			// thinking can still print the literal tag in code.
			if !state.inlineThinkSeen {
				state.inlineThinkRaw = ""
				return append(events, state.emitText(raw)...)
			}
			if index := strings.Index(raw, thinkOpenTag); index >= 0 {
				state.inlineThinkMode = inlineThinkReasoning
				state.inlineThinkRaw = raw[index+len(thinkOpenTag):]
				events = append(events, state.emitText(raw[:index])...)
				continue
			}
			keep := trailingTagPrefixLen(raw, thinkOpenTag)
			state.inlineThinkRaw = raw[len(raw)-keep:]
			return append(events, state.emitText(raw[:len(raw)-keep])...)
		}
	}
}

// trailingTagPrefixLen returns how many bytes at the end of text could be the
// beginning of tag split across chunks.
func trailingTagPrefixLen(text, tag string) int {
	limit := len(tag) - 1
	if len(text) < limit {
		limit = len(text)
	}
	for size := limit; size > 0; size-- {
		if strings.HasSuffix(text, tag[:size]) {
			return size
		}
	}
	return 0
}

// flushInlineThink releases whatever the splitter is holding back (an
// undecided prefix or a possible split tag). Called before tool calls and
// refusals and at the end of the stream.
func (state *StreamState) flushInlineThink() []Event {
	raw := state.inlineThinkRaw
	state.inlineThinkRaw = ""
	switch state.inlineThinkMode {
	case inlineThinkDetecting:
		state.inlineThinkMode = inlineThinkText
		return state.emitText(raw)
	case inlineThinkReasoning:
		// An unterminated block is still reasoning.
		state.inlineThinkMode = inlineThinkText
		return state.pushReasoning(raw)
	default:
		if state.inlineTrimLeading {
			raw = strings.TrimLeft(raw, " \t\r\n")
			if raw != "" {
				state.inlineTrimLeading = false
			}
		}
		return state.emitText(raw)
	}
}

func (state *StreamState) closeMessagePart() []Event {
	message := state.message
	if message == nil || message.PartKind == "" {
		return nil
	}
	text, index := message.PartText.String(), len(message.Parts)
	part := map[string]any{"type": "refusal", "refusal": text}
	eventType, field := "response.refusal.done", "refusal"
	if message.PartKind == "output_text" {
		part = map[string]any{"type": "output_text", "text": text, "annotations": []any{}}
		eventType, field = "response.output_text.done", "text"
	}
	message.Parts = append(message.Parts, part)
	message.PartKind = ""
	message.PartText.Reset()
	return []Event{
		event(eventType, map[string]any{
			"item_id": message.ItemID, "output_index": message.OutputIndex,
			"content_index": index, field: text,
		}),
		event("response.content_part.done", map[string]any{
			"item_id": message.ItemID, "output_index": message.OutputIndex, "content_index": index, "part": part,
		}),
	}
}

func (state *StreamState) closeMessage() []Event {
	if state.message == nil || state.message.Done {
		return nil
	}
	events := state.closeMessagePart()
	state.message.Done = true
	item := map[string]any{
		"id": state.message.ItemID, "type": "message", "status": "completed", "role": "assistant", "content": state.message.Parts,
	}
	if len(state.message.Parts) > 0 {
		state.addOutput(state.message.OutputIndex, item)
	}
	return append(events, event("response.output_item.done", map[string]any{"output_index": state.message.OutputIndex, "item": item}))
}

func (state *StreamState) ensureTool(raw map[string]any) *toolState {
	index := -1
	if value, found := raw["index"]; found {
		index = int(intValue(value))
	}
	callID := jsonx.String(raw["id"])
	if index < 0 && callID != "" {
		for key, current := range state.tools {
			if current.CallID == callID {
				index = key
				break
			}
		}
	}
	if index < 0 && callID != "" {
		index = len(state.tools)
		for state.tools[index] != nil {
			index++
		}
	}
	if index < 0 {
		if state.lastToolIndex >= 0 {
			index = state.lastToolIndex
		} else {
			index = 0
		}
	}
	state.lastToolIndex = index
	current := state.tools[index]
	if current == nil {
		current = &toolState{Key: index, OutputIndex: state.nextOutputIndex, CallID: callID}
		state.nextOutputIndex++
		state.tools[index] = current
	}
	if callID != "" {
		current.CallID = callID
	}
	if function := jsonx.Map(raw["function"]); function != nil {
		if name := jsonx.String(function["name"]); name != "" {
			current.ChatName = name
		}
	}
	return current
}

func (state *StreamState) pushToolCall(raw map[string]any) []Event {
	if function := jsonx.Map(raw["function"]); state.context.isProviderTool(jsonx.String(function["name"])) {
		// Provider-executed tools (web search and friends) run inside the
		// gateway; surfacing them would hand the client a tool it never declared.
		return nil
	}
	current := state.ensureTool(raw)
	function := jsonx.Map(raw["function"])
	argumentDelta := jsonx.String(function["arguments"])
	current.Arguments.WriteString(argumentDelta)
	bufferShell := state.context != nil && state.context.enforcesShell(current.ChatName)
	events := []Event{}
	if !current.Added && current.ChatName != "" {
		base := state.context.responseOutputItemFromTool(map[string]any{
			"id":       current.CallID,
			"function": map[string]any{"name": current.ChatName, "arguments": ""},
		}, "in_progress")
		current.ItemID = jsonx.String(base["id"])
		current.Kind = jsonx.String(base["type"])
		current.Added = true
		events = append(events, state.closeReasoning()...)
		events = append(events, state.closeMessage()...)
		events = append(events, event("response.output_item.added", map[string]any{
			"output_index": current.OutputIndex, "item": base,
		}))
		if !bufferShell && streamsFunctionArguments(current.Kind) && current.Arguments.Len() > 0 {
			events = append(events, event("response.function_call_arguments.delta", map[string]any{
				"item_id": current.ItemID, "output_index": current.OutputIndex, "delta": current.Arguments.String(),
			}))
		}
	} else if !bufferShell && current.Added && streamsFunctionArguments(current.Kind) && argumentDelta != "" {
		events = append(events, event("response.function_call_arguments.delta", map[string]any{
			"item_id": current.ItemID, "output_index": current.OutputIndex, "delta": argumentDelta,
		}))
	}
	return events
}

func (state *StreamState) closeTools() []Event {
	keys := make([]int, 0, len(state.tools))
	for key := range state.tools {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	events := []Event{}
	for _, key := range keys {
		current := state.tools[key]
		if current.Done {
			continue
		}
		current.Done = true
		if !current.Added || current.ChatName == "" {
			state.droppedTools++
			continue
		}
		bufferShell := state.context != nil && state.context.enforcesShell(current.ChatName)
		arguments, valid := canonicalToolArguments(current.Arguments.String())
		if !valid {
			state.droppedTools++
			continue
		}
		if bufferShell {
			if rewritten, ok := state.context.rewriteToolShell(current.ChatName, arguments); ok {
				arguments = rewritten
			}
		}
		base := state.context.responseOutputItemFromTool(map[string]any{
			"id":       current.CallID,
			"function": map[string]any{"name": current.ChatName, "arguments": arguments},
		}, "completed")
		base["id"] = current.ItemID
		switch jsonx.String(base["type"]) {
		case "custom_tool_call":
			events = append(events,
				event("response.custom_tool_call_input.delta", map[string]any{
					"item_id": jsonx.String(base["id"]), "output_index": current.OutputIndex, "delta": base["input"],
				}),
				event("response.custom_tool_call_input.done", map[string]any{
					"item_id": jsonx.String(base["id"]), "output_index": current.OutputIndex, "input": base["input"],
				}),
			)
		case "function_call", "tool_search_call":
			if bufferShell {
				events = append(events, event("response.function_call_arguments.delta", map[string]any{
					"item_id": jsonx.String(base["id"]), "output_index": current.OutputIndex,
					"delta": toolArgumentsJSON(base["arguments"]),
				}))
			}
			events = append(events, event("response.function_call_arguments.done", map[string]any{
				"item_id": jsonx.String(base["id"]), "output_index": current.OutputIndex,
				"arguments": toolArgumentsJSON(base["arguments"]),
			}))
		}
		events = append(events, event("response.output_item.done", map[string]any{
			"output_index": current.OutputIndex, "item": base,
		}))
		state.addOutput(current.OutputIndex, base)
	}
	return events
}

func streamsFunctionArguments(kind string) bool {
	return kind == "function_call" || kind == "tool_search_call"
}

func toolArgumentsJSON(value any) string {
	if text, ok := value.(string); ok {
		if strings.TrimSpace(text) == "" {
			return "{}"
		}
		return text
	}
	raw, err := json.Marshal(value)
	if err != nil || string(raw) == "null" {
		return "{}"
	}
	return string(raw)
}

func canonicalToolArguments(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "{}", true
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(trimmed)); err != nil {
		return "", false
	}
	return compact.String(), true
}

func (state *StreamState) HandleChunk(chunk map[string]any) []Event {
	if chunk == nil || state.completed {
		return nil
	}
	if rawError := chunk["error"]; rawError != nil {
		details, _ := apierr.FromBody(chunk, 0)
		if details.Message == "" {
			raw, _ := json.Marshal(rawError)
			details.Message = string(raw)
		}
		code := apierr.StringValue(details.Code)
		if code == "" {
			code = "upstream_error"
		}
		return state.failWithDetails(details.Message, code, details.Type)
	}
	if !state.identityLocked {
		if id := jsonx.String(chunk["id"]); id != "" {
			state.responseID = responseIDFromChatID(id)
		}
		// Keep the client-requested model. ChatGPT Desktop / Codex drops
		// thinking blocks when the streamed model name does not match.
		if createdAt := intValue(chunk["created"]); createdAt != 0 {
			state.createdAt = createdAt
		}
		if jsonx.String(chunk["id"]) != "" || jsonx.String(chunk["model"]) != "" || len(jsonx.Slice(chunk["choices"])) > 0 {
			state.identityLocked = true
		}
	}
	if chunk["usage"] != nil {
		state.usage = usageToResponses(chunk["usage"])
	}
	choices := jsonx.Slice(chunk["choices"])
	if len(choices) == 0 {
		return nil
	}
	choice := jsonx.Map(choices[0])
	if choice == nil {
		return nil
	}
	events := state.ensureStarted()
	delta := jsonx.Map(choice["delta"])
	if delta != nil {
		if reasoning := reasoningDeltaText(delta); reasoning != "" {
			events = append(events, state.pushReasoning(reasoning)...)
		}
		if content, ok := delta["content"].(string); ok {
			events = append(events, state.pushContent(content)...)
		} else {
			for _, raw := range jsonx.Slice(delta["content"]) {
				part := jsonx.Map(raw)
				if part["type"] == "refusal" {
					events = append(events, state.pushRefusal(jsonx.String(part["refusal"]))...)
				} else {
					events = append(events, state.pushContent(jsonx.String(part["text"]))...)
				}
			}
		}
		if refusal := jsonx.String(delta["refusal"]); refusal != "" {
			events = append(events, state.pushRefusal(refusal)...)
		}
		if toolCalls := jsonx.Slice(delta["tool_calls"]); len(toolCalls) > 0 {
			events = append(events, state.flushInlineThink()...)
			for _, raw := range toolCalls {
				if toolCall := jsonx.Map(raw); toolCall != nil {
					events = append(events, state.pushToolCall(toolCall)...)
				}
			}
		}
	}
	if finishReason := jsonx.String(choice["finish_reason"]); finishReason != "" {
		state.finishReason = finishReason
	}
	return events
}

// reasoningDeltaText is the streaming variant of extractReasoningFieldText.
// A whitespace-only delta is a real paragraph break inside the thinking text
// and must not be dropped just because it trims to nothing.
func reasoningDeltaText(delta map[string]any) string {
	for _, key := range []string{"reasoning_content", "reasoning"} {
		if raw := jsonx.String(delta[key]); raw != "" {
			return raw
		}
	}
	if reasoning := jsonx.Map(delta["reasoning"]); reasoning != nil {
		for _, key := range []string{"content", "text", "summary"} {
			if raw := jsonx.String(reasoning[key]); raw != "" {
				return raw
			}
		}
	}
	return extractReasoningDetailsText(delta["reasoning_details"])
}

func (state *StreamState) Fail(message, code string) []Event {
	return state.failWithDetails(message, code, "upstream_error")
}

func (state *StreamState) failWithDetails(message, code, errorType string) []Event {
	if state.completed {
		return nil
	}
	state.completed = true
	if code == "" {
		code = "upstream_error"
	}
	if errorType == "" {
		errorType = "upstream_error"
	}
	responseError := map[string]any{"code": code, "message": message, "type": errorType}
	// OpenAI always opens the lifecycle before reporting failure, even when
	// the very first upstream chunk is an error.
	events := state.ensureStarted()
	response := state.baseResponse("failed", responseError, "")
	return append(events, event("response.failed", map[string]any{"response": response}))
}

func (state *StreamState) Finalize(sawDone bool, readErr error) []Event {
	if state.completed {
		return nil
	}
	if readErr != nil {
		return state.Fail(readErr.Error(), "stream_error")
	}
	events := state.flushInlineThink()
	events = append(events, state.closeReasoning()...)
	events = append(events, state.closeMessage()...)
	events = append(events, state.closeTools()...)

	if state.droppedTools > 0 {
		return append(events, state.Fail(
			fmt.Sprintf("upstream returned %d structurally incomplete tool call(s)", state.droppedTools),
			"upstream_tool_call_dropped",
		)...)
	}

	hasMessage := false
	hasToolCall := false
	for _, entry := range state.output {
		switch jsonx.String(entry.item["type"]) {
		case "message":
			if strings.TrimSpace(textFromParts(entry.item["content"])) != "" {
				hasMessage = true
			}
		case "function_call", "custom_tool_call", "tool_search_call":
			hasToolCall = true
		}
	}

	switch state.finishReason {
	case "length":
		return state.finish(events, "incomplete", "max_output_tokens")
	case "content_filter":
		return state.finish(events, "incomplete", "content_filter")
	case "tool_calls", "function_call":
		if hasToolCall {
			return state.finish(events, "completed", "")
		}
		return append(events, state.Fail(
			"upstream finish_reason=tool_calls did not include a complete tool call",
			"upstream_tool_call_missing",
		)...)
	case "stop":
		if hasMessage || hasToolCall {
			return state.finish(events, "completed", "")
		}
		return append(events, state.Fail(
			"upstream finish_reason=stop did not include a final output message or complete tool call",
			"upstream_final_output_missing",
		)...)
	case "":
		// [DONE] is a transport sentinel. Some compatible upstreams omit
		// finish_reason but still close cleanly with it, so preserve that
		// compatibility. A bare EOF with partial output is ambiguous and must
		// fail instead of silently committing a truncated turn.
		if sawDone && (hasMessage || hasToolCall) {
			return state.finish(events, "completed", "")
		}
		message := "upstream Chat Completions stream ended without finish_reason"
		if hasMessage || hasToolCall {
			message = "upstream Chat Completions stream ended after partial output but before sending finish_reason"
		}
		return append(events, state.Fail(message, "stream_truncated")...)
	default:
		return append(events, state.Fail(
			"upstream returned unknown finish_reason="+state.finishReason,
			"upstream_finish_reason_unknown",
		)...)
	}
}

func (state *StreamState) finish(events []Event, status, incompleteReason string) []Event {
	if status == "completed" {
		if err := state.validateStructuredOutput(); err != nil {
			return append(events, state.Fail(err.Error(), "upstream_schema_validation_failed")...)
		}
	}
	state.completed = true
	response := state.baseResponse(status, nil, incompleteReason)
	eventType := "response.completed"
	if status == "incomplete" {
		eventType = "response.incomplete"
	}
	return append(events, event(eventType, map[string]any{"response": response}))
}

type StreamAdapter struct {
	state  *StreamState
	parser sse.Parser
	done   bool
}

func NewStreamAdapter(context *Context) *StreamAdapter {
	return &StreamAdapter{state: NewStreamState(context)}
}

func (adapter *StreamAdapter) Completed() bool {
	return adapter.done
}

func parseSSEData(block string) string {
	parts := make([]string, 0, 2)
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "data:") {
			parts = append(parts, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	return strings.Join(parts, "\n")
}

func (adapter *StreamAdapter) handleBlock(block string) []Event {
	data := parseSSEData(block)
	if data == "" {
		return nil
	}
	if data == "[DONE]" {
		adapter.done = true
		return adapter.state.Finalize(true, nil)
	}
	var chunk map[string]any
	if err := json.Unmarshal([]byte(data), &chunk); err != nil || chunk == nil {
		adapter.done = true
		return adapter.state.Fail("upstream returned malformed JSON in an SSE data event", "stream_invalid_json")
	}
	events := adapter.state.HandleChunk(chunk)
	if adapter.state.completed {
		adapter.done = true
	}
	return events
}

func (adapter *StreamAdapter) Feed(data []byte) []Event {
	if adapter.done || len(data) == 0 {
		return nil
	}
	events := []Event{}
	err := adapter.parser.Feed(data, func(block string) bool {
		events = append(events, adapter.handleBlock(block)...)
		return !adapter.done
	})
	if err != nil {
		adapter.done = true
		events = append(events, adapter.state.Fail(err.Error(), "stream_event_too_large")...)
	}
	return events
}

func (adapter *StreamAdapter) Finish(readErr error) []Event {
	if adapter.done {
		return nil
	}
	events := []Event{}
	if remaining := adapter.parser.Finish(); strings.TrimSpace(remaining) != "" {
		events = append(events, adapter.handleBlock(remaining)...)
	}
	if adapter.done {
		return events
	}
	adapter.done = true
	return append(events, adapter.state.Finalize(false, readErr)...)
}
