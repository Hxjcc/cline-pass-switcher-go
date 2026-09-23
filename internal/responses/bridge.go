package responses

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	toolSearchName       = "tool_search"
	customToolInputKey   = "input"
	toolMediaPlaceholder = "[tool media moved to the following user message]"
	// Some Chat providers reject a tool message whose content is an empty
	// string; the placeholder keeps the call/output pairing intact.
	toolEmptyOutputPlaceholder = "(no output)"
)

// Context keeps the parts of a Responses request needed to translate the
// upstream Chat Completions response back to the Responses wire format.
type Context struct {
	Model                    string
	Instructions             any
	ResponseTools            []any
	ResponseToolChoice       any
	ResponseText             any
	Reasoning                any
	MaxOutputTokens          any
	ParallelToolCalls        bool
	Temperature              any
	TopP                     any
	Metadata                 map[string]any
	RequestedReasoningEffort string
	MappedReasoningEffort    string
	RawReasoning             bool
	// webSearchTool and webFetchTool are gateway provider tools (for example
	// vercel:exa_search and vercel:browserbase_fetch) that stand in for the
	// client's hosted web_search and for reading a link the user pasted.
	// Empty keeps the capability off.
	webSearchTool string
	webFetchTool  string
	// compactionRecent is the verbatim tail embedded in compaction items; it
	// survives the summary so exact paths, commands and errors are not lost.
	compactionRecent []CompactionRecentMessage
	// shellCompat restricts forwarded tool schemas that declare a "shell"
	// parameter to this value and marks the parameter required. Empty keeps
	// the client's own schema untouched.
	shellCompat        string
	shellCompatEnforce bool
	providerTools      map[string]struct{}
	bindings           map[string]toolBinding
	originalToChat     map[string]string
	chatTools          []any
	toolNames          map[string]struct{}
	compactionUsers    []any
	outputSchema       *jsonschema.Schema
	// InputTokenCap is the model's context window. Reported input tokens are
	// clamped to it so a summed upstream usage cannot look larger than the
	// window. Zero leaves the number uncapped.
	InputTokenCap int64
}

// Options controls optional Responses to Chat compatibility behavior.
type Options struct {
	ReplayReasoning  bool
	ReasoningEfforts []string
	RawReasoning     bool
	// StrictToolHistory rejects a request whose tool history does not map onto
	// Chat Completions exactly. It is off by default: ChatGPT Desktop replays
	// results of calls that are not part of the request (delegation hand-offs,
	// pruned history, host-side tools), and those become user text instead of
	// failing the turn.
	StrictToolHistory bool
	// WebSearchUpstream maps the client's hosted web_search declaration onto a
	// tool the upstream gateway executes itself (vercel:exa_search and friends).
	// Empty keeps the hosted tool unsupported, which is the default.
	WebSearchUpstream string
	// WebFetchUpstream declares a gateway tool that reads a URL the user pasted
	// (vercel:browserbase_fetch). It is only declared when the request carries a
	// link in user-authored text.
	WebFetchUpstream string
	// RecentCompactionTokens is the verbatim tail (estimated tokens) kept inside
	// compaction items. Zero disables it: the item then carries the summary only.
	RecentCompactionTokens int
	// CompactionReasoningEffort is the level compaction turns run at, or "auto"
	// to pick the one closest to high.
	CompactionReasoningEffort string
	// ShellCompat restricts forwarded tool schemas that declare a "shell"
	// parameter to this value (for example "powershell") and marks it
	// required, so Windows clients stop falling back to cmd.exe when a model
	// omits the parameter. Empty or "off" leaves schemas untouched.
	ShellCompat string
	// ShellCompatEnforce also rewrites complete tool-call arguments for
	// shell-capable tools. It only takes effect when ShellCompat is set.
	ShellCompatEnforce bool
}

