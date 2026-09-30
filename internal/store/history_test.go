package store

import (
	"path/filepath"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

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
