package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	responsesbridge "github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/responses"
)

// Opt-in soak: concurrent writers, disconnects/reconnects, disk spill and final
// resource release. Uses only local generated data, never real upstream tokens.
func TestSharedStreamSoak(t *testing.T) {
	seconds, _ := strconv.Atoi(os.Getenv("CLINE_STRESS_SECONDS"))
	if seconds <= 0 {
		t.Skip("set CLINE_STRESS_SECONDS to run the shared-stream soak")
	}
	hub := newStreamShareHub()
	hub.budget = replayTestBudget(t)
	hub.budget.limits.memoryPerStream = replayBlockBytes
	hub.budget.limits.memoryTotal = 2 * replayBlockBytes
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
	defer cancel()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	baselineGoroutines := runtime.NumGoroutine()
	var rounds atomic.Int64
	var workers sync.WaitGroup
	for worker := range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for ctx.Err() == nil {
				key := fmt.Sprintf("worker-%d-round-%d", worker, rounds.Add(1))
				resume := make(chan struct{})
				job := hub.join(context.Background(), key, func(job *sharedResponsesStream) {
					for chunk := range 64 {
						if err := job.publishEvents([]responsesbridge.Event{{Type: "response.output_text.delta", Data: map[string]any{"type": "response.output_text.delta", "delta": string(bytes.Repeat([]byte("x"), 1024)), "chunk": chunk}}}); err != nil {
							t.Error(err)
							return
						}
						if chunk == 0 {
							job.markReady()
							<-resume
						}
					}
				})
				<-job.ready
				readCtx, readCancel := context.WithTimeout(context.Background(), 5*time.Second)
				first := job.subscribe()
				if _, _, err := first.next(readCtx); err != nil {
					t.Error(err)
				}
				job.release() // Disconnect, then reconnect within the grace window.
				reconnected := hub.join(context.Background(), key, func(*sharedResponsesStream) { t.Error("reconnect generated a second run") })
				second := hub.join(context.Background(), key, func(*sharedResponsesStream) { t.Error("duplicate subscriber generated a second run") })
				close(resume)
				read := func(sub *shareSub) []byte {
					var result []byte
					for {
						part, ok, err := sub.next(readCtx)
						if err != nil {
							t.Error(err)
							return nil
						}
						if !ok {
							return result
						}
						result = append(result, part.data...)
					}
				}
				left := read(reconnected.subscribe())
				right := read(second.subscribe()) // Deliberately lagging reader.
				if len(left) == 0 || !bytes.Equal(left, right) {
					t.Error("replay changed across subscribers")
				}
				reconnected.release()
				second.release()
				readCancel()
			}
		}()
	}
	workers.Wait()
	// Allow the existing reconnect grace timers and runner cleanup to finish.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		hub.mu.Lock()
		jobs := len(hub.jobs)
		hub.mu.Unlock()
		if jobs == 0 && runtime.NumGoroutine() <= baselineGoroutines+2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	assertReplayReleased(t, hub.budget)
	hub.mu.Lock()
	remainingJobs := len(hub.jobs)
	hub.mu.Unlock()
	if remainingJobs != 0 {
		t.Fatalf("%d shared jobs retained", remainingJobs)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	if retained > 64<<20 {
		t.Fatalf("retained heap grew by %d bytes", retained)
	}
	if goroutines := runtime.NumGoroutine(); goroutines > baselineGoroutines+8 {
		t.Fatalf("goroutines grew from %d to %d", baselineGoroutines, goroutines)
	}
	t.Logf("duration=%ds workers=8 rounds=%d retained_heap_delta=%d bytes goroutines_before=%d after=%d; replay bytes=0 files=0 jobs=0", seconds, rounds.Load(), retained, baselineGoroutines, runtime.NumGoroutine())
}
