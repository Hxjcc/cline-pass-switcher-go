package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

func TestAccountingFailureStopsLimitedGenerationButKeepsInventoryAndDiagnostics(t *testing.T) {
	dir := t.TempDir()
	st, server := newTestServerInDir(t, dir)
	upstream := &keyedUpstream{body: costedCompletion}
	us := upstream.server(t)
	configureKeys(t, st, us.URL, func(c *model.Config) {
		c.ProxyKey, c.AdminKey = "master", "admin"
		c.ProxyKeys = []model.ProxyKeyGrant{
			{ID: "k", Key: "limited", Enabled: true, SpendLimitUSD: 10},
			{ID: "free", Key: "free", Enabled: true},
			{ID: "off", Key: "off", Enabled: false},
		}
	})
	// Configuration was checkpointed, so the empty journal has no open handle.
	// Replace only this test's journal with a directory to fail the next append.
	journal := filepath.Join(dir, "store.journal")
	if err := os.Remove(journal); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(journal, 0o700); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }() // A failed store reports its error on first close.
	if response := postChat(server, "limited"); response.Code != http.StatusOK {
		t.Fatalf("the already-served result must be retained: %d %s", response.Code, response.Body)
	}
	for _, suffix := range []string{"/chat/completions", "/responses", "/responses/compact"} {
		for _, prefix := range []string{"", "/v1", "/api/v1"} {
			request := localRequest(http.MethodPost, prefix+suffix, strings.NewReader(`{"model":"cline-pass/test","input":"hello"}`))
			request.Header.Set("Authorization", "Bearer limited")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "accounting_unavailable") {
				t.Fatalf("%s%s was not refused: %d %s", prefix, suffix, response.Code, response.Body)
			}
		}
	}
	if got := len(upstream.credentials()); got != 1 {
		t.Fatalf("refused calls reached the upstream: %d", got)
	}
	for _, key := range []string{"master", "free"} {
		if response := postChat(server, key); response.Code != http.StatusOK {
			t.Fatalf("unlimited credential %s was blocked: %d", key, response.Code)
		}
	}
	for _, path := range []string{"/models", "/v1/models", "/api/v1/models"} {
		for _, tc := range []struct {
			key    string
			status int
		}{{"limited", 200}, {"off", 403}, {"wrong", 401}} {
			response := httptest.NewRecorder()
			getWithAdminKey(server, path, tc.key, response)
			if response.Code != tc.status {
				t.Fatalf("%s with %s: got %d, want %d", path, tc.key, response.Code, tc.status)
			}
		}
	}
	for _, tc := range []struct {
		path   string
		status int
	}{{"/healthz", 200}, {"/readyz", 503}, {"/api/meta", 200}} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, localRequest(http.MethodGet, tc.path, nil))
		if response.Code != tc.status || strings.Contains(response.Body.String(), "detail") {
			t.Fatalf("probe exposed details or wrong status: %s %d %s", tc.path, response.Code, response.Body)
		}
	}
	admin := httptest.NewRecorder()
	getWithAdminKey(server, "/api/meta", "admin", admin)
	var meta struct {
		Storage struct{ Status, Detail string }
	}
	if err := json.Unmarshal(admin.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Storage.Status != "unavailable" || meta.Storage.Detail == "" {
		t.Fatalf("admin did not receive diagnostics: %s", admin.Body)
	}
	client := httptest.NewRecorder()
	getWithAdminKey(server, "/api/meta", "limited", client)
	if strings.Contains(client.Body.String(), "storage") {
		t.Fatalf("client key saw admin-only diagnostics: %s", client.Body)
	}
	// Fixing the path alone must not silently forgive the missing record.
	if err := os.Remove(journal); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateConfig(func(*model.Config) {}); err == nil {
		t.Fatal("write failure must stay latched until recovery")
	}
}

func TestModelsOnlyRequireAnEnabledCredential(t *testing.T) {
	for _, condition := range []string{"spent", "reserved", "missing_account"} {
		t.Run(condition, func(t *testing.T) {
			st, server := newTestServer(t)
			grant := model.ProxyKeyGrant{ID: "k", Key: "limited", Enabled: true, SpendLimitUSD: 1}
			if condition == "missing_account" {
				grant.AccountID = "deleted"
			}
			configureKeys(t, st, "http://127.0.0.1:1", func(c *model.Config) { c.ProxyKeys = []model.ProxyKeyGrant{grant} })
			cost := 1.0
			if condition == "reserved" {
				cost = 0.8
			}
			if err := st.Record(model.HistoryEntry{KeyID: "k", Usage: &model.UsageStats{Cost: &cost}}); err != nil {
				t.Fatal(err)
			}
			if condition == "reserved" {
				hold, err := st.ReserveSpend(grant)
				if err != nil {
					t.Fatal(err)
				}
				defer hold.Release()
			}
			for _, path := range []string{"/models", "/v1/models", "/api/v1/models"} {
				response := httptest.NewRecorder()
				getWithAdminKey(server, path, "limited", response)
				if response.Code != http.StatusOK {
					t.Fatalf("inventory rejected: %d %s", response.Code, response.Body)
				}
			}
			if usage := st.KeyUsage()["k"]; usage.Requests != 1 {
				t.Fatalf("inventory was counted as usage: %+v", usage)
			}
			if response := postChat(server, "limited"); response.Code != http.StatusTooManyRequests {
				t.Fatalf("generation escaped its restriction: %d %s", response.Code, response.Body)
			}
		})
	}
}

func TestReadyRequiresConfiguration(t *testing.T) {
	_, server := newTestServer(t)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, localRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured server is ready: %d", response.Code)
	}
}

func TestReadyStaysAvailableDuringSnapshotDegradation(t *testing.T) {
	dir := t.TempDir()
	st, server := newTestServerInDir(t, dir)
	upstream := &keyedUpstream{body: costedCompletion}
	us := upstream.server(t)
	configureKeys(t, st, us.URL, func(c *model.Config) {
		c.ProxyKeys = []model.ProxyKeyGrant{{ID: "k", Key: "limited", Enabled: true, SpendLimitUSD: 10}}
	})
	metadata := filepath.Join(dir, "metadata.json")
	if err := os.Remove(metadata); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(metadata, 0o700); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(metadata) }()
	if err := st.UpdateConfig(func(*model.Config) {}); err != nil {
		t.Fatal(err)
	}
	if health := st.Health(); health.Status != "degraded" {
		t.Fatalf("unexpected health: %+v", health)
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, localRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "degraded") {
		t.Fatalf("snapshot failure removed readiness: %d %s", response.Code, response.Body)
	}
	if response := postChat(server, "limited"); response.Code != http.StatusOK {
		t.Fatalf("durable accounting was blocked: %d %s", response.Code, response.Body)
	}
	if usage := st.KeyUsage()["k"]; usage.SpentMicroUSD != 250_000 {
		t.Fatalf("charge was lost: %+v", usage)
	}
}
