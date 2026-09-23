package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

// sendKey performs one protected request from the given source address.
func sendKey(t *testing.T, server *Server, remote, key string) *httptest.ResponseRecorder {
	t.Helper()
	request := localRequest(http.MethodGet, "/api/accounts", nil)
	request.RemoteAddr = remote
	if key != "" {
		request.Header.Set("X-Admin-Key", key)
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}

func newThrottleServer(t *testing.T) (*Server, *time.Time) {
	t.Helper()
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(c *model.Config) { c.ProxyKey = "correct-key" }); err != nil {
		t.Fatal(err)
	}
	clock := time.Now()
	server.throttle.now = func() time.Time { return clock }
	return server, &clock
}

func TestProxyKeyGuessingIsThrottled(t *testing.T) {
	server, clock := newThrottleServer(t)
	const remote = "203.0.113.7:4444"

	// The first attempts are answered with 401; the one that exhausts the
	// budget already returns the cooldown.
	for attempt := 1; attempt < authFailureLimit; attempt++ {
		response := sendKey(t, server, remote, "wrong-key")
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: got %d, want 401", attempt, response.Code)
		}
	}
	if last := sendKey(t, server, remote, "wrong-key"); last.Code != http.StatusTooManyRequests {
		t.Fatalf("the attempt that exhausts the budget: got %d, want 429", last.Code)
	}

	// Once the budget is spent the address is refused *before* the key is
	// compared, so guessing cannot continue at full speed.
	blocked := sendKey(t, server, remote, "correct-key")
	if blocked.Code != http.StatusTooManyRequests {
		t.Fatalf("throttled attempt: got %d, want 429", blocked.Code)
	}
	retryAfter, err := strconv.Atoi(blocked.Header().Get("Retry-After"))
	if err != nil || retryAfter < 1 {
		t.Fatalf("Retry-After = %q", blocked.Header().Get("Retry-After"))
	}

	// Another source address keeps its own budget.
	if other := sendKey(t, server, "198.51.100.9:4444", "correct-key"); other.Code != http.StatusOK {
		t.Fatalf("unrelated address: got %d, want 200", other.Code)
	}

	// After the cooldown the right key works again and clears the counter.
	*clock = clock.Add(authInitialBlock + time.Second)
	if recovered := sendKey(t, server, remote, "correct-key"); recovered.Code != http.StatusOK {
		t.Fatalf("after cooldown: got %d, want 200", recovered.Code)
	}
	if again := sendKey(t, server, remote, "wrong-key"); again.Code != http.StatusUnauthorized {
		t.Fatalf("counter should restart at 401, got %d", again.Code)
	}
}

func TestCorrectKeyClearsFailureCounter(t *testing.T) {
	server, _ := newThrottleServer(t)
	const remote = "203.0.113.8:4444"

	for attempt := 1; attempt < authFailureLimit; attempt++ {
		if response := sendKey(t, server, remote, "wrong-key"); response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: got %d", attempt, response.Code)
		}
	}
	if response := sendKey(t, server, remote, "correct-key"); response.Code != http.StatusOK {
		t.Fatalf("correct key should still be accepted below the limit: %d", response.Code)
	}
	// The successful call reset the counter, so the budget starts over instead
	// of tripping on the next mistake.
	for attempt := 1; attempt < authFailureLimit; attempt++ {
		if response := sendKey(t, server, remote, "wrong-key"); response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d after reset: got %d, want 401", attempt, response.Code)
		}
	}
	if response := sendKey(t, server, remote, "correct-key"); response.Code != http.StatusOK {
		t.Fatalf("the reset counter should not have blocked yet: %d", response.Code)
	}
}

func TestCooldownGrowsForRepeatOffenders(t *testing.T) {
	server, clock := newThrottleServer(t)
	const remote = "203.0.113.9:4444"

	exhaust := func() int {
		t.Helper()
		for attempt := 1; attempt <= authFailureLimit; attempt++ {
			sendKey(t, server, remote, "wrong-key")
		}
		response := sendKey(t, server, remote, "wrong-key")
		if response.Code != http.StatusTooManyRequests {
			t.Fatalf("expected a cooldown, got %d", response.Code)
		}
		seconds, err := strconv.Atoi(response.Header().Get("Retry-After"))
		if err != nil {
			t.Fatalf("Retry-After = %q", response.Header().Get("Retry-After"))
		}
		return seconds
	}

	first := exhaust()
	*clock = clock.Add(time.Duration(first)*time.Second + time.Second)
	second := exhaust()
	if second <= first {
		t.Fatalf("the second cooldown should be longer: %ds then %ds", first, second)
	}
	if limit := int(authMaxBlock / time.Second); second > limit {
		t.Fatalf("cooldown %ds exceeds the %ds cap", second, limit)
	}
}

// Without a configured key every request is authorized, so the limiter must
// stay out of the way of ordinary local use.
func TestThrottleIgnoresRequestsWhenNoKeyIsConfigured(t *testing.T) {
	_, server := newTestServer(t)
	for attempt := 0; attempt < authFailureLimit*3; attempt++ {
		if response := sendKey(t, server, "127.0.0.1:4444", ""); response.Code != http.StatusOK {
			t.Fatalf("attempt %d: got %d, want 200", attempt, response.Code)
		}
	}
}

// A console page opened without a stored key fires several protected requests
// before the operator types anything. They are not guesses, so they must not
// push the operator into a cooldown of his own making - only wrong credentials
// spend the budget.
func TestMissingCredentialDoesNotSpendTheThrottleBudget(t *testing.T) {
	server, _ := newThrottleServer(t)
	const remote = "203.0.113.9:4444"

	for attempt := 0; attempt < authFailureLimit+2; attempt++ {
		response := sendKey(t, server, remote, "")
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: got %d, want 401", attempt, response.Code)
		}
	}
	if allowed := sendKey(t, server, remote, "correct-key"); allowed.Code != http.StatusOK {
		t.Fatalf("logging in must still work after empty probes: %d %s", allowed.Code, allowed.Body)
	}

	// The guesses themselves are still throttled.
	for attempt := 0; attempt < authFailureLimit; attempt++ {
		sendKey(t, server, remote, "still-wrong")
	}
	if blocked := sendKey(t, server, remote, "correct-key"); blocked.Code != http.StatusTooManyRequests {
		t.Fatalf("wrong credentials must still trigger the cooldown: %d", blocked.Code)
	}
}
