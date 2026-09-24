package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

const (
	drainStreamHead = "data: {\"choices\":[{\"delta\":{\"content\":\"start\"}}]}\n\n"
	drainStreamTail = "data: {\"choices\":[{\"delta\":{\"content\":\" end\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15,\"cost\":0.25}}\n\n" +
		"data: [DONE]\n\n"
)

// heldUpstream streams its first chunk, then holds the rest until the test
// releases it. It reports whether the proxy cancelled the call meanwhile.
type heldUpstream struct {
	started   chan struct{}
	proceed   chan struct{}
	cancelled chan struct{}
	startOnce sync.Once
	mu        sync.Mutex
	bodies    []map[string]any
}

func newHeldUpstream(t *testing.T) (*heldUpstream, *httptest.Server) {
	t.Helper()
	u := &heldUpstream{started: make(chan struct{}), proceed: make(chan struct{}), cancelled: make(chan struct{})}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/users/me/plan") {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		u.mu.Lock()
		u.bodies = append(u.bodies, body)
		u.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, drainStreamHead)
		w.(http.Flusher).Flush()
		u.startOnce.Do(func() { close(u.started) })
		select {
		case <-u.proceed:
			_, _ = io.WriteString(w, drainStreamTail)
		case <-r.Context().Done():
			close(u.cancelled)
		}
	}))
	t.Cleanup(server.Close)
	return u, server
}

func (u *heldUpstream) body(t *testing.T) map[string]any {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.bodies) != 1 {
		t.Fatalf("upstream saw %d requests, want 1", len(u.bodies))
	}
	return u.bodies[0]
}

func runningReservations(holds *spendHolds) map[string]int {
	holds.mu.Lock()
	defer holds.mu.Unlock()
	return maps.Clone(holds.running)
}

func waitFor(t *testing.T, what string, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// serveWithClient runs one request whose client can disconnect via the
// returned cancel function. done closes once the handler has returned.
func serveWithClient(server *Server, path, key, body string) (cancel context.CancelFunc, done chan struct{}, recorder *httptest.ResponseRecorder) {
	ctx, cancel := context.WithCancel(context.Background())
	request := localRequest(http.MethodPost, path, strings.NewReader(body)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)
	recorder = httptest.NewRecorder()
	done = make(chan struct{})
	go func() {
		defer close(done)
		server.ServeHTTP(recorder, request)
	}()
	return cancel, done, recorder
}

func configureDrainKeys(t *testing.T, server *Server, upstreamURL string) {
	t.Helper()
	configureKeys(t, server.store, upstreamURL, func(c *model.Config) {
		c.ProxyKey = "master"
		c.Accounts[0].ID = "a"
		c.ProxyKeys = []model.ProxyKeyGrant{
			{ID: "limited", Key: "limited-key", Enabled: true, SpendLimitUSD: 10},
			{ID: "open", Key: "open-key", Enabled: true},
		}
	})
}

const streamChatBody = `{"model":"cline-pass/test","messages":[{"role":"user","content":"hi"}],"stream":true}`

// A client that stops the stream stops the upstream call too. The cost the
// upstream would have reported at the end never arrives, so the request is
// recorded as cancelled without one; the reservation is still released.
func TestClientDisconnectCancelsTheUpstream(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/v1/responses"} {
		t.Run(path, func(t *testing.T) {
			u, us := newHeldUpstream(t)
			st, server := newTestServer(t)
			configureDrainKeys(t, server, us.URL)

			body := streamChatBody
			if path == "/v1/responses" {
				body = `{"model":"cline-pass/test","input":"hello","stream":true}`
			}
			disconnect, done, _ := serveWithClient(server, path, "limited-key", body)
			waitFor(t, "the first upstream chunk", u.started)
			disconnect()
			waitFor(t, "the upstream cancellation", u.cancelled)
			waitFor(t, "the handler", done)
			server.shares.running.Wait()

			if usage := st.KeyUsage()["limited"]; usage.Requests != 1 || usage.SpentMicroUSD != 0 {
				t.Fatalf("key usage = %+v, want one request without a cost", usage)
			}
			history := st.Metadata().History
			if len(history) != 1 || history[0].Error == nil || *history[0].Error != "客户端取消" {
				t.Fatalf("the disconnect must stay visible in the history: %+v", history)
			}
			if running := runningReservations(server.spendHolds); len(running) != 0 {
				t.Fatalf("reservations leaked: %v", running)
			}
		})
	}
}

