package upstream

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/strx"
)

// Quota probing reads the plan utilization the Cline dashboard itself uses.
// The endpoints are not part of the public API docs:
//
//	GET /users/me/plan              plan + inferenceCapThreshold caps
//	GET /users/me/plan/usage-limits five_hour / weekly / monthly utilization
//
// Both are read-only GETs authenticated with the account key. Integer money
// values are 1e-8 USD units (a generation whose gateway cost was $0.000006 is
// recorded as costUsd=600, and a $0.00001785 request as 1785), so a cap of
// 5_000_000_000 is $50. A window at 100% keeps that account out of selection
// until it resets, when another usable account exists. A probe failure does
// not, so a missing readout never blocks forwarding.
const (
	quotaTimeout          = 30 * time.Second
	quotaProbeConcurrency = 3
	quotaCacheTTL         = 30 * time.Second
	// quotaRoutingTTL is how long a failed probe, or a snapshot that is still
	// under every cap, may be reused on the request path.
	quotaRoutingTTL = 60 * time.Second
	// quotaRouteTimeout bounds a plan read that sits in front of a model
	// call. The console probe keeps the longer quotaTimeout.
	quotaRouteTimeout = 4 * time.Second
	// quotaExhaustedFallback applies when a full window has no usable reset
	// time, so the account is retried instead of being skipped indefinitely.
	quotaExhaustedFallback = 5 * time.Minute
	// quotaExhaustedMax stops a distant or bogus resetsAt from pinning an
	// account out of the pool.
	quotaExhaustedMax = 32 * 24 * time.Hour
)

// QuotaLimit is one utilization window of the plan.
type QuotaLimit struct {
	Type        string `json:"type"`
	PercentUsed int    `json:"percentUsed"`
	ResetsAt    string `json:"resetsAt,omitempty"`
}

// QuotaCaps are the raw inferenceCapThreshold values (1e-8 USD units).
type QuotaCaps struct {
	FiveHour int64 `json:"fiveHour"`
	Weekly   int64 `json:"weekly"`
	Monthly  int64 `json:"monthly"`
}

