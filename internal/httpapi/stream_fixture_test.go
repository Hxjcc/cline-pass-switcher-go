package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/store"
)

// One real capture from Cline Pass (deepseek-v4.1-flash): the stream ids,
// session ids and the model's reasoning text were replaced with placeholders,
// every event shape, field order and the trailing [DONE] are as received. See
// the fixture's "note" field.
const capturedStreamFixture = "testdata/upstream-stream-deepseek-v4-1-flash.json"

type upstreamStreamFixture struct {
	Note      string `json:"note"`
	Model     string `json:"model"`
	Captured  string `json:"captured"`
	BodyBytes int    `json:"bodyBytes"`
	Chunks    []int  `json:"chunks"`
	Text      string `json:"text"`
}

func loadUpstreamStreamFixture(t *testing.T) upstreamStreamFixture {
	t.Helper()
	raw, err := os.ReadFile(capturedStreamFixture)
	if err != nil {
		t.Fatal(err)
	}
	var fixture upstreamStreamFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.BodyBytes != len([]byte(fixture.Text)) {
		t.Fatalf("fixture body is %d bytes but claims %d - do not hand-edit it",
			len([]byte(fixture.Text)), fixture.BodyBytes)
	}
	if !strings.HasSuffix(fixture.Text, "data: [DONE]\n\n") {
		t.Fatal("fixture must end with the [DONE] sentinel")
	}
	return fixture
}

