package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	responsesbridge "github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/responses"
)

func setResponsesHeaders(writer http.ResponseWriter, targets []string, result chainResult, effort string) {
	writer.Header().Set("X-Cline-Target-Upstream", targetHeader(targets))
	writer.Header().Set("X-Cline-Actual-Upstream", firstNonEmpty(result.Routing.FinalProvider, "unknown"))
	writer.Header().Set("X-Cline-Canonical-Model", result.Routing.CanonicalSlug)
	writer.Header().Set("X-Cline-Attempts", strconv.Itoa(len(result.Trace)))
	writer.Header().Set("X-Cline-Account", headerSafe(result.Account.Name))
	if effort != "" {
		writer.Header().Set("X-Cline-Reasoning-Effort", headerSafe(effort))
	}
}

func (s *Server) handleResponses(writer http.ResponseWriter, request *http.Request) {
	var body map[string]any
	if err := readJSON(request, &body); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"message": "invalid JSON body", "type": "invalid_request_error"},
		})
		return
	}
	// Remote compaction v2 arrives as an ordinary /responses request that
	// carries a compaction_trigger item. It is answered by the compaction
	// pipeline rather than a normal generation turn.
	if responsesbridge.RequestTriggersCompaction(body) {
		s.handleResponsesCompactionTrigger(writer, request, body)
		return
	}
	requestedModel, _ := body["model"].(string)
	chatBody, bridgeContext, err := responsesbridge.ToChatWithOptions(body, responsesbridge.Options{
		ReplayReasoning:    responsesbridge.ShouldReplayReasoning(requestedModel),
		ReasoningEfforts:   s.store.ModelMeta(requestedModel).ReasoningEfforts,
		RawReasoning:       responsesbridge.ShouldUseRawReasoning(requestedModel),
		StrictToolHistory:  s.store.StrictToolHistory(),
		WebSearchUpstream:  s.store.WebSearchUpstream(),
		WebFetchUpstream:   s.store.WebFetchUpstream(),
		ShellCompat:        s.store.ShellCompat(),
		ShellCompatEnforce: s.store.ShellCompatEnforce(),
	})
	if err != nil {
		writeResponsesRequestError(writer, err)
		return
	}
	modelID := bridgeContext.Model
	modelConfig := s.store.ModelConfig(modelID)
	bridgeContext.InputTokenCap = int64(s.store.ModelMeta(modelID).ContextWindow)
	stream, _ := body["stream"].(bool)
	if stream {
		s.handleStreamingResponses(writer, request, body, chatBody, bridgeContext, modelID, modelConfig)
		return
	}

	result := s.runNonStreamChain(request.Context(), modelID, chatBody, modelConfig, s.upstream.NonStreamTimeout())
	if result.Out == nil {
		writeJSON(writer, http.StatusBadGateway, map[string]any{
			"error": map[string]any{"message": "no upstream response", "type": "upstream_error"},
		})
		return
	}
	targets := attemptTargets(s.requestAttempts(modelID, modelConfig, chatBody))
	setResponsesHeaders(writer, targets, result, bridgeContext.MappedReasoningEffort)
	if result.Status != http.StatusOK {
		message := chainErrorMessage(result)
		s.record(model.HistoryEntry{
			TS: time.Now().UnixMilli(), Model: modelID, MS: time.Since(result.Started).Milliseconds(),
			Stream: false, Kind: "responses", Effort: recordedEffort(bridgeContext.MappedReasoningEffort, chatBody),
			RequestedEffort: bridgeContext.RequestedReasoningEffort,
			Error:           &message, Account: result.Account.Name, AccountID: result.Account.ID,
			Attempts: traceUpstreams(result.Trace), Trace: result.Trace,
		})
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		writer.WriteHeader(result.Status)
		_ = json.NewEncoder(writer).Encode(result.Out)
		return
	}
	response, err := responsesbridge.FromChat(result.Out, bridgeContext)
	if err != nil {
		message := err.Error()
		s.record(model.HistoryEntry{
			TS: time.Now().UnixMilli(), Model: modelID, Provider: result.Routing.FinalProvider,
			Canonical: result.Routing.CanonicalSlug, MS: time.Since(result.Started).Milliseconds(),
			Stream: false, Kind: "responses", Effort: recordedEffort(bridgeContext.MappedReasoningEffort, chatBody),
			RequestedEffort: bridgeContext.RequestedReasoningEffort,
			Error:           &message, Account: result.Account.Name, AccountID: result.Account.ID,
			Attempts: traceUpstreams(result.Trace), Trace: result.Trace,
		})
		writeJSON(writer, http.StatusBadGateway, map[string]any{"error": conversionErrorBody(err)})
		return
	}
	entry := model.HistoryEntry{
		TS: time.Now().UnixMilli(), Model: modelID, Provider: result.Routing.FinalProvider,
		Canonical: result.Routing.CanonicalSlug, MS: time.Since(result.Started).Milliseconds(),
		Stream: false, Kind: "responses",
		Error: nil, Account: result.Account.Name, AccountID: result.Account.ID,
		Attempts: traceUpstreams(result.Trace), Trace: result.Trace,
	}
	applyReasoningEffort(&entry, bridgeContext.MappedReasoningEffort, bridgeContext.RequestedReasoningEffort, chatBody)
	applyChatStats(&entry, result.Out, entry.MS)
	s.record(entry)
	writeJSON(writer, http.StatusOK, response)
}

