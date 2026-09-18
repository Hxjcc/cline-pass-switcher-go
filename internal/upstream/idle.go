package upstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// Streaming timeouts are split into two phases instead of one fixed deadline.
//
//   - Before the first SSE event the attempt is still cheap to abandon, so a
//     hard budget applies and the proxy can still fail over to another channel.
//   - After the first event the response is already committed to the client,
//     and reasoning models are expected to stream for minutes. Liveness is
//     measured by data flow: only a silent upstream is treated as stalled.
//
// A single per-attempt deadline cannot express this, and applying it to the
// whole stream is what surfaced as "context deadline exceeded" in the middle
// of otherwise healthy long responses.
const (
	defaultStreamHeadTimeout = 180 * time.Second
	// Hidden reasoning can legitimately produce no upstream SSE events for
	// several minutes. Keep the silence budget aligned with the longest
	// client watchdog window we support (5 minutes) instead of failing at
	// 180s while the model is still thinking.
	defaultStreamIdleTimeout = 300 * time.Second
	// A buffered completion arrives all at once, so the only liveness signal
	// is total time. High-effort reasoning models regularly think for several
	// minutes before the body lands; the previous 120s cut them off.
	defaultNonStreamTimeout = 600 * time.Second
)

// SetNonStreamTimeout changes the total budget of a buffered (non-stream)
// attempt. Non-positive values reset it to the default.
func (s *Service) SetNonStreamTimeout(timeout time.Duration) {
	if timeout <= 0 {
		timeout = defaultNonStreamTimeout
	}
	atomic.StoreInt64(&s.nonStreamNanos, int64(timeout))
}

// NonStreamTimeout returns the total budget of a buffered attempt.
func (s *Service) NonStreamTimeout() time.Duration {
	if value := atomic.LoadInt64(&s.nonStreamNanos); value > 0 {
		return time.Duration(value)
	}
	return defaultNonStreamTimeout
}

// ErrStreamStalled reports an upstream that stopped sending data mid-stream.
var ErrStreamStalled = errors.New("upstream stream stalled")

// SetStreamIdleTimeout changes the mid-stream silence budget for future
// attempts. Non-positive values reset it to the default.
func (s *Service) SetStreamIdleTimeout(timeout time.Duration) {
	if timeout <= 0 {
		timeout = defaultStreamIdleTimeout
	}
	atomic.StoreInt64(&s.streamIdleNanos, int64(timeout))
}

func (s *Service) streamIdleTimeout() time.Duration {
	if value := atomic.LoadInt64(&s.streamIdleNanos); value > 0 {
		return time.Duration(value)
	}
	return defaultStreamIdleTimeout
}

// StreamIdleTimeout returns the current mid-stream silence budget. The HTTP
// layer uses it to derive a keepalive cadence that is shorter than the budget.
func (s *Service) StreamIdleTimeout() time.Duration {
	return s.streamIdleTimeout()
}

// SetStreamHeadTimeout changes how long an attempt may stay without a first SSE
// event before failing over. Non-positive values reset it to the default.
func (s *Service) SetStreamHeadTimeout(timeout time.Duration) {
	if timeout <= 0 {
		timeout = defaultStreamHeadTimeout
	}
	atomic.StoreInt64(&s.streamHeadNanos, int64(timeout))
}

func (s *Service) streamHeadTimeout() time.Duration {
	if value := atomic.LoadInt64(&s.streamHeadNanos); value > 0 {
		return time.Duration(value)
	}
	return defaultStreamHeadTimeout
}

type guardPhase string

const (
	guardPhaseHead   guardPhase = "head"
	guardPhaseStream guardPhase = "stream"
)

// idleGuard enforces the head budget and then the idle budget for one upstream
// attempt. Callers keep a committed stream alive by calling Touch as bytes
// arrive.
type idleGuard struct {
	head   time.Duration
	idle   time.Duration
	cancel context.CancelFunc
	touch  chan struct{}
	commit chan struct{}
	stop   chan struct{}
	once   sync.Once

	mu      sync.Mutex
	expired bool
	phase   guardPhase
}

func newIdleGuard(parent context.Context, head, idle time.Duration) (context.Context, *idleGuard) {
	ctx, cancel := context.WithCancel(parent)
	guard := &idleGuard{
		head:   head,
		idle:   idle,
		phase:  guardPhaseHead,
		cancel: cancel,
		touch:  make(chan struct{}, 1),
		commit: make(chan struct{}, 1),
		stop:   make(chan struct{}),
	}
	go guard.watch(ctx)
	return ctx, guard
}

func (guard *idleGuard) watch(ctx context.Context) {
	timer := time.NewTimer(guard.head)
	defer timer.Stop()
	phase := guardPhaseHead
	for {
		select {
		case <-ctx.Done():
			return
		case <-guard.stop:
			return
		case <-guard.commit:
			phase = guardPhaseStream
			resetTimer(timer, guard.idle)
		case <-guard.touch:
			if phase == guardPhaseStream {
				resetTimer(timer, guard.idle)
			}
		case <-timer.C:
			guard.expire(phase)
			guard.cancel()
			return
		}
	}
}

func resetTimer(timer *time.Timer, after time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(after)
}

func (guard *idleGuard) expire(phase guardPhase) {
	guard.mu.Lock()
	guard.expired = true
	guard.phase = phase
	guard.mu.Unlock()
}

// Commit switches the guard from the head budget to the idle budget once the
// first SSE event has been observed.
func (guard *idleGuard) Commit() {
	select {
	case guard.commit <- struct{}{}:
	default:
	}
}

// Touch reports upstream activity and restarts the idle window.
func (guard *idleGuard) Touch() {
	select {
	case guard.touch <- struct{}{}:
	default:
	}
}

// release stops the watchdog and cancels the attempt context. Idempotent.
func (guard *idleGuard) release() {
	guard.once.Do(func() {
		close(guard.stop)
		guard.cancel()
	})
}

// timeoutError describes why the guard fired, so long-running failures are no
// longer reported as a bare "context deadline exceeded" or "context canceled".
func (guard *idleGuard) timeoutError() error {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if !guard.expired {
		return nil
	}
	if guard.phase == guardPhaseHead {
		return fmt.Errorf("%w: no first event within %s", ErrStreamStalled, guard.head)
	}
	return fmt.Errorf("%w: no data for %s", ErrStreamStalled, guard.idle)
}

// guardError prefers the guard description over the transport error produced by
// its own cancellation.
func guardError(guard *idleGuard, err error) error {
	if timeout := guard.timeoutError(); timeout != nil {
		return timeout
	}
	return err
}

// idleStreamBody keeps the idle guard alive while the caller reads, and turns a
// stalled read into a descriptive error instead of "context canceled".
type idleStreamBody struct {
	io.ReadCloser
	guard *idleGuard
}

func (body *idleStreamBody) Read(buffer []byte) (int, error) {
	count, err := body.ReadCloser.Read(buffer)
	if count > 0 {
		body.guard.Touch()
	}
	if err != nil && !errors.Is(err, io.EOF) {
		err = guardError(body.guard, err)
	}
	return count, err
}

func (body *idleStreamBody) Close() error {
	err := body.ReadCloser.Close()
	body.guard.release()
	return err
}