// replayBody answers every completion with the given SSE body. size <= 0 uses
// the reader-sized splits seen during capture; a positive size writes that many
// bytes per flush, which is how a test reproduces an awkward network.
func replayBody(t *testing.T, body []byte, chunks []int, size int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/users/me/plan") {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		flush, _ := writer.(http.Flusher)
		write := func(piece []byte) {
			_, _ = writer.Write(piece)
			if flush != nil {
				flush.Flush()
			}
		}
		if size <= 0 {
			offset := 0
			for _, length := range chunks {
				if offset >= len(body) {
					break
				}
				end := min(offset+length, len(body))
				write(body[offset:end])
				offset = end
			}
			if offset < len(body) {
				write(body[offset:])
			}
			return
		}
		for offset := 0; offset < len(body); offset += size {
			write(body[offset:min(offset+size, len(body))])
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func replayUpstream(t *testing.T, fixture upstreamStreamFixture, size int) *httptest.Server {
	t.Helper()
	return replayBody(t, []byte(fixture.Text), fixture.Chunks, size)
}

type streamObservation struct {
	types []string
	text  string
	usage *model.UsageStats
}

// driveResponsesStream runs one /v1/responses turn against the given upstream
// and returns the events the client received plus the recorded store.
func driveResponsesStream(t *testing.T, upstreamURL, modelID string) ([]parsedStreamEvent, *store.Store) {
	t.Helper()
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamURL
		config.Accounts = []model.Account{{Name: "main", Key: "cline-key", Enabled: true}}
		config.KnownModels = []string{modelID}
	}); err != nil {
		t.Fatal(err)
	}

	body := `{"model":"` + modelID + `","stream":true,"input":[` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"Reply with the single word: ok"}]}]}`
	request := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("stream failed: %d %s", response.Code, response.Body.String())
	}
	return parseStreamEvents(t, response.Body.String()), st
}

// observeCapturedStream drives the fixture through the proxy and reports what
// the client received.
func observeCapturedStream(t *testing.T, fixture upstreamStreamFixture, size int) streamObservation {
	t.Helper()
	upstreamServer := replayUpstream(t, fixture, size)
	events, st := driveResponsesStream(t, upstreamServer.URL, fixture.Model)
	observation := streamObservation{}
	for _, event := range events {
		observation.types = append(observation.types, event.Type)
		switch event.Type {
		case "response.output_text.delta":
			observation.text += jsonxString(event.Data["delta"])
		case "response.completed":
			if payload, ok := event.Data["response"].(map[string]any); ok {
				observation.usage = usageFromValue(payload["usage"])
			}
		}
	}
	history := st.Metadata().History
	if len(history) != 1 || history[0].Error != nil || history[0].Usage == nil {
		reason := ""
		if len(history) == 1 && history[0].Error != nil {
			reason = *history[0].Error
		}
		t.Fatalf("streamed turn was not recorded cleanly (%s) events=%v: %#v",
			reason, observation.types, history)
	}
	return observation
}

type parsedStreamEvent struct {
	Type string
	Data map[string]any
}

func parseStreamEvents(t *testing.T, stream string) []parsedStreamEvent {
	t.Helper()
	events := make([]parsedStreamEvent, 0, 16)
	for _, block := range strings.Split(stream, "\n\n") {
		var name, data string
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event:"):
				name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
		if name == "" || data == "" || !strings.HasPrefix(data, "{") {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			t.Fatalf("event %s carried invalid JSON: %v", name, err)
		}
		events = append(events, parsedStreamEvent{Type: name, Data: payload})
	}
	if len(events) == 0 {
		t.Fatalf("no events in stream: %s", stream)
	}
	return events
}

func jsonxString(value any) string {
	text, _ := value.(string)
	return text
}

// Replaying one captured upstream stream must produce the same Responses turn
// no matter how the network sliced it. The 1-byte case also splits the
// multi-byte character in the fixture, so a decoder that assumed whole runes
// per read would corrupt the text.
func TestCapturedUpstreamStreamBecomesResponsesEvents(t *testing.T) {
	fixture := loadUpstreamStreamFixture(t)
	cases := []struct {
		name string
		size int
	}{
		{"captured split", 0},
		{"whole body", len(fixture.Text)},
		{"one byte", 1},
		{"seven bytes", 7},
	}

	reference := streamObservation{}
	for index, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			observation := observeCapturedStream(t, fixture, testCase.size)
			if observation.text != "ok" {
				t.Fatalf("assembled text is %q, want %q", observation.text, "ok")
			}
			if observation.types[0] != "response.created" {
				t.Fatalf("first event is %q", observation.types[0])
			}
			if last := observation.types[len(observation.types)-1]; last != "response.completed" {
				t.Fatalf("last event is %q, sequence: %v", last, observation.types)
			}
			for _, name := range observation.types {
				if name == "response.failed" || name == "error" {
					t.Fatalf("stream reported a failure: %v", observation.types)
				}
			}
			if observation.usage == nil {
				t.Fatal("completed event carried no usage")
			}
			if observation.usage.PromptTokens != 37 || observation.usage.CompletionTokens != 19 ||
				observation.usage.ReasoningTokens != 17 {
				t.Fatalf("usage does not match the capture: %#v", observation.usage)
			}
			if index == 0 {
				reference = observation
				return
			}
			if !equalStrings(reference.types, observation.types) {
				t.Fatalf("event sequence changed with chunking:\n %v\n %v", reference.types, observation.types)
			}
			if reference.usage.CompletionTokens != observation.usage.CompletionTokens ||
				reference.usage.ReasoningTokens != observation.usage.ReasoningTokens {
				t.Fatalf("usage changed with chunking: %#v / %#v", reference.usage, observation.usage)
			}
		})
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// The same capture with one broken event: the proxy must fail the turn instead
// of completing it or hanging, and it must say why in the history. This is how
// a gateway that starts emitting truncated JSON shows up in production.
func TestCapturedStreamWithBrokenEventFailsTheTurn(t *testing.T) {
	fixture := loadUpstreamStreamFixture(t)
	const sound = `"content":"ok"`
	index := strings.Index(fixture.Text, sound)
	if index < 0 {
		t.Fatal("fixture no longer carries the visible answer")
	}
	broken := fixture.Text[:index] + `"content:ok"` + fixture.Text[index+len(sound):]
	upstreamServer := replayBody(t, []byte(broken), fixture.Chunks, 0)

	events, st := driveResponsesStream(t, upstreamServer.URL, fixture.Model)
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	if types[len(types)-1] != "response.failed" {
		t.Fatalf("a broken upstream event must fail the turn, got %v", types)
	}
	for _, name := range types {
		if name == "response.completed" {
			t.Fatalf("a broken turn must not complete: %v", types)
		}
	}
	history := st.Metadata().History
	if len(history) != 1 || history[0].Error == nil {
		t.Fatalf("the failure must be recorded: %#v", history)
	}
	if !strings.Contains(*history[0].Error, "json") && !strings.Contains(*history[0].Error, "JSON") {
		t.Fatalf("the recorded reason should name the malformed payload: %q", *history[0].Error)
	}
}
