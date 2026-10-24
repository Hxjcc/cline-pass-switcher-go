package store

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

func TestUsageDoesNotInventMissingTokenCounts(t *testing.T) {
	s, _ := testStore(t)
	now := time.Now()
	cost := 0.4
	if err := s.Record(model.HistoryEntry{TS: now.UnixMilli(), Model: "test", Usage: &model.UsageStats{Cost: &cost}}); err != nil {
		t.Fatal(err)
	}
	r, err := s.usageReport(UsageQuery{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if r.Summary.UsageRequests != 0 || r.Summary.CostRequests != 1 || r.Summary.Cost != 0.4 || r.Summary.TotalTokens != 0 {
		t.Fatalf("cost-only response invented usage: %+v", r.Summary)
	}
}

func TestUsageSurvivesHistoryRetentionClearAndCrashReplay(t *testing.T) {
	s, dir := testStore(t)
	now := time.Now()
	cost := 0.0015
	for i := 0; i < model.HistoryLimit+7; i++ {
		if err := s.Record(model.HistoryEntry{TS: now.UnixMilli(), Model: "cline-pass/test", AccountID: "account-a", Account: "A", Usage: &model.UsageStats{PromptTokens: 100, CompletionTokens: 20, ReasoningTokens: 5, CachedTokens: 60, Cost: &cost}}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(st *Store) {
		t.Helper()
		r, err := st.usageReport(UsageQuery{From: usageDay(now), To: usageDay(now)}, now)
		if err != nil {
			t.Fatal(err)
		}
		count := int64(model.HistoryLimit + 7)
		if r.Summary.Requests != count || r.Summary.TotalTokens != count*120 || r.Summary.ReasoningTokens != count*5 || math.Abs(r.Summary.Cost-float64(count)*cost) > 1e-9 {
			t.Fatalf("bad usage totals: %+v", r.Summary)
		}
		if len(r.Models) != 1 || len(r.Trend) != 1 {
			t.Fatalf("bad usage groups: %+v", r)
		}
	}
	check(s)
	if len(s.History()) != model.HistoryLimit {
		t.Fatal("history retention changed")
	}
	if err := s.ClearHistory(); err != nil {
		t.Fatal(err)
	}
	check(s)
	s.simulateCrash(t)
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	check(reopened)
	if len(reopened.History()) != 0 {
		t.Fatal("cleared history returned after replay")
	}
	copy := reopened.Metadata()
	clear(copy.UsageLedger.Buckets)
	check(reopened)
}

func TestUsageMigrationImportsExistingHistoryOnce(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	cost := 0.2
	meta := model.Metadata{History: []model.HistoryEntry{{TS: now.UnixMilli(), Model: "old", Usage: &model.UsageStats{PromptTokens: 10, CompletionTokens: 2, Cost: &cost}}, {TS: now.UnixMilli(), Model: "old"}}}
	if err := writeJSON(filepath.Join(dir, "metadata.json"), meta); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Record(model.HistoryEntry{TS: now.UnixMilli(), Model: "new"}); err != nil {
		t.Fatal(err)
	}
	s.simulateCrash(t)
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	r, err := reopened.usageReport(UsageQuery{From: usageDay(now), To: usageDay(now)}, now)
	if err != nil {
		t.Fatal(err)
	}
	if r.ImportedRequests != 2 || r.Summary.Requests != 3 || r.Summary.TotalTokens != 12 || r.Summary.UsageRequests != 1 || r.Summary.CostRequests != 1 {
		t.Fatalf("migration lost or duplicated history: %+v", r)
	}
}

func TestUsageFiltersUseCalendarDaysAndStableAccountIdentity(t *testing.T) {
	s, _ := testStore(t)
	now := time.Now()
	today, _ := time.ParseInLocation(time.DateOnly, usageDay(now), usageLocation)
	if err := s.UpdateConfig(func(c *model.Config) {
		c.Accounts = []model.Account{{ID: "a", Name: "Old A", Key: "placeholder-a", Enabled: true}, {ID: "b", Name: "B", Key: "placeholder-b", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	cost := 0.3
	failure := "upstream failed after billing"
	for _, entry := range []model.HistoryEntry{
		{TS: today.Add(-time.Millisecond).UnixMilli(), Model: "alpha", AccountID: "a", Account: "Old A", KeyID: "x", KeyName: "X", Usage: &model.UsageStats{PromptTokens: 100, CompletionTokens: 10}},
		{TS: today.UnixMilli(), Model: "alpha", AccountID: "a", Account: "Old A", KeyID: "y", KeyName: "Y", Error: &failure, Usage: &model.UsageStats{PromptTokens: 200, CompletionTokens: 20, Cost: &cost}},
		{TS: today.Add(time.Hour).UnixMilli(), Model: "beta", AccountID: "b", Account: "B", KeyID: "x", KeyName: "X", Usage: &model.UsageStats{PromptTokens: 1000, CompletionTokens: 100, TotalTokens: 1100}},
	} {
		if err := s.Record(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpdateConfig(func(c *model.Config) { c.Accounts[0].Name = "Renamed A" }); err != nil {
		t.Fatal(err)
	}
	r, err := s.usageReport(UsageQuery{From: usageDay(today), To: usageDay(today), Model: "alpha", Account: "a", Key: "y"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if r.Summary.Requests != 1 || r.Summary.Errors != 1 || r.Summary.TotalTokens != 220 || r.Summary.Cost != cost || r.Accounts[0].Name != "Renamed A" {
		t.Fatalf("filters or account rename failed: %+v", r)
	}
	if len(r.Filters.Models) != 2 || len(r.Filters.Accounts) != 2 || len(r.Filters.Keys) != 2 {
		t.Fatal("options were narrowed by active filters")
	}
	all, err := s.usageReport(UsageQuery{From: usageDay(today.AddDate(0, 0, -1)), To: usageDay(today)}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Trend) != 2 || all.Trend[0].Requests != 1 || all.Trend[1].Requests != 2 || all.Summary.Requests != 3 {
		t.Fatalf("UTC+8 midnight was counted incorrectly: %+v", all.Trend)
	}
}

func TestUsagePrunesOldDaysAndIgnoresLateExpiredRecords(t *testing.T) {
	s, _ := testStore(t)
	now := time.Now()
	future := now.AddDate(0, 0, UsageRetentionDays)
	for _, ts := range []int64{now.UnixMilli(), future.UnixMilli(), now.UnixMilli()} {
		if err := s.Record(model.HistoryEntry{TS: ts, Model: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(s.Metadata().UsageLedger.Buckets); got != 1 {
		t.Fatalf("expired usage remained: %d buckets", got)
	}
	r, err := s.usageReport(UsageQuery{From: usageDay(future), To: usageDay(future)}, future)
	if err != nil {
		t.Fatal(err)
	}
	if r.Summary.Requests != 1 {
		t.Fatalf("expired record was counted: %+v", r.Summary)
	}
}
