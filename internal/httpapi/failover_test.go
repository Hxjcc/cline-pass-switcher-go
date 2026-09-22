package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/store"
)

const (
	chatCompletionBody = `{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`
	chatStreamBody     = "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
)

// newAccountPoolServer rejects one credential and answers the other with the
// protocol the client asked for.
func newAccountPoolServer(t *testing.T) (bad, good *atomic.Int32, server *httptest.Server) {
	t.Helper()
	bad, good = &atomic.Int32{}, &atomic.Int32{}
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/users/me/plan") {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") == "Bearer bad-key" {
			bad.Add(1)
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(writer, `{"error":{"message":"Invalid API key","type":"authentication_error"}}`)
			return
		}
		good.Add(1)
		var payload map[string]any
		_ = json.NewDecoder(request.Body).Decode(&payload)
		stream, _ := payload["stream"].(bool)
		if !stream {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, chatCompletionBody)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, chatStreamBody)
	}))
	t.Cleanup(server.Close)
	return bad, good, server
}

func configureTwoAccounts(t *testing.T, st *store.Store, baseURL string, pinChannel bool) {
	t.Helper()
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = baseURL
		config.AccountMode = "single"
		config.ActiveAccount = 0
		config.Accounts = []model.Account{
			{Name: "expired", Key: "bad-key", Enabled: true},
			{Name: "backup", Key: "good-key", Enabled: true},
		}
		config.KnownModels = []string{"cline-pass/test"}
		if pinChannel {
			config.PerModel["cline-pass/test"] = model.PerModelConfig{Upstreams: []string{"only"}, PinMode: "strict"}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if pinChannel {
		if _, err := st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) {
			meta.Pipeline = "planner"
			meta.Upstreams = []string{"only"}
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// A 401 is a property of the account, not of the channel: with a single pinned
// channel (or an unpinned auto route) the request still has to reach a healthy
// account within the same client request.
func TestAuthFailureRetriesAnotherAccountWithinOneChannel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		path   string
		stream bool
		check  func(t *testing.T, response *httptest.ResponseRecorder)
	}{
		{
			name: "chat/buffered", path: "/v1/chat/completions",
			check: func(t *testing.T, response *httptest.ResponseRecorder) {
				if !strings.Contains(response.Body.String(), `"content":"OK"`) {
					t.Fatalf("unexpected chat body: %s", response.Body.String())
				}
			},
		},
		{
			name: "chat/stream", path: "/v1/chat/completions", stream: true,
			check: func(t *testing.T, response *httptest.ResponseRecorder) {
				if !strings.Contains(response.Body.String(), "data: [DONE]") {
					t.Fatalf("unexpected chat stream: %s", response.Body.String())
				}
			},
		},
		{
			name: "responses/buffered", path: "/v1/responses",
			check: func(t *testing.T, response *httptest.ResponseRecorder) {
				var payload map[string]any
				if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || payload["object"] != "response" {
					t.Fatalf("unexpected responses body: %s", response.Body.String())
				}
			},
		},
		{
			name: "responses/stream", path: "/v1/responses", stream: true,
			check: func(t *testing.T, response *httptest.ResponseRecorder) {
				if !strings.Contains(response.Body.String(), "event: response.completed") {
					t.Fatalf("unexpected responses stream: %s", response.Body.String())
				}
			},
		},
	} {
		for _, pinned := range []bool{true, false} {
			route := "auto"
			if pinned {
				route = "pinned"
			}
			t.Run(tc.name+"/"+route, func(t *testing.T) {
				bad, good, upstreamServer := newAccountPoolServer(t)
				st, server := newTestServer(t)
				configureTwoAccounts(t, st, upstreamServer.URL, pinned)

				body := map[string]any{
					"model": "cline-pass/test",
				}
				if tc.path == "/v1/chat/completions" {
					body["messages"] = []any{map[string]any{"role": "user", "content": "hi"}}
				} else {
					body["input"] = "hi"
				}
				if tc.stream {
					body["stream"] = true
				}
				raw, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				request := localRequest(http.MethodPost, tc.path, strings.NewReader(string(raw)))
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				server.ServeHTTP(response, request)

				if response.Code != http.StatusOK {
					t.Fatalf("healthy account should have served the request: %d %s", response.Code, response.Body.String())
				}
				if response.Header().Get("X-Cline-Account") != "backup" {
					t.Fatalf("response attributed to the wrong account: %q", response.Header().Get("X-Cline-Account"))
				}
				tc.check(t, response)
				if bad.Load() != 1 || good.Load() != 1 {
					t.Fatalf("expected one rejected and one successful call, got bad=%d good=%d", bad.Load(), good.Load())
				}
				history := st.Metadata().History
				if len(history) != 1 || history[0].Account != "backup" || history[0].Error != nil {
					t.Fatalf("failover should be recorded as one successful request: %#v", history)
				}
			})
		}
	}
}

// An SSE 200 whose first data event is an authentication_error is the same
// account verdict as an HTTP 401 and must cool the account down.
func TestStreamFirstEventAuthErrorCoolsTheAccount(t *testing.T) {
	bad, good := &atomic.Int32{}, &atomic.Int32{}
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/users/me/plan") {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		if request.Header.Get("Authorization") == "Bearer bad-key" {
			bad.Add(1)
			_, _ = io.WriteString(writer, "data: {\"error\":{\"message\":\"Invalid API key\",\"type\":\"authentication_error\"}}\n\n")
			return
		}
		good.Add(1)
		_, _ = io.WriteString(writer, chatStreamBody)
	}))
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	configureTwoAccounts(t, st, upstreamServer.URL, true)

	request := localRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"cline-pass/test","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("the healthy account should have served the request: %d %s", response.Code, response.Body.String())
	}
	if bad.Load() != 1 || good.Load() != 1 {
		t.Fatalf("expected the account to be cooled down and retried, got bad=%d good=%d", bad.Load(), good.Load())
	}
}

