package httpapi

import (
	"strings"
	"sync"
	"time"
)

// A public deployment must not allow unlimited guessing. Attempts that present
// a *wrong* credential are counted per source address; once the limit is
// reached the address is refused outright for a growing cooldown, including
// attempts that would otherwise succeed. That last part is deliberate: rate
// limiting only the *rejections* would still evaluate every guess and hand the
// key to whoever finds it. A request that presents no credential at all is not
// a guess - it is answered with 401 and leaves the counter alone, so a console
// page loading before login cannot lock its own operator out.
//
// Behind a Docker port mapping every client appears as the bridge gateway, so
// all remote users share one bucket. That is acceptable for a self-hosted
// console with one operator; it is also why the entry map is bounded.
const (
	authFailureLimit  = 5
	authFailureWindow = time.Minute
	authInitialBlock  = 30 * time.Second
	authMaxBlock      = 15 * time.Minute
	authThrottleMaxIP = 4096
)

type authAttempts struct {
	windowStart  time.Time
	failures     int
	blockedUntil time.Time
	blockLevel   time.Duration
}

// authThrottle is an in-memory failure counter keyed by client address. State
// is intentionally not persisted: restarting the process clears the counters.
type authThrottle struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]*authAttempts
}

func newAuthThrottle() *authThrottle {
	return &authThrottle{now: time.Now, entries: map[string]*authAttempts{}}
}

// clientKey normalises the source address so IPv6 spellings of one host share
// a bucket. Requests without a parseable address are counted by their raw
// RemoteAddr instead of silently escaping the limiter.
func clientKey(remoteAddr string) string {
	if ip := remoteIP(remoteAddr); ip != nil {
		return ip.String()
	}
	return strings.TrimSpace(remoteAddr)
}

// blocked reports how long the address must wait before its next attempt.
func (t *authThrottle) blocked(ip string) (time.Duration, bool) {
	if ip == "" {
		return 0, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := t.entries[ip]
	if entry == nil {
		return 0, false
	}
	if remaining := entry.blockedUntil.Sub(t.now()); remaining > 0 {
		return remaining, true
	}
	t.expireLocked(ip, entry)
	return 0, false
}

// fail records one rejected attempt and reports the cooldown it triggered, or
// zero when the attempt is still allowed to be answered with 401.
func (t *authThrottle) fail(ip string) time.Duration {
	if ip == "" {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	entry := t.entries[ip]
	if entry == nil {
		if len(t.entries) >= authThrottleMaxIP {
			t.evictLocked()
		}
		entry = &authAttempts{windowStart: now}
		t.entries[ip] = entry
	}
	if now.Sub(entry.windowStart) >= authFailureWindow {
		entry.windowStart, entry.failures = now, 0
	}
	entry.failures++
	if entry.failures < authFailureLimit {
		return 0
	}
	if entry.blockLevel == 0 {
		entry.blockLevel = authInitialBlock
	} else if entry.blockLevel < authMaxBlock {
		entry.blockLevel *= 2
		if entry.blockLevel > authMaxBlock {
			entry.blockLevel = authMaxBlock
		}
	}
	entry.failures = 0
	entry.windowStart = now
	entry.blockedUntil = now.Add(entry.blockLevel)
	return entry.blockLevel
}

// succeed clears the counter for an address that presented the right key.
func (t *authThrottle) succeed(ip string) {
	if ip == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, ip)
}

// expireLocked forgets finished records so idle addresses do not accumulate.
func (t *authThrottle) expireLocked(ip string, entry *authAttempts) {
	if entry.blockLevel == 0 && t.now().Sub(entry.windowStart) >= authFailureWindow {
		delete(t.entries, ip)
	}
}

// evictLocked makes room by dropping the cooldown state that is closest to
// expiring; a full table must never turn into an unbounded allocation.
func (t *authThrottle) evictLocked() {
	now := t.now()
	oldestIP, oldestAt := "", time.Time{}
	for ip, entry := range t.entries {
		if !entry.blockedUntil.After(now) && entry.blockLevel == 0 {
			delete(t.entries, ip)
			continue
		}
		if oldestIP == "" || entry.windowStart.Before(oldestAt) {
			oldestIP, oldestAt = ip, entry.windowStart
		}
	}
	if oldestIP != "" {
		delete(t.entries, oldestIP)
	}
}
