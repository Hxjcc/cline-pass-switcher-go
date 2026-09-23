package upstream

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
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
	accountAuthCooldown = 10 * time.Minute
	// A 429 is often the gateway, not one pinned provider. Keep the account
	// out of rotation longer than a single client retry, without waiting out
	// a quota window.
	accountLimitCooldown = 2 * time.Minute
	// maxAccountAttemptsPerChannel bounds how many accounts one channel
	// attempt may try. A large pool must not turn a single client request into
	// a burst against the gateway.
	maxAccountAttemptsPerChannel = 4
	// quotaRouteScanLimit bounds how many plan probes one selection may make
	// while looking for an account that still has quota. Chat attempts stay
	// on the tighter cap above.
	quotaRouteScanLimit = 8
	// quotaSelectionBudget bounds the total time one selection may spend
	// reading plan snapshots. Without it a hanging quota endpoint would cost
	// one timeout per candidate account before the model call even starts. An
	// exhausted budget only stops further probes: selection still returns an
	// account, because a plan read may never block forwarding.
	quotaSelectionBudget = 6 * time.Second
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

// excludedAccounts snapshots cooldown and quota holds that still apply to the
// accounts as they are configured right now.
func (s *Service) excludedAccounts(now time.Time) map[string]struct{} {
	accounts := s.store.Accounts()
	current := make(map[string]string, len(accounts))
	for _, account := range accounts {
		if account.ID != "" {
			current[account.ID] = accountKeyHash(account.Key)
		}
	}
	excluded := s.accounts.excluded(now, current)
	for id := range s.quotaHolds(now, current) {
		excluded[id] = struct{}{}
	}
	return excluded
}

func (s *Service) usableAccounts() []model.Account {
	accounts := s.store.Accounts()
	usable := make([]model.Account, 0, len(accounts))
	for _, account := range accounts {
		if account.Key != "" && account.Enabled {
			usable = append(usable, account)
		}
	}
	return usable
}

// pickAccount chooses the account for one upstream attempt. A plan window at
// 100% is skipped while another usable account remains, until that window
// resets. A single account, a failed probe, or a pool where every account is
// full still returns an account so the request can proceed.
func (s *Service) pickAccount(ctx context.Context) model.Account {
	if ctx == nil {
		ctx = context.Background()
	}
	// A pinned request skips stickiness, quota avoidance and round-robin: one
	// key, one account. An unavailable pin returns no account so the caller
	// answers with a configuration error instead of spending another budget.
	if accountID, pinned := AccountPinFrom(ctx); pinned {
		return s.pinnedAccount(accountID)
	}
	budget := s.quotaBudget
	if budget <= 0 {
		budget = quotaSelectionBudget
	}
	budgetCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	if account, ok := s.preferredAccount(budgetCtx); ok {
		return account
	}
	return s.pickOpenAccount(budgetCtx)
}

// pinnedAccount resolves a hard pin. Only a usable row qualifies: a deleted,
// disabled or key-less account returns the zero value, which every attempt
// path turns into a "no account" configuration error.
func (s *Service) pinnedAccount(accountID string) model.Account {
	account := s.store.FindAccount(accountID)
	if account.Key == "" || !account.Enabled {
		return model.Account{}
	}
	return account
}

// pinBlocksFailover reports whether a failure of a pinned request should end
// the chain immediately. The pin exists precisely because the holder may only
// use that account, so a rejected credential, a rate limit or a server error
// that another account could fix is still not ours to fix - the request must
// come back to the caller instead of spending somebody else's budget.
func pinBlocksFailover(ctx context.Context, status int) bool {
	if _, pinned := AccountPinFrom(ctx); !pinned {
		return false
	}
	return status >= 400
}

// preferredAccount returns the conversation's sticky account when it can still
// take a request. Quota, cooldown, and a disabled row all decline it, and the
// caller then uses the normal selection. The round-robin counter is not moved.
func (s *Service) preferredAccount(ctx context.Context) (model.Account, bool) {
	hint := stickHintFrom(ctx)
	if hint.accountID == "" {
		return model.Account{}, false
	}
	account := s.store.FindAccount(hint.accountID)
	if account.Key == "" || !account.Enabled {
		return model.Account{}, false
	}
	if _, skip := s.excludedAccounts(time.Now())[account.ID]; skip {
		return model.Account{}, false
	}
	if len(s.usableAccounts()) < 2 {
		return account, true
	}
	if s.routingDecisionFresh(account, time.Now()) {
		if _, full := s.cachedExhaustedUntil(account); full {
			return model.Account{}, false
		}
		return account, true
	}
	snapshot := s.probeQuotaForRouting(ctx, account)
	if _, full := quotaExhaustedUntil(snapshot, time.Now()); full {
		return model.Account{}, false
	}
	return account, true
}

func (s *Service) pickOpenAccount(ctx context.Context) model.Account {
	now := time.Now()
	excluded := s.excludedAccounts(now)
	usable := s.usableAccounts()
	if len(usable) < 2 {
		return s.store.PickAccountExcluding(excluded)
	}
	limit := len(usable)
	if limit > quotaRouteScanLimit {
		limit = quotaRouteScanLimit
	}
	seen := map[string]struct{}{}
	var last model.Account
	for range limit {
		account := s.store.PickAccountExcluding(excluded)
		if account.Key == "" {
			return account
		}
		if _, duplicate := seen[account.ID]; duplicate {
			return account
		}
		seen[account.ID] = struct{}{}
		last = account
		if s.routingDecisionFresh(account, time.Now()) {
			// A cached full window stays out of the pool even if its hold was
			// not in the map this iteration. The duplicate check above returns
			// the account when every candidate is already full.
			if _, full := s.cachedExhaustedUntil(account); full {
				excluded[account.ID] = struct{}{}
				continue
			}
			return account
		}
		snapshot := s.probeQuotaForRouting(ctx, account)
		if until, full := quotaExhaustedUntil(snapshot, time.Now()); full {
			excluded[account.ID] = struct{}{}
			name := account.Name
			if name == "" {
				name = account.ID
			}
			log.Printf("账号 %s 的套餐用量已满，跳过至 %s", name, until.Format(time.RFC3339))
			continue
		}
		return account
	}
	return last
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
		// A fresh "still under the cap" snapshot would send the next request
		// back to the account that just refused it. Drop the snapshot so the
		// next selection reads the plan again; an existing full-window hold
		// stays in place.
		s.invalidateQuotaSnapshot(account.ID)
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