// conversionErrorBody keeps the state machine's error code/type when a
// buffered upstream turn is rejected, instead of flattening it to
// upstream_error.
func conversionErrorBody(err error) map[string]any {
	body := map[string]any{"message": err.Error(), "type": "upstream_error"}
	var failure *responsesbridge.ChatFailure
	if errors.As(err, &failure) {
		if failure.Type != "" {
			body["type"] = failure.Type
		}
		if failure.Code != "" {
			body["code"] = failure.Code
		}
	}
	return body
}

func writeResponsesRequestError(writer http.ResponseWriter, err error) {
	body := map[string]any{"message": err.Error(), "type": "invalid_request_error"}
	var unsupported *responsesbridge.RequestError
	if errors.As(err, &unsupported) {
		body["code"], body["param"] = firstNonEmpty(unsupported.Code, "unsupported_feature"), unsupported.Param
	}
	writeJSON(writer, http.StatusBadRequest, map[string]any{"error": body})
}

func (s *Server) handleResponsesCompact(writer http.ResponseWriter, request *http.Request) {
	var body map[string]any
	if err := readJSON(request, &body); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"message": "invalid JSON body", "type": "invalid_request_error"},
		})
		return
	}
	requestedModel, _ := body["model"].(string)
	chatBody, bridgeContext, err := responsesbridge.ToCompactionChatWithOptions(body, responsesbridge.Options{
		ReplayReasoning:   responsesbridge.ShouldReplayReasoning(requestedModel),
		ReasoningEfforts:  s.store.ModelMeta(requestedModel).ReasoningEfforts,
		RawReasoning:      responsesbridge.ShouldUseRawReasoning(requestedModel),
		StrictToolHistory: s.store.StrictToolHistory(),
		WebSearchUpstream: s.store.WebSearchUpstream(),
		WebFetchUpstream:  s.store.WebFetchUpstream(),
	})
	if err != nil {
		writeResponsesRequestError(writer, err)
		return
	}
	modelID := bridgeContext.Model
	modelConfig := s.store.ModelConfig(modelID)
	bridgeContext.InputTokenCap = int64(s.store.ModelMeta(modelID).ContextWindow)
	// Compaction needs the complete summary before it can be wrapped into one
	// opaque output item, so the upstream call is always buffered.
	ensureCompactionBudget(chatBody)
	bridgeContext.MaxOutputTokens = chatBody["max_tokens"]

	stream, _ := body["stream"].(bool)
	started := time.Now()
	result := s.runNonStreamChain(request.Context(), modelID, chatBody, modelConfig, s.upstream.NonStreamTimeout())
	if result.Status != http.StatusOK || result.Out == nil {
		message := chainErrorMessage(result)
		if message == "" {
			message = "upstream returned no response"
		}
		s.record(model.HistoryEntry{
			TS: time.Now().UnixMilli(), Model: modelID, MS: time.Since(started).Milliseconds(),
			Stream: stream, Kind: "compact", Effort: recordedEffort(bridgeContext.MappedReasoningEffort, chatBody),
			RequestedEffort: bridgeContext.RequestedReasoningEffort,
			Error:           &message, Account: result.Account.Name, AccountID: result.Account.ID,
			Attempts: traceUpstreams(result.Trace), Trace: result.Trace,
		})
		if stream {
			details := map[string]any{"message": message, "type": "upstream_error"}
			if upstreamError, ok := result.Out["error"].(map[string]any); ok {
				details = upstreamError
			}
			writeCompactFailure(writer, modelID, details)
			return
		}
		if result.Out != nil {
			status := result.Status
			if status < 400 {
				status = http.StatusBadGateway
			}
			writeJSON(writer, status, result.Out)
			return
		}
		writeJSON(writer, http.StatusBadGateway, map[string]any{
			"error": map[string]any{"message": message, "type": "upstream_error"},
		})
		return
	}

	compaction, err := responsesbridge.CompactionResponse(result.Out, bridgeContext)
	if err != nil {
		message := err.Error()
		s.record(model.HistoryEntry{
			TS: time.Now().UnixMilli(), Model: modelID, MS: time.Since(started).Milliseconds(),
			Stream: stream, Kind: "compact", Effort: recordedEffort(bridgeContext.MappedReasoningEffort, chatBody),
			RequestedEffort: bridgeContext.RequestedReasoningEffort,
			Error:           &message, Account: result.Account.Name, AccountID: result.Account.ID,
			Attempts: traceUpstreams(result.Trace), Trace: result.Trace,
		})
		if stream {
			writeCompactFailure(writer, modelID, conversionErrorBody(err))
			return
		}
		writeJSON(writer, http.StatusBadGateway, map[string]any{"error": conversionErrorBody(err)})
		return
	}

	compactEntry := model.HistoryEntry{
		TS: time.Now().UnixMilli(), Model: modelID,
		Provider: result.Routing.FinalProvider, Canonical: result.Routing.CanonicalSlug,
		MS: time.Since(started).Milliseconds(), Stream: stream, Kind: "compact",
		Account: result.Account.Name, AccountID: result.Account.ID,
		Attempts: traceUpstreams(result.Trace), Trace: result.Trace,
	}
	applyReasoningEffort(&compactEntry, bridgeContext.MappedReasoningEffort, bridgeContext.RequestedReasoningEffort, chatBody)
	applyChatStats(&compactEntry, result.Out, compactEntry.MS)
	s.record(compactEntry)
	targets := attemptTargets(s.upstream.BuildAttempts(modelID, modelConfig))
	if !stream {
		setResponsesHeaders(writer, targets, result, bridgeContext.MappedReasoningEffort)
		writeJSON(writer, http.StatusOK, compaction)
		return
	}

	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	setResponsesHeaders(writer, targets, result, bridgeContext.MappedReasoningEffort)
	writer.WriteHeader(http.StatusOK)
	_ = writeResponseEvents(writer, responsesbridge.NewEventWriter(writer), responsesbridge.CompactionEvents(compaction, bridgeContext))
}

