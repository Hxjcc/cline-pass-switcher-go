package httpapi

import "testing"

func TestConcurrencyLimiterBoundsGlobalAndPerKey(t *testing.T) {
	limiter := newConcurrencyLimiter(2, 1)
	first, ok := limiter.acquire("key-a")
	if !ok {
		t.Fatal("first request should be admitted")
	}
	if _, ok := limiter.acquire("key-a"); ok {
		t.Fatal("a second request on the same key should be rejected")
	}
	second, ok := limiter.acquire("key-b")
	if !ok {
		t.Fatal("another key should use the remaining global slot")
	}
	if _, ok := limiter.acquire("key-c"); ok {
		t.Fatal("the global limit should reject a third key")
	}
	first()
	third, ok := limiter.acquire("key-c")
	if !ok {
		t.Fatal("releasing a slot should admit the next key")
	}
	first()
	second()
	third()
}