// Cline Pass reports usage and cost at the end of every Chat stream whether or
// not the client asked for it, so the request goes upstream as the client
// sent it and the cost is still recorded.
func TestChatStreamForwardsTheClientRequestAsSent(t *testing.T) {
	u, us := newHeldUpstream(t)
	close(u.proceed)
	st, server := newTestServer(t)
	configureDrainKeys(t, server, us.URL)

	_, done, recorder := serveWithClient(server, "/v1/chat/completions", "master", streamChatBody)
	waitFor(t, "the handler", done)
	if options, found := u.body(t)["stream_options"]; found {
		t.Fatalf("the proxy added stream_options = %v", options)
	}
	if !strings.Contains(recorder.Body.String(), "[DONE]") {
		t.Fatalf("stream lost its terminator:\n%s", recorder.Body)
	}
	history := st.Metadata().History
	if len(history) != 1 || history[0].Usage == nil || history[0].Usage.Cost == nil || *history[0].Usage.Cost != 0.25 {
		t.Fatalf("the cost must be recorded: %+v", history)
	}
}

func TestSpendHoldsReserveRunningRequests(t *testing.T) {
	holds := newSpendHolds()
	grant := model.ProxyKeyGrant{ID: "k", SpendLimitUSD: 1}
	usage := model.KeyUsage{Requests: 4, SpentMicroUSD: 800_000}

	first, message := holds.admit(grant, usage)
	if first == nil || message != "" {
		t.Fatalf("the first request must be admitted: %q", message)
	}
	// 0.80 spent plus one running request at the 0.20 average reaches 1.00.
	if second, message := holds.admit(grant, usage); second != nil || !strings.Contains(message, "进行中") {
		t.Fatalf("a request that could overrun the limit was admitted: %q", message)
	}
	// A run that outlives its handler keeps the slot until it releases too.
	first.retain()
	first.release()
	if again, _ := holds.admit(grant, usage); again != nil {
		t.Fatal("the slot was freed while the run still held it")
	}
	first.release()
	if again, _ := holds.admit(grant, usage); again == nil {
		t.Fatal("the slot was not freed")
	}

	// Without any history there is nothing to estimate from.
	fresh := newSpendHolds()
	for range 3 {
		if hold, message := fresh.admit(grant, model.KeyUsage{}); hold == nil {
			t.Fatalf("a key without history was refused: %q", message)
		}
	}
	// Unlimited keys are not tracked at all.
	if hold, _ := fresh.admit(model.ProxyKeyGrant{ID: "free"}, usage); hold != nil {
		t.Fatal("an unlimited key was reserved")
	}
	if _, tracked := runningReservations(fresh)["free"]; tracked {
		t.Fatal("an unlimited key was counted")
	}
}

