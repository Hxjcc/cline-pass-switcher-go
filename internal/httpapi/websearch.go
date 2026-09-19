package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	responsesbridge "github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/responses"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/search"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/strx"
)

const (
	// maxWebSearchLegs bounds how often one client turn may go back upstream
	// after a proxy-side search before the proxy gives up.
	maxWebSearchLegs = 5
	// webSearchResultLimit is how many pages one query returns.
	webSearchResultLimit = 6
)

// directSearchEnabled reports whether a search API key is configured.
func (s *Server) directSearchEnabled() bool {
	apiKey, _ := s.store.WebSearchDirect()
	return apiKey != ""
}

// searchProvider builds the proxy-side search client, or nil when disabled.
func (s *Server) searchProvider() search.Provider {
	apiKey, baseURL := s.store.WebSearchDirect()
	if apiKey == "" {
		return nil
	}
	return search.NewExa(apiKey, baseURL)
}

// searchOutcome is one finished search: the call the client is shown and the
// tool message that feeds the pages back to the model.
type searchOutcome struct {
	call    responsesbridge.WebSearchCall
	message map[string]any
}

// executeSearches runs captured search calls against the provider. Search
// failures are reported to the model instead of failing the whole turn.
func (s *Server) executeSearches(
	ctx context.Context,
	provider search.Provider,
	requests []responsesbridge.SearchRequest,
) ([]searchOutcome, []model.Trace) {
	outcomes := make([]searchOutcome, 0, len(requests))
	traces := make([]model.Trace, 0, len(requests))
	for _, request := range requests {
		started := time.Now()
		results, err := provider.Search(ctx, request.Query, webSearchResultLimit)
		trace := model.Trace{
			Upstream: "web_search",
			Status:   http.StatusOK,
			MS:       time.Since(started).Milliseconds(),
			Note:     strx.Truncate(request.Query, 120),
		}
		content := responsesbridge.WebSearchToolContent(request.Query, bridgeSearchResults(results))
		if err != nil {
			trace.Status = http.StatusBadGateway
			trace.Note = strx.Truncate(request.Query+" — "+err.Error(), 160)
			content = responsesbridge.WebSearchFailureContent(err)
		}
		outcomes = append(outcomes, searchOutcome{
			call:    responsesbridge.WebSearchCall{Query: request.Query},
			message: map[string]any{"role": "tool", "tool_call_id": request.CallID, "content": content},
		})
		traces = append(traces, trace)
	}
	return outcomes, traces
}

func bridgeSearchResults(results []search.Result) []responsesbridge.WebSearchResult {
	if len(results) == 0 {
		return nil
	}
	converted := make([]responsesbridge.WebSearchResult, 0, len(results))
	for _, result := range results {
		converted = append(converted, responsesbridge.WebSearchResult{
			Title: result.Title, URL: result.URL, Text: result.Text,
		})
	}
	return converted
}

// runBufferedSearchLegs drives proxy-side searches for the non-streaming
// Responses endpoint. Every upstream answer that ends in a search call is
// answered here and followed by another upstream call, up to
// maxWebSearchLegs.
func (s *Server) runBufferedSearchLegs(
	ctx context.Context,
	provider search.Provider,
	result chainResult,
	chatBody map[string]any,
	bridgeContext *responsesbridge.Context,
	modelID string,
	modelConfig model.PerModelConfig,
) (chainResult, map[string]any) {
	var totalUsage any
	for leg := 0; leg < maxWebSearchLegs; leg++ {
		if result.Status != http.StatusOK || result.Out == nil {
			return result, chatBody
		}
		assistant, requests := responsesbridge.ChatSearchTurn(result.Out, bridgeContext)
		if len(requests) == 0 {
			return result, chatBody
		}
		totalUsage = responsesbridge.MergeChatUsage(totalUsage, result.Out["usage"])
		outcomes, traces := s.executeSearches(ctx, provider, requests)
		result.Trace = append(result.Trace, traces...)
		messages := []any{assistant}
		for _, outcome := range outcomes {
			bridgeContext.RecordWebSearch(outcome.call)
			messages = append(messages, outcome.message)
		}
		chatBody = responsesbridge.AppendMessages(chatBody, messages...)
		result = s.runNonStreamChain(ctx, modelID, chatBody, modelConfig, s.upstream.NonStreamTimeout())
		if result.Out != nil {
			result.Out["usage"] = responsesbridge.MergeChatUsage(totalUsage, result.Out["usage"])
		}
	}
	return result, chatBody
}

// insertWebSearchItems prepends synthesized search items to a buffered
// response so clients show every search before the answer.
func insertWebSearchItems(response map[string]any, searches []responsesbridge.WebSearchCall) {
	if response == nil || len(searches) == 0 {
		return
	}
	output := make([]any, 0, len(searches)+len(jsonx.Slice(response["output"])))
	for _, call := range searches {
		output = append(output, responsesbridge.WebSearchCallItem(call))
	}
	output = append(output, jsonx.Slice(response["output"])...)
	response["output"] = output
}
