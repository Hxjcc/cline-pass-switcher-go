package store

import (
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

// A spend limit is only meaningful if the counter outlives a restart, so the
// journal has to replay per-key usage exactly like the account counters.
func TestKeyUsageSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	cost := 0.25
	for index := range 3 {
		if err := first.Record(model.HistoryEntry{
			TS: int64(index + 1), Model: "cline-pass/test", KeyID: "key_1",
			Usage: &model.UsageStats{Cost: &cost},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if usage := first.Metadata().KeyUsage["key_1"]; usage.Requests != 3 || usage.SpentMicroUSD != 750_000 {
		t.Fatalf("usage not accumulated: %#v", usage)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	if usage := reopened.Metadata().KeyUsage["key_1"]; usage.Requests != 3 || usage.SpentMicroUSD != 750_000 {
		t.Fatalf("usage must survive a restart: %#v", usage)
	}

	// Resetting is durable too, and pruning drops counters of deleted grants.
	if err := reopened.ResetKeyUsage("key_1", false); err != nil {
		t.Fatal(err)
	}
	if _, found := reopened.Metadata().KeyUsage["key_1"]; found {
		t.Fatalf("reset must clear the counter: %#v", reopened.Metadata().KeyUsage)
	}
}

func TestPruneKeyUsageKeepsLiveGrants(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	cost := 1.0
	for _, id := range []string{"key_keep", "key_drop"} {
		if err := st.Record(model.HistoryEntry{TS: 1, Model: "cline-pass/test", KeyID: id,
			Usage: &model.UsageStats{Cost: &cost}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.PruneKeyUsage(map[string]struct{}{"key_keep": {}}); err != nil {
		t.Fatal(err)
	}
	usage := st.Metadata().KeyUsage
	if len(usage) != 1 || usage["key_keep"].SpentMicroUSD != 1_000_000 {
		t.Fatalf("prune kept the wrong rows: %#v", usage)
	}
}
