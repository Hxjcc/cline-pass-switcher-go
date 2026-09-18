package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	responsesbridge "github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/responses"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/upstream"
)

const (
	streamShareLinger        = 400 * time.Millisecond
	streamClientWriteTimeout = 30 * time.Second
)

type sharePayload struct {
	data []byte
	ping bool
}

type streamShareHub struct {
	mu     sync.Mutex
	jobs   map[string]*sharedResponsesStream
	budget *replayBudget
}

func newStreamShareHub() *streamShareHub {
	return &streamShareHub{jobs: map[string]*sharedResponsesStream{}, budget: &replayBudget{limits: defaultReplayLimits()}}
}

// responsesShareKey identifies requests that may share one upstream stream.
// Include the original Responses semantics, the effective Chat generation,
// and routing preferences. Conversion alone is lossy (e.g. tool namespaces and
// metadata), so identical Chat bodies need not produce identical Responses.
func responsesShareKey(modelID string, requestBody, chatBody map[string]any, modelConfig model.PerModelConfig) string {
	withoutTransport := func(body map[string]any) map[string]any {
		view := make(map[string]any, len(body))
		for key, value := range body {
			switch key {
			case "stream", "stream_options":
				continue
			}
			view[key] = value
		}
		return view
	}
	view := map[string]any{
		"model": modelID, "responses": withoutTransport(requestBody),
		"chat": withoutTransport(chatBody), "routing": modelConfig,
	}
	raw, _ := json.Marshal(view)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:16])
}

// sharedResponsesStream fans one upstream stream out to every client that
// joined with the same key. Fields below `ready` are written by the run
// goroutine before markReady and only read by handlers afterwards; the
// outcome of the stream (routing, errors, usage) is recorded by the run
// goroutine itself so handlers never race with it.
type sharedResponsesStream struct {
	hub *streamShareHub
	key string

	ctx    context.Context
	cancel context.CancelFunc

	replay       *replayLog
	refs         int
	runDone      bool
	events       *responsesbridge.EventWriter
	nextSequence atomic.Int64
	keepalive    time.Duration

	ready chan struct{}
	last  chainResult
	okSSE bool

	targets   []string
	effort    string
	provider  string
	canonical string
	aborted   bool
	stats     *streamStats
	outcome   responsesbridge.Outcome
	readError string
}

func (h *streamShareHub) join(key string, run func(*sharedResponsesStream)) *sharedResponsesStream {
	h.mu.Lock()
	job := h.jobs[key]
	started := false
	if job == nil || job.runDone || job.ctx.Err() != nil {
		ctx, cancel := context.WithCancel(context.Background())
		job = &sharedResponsesStream{
			hub:       h,
			key:       key,
			ctx:       ctx,
			cancel:    cancel,
			ready:     make(chan struct{}),
			stats:     newStreamStats(time.Now()),
			replay:    newReplayLog(h.budget),
			keepalive: maxStreamKeepaliveInterval,
		}
		job.events = responsesbridge.NewEventWriter(job.replay)
		h.jobs[key] = job
		started = true
	}
	job.refs++
	h.mu.Unlock()
	if started {
		go func() {
			run(job)
			job.finish()
		}()
	}
	return job
}

func (j *sharedResponsesStream) release() {
	j.hub.mu.Lock()
	j.refs--
	refs := j.refs
	done := j.runDone
	j.maybeRemoveLocked()
	j.hub.mu.Unlock()
	if refs == 0 && !done {
		go func() {
			time.Sleep(streamShareLinger)
			j.hub.mu.Lock()
			if j.refs == 0 && !j.runDone {
				j.cancel()
			}
			j.hub.mu.Unlock()
		}()
	}
}

func (j *sharedResponsesStream) maybeRemoveLocked() {
	if j.runDone && j.refs == 0 {
		if j.hub.jobs[j.key] == j {
			delete(j.hub.jobs, j.key)
		}
		j.replay.dispose()
	}
}

func (j *sharedResponsesStream) markReady() {
	select {
	case <-j.ready:
	default:
		close(j.ready)
	}
}

// Only the upstream runner observes the outcome; subscribers receive immutable
// wire bytes. Every subscriber replays from zero, so sequence numbers and JSON
// encoding are shared too. Heartbeats are generated separately by subscribers.
func (j *sharedResponsesStream) publishEvents(events []responsesbridge.Event) error {
	for _, event := range events {
		if err := j.events.Write(event); err != nil {
			j.replay.abort(err)
			return err
		}
		j.nextSequence.Add(1)
	}
	if outcome := responsesbridge.OutcomeFromEvents(events); outcome.Status != "" {
		j.outcome = outcome
	}
	return nil
}

func (j *sharedResponsesStream) finish() {
	j.replay.finish()
	j.hub.mu.Lock()
	j.runDone = true
	j.cancel()
	j.maybeRemoveLocked()
	j.hub.mu.Unlock()
}

type shareSub struct {
	job    *sharedResponsesStream
	offset int64
}

func (j *sharedResponsesStream) subscribe() *shareSub {
	return &shareSub{job: j}
}

