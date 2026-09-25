package responses

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
)

var invalidToolName = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

type toolBinding struct {
	Kind      string
	Name      string
	Namespace string
	HasShell  bool
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
	if isWebSearchToolType(jsonx.String(tool["type"])) {
		context.addProviderWebSearch()
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

	parameters := tool["parameters"]
	if parameters == nil {
		parameters = tool["input_schema"]
	}
	context.bindings[chatName] = toolBinding{
		Kind: "function", Name: name, Namespace: namespace, HasShell: schemaHasShellProperty(parameters),
	}
	parameters = context.lockToolShell(parameters)
	context.chatTools = append(context.chatTools, functionTool(chatName, jsonx.String(tool["description"]), parameters, tool["strict"]))
}

// lockToolShell restricts a forwarded tool schema's "shell" property to the
// configured shell and marks it required, so models include the parameter
// instead of leaving the client to pick a platform default (cmd.exe on
// Windows). It is schema-driven and copies the maps it touches: tools without
// a shell property and the caller's original request body stay untouched.
func (context *Context) lockToolShell(parameters any) any {
	if context.shellCompat == "" {
		return parameters
	}
	schema := jsonx.Map(parameters)
	if schema == nil {
		return parameters
	}
	properties := jsonx.Map(schema["properties"])
	shell := jsonx.Map(properties["shell"])
	if shell == nil {
		return parameters
	}
	nextSchema := make(map[string]any, len(schema)+1)
	for key, value := range schema {
		nextSchema[key] = value
	}
	nextProperties := make(map[string]any, len(properties))
	for key, value := range properties {
		nextProperties[key] = value
	}
	nextShell := make(map[string]any, len(shell)+1)
	for key, value := range shell {
		nextShell[key] = value
	}
	nextShell["enum"] = []any{context.shellCompat}
	nextProperties["shell"] = nextShell
	nextSchema["properties"] = nextProperties

	required := make([]any, 0, len(jsonx.Slice(nextSchema["required"]))+1)
	found := false
	for _, raw := range jsonx.Slice(nextSchema["required"]) {
		if jsonx.String(raw) == "shell" {
			found = true
		}
		required = append(required, raw)
	}
	if !found {
		required = append(required, "shell")
	}
	nextSchema["required"] = required
	return nextSchema
}

func schemaHasShellProperty(parameters any) bool {
	schema := jsonx.Map(parameters)
	if schema == nil {
		return false
	}
	properties := jsonx.Map(schema["properties"])
	if properties == nil {
		return false
	}
	_, found := properties["shell"]
	return found
}

func (context *Context) toolHasShell(name string) bool {
	if context == nil {
		return false
	}
	binding, found := context.bindings[name]
	return found && binding.HasShell
}

func (context *Context) enforcesShell(name string) bool {
	return context != nil && context.shellCompat != "" && context.shellCompatEnforce && context.toolHasShell(name)
}

// rewriteToolShell replaces only the shell property in a complete JSON
// argument object. Other properties are kept as raw JSON so their values are
// not re-encoded.
func (context *Context) rewriteToolShell(name, arguments string) (string, bool) {
	if !context.enforcesShell(name) {
		return arguments, false
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(arguments), &object); err != nil {
		return arguments, false
	}
	if object == nil {
		object = map[string]json.RawMessage{}
	}
	shell, err := json.Marshal(context.shellCompat)
	if err != nil {
		return arguments, false
	}
	object["shell"] = shell
	rewritten, err := json.Marshal(object)
	if err != nil {
		return arguments, false
	}
	return string(rewritten), true
}

// isWebSearchToolType reports whether the client declared OpenAI's hosted web
// search. The other hosted tools (file_search, computer, ...) stay unsupported.
func isWebSearchToolType(typeName string) bool {
	switch typeName {
	case "web_search", "web_search_preview", "web_search_preview_2025_03_11":
		return true
	default:
		return false
	}
}

// addProviderWebSearch declares the gateway-executed search tool that stands in
// for the hosted web_search. The gateway performs the search, so the client only
// ever sees the finished answer.
func (context *Context) addProviderWebSearch() {
	context.addProviderTool(context.webSearchTool)
}

// addProviderTool declares one gateway-executed tool exactly once and remembers
// its names so calls the gateway answers itself never reach the client.
func (context *Context) addProviderTool(tool string) {
	if tool == "" {
		return
	}
	if context.providerTools == nil {
		context.providerTools = map[string]struct{}{}
	}
	if _, found := context.providerTools[tool]; found {
		return
	}
	context.providerTools[tool] = struct{}{}
	context.chatTools = append(context.chatTools, map[string]any{"type": tool})
	if index := strings.LastIndex(tool, ":"); index >= 0 {
		context.providerTools[tool[index+1:]] = struct{}{}
	}
}

// normaliseWebFetchTool maps configuration onto the gateway fetch tool.
func normaliseWebFetchTool(value string) string {
	trimmed := strings.TrimSpace(value)
	switch strings.ToLower(trimmed) {
	case "", "off", "none", "false", "disabled":
		return ""
	case "browserbase", "browserbase_fetch", "fetch", "on", "true":
		return "vercel:browserbase_fetch"
	default:
		if strings.HasPrefix(strings.ToLower(trimmed), "vercel:") {
			return trimmed
		}
		return ""
	}
}

// normaliseShellCompat turns the configured shell-compat value into the shell
// forced into forwarded tool schemas, or "" when the feature is off.
func normaliseShellCompat(value string) string {
	trimmed := strings.TrimSpace(value)
	switch strings.ToLower(trimmed) {
	case "", "off", "none", "false", "disabled":
		return ""
	default:
		return trimmed
	}
}

// normaliseWebSearchTool maps configuration onto a gateway provider tool id.
func normaliseWebSearchTool(value string) string {
	trimmed := strings.TrimSpace(value)
	switch strings.ToLower(trimmed) {
	case "", "off", "none", "false", "disabled":
		return ""
	case "exa", "exa_search":
		return "vercel:exa_search"
	case "tako", "tako_search":
		return "vercel:tako_search"
	case "perplexity", "perplexity_search":
		return "vercel:perplexity_search"
	case "parallel", "parallel_search":
		return "vercel:parallel_search"
	case "browserbase_search":
		return "vercel:browserbase_search"
	case "browserbase", "browserbase_fetch", "fetch":
		return "vercel:browserbase_fetch"
	default:
		if strings.HasPrefix(strings.ToLower(trimmed), "vercel:") {
			return trimmed
		}
		return ""
	}
}

// isProviderTool reports whether a Chat tool call belongs to a tool the gateway
// executes on its own. Those calls never reach the client.
func (context *Context) isProviderTool(name string) bool {
	if name == "" || len(context.providerTools) == 0 {
		return false
	}
	_, found := context.providerTools[name]
	return found
}

// forcedHostedSearch reports whether the client pinned this turn to the hosted
// web search tool instead of letting the model decide.
func (context *Context) forcedHostedSearch() bool {
	return isWebSearchToolType(jsonx.String(jsonx.Map(context.ResponseToolChoice)["type"]))
}

func (context *Context) webSearchPolicy() string {
	if context == nil || context.webSearchTool == "" || !context.isProviderTool(context.webSearchTool) {
		return ""
	}
	return "Web search policy:\n" +
		"- Prefer at most one web search call per turn.\n" +
		"- Request at most 3 results.\n" +
		"- Do not issue parallel web searches.\n" +
		"- When invoking tools, do not output raw XML or DSML tool-call markup.\n" +
		"- After receiving search results, answer directly; only search again if the results are clearly insufficient."
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

// httpURLPattern matches an http(s) link inside user-authored text.
var httpURLPattern = regexp.MustCompile("https?://[^\\s<>\"')]+")

// requestHasUserURL reports whether the user sent a link. Reading a page is only
// useful in that case, so the fetch tool is declared lazily.
func requestHasUserURL(input any) bool {
	if text, ok := input.(string); ok {
		return httpURLPattern.MatchString(text)
	}
	for _, raw := range jsonx.Slice(input) {
		if text, ok := raw.(string); ok {
			if httpURLPattern.MatchString(text) {
				return true
			}
			continue
		}
		item := jsonx.Map(raw)
		if item == nil {
			continue
		}
		if role := jsonx.String(item["role"]); role != "" && role != "user" {
			continue
		}
		if kind := jsonx.String(item["type"]); kind != "" && kind != "message" && !strings.HasPrefix(kind, "input_") {
			continue
		}
		if httpURLPattern.MatchString(collectPartText(item["content"])) {
			return true
		}
		if httpURLPattern.MatchString(jsonx.String(item["text"])) {
			return true
		}
	}
	return false
}

// collectPartText flattens the text of a Responses content value.
func collectPartText(content any) string {
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
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}
