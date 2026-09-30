package store

import (
	"errors"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

func TestSpendReservationUsesCurrentLedgerAndRetainsSharedRun(t *testing.T) {
	s, _ := testStore(t)
	grant := model.ProxyKeyGrant{ID: "k", SpendLimitUSD: 1}
	cost := 0.2
	for range 4 {
		if err := s.Record(model.HistoryEntry{KeyID: "k", Usage: &model.UsageStats{Cost: &cost}}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.ReserveSpend(grant)
	if err != nil {
		t.Fatal(err)
	}
	assertRefused := func(reason string) {
		t.Helper()
		hold, err := s.ReserveSpend(grant)
		var limit *SpendLimitError
		if hold != nil || !errors.As(err, &limit) || limit.Reason != reason {
			t.Fatalf("expected %s, got reservation %v, error %v", reason, hold, err)
		}
	}
	assertRefused("reserved")
	first.Retain()
	first.Release()
	assertRefused("reserved")
	// Record and reservation admission use the same lock; the cost is visible
	// before the last owner releases its reservation.
	if err := s.Record(model.HistoryEntry{KeyID: "k", Usage: &model.UsageStats{Cost: &cost}}); err != nil {
		t.Fatal(err)
	}
	first.Release()
	assertRefused("exceeded")
	if len(s.spendRunning) != 0 {
		t.Fatalf("leaked reservations: %v", s.spendRunning)
	}
	if err := s.ResetKeyUsage("k", false); err != nil {
		t.Fatal(err)
	}
	// Keep the existing estimation policy for keys without any history.
	for range 3 {
		hold, err := s.ReserveSpend(grant)
		if err != nil {
			t.Fatal(err)
		}
		defer hold.Release()
	}
	if hold, err := s.ReserveSpend(model.ProxyKeyGrant{ID: "free"}); hold != nil || err != nil {
		t.Fatalf("unlimited keys must not reserve: %v, %v", hold, err)
	}
}

func TestAdmissionWaitingForLedgerCommitSeesCompletedCharge(t *testing.T) {
	s, _ := testStore(t)
	grant := model.ProxyKeyGrant{ID: "k", SpendLimitUSD: 1}
	first, err := s.ReserveSpend(grant)
	if err != nil {
		t.Fatal(err)
	}
	// Queue a new admission while the finishing request holds the ledger lock.
	s.mu.Lock()
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		hold, err := s.ReserveSpend(grant)
		hold.Release()
		result <- err
	}()
	<-started
	cost := 1.0
	err = s.commitLocked(journalEntry{Kind: "record", Record: &model.HistoryEntry{KeyID: "k", Usage: &model.UsageStats{Cost: &cost}}})
	s.mu.Unlock()
	first.Release()
	if err != nil {
		t.Fatal(err)
	}
	var limit *SpendLimitError
	if err := <-result; !errors.As(err, &limit) || limit.Reason != "exceeded" {
		t.Fatalf("admission used a stale balance: %v", err)
	}
}
