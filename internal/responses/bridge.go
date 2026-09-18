package responses

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

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

var invalidToolName = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

const compactionEnvelopePrefix = "ocx1:"

type toolBinding struct {
	Kind      string
	Name      string
	Namespace string
}

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
	bindings                 map[string]toolBinding
	originalToChat           map[string]string
	chatTools                []any
	toolNames                map[string]struct{}
	compactionUsers          []any
	outputSchema             *jsonschema.Schema
}

func boolValue(value any, fallback bool) bool {
	if result, ok := value.(bool); ok {
		return result
	}
	return fallback
}

func normalizedImageDetail(value any) string {
	detail := jsonx.String(value)
	if detail == "original" {
		return "high"
	}
	switch detail {
	case "auto", "low", "high":
		return detail
	default:
		return ""
	}
}

func safeToolName(value string) string {
	value = invalidToolName.ReplaceAllString(strings.TrimSpace(value), "_")
	value = strings.Trim(value, "_")
	if value == "" {
		value = "tool"
	}
	if len(value) > 64 {
		value = value[:64]
	}
	return value
}

func originalToolKey(namespace, name string) string {
	return namespace + "\x00" + name
}

func (context *Context) allocateToolName(namespace, name string) string {
	base := name
	if namespace != "" {
		base = namespace + "__" + name
	}
	base = safeToolName(base)
	candidate := base
	for index := 2; ; index++ {
		if _, found := context.toolNames[candidate]; !found {
			context.toolNames[candidate] = struct{}{}
			context.originalToChat[originalToolKey(namespace, name)] = candidate
			if namespace == "" {
				context.originalToChat[name] = candidate
			}
			return candidate
		}
		suffix := fmt.Sprintf("_%d", index)
		limit := 64 - len(suffix)
		if limit < 1 {
			limit = 1
		}
		trimmed := base
		if len(trimmed) > limit {
			trimmed = trimmed[:limit]
		}
		candidate = trimmed + suffix
	}
}

func functionTool(name, description string, parameters any, strict any) map[string]any {
	if parameters == nil {
		parameters = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	function := map[string]any{
		"name":        name,
		"description": description,
		"parameters":  parameters,
	}
	if value, ok := strict.(bool); ok {
		function["strict"] = value
	}
	return map[string]any{"type": "function", "function": function}
}

func defaultToolSearchParameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "Search query for deferred tools."},
			"limit": map[string]any{"type": "integer", "minimum": 1, "description": "Maximum number of tools to return (defaults to 8)."},
		},
		"required":             []any{"query"},
		"additionalProperties": false,
	}
}

func isServerHostedToolType(typeName string) bool {
	switch typeName {
	case "web_search", "web_search_preview", "web_search_preview_2025_03_11", "file_search", "code_interpreter",
		"image_generation", "computer", "computer_use", "computer_use_preview", "mcp":
		return true
	default:
		return false
	}
}

// Clients can advertise hosted tools even for an ordinary text turn. These
// optional declarations are omitted from Chat; an explicit forced selection
// is validated separately rather than rejecting the entire tool inventory.
func isUnforwardedTool(tool map[string]any) bool {
	return isServerHostedToolType(jsonx.String(tool["type"])) ||
		(jsonx.String(tool["type"]) == "tool_search" && jsonx.String(tool["execution"]) == "server")
}

func (context *Context) alreadyBound(namespace, name string) bool {
	if context.originalToChat[originalToolKey(namespace, name)] != "" {
		return true
	}
	return namespace == "" && context.originalToChat[name] != ""
}

func toolDescriptionWithDefinition(tool map[string]any) string {
	description := jsonx.String(tool["description"])
	definition := map[string]any{
		"type":        tool["type"],
		"name":        tool["name"],
		"description": tool["description"],
		"format":      tool["format"],
	}
	raw, _ := json.Marshal(definition)
	return description + "\n\nOriginal Responses tool definition:\n```json\n" + string(raw) + "\n```"
}

