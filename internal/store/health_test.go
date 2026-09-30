package store

import (
	"errors"
	"math"
	"os"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

type failingJournal struct {
	journalFile
	failure string
}

func (f failingJournal) Stat() (os.FileInfo, error) {
	if f.failure == "stat" {
		return nil, errors.New("injected stat failure")
	}
	return f.journalFile.Stat()
}

func (f failingJournal) Write(data []byte) (int, error) {
	switch f.failure {
	case "write", "rollback":
		return 0, errors.New("injected write failure")
	case "short_write":
		return f.journalFile.Write(data[:len(data)/2])
	}
	return f.journalFile.Write(data)
}

func (f failingJournal) Sync() error {
	if f.failure == "sync" {
		return errors.New("injected sync failure")
	}
	return f.journalFile.Sync()
}

func (f failingJournal) Truncate(size int64) error {
	if f.failure == "rollback" {
		return errors.New("injected rollback failure")
	}
	return f.journalFile.Truncate(size)
}

func TestJournalIOFailuresStopLimitedAdmissionsAndRecoverOnce(t *testing.T) {
	for _, failure := range []string{"open", "stat", "write", "short_write", "rollback", "sync"} {
		t.Run(failure, func(t *testing.T) {
			s, dir := testStore(t)
			cost := 0.01
			entry := model.HistoryEntry{KeyID: "k", Usage: &model.UsageStats{Cost: &cost}}
			wantRequests := int64(0)
			if failure == "sync" {
				for range journalSyncInterval - 1 {
					if err := s.Record(entry); err != nil {
						t.Fatal(err)
					}
				}
				// The last record is appended and published before batch fsync.
				wantRequests = journalSyncInterval
			}
			if failure == "open" {
				s.journalPath = t.TempDir()
			} else {
				file, err := s.journalHandleLocked()
				if err != nil {
					t.Fatal(err)
				}
				s.journal = failingJournal{journalFile: file, failure: failure}
			}
			if err := s.Record(entry); err == nil {
				t.Fatal("expected a failed journal commit")
			}
			if health := s.Health(); health.Status != "unavailable" || health.Detail == "" {
				t.Fatalf("write failure was hidden: %+v", health)
			}
			if hold, err := s.ReserveSpend(model.ProxyKeyGrant{ID: "k", SpendLimitUSD: 10}); hold != nil || !errors.Is(err, ErrAccountingUnavailable) {
				t.Fatalf("limited key admitted after failure: %v, %v", hold, err)
			}
			// No automatic retry of the request record: fsync failures may have
			// committed it already, and replay must charge it exactly once.
			s.simulateCrash(t)
			recovered, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer recovered.Close()
			usage := recovered.KeyUsage()["k"]
			if usage.Requests != wantRequests || usage.SpentMicroUSD != wantRequests*10_000 {
				t.Fatalf("incorrect recovered accounting: %+v, want %d requests", usage, wantRequests)
			}
		})
	}
}

func TestCheckpointFailureDegradesWithoutBlockingDurableCharges(t *testing.T) {
	for _, repair := range []bool{false, true} {
		t.Run(map[bool]string{false: "recover_from_journal", true: "successful_checkpoint_clears_warning"}[repair], func(t *testing.T) {
			s, dir := testStore(t)
			original := s.metaPath
			s.metaPath = t.TempDir()
			cost := 0.25
			if err := s.Record(model.HistoryEntry{KeyID: "k", Usage: &model.UsageStats{Cost: &cost}}); err != nil {
				t.Fatal(err)
			}
			if err := s.UpdateMetadata(func(*model.Metadata) {}); err != nil {
				t.Fatalf("durable journal commit must succeed: %v", err)
			}
			if health := s.Health(); health.Status != "degraded" {
				t.Fatalf("expected degraded snapshot, got %+v", health)
			}
			hold, err := s.ReserveSpend(model.ProxyKeyGrant{ID: "k", SpendLimitUSD: 10})
			if err != nil {
				t.Fatalf("snapshot failure blocked reliable accounting: %v", err)
			}
			hold.Release()
			if repair {
				s.metaPath = original
				if err := s.UpdateMetadata(func(*model.Metadata) {}); err != nil {
					t.Fatal(err)
				}
				if health := s.Health(); health.Status != "ok" {
					t.Fatalf("successful checkpoint left stale warning: %+v", health)
				}
			}
			s.simulateCrash(t)
			recovered, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer recovered.Close()
			if usage := recovered.KeyUsage()["k"]; usage.Requests != 1 || usage.SpentMicroUSD != 250_000 {
				t.Fatalf("checkpoint recovery lost or duplicated the charge: %+v", usage)
			}
		})
	}
}

func TestLostRecordIsNotClearedByUnrelatedSuccessfulWrite(t *testing.T) {
	s, _ := testStore(t)
	cost := math.NaN()
	if err := s.Record(model.HistoryEntry{KeyID: "k", Usage: &model.UsageStats{Cost: &cost}}); err == nil {
		t.Fatal("expected invalid record to fail")
	}
	if err := s.UpdateConfig(func(*model.Config) {}); err != nil {
		t.Fatal(err)
	}
	if health := s.Health(); health.Status != "unavailable" {
		t.Fatalf("a successful write must not forgive a lost charge: %+v", health)
	}
}
