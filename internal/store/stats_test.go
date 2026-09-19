package store

import (
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

func TestAccountStatsFollowIdentityAcrossRename(t *testing.T) {
	s, _ := testStore(t)
	if err := s.UpdateConfig(func(cfg *model.Config) {
		cfg.Accounts = []model.Account{{Name: "main", Key: "key", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	account := s.Config().Accounts[0]
	if account.ID == "" {
		t.Fatal("normalization should assign an identity")
	}
	if err := s.Record(model.HistoryEntry{
		Model: "cline-pass/test", Account: account.Name, AccountID: account.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if stats := s.Metadata().Stats[account.ID]; stats.Requests != 1 {
		t.Fatalf("counters should be keyed by identity: %#v", s.Metadata().Stats)
	}
	if err := s.UpdateConfig(func(cfg *model.Config) { cfg.Accounts[0].Name = "renamed" }); err != nil {
		t.Fatal(err)
	}
	if stats := s.Metadata().Stats[account.ID]; stats.Requests != 1 {
		t.Fatalf("renaming the account dropped its counters: %#v", s.Metadata().Stats)
	}
}

func TestLegacyNameKeyedStatsMigrateToIdentity(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateConfig(func(cfg *model.Config) {
		cfg.Accounts = []model.Account{{Name: "main", Key: "key", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	account := s.Config().Accounts[0]
	// Entries written by older versions only carried a name.
	if err := s.Record(model.HistoryEntry{Model: "cline-pass/test", Account: account.Name}); err != nil {
		t.Fatal(err)
	}
	if _, found := s.Metadata().Stats[account.Name]; !found {
		t.Fatalf("legacy record should still count by name: %#v", s.Metadata().Stats)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if stats := reopened.Metadata().Stats[account.ID]; stats.Requests != 1 {
		t.Fatalf("legacy counters were not migrated to the identity: %#v", reopened.Metadata().Stats)
	}
	if _, found := reopened.Metadata().Stats[account.Name]; found {
		t.Fatalf("legacy name key survived the migration: %#v", reopened.Metadata().Stats)
	}
}

// The migration must be durable: a crash between the rewrite and the next
// snapshot used to replay id-keyed records onto a name-keyed snapshot and then
// throw the legacy half away.
func TestLegacyStatsMigrationSurvivesCrashBeforeCheckpoint(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateConfig(func(cfg *model.Config) {
		cfg.Accounts = []model.Account{{Name: "main", Key: "key", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	account := s.Config().Accounts[0]
	// What a pre-identity version left behind: 100 requests under the name.
	if err := s.UpdateMetadata(func(meta *model.Metadata) {
		meta.Stats[account.Name] = model.AccountStats{Requests: 100, LastUsed: 100}
	}); err != nil {
		t.Fatal(err)
	}
	s.simulateCrash(t)

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if stats := reopened.Metadata().Stats[account.ID]; stats.Requests != 100 {
		t.Fatalf("migration did not move the legacy counters: %#v", reopened.Metadata().Stats)
	}
	// One new request is counted under the identity, then the process dies
	// before the next metadata snapshot.
	if err := reopened.Record(model.HistoryEntry{
		Model: "cline-pass/test", Account: account.Name, AccountID: account.ID, TS: 1000,
	}); err != nil {
		t.Fatal(err)
	}
	if stats := reopened.Metadata().Stats[account.ID]; stats.Requests != 101 {
		t.Fatalf("in-memory counters wrong before the crash: %#v", stats)
	}
	reopened.simulateCrash(t)

	final, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer final.Close()
	stats := final.Metadata().Stats[account.ID]
	if stats.Requests != 101 {
		t.Fatalf("recovery lost legacy counters: %#v", final.Metadata().Stats)
	}
	if _, found := final.Metadata().Stats[account.Name]; found {
		t.Fatalf("legacy name key survived recovery: %#v", final.Metadata().Stats)
	}
	if stats.LastUsed != 1000 {
		t.Fatalf("recovery lost the latest usage: %#v", stats)
	}
}
