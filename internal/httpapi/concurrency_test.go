package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

func sendConcurrencyRequest(t *testing.T, proxy *httptest.Server, path, key, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, proxy.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)
	client := proxy.Client()
	client.Timeout = 5 * time.Second
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func assertConcurrencyRejection(t *testing.T, response *http.Response) {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected a concurrency rejection, got HTTP %d", response.StatusCode)
	}
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "concurrency_limit" || response.Header.Get("Retry-After") != "1" {
		t.Fatalf("wrong rejection: %+v, headers %v", body, response.Header)
	}
}

func TestSharedResponsesConcurrencySurvivesDisconnectAndAllowsReconnect(t *testing.T) {
	for _, limits := range []struct {
		name       string
		global     int
		blockedKey string
	}{
		{name: "global", global: 1, blockedKey: "master"},
		{name: "per-key", global: 2, blockedKey: "limited-key"},
	} {
		t.Run(limits.name, func(t *testing.T) {
			u, upstreamServer := newHeldUpstream(t)
			_, server := newTestServer(t)
			configureDrainKeys(t, server, upstreamServer.URL)
			server.limiter = newConcurrencyLimiter(limits.global, 1)
			finished := make(chan struct{}, 16)
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				server.ServeHTTP(w, r)
				finished <- struct{}{}
			}))
			defer proxy.Close()
			release := sync.OnceFunc(func() { close(u.proceed) })
			defer func() { release(); server.shares.running.Wait() }()

			first := sendConcurrencyRequest(t, proxy, "/v1/responses", "limited-key", limitedReplayBody)
			if first.StatusCode != http.StatusOK {
				t.Fatalf("first stream failed: %d", first.StatusCode)
			}
			_ = first.Body.Close()
			waitFor(t, "disconnected subscriber", finished)
			// There is no handler left, but the upstream job still owns its slot.
			different := strings.Replace(limitedReplayBody, "hello", "different", 1)
			assertConcurrencyRejection(t, sendConcurrencyRequest(t, proxy, "/v1/responses", limits.blockedKey, different))
			reconnected := sendConcurrencyRequest(t, proxy, "/api/v1/responses", "limited-key", limitedReplayBody)
			if reconnected.StatusCode != http.StatusOK {
				t.Fatalf("reconnect was charged another concurrency slot: %d", reconnected.StatusCode)
			}
			u.body(t) // Reconnection and rejected admission started no new upstream.
			release()
			if wire, err := io.ReadAll(reconnected.Body); err != nil || !strings.Contains(string(wire), "response.completed") {
				t.Fatalf("reconnection did not finish: %v %s", err, wire)
			}
			server.shares.running.Wait()
			next := sendConcurrencyRequest(t, proxy, "/v1/responses", "master", different)
			if next.StatusCode != http.StatusOK {
				t.Fatalf("completed run did not release its slot: %d", next.StatusCode)
			}
			_, _ = io.Copy(io.Discard, next.Body)
		})
	}
}

func TestGenerationConcurrencyAppliesToEveryGenerationPath(t *testing.T) {
	u, upstreamServer := newHeldUpstream(t)
	_, server := newTestServer(t)
	configureDrainKeys(t, server, upstreamServer.URL)
	server.limiter = newConcurrencyLimiter(1, 1)
	proxy := httptest.NewServer(server)
	defer proxy.Close()
	release := sync.OnceFunc(func() { close(u.proceed) })
	defer func() { release(); server.shares.running.Wait() }()
	first := sendConcurrencyRequest(t, proxy, "/v1/responses", "open-key", limitedReplayBody)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first stream failed: %d", first.StatusCode)
	}
	for _, prefix := range []string{"", "/v1", "/api/v1"} {
		for _, request := range []struct{ path, body string }{
			{"/chat/completions", streamChatBody},
			{"/responses", `{"model":"cline-pass/test","input":"different","stream":true}`},
			{"/responses", `{"model":"cline-pass/test","input":"different","stream":false}`},
			{"/responses", `{"model":"cline-pass/test","input":"different","stream":true,` + strictOutputFormat + `}`},
			{"/responses", `{"model":"cline-pass/test","input":[{"role":"user","content":"different"},{"type":"compaction_trigger"}],"stream":true}`},
			{"/responses/compact", `{"model":"cline-pass/test","input":"different"}`},
		} {
			assertConcurrencyRejection(t, sendConcurrencyRequest(t, proxy, prefix+request.path, "master", request.body))
		}
	}
	u.body(t)
	release()
	_, _ = io.Copy(io.Discard, first.Body)
}

func TestResponseSpendRejectionReleasesConcurrency(t *testing.T) {
	for _, body := range []string{
		limitedReplayBody,
		`{"model":"cline-pass/test","input":"hello","stream":false}`,
		`{"model":"cline-pass/test","input":"hello","stream":true,` + strictOutputFormat + `}`,
		`{"model":"cline-pass/test","input":[{"role":"user","content":"hello"},{"type":"compaction_trigger"}],"stream":true}`,
	} {
		t.Run(body, func(t *testing.T) {
			u, upstreamServer := newHeldUpstream(t)
			st, server := newTestServer(t)
			configureDrainKeys(t, server, upstreamServer.URL)
			server.limiter = newConcurrencyLimiter(1, 1)
			cost := 10.0
			if err := st.Record(model.HistoryEntry{KeyID: "limited", Usage: &model.UsageStats{Cost: &cost}}); err != nil {
				t.Fatal(err)
			}
			proxy := httptest.NewServer(server)
			defer proxy.Close()
			release := sync.OnceFunc(func() { close(u.proceed) })
			defer func() { release(); server.shares.running.Wait() }()
			rejected := sendConcurrencyRequest(t, proxy, "/v1/responses", "limited-key", body)
			if rejected.StatusCode != http.StatusTooManyRequests || rejected.Header.Get("X-Cline-Key-Limit") != "exceeded" {
				t.Fatalf("expected exhausted spend, got %d", rejected.StatusCode)
			}
			next := sendConcurrencyRequest(t, proxy, "/v1/responses", "master", limitedReplayBody)
			if next.StatusCode != http.StatusOK {
				t.Fatalf("spend rejection leaked the generation slot: %d", next.StatusCode)
			}
			release()
			_, _ = io.Copy(io.Discard, next.Body)
		})
	}
}

func TestSharedResponsesConcurrencyReleasesAfterAbandonment(t *testing.T) {
	u, upstreamServer := newHeldUpstream(t)
	_, server := newTestServer(t)
	configureDrainKeys(t, server, upstreamServer.URL)
	server.limiter = newConcurrencyLimiter(1, 1)
	cancel, finished, _ := serveWithClient(server, "/v1/responses", "master", limitedReplayBody)
	defer func() {
		cancel()
		server.shares.close()
		server.shares.running.Wait()
	}()
	waitFor(t, "upstream stream", u.started)
	cancel()
	waitFor(t, "disconnected subscriber", finished)
	waitFor(t, "abandoned upstream", u.cancelled)
	server.shares.running.Wait()
	release, err := server.acquireGeneration(context.Background())
	if err != nil {
		t.Fatal("abandoned run did not release concurrency")
	}
	release()
}