func (context *Context) addResponseTool(value any, namespace string) {
	if text, ok := value.(string); ok {
		name := strings.TrimSpace(text)
		if name == "" {
			return
		}
		chatName := context.allocateToolName(namespace, name)
		context.bindings[chatName] = toolBinding{Kind: "custom", Name: name, Namespace: namespace}
		context.chatTools = append(context.chatTools, functionTool(chatName, "Codex custom tool", map[string]any{
			"type":       "object",
			"properties": map[string]any{customToolInputKey: map[string]any{"type": "string"}},
			"required":   []any{customToolInputKey},
		}, nil))
		return
	}

	tool := jsonx.Map(value)
	if tool == nil {
		return
	}
	if isUnforwardedTool(tool) {
		return
	}
	typeName := jsonx.String(tool["type"])
	if typeName == "namespace" {
		nextNamespace := jsonx.String(tool["name"])
		if nextNamespace == "" {
			nextNamespace = namespace
		}
		for _, child := range jsonx.Slice(tool["tools"]) {
			context.addResponseTool(child, nextNamespace)
		}
		return
	}
	if typeName == "tool_search" {
		if _, found := context.bindings[toolSearchName]; found {
			return
		}
		context.toolNames[toolSearchName] = struct{}{}
		context.bindings[toolSearchName] = toolBinding{Kind: "tool_search", Name: toolSearchName}
		context.originalToChat[toolSearchName] = toolSearchName
		description := jsonx.String(tool["description"])
		if description == "" {
			description = "Search and load Codex tools, plugins, connectors, and MCP namespaces."
		}
		parameters := tool["parameters"]
		if parameters == nil {
			parameters = defaultToolSearchParameters()
		}
		context.chatTools = append(context.chatTools, functionTool(toolSearchName, description, parameters, nil))
		return
	}
	name := jsonx.String(tool["name"])
	if name == "" {
		return
	}
	if context.alreadyBound(namespace, name) {
		return
	}
	chatName := context.allocateToolName(namespace, name)
	if typeName == "custom" {
		context.bindings[chatName] = toolBinding{Kind: "custom", Name: name, Namespace: namespace}
		context.chatTools = append(context.chatTools, functionTool(chatName, toolDescriptionWithDefinition(tool), map[string]any{
			"type":       "object",
			"properties": map[string]any{customToolInputKey: map[string]any{"type": "string"}},
			"required":   []any{customToolInputKey},
		}, nil))
		return
	}

	context.bindings[chatName] = toolBinding{Kind: "function", Name: name, Namespace: namespace}
	parameters := tool["parameters"]
	if parameters == nil {
		parameters = tool["input_schema"]
	}
	context.chatTools = append(context.chatTools, functionTool(chatName, jsonx.String(tool["description"]), parameters, tool["strict"]))
}

func (context *Context) collectDeclaredInputTools(value any, depth int) {
	if depth > 16 {
		return
	}
	switch typed := value.(type) {
	case []any:
		for _, child := range typed {
			context.collectDeclaredInputTools(child, depth+1)
		}
	case map[string]any:
		switch jsonx.String(typed["type"]) {
		case "additional_tools":
			tools := jsonx.Slice(typed["tools"])
			if len(context.ResponseTools) == 0 && len(tools) > 0 {
				context.ResponseTools = tools
			}
			for _, tool := range tools {
				context.addResponseTool(tool, "")
			}
		case "tool_search_output":
			for _, tool := range jsonx.Slice(typed["tools"]) {
				context.addResponseTool(tool, "")
			}
		}
		for _, child := range typed {
			context.collectDeclaredInputTools(child, depth+1)
		}
	}
}

func isHostedToolSearchItem(item map[string]any) bool {
	if jsonx.String(item["execution"]) == "server" {
		return true
	}
	return strings.TrimSpace(jsonx.String(item["call_id"])) == "" && jsonx.String(item["execution"]) != "client"
}

