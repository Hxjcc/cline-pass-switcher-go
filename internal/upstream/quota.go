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
// 5_000_000_000 is $50. A probe failure only hides the readout: routing never
// consults quota.
const (
	quotaTimeout          = 30 * time.Second
	quotaProbeConcurrency = 3
	quotaCacheTTL         = 30 * time.Second
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

// AccountQuota is one account's quota snapshot. It is display-only data: the
// retry and routing logic never reads it.
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
		if cached, found := s.cachedQuota(account.ID); found {
			return cached
		}
	}
	result := s.fetchQuota(ctx, account)
	if result.OK {
		s.rememberQuota(result)
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

func (s *Service) cachedQuota(accountID string) (AccountQuota, bool) {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	cached, found := s.quotaCache[accountID]
	if !found || cached.FetchedAt == 0 || time.Since(time.UnixMilli(cached.FetchedAt)) > quotaCacheTTL {
		return AccountQuota{}, false
	}
	return cached, true
}

func (s *Service) rememberQuota(result AccountQuota) {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	if s.quotaCache == nil {
		s.quotaCache = map[string]AccountQuota{}
	}
	s.quotaCache[result.AccountID] = result
}
