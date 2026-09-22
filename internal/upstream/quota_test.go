package upstream

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/store"
)

const quotaPlanBody = `{"data":{"userId":"usr-1","plan":{"id":"pln-1","name":"Cline Pass (Monthly)[Internal]","displayName":"Cline Pass (Monthly)","entitlements":{"cline_pass":{"enabled":true,"inferenceCapThreshold":{"last5HoursUsageCostUSDPerUser":1000000000,"last7daysUsageCostUSDPerUser":2500000000,"last30daysUsageCostUSDPerUser":5000000000}}},"isActive":true},"subscriptionId":"sub-1","currentPeriodStart":"2026-09-14T16:23:10Z","currentPeriodEnd":"2026-10-14T16:23:10Z"},"success":true}`
const quotaLimitsBody = `{"data":{"limits":[{"type":"five_hour","percentUsed":1,"resetsAt":"2026-09-19T17:07:47.759222671Z"},{"type":"weekly","percentUsed":35,"resetsAt":"2026-09-21T16:56:20.76252457Z"},{"type":"monthly","percentUsed":17,"resetsAt":"2026-10-14T16:56:20.76495691Z"}]},"success":true}`

func newQuotaTestStore(t *testing.T, baseURL string) *store.Store {
	t.Helper()
	for _, name := range []string{"CLINE_PASS_KEY", "PROXY_KEY", "PUBLIC_BASE_URL", "PORT"} {
		t.Setenv(name, "")
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := st.UpdateConfig(func(cfg *model.Config) {
		cfg.UpstreamBase = baseURL
		cfg.Accounts = []model.Account{{Name: "main", Key: "sk_test", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestProbeQuotaParsesPlanAndUtilization(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer sk_test" {
			t.Errorf("probe must use the account key: %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/users/me/plan":
			_, _ = io.WriteString(writer, quotaPlanBody)
		case "/users/me/plan/usage-limits":
			_, _ = io.WriteString(writer, quotaLimitsBody)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer upstreamServer.Close()

	st := newQuotaTestStore(t, upstreamServer.URL)
	account := st.Config().Accounts[0]
	quota := New(st).ProbeQuota(t.Context(), account.ID, false)
	if !quota.OK || quota.Error != "" {
		t.Fatalf("probe failed: %#v", quota)
	}
	if quota.Plan != "Cline Pass (Monthly)" || !quota.Active {
		t.Fatalf("plan not parsed: %#v", quota)
	}
	if quota.CurrentPeriodEnd != "2026-10-14T16:23:10Z" {
		t.Fatalf("expiry must come from the subscription, not the monthly reset: %q", quota.CurrentPeriodEnd)
	}
	if quota.Caps == nil || quota.Caps.FiveHour != 1_000_000_000 || quota.Caps.Weekly != 2_500_000_000 || quota.Caps.Monthly != 5_000_000_000 {
		t.Fatalf("caps not parsed: %#v", quota.Caps)
	}
	if len(quota.Limits) != 3 {
		t.Fatalf("limits not parsed: %#v", quota.Limits)
	}
	weekly := quota.Limits[1]
	if weekly.Type != "weekly" || weekly.PercentUsed != 35 || !strings.HasPrefix(weekly.ResetsAt, "2026-09-21T16:56:20") {
		t.Fatalf("weekly limit wrong: %#v", weekly)
	}
	if quota.FetchedAt == 0 || quota.Account != "main" || quota.AccountID != account.ID {
		t.Fatalf("missing metadata: %#v", quota)
	}
}

func TestProbeQuotaDoesNotUseCancellationOrUsageResetAsExpiry(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/users/me/plan/usage-limits" {
			_, _ = io.WriteString(writer, quotaLimitsBody)
			return
		}
		_, _ = io.WriteString(writer, `{"data":{"plan":{"displayName":"Cline Pass","isActive":true},"currentPeriodEnd":null,"cancelAt":"2026-10-14T16:23:10Z","canceledAt":"2026-09-14T16:27:56Z"}}`)
	}))
	defer upstreamServer.Close()
	st := newQuotaTestStore(t, upstreamServer.URL)
	quota := New(st).ProbeQuota(t.Context(), st.Config().Accounts[0].ID, false)
	if !quota.OK || quota.CurrentPeriodEnd != "" || len(quota.Limits) != 3 {
		t.Fatalf("missing period end should stay unknown while utilization remains available: %#v", quota)
	}
}

func TestProbeQuotaReportsUpstreamAuthFailure(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(writer, `{"error":"Unauthorized: Please make sure you're using the latest version of Cline and re-authenticate your Cline account."}`)
	}))
	defer upstreamServer.Close()

	st := newQuotaTestStore(t, upstreamServer.URL)
	quota := New(st).ProbeQuota(t.Context(), st.Config().Accounts[0].ID, false)
	if quota.OK {
		t.Fatalf("rejected key must not report a quota: %#v", quota)
	}
	if !strings.Contains(quota.Error, "未授权") {
		t.Fatalf("auth failure should be explained: %q", quota.Error)
	}
}