func toolSearchOutputContent(item map[string]any) string {
	if output := item["output"]; output != nil {
		parts := parseToolOutput(output)
		if text := strings.Join(parts.Text, "\n"); text != "" {
			return text
		}
	}
	if tools := item["tools"]; tools != nil {
		raw, err := json.Marshal(tools)
		if err == nil && strings.TrimSpace(string(raw)) != "" && string(raw) != "null" {
			return string(raw)
		}
	}
	return "[]"
}

func imageURLPart(part map[string]any) (map[string]any, bool) {
	typeName := jsonx.String(part["type"])
	if typeName != "input_image" && typeName != "image_url" && typeName != "image" && typeName != "output_image" {
		return nil, false
	}
	detail := normalizedImageDetail(part["detail"])
	var imageURL any
	if value := part["image_url"]; value != nil {
		imageURL = value
	} else if value := jsonx.String(part["url"]); value != "" {
		imageURL = value
	} else if value := jsonx.String(part["image"]); value != "" {
		imageURL = value
	} else if data := jsonx.String(part["data"]); data != "" {
		if strings.HasPrefix(data, "data:") {
			imageURL = data
		} else {
			mimeType := jsonx.String(part["mimeType"])
			if mimeType == "" {
				mimeType = jsonx.String(part["mime_type"])
			}
			if mimeType == "" {
				mimeType = "image/png"
			}
			imageURL = "data:" + mimeType + ";base64," + data
		}
	}
	if imageURL == nil {
		return nil, false
	}

	switch typed := imageURL.(type) {
	case string:
		if typed == "" {
			return nil, false
		}
		value := map[string]any{"url": typed}
		if detail != "" {
			value["detail"] = detail
		}
		return map[string]any{"type": "image_url", "image_url": value}, true
	case map[string]any:
		value := make(map[string]any, len(typed)+1)
		for key, child := range typed {
			value[key] = child
		}
		if detail != "" && value["detail"] == nil {
			value["detail"] = detail
		}
		if jsonx.String(value["url"]) == "" {
			return nil, false
		}
		return map[string]any{"type": "image_url", "image_url": value}, true
	default:
		return nil, false
	}
}

