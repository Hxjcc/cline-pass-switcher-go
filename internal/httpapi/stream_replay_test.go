package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	responsesbridge "github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/responses"
)

func replayTestBudget(t *testing.T) *replayBudget {
	t.Helper()
	limits := defaultReplayLimits()
	limits.tempDir = t.TempDir()
	limits.memoryPerStream = replayBlockBytes
	limits.memoryTotal = 2 * replayBlockBytes
	return &replayBudget{limits: limits}
}

func readAllReplay(t *testing.T, log *replayLog) ([]byte, error) {
	t.Helper()
	var out []byte
	for {
		data, wait, err := log.read(int64(len(out)))
		out = append(out, data...)
		if err != nil {
			return out, err
		}
		if wait != nil {
			t.Fatal("replay reader unexpectedly waiting")
		}
	}
}

func assertReplayReleased(t *testing.T, b *replayBudget) {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.memory != 0 || b.stored != 0 {
		t.Fatalf("budget leaked: memory=%d stored=%d", b.memory, b.stored)
	}
	files, err := os.ReadDir(b.limits.tempDir)
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary files not removed: %v %v", files, err)
	}
}

func TestReplaySpillsAndReadsFromStart(t *testing.T) {
	budget := replayTestBudget(t)
	log := newReplayLog(budget)
	defer log.dispose()
	first := bytes.Repeat([]byte("a"), replayBlockBytes-3)
	second := bytes.Repeat([]byte("bc"), replayBlockBytes)
	if _, err := log.Write(first); err != nil {
		t.Fatal(err)
	}
	if log.file != nil {
		t.Fatal("small replay unnecessarily spilled")
	}
	read, _, err := log.read(0)
	if err != nil || !bytes.Equal(read, first) {
		t.Fatal("memory read mismatch")
	}
	if _, err := log.Write(second); err != nil {
		t.Fatal(err)
	}
	if log.file == nil || len(log.blocks) != 0 || budget.memory != 0 {
		t.Fatal("spill retained memory blocks")
	}
	log.finish()
	got, err := readAllReplay(t, log)
	if !errors.Is(err, io.EOF) || !bytes.Equal(got, append(first, second...)) {
		t.Fatalf("full replay mismatch: %v", err)
	}
	got, _, err = log.read(int64(len(first)))
	if err != nil || !bytes.Equal(got, second[:len(got)]) {
		t.Fatal("pre-spill reader offset changed")
	}
	log.dispose()
	log.dispose()
	assertReplayReleased(t, budget)
}

func TestReplayGlobalMemoryAndStorageBudgets(t *testing.T) {
	budget := replayTestBudget(t)
	budget.limits.memoryTotal = replayBlockBytes
	budget.limits.bytesTotal = 12
	a, b := newReplayLog(budget), newReplayLog(budget)
	defer a.dispose()
	defer b.dispose()
	if _, err := a.Write([]byte("first")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("second")); err != nil {
		t.Fatal(err)
	}
	if a.file != nil || b.file == nil || budget.memory != replayBlockBytes {
		t.Fatal("global memory budget was not respected")
	}
	if _, err := b.Write([]byte("over")); !errors.Is(err, errReplayLimit) {
		t.Fatalf("global storage cap ignored: %v", err)
	}
	got, err := readAllReplay(t, b)
	if string(got) != "second" || !errors.Is(err, errReplayLimit) {
		t.Fatalf("valid prefix or failure lost: %q %v", got, err)
	}
	b.dispose()
	if _, err := a.Write([]byte("more")); err != nil {
		t.Fatalf("released budget not reusable: %v", err)
	}
	a.dispose()
	assertReplayReleased(t, budget)
}