func TestSameNamedAccountsKeepSeparateHealth(t *testing.T) {
	bad, good, upstreamServer := newAccountPoolServer(t)
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.AccountMode = "single"
		config.ActiveAccount = 0
		config.Accounts = []model.Account{
			{Name: "duplicate", Key: "bad-key", Enabled: true},
			{Name: "duplicate", Key: "good-key", Enabled: true},
		}
		config.KnownModels = []string{"cline-pass/test"}
		config.PerModel["cline-pass/test"] = model.PerModelConfig{Upstreams: []string{"only"}, PinMode: "strict"}
	}); err != nil {
		t.Fatal(err)
	}
	accounts := st.Config().Accounts
	if accounts[0].ID == accounts[1].ID || accounts[0].ID == "" {
		t.Fatalf("accounts need distinct identities: %#v", accounts)
	}

	request := localRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"cline-pass/test","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("same-named healthy account was excluded: %d %s", response.Code, response.Body.String())
	}
	if bad.Load() != 1 || good.Load() != 1 {
		t.Fatalf("expected one rejection and one success, got bad=%d good=%d", bad.Load(), good.Load())
	}
}

// The legacy top-level apiKey is folded into the account pool once. It must
// never come back at runtime to bypass a disabled account.
func TestDisabledLegacyAccountIsNotARuntimeFallback(t *testing.T) {
	var hits atomic.Int32
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, chatCompletionBody)
	}))
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.APIKey = "legacy-key"
		config.Accounts = []model.Account{{Name: "legacy", Key: "legacy-key", Enabled: false}}
		config.KnownModels = []string{"cline-pass/test"}
		config.PerModel["cline-pass/test"] = model.PerModelConfig{
			Upstreams: []string{"first", "second", "third"},
			PinMode:   "strict",
		}
	}); err != nil {
		t.Fatal(err)
	}
	if st.Config().APIKey != "" {
		t.Fatal("legacy apiKey should be dropped after the one-time migration")
	}
	if st.IsConfigured() {
		t.Fatal("a disabled account is not a configured account")
	}

	request := localRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"cline-pass/test","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if hits.Load() != 0 {
		t.Fatalf("no account is usable; the upstream must not be called (%d hits)", hits.Load())
	}
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected an explicit no-account failure, got %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "legacy-key") {
		t.Fatalf("response leaked the legacy key: %s", response.Body.String())
	}
	history := st.Metadata().History
	if len(history) != 1 || history[0].Error == nil {
		t.Fatalf("failed chat request should be recorded: %#v", history)
	}
	if len(history[0].Trace) != 1 {
		t.Fatalf("a missing account cannot be fixed by another channel: %#v", history[0].Trace)
	}
}