func chatContentFromResponseContent(content any) any {
	if text, ok := content.(string); ok {
		return text
	}
	items := jsonx.Slice(content)
	if items == nil {
		return ""
	}
	parts := make([]any, 0, len(items))
	allText := true
	for _, value := range items {
		part := jsonx.Map(value)
		if part == nil {
			continue
		}
		typeName := jsonx.String(part["type"])
		switch typeName {
		case "input_text", "output_text", "text":
			if text := jsonx.String(part["text"]); text != "" {
				parts = append(parts, map[string]any{"type": "text", "text": text})
			}
		case "refusal":
			if text := jsonx.String(part["refusal"]); text != "" {
				parts = append(parts, map[string]any{"type": "text", "text": text})
			}
		case "input_image", "image_url", "image", "output_image":
			if image, ok := imageURLPart(part); ok {
				parts = append(parts, image)
				allText = false
			}
		case "input_file":
			name := jsonx.String(part["filename"])
			if name == "" {
				name = jsonx.String(part["file_id"])
			}
			if name == "" {
				name = "attachment"
			}
			parts = append(parts, map[string]any{"type": "text", "text": "[file input omitted: " + name + "]"})
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if allText {
		var builder strings.Builder
		for _, value := range parts {
			builder.WriteString(jsonx.String(jsonx.Map(value)["text"]))
		}
		return builder.String()
	}
	return parts
}

type outputParts struct {
	Text   []string
	Images []any
}

func (parts *outputParts) addText(value string) {
	if value != "" {
		parts.Text = append(parts.Text, value)
	}
}

// Tool results are opaque data unless they explicitly contain protocol content
// blocks. A business object with a field named content/output/text is not a
// wrapper and must retain every field.
func isToolContent(value any, depth int) bool {
	if depth > 20 {
		return false
	}
	switch typed := value.(type) {
	case []any:
		if len(typed) == 0 {
			return false
		}
		for _, child := range typed {
			if !isToolContent(child, depth+1) {
				return false
			}
		}
		return true
	case map[string]any:
		if _, ok := imageURLPart(typed); ok {
			return true
		}
		switch jsonx.String(typed["type"]) {
		case "input_text", "output_text", "text":
			_, ok := typed["text"].(string)
			return ok
		}
		for _, key := range []string{"content", "output"} {
			if isToolContent(typed[key], depth+1) {
				return true
			}
		}
	}
	return false
}

func (parts *outputParts) addJSON(value any) {
	if raw, err := json.Marshal(value); err == nil {
		parts.addText(string(raw))
	}
}

func collectToolOutput(value any, parts *outputParts, depth int) {
	if value == nil {
		return
	}
	if depth > 20 {
		parts.addJSON(value)
		return
	}
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
			(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
			var decoded any
			decoder := json.NewDecoder(strings.NewReader(trimmed))
			decoder.UseNumber()
			if decoder.Decode(&decoded) == nil && json.Valid([]byte(trimmed)) && isToolContent(decoded, depth+1) {
				media := outputParts{}
				collectToolOutput(decoded, &media, depth+1)
				// Only decode a string when needed to deliver actual images.
				// Otherwise preserve its whitespace, JSON shape and number spelling.
				if len(media.Images) > 0 {
					parts.Text = append(parts.Text, media.Text...)
					parts.Images = append(parts.Images, media.Images...)
					return
				}
			}
		}
		parts.addText(typed)
	case []any:
		if !isToolContent(typed, depth) {
			parts.addJSON(typed)
			return
		}
		for _, child := range typed {
			collectToolOutput(child, parts, depth+1)
		}
	case map[string]any:
		if image, ok := imageURLPart(typed); ok {
			parts.Images = append(parts.Images, image)
			return
		}
		typeName := jsonx.String(typed["type"])
		if typeName == "input_text" || typeName == "output_text" || typeName == "text" {
			parts.addText(jsonx.String(typed["text"]))
			return
		}
		for _, key := range []string{"content", "output"} {
			if child := typed[key]; isToolContent(child, depth+1) {
				collectToolOutput(child, parts, depth+1)
				// MCP wrappers can carry isError, structuredContent, cursors,
				// and other data beside their content blocks.
				extra := make(map[string]any, len(typed)-1)
				for name, field := range typed {
					if name != key {
						extra[name] = field
					}
				}
				if len(extra) > 0 {
					parts.addJSON(extra)
				}
				return
			}
		}
		parts.addJSON(typed)
	default:
		parts.addJSON(typed)
	}
}

func parseToolOutput(value any) outputParts {
	parts := outputParts{}
	collectToolOutput(value, &parts, 0)
	return parts
}

func messageFromResponseItem(item map[string]any) map[string]any {
	role := jsonx.String(item["role"])
	if role == "developer" {
		role = "system"
	}
	switch role {
	case "system", "assistant", "user":
	default:
		role = "user"
	}
	message := map[string]any{"role": role, "content": chatContentFromResponseContent(item["content"])}
	if role == "assistant" {
		content := make([]any, 0)
		var refusal strings.Builder
		for _, raw := range jsonx.Slice(item["content"]) {
			part := jsonx.Map(raw)
			if part["type"] == "refusal" {
				refusal.WriteString(jsonx.String(part["refusal"]))
			} else {
				content = append(content, raw)
			}
		}
		if refusal.Len() > 0 {
			message["refusal"] = refusal.String()
			message["content"] = chatContentFromResponseContent(content)
			if len(content) == 0 {
				message["content"] = nil
			}
		}
	}
	return message
}

func (context *Context) chatToolName(name, namespace string) string {
	if value := context.originalToChat[originalToolKey(namespace, name)]; value != "" {
		return value
	}
	if value := context.originalToChat[name]; value != "" {
		return value
	}
	if namespace != "" {
		return safeToolName(namespace + "__" + name)
	}
	return safeToolName(name)
}

func functionCallFromResponseItem(item map[string]any, context *Context) map[string]any {
	callID := jsonx.String(item["call_id"])
	if callID == "" {
		callID = jsonx.String(item["id"])
	}
	typeName := jsonx.String(item["type"])
	name := jsonx.String(item["name"])
	namespace := jsonx.String(item["namespace"])
	chatName := context.chatToolName(name, namespace)
	arguments := jsonx.String(item["arguments"])
	if typeName == "custom_tool_call" {
		argumentsRaw, _ := json.Marshal(map[string]any{customToolInputKey: jsonx.String(item["input"])})
		arguments = string(argumentsRaw)
	} else if typeName == "tool_search_call" {
		chatName = toolSearchName
		if arguments == "" {
			argumentsRaw, _ := json.Marshal(item["arguments"])
			arguments = string(argumentsRaw)
		}
	}
	if arguments == "" || arguments == "null" {
		arguments = "{}"
	}
	return map[string]any{
		"id":   callID,
		"type": "function",
		"function": map[string]any{
			"name":      chatName,
			"arguments": arguments,
		},
	}
}

// orphanToolOutputMessage renders a tool result whose call is missing from the
// request as user-visible content. ChatGPT Desktop writes delegation hand-offs
// and host-side tool results into a thread without the matching call item, and
// Chat Completions cannot carry a tool message that follows no tool call.
func orphanToolOutputMessage(item map[string]any, text string, images []any) map[string]any {
	body := text
	if isDelegationToolOutput(item, text) {
		body = delegationInput(text)
	} else {
		name := strings.TrimSpace(jsonx.String(item["name"]))
		marker := "[tool result without a recorded call"
		if name != "" {
			marker += " " + name
		}
		marker += "]\n"
		body = marker + text
	}
	if len(images) == 0 {
		return map[string]any{"role": "user", "content": body}
	}
	parts := []any{map[string]any{"type": "text", "text": body}}
	parts = append(parts, images...)
	return map[string]any{"role": "user", "content": parts}
}

// isDelegationToolOutput recognizes the cross-task hand-off payload that the
// desktop client stores at the head of a delegated thread.
func isDelegationToolOutput(item map[string]any, text string) bool {
	if jsonx.String(item["name"]) == "create_thread" || jsonx.String(item["namespace"]) == "codex_app" {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(text), "<codex_delegation>")
}

// delegationInput unwraps the XML envelope so the model receives the delegated
// prompt as an ordinary user message; the envelope itself is client bookkeeping.
func delegationInput(text string) string {
	start := strings.Index(text, "<input>")
	end := strings.LastIndex(text, "</input>")
	if start < 0 || end <= start {
		return strings.TrimSpace(text)
	}
	inner := text[start+len("<input>") : end]
	if strings.Contains(inner, "&") {
		inner = strings.NewReplacer(
			"&lt;", "<", "&gt;", ">", "&apos;", "'", "&amp;", "&",
		).Replace(inner)
	}
	return strings.TrimSpace(inner)
}

func (context *Context) toolChoiceToChat(value any) any {
	if text, ok := value.(string); ok {
		switch text {
		case "auto", "none", "required":
			return text
		default:
			return nil
		}
	}
	choice := jsonx.Map(value)
	if choice == nil {
		return nil
	}
	name := jsonx.String(choice["name"])
	if name == "" {
		return nil
	}
	chatName := context.chatToolName(name, jsonx.String(choice["namespace"]))
	if jsonx.String(choice["type"]) == "tool_search" {
		chatName = toolSearchName
	}
	return map[string]any{"type": "function", "function": map[string]any{"name": chatName}}
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
}

// ShouldReplayReasoning reports whether reasoning_content can be replayed in
// assistant history for the model. Moonshot/Kimi Chat endpoints are known to
// reject or corrupt replayed reasoning, so they opt out.
func ShouldReplayReasoning(modelID string) bool {
	model := strings.ToLower(strings.TrimSpace(modelID))
	return !strings.Contains(model, "kimi") && !strings.Contains(model, "moonshot")
}

// ShouldUseRawReasoning reports whether the upstream exposes readable
// chain-of-thought that Codex Desktop can also render through the raw
// reasoning lifecycle. DeepSeek-family Cline models use this shape.
// ChatGPT Desktop still needs the summary events; those are always emitted.
func ShouldUseRawReasoning(modelID string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(modelID)), "deepseek")
}

