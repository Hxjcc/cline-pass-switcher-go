package upstream

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/store"
)

// newStreamTestStore wires a store pointing at the given upstream server.
func newStreamTestStore(t *testing.T, baseURL string) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateConfig(func(cfg *model.Config) {
		cfg.UpstreamBase = baseURL
		cfg.Accounts = []model.Account{{Name: "main", Key: "sk_test", Enabled: true}}
		cfg.KnownModels = []string{"cline-pass/test"}
	}); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestStreamAttemptOutlivesFixedDeadlineWhileDataFlows(t *testing.T) {
	// The upstream trickles events for well over the idle budget. Before the
	// fix a fixed 120s attempt deadline aborted long but healthy streams.
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := writer.(http.Flusher)
		for index := 0; index < 8; index++ {
			_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			select {
			case <-request.Context().Done():
				return
			case <-time.After(45 * time.Millisecond):
			}
		}
		_, _ = io.WriteString(writer, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer upstreamServer.Close()

	st := newStreamTestStore(t, upstreamServer.URL)
	service := New(st)
	service.SetStreamIdleTimeout(80 * time.Millisecond)

	result := service.StartStreamAttempt(t.Context(), "cline-pass/test", map[string]any{
		"model": "cline-pass/test", "stream": true,
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}, Attempt{})
	if !result.SSE {
		t.Fatalf("expected a committed stream, got %+v", result)
	}
	defer result.Body.Close()

	raw, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatalf("stream should survive while data keeps flowing, got %v", err)
	}
	if !strings.Contains(string(raw), "[DONE]") {
		t.Fatalf("expected the full stream, got %q", raw)
	}
}

func TestStreamAttemptReportsSilentUpstreamAsStalled(t *testing.T) {
	// The upstream commits a stream and then goes silent forever. The proxy has
	// to give up eventually and must say why.
	release := make(chan struct{})
	defer close(release)
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case <-request.Context().Done():
		case <-release:
		}
	}))
	defer upstreamServer.Close()

	st := newStreamTestStore(t, upstreamServer.URL)
	service := New(st)
	service.SetStreamIdleTimeout(60 * time.Millisecond)

	result := service.StartStreamAttempt(t.Context(), "cline-pass/test", map[string]any{
		"model": "cline-pass/test", "stream": true,
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}, Attempt{})
	if !result.SSE {
		t.Fatalf("expected a committed stream, got %+v", result)
	}
	defer result.Body.Close()

	_, err := io.ReadAll(result.Body)
	if err == nil {
		t.Fatal("expected a stall error from a silent upstream")
	}
	if !errors.Is(err, ErrStreamStalled) {
		t.Fatalf("expected ErrStreamStalled, got %v", err)
	}
	if strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("stall should be described, got %v", err)
	}
}

func TestStreamAttemptHeadTimeoutStillAllowsFailover(t *testing.T) {
	// No SSE head at all: the attempt must fail before events are committed so
	// the caller can still try the next channel.
	var requests atomic.Int32
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		<-request.Context().Done()
	}))
	defer upstreamServer.Close()

	st := newStreamTestStore(t, upstreamServer.URL)
	service := New(st)
	service.SetStreamIdleTimeout(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(t.Context(), 400*time.Millisecond)
	defer cancel()
	started := time.Now()
	result := service.StartStreamAttempt(ctx, "cline-pass/test", map[string]any{
		"model": "cline-pass/test", "stream": true,
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}, Attempt{})
	if result.SSE {
		t.Fatalf("expected no committed stream, got %+v", result)
	}
	if requests.Load() != 1 {
		t.Fatalf("expected exactly one upstream request, got %d", requests.Load())
	}
	if elapsed := time.Since(started); elapsed < 300*time.Millisecond {
		t.Fatalf("head budget should not fire early, gave up after %s", elapsed)
	}
}