// Excluding every known channel leaves an empty allow list. That is not the
// same as "no constraint": the request must fail instead of going upstream
// unrestricted past the exclusion rules.
func TestExcludingEveryChannelFailsWithoutCallingUpstream(t *testing.T) {
	for _, tc := range []struct {
		name string
		meta []string
	}{
		{name: "probed channel list", meta: []string{"only"}},
		{name: "not probed yet", meta: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				hits.Add(1)
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, chatCompletionBody)
			}))
			defer upstreamServer.Close()

			st, server := newTestServer(t)
			if err := st.UpdateConfig(func(config *model.Config) {
				config.UpstreamBase = upstreamServer.URL
				config.Accounts = []model.Account{{Name: "main", Key: "test", Enabled: true}}
				config.KnownModels = []string{"cline-pass/test"}
				config.PerModel["cline-pass/test"] = model.PerModelConfig{
					Exclude: []string{"only"},
					PinMode: "strict",
				}
			}); err != nil {
				t.Fatal(err)
			}
			if tc.meta != nil {
				if _, err := st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) {
					meta.Pipeline = "planner"
					meta.Upstreams = tc.meta
				}); err != nil {
					t.Fatal(err)
				}
			}

			request := localRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
				`{"model":"cline-pass/test","messages":[{"role":"user","content":"hi"}]}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)

			if response.Code == http.StatusOK {
				t.Fatalf("excluded channels must not serve the request: %s", response.Body.String())
			}
			if hits.Load() != 0 {
				t.Fatalf("unconstrained request leaked upstream (%d hits)", hits.Load())
			}
		})
	}
}

func TestStrictPinStillWorksWithUnrelatedExcludeList(t *testing.T) {
	var hits atomic.Int32
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, chatCompletionBody)
	}))
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.Accounts = []model.Account{{Name: "main", Key: "test", Enabled: true}}
		config.KnownModels = []string{"cline-pass/test"}
		config.PerModel["cline-pass/test"] = model.PerModelConfig{
			Upstreams: []string{"only"},
			Exclude:   []string{"other"},
			PinMode:   "strict",
		}
	}); err != nil {
		t.Fatal(err)
	}
	request := localRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"cline-pass/test","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || hits.Load() != 1 {
		t.Fatalf("a pinned channel should still serve the request: %d %s", response.Code, response.Body.String())
	}
}

// A chat stream that closes without finish_reason/[DONE] is a protocol
// truncation even though the HTTP transport ended cleanly.
func TestChatStreamRecordsTruncationAndStreamErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		wire  string
		match string
	}{
		{
			name:  "truncated",
			wire:  "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n",
			match: "未正常结束",
		},
		{
			name: "in-stream error",
			wire: "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n" +
				"data: {\"error\":{\"message\":\"upstream exploded\",\"code\":\"server_error\"}}\n\n",
			match: "upstream exploded",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(writer, tc.wire)
			}))
			defer upstreamServer.Close()

			st, server := newTestServer(t)
			if err := st.UpdateConfig(func(config *model.Config) {
				config.UpstreamBase = upstreamServer.URL
				config.Accounts = []model.Account{{Name: "main", Key: "test", Enabled: true}}
			}); err != nil {
				t.Fatal(err)
			}
			request := localRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
				`{"model":"test","messages":[{"role":"user","content":"hi"}],"stream":true}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("the stream was committed before the truncation: %d %s", response.Code, response.Body.String())
			}

			history := st.Metadata().History
			if len(history) != 1 || history[0].Error == nil || !strings.Contains(*history[0].Error, tc.match) {
				t.Fatalf("protocol failure not recorded: %#v", history)
			}
			if stats := accountStats(t, st, "main"); stats.LastError == nil {
				t.Fatal("account statistics still report success")
			}
		})
	}
}

// Every exit of the streaming chat path records history, including the one
// where no upstream ever committed a stream.
func TestChatStreamFailureIsRecorded(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(writer, `{"error":{"message":"overloaded","type":"server_error"}}`)
	}))
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.Accounts = []model.Account{{Name: "main", Key: "test", Enabled: true}}
		config.KnownModels = []string{"cline-pass/test"}
		config.PerModel["cline-pass/test"] = model.PerModelConfig{Upstreams: []string{"only"}, PinMode: "strict"}
	}); err != nil {
		t.Fatal(err)
	}

	request := localRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"cline-pass/test","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("client should see the upstream failure: %d %s", response.Code, response.Body.String())
	}
	history := st.Metadata().History
	if len(history) != 1 {
		t.Fatalf("failed chat stream must be recorded once, got %d: %#v", len(history), history)
	}
	entry := history[0]
	if entry.Error == nil || !entry.Stream || entry.Kind != "chat" || entry.Account != "main" || len(entry.Trace) != 1 {
		t.Fatalf("failure entry is missing context: %#v", entry)
	}
}

// One SSE event may spread its JSON over several data: lines. Joining them is
// required, otherwise an error event is dropped and a truncated stream with a
// trailing [DONE] looks like a success.
func TestChatStreamMultiLineErrorEventIsRecorded(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer,
			"data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"+
				"event: error\ndata: {\"error\":\ndata: {\"message\":\"upstream exploded\"}}\n\n"+
				"data: [DONE]\n\n")
	}))
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.Accounts = []model.Account{{Name: "main", Key: "test", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	request := localRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("the stream was committed before the error: %d %s", response.Code, response.Body.String())
	}

	history := st.Metadata().History
	if len(history) != 1 || history[0].Error == nil || !strings.Contains(*history[0].Error, "upstream exploded") {
		t.Fatalf("multi-line error event was not recorded: %#v", history)
	}
}