func reasoningEffortDisabled(effort string) bool {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "none", "off", "disabled":
		return true
	default:
		return false
	}
}

func pickReasoningEffort(allowed []string, preference ...string) string {
	for _, wanted := range preference {
		for _, value := range allowed {
			if value == wanted {
				return value
			}
		}
	}
	return ""
}

func mapReasoningEffort(effort string, supported []string) string {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" {
		return ""
	}
	allowed := make([]string, 0, len(supported))
	for _, value := range supported {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			allowed = append(allowed, value)
		}
	}
	if len(allowed) == 0 {
		return effort
	}
	for _, value := range allowed {
		if value == effort {
			return effort
		}
	}
	switch effort {
	case "none", "off", "disabled":
		return pickReasoningEffort(allowed, "none")
	case "minimal":
		return pickReasoningEffort(allowed, "minimal", "low", "medium", "high", "max", "xhigh")
	case "low":
		return pickReasoningEffort(allowed, "low", "minimal", "medium", "high", "max", "xhigh")
	case "medium":
		// Prefer a stronger supported tier over a weaker one. DeepSeek's
		// none/low/high/max list has no medium, and dropping to low made
		// ChatGPT's middle/high slider stops look like "最低".
		return pickReasoningEffort(allowed, "medium", "high", "max", "xhigh", "low", "minimal")
	case "high":
		return pickReasoningEffort(allowed, "high", "max", "xhigh", "medium", "low", "minimal")
	case "xhigh", "max", "ultra":
		return pickReasoningEffort(allowed, "xhigh", "max", "ultra", "high", "medium", "low", "minimal")
	default:
		return ""
	}
}

