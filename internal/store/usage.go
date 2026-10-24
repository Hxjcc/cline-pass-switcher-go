package store

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

const UsageRetentionDays = 365

// Calendar boundaries are explicit and independent of the host/container TZ.
var usageLocation = time.FixedZone("UTC+08:00", 8*60*60)

func usageDay(t time.Time) string { return t.In(usageLocation).Format(time.DateOnly) }

func (s *Store) initializeUsageLedger(now time.Time) bool {
	if s.meta.UsageLedger != nil {
		return false
	}
	s.meta.UsageLedger = &model.UsageLedger{StartedAt: now.UnixMilli(), LatestDay: usageDay(now), Buckets: map[string]model.UsageBucket{}}
	// Older discarded history cannot be reconstructed. Import only the rows
	// still present after recovery, then persist this migration once.
	for i := len(s.meta.History) - 1; i >= 0; i-- {
		if s.recordUsage(s.meta.History[i]) {
			s.meta.UsageLedger.ImportedRequests++
		}
	}
	return true
}

func addUsageTotals(dst *model.UsageTotals, src model.UsageTotals) {
	dst.Requests += src.Requests
	dst.Errors += src.Errors
	dst.UsageRequests += src.UsageRequests
	dst.CostRequests += src.CostRequests
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens
	dst.ReasoningTokens += src.ReasoningTokens
	dst.CachedTokens += src.CachedTokens
	dst.TotalTokens += src.TotalTokens
	dst.Cost += src.Cost
}

func totalsForRecord(e model.HistoryEntry) model.UsageTotals {
	r := model.UsageTotals{Requests: 1}
	if e.Error != nil {
		r.Errors = 1
	}
	if u := e.Usage; u != nil {
		// Cost-only error responses do not report token usage. The persisted
		// zero-valued fields must not be presented as a known zero-token turn.
		if u.PromptTokens > 0 || u.CompletionTokens > 0 || u.TotalTokens > 0 || u.ReasoningTokens > 0 || u.CachedTokens > 0 {
			r.UsageRequests = 1
		}
		r.InputTokens = max(0, u.PromptTokens)
		r.OutputTokens = max(0, u.CompletionTokens)
		r.ReasoningTokens = max(0, u.ReasoningTokens)
		r.CachedTokens = max(0, u.CachedTokens)
		r.TotalTokens = u.TotalTokens
		if r.TotalTokens <= 0 {
			r.TotalTokens = r.InputTokens + r.OutputTokens
		}
		if u.Cost != nil && !math.IsNaN(*u.Cost) && !math.IsInf(*u.Cost, 0) && *u.Cost >= 0 {
			r.Cost = *u.Cost
			r.CostRequests = 1
		}
	}
	return r
}

func (s *Store) recordUsage(e model.HistoryEntry) bool {
	ledger := s.meta.UsageLedger
	if ledger == nil || e.TS <= 0 {
		return false
	}
	day := usageDay(time.UnixMilli(e.TS))
	if day > ledger.LatestDay {
		ledger.LatestDay = day
		cutoff, _ := time.ParseInLocation(time.DateOnly, day, usageLocation)
		oldest := usageDay(cutoff.AddDate(0, 0, 1-UsageRetentionDays))
		for key, bucket := range ledger.Buckets {
			if bucket.Day < oldest {
				delete(ledger.Buckets, key)
			}
		}
	}
	latest, _ := time.ParseInLocation(time.DateOnly, ledger.LatestDay, usageLocation)
	if day < usageDay(latest.AddDate(0, 0, 1-UsageRetentionDays)) {
		return false
	}
	account, accountName := e.AccountID, e.Account
	if account == "" && accountName != "" {
		for _, a := range s.config.Accounts {
			if a.Name == accountName {
				if account != "" {
					account = ""
					break
				}
				account = a.ID
			}
		}
		if account == "" {
			account = "legacy:" + accountName
		}
	}
	if account == "" {
		account = "unassigned"
	}
	if accountName == "" {
		accountName = "未标记账号"
	}
	keyID, keyName := e.KeyID, e.KeyName
	if keyID == "" {
		keyID = "unassigned"
	}
	if keyName == "" {
		keyName = "未标记密钥"
	}
	identity, _ := json.Marshal([]string{day, e.Model, account, keyID})
	key := fmt.Sprintf("%s:%x", day, sha256.Sum256(identity))
	if ledger.Buckets == nil {
		ledger.Buckets = map[string]model.UsageBucket{}
	}
	bucket := ledger.Buckets[key]
	bucket.Day, bucket.Model, bucket.Account, bucket.Key = day, e.Model, account, keyID
	bucket.AccountName, bucket.KeyName = accountName, keyName
	addUsageTotals(&bucket.UsageTotals, totalsForRecord(e))
	ledger.Buckets[key] = bucket
	return true
}

type UsageQuery struct{ From, To, Model, Account, Key string }
type UsageOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type UsageGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	model.UsageTotals
}
type UsageDay struct {
	Day string `json:"day"`
	model.UsageTotals
}
type UsageFilters struct {
	Models   []UsageOption `json:"models"`
	Accounts []UsageOption `json:"accounts"`
	Keys     []UsageOption `json:"keys"`
}
type UsageReport struct {
	From             string            `json:"from"`
	To               string            `json:"to"`
	Timezone         string            `json:"timezone"`
	RetentionDays    int               `json:"retentionDays"`
	StartedAt        int64             `json:"startedAt"`
	ImportedRequests int64             `json:"importedRequests"`
	AvailableFrom    string            `json:"availableFrom"`
	GeneratedAt      int64             `json:"generatedAt"`
	Summary          model.UsageTotals `json:"summary"`
	Trend            []UsageDay        `json:"trend"`
	Models           []UsageGroup      `json:"models"`
	Accounts         []UsageGroup      `json:"accounts"`
	Keys             []UsageGroup      `json:"keys"`
	Filters          UsageFilters      `json:"filters"`
}

