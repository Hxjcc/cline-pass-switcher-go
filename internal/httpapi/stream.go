package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	responsesbridge "github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/responses"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/upstream"
)

var (
	streamProviderRE  = regexp.MustCompile(`"finalProvider":"([^"]+)"`)
	streamCanonicalRE = regexp.MustCompile(`"canonicalSlug":"([^"]+)"`)
)

func (s *Server) handleStreamingChat(writer http.ResponseWriter, request *http.Request, modelID string, body map[string]any, modelConfig model.PerModelConfig) {
	attempts := s.upstream.BuildAttempts(modelID, modelConfig)
	targets := attemptTargets(attempts)
	var last chainResult
	last.Status = http.StatusBadGateway
	last.Started = time.Now()

	for _, attempt := range attempts {
		started := time.Now()
		// No overall deadline: reasoning responses legitimately stream for
		// minutes. StartStreamAttempt applies a first-event budget and then an
		// idle (silence) budget so long but healthy streams survive.
		result := s.upstream.StartStreamAttempt(request.Context(), modelID, body, attempt)
		if isBufferedCompletion(result) {
			last.Trace = append(last.Trace, model.Trace{
				Upstream: attempt.Upstream,
				Status:   http.StatusOK,
				MS:       time.Since(started).Milliseconds(),
				Note:     "buffered completion",
			})
			s.writeBufferedChatStream(writer, targets, last, result, modelID, body)
			return
		}
		if !result.SSE {
			message := result.NetErr
			if message == "" {
				message = extractAttemptError(result.Out)
			}
			trace := model.Trace{
				Upstream: attempt.Upstream,
				Status:   result.Status,
				MS:       time.Since(started).Milliseconds(),
				Note:     truncate(message, 160),
			}
			last.Trace = append(last.Trace, trace)
			last.Status = result.Status
			last.Out = result.Out
			last.NetErr = result.NetErr
			last.Account = result.Account
			s.upstream.LearnFailure(modelID, attempt, message)
			if s.stopFailover(result.Status) {
				break
			}
			continue
		}

		last.Trace = append(last.Trace, model.Trace{
			Upstream: attempt.Upstream,
			Status:   http.StatusOK,
			MS:       time.Since(started).Milliseconds(),
			Note:     "stream",
		})
		contentType := result.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "text/event-stream"
		}
		writer.Header().Set("Content-Type", contentType)
		writer.Header().Set("Cache-Control", "no-cache")
		writer.Header().Set("Connection", "keep-alive")
		writer.Header().Set("X-Cline-Target-Upstream", targetHeader(targets))
		writer.Header().Set("X-Cline-Attempts", strconv.Itoa(len(last.Trace)))
		writer.Header().Set("X-Cline-Account", headerSafe(result.Account.Name))
		writer.WriteHeader(http.StatusOK)

		tap := &streamTapWriter{writer: writer}
		rewriter := &sseJSONRewriter{}
		stats := newStreamStats(started)
		writeChunk := func(data []byte) error {
			stats.Observe(data)
			rewritten := rewriter.push(data, rewriteChatReasoningBlock)
			if len(rewritten) == 0 {
				return nil
			}
			_, err := tap.Write(rewritten)
			return err
		}
		if len(result.FirstChunk) > 0 {
			_ = writeChunk(result.FirstChunk)
		}
		copyErr := consumeStream(
			request.Context(),
			result.Body,
			streamKeepaliveInterval(s.upstream.StreamIdleTimeout()),
			nil,
			writeChunk,
			func() error { return writeSSEKeepalive(tap) },
		)
		if leftover := rewriter.flush(); len(leftover) > 0 && copyErr == nil {
			_, copyErr = tap.Write(leftover)
		}
		_ = result.Body.Close()

		provider, canonical := parseStreamRouting(tap.tailText())
		provider = s.upstream.CanonicalProvider(modelID, provider)
		errorMessage := (*string)(nil)
		if copyErr != nil {
			message := copyErr.Error()
			errorMessage = &message
		}
		entry := model.HistoryEntry{
			TS:        time.Now().UnixMilli(),
			Model:     modelID,
			Provider:  provider,
			Canonical: canonical,
			MS:        time.Since(last.Started).Milliseconds(),
			Stream:    true,
			Kind:      "chat",
			Effort:    effortFromChatBody(body),
			Error:     errorMessage,
			Account:   result.Account.Name,
			Attempts:  traceUpstreams(last.Trace),
			Trace:     last.Trace,
		}
		applyStreamStats(&entry, stats)
		_ = s.store.Record(entry)
		return
	}

	if last.Out == nil {
		last.Out = map[string]any{
			"error": map[string]any{"message": "upstream returned no response", "type": "upstream_error"},
		}
	}
	writeJSON(writer, last.Status, last.Out)
}