// AccountQuota is one account's quota snapshot. Selection treats a window at
// 100% as exhausted; anything short of that, including a failed probe, leaves
// the account eligible.
type AccountQuota struct {
	Account   string `json:"account,omitempty"`
	AccountID string `json:"accountId,omitempty"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	Plan      string `json:"plan,omitempty"`
	Active    bool   `json:"active,omitempty"`
	// CurrentPeriodEnd comes from the subscription, never a usage reset.
	CurrentPeriodEnd string       `json:"currentPeriodEnd,omitempty"`
	Caps             *QuotaCaps   `json:"caps,omitempty"`
	Limits           []QuotaLimit `json:"limits,omitempty"`
	FetchedAt        int64        `json:"fetchedAt"`
}

// ProbeQuota reads one account's plan and utilization. Successful probes are
// cached for a short window so the console cannot hammer the upstream; pass
// refresh to bypass the cache (the manual refresh button does).
func (s *Service) ProbeQuota(ctx context.Context, accountID string, refresh bool) AccountQuota {
	account := s.store.FindAccount(strings.TrimSpace(accountID))
	if account.Key == "" {
		return AccountQuota{
			AccountID: account.ID,
			Error:     errNoAccount.Error(),
			FetchedAt: time.Now().UnixMilli(),
		}
	}
	if !refresh {
		if cached, found := s.cachedQuota(account.ID, accountKeyHash(account.Key)); found {
			return cached
		}
	}
	result := s.fetchQuota(ctx, account)
	if result.OK {
		s.noteQuota(account, result)
	}
	return result
}

// ProbeQuotas probes the given account ids, or every account that has a
// credential when the list is empty. Enablement is a routing concern: the
// console draws a meter for disabled rows too, and these endpoints are
// read-only, so a switched-off account still reports its remaining quota.
// Results keep the account order.
func (s *Service) ProbeQuotas(ctx context.Context, accountIDs []string, refresh bool) []AccountQuota {
	accounts := make([]model.Account, 0, 4)
	if len(accountIDs) == 0 {
		for _, account := range s.store.Accounts() {
			if account.Key != "" {
				accounts = append(accounts, account)
			}
		}
	} else {
		for _, id := range accountIDs {
			if account := s.store.FindAccount(strings.TrimSpace(id)); account.Key != "" {
				accounts = append(accounts, account)
			}
		}
	}
	results := make([]AccountQuota, len(accounts))
	semaphore := make(chan struct{}, quotaProbeConcurrency)
	var waitGroup sync.WaitGroup
	for index, account := range accounts {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results[index] = AccountQuota{
					Account: account.Name, AccountID: account.ID,
					Error: "探测已取消", FetchedAt: time.Now().UnixMilli(),
				}
				return
			}
			results[index] = s.ProbeQuota(ctx, account.ID, refresh)
		}()
	}
	waitGroup.Wait()
	return results
}

func (s *Service) fetchQuota(ctx context.Context, account model.Account) AccountQuota {
	result := AccountQuota{
		Account:   account.Name,
		AccountID: account.ID,
		FetchedAt: time.Now().UnixMilli(),
	}
	base := s.store.UpstreamBase()
	status, raw, err := s.fetchJSON(ctx, http.MethodGet, base+"/users/me/plan", chatHeaders(account.Key), nil, quotaTimeout)
	if err != nil {
		result.Error = "读取套餐失败：" + err.Error()
		return result
	}
	root := jsonx.Map(raw)
	if status != http.StatusOK {
		result.Error = quotaHTTPError(status, root)
		return result
	}
	data := getMap(root, "data")
	plan := getMap(data, "plan")
	if plan == nil {
		result.Error = "上游没有返回套餐信息"
		return result
	}
	result.Plan = strings.TrimSpace(firstQuotaString(getString(plan, "displayName"), getString(plan, "name")))
	if result.Plan == "" {
		result.Plan = "未知套餐"
	}
	result.Active = getBool(plan, "isActive")
	result.CurrentPeriodEnd = strings.TrimSpace(getString(data, "currentPeriodEnd"))
	result.Caps = quotaCaps(plan)
	// Utilization is best-effort: the caps alone are still worth showing.
	result.Limits = s.fetchQuotaLimits(ctx, base, account.Key)
	result.OK = true
	return result
}

func (s *Service) fetchQuotaLimits(ctx context.Context, base, key string) []QuotaLimit {
	status, raw, err := s.fetchJSON(ctx, http.MethodGet, base+"/users/me/plan/usage-limits", chatHeaders(key), nil, quotaTimeout)
	if err != nil || status != http.StatusOK {
		return nil
	}
	items := getSlice(getMap(jsonx.Map(raw), "data"), "limits")
	limits := make([]QuotaLimit, 0, len(items))
	for _, item := range items {
		entry := jsonx.Map(item)
		limitType := strings.TrimSpace(getString(entry, "type"))
		if limitType == "" {
			continue
		}
		limits = append(limits, QuotaLimit{
			Type:        limitType,
			PercentUsed: formatInt(entry["percentUsed"]),
			ResetsAt:    strings.TrimSpace(getString(entry, "resetsAt")),
		})
	}
	if len(limits) == 0 {
		return nil
	}
	return limits
}

func quotaCaps(plan map[string]any) *QuotaCaps {
	entitlements := getMap(plan, "entitlements")
	if entitlements == nil {
		return nil
	}
	pass := getMap(entitlements, "cline_pass")
	if pass == nil {
		pass = getMap(entitlements, "clinePass")
	}
	threshold := getMap(pass, "inferenceCapThreshold")
	if threshold == nil {
		return nil
	}
	caps := &QuotaCaps{
		FiveHour: formatInt64(threshold["last5HoursUsageCostUSDPerUser"]),
		Weekly:   formatInt64(threshold["last7daysUsageCostUSDPerUser"]),
		Monthly:  formatInt64(threshold["last30daysUsageCostUSDPerUser"]),
	}
	if caps.FiveHour == 0 && caps.Weekly == 0 && caps.Monthly == 0 {
		return nil
	}
	return caps
}

func quotaHTTPError(status int, root map[string]any) string {
	message := strings.TrimSpace(extractError(root))
	if message == "" {
		message = strings.TrimSpace(jsonx.String(root["message"]))
	}
	if message == "" {
		message = http.StatusText(status)
	}
	if message == "" {
		message = "上游返回错误"
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return "密钥无效或未授权：" + strx.Truncate(message, 160)
	}
	return fmt.Sprintf("上游返回 %d：%s", status, strx.Truncate(message, 160))
}

func firstQuotaString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// quotaState is the last plan read for one account. A failure is stored too,
// so selection can back off without treating the account as full.
type quotaState struct {
	quota     AccountQuota
	keyHash   string
	ok        bool
	checkedAt time.Time
}

// quotaHold keeps an account out of selection until a full window resets.
// The key fingerprint drops the hold when the credential changes.
type quotaHold struct {
	until   time.Time
	keyHash string
}

type quotaFlight struct {
	done   chan struct{}
	result AccountQuota
}

func (s *Service) cachedQuota(accountID, keyHash string) (AccountQuota, bool) {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	state, found := s.quotaState[accountID]
	if !found || !state.ok || state.keyHash != keyHash || state.checkedAt.IsZero() || time.Since(state.checkedAt) > quotaCacheTTL {
		return AccountQuota{}, false
	}
	return state.quota, true
}

// noteQuota records a plan read and, on success, installs or clears the
// selection hold. A recent successful snapshot is kept when a later read
// fails, so a blip cannot forget that a window is full.
func (s *Service) noteQuota(account model.Account, result AccountQuota) {
	if account.ID == "" {
		return
	}
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	if s.quotaState == nil {
		s.quotaState = map[string]quotaState{}
	}
	keyHash := accountKeyHash(account.Key)
	now := time.Now()
	if !result.OK {
		if current, found := s.quotaState[account.ID]; found && current.ok && current.keyHash == keyHash && now.Sub(current.checkedAt) < quotaCacheTTL {
			return
		}
		s.quotaState[account.ID] = quotaState{keyHash: keyHash, checkedAt: now}
		return
	}
	s.quotaState[account.ID] = quotaState{quota: result, keyHash: keyHash, ok: true, checkedAt: now}
	if s.quotaHold == nil {
		s.quotaHold = map[string]quotaHold{}
	}
	if until, full := quotaExhaustedUntil(result, now); full {
		s.quotaHold[account.ID] = quotaHold{until: until, keyHash: keyHash}
		return
	}
	delete(s.quotaHold, account.ID)
}

func (s *Service) invalidateQuotaSnapshot(id string) {
	if id == "" {
		return
	}
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	delete(s.quotaState, id)
}

// quotaHolds returns accounts whose current credential is still over a cap.
func (s *Service) quotaHolds(now time.Time, currentKeys map[string]string) map[string]struct{} {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	result := map[string]struct{}{}
	for id, hold := range s.quotaHold {
		keyHash, found := currentKeys[id]
		if !found || keyHash != hold.keyHash || !now.Before(hold.until) {
			delete(s.quotaHold, id)
			continue
		}
		result[id] = struct{}{}
	}
	return result
}

// cachedExhaustedUntil reports a still-active full window from the last
// successful read. A reset time that has already passed is not reused.
func (s *Service) cachedExhaustedUntil(account model.Account) (time.Time, bool) {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	state, found := s.quotaState[account.ID]
	if !found || !state.ok || state.keyHash != accountKeyHash(account.Key) {
		return time.Time{}, false
	}
	until, full := quotaExhaustedUntil(state.quota, state.checkedAt)
	if !full || !time.Now().Before(until) {
		return time.Time{}, false
	}
	return until, true
}

// routingDecisionFresh reports whether selection can trust the last plan read
// for this credential. A full window stays fresh until its hold elapses; a
// reading under the cap, and a failed read, stay fresh for quotaRoutingTTL.
func (s *Service) routingDecisionFresh(account model.Account, now time.Time) bool {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	state, found := s.quotaState[account.ID]
	if !found || state.keyHash != accountKeyHash(account.Key) {
		return false
	}
	if !state.ok {
		return now.Sub(state.checkedAt) < quotaRoutingTTL
	}
	if until, full := quotaExhaustedUntil(state.quota, state.checkedAt); full {
		return now.Before(until)
	}
	return now.Sub(state.checkedAt) < quotaRoutingTTL
}

// probeQuotaForRouting reads one account's plan for selection. Concurrent
// callers share one upstream read. Cancelling the parent request does not
// poison the cache.
func (s *Service) probeQuotaForRouting(ctx context.Context, account model.Account) AccountQuota {
	if ctx == nil {
		ctx = context.Background()
	}
	s.quotaMu.Lock()
	if s.quotaFlight == nil {
		s.quotaFlight = map[string]*quotaFlight{}
	}
	if flight, found := s.quotaFlight[account.ID]; found {
		s.quotaMu.Unlock()
		select {
		case <-flight.done:
			return flight.result
		case <-ctx.Done():
			return AccountQuota{AccountID: account.ID, Error: ctx.Err().Error()}
		}
	}
	flight := &quotaFlight{done: make(chan struct{})}
	s.quotaFlight[account.ID] = flight
	s.quotaMu.Unlock()

	routeCtx, cancel := context.WithTimeout(ctx, quotaRouteTimeout)
	result := s.fetchQuota(routeCtx, account)
	cancel()
	if ctx.Err() == nil {
		s.noteQuota(account, result)
	}
	flight.result = result
	close(flight.done)

	s.quotaMu.Lock()
	delete(s.quotaFlight, account.ID)
	s.quotaMu.Unlock()
	return result
}

// quotaExhaustedUntil reports when an account at 100% on any window may be
// selected again. Several full windows wait for the latest reset. A missing
// or already-passed reset waits quotaExhaustedFallback instead.
func quotaExhaustedUntil(quota AccountQuota, now time.Time) (time.Time, bool) {
	if !quota.OK {
		return time.Time{}, false
	}
	var until time.Time
	found := false
	for _, limit := range quota.Limits {
		if limit.PercentUsed < 100 {
			continue
		}
		reset := now.Add(quotaExhaustedFallback)
		if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(limit.ResetsAt)); err == nil && parsed.After(now) {
			reset = parsed
		}
		if reset.After(now.Add(quotaExhaustedMax)) {
			reset = now.Add(quotaExhaustedMax)
		}
		if !found || reset.After(until) {
			until = reset
			found = true
		}
	}
	return until, found
}
