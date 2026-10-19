package httpapi

import "sync"

// concurrencyLimiter bounds generation work rather than merely counting
// completed failures. A valid key could otherwise open arbitrarily many long
// streams and retain replay buffers and goroutines until they time out.
type concurrencyLimiter struct {
	global chan struct{}
	perKey int

	mu     sync.Mutex
	active map[string]int
}

func newConcurrencyLimiter(global, perKey int) *concurrencyLimiter {
	limiter := &concurrencyLimiter{perKey: perKey, active: map[string]int{}}
	if global > 0 {
		limiter.global = make(chan struct{}, global)
	}
	return limiter
}

// acquire returns a release function and false when either the global or the
// per-credential bucket is full. It never waits: rejecting quickly is more
// useful to a client than holding another goroutine behind a long stream.
func (limiter *concurrencyLimiter) acquire(key string) (func(), bool) {
	if limiter == nil {
		return func() {}, true
	}
	if limiter.global != nil {
		select {
		case limiter.global <- struct{}{}:
		default:
			return nil, false
		}
	}
	if limiter.perKey > 0 {
		limiter.mu.Lock()
		if limiter.active[key] >= limiter.perKey {
			limiter.mu.Unlock()
			if limiter.global != nil {
				<-limiter.global
			}
			return nil, false
		}
		limiter.active[key]++
		limiter.mu.Unlock()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			if limiter.perKey > 0 {
				limiter.mu.Lock()
				limiter.active[key]--
				if limiter.active[key] <= 0 {
					delete(limiter.active, key)
				}
				limiter.mu.Unlock()
			}
			if limiter.global != nil {
				<-limiter.global
			}
		})
	}, true
}
