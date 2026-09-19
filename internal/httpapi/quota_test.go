package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

// The console reads quota through the proxy API so the account keys never
// leave the server, and the endpoint stays behind the normal access guard.
func TestAccountQuotaEndpoint(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/users/me/plan":
			_, _ = io.WriteString(writer, `{"data":{"currentPeriodEnd":"2026-10-14T16:23:10Z","plan":{"displayName":"Cline Pass (Monthly)","isActive":true,"entitlements":{"cline_pass":{"inferenceCapThreshold":{"last5HoursUsageCostUSDPerUser":1000000000,"last7daysUsageCostUSDPerUser":2500000000,"last30daysUsageCostUSDPerUser":5000000000}}}}},"success":true}`)
		case "/users/me/plan/usage-limits":
			_, _ = io.WriteString(writer, `{"data":{"limits":[{"type":"five_hour","percentUsed":1,"resetsAt":"2026-09-19T17:07:47Z"},{"type":"weekly","percentUsed":35,"resetsAt":"2026-09-21T16:56:20Z"},{"type":"monthly","percentUsed":17,"resetsAt":"2026-10-14T16:56:20Z"}]},"success":true}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer upstreamServer.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = upstreamServer.URL
		config.Accounts = []model.Account{{Name: "main", Key: "sk_test", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}

	request := localRequest(http.MethodGet, "/api/accounts/quota", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("quota probe failed: %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Accounts []struct {
			Account          string `json:"account"`
			AccountID        string `json:"accountId"`
			OK               bool   `json:"ok"`
			Plan             string `json:"plan"`
			CurrentPeriodEnd string `json:"currentPeriodEnd"`
			Caps             *struct {
				FiveHour int64 `json:"fiveHour"`
				Weekly   int64 `json:"weekly"`
				Monthly  int64 `json:"monthly"`
			} `json:"caps"`
			Limits []struct {
				Type        string `json:"type"`
				PercentUsed int    `json:"percentUsed"`
			} `json:"limits"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode quota response: %v (%s)", err, response.Body.String())
	}
	if len(payload.Accounts) != 1 {
		t.Fatalf("expected one account, got %#v", payload.Accounts)
	}
	quota := payload.Accounts[0]
	if !quota.OK || quota.Account != "main" || quota.AccountID == "" || quota.Plan != "Cline Pass (Monthly)" {
		t.Fatalf("unexpected quota payload: %#v", quota)
	}
	if quota.CurrentPeriodEnd != "2026-10-14T16:23:10Z" {
		t.Fatalf("subscription expiry missing from API: %q", quota.CurrentPeriodEnd)
	}
	if quota.Caps == nil || quota.Caps.FiveHour != 1_000_000_000 || quota.Caps.Monthly != 5_000_000_000 {
		t.Fatalf("caps missing: %#v", quota.Caps)
	}
	if len(quota.Limits) != 3 || quota.Limits[1].Type != "weekly" || quota.Limits[1].PercentUsed != 35 {
		t.Fatalf("limits missing: %#v", quota.Limits)
	}
}
