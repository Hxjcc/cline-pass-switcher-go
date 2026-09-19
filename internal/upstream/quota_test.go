package upstream

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

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