// writeBufferedChatStream serves a client that asked for SSE when the upstream
// answered with a plain JSON completion: the completion becomes one chunk
// followed by [DONE], so the client still gets the protocol it requested.
func (s *Server) writeBufferedChatStream(
	writer http.ResponseWriter,
	targets []string,
	last chainResult,
	result upstream.StreamAttemptResult,
	modelID string,
	body map[string]any,
) {
	routing := s.upstream.RoutingFor(modelID, result.Out)
	responsesbridge.AliasChatReasoning(result.Out)
	raw, err := json.Marshal(responsesbridge.ChatCompletionAsChunk(result.Out))
	if err != nil {
		writeJSON(writer, http.StatusBadGateway, map[string]any{
			"error": map[string]any{"message": err.Error(), "type": "upstream_error"},
		})
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Cline-Target-Upstream", targetHeader(targets))
	writer.Header().Set("X-Cline-Actual-Upstream", firstNonEmpty(routing.FinalProvider, "unknown"))
	writer.Header().Set("X-Cline-Canonical-Model", routing.CanonicalSlug)
	writer.Header().Set("X-Cline-Attempts", strconv.Itoa(len(last.Trace)))
	writer.Header().Set("X-Cline-Account", headerSafe(result.Account.Name))
	writer.WriteHeader(http.StatusOK)
	_, writeErr := io.WriteString(writer, "data: "+string(raw)+"\n\ndata: [DONE]\n\n")
	if flusher, ok := writer.(http.Flusher); ok {
		flusher.Flush()
	}

	errorMessage := (*string)(nil)
	if writeErr != nil {
		message := writeErr.Error()
		errorMessage = &message
	}
	entry := model.HistoryEntry{
		TS:        time.Now().UnixMilli(),
		Model:     modelID,
		Provider:  routing.FinalProvider,
		Canonical: routing.CanonicalSlug,
		MS:        time.Since(last.Started).Milliseconds(),
		Stream:    true,
		Kind:      "chat",
		Effort:    effortFromChatBody(body),
		Error:     errorMessage,
		Account:   result.Account.Name,
		Attempts:  traceUpstreams(last.Trace),
		Trace:     last.Trace,
	}
	applyChatStats(&entry, result.Out, entry.MS)
	_ = s.store.Record(entry)
}

type streamTapWriter struct {
	writer http.ResponseWriter
	tail   []byte
}

func (writer *streamTapWriter) Write(data []byte) (int, error) {
	const maxTail = 128 << 10
	writer.tail = append(writer.tail, data...)
	if len(writer.tail) > maxTail {
		writer.tail = writer.tail[len(writer.tail)-maxTail:]
	}
	count, err := writer.writer.Write(data)
	if flusher, ok := writer.writer.(http.Flusher); ok {
		flusher.Flush()
	}
	return count, err
}

func (writer *streamTapWriter) tailText() string {
	return string(writer.tail)
}

func parseStreamRouting(text string) (string, string) {
	lines := strings.Split(text, "\n")
	for index := len(lines) - 1; index >= 0 && index >= len(lines)-20; index-- {
		line := strings.TrimSpace(lines[index])
		if !strings.HasPrefix(line, "data:") || strings.Contains(line, "[DONE]") {
			continue
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &chunk); err != nil {
			continue
		}
		if provider, ok := chunk["provider"].(string); ok && provider != "" {
			canonical, _ := chunk["model"].(string)
			return strings.ToLower(strings.ReplaceAll(provider, " ", "-")), canonical
		}
	}
	provider := ""
	canonical := ""
	if match := streamProviderRE.FindStringSubmatch(text); len(match) > 1 {
		provider = match[1]
	}
	if match := streamCanonicalRE.FindStringSubmatch(text); len(match) > 1 {
		canonical = match[1]
	}
	return provider, canonical
}

type sseJSONRewriter struct {
	buf string
}

func (rewriter *sseJSONRewriter) push(data []byte, rewrite func(string) string) []byte {
	rewriter.buf += string(data)
	rewriter.buf = strings.ReplaceAll(rewriter.buf, "\r\n", "\n")
	var out strings.Builder
	for {
		index := strings.Index(rewriter.buf, "\n\n")
		if index < 0 {
			break
		}
		block := rewriter.buf[:index]
		rewriter.buf = rewriter.buf[index+2:]
		out.WriteString(rewrite(block))
		out.WriteString("\n\n")
	}
	return []byte(out.String())
}

func (rewriter *sseJSONRewriter) flush() []byte {
	leftover := rewriter.buf
	rewriter.buf = ""
	if leftover == "" {
		return nil
	}
	return []byte(leftover)
}

func rewriteChatReasoningBlock(block string) string {
	dataLines := make([]string, 0, 1)
	other := make([]string, 0)
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			continue
		}
		other = append(other, line)
	}
	data := strings.Join(dataLines, "\n")
	if data == "" || data == "[DONE]" {
		return block
	}
	var chunk map[string]any
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return block
	}
	if !responsesbridge.AliasChatReasoning(chunk) {
		return block
	}
	raw, err := json.Marshal(chunk)
	if err != nil {
		return block
	}
	rebuilt := strings.Join(other, "\n")
	if rebuilt != "" {
		rebuilt += "\n"
	}
	return rebuilt + "data: " + string(raw)
}
