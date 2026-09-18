package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

// TestManualStreamPastLegacy120sDeadline is an opt-in reproduction of the
// original failure. It keeps a healthy stream open for 130 seconds; the old
// fixed 120-second deadline cut it off with "context deadline exceeded".
//
// Run with:
//
//	CLINE_MANUAL_LONG_STREAM=1 go test ./internal/httpapi -run ManualStreamPastLegacy120sDeadline -v -timeout 180s
func TestManualStreamPastLegacy120sDeadline(t *testing.T) {
	if testing.Short() {
		t.Skip("manual long-stream test")
	}
	if os.Getenv("CLINE_MANUAL_LONG_STREAM") != "1" {
		t.Skip("set CLINE_MANUAL_LONG_STREAM=1 to run")
	}

	const chunks = 13
	const gap = 10 * time.Second
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := writer.(http.Flusher)
		write := func(value string) bool {
			if _, err := io.WriteString(writer, value); err != nil {
				return false
			}
			if flusher != nil {
				flusher.Flush()
			}
			return true
		}
		if !write("data: {\"id\":\"chatcmpl-long\",\"model\":\"cline-pass/test\",\"choices\":[{\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n") {
			return
		}
		for index := 0; index < chunks; index++ {
			select {
			case <-request.Context().Done():
				return
			case <-time.After(gap):
			}
			if !write("data: {\"choices\":[{\"delta\":{\"content\":\"x\"},\"finish_reason\":null}]}\n\n") {
				return
			}
		}
		write("data: [DONE]\n\n")
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
	server.upstream.SetStreamHeadTimeout(30 * time.Second)
	server.upstream.SetStreamIdleTimeout(30 * time.Second)

	request := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
	  "model":"cline-pass/test","input":"keep thinking","stream":true
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	started := time.Now()
	server.ServeHTTP(response, request)
	elapsed := time.Since(started)

	if response.Code != http.StatusOK {
		t.Fatalf("streaming request failed: %d %s", response.Code, response.Body.String())
	}
	stream := response.Body.String()
	if !strings.Contains(stream, "event: response.completed") {
		t.Fatalf("stream did not complete: %s", stream)
	}
	if strings.Contains(stream, "event: response.failed") {
		t.Fatalf("stream failed: %s", stream)
	}
	history := st.Metadata().History
	if len(history) != 1 || history[0].Error != nil {
		t.Fatalf("long stream should complete without an error: %#v", history)
	}
	if elapsed < 125*time.Second {
		t.Fatalf("test stream finished too early to cover the legacy deadline: %s", elapsed)
	}
}