// ToChat converts an OpenAI Responses request into the Chat Completions shape
// understood by Cline Pass. Image-bearing tool outputs are intentionally moved
// into a following user message: Chat tool messages are text-only on several
// Cline upstreams, and all tool results must be completed before a user message.
func ToChat(body map[string]any) (map[string]any, *Context, error) {
	modelID := ""
	if body != nil {
		modelID = jsonx.String(body["model"])
	}
	return ToChatWithOptions(body, Options{
		ReplayReasoning: true,
		RawReasoning:    ShouldUseRawReasoning(modelID),
	})
}

// ToChatWithOptions converts a Responses request using explicit compatibility
// switches.
func ToChatWithOptions(body map[string]any, options Options) (map[string]any, *Context, error) {
	if body == nil {
		return nil, nil, errors.New("invalid Responses request body")
	}
	modelID := strings.TrimSpace(jsonx.String(body["model"]))
	if modelID == "" {
		return nil, nil, errors.New("model is required")
	}
	// The proxy keeps no server-side state (every response is store:false),
	// so chaining on a previous response would silently drop the earlier
	// turns. Refusing is the only honest answer.
	if previous := strings.TrimSpace(jsonx.String(body["previous_response_id"])); previous != "" {
		return nil, nil, errors.New("previous_response_id is not supported by this proxy; send the full conversation in input")
	}
	if err := validateRequestCapabilities(body); err != nil {
		return nil, nil, err
	}
	responseTools := jsonx.Slice(body["tools"])
	if responseTools == nil {
		responseTools = []any{}
	}
	metadata := jsonx.Map(body["metadata"])
	if metadata == nil {
		metadata = map[string]any{}
	}
	context := &Context{
		Model:              modelID,
		Instructions:       body["instructions"],
		ResponseTools:      responseTools,
		ResponseToolChoice: body["tool_choice"],
		ResponseText:       body["text"],
		Reasoning:          body["reasoning"],
		MaxOutputTokens:    body["max_output_tokens"],
		ParallelToolCalls:  boolValue(body["parallel_tool_calls"], true),
		Temperature:        body["temperature"],
		TopP:               body["top_p"],
		Metadata:           metadata,
		RawReasoning:       options.RawReasoning,
		webSearchTool:      normaliseWebSearchTool(options.WebSearchUpstream),
		webFetchTool:       normaliseWebFetchTool(options.WebFetchUpstream),
		shellCompat:        normaliseShellCompat(options.ShellCompat),
		shellCompatEnforce: options.ShellCompatEnforce,
		providerTools:      map[string]struct{}{},
		bindings:           map[string]toolBinding{},
		originalToChat:     map[string]string{},
		chatTools:          []any{},
		toolNames:          map[string]struct{}{},
	}
	if context.ResponseText == nil {
		context.ResponseText = map[string]any{"format": map[string]any{"type": "text"}}
	}
	if context.ResponseToolChoice == nil {
		context.ResponseToolChoice = "auto"
	}
	compiledSchema, err := compileOutputSchema(context.ResponseText)
	if err != nil {
		return nil, nil, err
	}
	context.outputSchema = compiledSchema
	for _, tool := range responseTools {
		context.addResponseTool(tool, "")
	}
	context.collectDeclaredInputTools(body["input"], 0)
	if context.webFetchTool != "" && requestHasUserURL(body["input"]) {
		context.addProviderTool(context.webFetchTool)
	}
	if jsonx.String(context.ResponseToolChoice) == "required" && len(context.chatTools) == 0 {
		return nil, nil, unsupported("tool_choice", "required tool execution when no client-executable tools are available")
	}
	if jsonx.String(jsonx.Map(context.ResponseToolChoice)["type"]) == "tool_search" && context.bindings[toolSearchName].Kind != "tool_search" {
		return nil, nil, unsupported("tool_choice", "forced tool search without a client-executable tool_search declaration")
	}

	messages := make([]any, 0, 16)
	prefixMessages := make([]any, 0, 2)
	webSearchPolicy := context.webSearchPolicy()
	if instructions := body["instructions"]; instructions != nil {
		text := jsonx.String(instructions)
		if text == "" {
			raw, _ := json.Marshal(instructions)
			text = string(raw)
		}
		if webSearchPolicy != "" {
			text = strings.TrimSpace(text + "\n\n" + webSearchPolicy)
		}
		if text != "" {
			prefixMessages = append(prefixMessages, map[string]any{"role": "system", "content": text})
		}
	} else if webSearchPolicy != "" {
		prefixMessages = append(prefixMessages, map[string]any{"role": "system", "content": webSearchPolicy})
	}
	input := jsonx.Slice(body["input"])
	if input == nil {
		input = []any{map[string]any{"type": "message", "role": "user", "content": body["input"]}}
	}
	pendingToolCalls := make([]any, 0)
	pendingReasoning := ""
	compactionSummaries := make([]string, 0, 1)
	compactionRecent := make([]CompactionRecentMessage, 0, 4)
	lastAssistantIndex := -1
	appendReasoning := func(value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		if pendingReasoning == "" {
			pendingReasoning = value
			return
		}
		if !strings.Contains(pendingReasoning, value) {
			pendingReasoning += "\n\n" + value
		}
	}
	takeReasoning := func() string {
		value := pendingReasoning
		pendingReasoning = ""
		return value
	}
	attachReasoning := func(message map[string]any) {
		value := takeReasoning()
		if !options.ReplayReasoning || value == "" || message == nil {
			return
		}
		if existing, _ := message["reasoning_content"].(string); strings.TrimSpace(existing) != "" {
			if !strings.Contains(existing, value) {
				message["reasoning_content"] = existing + "\n\n" + value
			}
			return
		}
		message["reasoning_content"] = value
	}
	attachReasoningToLastAssistant := func() {
		value := takeReasoning()
		if !options.ReplayReasoning || value == "" || lastAssistantIndex < 0 || lastAssistantIndex >= len(messages) {
			return
		}
		message := jsonx.Map(messages[lastAssistantIndex])
		if message == nil || jsonx.String(message["role"]) != "assistant" {
			return
		}
		if existing, _ := message["reasoning_content"].(string); strings.TrimSpace(existing) != "" {
			if !strings.Contains(existing, value) {
				message["reasoning_content"] = existing + "\n\n" + value
			}
			return
		}
		message["reasoning_content"] = value
	}
	conversationStarted := func() bool {
		for _, raw := range messages {
			if jsonx.String(jsonx.Map(raw)["role"]) != "system" {
				return true
			}
		}
		return false
	}
	appendMessage := func(message map[string]any) {
		if message == nil {
			return
		}
		role := jsonx.String(message["role"])
		if role == "assistant" {
			attachReasoning(message)
			messages = append(messages, message)
			lastAssistantIndex = len(messages) - 1
			return
		}
		// Codex injects developer notices (<environment_context>,
		// <image_resize_notice>, ...) in the middle of the history. Only the
		// leading block can stay a system message: several Chat providers
		// reject or drop system turns that appear after user/assistant
		// turns, so later ones are carried as (already tagged) user content.
		if role == "system" && conversationStarted() {
			message["role"] = "user"
		}
		attachReasoningToLastAssistant()
		messages = append(messages, message)
	}
	type imageGroup struct {
		CallID string
		Images []any
	}
	pendingImages := make([]imageGroup, 0)
	unanswered := map[string]struct{}{}
	buffered := make([]map[string]any, 0)
	registerCalls := func(calls []any) {
		for _, raw := range calls {
			call := jsonx.Map(raw)
			id := strings.TrimSpace(jsonx.String(call["id"]))
			if id != "" {
				unanswered[id] = struct{}{}
			}
		}
	}
	toolGroupOpen := func() bool {
		return len(pendingToolCalls) > 0 || len(unanswered) > 0
	}
	flushToolCalls := func() {
		if len(pendingToolCalls) == 0 {
			return
		}
		calls := append([]any(nil), pendingToolCalls...)
		pendingToolCalls = pendingToolCalls[:0]
		registerCalls(calls)
		// A Responses turn may contain a commentary message followed by tool
		// calls. Chat models expect one assistant message with both content and
		// tool_calls; two consecutive assistant messages teach them to stop
		// after the commentary text.
		if len(messages) > 0 {
			last := jsonx.Map(messages[len(messages)-1])
			if last != nil && jsonx.String(last["role"]) == "assistant" && last["tool_calls"] == nil {
				last["tool_calls"] = calls
				attachReasoning(last)
				lastAssistantIndex = len(messages) - 1
				return
			}
		}
		message := map[string]any{"role": "assistant", "content": nil, "tool_calls": calls}
		attachReasoning(message)
		messages = append(messages, message)
		lastAssistantIndex = len(messages) - 1
	}
	flushImages := func() {
		if len(pendingImages) == 0 {
			return
		}
		attachReasoningToLastAssistant()
		content := []any{map[string]any{"type": "text", "text": "Images returned by the preceding tool call(s):"}}
		for _, group := range pendingImages {
			content = append(content, map[string]any{"type": "text", "text": "Image returned by tool call " + group.CallID + ":"})
			content = append(content, group.Images...)
		}
		pendingImages = pendingImages[:0]
		messages = append(messages, map[string]any{"role": "user", "content": content})
	}
	// Codex Desktop injects developer <image_resize_notice> messages between
	// function_call_output items of the same tool batch. Flushing images or
	// those notices early would insert a user/system turn while later call_ids
	// are still unanswered.
	flushAfterToolGroup := func() {
		if toolGroupOpen() {
			return
		}
		for _, message := range buffered {
			appendMessage(message)
		}
		buffered = nil
		flushImages()
	}
	queueOrAppend := func(message map[string]any) {
		if message == nil {
			return
		}
		if toolGroupOpen() {
			buffered = append(buffered, message)
			return
		}
		flushAfterToolGroup()
		appendMessage(message)
	}
	flushPending := func() {
		flushToolCalls()
		flushAfterToolGroup()
	}

	for _, rawItem := range input {
		if text, ok := rawItem.(string); ok {
			queueOrAppend(map[string]any{"role": "user", "content": text})
			continue
		}
		item := jsonx.Map(rawItem)
		if item == nil {
			continue
		}
		switch jsonx.String(item["type"]) {
		case "reasoning":
			appendReasoning(reasoningTextFromItem(item))
		case "function_call", "custom_tool_call":
			if !toolGroupOpen() {
				flushAfterToolGroup()
			}
			pendingToolCalls = append(pendingToolCalls, functionCallFromResponseItem(item, context))
		case "tool_search_call":
			if isHostedToolSearchItem(item) {
				continue
			}
			if !toolGroupOpen() {
				flushAfterToolGroup()
			}
			pendingToolCalls = append(pendingToolCalls, functionCallFromResponseItem(item, context))
		case "function_call_output", "custom_tool_call_output":
			flushToolCalls()
			callID := jsonx.String(item["call_id"])
			if callID == "" {
				callID = jsonx.String(item["id"])
			}
			parts := parseToolOutput(item["output"])
			text := strings.Join(parts.Text, "\n")
			if text == "" && len(parts.Images) > 0 {
				text = toolMediaPlaceholder
			}
			if text == "" {
				text = toolEmptyOutputPlaceholder
			}
			if _, answered := unanswered[strings.TrimSpace(callID)]; !answered && !options.StrictToolHistory {
				queueOrAppend(orphanToolOutputMessage(item, text, parts.Images))
				break
			}
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": callID, "content": text})
			delete(unanswered, strings.TrimSpace(callID))
			if len(parts.Images) > 0 {
				pendingImages = append(pendingImages, imageGroup{CallID: callID, Images: parts.Images})
			}
			flushAfterToolGroup()
		case "tool_search_output":
			if isHostedToolSearchItem(item) {
				continue
			}
			flushToolCalls()
			callID := jsonx.String(item["call_id"])
			if callID == "" {
				callID = jsonx.String(item["id"])
			}
			content := toolSearchOutputContent(item)
			if _, answered := unanswered[strings.TrimSpace(callID)]; !answered && !options.StrictToolHistory {
				queueOrAppend(orphanToolOutputMessage(item, content, nil))
				break
			}
			messages = append(messages, map[string]any{
				"role": "tool", "tool_call_id": callID, "content": content,
			})
			delete(unanswered, strings.TrimSpace(callID))
			flushAfterToolGroup()
		case "compaction":
			if payload, ok := compactionPayloadFromEnvelope(jsonx.String(item["encrypted_content"])); ok {
				compactionSummaries = append(compactionSummaries, payload.Summary)
				for _, message := range payload.Recent {
					role := strings.TrimSpace(message.Role)
					text := strings.TrimSpace(message.Text)
					if (role != "user" && role != "assistant") || text == "" {
						continue
					}
					compactionRecent = append(compactionRecent, CompactionRecentMessage{Role: role, Text: text})
				}
			} else {
				compactionSummaries = append(compactionSummaries, "Earlier conversation was compacted, but its details are not readable by this provider.")
			}
		case "additional_tools":
			// Responses Lite carries dynamic tool declarations here; they are
			// not Chat messages and must not be replayed as user text.
		case "compaction_trigger":
			// Remote compaction v2 marker: the request itself is handled by the
			// compaction path, and the marker must never reach the model as a
			// message.
		default:
			if item["role"] != nil || jsonx.String(item["type"]) == "message" || item["type"] == nil {
				queueOrAppend(messageFromResponseItem(item))
			} else if typeName := jsonx.String(item["type"]); typeName == "input_text" || typeName == "input_image" || typeName == "input_file" || typeName == "input_audio" {
				queueOrAppend(messageFromResponseItem(map[string]any{"role": "user", "content": []any{item}}))
			} else {
				raw, _ := json.Marshal(item)
				queueOrAppend(map[string]any{"role": "user", "content": string(raw)})
			}
		}
	}
	flushPending()
	if options.ReplayReasoning {
		attachReasoningToLastAssistant()
	} else {
		pendingReasoning = ""
	}
	if len(compactionSummaries) > 0 {
		prefixMessages = append(prefixMessages, map[string]any{
			"role":    "system",
			"content": "Earlier conversation was compacted. Summary:\n" + strings.Join(compactionSummaries, "\n\n"),
		})
	}
	// Replay the verbatim tail right after the summary so the next model sees
	// the exact recent turns before the new user message.
	for _, message := range compactionRecent {
		prefixMessages = append(prefixMessages, map[string]any{
			"role": message.Role, "content": message.Text,
		})
	}
	messages = append(prefixMessages, messages...)
	if err := validateChatToolHistory(messages); err != nil {
		return nil, nil, err
	}

	chat := map[string]any{"model": modelID, "messages": messages}
	if len(context.chatTools) > 0 {
		chat["tools"] = context.chatTools
	}
	if choice := context.toolChoiceToChat(context.ResponseToolChoice); choice != nil {
		chat["tool_choice"] = choice
	}
	if _, found := body["parallel_tool_calls"]; found {
		chat["parallel_tool_calls"] = context.ParallelToolCalls
	}
	if context.Temperature != nil {
		chat["temperature"] = context.Temperature
	}
	if context.TopP != nil {
		chat["top_p"] = context.TopP
	}
	if context.MaxOutputTokens != nil {
		chat["max_tokens"] = context.MaxOutputTokens
	}
	// Chat Completions accepts the same cache hint; passing it through lets
	// prompt-caching upstreams keep routing a conversation to a warm cache.
	if cacheKey := strings.TrimSpace(jsonx.String(body["prompt_cache_key"])); cacheKey != "" {
		chat["prompt_cache_key"] = cacheKey
	}
	reasoning := jsonx.Map(body["reasoning"])
	rawEffort := jsonx.String(reasoning["effort"])
	context.RequestedReasoningEffort = strings.ToLower(strings.TrimSpace(rawEffort))
	effort := mapReasoningEffort(rawEffort, options.ReasoningEfforts)
	disabled := reasoningEffortDisabled(rawEffort) || reasoningEffortDisabled(effort)
	if effort != "" {
		chat["reasoning_effort"] = effort
		context.MappedReasoningEffort = effort
		if !disabled {
			chat["reasoning"] = map[string]any{"effort": effort}
		}
	}
	// Cline/OpenRouter omit thinking tokens unless asked. ChatGPT Desktop
	// shows an empty "thinking" spinner without include_reasoning.
	if (reasoning != nil || effort != "") && !disabled {
		if exclude, ok := reasoning["exclude"].(bool); !ok || !exclude {
			chat["include_reasoning"] = true
		}
	}
	if stream, _ := body["stream"].(bool); stream {
		chat["stream"] = true
		chat["stream_options"] = map[string]any{"include_usage": true}
	}
	// Strict OpenAI-compatible upstreams reject tool_choice or
	// parallel_tool_calls when the converted request has no tool definitions.
	if len(context.chatTools) == 0 {
		delete(chat, "tool_choice")
		delete(chat, "parallel_tool_calls")
	}
	if text := jsonx.Map(body["text"]); text != nil {
		if format := jsonx.Map(text["format"]); format != nil {
			switch jsonx.String(format["type"]) {
			case "json_schema":
				if format["schema"] != nil {
					name := jsonx.String(format["name"])
					if name == "" {
						name = "response"
					}
					chat["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{
						"name": name, "schema": format["schema"], "strict": boolValue(format["strict"], true),
					}}
				}
			case "json_object":
				chat["response_format"] = map[string]any{"type": "json_object"}
			}
		}
	}
	return chat, context, nil
}