// CompactionEvents preserves the proxy's streaming extension. The buffered
// endpoint returns response.compaction; SSE uses the normal Response lifecycle
// with retained user messages followed by the compaction item.
func CompactionEvents(compaction map[string]any, context *Context) []Event {
	response := context.responseBase(jsonx.String(compaction["id"]), intValue(compaction["created_at"]),
		context.Model, "completed", jsonx.Slice(compaction["output"]), compaction["usage"], nil, "")
	inProgress := context.responseBase(jsonx.String(compaction["id"]), intValue(compaction["created_at"]),
		context.Model, "in_progress", []any{}, nil, nil, "")
	events := []Event{
		event("response.created", map[string]any{"response": inProgress}),
		event("response.in_progress", map[string]any{"response": inProgress}),
	}
	for index, item := range jsonx.Slice(compaction["output"]) {
		events = append(events,
			event("response.output_item.added", map[string]any{"output_index": index, "item": item}),
			event("response.output_item.done", map[string]any{"output_index": index, "item": item}),
		)
	}
	return append(events, event("response.completed", map[string]any{"response": response}))
}

// CompactionEnvelope wraps a readable summary in the same opaque envelope shape
// used by Codex-compatible third-party compaction implementations.
func CompactionEnvelope(summary string) string {
	return compactionEnvelopePrefix + base64.StdEncoding.EncodeToString([]byte(summary))
}

func compactionSummaryFromEnvelope(value string) (string, bool) {
	encoded, found := strings.CutPrefix(value, compactionEnvelopePrefix)
	if !found {
		return "", false
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", false
	}
	summary := strings.TrimSpace(string(raw))
	return summary, summary != ""
}

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

