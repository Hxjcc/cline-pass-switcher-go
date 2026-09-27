package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

// The console has to be able to answer "why does THIS thread keep landing on
// another channel", which needs the conversation id on the history row.
func TestHistoryRecordsTheClientSession(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"chatcmpl-1","model":"cline-pass/test","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`))
	}))
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.Accounts = []model.Account{{Name: "main", Key: "cline-key", Enabled: true}}
		config.KnownModels = []string{"cline-pass/test"}
	}); err != nil {
		t.Fatal(err)
	}

	const session = "01a0e287-7a6b-7a80-8e07-1e7aedffe494"
	request := localRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"cline-pass/test","messages":[{"role":"user","content":"hi"}],"prompt_cache_key":"`+session+`"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("chat failed: %d %s", response.Code, response.Body.String())
	}

	history := st.Metadata().History
	if len(history) != 1 || history[0].Session != session {
		t.Fatalf("history must keep the client session: %#v", history)
	}

	// ... and the console's search finds the thread by that id.
	filtered := filterHistory(history, session, "")
	if len(filtered) != 1 {
		t.Fatalf("searching by session id returned %d rows", len(filtered))
	}
}

// A request without prompt_cache_key stores no session, and the row still
// serialises without the field.
func TestHistoryOmitsAnAbsentSession(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"chatcmpl-1","model":"cline-pass/test","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`))
	}))
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.Accounts = []model.Account{{Name: "main", Key: "cline-key", Enabled: true}}
		config.KnownModels = []string{"cline-pass/test"}
	}); err != nil {
		t.Fatal(err)
	}

	request := localRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"cline-pass/test","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("chat failed: %d", response.Code)
	}
	history := st.Metadata().History
	if len(history) != 1 || history[0].Session != "" {
		t.Fatalf("a keyless request must not claim a session: %#v", history)
	}
	raw, err := json.Marshal(history[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"session"`) {
		t.Fatalf("empty session must stay out of the payload: %s", raw)
	}
}
