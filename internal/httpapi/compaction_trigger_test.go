package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	responsesbridge "github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/responses"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/store"
)

const compactionChatBody = `{"id":"chatcmpl-cmp","created":1,"model":"cline-pass/test","choices":[{"index":0,"message":{"role":"assistant","content":"condensed history"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`

// upstreamRecorder captures the chat requests the proxy forwards, so tests can
// assert what the compaction pipeline actually sent.
type upstreamRecorder struct {
	mu       sync.Mutex
	payloads []map[string]any
}

func (recorder *upstreamRecorder) add(payload map[string]any) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.payloads = append(recorder.payloads, payload)
}

func (recorder *upstreamRecorder) all() []map[string]any {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]map[string]any(nil), recorder.payloads...)
}

func newCompactionRecorderServer(t *testing.T) (*upstreamRecorder, *httptest.Server) {
	t.Helper()
	recorder := &upstreamRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(request.Body).Decode(&payload)
		recorder.add(payload)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, compactionChatBody)
	}))
	t.Cleanup(server.Close)
	return recorder, server
}

func configureCompactionServer(t *testing.T, baseURL string) (*Server, *store.Store) {
	t.Helper()
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = baseURL
		config.Accounts = []model.Account{{Name: "main", Key: "cline-key", Enabled: true}}
		config.KnownModels = []string{"cline-pass/test"}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) {
		meta.Reasoning = true
		meta.ReasoningEfforts = []string{"low", "high", "max"}
	}); err != nil {
		t.Fatal(err)
	}
	return server, st
}

// Remote compaction v2: Codex sends an ordinary /responses request with a
// compaction_trigger item and expects exactly one compaction output item plus a
// response.completed event over the normal lifecycle.
func TestResponsesCompactionTriggerStreamsSingleCompactionItem(t *testing.T) {
	recorder, upstreamServer := newCompactionRecorderServer(t)
	server, st := configureCompactionServer(t, upstreamServer.URL)

	body := `{"model":"cline-pass/test","stream":true,"reasoning":{"effort":"max"},"input":[
	  {"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]},
	  {"type":"compaction_trigger"}]}`
	request := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("compaction trigger failed: %d %s", response.Code, response.Body.String())
	}
	stream := response.Body.String()
	for _, expected := range []string{
		"event: response.created",
		"event: response.output_item.done",
		"event: response.completed",
		`"type":"compaction"`,
		`"encrypted_content":"ocx1:`,
	} {
		if !strings.Contains(stream, expected) {
			t.Fatalf("missing %q in remote compaction stream: %s", expected, stream)
		}
	}
	// Codex counts output_item.done events: exactly one, and it must be the
	// compaction item. The item legitimately appears again in the completed
	// response and in the item.added event.
	if count := strings.Count(stream, "event: response.output_item.done"); count != 1 {
		t.Fatalf("expected exactly one output_item.done event, saw %d", count)
	}
	if strings.Contains(stream, "compaction_trigger") {
		t.Fatalf("compaction marker leaked into the response stream: %s", stream)
	}
	// The proxy must have asked the upstream for a summary, not a normal turn.
	payloads := recorder.all()
	if len(payloads) != 1 {
		t.Fatalf("expected one upstream call, got %d", len(payloads))
	}
	raw, _ := json.Marshal(payloads[0]["messages"])
	if !strings.Contains(string(raw), "compaction task") {
		t.Fatalf("upstream request was not a compaction summary request: %s", raw)
	}
	// Compaction runs at the model's strongest level by default.
	if payloads[0]["reasoning_effort"] != "max" {
		t.Fatalf("compaction should use the strongest reasoning level, got %#v", payloads[0]["reasoning_effort"])
	}
	if tokens, ok := payloads[0]["max_tokens"].(float64); !ok || tokens < model.DefaultCompactionMinOutputTokens {
		t.Fatalf("compaction must ask for at least %d output tokens, got %#v", model.DefaultCompactionMinOutputTokens, payloads[0]["max_tokens"])
	}

	history := st.Metadata().History
	if len(history) != 1 || history[0].Kind != "compact" || history[0].Error != nil || !history[0].Stream {
		t.Fatalf("remote compaction should be recorded as a compact history entry: %#v", history)
	}
}

