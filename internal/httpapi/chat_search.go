package httpapi

import (
	"strings"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
	responsesbridge "github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/responses"
)

// mapChatSearch rewrites a client-declared web_search on Chat Completions
// requests into the gateway's search tool for planner models. Declaration
// driven: a client that declares nothing keeps no search. Direct (OpenRouter)
// routes keep the client's own tool because they reject the gateway's private
// tool ids.
func (s *Server) mapChatSearch(modelID string, body map[string]any) {
	tool := responsesbridge.NormaliseWebSearchTool(s.store.WebSearchUpstream())
	if tool == "" || body == nil || s.store.ModelMeta(modelID).Pipeline == "direct" {
		return
	}
	if !responsesbridge.MapChatSearchTools(body, tool) {
		return
	}
	appendChatSearchPolicy(body)
}

// appendChatSearchPolicy adds the gateway search guidance to the request's
// system message, creating one when the request carries none.
func appendChatSearchPolicy(body map[string]any) {
	policy := responsesbridge.GatewaySearchPolicy()
	messages := jsonx.Slice(body["messages"])
	if len(messages) > 0 {
		first := jsonx.Map(messages[0])
		if jsonx.String(first["role"]) == "system" {
			if text, ok := first["content"].(string); ok {
				first["content"] = strings.TrimSpace(text + "\n\n" + policy)
				return
			}
		}
	}
	body["messages"] = append([]any{map[string]any{"role": "system", "content": policy}}, messages...)
}
