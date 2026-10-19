package store

import (
	"crypto/subtle"
	"maps"
	"slices"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

// ModelMeta copies only one model, including its mutable maps and slices.
// Request paths do not need to serialize the catalog, history and account stats.
func (s *Store) ModelMeta(modelID string) model.ModelMeta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneModelMeta(s.meta.Models[modelID])
}

// ModelsMeta returns the probed model catalog without the request history.
// Console model pages and routing probes only need this map; cloning the whole
// metadata document made every such read pay for 500 history rows.
func (s *Store) ModelsMeta() map[string]model.ModelMeta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	models := make(map[string]model.ModelMeta, len(s.meta.Models))
	for id, meta := range s.meta.Models {
		models[id] = cloneModelMeta(meta)
	}
	return models
}

// History returns a detached copy of the newest-first request log.
func (s *Store) History() []model.HistoryEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	history := make([]model.HistoryEntry, len(s.meta.History))
	for index, entry := range s.meta.History {
		history[index] = cloneHistoryEntry(entry)
	}
	return history
}

// AccountStats returns only the per-account counters the account panel needs.
func (s *Store) AccountStats() map[string]model.AccountStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	stats := make(map[string]model.AccountStats, len(s.meta.Stats))
	for id, value := range s.meta.Stats {
		if value.LastError != nil {
			message := *value.LastError
			value.LastError = &message
		}
		stats[id] = value
	}
	return stats
}

// OfficialModelsFetch returns the last catalog sync summary, if any.
func (s *Store) OfficialModelsFetch() *model.OfficialFetch {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.meta.OfficialModelsFetch == nil {
		return nil
	}
	fetch := *s.meta.OfficialModelsFetch
	fetch.Sources = slices.Clone(fetch.Sources)
	fetch.Added = slices.Clone(fetch.Added)
	return &fetch
}

// ORModelList returns the cached OpenRouter catalog and its fetch time.
func (s *Store) ORModelList() ([]string, int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.meta.ORModels), s.meta.ORModelsFetchedAt
}

// Accounts returns a detached copy of the account pool. Accounts only carry
// value fields, so this is cheaper than cloning the whole configuration on
// every upstream attempt.
func (s *Store) Accounts() []model.Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.config.Accounts)
}

func (s *Store) ModelConfig(modelID string) model.PerModelConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg := s.config.PerModel[modelID]
	cfg.Upstreams = slices.Clone(cfg.Upstreams)
	cfg.Exclude = slices.Clone(cfg.Exclude)
	return cfg
}

// PerModelConfigs returns the routing preferences without accounts, keys or
// the rest of config.json.
func (s *Store) PerModelConfigs() map[string]model.PerModelConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	configs := make(map[string]model.PerModelConfig, len(s.config.PerModel))
	for id, value := range s.config.PerModel {
		value.Upstreams = slices.Clone(value.Upstreams)
		value.Exclude = slices.Clone(value.Exclude)
		configs[id] = value
	}
	return configs
}

func (s *Store) KnownModels() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.config.KnownModels)
}

func (s *Store) Port() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.Port
}

func (s *Store) AccountMode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.AccountMode
}

func (s *Store) ActiveAccount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.ActiveAccount
}

func (s *Store) PublicBaseURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.PublicBaseURL
}

// StickTTL is read on every successful conversation turn; keep it out of the
// full configuration clone.
func (s *Store) StickTTL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.StickTTL
}

func (s *Store) MaxConcurrentRequests() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.MaxConcurrentRequests
}

func (s *Store) MaxConcurrentRequestsPerKey() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.MaxConcurrentRequestsPerKey
}

func (s *Store) ProxyKey() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.ProxyKey
}

// AdminKey is the console key. An empty value means the console keeps working
// with the proxy key, which is how single-machine deployments have always
// behaved.
func (s *Store) AdminKey() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config.AdminKey != "" {
		return s.config.AdminKey
	}
	return s.config.ProxyKey
}

// ProxyKeys returns a detached copy of the issued client keys.
func (s *Store) ProxyKeys() []model.ProxyKeyGrant {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.config.ProxyKeys)
}

// FindProxyKey looks up an issued key. Disabled grants are still reported so the
// caller can answer "this key was revoked" instead of "unknown key".
func (s *Store) FindProxyKey(candidate string) (model.ProxyKeyGrant, bool) {
	if candidate == "" {
		return model.ProxyKeyGrant{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, grant := range s.config.ProxyKeys {
		if subtle.ConstantTimeCompare([]byte(grant.Key), []byte(candidate)) == 1 {
			return grant, true
		}
	}
	return model.ProxyKeyGrant{}, false
}

// AuthEnabled reports whether any credential is configured. While it is false
// the console relies on the network boundary exactly as before.
func (s *Store) AuthEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config.ProxyKey != "" || s.config.AdminKey != "" {
		return true
	}
	for _, grant := range s.config.ProxyKeys {
		if grant.Key != "" {
			return true
		}
	}
	return false
}

// KeyUsage reports the durable accounting of every issued key.
func (s *Store) KeyUsage() map[string]model.KeyUsage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return maps.Clone(s.meta.KeyUsage)
}