func TestResponsesCompactionTriggerBufferedReturnsNormalResponse(t *testing.T) {
	_, upstreamServer := newCompactionRecorderServer(t)
	server, _ := configureCompactionServer(t, upstreamServer.URL)

	body := `{"model":"cline-pass/test","input":[{"type":"compaction_trigger"}]}`
	request := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("buffered compaction trigger failed: %d %s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	output, _ := payload["output"].([]any)
	if payload["object"] != "response" || payload["status"] != "completed" || len(output) != 1 {
		t.Fatalf("expected one normal response with a single compaction item: %#v", payload)
	}
	item, _ := output[0].(map[string]any)
	if item["type"] != "compaction" || !strings.HasPrefix(item["encrypted_content"].(string), "ocx1:") {
		t.Fatalf("unexpected compaction item: %#v", item)
	}
}

// A summary that starved on hidden reasoning is retried once at the model's top
// effort with a doubled budget, and the retry is what the client receives.
func TestResponsesCompactionTriggerEscalatesWhenSummaryStarves(t *testing.T) {
	recorder := &upstreamRecorder{}
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(request.Body).Decode(&payload)
		recorder.add(payload)
		writer.Header().Set("Content-Type", "application/json")
		if len(recorder.all()) == 1 {
			writer.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(writer, `{"error":"empty response content","success":false}`)
			return
		}
		_, _ = io.WriteString(writer, compactionChatBody)
	}))
	defer upstreamServer.Close()

	server, st := configureCompactionServer(t, upstreamServer.URL)
	body := `{"model":"cline-pass/test","stream":true,"reasoning":{"effort":"max"},"input":[
	  {"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]},
	  {"type":"compaction_trigger"}]}`
	request := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "event: response.completed") {
		t.Fatalf("escalated compaction should succeed: %d %s", response.Code, response.Body.String())
	}
	payloads := recorder.all()
	if len(payloads) != 2 {
		t.Fatalf("expected the starved attempt plus one retry, got %d calls", len(payloads))
	}
	if payloads[0]["reasoning_effort"] != "max" {
		t.Fatalf("first pass should already run at max, got %#v", payloads[0]["reasoning_effort"])
	}
	if payloads[1]["reasoning_effort"] != "max" {
		t.Fatalf("retry keeps the strongest level, got %#v", payloads[1]["reasoning_effort"])
	}
	first, _ := payloads[0]["max_tokens"].(float64)
	retry, _ := payloads[1]["max_tokens"].(float64)
	if retry <= first {
		t.Fatalf("retry should get a bigger budget: %v -> %v", first, retry)
	}
	if retry < 32768 {
		t.Fatalf("the single retry is the last pass before degrading and should reach the ceiling: %v", retry)
	}
	history := st.Metadata().History
	if len(history) != 1 || history[0].Kind != "compact" || history[0].Error != nil || len(history[0].Trace) < 2 {
		t.Fatalf("both passes belong to one successful history entry: %#v", history)
	}
}

