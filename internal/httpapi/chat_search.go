package httpapi

import (
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
	responsesbridge "github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/responses"
)

// injectChatSearch declares the gateway-executed search tool on Chat
// Completions requests for planner models. Clients that speak Chat Completions
// (DeepSeek Harness, Cherry Studio, scripts, ...) cannot run the gateway's
// private tool ids themselves, but once the tool is declared the gateway runs
// the search inside its own planner loop and the client only sees the finished
// answer. Direct (OpenRouter) routes keep nothing: they forward the tool list
// to the provider, which rejects the gateway's tool ids.
func (s *Server) injectChatSearch(modelID string, body map[string]any) {
	tool := responsesbridge.NormaliseWebSearchTool(s.store.WebSearchUpstream())
	if tool == "" || body == nil {
		return
	}
	if s.store.ModelMeta(modelID).Pipeline == "direct" {
		return
	}
	tools := jsonx.Slice(body["tools"])
	for _, raw := range tools {
		if jsonx.String(jsonx.Map(raw)["type"]) == tool {
			return
		}
	}
	body["tools"] = append(tools, map[string]any{"type": tool})
}
