package responses

import (
	"strings"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
)

// MapChatSearchTools replaces a client-declared web_search tool with the
// gateway's provider tool id on a Chat Completions request body. The caller
// decides whether the model may carry gateway tools at all; this function only
// rewrites the declaration. Returns true when the body changed.
//
// Declaration-driven on purpose: a client that declares no search tool keeps
// none, matching the Codex contract.
func MapChatSearchTools(body map[string]any, gatewayTool string) bool {
	if body == nil || gatewayTool == "" {
		return false
	}
	tools := jsonx.Slice(body["tools"])
	if len(tools) == 0 {
		return false
	}
	kept := make([]any, 0, len(tools)+1)
	found := false
	hasGateway := false
	for _, raw := range tools {
		tool := jsonx.Map(raw)
		if jsonx.String(tool["type"]) == gatewayTool {
			hasGateway = true
			kept = append(kept, raw)
			continue
		}
		if isClientChatWebSearch(tool) {
			found = true
			continue
		}
		kept = append(kept, raw)
	}
	if !found {
		return false
	}
	if !hasGateway {
		kept = append(kept, map[string]any{"type": gatewayTool})
	}
	body["tools"] = kept
	return true
}

// isClientChatWebSearch recognises the search declarations a Chat Completions
// client may send: the OpenAI function shape (DeepSeek Harness declares its
// search this way) and the hosted type some compatible gateways accept.
func isClientChatWebSearch(tool map[string]any) bool {
	if tool == nil {
		return false
	}
	if isWebSearchToolType(jsonx.String(tool["type"])) {
		return true
	}
	if jsonx.String(tool["type"]) != "function" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(jsonx.String(jsonx.Map(tool["function"])["name"]))) {
	case "web_search", "web_search_preview":
		return true
	default:
		return false
	}
}

// GatewaySearchPolicy is the guidance injected next to a declared gateway
// search tool. The Responses bridge injects it into instructions; the Chat
// Completions path injects it as a system message.
func GatewaySearchPolicy() string {
	return "Web search policy:\n" +
		"- Prefer at most one web search call per turn.\n" +
		"- Request at most 3 results.\n" +
		"- Do not issue parallel web searches.\n" +
		"- When invoking tools, do not output raw XML or DSML tool-call markup.\n" +
		"- After receiving search results, answer directly; only search again if the results are clearly insufficient."
}
