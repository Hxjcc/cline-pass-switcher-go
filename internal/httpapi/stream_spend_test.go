package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/store"
)

const limitedReplayBody = `{"model":"cline-pass/test","input":"hello","stream":true}`

func sendLimitedReplay(t *testing.T, client *http.Client, url, body string) *http.Response {
	t.Helper()
	r, err := http.NewRequest(http.MethodPost, url+"/v1/responses", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer limited-key")
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func TestLimitedResponsesReconnectKeepsOriginalReservation(t *testing.T) {
	for _, priced := range []bool{false, true} {
		name := "fresh"
		if priced {
			name = "near-limit"
		}
		t.Run(name, func(t *testing.T) {
			u, us := newHeldUpstream(t)
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(u.proceed) }) }
			defer release()
			st, server := newTestServer(t)
			configureDrainKeys(t, server, us.URL)
			if err := st.UpdateConfig(func(c *model.Config) { c.ProxyKeys[0].SpendLimitUSD = 0.2 }); err != nil {
				t.Fatal(err)
			}
			if priced {
				cost := 0.1
				if err := st.Record(model.HistoryEntry{KeyID: "limited", Usage: &model.UsageStats{Cost: &cost}}); err != nil {
					t.Fatal(err)
				}
			}
			finished := make(chan struct{}, 8)
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				server.ServeHTTP(w, r)
				finished <- struct{}{}
			}))
			defer proxy.Close()
			client := proxy.Client()
			client.Timeout = 5 * time.Second
			first := sendLimitedReplay(t, client, proxy.URL, limitedReplayBody)
			if first.StatusCode != http.StatusOK {
				t.Fatalf("first stream rejected: %d", first.StatusCode)
			}
			_ = first.Body.Close()
			waitFor(t, "the disconnected subscriber", finished)
			reconnected := sendLimitedReplay(t, client, proxy.URL, limitedReplayBody)
			if reconnected.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(reconnected.Body)
				t.Fatalf("reconnect refused its existing stream: %d %s", reconnected.StatusCode, body)
			}
			// A different input would spend again and must still be refused.
			different := sendLimitedReplay(t, client, proxy.URL, strings.Replace(limitedReplayBody, "hello", "different", 1))
			if different.StatusCode != http.StatusTooManyRequests || different.Header.Get("X-Cline-Key-Limit") != "reserved" {
				t.Fatalf("different generation bypassed admission: %d", different.StatusCode)
			}
			for _, body := range []string{
				`{"model":"cline-pass/test","input":"hello","stream":false}`,
				`{"model":"cline-pass/test","input":"hello","stream":true,` + strictOutputFormat + `}`,
				`{"model":"cline-pass/test","input":[{"type":"message","role":"user","content":"hello"},{"type":"compaction_trigger"}],"stream":true}`,
			} {
				if response := sendLimitedReplay(t, client, proxy.URL, body); response.StatusCode != http.StatusTooManyRequests {
					t.Fatalf("buffered/strict/compaction request bypassed admission: %d", response.StatusCode)
				}
			}
			u.body(t) // Exactly one request has reached the upstream.
			release()
			wire, err := io.ReadAll(reconnected.Body)
			if err != nil || !strings.Contains(string(wire), "response.completed") || !strings.Contains(string(wire), "start") {
				t.Fatalf("reconnect did not replay and finish: %v %s", err, wire)
			}
			server.shares.running.Wait()
			wantRequests, wantSpend := int64(1), int64(250_000)
			if priced {
				wantRequests, wantSpend = 2, 350_000
			}
			if usage := st.KeyUsage()["limited"]; usage.Requests != wantRequests || usage.SpentMicroUSD != wantSpend {
				t.Fatalf("reconnect charged twice: %+v", usage)
			}
			// A finished stream cannot be resurrected to evade the now spent limit.
			if ended := sendLimitedReplay(t, client, proxy.URL, limitedReplayBody); ended.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("finished stream bypassed the limit: %d", ended.StatusCode)
			}
		})
	}
}

func TestLimitedResponsesConcurrentDuplicatesReserveOneRun(t *testing.T) {
	u, us := newHeldUpstream(t)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(u.proceed) }) }
	defer release()
	st, server := newTestServer(t)
	configureDrainKeys(t, server, us.URL)
	proxy := httptest.NewServer(server)
	defer proxy.Close()
	client := proxy.Client()
	client.Timeout = 5 * time.Second
	const subscribers = 8
	responses := make(chan *http.Response, subscribers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range subscribers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r, _ := http.NewRequest(http.MethodPost, proxy.URL+"/v1/responses", strings.NewReader(limitedReplayBody))
			r.Header.Set("Authorization", "Bearer limited-key")
			response, err := client.Do(r)
			if err != nil {
				t.Error(err)
			}
			responses <- response
		}()
	}
	close(start)
	wg.Wait()
	close(responses)
	all := make([]*http.Response, 0, subscribers)
	for response := range responses {
		if response == nil {
			continue
		}
		defer response.Body.Close()
		all = append(all, response)
		if response.StatusCode != http.StatusOK {
			t.Errorf("duplicate rejected: %d", response.StatusCode)
		}
	}
	if len(all) != subscribers || t.Failed() {
		release()
		return
	}
	hold, err := st.ReserveSpend(model.ProxyKeyGrant{ID: "limited", SpendLimitUSD: 10})
	hold.Release()
	var limit *store.SpendLimitError
	if !errors.As(err, &limit) || limit.Running != 1 {
		t.Fatalf("duplicates reserved more than one upstream run: %v", err)
	}
	// Revoking the credential must still stop attachment to the existing run.
	if err := st.UpdateConfig(func(c *model.Config) { c.ProxyKeys[0].Enabled = false }); err != nil {
		t.Fatal(err)
	}
	if disabled := sendLimitedReplay(t, client, proxy.URL, limitedReplayBody); disabled.StatusCode != http.StatusForbidden {
		t.Fatalf("disabled key attached to an active stream: %d", disabled.StatusCode)
	}
	u.body(t)
	release()
	for _, response := range all {
		if wire, err := io.ReadAll(response.Body); err != nil || !strings.Contains(string(wire), "response.completed") {
			t.Fatalf("duplicate did not finish: %v %s", err, wire)
		}
	}
	server.shares.running.Wait()
	if usage := st.KeyUsage()["limited"]; usage.Requests != 1 || usage.SpentMicroUSD != 250_000 {
		t.Fatalf("duplicates were billed more than once: %+v", usage)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}