func boolPtr(value bool) *bool { return &value }

// rateLimitUpstream answers chat completions with 429 and records whether a
// provider pin was attached. Quota probes are not chat attempts.
func rateLimitUpstream(t *testing.T, calls *atomic.Int32, pinned *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/users/me/plan") || request.URL.Path != "/chat/completions" {
			http.NotFound(writer, request)
			return
		}
		calls.Add(1)
		var payload map[string]any
		_ = json.NewDecoder(request.Body).Decode(&payload)
		pin := ""
		if options, _ := payload["providerOptions"].(map[string]any); options != nil {
			if gateway, _ := options["gateway"].(map[string]any); gateway != nil {
				if only, _ := gateway["only"].([]any); len(only) == 1 {
					pin, _ = only[0].(string)
				}
			}
		}
		*pinned = append(*pinned, pin)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(writer, `{"error":{"message":"rate limited","type":"rate_limit_error"}}`)
	}))
}

func TestRateLimitOnAutoRouteSwitchesAccountButNotChannel(t *testing.T) {
	var calls atomic.Int32
	var pinned []string
	upstreamServer := rateLimitUpstream(t, &calls, &pinned)
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.AccountMode = "single"
		config.ActiveAccount = 0
		config.Accounts = []model.Account{
			{Name: "first", Key: "key-a", Enabled: true},
			{Name: "second", Key: "key-b", Enabled: true},
		}
		config.KnownModels = []string{"cline-pass/test"}
		config.PerModel["cline-pass/test"] = model.PerModelConfig{
			Upstreams: []string{"deepseek", "glm"},
			PinMode:   "strict",
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) {
		meta.Pipeline = "planner"
		meta.Pinnable = boolPtr(false)
		meta.PinReason = "gateway_ignores_provider_preferences"
		meta.Upstreams = []string{"deepseek", "glm"}
	}); err != nil {
		t.Fatal(err)
	}

	request := localRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"cline-pass/test","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limit should surface: %d %s", response.Code, response.Body.String())
	}
	if calls.Load() != 2 {
		t.Fatalf("auto route should try the other account once, got %d calls", calls.Load())
	}
	for _, pin := range pinned {
		if pin != "" {
			t.Fatalf("auto route must not send a stale pin: %#v", pinned)
		}
	}
	if response.Header().Get("X-Cline-Account") != "second" {
		t.Fatalf("the second account should have taken the last attempt: %q", response.Header().Get("X-Cline-Account"))
	}
}

func TestRateLimitOnPinnableModelTriesTheNextChannel(t *testing.T) {
	var calls atomic.Int32
	var pinned []string
	upstreamServer := rateLimitUpstream(t, &calls, &pinned)
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.Accounts = []model.Account{{Name: "main", Key: "key-a", Enabled: true}}
		config.KnownModels = []string{"cline-pass/test"}
		config.PerModel["cline-pass/test"] = model.PerModelConfig{
			Upstreams: []string{"deepseek", "glm"},
			PinMode:   "strict",
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) {
		meta.Pipeline = "planner"
		meta.Pinnable = boolPtr(true)
		meta.Upstreams = []string{"deepseek", "glm"}
	}); err != nil {
		t.Fatal(err)
	}

	request := localRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"cline-pass/test","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limit should surface: %d %s", response.Code, response.Body.String())
	}
	if calls.Load() != 2 || len(pinned) != 2 || pinned[0] != "deepseek" || pinned[1] != "glm" {
		t.Fatalf("pinnable model should walk the pinned channels once: calls=%d pins=%#v", calls.Load(), pinned)
	}
}

func TestStickySessionReusesTheAccountAcrossTurns(t *testing.T) {
	var auths []string
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/users/me/plan") || request.URL.Path != "/chat/completions" {
			http.NotFound(writer, request)
			return
		}
		auths = append(auths, request.Header.Get("Authorization"))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, chatCompletionBody)
	}))
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.AccountMode = "roundrobin"
		config.Accounts = []model.Account{
			{Name: "a", Key: "key-a", Enabled: true},
			{Name: "b", Key: "key-b", Enabled: true},
		}
		config.KnownModels = []string{"cline-pass/test"}
	}); err != nil {
		t.Fatal(err)
	}

	send := func(cacheKey string) {
		t.Helper()
		body := `{"model":"cline-pass/test","prompt_cache_key":"` + cacheKey + `","messages":[{"role":"user","content":"hi"}]}`
		request := localRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("chat failed: %d %s", response.Code, response.Body.String())
		}
	}
	send("session-42")
	send("session-42")
	send("session-other")
	if len(auths) != 3 || auths[0] == "" || auths[0] != auths[1] || auths[2] == auths[0] {
		t.Fatalf("the same conversation should keep its account, a new one should move on: %#v", auths)
	}
}
