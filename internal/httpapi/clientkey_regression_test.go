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

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/upstream"
)

func TestPartialKeyConfigurationKeepsUncredentialedAPILocal(t *testing.T) {
	for _, adminOnly := range []bool{false, true} {
		name, path := "issued-only", "/api/accounts?reveal=1"
		if adminOnly {
			name, path = "admin-only", "/v1/models"
		}
		t.Run(name, func(t *testing.T) {
			st, server := newTestServer(t)
			if err := st.UpdateConfig(func(c *model.Config) {
				c.ProxyKey = ""
				c.AdminKey = ""
				c.TrustLocalPortForward = false
				c.Accounts = []model.Account{{ID: "a", Name: "test", Key: "upstream-secret", Enabled: true}}
				c.PublicBaseURL = "https://console.example"
				if adminOnly {
					c.AdminKey = "admin"
				} else {
					c.ProxyKeys = []model.ProxyKeyGrant{{ID: "k", Key: "issued", Enabled: true}}
				}
			}); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name, peer, host, forwarded string
				status                      int
			}{
				{"remote", "198.51.100.15:1234", "console.example", "", 403},
				{"spoofed-host", "198.51.100.15:1234", "localhost", "", 403},
				{"forwarded-remote", "127.0.0.1:1234", "localhost", "198.51.100.15", 403},
				{"rebound-host", "127.0.0.1:1234", "evil.example", "", 403},
				{"local", "127.0.0.1:1234", "localhost", "", 200},
			} {
				t.Run(tc.name, func(t *testing.T) {
					r := httptest.NewRequest(http.MethodGet, "http://"+tc.host+path, nil)
					r.RemoteAddr = tc.peer
					r.Header.Set("X-Forwarded-For", tc.forwarded)
					w := httptest.NewRecorder()
					server.ServeHTTP(w, r)
					if w.Code != tc.status {
						t.Fatalf("got %d, want %d: %s", w.Code, tc.status, w.Body)
					}
				})
			}
			for _, local := range []bool{false, true} {
				r := localRequest(http.MethodGet, "/api/meta", nil)
				if !local {
					r.RemoteAddr = "198.51.100.15:1234"
				}
				w := httptest.NewRecorder()
				server.ServeHTTP(w, r)
				var meta map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &meta); err != nil {
					t.Fatal(err)
				}
				if meta["authRequired"] != adminOnly {
					t.Fatalf("login must follow the console credential: %v", meta)
				}
				_, showsBase := meta["proxyBase"]
				if showsBase != (local && !adminOnly) {
					t.Fatalf("unexpected public address disclosure: local=%v meta=%v", local, meta)
				}
			}
		})
	}
}

func postIssuedResponses(server *Server, key string) *httptest.ResponseRecorder {
	r := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"cline-pass/test","input":"hello","stream":true}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, r)
	return w
}

func TestIssuedStreamingResponsesPreservesPinAndSpend(t *testing.T) {
	for _, sse := range []bool{false, true} {
		name := "buffered-upstream"
		if sse {
			name = "sse-upstream"
		}
		t.Run(name, func(t *testing.T) {
			u := &keyedUpstream{body: costedCompletion}
			if sse {
				u.contentType = "text/event-stream"
				u.body = "data: " + strings.ReplaceAll(costedCompletion, `"message":`, `"delta":`) + "\n\ndata: [DONE]\n\n"
			}
			us := u.server(t)
			st, server := newTestServer(t)
			configureKeys(t, st, us.URL, func(c *model.Config) {
				c.ProxyKey = "master"
				c.Accounts = []model.Account{
					{ID: "a", Name: "first", Key: "first-key", Enabled: true},
					{ID: "b", Name: "pinned", Key: "pinned-key", Enabled: true},
				}
				c.ProxyKeys = []model.ProxyKeyGrant{{ID: "k", Key: "issued", Enabled: true, AccountID: "b", SpendLimitUSD: 0.20}}
			})
			if first := postIssuedResponses(server, "issued"); first.Code != 200 || !strings.Contains(first.Body.String(), "response.completed") {
				t.Fatalf("first response failed: %d %s", first.Code, first.Body)
			}
			if second := postIssuedResponses(server, "issued"); second.Code != 429 {
				t.Fatalf("spent key must be refused: %d %s", second.Code, second.Body)
			}
			if creds := u.credentials(); len(creds) != 1 || creds[0] != "Bearer pinned-key" {
				t.Fatalf("wrong upstream credential: %v", creds)
			}
			if usage := st.KeyUsage()["k"]; usage.Requests != 1 || usage.SpentMicroUSD != 250_000 {
				t.Fatalf("wrong key usage: %+v", usage)
			}
			if history := st.Metadata().History; len(history) != 1 || history[0].KeyID != "k" || history[0].AccountID != "b" {
				t.Fatalf("wrong attribution: %+v", history)
			}
		})
	}
}