func extractReasoningDetailsText(value any) string {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) != "" {
			return typed
		}
		return ""
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := extractReasoningDetailPartText(item); text != "" {
				parts = append(parts, text)
			}
		}
		joined := strings.Join(parts, "\n\n")
		if strings.TrimSpace(joined) != "" {
			return joined
		}
		return ""
	default:
		return extractReasoningDetailPartText(value)
	}
}

func extractReasoningDetailPartText(value any) string {
	object := jsonx.Map(value)
	if object == nil {
		return ""
	}
	for _, key := range []string{"text", "content", "summary"} {
		raw := jsonx.String(object[key])
		if strings.TrimSpace(raw) != "" {
			return raw
		}
	}
	if parts := object["parts"]; parts != nil {
		return extractReasoningDetailsText(parts)
	}
	return ""
}

// extractReasoningFieldText pulls readable reasoning from the various shapes
// used by OpenAI-compatible Chat upstreams.
func extractReasoningFieldText(value any) string {
	object := jsonx.Map(value)
	if object == nil {
		return ""
	}
	for _, key := range []string{"reasoning_content", "reasoning"} {
		raw := jsonx.String(object[key])
		if strings.TrimSpace(raw) != "" {
			return raw
		}
	}
	if reasoning := jsonx.Map(object["reasoning"]); reasoning != nil {
		for _, key := range []string{"content", "text", "summary"} {
			raw := jsonx.String(reasoning[key])
			if strings.TrimSpace(raw) != "" {
				return raw
			}
		}
	}
	return extractReasoningDetailsText(object["reasoning_details"])
}

// AliasChatReasoning copies Cline/OpenRouter `reasoning` onto OpenAI-style
// `reasoning_content` (and the reverse) so Chat Completions clients can render
// thinking without protocol conversion.
func AliasChatReasoning(chunk map[string]any) bool {
	if chunk == nil {
		return false
	}
	changed := false
	for _, raw := range jsonx.Slice(chunk["choices"]) {
		choice := jsonx.Map(raw)
		if choice == nil {
			continue
		}
		for _, key := range []string{"delta", "message"} {
			object := jsonx.Map(choice[key])
			if object == nil {
				continue
			}
			if aliasReasoningFields(object) {
				changed = true
			}
		}
	}
	return changed
}

func aliasReasoningFields(object map[string]any) bool {
	reasoningText := jsonx.String(object["reasoning"])
	if reasoningText == "" {
		if nested := jsonx.Map(object["reasoning"]); nested != nil {
			reasoningText = firstString(jsonx.String(nested["content"]), jsonx.String(nested["text"]), jsonx.String(nested["summary"]))
		}
	}
	if reasoningText == "" {
		reasoningText = extractReasoningDetailsText(object["reasoning_details"])
	}
	contentText := jsonx.String(object["reasoning_content"])
	changed := false
	if reasoningText != "" && strings.TrimSpace(contentText) == "" {
		object["reasoning_content"] = reasoningText
		changed = true
	}
	if contentText != "" && jsonx.String(object["reasoning"]) == "" && jsonx.Map(object["reasoning"]) == nil {
		object["reasoning"] = contentText
		changed = true
	}
	return changed
}

func extractReasoningSummaryText(value any) string {
	object := jsonx.Map(value)
	if object == nil {
		return ""
	}
	for _, key := range []string{"content", "summary"} {
		raw := textFromParts(object[key])
		if strings.TrimSpace(raw) != "" {
			return raw
		}
	}
	for _, key := range []string{"reasoning_content", "text"} {
		raw := jsonx.String(object[key])
		if strings.TrimSpace(raw) != "" {
			return raw
		}
	}
	return ""
}

