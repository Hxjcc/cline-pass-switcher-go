package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"
)

const (
	minStreamKeepaliveInterval = 50 * time.Millisecond
	maxStreamKeepaliveInterval = 10 * time.Second
)

type streamRead struct {
	data []byte
	err  error
}

type clientWriteError struct {
	err error
}

func (err *clientWriteError) Error() string { return err.err.Error() }
func (err *clientWriteError) Unwrap() error { return err.err }

// streamKeepaliveInterval derives a cadence from the upstream silence budget.
// Production uses 10s; tests that shorten the budget get proportionally
// shorter intervals without waiting ten seconds.
func streamKeepaliveInterval(idle time.Duration) time.Duration {
	if idle <= 0 {
		return maxStreamKeepaliveInterval
	}
	interval := idle / 3
	if interval > maxStreamKeepaliveInterval {
		interval = maxStreamKeepaliveInterval
	}
	if interval < minStreamKeepaliveInterval {
		interval = minStreamKeepaliveInterval
	}
	return interval
}

func pumpStreamReads(body io.Reader, output chan<- streamRead, done <-chan struct{}) {
	defer close(output)
	buffer := make([]byte, 32<<10)
	for {
		count, err := body.Read(buffer)
		if count > 0 {
			data := append([]byte(nil), buffer[:count]...)
			select {
			case output <- streamRead{data: data}:
			case <-done:
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				select {
				case output <- streamRead{err: err}:
				case <-done:
				}
			}
			return
		}
	}
}

// consumeStream reads an upstream body while periodically invoking onKeepalive.
// Keepalives only protect the downstream connection; they do not reset the
// upstream idle guard, which is owned by upstream.StartStreamAttempt.
func consumeStream(
	ctx context.Context,
	body io.Reader,
	interval time.Duration,
	shouldStop func() bool,
	onData func([]byte) error,
	onKeepalive func() error,
) error {
	if shouldStop != nil && shouldStop() {
		return nil
	}
	done := make(chan struct{})
	defer close(done)
	reads := make(chan streamRead, 1)
	go pumpStreamReads(body, reads, done)

	var ticker *time.Ticker
	var tick <-chan time.Time
	if interval > 0 && onKeepalive != nil {
		ticker = time.NewTicker(interval)
		defer ticker.Stop()
		tick = ticker.C
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result, ok := <-reads:
			if !ok {
				return nil
			}
			if len(result.data) > 0 {
				if err := onData(result.data); err != nil {
					return &clientWriteError{err: err}
				}
			}
			if result.err != nil {
				return result.err
			}
			if shouldStop != nil && shouldStop() {
				return nil
			}
		case <-tick:
			if err := onKeepalive(); err != nil {
				return &clientWriteError{err: err}
			}
		}
	}
}

func writeSSEKeepalive(writer io.Writer) error {
	if _, err := io.WriteString(writer, ": ping\n\n"); err != nil {
		return err
	}
	if flusher, ok := writer.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}