// When even the escalated pass fails, the client still receives a valid
// compaction item so the session can continue instead of stranding it above
// its context limit.
func TestResponsesCompactionTriggerDegradesWhenSummaryKeepsFailing(t *testing.T) {
	recorder := &upstreamRecorder{}
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(request.Body).Decode(&payload)
		recorder.add(payload)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(writer, `{"error":"empty response content","success":false}`)
	}))
	defer upstreamServer.Close()

	server, st := configureCompactionServer(t, upstreamServer.URL)
	body := `{"model":"cline-pass/test","stream":true,"reasoning":{"effort":"max"},"input":[
	  {"type":"message","role":"user","content":[{"type":"input_text","text":"finish the parser fix in internal/parse.go"}]},
	  {"type":"compaction_trigger"}]}`
	request := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "event: response.completed") {
		t.Fatalf("degraded compaction should still complete: %d %s", response.Code, response.Body.String())
	}
	payload := decodeCompactionEnvelope(t, extractCompactionEnvelope(t, response.Body.String()))
	if !payload.Degraded {
		t.Fatalf("degraded payload must be marked: %#v", payload)
	}
	for _, expected := range []string{"compaction degraded", "## Objective", "## Work State", "## Next Move", "## Relevant Files"} {
		if !strings.Contains(payload.Summary, expected) {
			t.Fatalf("degraded summary is missing %q:\n%s", expected, payload.Summary)
		}
	}
	if len(payload.Recent) != 1 || !strings.Contains(payload.Recent[0].Text, "parser fix") || payload.Recent[0].Role != "user" {
		t.Fatalf("degraded payload should keep the recent user request verbatim: %#v", payload.Recent)
	}
	if calls := len(recorder.all()); calls != 2 {
		t.Fatalf("starvation should escalate once before degrading, got %d calls", calls)
	}
	history := st.Metadata().History
	if len(history) != 1 || history[0].Kind != "compact" || history[0].Error != nil || len(history[0].Trace) != 2 {
		t.Fatalf("degraded compaction should be one successful entry with both passes traced: %#v", history)
	}
}

// A failure that another pass cannot fix (gateway 503) degrades immediately
// instead of burning a second expensive call.
func TestResponsesCompactionDegradesWithoutEscalatingOnGatewayFailure(t *testing.T) {
	recorder := &upstreamRecorder{}
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(request.Body).Decode(&payload)
		recorder.add(payload)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(writer, `{"error":{"message":"gateway unavailable","type":"server_error"}}`)
	}))
	defer upstreamServer.Close()

	server, _ := configureCompactionServer(t, upstreamServer.URL)
	body := `{"model":"cline-pass/test","input":[{"type":"compaction_trigger"}]}`
	request := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("degraded compaction should succeed: %d %s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	output, _ := payload["output"].([]any)
	item, _ := output[0].(map[string]any)
	payload2 := decodeCompactionEnvelope(t, item["encrypted_content"].(string))
	if !strings.Contains(payload2.Summary, "gateway unavailable") {
		t.Fatalf("degraded summary should carry the failure reason: %s", payload2.Summary)
	}
	if calls := len(recorder.all()); calls != 1 {
		t.Fatalf("a gateway failure should not escalate, got %d calls", calls)
	}
}

func decodeCompactionEnvelope(t *testing.T, envelope string) responsesbridge.CompactionPayload {
	t.Helper()
	payload, ok := responsesbridge.DecodeCompactionEnvelope(envelope)
	if !ok {
		t.Fatalf("not a readable compaction envelope: %q", envelope)
	}
	return payload
}

// The compaction item the proxy returns must be usable in the next request:
// the summary is decoded and replayed as context for the next turn.
func TestCompactionTriggerItemReplaysAsSummary(t *testing.T) {
	recorder, upstreamServer := newCompactionRecorderServer(t)
	server, _ := configureCompactionServer(t, upstreamServer.URL)

	trigger := `{"model":"cline-pass/test","stream":true,"input":[
	  {"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]},
	  {"type":"compaction_trigger"}]}`
	request := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(trigger))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("compaction trigger failed: %d %s", response.Code, response.Body.String())
	}
	envelope := extractCompactionEnvelope(t, response.Body.String())

	replay := `{"model":"cline-pass/test","input":[
	  {"type":"compaction","encrypted_content":"` + envelope + `"},
	  {"type":"message","role":"user","content":[{"type":"input_text","text":"continue"}]}]}`
	request = localRequest(http.MethodPost, "/v1/responses", strings.NewReader(replay))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("replay failed: %d %s", response.Code, response.Body.String())
	}

	payloads := recorder.all()
	if len(payloads) != 2 {
		t.Fatalf("expected two upstream calls, got %d", len(payloads))
	}
	raw, _ := json.Marshal(payloads[1]["messages"])
	if !strings.Contains(string(raw), "condensed history") || !strings.Contains(string(raw), "Earlier conversation was compacted") {
		t.Fatalf("compaction summary was not replayed: %s", raw)
	}
	// The verbatim tail rides between the summary and the new user message.
	replayed := string(raw)
	summaryAt := strings.Index(replayed, "Earlier conversation was compacted")
	recentAt := strings.Index(replayed, "hello")
	requestAt := strings.Index(replayed, "continue")
	if summaryAt < 0 || recentAt < 0 || requestAt < 0 || !(summaryAt < recentAt && recentAt < requestAt) {
		t.Fatalf("replay order must be summary -> recent turns -> new input: %s", replayed)
	}
}

