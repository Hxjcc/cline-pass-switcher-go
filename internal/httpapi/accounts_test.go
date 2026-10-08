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
)

func accountsRequest(t *testing.T, server *Server, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := localRequest(method, target, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

func decodeAccounts(t *testing.T, recorder *httptest.ResponseRecorder) []accountView {
	t.Helper()
	var payload struct {
		Accounts []accountView `json:"accounts"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode accounts: %v (%s)", err, recorder.Body.String())
	}
	return payload.Accounts
}

func saveAccounts(t *testing.T, server *Server, accounts []model.Account) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"accounts": accounts, "mode": "single", "active": 0})
	if err != nil {
		t.Fatal(err)
	}
	return accountsRequest(t, server, http.MethodPost, "/api/accounts", string(body))
}

func TestAccountListMasksStoredKeys(t *testing.T) {
	const key = "sk_live_abcdef1234567890"
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.Accounts = []model.Account{{Name: "main", Key: key, Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}

	masked := accountsRequest(t, server, http.MethodGet, "/api/accounts", "")
	if masked.Code != http.StatusOK {
		t.Fatalf("list accounts: %d %s", masked.Code, masked.Body.String())
	}
	if strings.Contains(masked.Body.String(), key) {
		t.Fatalf("stored key leaked: %s", masked.Body.String())
	}
	accounts := decodeAccounts(t, masked)
	if len(accounts) != 1 {
		t.Fatalf("expected one account, got %d", len(accounts))
	}
	view := accounts[0]
	if view.ID == "" || view.Key != "" || !view.HasKey {
		t.Fatalf("unexpected masked view: %#v", view)
	}
	if view.KeyPreview == "" || view.KeyPreview == key || !strings.HasPrefix(view.KeyPreview, "sk_liv") {
		t.Fatalf("unexpected preview: %q", view.KeyPreview)
	}
	if strings.Contains(view.KeyPreview, "ef1234567890") {
		t.Fatalf("preview exposes too much of the key: %q", view.KeyPreview)
	}

	revealed := accountsRequest(t, server, http.MethodGet, "/api/accounts?reveal=1", "")
	accounts = decodeAccounts(t, revealed)
	if len(accounts) != 1 || accounts[0].Key != key {
		t.Fatalf("reveal should return the stored key: %#v", accounts)
	}
}

func TestSaveKeepsStoredKeyWhenBlank(t *testing.T) {
	const key = "sk_live_keep_me_123456"
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.Accounts = []model.Account{{Name: "main", Key: key, Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	before := st.Config().Accounts
	if len(before) != 1 || before[0].ID == "" {
		t.Fatalf("normalization did not assign an identity: %#v", before)
	}

	// The console sends the id back with an empty key after only editing the name.
	response := saveAccounts(t, server, []model.Account{{ID: before[0].ID, Name: "renamed", Enabled: false}})
	if response.Code != http.StatusOK {
		t.Fatalf("save: %d %s", response.Code, response.Body.String())
	}
	after := st.Config().Accounts
	if len(after) != 1 {
		t.Fatalf("account lost: %#v", after)
	}
	if after[0].Key != key || after[0].Name != "renamed" || after[0].ID != before[0].ID || after[0].Enabled {
		t.Fatalf("stored key or identity changed: %#v", after)
	}
}

func TestSaveReordersWithoutMixingKeys(t *testing.T) {
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.Accounts = []model.Account{
			{Name: "first", Key: "key-one", Enabled: true},
			{Name: "second", Key: "key-two", Enabled: true},
		}
	}); err != nil {
		t.Fatal(err)
	}
	before := st.Config().Accounts
	reversed := []model.Account{
		{ID: before[1].ID, Name: before[1].Name},
		{ID: before[0].ID, Name: before[0].Name},
	}
	if response := saveAccounts(t, server, reversed); response.Code != http.StatusOK {
		t.Fatalf("save: %d %s", response.Code, response.Body.String())
	}
	after := decodeAccounts(t, accountsRequest(t, server, http.MethodGet, "/api/accounts?reveal=1", ""))
	if len(after) != 2 || after[0].Key != "key-two" || after[1].Key != "key-one" {
		t.Fatalf("keys did not follow their accounts: %#v", after)
	}
}

func TestSaveDropsNewAccountsWithoutKey(t *testing.T) {
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.Accounts = []model.Account{{Name: "main", Key: "key-one", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	existing := st.Config().Accounts[0]

	response := saveAccounts(t, server, []model.Account{
		{ID: existing.ID, Name: "main"},
		{Name: "empty"}, // new row the user never filled in
	})
	if response.Code != http.StatusOK {
		t.Fatalf("save: %d %s", response.Code, response.Body.String())
	}
	stored := st.Config().Accounts
	if len(stored) != 1 || stored[0].Key != "key-one" {
		t.Fatalf("keyless new account was stored: %#v", stored)
	}

	// A brand new account still has to carry a key of its own.
	response = saveAccounts(t, server, []model.Account{{Name: "brand-new"}})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("keyless new account accepted: %d %s", response.Code, response.Body.String())
	}
	combined := []model.Account{
		{ID: existing.ID, Name: "main"},
		{Name: "brand-new", Key: "key-new", Enabled: true},
	}
	if response = saveAccounts(t, server, combined); response.Code != http.StatusOK {
		t.Fatalf("new account with key rejected: %d %s", response.Code, response.Body.String())
	}
	stored = st.Config().Accounts
	if len(stored) != 2 || stored[0].Key != "key-one" {
		t.Fatalf("existing key lost while adding an account: %#v", stored)
	}
	if stored[1].Key != "key-new" || stored[1].ID == "" || stored[1].ID == existing.ID {
		t.Fatalf("new account was not assigned its own identity: %#v", stored[1])
	}
}

func TestSaveKeepsIdentityForClientsWithoutIDs(t *testing.T) {
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.Accounts = []model.Account{{Name: "main", Key: "key-one", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	before := st.Config().Accounts[0]

	// Older clients post the full key without an id; the identity must survive.
	if response := saveAccounts(t, server, []model.Account{{Name: "main", Key: "key-one"}}); response.Code != http.StatusOK {
		t.Fatalf("save: %d %s", response.Code, response.Body.String())
	}
	after := st.Config().Accounts
	if len(after) != 1 || after[0].ID != before.ID {
		t.Fatalf("identity changed for a legacy save: %#v", after)
	}

	// The same client may post an empty key; the stored one is kept by name.
	if response := saveAccounts(t, server, []model.Account{{Name: "main"}}); response.Code != http.StatusOK {
		t.Fatalf("save: %d %s", response.Code, response.Body.String())
	}
	after = st.Config().Accounts
	if len(after) != 1 || after[0].Key != "key-one" {
		t.Fatalf("legacy empty key dropped the stored key: %#v", after)
	}
}

func TestSaveIgnoresDuplicatedIdentity(t *testing.T) {
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.Accounts = []model.Account{{Name: "main", Key: "key-one", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	existing := st.Config().Accounts[0]

	// A buggy client could repeat one id; the second row must not inherit the
	// same credential.
	response := saveAccounts(t, server, []model.Account{
		{ID: existing.ID, Name: "main"},
		{ID: existing.ID, Name: "copy"},
	})
	if response.Code != http.StatusOK {
		t.Fatalf("save: %d %s", response.Code, response.Body.String())
	}
	stored := st.Config().Accounts
	if len(stored) != 1 || stored[0].Key != "key-one" {
		t.Fatalf("duplicated identity duplicated the key: %#v", stored)
	}
}

// The console can test a saved credential by identity, so a revealed key that
// went stale in the browser is never used for the check.
func TestAccountTestAcceptsStoredIdentity(t *testing.T) {
	var seen atomic.Value
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seen.Store(request.Header.Get("Authorization"))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`)
	}))
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.Accounts = []model.Account{{Name: "main", Key: "stored-key", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	account := st.Config().Accounts[0]

	body, err := json.Marshal(map[string]string{"id": account.ID})
	if err != nil {
		t.Fatal(err)
	}
	response := accountsRequest(t, server, http.MethodPost, "/api/accounts/test", string(body))
	if response.Code != http.StatusOK {
		t.Fatalf("test by identity failed: %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || !payload.OK {
		t.Fatalf("stored credential was not accepted: %s", response.Body.String())
	}
	if value, _ := seen.Load().(string); value != "Bearer stored-key" {
		t.Fatalf("stored key was not used: %q", value)
	}
}