// AccessPolicy is the immutable snapshot the request guard evaluates: the
// proxy key, the optional public console URL, and the explicit trust the
// operator declared for unauthenticated local access.
type AccessPolicy struct {
	ProxyKey              string
	AdminKey              string
	AuthEnabled           bool
	PublicBaseURL         string
	TrustedProxies        []string
	TrustLocalPortForward bool
}

type ModelsSnapshot struct {
	KnownModels []string
	PerModel    map[string]model.PerModelConfig
	Models      map[string]model.ModelMeta
	Official    *model.OfficialFetch
	PublicBase  string
	Port        int
}

// ModelsSnapshot keeps the four parts of the model page on one store lock, so
// a concurrent admin save cannot produce a subscription/config mismatch.
func (s *Store) ModelsSnapshot() ModelsSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot := ModelsSnapshot{
		KnownModels: slices.Clone(s.config.KnownModels),
		PerModel:    make(map[string]model.PerModelConfig, len(s.config.PerModel)),
		Models:      make(map[string]model.ModelMeta, len(s.meta.Models)),
		PublicBase:  s.config.PublicBaseURL,
		Port:        s.config.Port,
	}
	for id, value := range s.config.PerModel {
		value.Upstreams = slices.Clone(value.Upstreams)
		value.Exclude = slices.Clone(value.Exclude)
		snapshot.PerModel[id] = value
	}
	for id, value := range s.meta.Models {
		snapshot.Models[id] = cloneModelMeta(value)
	}
	if s.meta.OfficialModelsFetch != nil {
		fetch := *s.meta.OfficialModelsFetch
		fetch.Sources = slices.Clone(fetch.Sources)
		fetch.Added = slices.Clone(fetch.Added)
		snapshot.Official = &fetch
	}
	return snapshot
}

type AccountsSnapshot struct {
	Accounts []model.Account
	Mode     string
	Active   int
	Stats    map[string]model.AccountStats
}

func (s *Store) AccountsSnapshot() AccountsSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot := AccountsSnapshot{
		Accounts: slices.Clone(s.config.Accounts),
		Mode:     s.config.AccountMode,
		Active:   s.config.ActiveAccount,
		Stats:    make(map[string]model.AccountStats, len(s.meta.Stats)),
	}
	for id, value := range s.meta.Stats {
		if value.LastError != nil {
			message := *value.LastError
			value.LastError = &message
		}
		snapshot.Stats[id] = value
	}
	return snapshot
}

func (s *Store) AccessPolicy() AccessPolicy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	admin := s.config.AdminKey
	if admin == "" {
		admin = s.config.ProxyKey
	}
	authenticated := s.config.ProxyKey != "" || admin != ""
	if !authenticated {
		for _, grant := range s.config.ProxyKeys {
			if grant.Key != "" {
				authenticated = true
				break
			}
		}
	}
	return AccessPolicy{
		ProxyKey:              s.config.ProxyKey,
		AdminKey:              admin,
		AuthEnabled:           authenticated,
		PublicBaseURL:         s.config.PublicBaseURL,
		TrustedProxies:        slices.Clone(s.config.TrustedProxies),
		TrustLocalPortForward: s.config.TrustLocalPortForward,
	}
}

func (s *Store) StrictToolHistory() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.StrictToolHistory
}

func (s *Store) WebSearchUpstream() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.WebSearchUpstream
}

func (s *Store) WebFetchUpstream() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.WebFetchUpstream
}

// ShellCompat returns the shell forced into forwarded tool schemas. Empty
// leaves the client's schemas untouched.
func (s *Store) ShellCompat() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.ShellCompat
}

func (s *Store) ShellCompatEnforce() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.ShellCompatEnforce
}

// CompactionRecentTokens is the verbatim tail budget embedded in compaction
// items. Zero disables the tail.
func (s *Store) CompactionRecentTokens() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.CompactionRecentTokens
}

// CompactionReasoningEffort is the reasoning level compaction runs at ("auto"
// picks the strongest advertised level).
func (s *Store) CompactionReasoningEffort() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.CompactionReasoningEffort
}

// CompactionMinOutputTokens is the output floor for compaction turns.
func (s *Store) CompactionMinOutputTokens() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.CompactionMinOutputTokens
}

func (s *Store) UpstreamBase() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.UpstreamBase
}