func extractCompactionEnvelope(t *testing.T, stream string) string {
	t.Helper()
	for _, block := range strings.Split(stream, "\n\n") {
		data := ""
		for _, line := range strings.Split(block, "\n") {
			if strings.HasPrefix(line, "data:") {
				data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
		if data == "" || !strings.HasPrefix(data, "{") {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		item, _ := event["item"].(map[string]any)
		if item == nil || item["type"] != "compaction" {
			continue
		}
		if envelope, ok := item["encrypted_content"].(string); ok {
			return envelope
		}
	}
	t.Fatalf("no compaction item in stream: %s", stream)
	return ""
}

// A summary that ends cleanly but drops anchored sections still compacts - the
// context does shrink - yet the omission is worth seeing, so it lands in the
// history as an advisory note instead of triggering another pass.
func TestResponsesCompactionRecordsMissingSummarySections(t *testing.T) {
	recorder, upstreamServer := newCompactionRecorderServer(t)
	server, st := configureCompactionServer(t, upstreamServer.URL)

	body := `{"model":"cline-pass/test","input":[
	  {"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]},
	  {"type":"compaction_trigger"}]}`
	request := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("compaction trigger failed: %d %s", response.Code, response.Body.String())
	}

	// The fixture summary ("condensed history") carries none of the four
	// anchored sections.
	history := st.Metadata().History
	if len(history) != 1 || history[0].Error != nil {
		t.Fatalf("compaction should be one successful entry: %#v", history)
	}
	want := []string{"Objective", "Work State", "Next Move", "Relevant Files"}
	if !slices.Equal(history[0].MissingSummarySections, want) {
		t.Fatalf("missing sections not recorded: %#v", history[0].MissingSummarySections)
	}
	if calls := len(recorder.all()); calls != 1 {
		t.Fatalf("a thin summary must not trigger another pass, got %d calls", calls)
	}
}

// A summary carrying all four anchored sections is accepted without a note.
func TestResponsesCompactionAcceptsCompleteSummary(t *testing.T) {
	const completeChatBody = `{"id":"chatcmpl-cmp","created":1,"model":"cline-pass/test","choices":[{"index":0,"message":{"role":"assistant","content":"## Objective\nship the parser fix\n\n## Work State\ndone\n\n## Next Move\nrun the tests\n\n## Relevant Files\ninternal/parse.go"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, completeChatBody)
	}))
	defer upstreamServer.Close()
	server, st := configureCompactionServer(t, upstreamServer.URL)

	body := `{"model":"cline-pass/test","input":[
	  {"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]},
	  {"type":"compaction_trigger"}]}`
	request := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("compaction trigger failed: %d %s", response.Code, response.Body.String())
	}

	history := st.Metadata().History
	if len(history) != 1 || history[0].Error != nil {
		t.Fatalf("compaction should be one successful entry: %#v", history)
	}
	if len(history[0].MissingSummarySections) != 0 {
		t.Fatalf("complete summary must not be flagged: %#v", history[0].MissingSummarySections)
	}
}
