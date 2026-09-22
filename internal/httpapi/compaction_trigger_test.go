package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
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
	return server, st
}

// Remote compaction v2: Codex sends an ordinary /responses request with a
// compaction_trigger item and expects exactly one compaction output item plus a
// response.completed event over the normal lifecycle.
func TestResponsesCompactionTriggerStreamsSingleCompactionItem(t *testing.T) {
	recorder, upstreamServer := newCompactionRecorderServer(t)
	server, st := configureCompactionServer(t, upstreamServer.URL)

	body := `{"model":"cline-pass/test","stream":true,"input":[
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
