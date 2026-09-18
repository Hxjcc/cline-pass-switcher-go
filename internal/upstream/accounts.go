package upstream

import (
	"net/http"
	"sync"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

// Account failover. An upstream 401/403 means the key is rejected and a 429
// means the key is throttled; both are properties of the account rather than
// of the provider channel, so the account is put on cooldown and the next
// attempt picks another one when the pool has more than one. The state is
// in-memory only: a restart gives every account a fresh chance.
const (
	accountAuthCooldown  = 10 * time.Minute
	accountLimitCooldown = 30 * time.Second
)

type accountHealth struct {
	mu       sync.Mutex
	cooldown map[string]time.Time
}

func (h *accountHealth) excluded(now time.Time) map[string]struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	result := map[string]struct{}{}
	for name, until := range h.cooldown {
		if now.Before(until) {
			result[name] = struct{}{}
		} else {
			delete(h.cooldown, name)
		}
	}
	return result
}

func (h *accountHealth) penalize(name string, until time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cooldown == nil {
		h.cooldown = map[string]time.Time{}
	}
	if current, found := h.cooldown[name]; !found || until.After(current) {
		h.cooldown[name] = until
	}
}

func (h *accountHealth) clear(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.cooldown, name)
}

func (s *Service) pickAccount() model.Account {
	return s.store.PickAccountExcluding(s.accounts.excluded(time.Now()))
}

// noteAccountStatus updates the cooldown state from the HTTP status an
// account just received.
func (s *Service) noteAccountStatus(account model.Account, status int) {
	if account.Name == "" {
		return
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		s.accounts.penalize(account.Name, time.Now().Add(accountAuthCooldown))
	case http.StatusTooManyRequests:
		s.accounts.penalize(account.Name, time.Now().Add(accountLimitCooldown))
	case http.StatusOK:
		s.accounts.clear(account.Name)
	}
}

// AccountFailoverAvailable reports whether retrying with a different account
// is possible: the pool has more than one usable account and at least one of
// them is not cooling down. Callers use it to stop walking upstream channels
// on authentication failures, which no channel change can fix.
func (s *Service) AccountFailoverAvailable() bool {
	excluded := s.accounts.excluded(time.Now())
	total, available := 0, 0
	for _, account := range s.store.Config().Accounts {
		if account.Key == "" || !account.Enabled {
			continue
		}
		total++
		if _, skip := excluded[account.Name]; !skip {
			available++
		}
	}
	return total > 1 && available > 0
}
