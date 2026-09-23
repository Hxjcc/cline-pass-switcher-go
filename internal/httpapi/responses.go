package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	responsesbridge "github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/responses"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/strx"
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
		ReplayReasoning:           responsesbridge.ShouldReplayReasoning(requestedModel),
		ReasoningEfforts:          s.store.ModelMeta(requestedModel).ReasoningEfforts,
		RawReasoning:              responsesbridge.ShouldUseRawReasoning(requestedModel),
		StrictToolHistory:         s.store.StrictToolHistory(),
		WebSearchUpstream:         s.store.WebSearchUpstream(),
		WebFetchUpstream:          s.store.WebFetchUpstream(),
		ShellCompat:               s.store.ShellCompat(),
		ShellCompatEnforce:        s.store.ShellCompatEnforce(),
		RecentCompactionTokens:    s.store.CompactionRecentTokens(),
		CompactionReasoningEffort: s.store.CompactionReasoningEffort(),
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
		s.record(request.Context(), model.HistoryEntry{
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
		s.record(request.Context(), model.HistoryEntry{
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
	s.record(request.Context(), entry)
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
		ReplayReasoning:           responsesbridge.ShouldReplayReasoning(requestedModel),
		ReasoningEfforts:          s.store.ModelMeta(requestedModel).ReasoningEfforts,
		RawReasoning:              responsesbridge.ShouldUseRawReasoning(requestedModel),
		StrictToolHistory:         s.store.StrictToolHistory(),
		WebSearchUpstream:         s.store.WebSearchUpstream(),
		WebFetchUpstream:          s.store.WebFetchUpstream(),
		RecentCompactionTokens:    s.store.CompactionRecentTokens(),
		CompactionReasoningEffort: s.store.CompactionReasoningEffort(),
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
	s.ensureCompactionBudget(chatBody)
	bridgeContext.MaxOutputTokens = chatBody["max_tokens"]

	stream, _ := body["stream"].(bool)
	started := time.Now()
	result, compaction, err := s.runCompactionChain(
		request.Context(), modelID, chatBody, modelConfig, bridgeContext, responsesbridge.CompactionResponse, responsesbridge.DegradedCompactionResponse,
	)
	if result.Status != http.StatusOK || result.Out == nil {
		message := chainErrorMessage(result)
		if message == "" {
			message = "upstream returned no response"
		}
		s.record(request.Context(), model.HistoryEntry{
			TS: time.Now().UnixMilli(), Model: modelID, MS: time.Since(started).Milliseconds(),
			Stream: stream, Kind: "compact", Effort: recordedEffort(bridgeContext.MappedReasoningEffort, chatBody),
			Usage:           result.Usage,
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

	if err != nil {
		message := err.Error()
		s.record(request.Context(), model.HistoryEntry{
			TS: time.Now().UnixMilli(), Model: modelID, MS: time.Since(started).Milliseconds(),
			Stream: stream, Kind: "compact", Effort: recordedEffort(bridgeContext.MappedReasoningEffort, chatBody),
			Usage:           result.Usage,
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
		MissingSummarySections: compactionMissingSections(compaction),
		Degraded:               result.Degraded,
		DegradeReason:          result.DegradeReason,
	}
	applyReasoningEffort(&compactEntry, bridgeContext.MappedReasoningEffort, bridgeContext.RequestedReasoningEffort, chatBody)
	applyChatStats(&compactEntry, result.Out, compactEntry.MS)
	compactEntry.Usage = result.Usage
	s.record(request.Context(), compactEntry)
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

// compactionMinOutputTokens is the built-in output floor for compaction turns;
// the configured floor can only raise it.
// Reasoning models can otherwise spend the entire budget on hidden thinking
// and the upstream answers "empty response content"; see ensureCompactionBudget.
const compactionMinOutputTokens = 4096

func (s *Server) ensureCompactionBudget(chatBody map[string]any) {
	floor := s.store.CompactionMinOutputTokens()
	if floor < compactionMinOutputTokens {
		floor = compactionMinOutputTokens
	}
	if positiveInt(chatBody["max_tokens"]) < floor {
		chatBody["max_tokens"] = floor
	}
}

// compactionConvert adapts a completed Chat response to the compaction shape
// the caller needs: the standalone endpoint's response.compaction object or the
// remote compaction v2 reply.
type compactionConvert func(chat map[string]any, context *responsesbridge.Context) (map[string]any, error)

// compactionDegrade builds the fallback payload used when no summary can be
// produced at all: the client still gets a compaction item and can continue,
// at the cost of the older context.
type compactionDegrade func(context *responsesbridge.Context, reason, partial string) map[string]any

// runCompactionChain runs the summarization chain and, when the model starved
// on hidden reasoning instead of writing the summary, retries once with the
// model's top reasoning level and a doubled output budget. Both passes stay in
// the trace so the history shows what actually happened. When every pass fails
// the caller receives a degraded compaction item instead of an error, so a
// compaction turn never strands the client above its context limit.
func (s *Server) runCompactionChain(
	ctx context.Context,
	modelID string,
	chatBody map[string]any,
	modelConfig model.PerModelConfig,
	bridgeContext *responsesbridge.Context,
	convert compactionConvert,
	degrade compactionDegrade,
) (chainResult, map[string]any, error) {
	result := s.runNonStreamChain(ctx, modelID, chatBody, modelConfig, s.upstream.NonStreamTimeout())
	result.Usage = usageFromValue(result.Out["usage"])
	compaction, err := convertCompaction(result, bridgeContext, convert)
	if err == nil {
		return result, compaction, nil
	}
	// Only reasoning starvation is worth a second, more expensive pass: other
	// failures already walked the account and channel failover.
	if compactionStarved(result, err) && ctx.Err() == nil {
		retryBody := model.Clone(chatBody)
		responsesbridge.EscalateCompactionBudget(retryBody, s.store.ModelMeta(modelID).ReasoningEfforts)
		bridgeContext.MaxOutputTokens = retryBody["max_tokens"]
		if effort := effortFromChatBody(retryBody); effort != "" {
			bridgeContext.MappedReasoningEffort = effort
		}
		escalated := s.runNonStreamChain(ctx, modelID, retryBody, modelConfig, s.upstream.NonStreamTimeout())
		escalated.Usage = addUsage(result.Usage, usageFromValue(escalated.Out["usage"]))
		escalated.Trace = append(append([]model.Trace(nil), result.Trace...), escalated.Trace...)
		compaction, err = convertCompaction(escalated, bridgeContext, convert)
		if err == nil {
			return escalated, compaction, nil
		}
		result = escalated
	}
	if degrade == nil || ctx.Err() != nil {
		return result, nil, err
	}
	reason := compactionFailureReason(result, err)
	degraded := degrade(bridgeContext, reason, responsesbridge.PartialCompactionSummary(result.Out))
	result.Status = http.StatusOK
	result.Out = map[string]any{}
	result.NetErr = ""
	result.Degraded = true
	result.DegradeReason = strx.Truncate(reason, 200)
	return result, degraded, nil
}

// compactionFailureReason is the short explanation embedded in a degraded
// compaction item.
func compactionFailureReason(result chainResult, err error) string {
	if message := chainErrorMessage(result); message != "" && message != "upstream error" {
		return message
	}
	if err != nil {
		return err.Error()
	}
	return "upstream returned no summary"
}

func convertCompaction(result chainResult, bridgeContext *responsesbridge.Context, convert compactionConvert) (map[string]any, error) {
	if result.Status != http.StatusOK || result.Out == nil {
		message := chainErrorMessage(result)
		if message == "" {
			message = "upstream returned no response"
		}
		return nil, errors.New(message)
	}
	return convert(result.Out, bridgeContext)
}

// compactionStarved reports the failures a more expensive second pass can fix:
// the gateway rejected an empty summary, or the summary came back incomplete.
func compactionStarved(result chainResult, err error) bool {
	if result.Status >= 400 {
		return strings.Contains(strings.ToLower(chainErrorMessage(result)), "empty response content")
	}
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "empty compaction summary") ||
		strings.Contains(message, "compaction_incomplete") ||
		strings.Contains(message, "did not finish") ||
		strings.Contains(message, "incomplete")
}

// compactionMissingSections lists the anchored summary sections a successful
// compaction left out. The compaction is served as-is; the omission only goes
// to the history so a thin summary shows up instead of staying silent.
func compactionMissingSections(compaction map[string]any) []string {
	for _, raw := range jsonx.Slice(compaction["output"]) {
		item := jsonx.Map(raw)
		if jsonx.String(item["type"]) != "compaction" {
			continue
		}
		payload, ok := responsesbridge.DecodeCompactionEnvelope(jsonx.String(item["encrypted_content"]))
		if !ok {
			return nil
		}
		return responsesbridge.MissingCompactionSections(payload.Summary)
	}
	return nil
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
		ReplayReasoning:           responsesbridge.ShouldReplayReasoning(requestedModel),
		ReasoningEfforts:          s.store.ModelMeta(requestedModel).ReasoningEfforts,
		RawReasoning:              responsesbridge.ShouldUseRawReasoning(requestedModel),
		StrictToolHistory:         s.store.StrictToolHistory(),
		WebSearchUpstream:         s.store.WebSearchUpstream(),
		WebFetchUpstream:          s.store.WebFetchUpstream(),
		ShellCompat:               s.store.ShellCompat(),
		ShellCompatEnforce:        s.store.ShellCompatEnforce(),
		RecentCompactionTokens:    s.store.CompactionRecentTokens(),
		CompactionReasoningEffort: s.store.CompactionReasoningEffort(),
	})
	if err != nil {
		writeResponsesRequestError(writer, err)
		return
	}
	modelID := bridgeContext.Model
	modelConfig := s.store.ModelConfig(modelID)
	bridgeContext.InputTokenCap = int64(s.store.ModelMeta(modelID).ContextWindow)
	s.ensureCompactionBudget(chatBody)
	bridgeContext.MaxOutputTokens = chatBody["max_tokens"]

	stream, _ := body["stream"].(bool)
	started := time.Now()
	result, compaction, err := s.runCompactionChain(
		request.Context(), modelID, chatBody, modelConfig, bridgeContext, responsesbridge.CompactionTriggerResponse, responsesbridge.DegradedCompactionTriggerResponse,
	)
	if result.Status != http.StatusOK || result.Out == nil {
		message := chainErrorMessage(result)
		if message == "" {
			message = "upstream returned no response"
		}
		s.record(request.Context(), model.HistoryEntry{
			TS: time.Now().UnixMilli(), Model: modelID, MS: time.Since(started).Milliseconds(),
			Stream: stream, Kind: "compact", Effort: recordedEffort(bridgeContext.MappedReasoningEffort, chatBody),
			Usage:           result.Usage,
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

	if err != nil {
		message := err.Error()
		s.record(request.Context(), model.HistoryEntry{
			TS: time.Now().UnixMilli(), Model: modelID, MS: time.Since(started).Milliseconds(),
			Stream: stream, Kind: "compact", Effort: recordedEffort(bridgeContext.MappedReasoningEffort, chatBody),
			Usage:           result.Usage,
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
		MissingSummarySections: compactionMissingSections(compaction),
		Degraded:               result.Degraded,
		DegradeReason:          result.DegradeReason,
	}
	applyReasoningEffort(&entry, bridgeContext.MappedReasoningEffort, bridgeContext.RequestedReasoningEffort, chatBody)
	applyChatStats(&entry, result.Out, entry.MS)
	entry.Usage = result.Usage
	s.record(request.Context(), entry)
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
