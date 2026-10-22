package store

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

func TestSuccessfulRequestRecoversChannelStatusAndSurvivesReplay(t *testing.T) {
	s, dir := testStore(t)
	pinnable := true
	if _, err := s.UpdateModelMeta("cline-pass/test", func(m *model.ModelMeta) {
		m.Pinnable = &pinnable
		m.Upstreams = []string{"deepseek", "baseten"}
		m.UpstreamStatus = map[string]model.UpstreamStatus{
			"deepseek": {Status: "bad", Note: "previous rejection", CheckedAt: 1000},
			"baseten":  {Status: "limited", Note: "rate limited", CheckedAt: 1000},
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(model.HistoryEntry{Model: "cline-pass/test", Provider: "DeepSeek", TS: 2000, MS: 1200}); err != nil {
		t.Fatal(err)
	}
	check := func(label string, meta model.ModelMeta) {
		t.Helper()
		got := meta.UpstreamStatus["deepseek"]
		if got.Status != "ok" || got.Note != "" || got.CheckedAt != 2000 || got.MS != 1200 {
			t.Fatalf("%s: successful channel retained its old failure: %#v", label, got)
		}
		if other := meta.UpstreamStatus["baseten"]; other.Status != "limited" || other.CheckedAt != 1000 {
			t.Fatalf("%s: another channel was changed: %#v", label, other)
		}
		if meta.Pinnable == nil || !*meta.Pinnable || meta.PinReason != "" {
			t.Fatalf("%s: request changed pinning capability: %#v", label, meta)
		}
	}
	check("live", s.ModelMeta("cline-pass/test"))
	s.simulateCrash(t)
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	check("replayed", reopened.ModelMeta("cline-pass/test"))
}

func TestRecordedSuccessOnlyUpdatesTheConfirmedChannel(t *testing.T) {
	message := "stream truncated"
	for _, tc := range []struct {
		name       string
		entry      model.HistoryEntry
		unpinnable bool
		wantOK     string
	}{
		{name: "failed stream", entry: model.HistoryEntry{Provider: "deepseek", TS: 2000, Error: &message}},
		{name: "degraded compaction", entry: model.HistoryEntry{Provider: "deepseek", TS: 2000, Degraded: true}},
		{name: "unknown provider", entry: model.HistoryEntry{Provider: "unknown", TS: 2000}},
		{name: "missing provider", entry: model.HistoryEntry{TS: 2000}},
		{name: "older completion", entry: model.HistoryEntry{Provider: "deepseek", TS: 500}},
		{name: "pin ignored", entry: model.HistoryEntry{Provider: "deepseek", TS: 2000}, unpinnable: true},
		{name: "provider display name", entry: model.HistoryEntry{Provider: "Z.AI", TS: 2000}, wantOK: "z-ai"},
		{name: "gateway fallback", entry: model.HistoryEntry{
			Provider: "deepseek", TS: 2000,
			GatewayAttempts: []model.GatewayAttempt{
				{Provider: "deepseek", Status: 503},
				{Provider: "Z.AI", Status: 200, Success: true},
			},
		}, wantOK: "z-ai"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testStore(t)
			pinnable := !tc.unpinnable
			before := map[string]model.UpstreamStatus{
				"deepseek": {Status: "bad", Note: "old rejection", CheckedAt: 1000},
				"z-ai":     {Status: "bad", Note: "old rejection", CheckedAt: 1000},
			}
			if _, err := s.UpdateModelMeta("cline-pass/test", func(m *model.ModelMeta) {
				m.Pinnable = &pinnable
				m.Upstreams = []string{"deepseek", "z-ai"}
				m.UpstreamStatus = before
			}); err != nil {
				t.Fatal(err)
			}
			tc.entry.Model = "cline-pass/test"
			if err := s.Record(tc.entry); err != nil {
				t.Fatal(err)
			}
			meta := s.ModelMeta("cline-pass/test")
			for slug, old := range before {
				got := meta.UpstreamStatus[slug]
				if slug == tc.wantOK {
					if got.Status != "ok" || got.Note != "" || got.CheckedAt != 2000 {
						t.Fatalf("confirmed channel not recovered: %#v", got)
					}
				} else if !reflect.DeepEqual(got, old) {
					t.Fatalf("unconfirmed channel %s was changed: %#v", slug, got)
				}
			}
		})
	}
}

func TestHistoryIDsMigrateAndSurviveJournalRecovery(t *testing.T) {
	dir := t.TempDir()
	metadata := model.Metadata{}
	metadata.History = []model.HistoryEntry{{TS: 1000, Model: "same"}, {TS: 1000, Model: "same"}}
	if err := writeJSON(filepath.Join(dir, "metadata.json"), metadata); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows := s.Metadata().History
	if rows[0].ID == "" || rows[1].ID == "" || rows[0].ID == rows[1].ID {
		t.Fatalf("bad migrated identities: %+v", rows)
	}
	// Crash directly after migration: its IDs must already be recoverable.
	s.simulateCrash(t)
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for i, row := range reopened.Metadata().History {
		if row.ID != rows[i].ID {
			t.Fatalf("migration changed ID after restart: %s != %s", row.ID, rows[i].ID)
		}
	}
	if err := reopened.Record(model.HistoryEntry{TS: 1000, Model: "same"}); err != nil {
		t.Fatal(err)
	}
	newRow := reopened.Metadata().History[0]
	if newRow.ID == rows[0].ID || newRow.ID == rows[1].ID {
		t.Fatal("new row reused an ID")
	}
	reopened.simulateCrash(t)
	recovered, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if recovered.Metadata().History[0].ID != newRow.ID {
		t.Fatal("journal replay changed the new row ID")
	}
	if err := recovered.ClearHistory(); err != nil {
		t.Fatal(err)
	}
	if err := recovered.Record(model.HistoryEntry{TS: 1000, Model: "same"}); err != nil {
		t.Fatal(err)
	}
	if recovered.Metadata().History[0].ID == newRow.ID {
		t.Fatal("clearing history reused a cursor")
	}
}