func TestReplaySingleStreamCapAndDiskErrors(t *testing.T) {
	for _, scenario := range []string{"size cap", "create failure", "write failure"} {
		t.Run(scenario, func(t *testing.T) {
			budget := replayTestBudget(t)
			log := newReplayLog(budget)
			defer log.dispose()
			switch scenario {
			case "size cap":
				budget.limits.bytesPerStream = 2
			case "create failure":
				budget.limits.memoryPerStream = 0
				budget.limits.tempDir = filepath.Join(budget.limits.tempDir, "missing")
			case "write failure":
				budget.limits.memoryPerStream = 0
				if _, err := log.Write([]byte("ok")); err != nil {
					t.Fatal(err)
				}
				log.file.Close()
			}
			_, changed, _ := log.read(log.size)
			if _, err := log.Write([]byte("failure")); err == nil {
				t.Fatal("storage failure not reported")
			}
			select {
			case <-changed:
			default:
				t.Fatal("waiting reader not woken on failure")
			}
			if _, _, err := log.read(log.size); err == nil {
				t.Fatal("reader did not receive storage error")
			}
			log.dispose()
			if budget.memory != 0 || budget.stored != 0 {
				t.Fatal("failed append leaked budget")
			}
		})
	}
}

func TestReplayConcurrentReadersAndSpill(t *testing.T) {
	budget := replayTestBudget(t)
	log := newReplayLog(budget)
	defer log.dispose()
	job := &sharedResponsesStream{replay: log, keepalive: time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results := make(chan []byte, 3)
	var readers sync.WaitGroup
	for range 3 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			sub := job.subscribe()
			var got []byte
			for {
				part, ok, err := sub.next(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				if !ok {
					results <- got
					return
				}
				got = append(got, part.data...)
			}
		}()
	}
	var expected []byte
	for i := range 300 {
		data := []byte(fmt.Sprintf("%04d:%s\n", i, strings.Repeat("x", 512)))
		expected = append(expected, data...)
		if _, err := log.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	log.finish()
	readers.Wait()
	close(results)
	for got := range results {
		if !bytes.Equal(got, expected) {
			t.Fatal("concurrent replay changed data")
		}
	}
}

func TestReplayHeartbeatAndCancellationDoNotAccumulate(t *testing.T) {
	budget := replayTestBudget(t)
	log := newReplayLog(budget)
	defer log.dispose()
	job := &sharedResponsesStream{replay: log, keepalive: time.Millisecond}
	sub := job.subscribe()
	for range 3 {
		payload, ok, err := sub.next(context.Background())
		if err != nil || !ok || !payload.ping {
			t.Fatal("missing live heartbeat")
		}
	}
	if log.size != 0 || budget.memory != 0 || budget.stored != 0 {
		t.Fatal("heartbeats entered replay storage")
	}
	job.keepalive = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, _, err := sub.next(ctx); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("wrong cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled replay subscriber stuck")
	}
}

func TestSharedReconnectReplaysSpilledEventsAndCleansOldJob(t *testing.T) {
	hub := newStreamShareHub()
	hub.budget = replayTestBudget(t)
	hub.budget.limits.memoryPerStream = 0
	release := make(chan struct{})
	job := hub.join(context.Background(), "same", func(job *sharedResponsesStream) {
		if err := job.publishEvents([]responsesbridge.Event{{Type: "response.output_text.delta", Data: map[string]any{"type": "response.output_text.delta", "delta": strings.Repeat("hello", 100)}}}); err != nil {
			t.Error(err)
		}
		job.markReady()
		<-release
	})
	<-job.ready
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	first, _, err := job.subscribe().next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	job.release()
	reconnected := hub.join(context.Background(), "same", func(*sharedResponsesStream) { t.Error("reconnect made a new upstream request") })
	if reconnected != job {
		t.Fatal("reconnect did not join original stream")
	}
	again, _, err := reconnected.subscribe().next(ctx)
	if err != nil || !bytes.Equal(first.data, again.data) {
		t.Fatal("reconnect did not replay exact wire bytes")
	}
	for _, line := range strings.Split(string(again.data), "\n") {
		if strings.HasPrefix(line, "data: ") {
			var payload map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &payload); err != nil || payload["sequence_number"] != float64(0) {
				t.Fatal("sequence number changed")
			}
		}
	}
	close(release)
	waitJobDone(t, job)
	// A new completed-key replacement must not prevent the old log's cleanup.
	replacement := hub.join(context.Background(), "same", func(j *sharedResponsesStream) { j.markReady() })
	waitJobDone(t, replacement)
	reconnected.release()
	replacement.release()
	assertReplayReleased(t, hub.budget)
}

