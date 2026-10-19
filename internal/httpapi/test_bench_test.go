package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

func TestFailedTestBenchCallIsRecorded(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte(`{"error":{"message":"probe failed"}}`))
	}))
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	configureKeys(t, st, upstreamServer.URL, func(*model.Config) {})

	request := localRequest(http.MethodPost, "/api/test", strings.NewReader(`{"model":"cline-pass/test"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("test bench status = %d, body=%s", response.Code, response.Body.String())
	}

	history := st.History()
	if len(history) != 1 || history[0].Kind != "test" || history[0].Error == nil ||
		!strings.Contains(*history[0].Error, "probe failed") {
		t.Fatalf("failed test bench call was not recorded: %#v", history)
	}

	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || payload["ok"] != false {
		t.Fatalf("unexpected test bench response: %s %v", response.Body.String(), err)
	}
}
