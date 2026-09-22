package upstream

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/store"
)

func TestAccountHealthIsKeyedByIdentityNotName(t *testing.T) {
	var health accountHealth
	now := time.Now()
	health.penalize("acc_1", accountKeyHash("bad-key"), now.Add(time.Minute))

	// Two accounts with the same name must not share a cooldown.
	excluded := health.excluded(now, map[string]string{
		"acc_1": accountKeyHash("bad-key"),
		"acc_2": accountKeyHash("bad-key"),
	})
	if _, found := excluded["acc_1"]; !found {
		t.Fatal("penalized account should be excluded")
	}
	if _, found := excluded["acc_2"]; found {
		t.Fatal("a different identity must keep its own health")
	}

	// Rotating the credential clears the verdict that belonged to the old key.
	excluded = health.excluded(now, map[string]string{"acc_1": accountKeyHash("new-key")})
	if len(excluded) != 0 {
		t.Fatalf("rotated credential kept its cooldown: %#v", excluded)
	}
}

func TestRateLimitCooldownOutlastsAShortRetry(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpdateConfig(func(cfg *model.Config) {
		cfg.Accounts = []model.Account{
			{Name: "limited", Key: "key-a", Enabled: true},
			{Name: "spare", Key: "key-b", Enabled: true},
		}
	}); err != nil {
		t.Fatal(err)
	}
	service := New(st)
	limited := st.Config().Accounts[0]
	service.noteAccountStatus(limited, http.StatusTooManyRequests)

	if _, cooling := service.excludedAccounts(time.Now().Add(30 * time.Second))[limited.ID]; !cooling {
		t.Fatal("a 429 should still be cooling after 30 seconds")
	}
	if _, cooling := service.excludedAccounts(time.Now().Add(accountLimitCooldown + time.Second))[limited.ID]; cooling {
		t.Fatal("the rate-limit cooldown should expire")
	}
}

func TestAccountAttemptLimitIsCapped(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpdateConfig(func(cfg *model.Config) {
		for index := 0; index < maxAccountAttemptsPerChannel+3; index++ {
			cfg.Accounts = append(cfg.Accounts, model.Account{Name: "pool", Key: "key", Enabled: true})
		}
	}); err != nil {
		t.Fatal(err)
	}
	service := New(st)
	if limit := service.AccountAttemptLimit(); limit != maxAccountAttemptsPerChannel {
		t.Fatalf("account attempts should be capped at %d, got %d", maxAccountAttemptsPerChannel, limit)
	}
	if !service.AccountFailoverAvailable() {
		t.Fatal("a pool of usable accounts should allow failover")
	}
}

func TestTestAccountUsesHTTPStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		ok     bool
	}{
		{"plain text 401", http.StatusUnauthorized, "unauthorized", false},
		{"plain text 403", http.StatusForbidden, "forbidden", false},
		{"json 401", http.StatusUnauthorized, `{"error":{"message":"no"}}`, false},
		{"server error", http.StatusServiceUnavailable, `{"error":{"message":"overloaded"}}`, false},
		{"success", http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "text/plain")
				writer.WriteHeader(tc.status)
				_, _ = io.WriteString(writer, tc.body)
			}))
			defer upstreamServer.Close()

			st, err := store.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if err := st.UpdateConfig(func(cfg *model.Config) { cfg.UpstreamBase = upstreamServer.URL }); err != nil {
				t.Fatal(err)
			}
			result := New(st).TestAccount(t.Context(), "sk_test", "")
			if result.OK != tc.ok {
				t.Fatalf("ok=%v, want %v (%s)", result.OK, tc.ok, result.Error)
			}
			if !tc.ok && result.Error == "" {
				t.Fatal("failed account test must explain why")
			}
		})
	}
}

func TestTestAccountCanAddressStoredKeyByIdentity(t *testing.T) {
	var seen string
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seen = request.Header.Get("Authorization")
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`)
	}))
	defer upstreamServer.Close()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpdateConfig(func(cfg *model.Config) {
		cfg.UpstreamBase = upstreamServer.URL
		cfg.Accounts = []model.Account{{Name: "saved", Key: "stored-key", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	account := st.Config().Accounts[0]
	result := New(st).TestAccount(t.Context(), "", account.ID)
	if !result.OK {
		t.Fatalf("stored account test failed: %s", result.Error)
	}
	if seen != "Bearer stored-key" {
		t.Fatalf("stored key was not used: %q", seen)
	}
}

func TestFetchOfficialModelsAlwaysReturnsAddedArray(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/cline":
			_, _ = io.WriteString(writer, `{"clinePass":[{"id":"cline-pass/known"},{"id":"cline-pass/also-known"}]}`)
		case "/models-dev":
			_, _ = io.WriteString(writer, `{"providers":{"cline-pass":{"models":{}}}}`)
		default:
			_, _ = io.WriteString(writer, "no models here")
		}
	}))
	defer upstreamServer.Close()

	previous := []string{officialClineModelsURL, officialModelsDevURL, officialDocsURL}
	officialClineModelsURL = upstreamServer.URL + "/cline"
	officialModelsDevURL = upstreamServer.URL + "/models-dev"
	officialDocsURL = upstreamServer.URL + "/docs"
	t.Cleanup(func() {
		officialClineModelsURL, officialModelsDevURL, officialDocsURL = previous[0], previous[1], previous[2]
	})

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpdateConfig(func(cfg *model.Config) {
		cfg.KnownModels = []string{"cline-pass/known", "cline-pass/also-known"}
	}); err != nil {
		t.Fatal(err)
	}

	result, err := New(st).FetchOfficialModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if result.Added == nil {
		t.Fatal("added must be an empty slice, not nil")
	}
	if len(result.Added) != 0 {
		t.Fatalf("nothing new should be added: %#v", result.Added)
	}
	raw, err := json.Marshal(result.Added)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "[]" {
		t.Fatalf("added should marshal as [], got %s", raw)
	}
}
