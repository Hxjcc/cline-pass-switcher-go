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
	server.adminThrottle.now = func() time.Time { return clock }
	server.clientThrottle.now = func() time.Time { return clock }
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

func sendClientKey(server *Server, remote, key string) *httptest.ResponseRecorder {
	request := localRequest(http.MethodGet, "/v1/models", nil)
	request.RemoteAddr = remote
	request.Header.Set("Authorization", "Bearer "+key)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}

func TestClientCredentialsCannotResetAdminThrottle(t *testing.T) {
	for _, tc := range []struct {
		key    string
		status int
	}{
		{"correct-key", http.StatusOK},
		{"issued-key", http.StatusOK},
		{"disabled-key", http.StatusForbidden},
	} {
		t.Run(tc.key, func(t *testing.T) {
			server, _ := newThrottleServer(t)
			if err := server.store.UpdateConfig(func(c *model.Config) {
				c.AdminKey = "admin-key"
				c.ProxyKeys = []model.ProxyKeyGrant{
					{ID: "enabled", Key: "issued-key", Enabled: true},
					{ID: "disabled", Key: "disabled-key", Enabled: false},
				}
			}); err != nil {
				t.Fatal(err)
			}
			const remote = "203.0.113.10:4444"
			for attempt := 1; attempt <= authFailureLimit; attempt++ {
				want := http.StatusUnauthorized
				if attempt == authFailureLimit {
					want = http.StatusTooManyRequests
				}
				if response := sendKey(t, server, remote, "wrong-admin"); response.Code != want {
					t.Fatalf("admin guess %d: got %d, want %d", attempt, response.Code, want)
				}
				// Client calls cannot clear failures or be blocked by an admin
				// cooldown, even when the master key is the credential used.
				if response := sendClientKey(server, remote, tc.key); response.Code != tc.status {
					t.Fatalf("client request: got %d, want %d", response.Code, tc.status)
				}
			}
			if response := sendKey(t, server, remote, "admin-key"); response.Code != http.StatusTooManyRequests {
				t.Fatalf("client call cleared the admin cooldown: %d", response.Code)
			}
		})
	}
}

func TestAdminLoginCannotResetClientThrottle(t *testing.T) {
	server, _ := newThrottleServer(t)
	const remote = "203.0.113.11:4444"
	for attempt := 1; attempt <= authFailureLimit; attempt++ {
		want := http.StatusUnauthorized
		if attempt == authFailureLimit {
			want = http.StatusTooManyRequests
		}
		if response := sendClientKey(server, remote, "wrong-key"); response.Code != want {
			t.Fatalf("client guess %d: got %d, want %d", attempt, response.Code, want)
		}
		if response := sendKey(t, server, remote, "correct-key"); response.Code != http.StatusOK {
			t.Fatalf("client guessing locked out the console: %d", response.Code)
		}
	}
	if response := sendClientKey(server, remote, "correct-key"); response.Code != http.StatusTooManyRequests {
		t.Fatalf("admin login cleared the client cooldown: %d", response.Code)
	}
}

func TestDisabledKeyCannotResetClientThrottle(t *testing.T) {
	server, _ := newThrottleServer(t)
	if err := server.store.UpdateConfig(func(c *model.Config) {
		c.ProxyKeys = []model.ProxyKeyGrant{{ID: "disabled", Key: "disabled-key", Enabled: false}}
	}); err != nil {
		t.Fatal(err)
	}
	const remote = "203.0.113.12:4444"
	for attempt := 1; attempt < authFailureLimit; attempt++ {
		if response := sendClientKey(server, remote, "wrong-key"); response.Code != http.StatusUnauthorized {
			t.Fatalf("client guess %d: got %d, want 401", attempt, response.Code)
		}
		if response := sendClientKey(server, remote, "disabled-key"); response.Code != http.StatusForbidden {
			t.Fatalf("disabled credential: got %d, want 403", response.Code)
		}
	}
	if response := sendClientKey(server, remote, "wrong-key"); response.Code != http.StatusTooManyRequests {
		t.Fatalf("disabled credential cleared the client failures: %d", response.Code)
	}
}