func (s *shareSub) next(ctx context.Context) (sharePayload, bool, error) {
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			return sharePayload{}, false, err
		}
		data, changed, err := s.job.replay.read(s.offset)
		if errors.Is(err, io.EOF) {
			return sharePayload{}, false, nil
		}
		if err != nil {
			return sharePayload{}, false, err
		}
		if len(data) > 0 {
			s.offset += int64(len(data))
			return sharePayload{data: data}, true, nil
		}
		if timer == nil {
			timer = time.NewTimer(s.job.keepalive)
		}
		select {
		case <-ctx.Done():
			return sharePayload{}, false, ctx.Err()
		case <-changed:
		case <-timer.C:
			return sharePayload{ping: true}, true, nil
		}
	}
}

func (s *Server) handleStreamingResponses(
	writer http.ResponseWriter,
	request *http.Request,
	requestBody map[string]any,
	chatBody map[string]any,
	bridgeContext *responsesbridge.Context,
	modelID string,
	modelConfig model.PerModelConfig,
) {
	job := s.shares.join(responsesShareKey(modelID, requestBody, chatBody, modelConfig), func(job *sharedResponsesStream) {
		s.runSharedResponses(job, chatBody, bridgeContext, modelID, modelConfig)
	})
	defer job.release()

	select {
	case <-job.ready:
	case <-request.Context().Done():
		return
	}

	if !job.okSSE {
		status := job.last.Status
		if status < 400 {
			status = http.StatusBadGateway
		}
		if job.last.Out != nil {
			writeJSON(writer, status, job.last.Out)
			return
		}
		writeJSON(writer, status, map[string]any{"error": map[string]any{
			"message": "upstream returned no response", "type": "upstream_error",
		}})
		return
	}

	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	setResponsesHeaders(writer, job.targets, job.last, job.effort)
	writer.WriteHeader(http.StatusOK)

	controller := http.NewResponseController(writer)
	defer controller.SetWriteDeadline(time.Time{})
	sub := job.subscribe()
	for {
		payload, ok, err := sub.next(request.Context())
		if err != nil {
			if request.Context().Err() == nil {
				_ = controller.SetWriteDeadline(time.Now().Add(streamClientWriteTimeout))
				_ = responsesbridge.WriteEvent(writer, responsesbridge.Event{Type: "error", Data: map[string]any{
					"type": "error", "code": "replay_storage_error", "message": err.Error(),
					"sequence_number": job.nextSequence.Load(),
				}})
				_ = controller.Flush()
			}
			return
		}
		if !ok {
			return
		}
		_ = controller.SetWriteDeadline(time.Now().Add(streamClientWriteTimeout))
		var writeErr error
		if payload.ping {
			_, writeErr = io.WriteString(writer, ": ping\n\n")
		} else {
			_, writeErr = writer.Write(payload.data)
		}
		if writeErr == nil {
			writeErr = controller.Flush()
		}
		if writeErr != nil {
			return
		}
	}
}

// recordSharedRun writes the single history entry for one upstream stream.
// It runs on the goroutine that drove the stream, after it has ended, so
// usage is counted once no matter how many clients were attached.
func (s *Server) recordSharedRun(
	job *sharedResponsesStream,
	modelID string,
	chatBody map[string]any,
	bridgeContext *responsesbridge.Context,
) {
	errorMessage := (*string)(nil)
	switch {
	case !job.okSSE:
		message := friendlyCancelText(chainErrorMessage(job.last))
		if job.ctx.Err() != nil {
			message = "客户端取消"
		} else if message == "" {
			message = "upstream returned no response"
		}
		errorMessage = &message
	case job.aborted:
		message := "客户端取消"
		errorMessage = &message
	case job.replay.failure() != nil:
		message := job.replay.failure().Error()
		errorMessage = &message
	case job.outcome.ErrorMessage() != "":
		message := friendlyCancelText(job.outcome.ErrorMessage())
		errorMessage = &message
	case job.readError != "":
		message := friendlyCancelText(job.readError)
		errorMessage = &message
	}
	entry := model.HistoryEntry{
		TS: time.Now().UnixMilli(), Model: modelID, Provider: job.provider, Canonical: job.canonical,
		MS: time.Since(job.last.Started).Milliseconds(), Stream: true, Kind: "responses",
		Error: errorMessage, Account: job.last.Account.Name,
		Attempts: traceUpstreams(job.last.Trace), Trace: job.last.Trace,
	}
	applyReasoningEffort(&entry, bridgeContext.MappedReasoningEffort, bridgeContext.RequestedReasoningEffort, chatBody)
	if job.okSSE {
		applyStreamStats(&entry, job.stats)
	}
	_ = s.store.Record(entry)
}