func TestProbeQuotaCachesUntilRefreshed(t *testing.T) {
	var hits atomic.Int32
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/users/me/plan/usage-limits" {
			_, _ = io.WriteString(writer, quotaLimitsBody)
			return
		}
		_, _ = io.WriteString(writer, quotaPlanBody)
	}))
	defer upstreamServer.Close()

	st := newQuotaTestStore(t, upstreamServer.URL)
	service := New(st)
	id := st.Config().Accounts[0].ID
	if quota := service.ProbeQuota(t.Context(), id, false); !quota.OK {
		t.Fatalf("first probe failed: %#v", quota)
	}
	if hits.Load() != 2 {
		t.Fatalf("first probe should read plan and limits, got %d calls", hits.Load())
	}
	if quota := service.ProbeQuota(t.Context(), id, false); !quota.OK {
		t.Fatalf("cached probe failed: %#v", quota)
	}
	if hits.Load() != 2 {
		t.Fatalf("cached probe should not hit the upstream, got %d calls", hits.Load())
	}
	if quota := service.ProbeQuota(t.Context(), id, true); !quota.OK {
		t.Fatalf("forced probe failed: %#v", quota)
	}
	if hits.Load() != 4 {
		t.Fatalf("forced probe should refresh both endpoints, got %d calls", hits.Load())
	}
}

func TestQuotaExhaustedUntilWaitsForTheLatestFullWindow(t *testing.T) {
	now := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	soon := now.Add(time.Hour).Format(time.RFC3339Nano)
	later := now.Add(48 * time.Hour).Format(time.RFC3339Nano)
	quota := AccountQuota{OK: true, Limits: []QuotaLimit{
		{Type: "five_hour", PercentUsed: 100, ResetsAt: soon},
		{Type: "weekly", PercentUsed: 100, ResetsAt: later},
		{Type: "monthly", PercentUsed: 40, ResetsAt: now.Add(10 * 24 * time.Hour).Format(time.RFC3339Nano)},
	}}
	until, full := quotaExhaustedUntil(quota, now)
	if !full || !until.Equal(now.Add(48*time.Hour)) {
		t.Fatalf("full windows should wait for the later reset, got full=%v until=%s", full, until)
	}

	under, full := quotaExhaustedUntil(AccountQuota{OK: true, Limits: []QuotaLimit{{Type: "five_hour", PercentUsed: 99, ResetsAt: soon}}}, now)
	if full || !under.IsZero() {
		t.Fatalf("99%% must stay eligible: full=%v until=%s", full, under)
	}

	fallback, full := quotaExhaustedUntil(AccountQuota{OK: true, Limits: []QuotaLimit{{Type: "five_hour", PercentUsed: 100}}}, now)
	if !full || !fallback.Equal(now.Add(quotaExhaustedFallback)) {
		t.Fatalf("a full window without a reset time should wait %s, got full=%v until=%s", quotaExhaustedFallback, full, fallback)
	}
}

func TestQuotaHoldFollowsTheCredential(t *testing.T) {
	service := New(newQuotaTestStore(t, "http://127.0.0.1"))
	account := model.Account{ID: "acc_1", Name: "main", Key: "old-key", Enabled: true}
	reset := time.Now().Add(time.Hour).Format(time.RFC3339Nano)
	service.noteQuota(account, AccountQuota{OK: true, Limits: []QuotaLimit{{Type: "five_hour", PercentUsed: 100, ResetsAt: reset}}})

	held := service.quotaHolds(time.Now(), map[string]string{account.ID: accountKeyHash(account.Key)})
	if _, found := held[account.ID]; !found {
		t.Fatal("a full window should hold the account that was probed")
	}
	rotated := service.quotaHolds(time.Now(), map[string]string{account.ID: accountKeyHash("new-key")})
	if len(rotated) != 0 {
		t.Fatalf("rotating the key should drop the hold: %#v", rotated)
	}
}

