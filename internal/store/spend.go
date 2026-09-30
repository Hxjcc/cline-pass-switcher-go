package store

import (
	"errors"
	"sync/atomic"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

var ErrAccountingUnavailable = errors.New("request accounting is unavailable")

// SpendLimitError carries the ledger snapshot used for a refused admission.
// Formatting the public error is the HTTP layer's responsibility.
type SpendLimitError struct {
	Reason           string
	LimitUSD         float64
	Usage            model.KeyUsage
	Running          int
	ExpectedMicroUSD int64
}

func (err *SpendLimitError) Error() string { return "key spend limit: " + err.Reason }

// SpendReservation stays live until both the handler and any shared upstream
// run have released it. A runner records its cost before releasing its slot.
type SpendReservation struct {
	store *Store
	keyID string
	refs  atomic.Int32
}

func (r *SpendReservation) Retain() {
	if r != nil {
		r.refs.Add(1)
	}
}

func (r *SpendReservation) Release() {
	if r == nil || r.refs.Add(-1) != 0 {
		return
	}
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	r.store.spendRunning[r.keyID]--
	if r.store.spendRunning[r.keyID] == 0 {
		delete(r.store.spendRunning, r.keyID)
	}
}

// ReserveSpend reads the current ledger and reserves admission under the same
// lock that commits charges and usage resets. No caller-supplied usage snapshot
// can outlive a completed request and let a spent-out key through.
func (s *Store) ReserveSpend(grant model.ProxyKeyGrant) (*SpendReservation, error) {
	if grant.SpendLimitUSD <= 0 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.healthLocked().Status == "unavailable" {
		return nil, ErrAccountingUnavailable
	}
	usage := s.meta.KeyUsage[grant.ID]
	running := s.spendRunning[grant.ID]
	refused := &SpendLimitError{LimitUSD: grant.SpendLimitUSD, Usage: usage, Running: running}
	if usage.SpentUSD() >= grant.SpendLimitUSD {
		refused.Reason = "exceeded"
		return nil, refused
	}
	if running > 0 && usage.Requests > 0 {
		refused.ExpectedMicroUSD = usage.SpentMicroUSD / usage.Requests * int64(running)
		if float64(usage.SpentMicroUSD+refused.ExpectedMicroUSD)/1e6 >= grant.SpendLimitUSD {
			refused.Reason = "reserved"
			return nil, refused
		}
	}
	if s.spendRunning == nil {
		s.spendRunning = make(map[string]int)
	}
	s.spendRunning[grant.ID] = running + 1
	reservation := &SpendReservation{store: s, keyID: grant.ID}
	reservation.refs.Store(1)
	return reservation, nil
}