func (s *Store) UsageReport(q UsageQuery) (UsageReport, error) { return s.usageReport(q, time.Now()) }

func (s *Store) usageReport(q UsageQuery, now time.Time) (UsageReport, error) {
	if q.To == "" {
		q.To = usageDay(now)
	}
	end, err := time.ParseInLocation(time.DateOnly, q.To, usageLocation)
	if err != nil {
		return UsageReport{}, fmt.Errorf("结束日期格式应为 YYYY-MM-DD")
	}
	if q.From == "" {
		q.From = usageDay(end.AddDate(0, 0, -6))
	}
	start, err := time.ParseInLocation(time.DateOnly, q.From, usageLocation)
	if err != nil {
		return UsageReport{}, fmt.Errorf("开始日期格式应为 YYYY-MM-DD")
	}
	if start.After(end) {
		return UsageReport{}, fmt.Errorf("开始日期不能晚于结束日期")
	}
	if end.Sub(start) >= UsageRetentionDays*24*time.Hour {
		return UsageReport{}, fmt.Errorf("每次最多查询 %d 天", UsageRetentionDays)
	}
	r := UsageReport{From: q.From, To: q.To, Timezone: "UTC+08:00", RetentionDays: UsageRetentionDays, GeneratedAt: now.UnixMilli(), Trend: []UsageDay{}, Models: []UsageGroup{}, Accounts: []UsageGroup{}, Keys: []UsageGroup{}}
	buckets := []model.UsageBucket{}
	accountNames, keyNames := map[string]string{}, map[string]string{}
	s.mu.RLock()
	if ledger := s.meta.UsageLedger; ledger != nil {
		r.StartedAt, r.ImportedRequests = ledger.StartedAt, ledger.ImportedRequests
		for _, bucket := range ledger.Buckets {
			buckets = append(buckets, bucket)
		}
	}
	for _, a := range s.config.Accounts {
		accountNames[a.ID] = a.Name
	}
	for _, k := range s.config.ProxyKeys {
		keyNames[k.ID] = k.Name
	}
	s.mu.RUnlock()
	// Stable traversal also gives stable floating-point sums and display names.
	slices.SortFunc(buckets, func(a, b model.UsageBucket) int {
		if c := strings.Compare(a.Day, b.Day); c != 0 {
			return c
		}
		if c := strings.Compare(a.Model, b.Model); c != 0 {
			return c
		}
		if c := strings.Compare(a.Account, b.Account); c != 0 {
			return c
		}
		return strings.Compare(a.Key, b.Key)
	})
	cutoff := usageDay(time.Date(now.In(usageLocation).Year(), now.In(usageLocation).Month(), now.In(usageLocation).Day(), 0, 0, 0, 0, usageLocation).AddDate(0, 0, 1-UsageRetentionDays))
	models, accounts, keys := map[string]string{}, map[string]string{}, map[string]string{}
	byModel, byAccount, byKey := map[string]UsageGroup{}, map[string]UsageGroup{}, map[string]UsageGroup{}
	byDay := map[string]model.UsageTotals{}
	for _, b := range buckets {
		if b.Day < cutoff {
			continue
		}
		if r.AvailableFrom == "" || b.Day < r.AvailableFrom {
			r.AvailableFrom = b.Day
		}
		accountName, keyName := b.AccountName, b.KeyName
		if name := accountNames[b.Account]; name != "" {
			accountName = name
		}
		if name := keyNames[b.Key]; name != "" {
			keyName = name
		}
		models[b.Model], accounts[b.Account], keys[b.Key] = b.Model, accountName, keyName
		if b.Day < q.From || b.Day > q.To || q.Model != "" && b.Model != q.Model || q.Account != "" && b.Account != q.Account || q.Key != "" && b.Key != q.Key {
			continue
		}
		addUsageTotals(&r.Summary, b.UsageTotals)
		totals := byDay[b.Day]
		addUsageTotals(&totals, b.UsageTotals)
		byDay[b.Day] = totals
		for _, group := range []struct {
			rows     map[string]UsageGroup
			id, name string
		}{{byModel, b.Model, b.Model}, {byAccount, b.Account, accountName}, {byKey, b.Key, keyName}} {
			row := group.rows[group.id]
			row.ID, row.Name = group.id, group.name
			addUsageTotals(&row.UsageTotals, b.UsageTotals)
			group.rows[group.id] = row
		}
	}
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		id := usageDay(day)
		r.Trend = append(r.Trend, UsageDay{Day: id, UsageTotals: byDay[id]})
	}
	r.Models, r.Accounts, r.Keys = usageGroups(byModel), usageGroups(byAccount), usageGroups(byKey)
	r.Filters = UsageFilters{Models: usageOptions(models), Accounts: usageOptions(accounts), Keys: usageOptions(keys)}
	return r, nil
}

func usageGroups(rows map[string]UsageGroup) []UsageGroup {
	result := make([]UsageGroup, 0, len(rows))
	for _, row := range rows {
		result = append(result, row)
	}
	slices.SortFunc(result, func(a, b UsageGroup) int {
		if a.TotalTokens > b.TotalTokens {
			return -1
		}
		if a.TotalTokens < b.TotalTokens {
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	return result
}
func usageOptions(rows map[string]string) []UsageOption {
	result := make([]UsageOption, 0, len(rows))
	for id, name := range rows {
		result = append(result, UsageOption{ID: id, Name: name})
	}
	slices.SortFunc(result, func(a, b UsageOption) int { return strings.Compare(a.ID, b.ID) })
	return result
}
