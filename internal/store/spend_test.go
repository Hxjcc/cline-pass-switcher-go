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
	// A reset discards the pricing history, so allow one request to establish
	// a new average before admitting concurrent spend again.
	hold, err := s.ReserveSpend(grant)
	if err != nil {
		t.Fatal(err)
	}
	assertRefused("reserved")
	hold.Release()
	if err := s.Record(model.HistoryEntry{KeyID: "k", Usage: &model.UsageStats{Cost: &cost}}); err != nil {
		t.Fatal(err)
	}
	// Once a positive average is known, the existing concurrent estimation
	// policy applies again while there is enough unreserved balance.
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

func TestUnpricedLimitedKeysAllowOnlyOneRunningRequest(t *testing.T) {
	for _, history := range []string{"fresh", "unknown-cost", "zero-cost", "rounded-down-average", "reset"} {
		t.Run(history, func(t *testing.T) {
			s, _ := testStore(t)
			grant := model.ProxyKeyGrant{ID: "k", SpendLimitUSD: 0.5}
			if history != "fresh" {
				entry := model.HistoryEntry{KeyID: "k"}
				if history != "unknown-cost" {
					cost := 0.0
					if history == "reset" {
						cost = 0.25
					} else if history == "rounded-down-average" {
						cost = 0.000001
					}
					entry.Usage = &model.UsageStats{Cost: &cost}
				}
				if err := s.Record(entry); err != nil {
					t.Fatal(err)
				}
				if history == "rounded-down-average" {
					if err := s.Record(model.HistoryEntry{KeyID: "k"}); err != nil {
						t.Fatal(err)
					}
				}
				if history == "reset" {
					if err := s.ResetKeyUsage("k", false); err != nil {
						t.Fatal(err)
					}
				}
			}
			first, err := s.ReserveSpend(grant)
			if err != nil {
				t.Fatal(err)
			}
			defer first.Release()
			for range 5 {
				hold, err := s.ReserveSpend(grant)
				var limit *SpendLimitError
				if hold != nil || !errors.As(err, &limit) || limit.Reason != "reserved" || limit.Running != 1 {
					t.Fatalf("unpriced key admitted another request: %v, %v", hold, err)
				}
			}
			if usage := s.KeyUsage()["k"]; usage.SpentMicroUSD != 0 && history != "rounded-down-average" {
				t.Fatalf("admission invented a charge: %+v", usage)
			}
		})
	}
}