func TestConcurrentRequestsCannotOverrunSpendLimit(t *testing.T) {
	proceed, started := make(chan struct{}), make(chan struct{}, 4)
	us := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/users/me/plan") {
			http.NotFound(w, r)
			return
		}
		started <- struct{}{}
		<-proceed
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, costedCompletion)
	}))
	defer us.Close()
	st, server := newTestServer(t)
	configureKeys(t, st, us.URL, func(c *model.Config) {
		c.ProxyKey = "master"
		c.ProxyKeys = []model.ProxyKeyGrant{{ID: "k", Key: "issued", Enabled: true, SpendLimitUSD: 2}}
	})
	for range 4 {
		cost := 0.4
		if err := st.Record(model.HistoryEntry{TS: time.Now().UnixMilli(), Model: "cline-pass/test", KeyID: "k", Usage: &model.UsageStats{Cost: &cost}}); err != nil {
			t.Fatal(err)
		}
	}

	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- postChat(server, "issued") }()
	waitFor(t, "the first upstream call", started)
	// 1.60 spent plus the running request at the 0.40 average reaches 2.00.
	if refused := postChat(server, "issued"); refused.Code != http.StatusTooManyRequests || refused.Header().Get("X-Cline-Key-Limit") != "reserved" {
		t.Fatalf("second request: %d %s", refused.Code, refused.Body)
	}
	close(proceed)
	if response := <-first; response.Code != http.StatusOK {
		t.Fatalf("first request: %d %s", response.Code, response.Body)
	}
	// 1.85 spent and nothing running: the key may continue.
	if response := postChat(server, "issued"); response.Code != http.StatusOK {
		t.Fatalf("request after the first finished: %d %s", response.Code, response.Body)
	}
}

func TestThrottleBucketsForwardedClients(t *testing.T) {
	server, _ := newThrottleServer(t)
	if err := server.store.UpdateConfig(func(c *model.Config) { c.TrustedProxies = []string{"10.0.0.2"} }); err != nil {
		t.Fatal(err)
	}
	send := func(peer, forwarded, key string) int {
		request := localRequest(http.MethodGet, "/api/accounts", nil)
		request.RemoteAddr = peer
		request.Header.Set("X-Admin-Key", key)
		if forwarded != "" {
			request.Header.Set("X-Forwarded-For", forwarded)
		}
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response.Code
	}
	exhaust := func(peer string, forwarded func(int) string) {
		t.Helper()
		for attempt := range authFailureLimit {
			send(peer, forwarded(attempt), "wrong-key")
		}
	}

	for _, tc := range []struct{ proxy, guesser, neighbour string }{
		{"10.0.0.2:4444", "198.51.100.1", "198.51.100.2"},
		{"127.0.0.1:4444", "198.51.100.3", "198.51.100.4"},
	} {
		exhaust(tc.proxy, func(int) string { return tc.guesser })
		if code := send(tc.proxy, tc.guesser, "correct-key"); code != http.StatusTooManyRequests {
			t.Fatalf("%s: the guessing client must be blocked: %d", tc.proxy, code)
		}
		if code := send(tc.proxy, tc.neighbour, "correct-key"); code != http.StatusOK {
			t.Fatalf("%s: another client behind the proxy was blocked: %d", tc.proxy, code)
		}
	}

	// An untrusted peer names whatever client it likes, so its own address
	// is charged and rotating the header buys no fresh budget.
	const direct = "203.0.113.5:4444"
	exhaust(direct, func(attempt int) string { return "198.51.100." + strconv.Itoa(10+attempt) })
	if code := send(direct, "198.51.100.99", "correct-key"); code != http.StatusTooManyRequests {
		t.Fatalf("a spoofed forwarding header escaped the throttle: %d", code)
	}
}

// Shutdown returns only after the requests still running have written their
// history, so the store is not closed underneath them.
func TestShutdownWaitsForInFlightRecords(t *testing.T) {
	u, us := newHeldUpstream(t)
	st, server := newTestServer(t)
	configureDrainKeys(t, server, us.URL)

	disconnect, done, _ := serveWithClient(server, "/v1/chat/completions", "limited-key", streamChatBody)
	waitFor(t, "the first upstream chunk", u.started)

	early, cancelEarly := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelEarly()
	if err := server.Shutdown(early); err == nil {
		t.Fatal("shutdown returned while a request was still running")
	}

	// The HTTP server closing its connections is what ends the request.
	disconnect()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown did not finish: %v", err)
	}
	waitFor(t, "the handler", done)
	if history := st.Metadata().History; len(history) != 1 {
		t.Fatalf("the cut-off request must be recorded before shutdown returns: %+v", history)
	}
}
