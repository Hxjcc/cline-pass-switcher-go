package upstream

import (
	"crypto/sha256"
	"encoding/hex"
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
	// maxAccountAttemptsPerChannel bounds how many accounts one channel
	// attempt may try. A large pool must not turn a single client request into
	// a burst against the gateway.
	maxAccountAttemptsPerChannel = 4
)

type accountHealth struct {
	mu       sync.Mutex
	cooldown map[string]accountPenalty
}

// accountPenalty remembers when an account may be used again. The key
// fingerprint is what the credential looked like when the penalty was
// recorded: rotating the key clears the old verdict, because the failure
// belonged to the previous credential.
type accountPenalty struct {
	until   time.Time
	keyHash string
}

// excluded returns the identities of accounts that are still cooling down for
// their current credential. currentKeys maps account ID to key fingerprint.
func (h *accountHealth) excluded(now time.Time, currentKeys map[string]string) map[string]struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	result := map[string]struct{}{}
	for id, penalty := range h.cooldown {
		if keyHash, found := currentKeys[id]; !found || keyHash != penalty.keyHash || !now.Before(penalty.until) {
			delete(h.cooldown, id)
			continue
		}
		result[id] = struct{}{}
	}
	return result
}

func (h *accountHealth) penalize(id, keyHash string, until time.Time) {
	if id == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cooldown == nil {
		h.cooldown = map[string]accountPenalty{}
	}
	if current, found := h.cooldown[id]; !found || keyHash != current.keyHash || until.After(current.until) {
		h.cooldown[id] = accountPenalty{until: until, keyHash: keyHash}
	}
}

func (h *accountHealth) clear(id string) {
	if id == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.cooldown, id)
}

func accountKeyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

// excludedAccounts snapshots the cooldowns that still apply to the accounts as
// they are configured right now.
func (s *Service) excludedAccounts(now time.Time) map[string]struct{} {
	accounts := s.store.Accounts()
	current := make(map[string]string, len(accounts))
	for _, account := range accounts {
		if account.ID != "" {
			current[account.ID] = accountKeyHash(account.Key)
		}
	}
	return s.accounts.excluded(now, current)
}

func (s *Service) pickAccount() model.Account {
	return s.store.PickAccountExcluding(s.excludedAccounts(time.Now()))
}

// noteAccountStatus updates the cooldown state from the HTTP status an
// account just received.
func (s *Service) noteAccountStatus(account model.Account, status int) {
	if account.ID == "" {
		return
	}
	keyHash := accountKeyHash(account.Key)
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		s.accounts.penalize(account.ID, keyHash, time.Now().Add(accountAuthCooldown))
	case http.StatusTooManyRequests:
		s.accounts.penalize(account.ID, keyHash, time.Now().Add(accountLimitCooldown))
	case http.StatusOK:
		s.accounts.clear(account.ID)
	}
}

// AccountFailoverAvailable reports whether retrying with a different account
// is possible: the pool has more than one usable account and at least one of
// them is not cooling down. Callers use it to stop walking upstream channels
// on authentication failures, which no channel change can fix.
func (s *Service) AccountFailoverAvailable() bool {
	excluded := s.excludedAccounts(time.Now())
	total, available := 0, 0
	for _, account := range s.store.Accounts() {
		if account.Key == "" || !account.Enabled {
			continue
		}
		total++
		if _, skip := excluded[account.ID]; !skip {
			available++
		}
	}
	return total > 1 && available > 0
}

// AccountAttemptLimit is how many accounts one channel attempt may try. It is
// the number of usable accounts, capped so a large pool cannot amplify a
// single client request.
func (s *Service) AccountAttemptLimit() int {
	usable := 0
	for _, account := range s.store.Accounts() {
		if account.Key != "" && account.Enabled {
			usable++
		}
	}
	if usable > maxAccountAttemptsPerChannel {
		usable = maxAccountAttemptsPerChannel
	}
	if usable < 1 {
		usable = 1
	}
	return usable
}