func waitJobDone(t *testing.T, job *sharedResponsesStream) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job.hub.mu.Lock()
		done := job.runDone
		job.hub.mu.Unlock()
		if done {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("shared job did not finish")
}

// A downstream connection that never reads. Clamp its deadline for the test,
// while checking that the handler sets and clears the production write budget.
type stalledResponseWriter struct {
	conn                         net.Conn
	header                       http.Header
	started                      chan struct{}
	once                         sync.Once
	deadlineSet, deadlineCleared bool
}

func (w *stalledResponseWriter) Header() http.Header { return w.header }
func (w *stalledResponseWriter) WriteHeader(int)     {}
func (w *stalledResponseWriter) FlushError() error   { return nil }
func (w *stalledResponseWriter) Write(data []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	return w.conn.Write(data)
}
func (w *stalledResponseWriter) SetWriteDeadline(deadline time.Time) error {
	if deadline.IsZero() {
		w.deadlineCleared = true
		return w.conn.SetWriteDeadline(deadline)
	}
	w.deadlineSet = deadline.After(time.Now()) && time.Until(deadline) <= streamClientWriteTimeout
	return w.conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
}

func TestSlowSubscriberTimesOutWithoutInterruptingHealthyClient(t *testing.T) {
	release := make(chan struct{})
	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"start\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\" end\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer up.Close()
	st, server := newTestServer(t)
	server.shares.budget = replayTestBudget(t)
	server.shares.budget.limits.memoryPerStream = 0
	if err := st.UpdateConfig(func(c *model.Config) {
		c.UpstreamBase = up.URL
		c.Accounts = []model.Account{{Name: "main", Key: "test", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	slow := &stalledResponseWriter{conn: left, header: make(http.Header), started: make(chan struct{})}
	body := `{"model":"test","input":"hi","stream":true}`
	slowDone := make(chan struct{})
	go func() {
		server.ServeHTTP(slow, localRequest("POST", "/v1/responses", strings.NewReader(body)))
		close(slowDone)
	}()
	select {
	case <-slow.started:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("slow subscriber did not start")
	}
	healthyDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		writer := httptest.NewRecorder()
		server.ServeHTTP(writer, localRequest("POST", "/v1/responses", strings.NewReader(body)))
		healthyDone <- writer
	}()
	select {
	case <-slowDone:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("slow write did not time out")
	}
	close(release)
	healthy := <-healthyDone
	if !slow.deadlineSet || !slow.deadlineCleared {
		t.Fatal("handler did not enforce and clear downstream deadline")
	}
	if hits.Load() != 1 || !strings.Contains(healthy.Body.String(), "response.completed") {
		t.Fatalf("slow subscriber interrupted healthy stream: hits=%d body=%s", hits.Load(), healthy.Body.String())
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		server.shares.mu.Lock()
		empty := len(server.shares.jobs) == 0
		server.shares.mu.Unlock()
		if empty {
			assertReplayReleased(t, server.shares.budget)
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("finished shared stream retained resources")
}

func TestReplayOverflowFailsClientAndHistory(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"%s\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", strings.Repeat("x", 2000))
	}))
	defer up.Close()
	st, server := newTestServer(t)
	server.shares.budget = replayTestBudget(t)
	server.shares.budget.limits.bytesPerStream = 1024
	if err := st.UpdateConfig(func(c *model.Config) {
		c.UpstreamBase = up.URL
		c.Accounts = []model.Account{{Name: "main", Key: "test", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	writer := httptest.NewRecorder()
	server.ServeHTTP(writer, localRequest("POST", "/v1/responses", strings.NewReader(`{"model":"test","input":"hi","stream":true}`)))
	if !strings.Contains(writer.Body.String(), "replay_storage_error") || strings.Contains(writer.Body.String(), "response.completed") {
		t.Fatalf("overflow reported success: %s", writer.Body.String())
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if history := st.Metadata().History; len(history) == 1 {
			if history[0].Error == nil || !strings.Contains(*history[0].Error, "replay storage limit") {
				t.Fatalf("overflow missing from history: %#v", history)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("overflow run did not finish")
}
