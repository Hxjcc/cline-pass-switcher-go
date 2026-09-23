package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/store"
)

func stringsReader(value string) io.Reader { return strings.NewReader(value) }

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

func floatPtr(value float64) *float64 { return &value }

// costedCompletion is a chat completion whose usage carries the price the
// upstream reported, which is what a spend limit is enforced against.
const costedCompletion = `{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"cost":0.25}}`

// keyedUpstream records which credential each upstream call presented.
type keyedUpstream struct {
	mu     sync.Mutex
	keys   []string
	body   string
	status int
}

func (u *keyedUpstream) server(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/users/me/plan") {
			http.NotFound(writer, request)
			return
		}
		u.mu.Lock()
		u.keys = append(u.keys, request.Header.Get("Authorization"))
		body, status := u.body, u.status
		u.mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		if status != 0 && status != http.StatusOK {
			writer.WriteHeader(status)
			_, _ = io.WriteString(writer, `{"error":{"message":"Invalid API key","type":"authentication_error"}}`)
			return
		}
		_, _ = io.WriteString(writer, body)
	}))
	t.Cleanup(server.Close)
	return server
}

func (u *keyedUpstream) credentials() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.keys...)
}

func configureKeys(t *testing.T, st *store.Store, baseURL string, update func(*model.Config)) {
	t.Helper()
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = baseURL
		config.KnownModels = []string{"cline-pass/test"}
		config.Accounts = []model.Account{{Name: "main", Key: "upstream-key", Enabled: true}}
		update(config)
	}); err != nil {
		t.Fatal(err)
	}
}

func postChat(server *Server, key string) *httptest.ResponseRecorder {
	body := `{"model":"cline-pass/test","messages":[{"role":"user","content":"hi"}]}`
	request := localRequest(http.MethodPost, "/v1/chat/completions", stringsReader(body))
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

func getWithAdminKey(server *Server, path, key string, recorder *httptest.ResponseRecorder) {
	request := localRequest(http.MethodGet, path, nil)
	if key != "" {
		request.Header.Set("X-Admin-Key", key)
	}
	server.ServeHTTP(recorder, request)
}

// The whole point of the split: a key handed to somebody else opens models and
// nothing else, while the console key stays on this machine's owner.
func TestConsoleKeyAndClientKeyAreSeparate(t *testing.T) {
	upstream := &keyedUpstream{body: chatCompletionBody}
	upstreamServer := upstream.server(t)
	st, server := newTestServer(t)
	configureKeys(t, st, upstreamServer.URL, func(config *model.Config) {
		config.ProxyKey = "client-master"
		config.AdminKey = "console-only"
	})

	console := httptest.NewRecorder()
	getWithAdminKey(server, "/api/accounts", "console-only", console)
	if console.Code != http.StatusOK {
		t.Fatalf("console key must open the management API: %d %s", console.Code, console.Body)
	}
	refused := httptest.NewRecorder()
	getWithAdminKey(server, "/api/accounts?reveal=1", "client-master", refused)
	if refused.Code != http.StatusUnauthorized {
		t.Fatalf("the client key must not reach the account list: %d %s", refused.Code, refused.Body)
	}
	if chat := postChat(server, "client-master"); chat.Code != http.StatusOK {
		t.Fatalf("the client key must keep calling models: %d %s", chat.Code, chat.Body)
	}
	if chat := postChat(server, "console-only"); chat.Code != http.StatusUnauthorized {
		t.Fatalf("the console key alone must not call models: %d %s", chat.Code, chat.Body)
	}
}

// Until an admin key exists the console keeps answering to the master key,
// which is how every existing deployment is configured.
func TestConsoleFallsBackToTheMasterKey(t *testing.T) {
	upstream := &keyedUpstream{body: chatCompletionBody}
	upstreamServer := upstream.server(t)
	st, server := newTestServer(t)
	configureKeys(t, st, upstreamServer.URL, func(config *model.Config) {
		config.ProxyKey = "only-key"
	})

	recorder := httptest.NewRecorder()
	getWithAdminKey(server, "/api/accounts", "only-key", recorder)
	if recorder.Code != http.StatusOK {
		t.Fatalf("master key must still open the console: %d %s", recorder.Code, recorder.Body)
	}
}

func TestIssuedKeyCallsModelsAndIsPinnedToItsAccount(t *testing.T) {
	upstream := &keyedUpstream{body: chatCompletionBody}
	upstreamServer := upstream.server(t)
	st, server := newTestServer(t)
	configureKeys(t, st, upstreamServer.URL, func(config *model.Config) {
		config.ProxyKey = "master"
		config.Accounts = []model.Account{
			{ID: "acc_main", Name: "main", Key: "main-key", Enabled: true},
			{ID: "acc_shared", Name: "shared", Key: "shared-key", Enabled: true},
		}
		config.ProxyKeys = []model.ProxyKeyGrant{
			{ID: "key_friend", Name: "friend", Key: "sk-friend", Enabled: true, AccountID: "acc_shared"},
		}
	})

	response := postChat(server, "sk-friend")
	if response.Code != http.StatusOK {
		t.Fatalf("issued key must be able to call models: %d %s", response.Code, response.Body)
	}
	credentials := upstream.credentials()
	if len(credentials) != 1 || credentials[0] != "Bearer shared-key" {
		t.Fatalf("the pinned account must serve the request, saw %#v", credentials)
	}

	console := httptest.NewRecorder()
	getWithAdminKey(server, "/api/accounts?reveal=1", "sk-friend", console)
	if console.Code != http.StatusUnauthorized {
		t.Fatalf("an issued key must not manage the console: %d %s", console.Code, console.Body)
	}
}

// A pinned key never falls back: the holder was promised that account, and a
// silent move to another one would spend a different budget.
func TestPinnedKeyDoesNotFailOverToAnotherAccount(t *testing.T) {
	upstream := &keyedUpstream{body: `{"error":{"message":"Invalid API key","type":"authentication_error"}}`, status: http.StatusUnauthorized}
	upstreamServer := upstream.server(t)
	st, server := newTestServer(t)
	configureKeys(t, st, upstreamServer.URL, func(config *model.Config) {
		config.ProxyKey = "master"
		config.Accounts = []model.Account{
			{ID: "acc_dead", Name: "dead", Key: "dead-key", Enabled: true},
			{ID: "acc_good", Name: "good", Key: "good-key", Enabled: true},
		}
		config.ProxyKeys = []model.ProxyKeyGrant{
			{ID: "key_dead", Name: "给小李", Key: "sk-dead", Enabled: true, AccountID: "acc_dead"},
		}
	})

	response := postChat(server, "sk-dead")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("a rejected pinned account must surface as-is: %d %s", response.Code, response.Body)
	}
	if credentials := upstream.credentials(); len(credentials) != 1 {
		t.Fatalf("a pinned failure must not retry the pool, saw %#v", credentials)
	}
	history := st.Metadata().History
	if len(history) != 1 || history[0].KeyID != "key_dead" || history[0].KeyName != "给小李" {
		t.Fatalf("history must name the key that caused the request: %#v", history)
	}
}

