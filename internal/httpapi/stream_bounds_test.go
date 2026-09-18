package httpapi

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	responsesbridge "github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/responses"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/sse"
)

type stalledFlushWriter struct{ *stalledResponseWriter }

func (w *stalledFlushWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *stalledFlushWriter) FlushError() error {
	_, err := w.stalledResponseWriter.Write([]byte("flush"))
	return err
}

func TestChatSlowWriteAndFlushReleaseRequest(t *testing.T) {
	for _, kind := range []string{"stream-write", "stream-flush", "buffered-write", "buffered-flush"} {
		t.Run(kind, func(t *testing.T) {
			cancelled := make(chan struct{})
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(kind, "buffered") {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
					close(cancelled)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"start\"}}]}\n\n")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(cancelled)
			}))
			defer up.Close()
			st, server := newTestServer(t)
			if err := st.UpdateConfig(func(c *model.Config) {
				c.UpstreamBase = up.URL
				c.Accounts = []model.Account{{Name: "test", Key: "fake", Enabled: true}}
			}); err != nil {
				t.Fatal(err)
			}
			left, right := net.Pipe()
			defer left.Close()
			defer right.Close()
			stalled := &stalledResponseWriter{conn: left, header: make(http.Header), started: make(chan struct{})}
			var writer http.ResponseWriter = stalled
			if strings.HasSuffix(kind, "flush") {
				writer = &stalledFlushWriter{stalled}
			}
			done := make(chan struct{})
			go func() {
				server.ServeHTTP(writer, localRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test","stream":true,"messages":[{"role":"user","content":"hi"}]}`)))
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				left.Close()
				t.Fatal("slow client stuck")
			}
			if !stalled.deadlineSet || !stalled.deadlineCleared {
				t.Fatal("write deadline missing")
			}
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("upstream body was not closed")
			}
			history := st.Metadata().History
			if len(history) != 1 || history[0].Error == nil {
				t.Fatal("client failure not recorded")
			}
		})
	}
}

func TestOversizeSSEIsTerminalAcrossConsumers(t *testing.T) {
	data := []byte("data: " + strings.Repeat("x", sse.MaxEventBytes))
	adapter := responsesbridge.NewStreamAdapter(&responsesbridge.Context{Model: "test"})
	events := adapter.Feed(data)
	if !adapter.Completed() {
		t.Fatal("Responses parser did not terminate")
	}
	found := false
	for _, e := range events {
		if e.Type == "response.failed" {
			found = true
		}
	}
	if !found {
		t.Fatal("Responses parser did not emit failure")
	}
	rewriter := &sseJSONRewriter{}
	if _, err := rewriter.push(data, rewriteChatReasoningBlock); err == nil {
		t.Fatal("Chat parser accepted oversized event")
	}
	stats := newStreamStats(time.Now())
	stats.Observe(data)
	stats.Observe([]byte("\n\ndata: {}\n\n"))
	if stats.TTFTMs() != 0 {
		t.Fatal("oversize statistics were accepted")
	}
}