// compactionMinOutputTokens is the output floor for compaction turns.
// Reasoning models can otherwise spend the entire budget on hidden thinking
// and the upstream answers "empty response content"; see ensureCompactionBudget.
const compactionMinOutputTokens = 4096

func ensureCompactionBudget(chatBody map[string]any) {
	if positiveInt(chatBody["max_tokens"]) < compactionMinOutputTokens {
		chatBody["max_tokens"] = compactionMinOutputTokens
	}
}

func writeCompactFailure(writer http.ResponseWriter, modelID string, details map[string]any) {
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.WriteHeader(http.StatusOK)
	state := responsesbridge.NewStreamState(&responsesbridge.Context{Model: modelID})
	events := state.HandleChunk(map[string]any{"error": details})
	_ = writeResponseEvents(writer, responsesbridge.NewEventWriter(writer), events)
}

// handleResponsesCompactionTrigger serves remote compaction v2: Codex sends an
// ordinary /responses request carrying a {type:"compaction_trigger"} input item
// and expects exactly one compaction output item plus a response.completed
// event. The work is the same summarization the standalone /responses/compact
// endpoint performs, so the history entry is recorded as kind "compact".
func (s *Server) handleResponsesCompactionTrigger(writer http.ResponseWriter, request *http.Request, body map[string]any) {
	requestedModel, _ := body["model"].(string)
	chatBody, bridgeContext, err := responsesbridge.ToCompactionChatWithOptions(body, responsesbridge.Options{
		ReplayReasoning:    responsesbridge.ShouldReplayReasoning(requestedModel),
		ReasoningEfforts:   s.store.ModelMeta(requestedModel).ReasoningEfforts,
		RawReasoning:       responsesbridge.ShouldUseRawReasoning(requestedModel),
		StrictToolHistory:  s.store.StrictToolHistory(),
		WebSearchUpstream:  s.store.WebSearchUpstream(),
		WebFetchUpstream:   s.store.WebFetchUpstream(),
		ShellCompat:        s.store.ShellCompat(),
		ShellCompatEnforce: s.store.ShellCompatEnforce(),
	})
	if err != nil {
		writeResponsesRequestError(writer, err)
		return
	}
	modelID := bridgeContext.Model
	modelConfig := s.store.ModelConfig(modelID)
	bridgeContext.InputTokenCap = int64(s.store.ModelMeta(modelID).ContextWindow)
	ensureCompactionBudget(chatBody)
	bridgeContext.MaxOutputTokens = chatBody["max_tokens"]

	stream, _ := body["stream"].(bool)
	started := time.Now()
	result := s.runNonStreamChain(request.Context(), modelID, chatBody, modelConfig, s.upstream.NonStreamTimeout())
	if result.Status != http.StatusOK || result.Out == nil {
		message := chainErrorMessage(result)
		if message == "" {
			message = "upstream returned no response"
		}
		s.record(model.HistoryEntry{
			TS: time.Now().UnixMilli(), Model: modelID, MS: time.Since(started).Milliseconds(),
			Stream: stream, Kind: "compact", Effort: recordedEffort(bridgeContext.MappedReasoningEffort, chatBody),
			RequestedEffort: bridgeContext.RequestedReasoningEffort,
			Error:           &message, Account: result.Account.Name, AccountID: result.Account.ID,
			Attempts: traceUpstreams(result.Trace), Trace: result.Trace,
		})
		if stream {
			details := map[string]any{"message": message, "type": "upstream_error"}
			if upstreamError, ok := result.Out["error"].(map[string]any); ok {
				details = upstreamError
			}
			writeCompactFailure(writer, modelID, details)
			return
		}
		status := result.Status
		if status < 400 {
			status = http.StatusBadGateway
		}
		if result.Out != nil {
			writeJSON(writer, status, result.Out)
			return
		}
		writeJSON(writer, status, map[string]any{
			"error": map[string]any{"message": message, "type": "upstream_error"},
		})
		return
	}

	compaction, err := responsesbridge.CompactionTriggerResponse(result.Out, bridgeContext)
	if err != nil {
		message := err.Error()
		s.record(model.HistoryEntry{
			TS: time.Now().UnixMilli(), Model: modelID, MS: time.Since(started).Milliseconds(),
			Stream: stream, Kind: "compact", Effort: recordedEffort(bridgeContext.MappedReasoningEffort, chatBody),
			RequestedEffort: bridgeContext.RequestedReasoningEffort,
			Error:           &message, Account: result.Account.Name, AccountID: result.Account.ID,
			Attempts: traceUpstreams(result.Trace), Trace: result.Trace,
		})
		if stream {
			writeCompactFailure(writer, modelID, conversionErrorBody(err))
			return
		}
		writeJSON(writer, http.StatusBadGateway, map[string]any{"error": conversionErrorBody(err)})
		return
	}

	entry := model.HistoryEntry{
		TS: time.Now().UnixMilli(), Model: modelID,
		Provider: result.Routing.FinalProvider, Canonical: result.Routing.CanonicalSlug,
		MS: time.Since(started).Milliseconds(), Stream: stream, Kind: "compact",
		Account: result.Account.Name, AccountID: result.Account.ID,
		Attempts: traceUpstreams(result.Trace), Trace: result.Trace,
	}
	applyReasoningEffort(&entry, bridgeContext.MappedReasoningEffort, bridgeContext.RequestedReasoningEffort, chatBody)
	applyChatStats(&entry, result.Out, entry.MS)
	s.record(entry)
	targets := attemptTargets(s.requestAttempts(modelID, modelConfig, chatBody))
	setResponsesHeaders(writer, targets, result, bridgeContext.MappedReasoningEffort)
	if !stream {
		writeJSON(writer, http.StatusOK, compaction)
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.WriteHeader(http.StatusOK)
	_ = writeResponseEvents(writer, responsesbridge.NewEventWriter(writer), responsesbridge.CompactionTriggerEvents(compaction, bridgeContext))
}

func positiveInt(value any) int {
	switch typed := value.(type) {
	case json.Number:
		result, _ := typed.Int64()
		return int(result)
	case float64:
		return int(typed)
	case int:
		return typed
	case int64:
		return int(typed)
	default:
		return 0
	}
}

func writeResponseEvents(writer http.ResponseWriter, sink *responsesbridge.EventWriter, events []responsesbridge.Event) error {
	controller := http.NewResponseController(writer)
	defer clearStreamDeadline(writer)
	for _, event := range events {
		_ = controller.SetWriteDeadline(time.Now().Add(streamClientWriteTimeout))
		if err := sink.Write(event); err != nil {
			return err
		}
		if err := controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
	}
	return nil
}

func appendRawTail(tail []byte, data []byte) []byte {
	const maxTail = 128 << 10
	tail = append(tail, data...)
	if len(tail) > maxTail {
		tail = tail[len(tail)-maxTail:]
	}
	return tail
}
