package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
			// With this balance, one leaked slot would prevent a new admission.
			cost := 9.75
			if err := st.Record(model.HistoryEntry{KeyID: "limited", Usage: &model.UsageStats{Cost: &cost}}); err != nil {
				t.Fatal(err)
			}
			hold, err := st.ReserveSpend(model.ProxyKeyGrant{ID: "limited", SpendLimitUSD: 10})
			if err != nil {
				t.Fatalf("cancelled request leaked its reservation: %v", err)
			}
			hold.Release()
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

func TestFreshLimitedKeyRefusesConcurrentGeneration(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/responses/compact"} {
		t.Run(path, func(t *testing.T) {
			proceed, started := make(chan struct{}), make(chan struct{}, 8)
			var calls atomic.Int32
			us := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/users/me/plan") {
					http.NotFound(w, r)
					return
				}
				calls.Add(1)
				started <- struct{}{}
				select {
				case <-proceed:
				case <-r.Context().Done():
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, costedCompletion)
			}))
			defer us.Close()
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(proceed) }) }
			defer release()
			st, server := newTestServer(t)
			configureKeys(t, st, us.URL, func(c *model.Config) {
				c.ProxyKey = "master"
				c.ProxyKeys = []model.ProxyKeyGrant{{ID: "k", Key: "issued", Enabled: true, SpendLimitUSD: 0.5}}
			})
			body := `{"model":"cline-pass/test","input":"hello"}`
			if path == "/v1/chat/completions" {
				body = `{"model":"cline-pass/test","messages":[{"role":"user","content":"hello"}]}`
			}
			send := func() *httptest.ResponseRecorder {
				r := localRequest(http.MethodPost, path, strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer issued")
				w := httptest.NewRecorder()
				server.ServeHTTP(w, r)
				return w
			}
			first := make(chan *httptest.ResponseRecorder, 1)
			go func() { first <- send() }()
			waitFor(t, "the first upstream call", started)
			for range 5 {
				if refused := send(); refused.Code != http.StatusTooManyRequests || refused.Header().Get("X-Cline-Key-Limit") != "reserved" || !strings.Contains(refused.Body.String(), "尚无可用的单次费用记录") {
					t.Fatalf("fresh key admitted concurrent spend: %d %s", refused.Code, refused.Body)
				}
			}
			// Looking up models neither reserves spend nor inherits the pricing
			// restriction on a generation already in progress.
			r := localRequest(http.MethodGet, "/v1/models", nil)
			r.Header.Set("Authorization", "Bearer issued")
			models := httptest.NewRecorder()
			server.ServeHTTP(models, r)
			if models.Code != http.StatusOK || calls.Load() != 1 {
				t.Fatalf("models were blocked or extra generations reached upstream: %d; calls=%d", models.Code, calls.Load())
			}
			release()
			if w := <-first; w.Code != http.StatusOK {
				t.Fatalf("first request failed: %d %s", w.Code, w.Body)
			}
			if w := send(); w.Code != http.StatusOK {
				t.Fatalf("completed request did not release its reservation: %d %s", w.Code, w.Body)
			}
			if w := send(); w.Code != http.StatusTooManyRequests || calls.Load() != 2 {
				t.Fatalf("spent-out key generated again: %d %s; calls=%d", w.Code, w.Body, calls.Load())
			}
			if usage := st.KeyUsage()["k"]; usage.Requests != 2 || usage.SpentMicroUSD != 500_000 {
				t.Fatalf("wrong key usage: %+v", usage)
			}
		})
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
