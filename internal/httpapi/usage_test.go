package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
	"github.com/Hxjcc/cline-pass-switcher-go/internal/store"
)

func TestUsageEndpointRequiresAdminAndOnlyReturnsReportedAmounts(t *testing.T) {
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(c *model.Config) { c.AdminKey = "usage-admin-secret"; c.ProxyKey = "usage-client-secret" }); err != nil {
		t.Fatal(err)
	}
	cost := 0.4
	message := "failed request"
	if err := st.Record(model.HistoryEntry{TS: time.Now().UnixMilli(), Model: "cline-pass/test", AccountID: "a", Account: "Account A", Error: &message, Usage: &model.UsageStats{PromptTokens: 20, CompletionTokens: 5, ReasoningTokens: 3, Cost: &cost}}); err != nil {
		t.Fatal(err)
	}
	send := func(key string) *httptest.ResponseRecorder {
		r := localRequest(http.MethodGet, "/api/usage?model=cline-pass%2Ftest", nil)
		r.Header.Set("X-Admin-Key", key)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	for _, key := range []string{"", "usage-client-secret"} {
		if w := send(key); w.Code != http.StatusUnauthorized {
			t.Fatalf("non-admin read usage: %d", w.Code)
		}
	}
	w := send("usage-admin-secret")
	if w.Code != http.StatusOK {
		t.Fatalf("usage HTTP %d: %s", w.Code, w.Body)
	}
	var r store.UsageReport
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Summary.Requests != 1 || r.Summary.Errors != 1 || r.Summary.TotalTokens != 25 || r.Summary.ReasoningTokens != 3 || r.Summary.Cost != 0.4 {
		t.Fatalf("reported usage was lost: %+v", r.Summary)
	}
	if strings.Contains(w.Body.String(), "usage-admin-secret") || strings.Contains(w.Body.String(), "usage-client-secret") {
		t.Fatal("credentials appeared in statistics")
	}
}

func TestUsageEndpointRejectsInvalidDateRanges(t *testing.T) {
	_, server := newTestServer(t)
	for _, query := range []string{"from=2026-02-30", "to=not-a-date", "from=2026-10-25&to=2026-10-24", "from=2020-01-01&to=2026-01-01"} {
		w := httptest.NewRecorder()
		server.ServeHTTP(w, localRequest(http.MethodGet, "/api/usage?"+query, nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid range accepted: %s HTTP %d", query, w.Code)
		}
	}
}