func TestIssuedKeySpendLimitBlocksFurtherCalls(t *testing.T) {
	upstream := &keyedUpstream{body: costedCompletion}
	upstreamServer := upstream.server(t)
	st, server := newTestServer(t)
	configureKeys(t, st, upstreamServer.URL, func(config *model.Config) {
		config.ProxyKey = "master"
		config.ProxyKeys = []model.ProxyKeyGrant{
			{ID: "key_capped", Name: "capped", Key: "sk-capped", Enabled: true, SpendLimitUSD: 0.20},
		}
	})

	if first := postChat(server, "sk-capped"); first.Code != http.StatusOK {
		t.Fatalf("the first call is inside the limit: %d %s", first.Code, first.Body)
	}
	usage := st.Metadata().KeyUsage["key_capped"]
	if usage.Requests != 1 || usage.SpentMicroUSD != 250_000 {
		t.Fatalf("spend must be accumulated from the upstream cost: %#v", usage)
	}

	blocked := postChat(server, "sk-capped")
	if blocked.Code != http.StatusTooManyRequests {
		t.Fatalf("the second call must be refused: %d %s", blocked.Code, blocked.Body)
	}
	var payload map[string]any
	if err := json.Unmarshal(blocked.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if errorBody, _ := payload["error"].(map[string]any); errorBody["code"] != "key_spend_limit" {
		t.Fatalf("unexpected refusal body: %#v", payload)
	}
	if credentials := upstream.credentials(); len(credentials) != 1 {
		t.Fatalf("a capped key must not reach the upstream again, saw %#v", credentials)
	}

	// The master key is never capped: the operator keeps working.
	if master := postChat(server, "master"); master.Code != http.StatusOK {
		t.Fatalf("the master key must stay unlimited: %d %s", master.Code, master.Body)
	}
}

func TestKeysApiSavesRevealsAndResets(t *testing.T) {
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.AdminKey = "console"
		config.Accounts = []model.Account{{ID: "acc_1", Name: "main", Key: "k", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}

	save := localRequest(http.MethodPost, "/api/keys", stringsReader(`{"keys":[
	  {"name":"给小王","key":"sk-handed-out","enabled":true,"accountId":"acc_1","spendLimitUsd":5}]}`))
	save.Header.Set("Content-Type", "application/json")
	save.Header.Set("X-Admin-Key", "console")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, save)
	if recorder.Code != http.StatusOK {
		t.Fatalf("saving a key failed: %d %s", recorder.Code, recorder.Body)
	}
	saved := st.Config().ProxyKeys
	if len(saved) != 1 || saved[0].ID == "" || saved[0].SpendLimitUSD != 5 || saved[0].AccountID != "acc_1" {
		t.Fatalf("stored grant is wrong: %#v", saved)
	}

	list := httptest.NewRecorder()
	getWithAdminKey(server, "/api/keys", "console", list)
	if list.Code != http.StatusOK {
		t.Fatalf("listing keys failed: %d %s", list.Code, list.Body)
	}
	var view struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Keys) != 1 || view.Keys[0]["keyPreview"] == "" || view.Keys[0]["key"] != nil {
		t.Fatalf("the list must preview the key without handing it out: %#v", view.Keys)
	}

	revealed := httptest.NewRecorder()
	getWithAdminKey(server, "/api/keys?reveal=1", "console", revealed)
	if !contains(revealed.Body.String(), "sk-handed-out") {
		t.Fatalf("reveal must return the stored secret: %s", revealed.Body)
	}

	// Re-saving a row without its secret keeps the stored one.
	if err := st.Record(model.HistoryEntry{TS: 1, Model: "cline-pass/test", KeyID: saved[0].ID,
		Usage: &model.UsageStats{Cost: floatPtr(0.75)}}); err != nil {
		t.Fatal(err)
	}
	if spent := st.Metadata().KeyUsage[saved[0].ID]; spent.SpentMicroUSD != 750_000 {
		t.Fatalf("spend recording failed: %#v", spent)
	}
	reset := localRequest(http.MethodPost, "/api/keys/reset", stringsReader(`{"id":"`+saved[0].ID+`"}`))
	reset.Header.Set("Content-Type", "application/json")
	reset.Header.Set("X-Admin-Key", "console")
	resetRecorder := httptest.NewRecorder()
	server.ServeHTTP(resetRecorder, reset)
	if resetRecorder.Code != http.StatusOK {
		t.Fatalf("reset failed: %d %s", resetRecorder.Code, resetRecorder.Body)
	}
	if spent := st.Metadata().KeyUsage[saved[0].ID]; spent.SpentMicroUSD != 0 || spent.Requests != 0 {
		t.Fatalf("reset must clear the counters: %#v", spent)
	}

	keep := localRequest(http.MethodPost, "/api/keys", stringsReader(`{"keys":[
	  {"id":"`+saved[0].ID+`","name":"给小王","key":"","enabled":true,"spendLimitUsd":5}]}`))
	keep.Header.Set("Content-Type", "application/json")
	keep.Header.Set("X-Admin-Key", "console")
	keepRecorder := httptest.NewRecorder()
	server.ServeHTTP(keepRecorder, keep)
	if keepRecorder.Code != http.StatusOK {
		t.Fatalf("re-saving failed: %d %s", keepRecorder.Code, keepRecorder.Body)
	}
	if keys := st.Config().ProxyKeys; len(keys) != 1 || keys[0].Key != "sk-handed-out" {
		t.Fatalf("an empty secret must keep the stored key: %#v", keys)
	}
}