// CompactionResponse converts a completed Chat Completions response into the
// Responses compaction shape expected by Codex. The readable summary is wrapped
// in a local envelope so later requests can restore it for third-party models.
func CompactionResponse(chat map[string]any, context *Context) (map[string]any, error) {
	response, err := FromChat(chat, context)
	if err != nil {
		return nil, err
	}
	if response["status"] != "completed" {
		reason := jsonx.String(jsonx.Map(response["incomplete_details"])["reason"])
		return nil, &ChatFailure{Code: "compaction_incomplete", Type: "upstream_error",
			Message: "upstream compaction summary is incomplete: " + reason, Response: response}
	}
	choice := jsonx.Map(jsonx.Slice(chat["choices"])[0])
	message := jsonx.Map(choice["message"])
	if jsonx.String(choice["finish_reason"]) != "stop" || len(jsonx.Slice(message["tool_calls"])) > 0 {
		return nil, errors.New("upstream compaction did not finish with a summary")
	}
	if jsonx.String(message["refusal"]) != "" {
		return nil, errors.New("upstream refused the compaction request")
	}
	for _, raw := range jsonx.Slice(message["content"]) {
		if jsonx.Map(raw)["type"] == "refusal" {
			return nil, errors.New("upstream refused the compaction request")
		}
	}
	summary := strings.TrimSpace(responseOutputText(response))
	if summary == "" {
		return nil, errors.New("upstream returned an empty compaction summary")
	}
	output := append([]any(nil), context.compactionUsers...)
	output = append(output, map[string]any{
		"id":                newID("cmp"),
		"type":              "compaction",
		"encrypted_content": CompactionEnvelope(summary),
	})
	return map[string]any{
		"id": response["id"], "object": "response.compaction", "created_at": response["created_at"],
		"output": output, "usage": response["usage"],
	}, nil
}

func reasoningTextFromItem(item map[string]any) string {
	if text := extractReasoningSummaryText(item); text != "" {
		return text
	}
	return extractReasoningFieldText(item)
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
	if jsonx.String(context.ResponseToolChoice) == "required" && len(context.chatTools) == 0 {
		return nil, nil, unsupported("tool_choice", "required tool execution when no client-executable tools are available")
	}
	if jsonx.String(jsonx.Map(context.ResponseToolChoice)["type"]) == "tool_search" && context.bindings[toolSearchName].Kind != "tool_search" {
		return nil, nil, unsupported("tool_choice", "forced tool search without a client-executable tool_search declaration")
	}

	messages := make([]any, 0, 16)
	prefixMessages := make([]any, 0, 2)
	if instructions := body["instructions"]; instructions != nil {
		text := jsonx.String(instructions)
		if text == "" {
			raw, _ := json.Marshal(instructions)
			text = string(raw)
		}
		if text != "" {
			prefixMessages = append(prefixMessages, map[string]any{"role": "system", "content": text})
		}
	}
	input := jsonx.Slice(body["input"])
	if input == nil {
		input = []any{map[string]any{"type": "message", "role": "user", "content": body["input"]}}
	}
	pendingToolCalls := make([]any, 0)
	pendingReasoning := ""
	compactionSummaries := make([]string, 0, 1)
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
			if summary, ok := compactionSummaryFromEnvelope(jsonx.String(item["encrypted_content"])); ok {
				compactionSummaries = append(compactionSummaries, summary)
			} else {
				compactionSummaries = append(compactionSummaries, "Earlier conversation was compacted, but its details are not readable by this provider.")
			}
		case "additional_tools":
			// Responses Lite carries dynamic tool declarations here; they are
			// not Chat messages and must not be replayed as user text.
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
	usage := jsonx.Map(value)
	if usage == nil {
		return nil
	}
	input := intValue(usage["prompt_tokens"])
	output := intValue(usage["completion_tokens"])
	promptDetails := jsonx.Map(usage["prompt_tokens_details"])
	completionDetails := jsonx.Map(usage["completion_tokens_details"])
	return map[string]any{
		"input_tokens":          input,
		"input_tokens_details":  map[string]any{"cached_tokens": intValue(promptDetails["cached_tokens"])},
		"output_tokens":         output,
		"output_tokens_details": map[string]any{"reasoning_tokens": intValue(completionDetails["reasoning_tokens"])},
		"total_tokens":          intValueWithFallback(usage["total_tokens"], input+output),
	}
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