// Identical requests from one grant coalesce, but two grants spending the
// same upstream account still need separate streams and separate counters.
func TestSharedResponsesIsolatesClientKeys(t *testing.T) {
	for _, sameKey := range []bool{false, true} {
		name := "different-keys"
		if sameKey {
			name = "same-key"
		}
		t.Run(name, func(t *testing.T) {
			release := make(chan struct{})
			us := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"start\"}}]}\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				_, _ = io.WriteString(w, "data: "+strings.ReplaceAll(costedCompletion, `"message":`, `"delta":`)+"\n\ndata: [DONE]\n\n")
			}))
			defer us.Close()
			st, server := newTestServer(t)
			configureKeys(t, st, us.URL, func(c *model.Config) {
				c.ProxyKey = "master"
				c.Accounts[0].ID = "a"
				c.ProxyKeys = []model.ProxyKeyGrant{
					{ID: "one", Key: "issued-one", Enabled: true, AccountID: "a"},
					{ID: "two", Key: "issued-two", Enabled: true, AccountID: "a"},
				}
			})
			var wg sync.WaitGroup
			var releaseOnce sync.Once
			finish := func() { releaseOnce.Do(func() { close(release) }); wg.Wait() }
			defer finish()
			keys := []string{"issued-one", "issued-two"}
			if sameKey {
				keys[1] = keys[0]
			}
			for _, key := range keys {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if w := postIssuedResponses(server, key); w.Code != 200 || !strings.Contains(w.Body.String(), "response.completed") {
						t.Errorf("stream failed: %d %s", w.Code, w.Body)
					}
				}()
			}
			deadline := time.Now().Add(2 * time.Second)
			for {
				server.shares.mu.Lock()
				refs, jobs := 0, len(server.shares.jobs)
				for _, job := range server.shares.jobs {
					refs += job.refs
				}
				server.shares.mu.Unlock()
				if refs == 2 {
					want := 2
					if sameKey {
						want = 1
					}
					if jobs != want {
						t.Fatalf("got %d upstream jobs, want %d", jobs, want)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("subscribers did not join")
				}
				time.Sleep(time.Millisecond)
			}
			finish()
			usage := st.KeyUsage()
			if usage["one"].Requests != 1 || usage["one"].SpentMicroUSD != 250_000 {
				t.Fatalf("wrong first grant usage: %+v", usage)
			}
			if sameKey {
				if len(usage) != 1 {
					t.Fatalf("duplicate billed twice: %+v", usage)
				}
			} else if usage["two"].Requests != 1 || usage["two"].SpentMicroUSD != 250_000 {
				t.Fatalf("wrong second grant usage: %+v", usage)
			}
		})
	}
}

func TestSharedResponsesRetainsAuthorizationAfterDisconnect(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	parent = withCallerKey(parent, callerKey{ID: "k", Issued: true})
	parent = upstream.WithAccountPin(parent, "a")
	hub := newStreamShareHub()
	release := make(chan struct{})
	job := hub.join(parent, "test", func(job *sharedResponsesStream) { job.markReady(); <-release })
	defer func() { close(release); waitJobDone(t, job); job.release() }()
	<-job.ready
	cancel()
	if job.ctx.Err() != nil {
		t.Fatal("one subscriber cancelled the shared upstream")
	}
	if grant, ok := callerKeyFrom(job.ctx); !ok || grant.ID != "k" {
		t.Fatal("lost caller key")
	}
	if pin, ok := upstream.AccountPinFrom(job.ctx); !ok || pin != "a" {
		t.Fatal("lost account pin")
	}
	otherPin := upstream.WithAccountPin(parent, "b")
	if responsesShareKey(parent, "m", nil, nil, model.PerModelConfig{}) == responsesShareKey(otherPin, "m", nil, nil, model.PerModelConfig{}) {
		t.Fatal("rebinding a grant must not reuse its old account's stream")
	}
}