func limitsDocument(percent int, reset string) string {
	return fmt.Sprintf(`{"data":{"limits":[{"type":"five_hour","percentUsed":%d,"resetsAt":%q},{"type":"weekly","percentUsed":1,"resetsAt":%q},{"type":"monthly","percentUsed":1,"resetsAt":%q}]},"success":true}`, percent, reset, reset, reset)
}

func TestPickAccountSkipsAFullWindowWhenAnotherAccountHasRoom(t *testing.T) {
	reset := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)
	var planHits atomic.Int32
	var chatKeys []string
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/users/me/plan":
			planHits.Add(1)
			_, _ = io.WriteString(writer, quotaPlanBody)
		case "/users/me/plan/usage-limits":
			percent := 12
			if request.Header.Get("Authorization") == "Bearer full-key" {
				percent = 100
			}
			_, _ = io.WriteString(writer, limitsDocument(percent, reset))
		case "/chat/completions":
			chatKeys = append(chatKeys, request.Header.Get("Authorization"))
			_, _ = io.WriteString(writer, `{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer upstreamServer.Close()

	st := newQuotaTestStore(t, upstreamServer.URL)
	if err := st.UpdateConfig(func(cfg *model.Config) {
		cfg.AccountMode = "single"
		cfg.ActiveAccount = 0
		cfg.Accounts = []model.Account{
			{Name: "full", Key: "full-key", Enabled: true},
			{Name: "spare", Key: "spare-key", Enabled: true},
		}
	}); err != nil {
		t.Fatal(err)
	}
	service := New(st)
	account := service.pickAccount(t.Context())
	if account.Name != "spare" {
		t.Fatalf("selection should leave the full account unused, got %#v", account)
	}
	again := service.pickAccount(t.Context())
	if again.ID != account.ID {
		t.Fatalf("the spare account should stay selected, got %#v", again)
	}
	if planHits.Load() != 2 {
		t.Fatalf("both accounts are probed once, then the spare snapshot is reused, got %d plan reads", planHits.Load())
	}

	result := service.AttemptNonStream(t.Context(), "cline-pass/test", map[string]any{
		"model": "cline-pass/test", "messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}, Attempt{})
	if result.Status != http.StatusOK || result.Account.Name != "spare" {
		t.Fatalf("the model call should use the spare account: status=%d account=%s err=%s", result.Status, result.Account.Name, result.NetErr)
	}
	if len(chatKeys) != 1 || chatKeys[0] != "Bearer spare-key" {
		t.Fatalf("full account reached the model endpoint: %#v", chatKeys)
	}
	if planHits.Load() != 2 {
		t.Fatalf("a fresh spare snapshot should not probe again, got %d plan reads", planHits.Load())
	}
}

func TestPickAccountKeepsTheOnlyFullAccountAndIgnoresProbeFailures(t *testing.T) {
	var hits atomic.Int32
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		http.Error(writer, "quota down", http.StatusBadGateway)
	}))
	defer upstreamServer.Close()

	st := newQuotaTestStore(t, upstreamServer.URL)
	service := New(st)
	only := st.Config().Accounts[0]
	if account := service.pickAccount(t.Context()); account.ID != only.ID || hits.Load() != 0 {
		t.Fatalf("one account needs no plan read: account=%#v hits=%d", account, hits.Load())
	}

	if err := st.UpdateConfig(func(cfg *model.Config) {
		cfg.Accounts = append(cfg.Accounts, model.Account{Name: "spare", Key: "spare-key", Enabled: true})
	}); err != nil {
		t.Fatal(err)
	}
	account := service.pickAccount(t.Context())
	if account.Name != "main" {
		t.Fatalf("a failed plan read should keep the selected account: %#v", account)
	}
	if hits.Load() == 0 {
		t.Fatal("two accounts should try a plan read")
	}
	probes := hits.Load()
	if again := service.pickAccount(t.Context()); again.ID != account.ID || hits.Load() != probes {
		t.Fatalf("a failed plan read should be reused: account=%#v hits=%d want %d", again, hits.Load(), probes)
	}
}

func TestProbeQuotaFailsClosedWithoutAccount(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	quota := New(st).ProbeQuota(t.Context(), "missing", false)
	if quota.OK || quota.Error == "" {
		t.Fatalf("missing account should be reported: %#v", quota)
	}
}