func TestKeysApiRejectsAnUnknownPinnedAccount(t *testing.T) {
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) { config.AdminKey = "console" }); err != nil {
		t.Fatal(err)
	}
	save := localRequest(http.MethodPost, "/api/keys", stringsReader(`{"keys":[
	  {"name":"x","key":"sk-x","enabled":true,"accountId":"missing"}]}`))
	save.Header.Set("Content-Type", "application/json")
	save.Header.Set("X-Admin-Key", "console")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, save)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("binding to a missing account must fail: %d %s", recorder.Code, recorder.Body)
	}
}

// /api/meta has to answer before login (the console uses it to decide whether
// to ask for a key), but the deployment's public address is only handed to a
// caller that already presented the console key.
func TestMetaHidesThePublicAddressBeforeLogin(t *testing.T) {
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.AdminKey = "console"
		config.PublicBaseURL = "https://console.example"
	}); err != nil {
		t.Fatal(err)
	}

	anonymous := httptest.NewRecorder()
	getWithAdminKey(server, "/api/meta", "", anonymous)
	if anonymous.Code != http.StatusOK {
		t.Fatalf("meta must stay reachable for the login dialog: %d", anonymous.Code)
	}
	if body := anonymous.Body.String(); strings.Contains(body, "console.example") || strings.Contains(body, "proxyBase") {
		t.Fatalf("the public address leaked to an anonymous caller: %s", body)
	} else if !strings.Contains(body, `"authRequired":true`) {
		t.Fatalf("meta must still say that a key is required: %s", body)
	}

	authenticated := httptest.NewRecorder()
	getWithAdminKey(server, "/api/meta", "console", authenticated)
	if !strings.Contains(authenticated.Body.String(), "console.example") {
		t.Fatalf("the console needs its own base URL after login: %s", authenticated.Body)
	}
}
