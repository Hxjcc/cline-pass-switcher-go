package store

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	t.Setenv("CLINE_PASS_KEY", "")
	t.Setenv("PROXY_KEY", "")
	t.Setenv("PUBLIC_BASE_URL", "")
	t.Setenv("PORT", "")
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, dir
}

// simulateCrash releases the file handles without checkpointing, which is what
// a killed process leaves behind: the journal on disk, no snapshot updates.
func (s *Store) simulateCrash(t *testing.T) {
	t.Helper()
	s.closed = true
	if err := s.closeJournalLocked(); err != nil {
		t.Fatal(err)
	}
	if err := s.lock.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFailedCommitsDoNotChangeMemoryOrSnapshots(t *testing.T) {
	s, _ := testStore(t)
	if err := s.UpdateModelMetaForTest(); err != nil {
		t.Fatal(err)
	}
	beforeConfig, beforeMeta := s.Config(), s.Metadata()
	configDisk, _ := os.ReadFile(s.configPath)
	metaDisk, _ := os.ReadFile(s.metaPath)
	original := s.journalPath
	s.journalPath = t.TempDir()
	for _, update := range []func() error{
		func() error {
			return s.UpdateConfig(func(c *model.Config) {
				c.ProxyKey = "new"
				c.PerModel["test"] = model.PerModelConfig{Upstreams: []string{"changed"}}
			})
		},
		func() error { return s.UpdateMetadata(func(m *model.Metadata) { delete(m.Models, "test") }) },
		func() error {
			_, err := s.UpdateModelMeta("test", func(m *model.ModelMeta) { m.UpstreamStatus["a"] = model.UpstreamStatus{Status: "error"} })
			return err
		},
		func() error { return s.Record(model.HistoryEntry{Model: "cline-pass/new", Account: "test"}) },
		func() error { return s.RemoveModel("test") }, s.ClearHistory,
	} {
		if err := update(); err == nil {
			t.Fatal("expected persistence error")
		}
		if !reflect.DeepEqual(beforeConfig, s.Config()) || !reflect.DeepEqual(beforeMeta, s.Metadata()) {
			t.Fatal("failed write changed memory")
		}
	}
	s.journalPath = original
	a, _ := os.ReadFile(s.configPath)
	b, _ := os.ReadFile(s.metaPath)
	if !bytes.Equal(a, configDisk) || !bytes.Equal(b, metaDisk) {
		t.Fatal("failed write changed snapshots")
	}
}

func (s *Store) UpdateModelMetaForTest() error {
	_, err := s.UpdateModelMeta("test", func(m *model.ModelMeta) { m.UpstreamStatus["a"] = model.UpstreamStatus{Status: "ok"} })
	return err
}

func TestRecordAppendsJournalAndRecoversExactlyOnce(t *testing.T) {
	s, dir := testStore(t)
	if err := s.UpdateMetadata(func(*model.Metadata) {}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(s.metaPath)
	for i := 0; i < 3; i++ {
		if err := s.Record(model.HistoryEntry{Model: "cline-pass/test", Account: "test", TS: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := os.ReadFile(s.metaPath)
	if !bytes.Equal(before, after) {
		t.Fatal("Record rewrote metadata snapshot")
	}
	// Simulate a crash: release only the process lock, without checkpointing.
	s.simulateCrash(t)
	recovered, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered.Metadata().History) != 3 || recovered.Metadata().Stats["test"].Requests != 3 || !slices.Contains(recovered.Config().KnownModels, "cline-pass/test") {
		t.Fatal("journal not recovered")
	}
	// Simulate interruption after both snapshots but before journal truncation.
	if err := recovered.writeConfigLocked(); err != nil {
		t.Fatal(err)
	}
	if err := recovered.writeMetaLocked(); err != nil {
		t.Fatal(err)
	}
	recovered.simulateCrash(t)
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.Metadata().Stats["test"].Requests != 3 {
		t.Fatal("replay double-counted requests")
	}
}

func TestRemoveModelRecoversInterruptedTwoFileCheckpoint(t *testing.T) {
	s, dir := testStore(t)
	if err := s.UpdateConfig(func(c *model.Config) { c.KnownModels = []string{"test"} }); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateModelMetaForTest(); err != nil {
		t.Fatal(err)
	}
	s.metaPath = t.TempDir() // Config rename succeeds; metadata rename fails.
	if err := s.RemoveModel("test"); err != nil {
		t.Fatal("durable journal commit failed:", err)
	}
	if err := s.Close(); err == nil {
		t.Fatal("expected checkpoint failure")
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if slices.Contains(reopened.Config().KnownModels, "test") {
		t.Fatal("config removal lost")
	}
	if _, found := reopened.Metadata().Models["test"]; found {
		t.Fatal("metadata removal lost")
	}
}

func TestDataDirectoryLockAndTornTailRecovery(t *testing.T) {
	s, dir := testStore(t)
	if second, err := Open(dir); err == nil {
		second.Close()
		t.Fatal("second process accepted")
	}
	if err := s.Record(model.HistoryEntry{Model: "test"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "store.journal"), os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"sequence":`)
	_ = f.Close()
	s.simulateCrash(t)
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if len(reopened.Metadata().History) != 1 {
		t.Fatal("lost committed history before torn tail")
	}
	if err := reopened.Record(model.HistoryEntry{Model: "test"}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointCompactsJournalWithoutLosingHistory(t *testing.T) {
	s, dir := testStore(t)
	for i := 0; i < checkpointInterval+7; i++ {
		if err := s.Record(model.HistoryEntry{Model: "test", Account: "test", TS: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := model.LoadMetadata(s.metaPath)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Stats["test"].Requests != checkpointInterval {
		t.Fatal("periodic checkpoint missing")
	}
	s.simulateCrash(t)
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	meta := reopened.Metadata()
	if meta.Stats["test"].Requests != checkpointInterval+7 || len(meta.History) != 100 || meta.History[0].TS != checkpointInterval+6 {
		t.Fatal("checkpoint or replay lost request history")
	}
}

func TestEnvironmentOverridesRecoveredConfiguration(t *testing.T) {
	s, dir := testStore(t)
	s.metaPath = t.TempDir() // Retain a committed config operation in the journal.
	if err := s.UpdateConfig(func(c *model.Config) { c.ProxyKey = "journal-key" }); err != nil {
		t.Fatal(err)
	}
	s.simulateCrash(t)
	t.Setenv("PROXY_KEY", "environment-key")
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.ProxyKey() != "environment-key" {
		t.Fatal("journal overrode environment")
	}
}

func TestRecordsSyncJournalInBatches(t *testing.T) {
	s, _ := testStore(t)
	for i := 0; i < journalSyncInterval-1; i++ {
		if err := s.Record(model.HistoryEntry{Model: "test", TS: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if s.journalSyncs != 0 {
		t.Fatalf("records must not fsync one by one: %d syncs", s.journalSyncs)
	}
	if err := s.Record(model.HistoryEntry{Model: "test", TS: -1}); err != nil {
		t.Fatal(err)
	}
	if s.journalSyncs != 1 {
		t.Fatalf("batch boundary must fsync once: %d", s.journalSyncs)
	}
	// Configuration cannot be rebuilt from anything else, so it always waits
	// for the disk even though it is written to the same journal.
	if err := s.UpdateConfig(func(config *model.Config) { config.ProxyKey = "batched" }); err != nil {
		t.Fatal(err)
	}
	if s.journalSyncs != 2 {
		t.Fatalf("configuration change must fsync: %d", s.journalSyncs)
	}
	if s.Config().ProxyKey != "batched" {
		t.Fatal("configuration not applied")
	}
}

func TestCloseFlushesBatchedRecords(t *testing.T) {
	s, dir := testStore(t)
	for i := 0; i < 3; i++ {
		if err := s.Record(model.HistoryEntry{Model: "test", TS: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if s.journalSyncs != 0 {
		t.Fatalf("records should still be in the page cache: %d", s.journalSyncs)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s.journalSyncs != 1 {
		t.Fatalf("close must flush the journal: %d", s.journalSyncs)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if len(reopened.Metadata().History) != 3 {
		t.Fatalf("records lost on close: %#v", reopened.Metadata().History)
	}
}