func validateChatToolHistory(messages []any) error {
	awaiting := map[string]struct{}{}
	missing := func(before string) error {
		ids := make([]string, 0, len(awaiting))
		for id := range awaiting {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		if before != "" {
			return fmt.Errorf("tool output history is incomplete before role=%s: missing outputs for %s", before, strings.Join(ids, ","))
		}
		return fmt.Errorf("tool output history is incomplete: missing outputs for %s", strings.Join(ids, ","))
	}

	for _, raw := range messages {
		message := jsonx.Map(raw)
		if message == nil {
			continue
		}
		role := jsonx.String(message["role"])
		if role == "tool" {
			callID := strings.TrimSpace(jsonx.String(message["tool_call_id"]))
			if callID == "" {
				return errors.New("tool output history is incomplete: tool_call_id is missing")
			}
			if _, found := awaiting[callID]; !found {
				return fmt.Errorf("tool output history is incomplete: orphan tool output call_id=%s", callID)
			}
			delete(awaiting, callID)
			continue
		}
		if len(awaiting) > 0 {
			return missing(role)
		}
		if role != "assistant" {
			continue
		}
		for _, rawCall := range jsonx.Slice(message["tool_calls"]) {
			call := jsonx.Map(rawCall)
			callID := strings.TrimSpace(jsonx.String(call["id"]))
			if callID == "" {
				return errors.New("tool output history is incomplete: assistant tool call id is missing")
			}
			if _, found := awaiting[callID]; found {
				return fmt.Errorf("tool output history is incomplete: duplicate assistant tool call id=%s", callID)
			}
			awaiting[callID] = struct{}{}
		}
	}
	if len(awaiting) > 0 {
		return missing("")
	}
	return nil
}

func parseArgumentsObject(value string) any {
	if strings.TrimSpace(value) == "" {
		return map[string]any{}
	}
	var result any
	if json.Unmarshal([]byte(value), &result) == nil {
		return result
	}
	return map[string]any{}
}

func customInputFromArguments(value string) string {
	var parsed any
	if json.Unmarshal([]byte(value), &parsed) == nil {
		if object, ok := parsed.(map[string]any); ok {
			if input := jsonx.String(object[customToolInputKey]); input != "" {
				return input
			}
		}
		if text, ok := parsed.(string); ok {
			return text
		}
	}
	return value
}