func (s *Server) runSharedResponses(
	job *sharedResponsesStream,
	chatBody map[string]any,
	bridgeContext *responsesbridge.Context,
	modelID string,
	modelConfig model.PerModelConfig,
) {
	attempts := s.upstream.BuildAttempts(modelID, modelConfig)
	job.targets = attemptTargets(attempts)
	job.effort = bridgeContext.MappedReasoningEffort
	job.keepalive = streamKeepaliveInterval(s.upstream.StreamIdleTimeout())
	job.last = chainResult{Status: http.StatusBadGateway, Started: time.Now()}
	recorded := false
	record := func() {
		if !recorded {
			recorded = true
			s.recordSharedRun(job, modelID, chatBody, bridgeContext)
		}
	}
	defer record()

	for _, attempt := range attempts {
		started := time.Now()
		result := s.upstream.StartStreamAttempt(job.ctx, modelID, chatBody, attempt)
		if isBufferedCompletion(result) {
			events, err := responsesbridge.EventsFromChat(result.Out, bridgeContext)
			if err == nil {
				s.publishBufferedResponses(job, modelID, result, attempt, started, events)
				return
			}
			result.Status = http.StatusBadGateway
			result.NetErr = err.Error()
			result.Out = map[string]any{"error": map[string]any{"message": err.Error(), "type": "upstream_error"}}
		}
		if !result.SSE {
			message := result.NetErr
			if message == "" {
				message = extractAttemptError(result.Out)
			}
			job.last.Trace = append(job.last.Trace, model.Trace{
				Upstream: attempt.Upstream, Status: result.Status,
				MS: time.Since(started).Milliseconds(), Note: truncate(message, 160),
			})
			job.last.Status, job.last.Out, job.last.NetErr, job.last.Account = result.Status, result.Out, result.NetErr, result.Account
			s.upstream.LearnFailure(modelID, attempt, message)
			if s.stopFailover(result.Status) {
				break
			}
			continue
		}

		job.last.Trace = append(job.last.Trace, model.Trace{
			Upstream: attempt.Upstream, Status: http.StatusOK,
			MS: time.Since(started).Milliseconds(), Note: "responses stream",
		})
		job.last.Account = result.Account
		job.last.Status = http.StatusOK
		job.last.Out, job.last.NetErr = nil, ""
		job.okSSE = true
		job.stats = newStreamStats(started)

		adapter := responsesbridge.NewStreamAdapter(bridgeContext)
		rawTail := make([]byte, 0, 32<<10)
		process := func(data []byte) error {
			job.stats.Observe(data)
			rawTail = appendRawTail(rawTail, data)
			events := adapter.Feed(data)
			return job.publishEvents(events)
		}
		readErr := process(result.FirstChunk)
		job.markReady()
		if readErr == nil && !adapter.Completed() {
			readErr = consumeStream(
				job.ctx,
				result.Body,
				0,
				adapter.Completed,
				process,
				nil,
			)
		}
		_ = result.Body.Close()
		// Check cancellation before Finish marks the adapter terminal.
		job.aborted = job.ctx.Err() != nil && !adapter.Completed()
		finish := adapter.Finish(readErr)
		if err := job.publishEvents(finish); err != nil && readErr == nil {
			readErr = err
		}
		if job.outcome.Status == "completed" || job.outcome.Status == "incomplete" {
			// A terminal sentinel may have been buffered without its trailing
			// delimiter when cancellation arrived. Finish can still validate it.
			job.aborted = false
		}
		job.provider, job.canonical = parseStreamRouting(string(rawTail))
		job.provider = s.upstream.CanonicalProvider(modelID, job.provider)
		if readErr != nil && job.ctx.Err() == nil {
			job.readError = readErr.Error()
		}
		return
	}
	// Every channel failed: the error is what clients will see, so it must be
	// in the history before they are released.
	record()
	job.markReady()
}

// isBufferedCompletion reports an upstream that ignored stream:true and
// answered with a complete JSON completion. StartStreamAttempt only returns
// a 200 without SSE for that case.
func isBufferedCompletion(result upstream.StreamAttemptResult) bool {
	return !result.SSE && result.Status == http.StatusOK && result.Out != nil &&
		len(statsSlice(result.Out["choices"])) > 0
}

// publishBufferedResponses replays a buffered completion as the full
// Responses event lifecycle instead of failing the attempt.
func (s *Server) publishBufferedResponses(
	job *sharedResponsesStream,
	modelID string,
	result upstream.StreamAttemptResult,
	attempt upstream.Attempt,
	started time.Time,
	events []responsesbridge.Event,
) {
	job.last.Trace = append(job.last.Trace, model.Trace{
		Upstream: attempt.Upstream, Status: http.StatusOK,
		MS: time.Since(started).Milliseconds(), Note: "buffered completion",
	})
	job.last.Account = result.Account
	job.last.Status = http.StatusOK
	job.last.NetErr = ""
	job.last.Out = result.Out
	job.last.Routing = s.upstream.RoutingFor(modelID, result.Out)
	job.provider, job.canonical = job.last.Routing.FinalProvider, job.last.Routing.CanonicalSlug
	job.okSSE = true
	job.stats = newStreamStats(started)
	if raw, err := json.Marshal(result.Out); err == nil {
		job.stats.Observe(append(append([]byte("data: "), raw...), '\n', '\n'))
	}
	job.last.Out = nil
	job.markReady()
	if err := job.publishEvents(events); err != nil {
		job.readError = err.Error()
	}
}
