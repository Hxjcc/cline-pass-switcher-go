package store

import (
	"crypto/subtle"
	"maps"
	"slices"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

// ModelMeta copies only one model, including its mutable maps and slices.
// Request paths do not need to serialize the catalog, history and account stats.
func (s *Store) ModelMeta(modelID string) model.ModelMeta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	meta := s.meta.Models[modelID]
	meta.ReasoningEfforts = slices.Clone(meta.ReasoningEfforts)
	meta.InputModalities = slices.Clone(meta.InputModalities)
	meta.OutputModalities = slices.Clone(meta.OutputModalities)
	meta.AvailableProviders = slices.Clone(meta.AvailableProviders)
	meta.Upstreams = slices.Clone(meta.Upstreams)
	meta.Tier0 = slices.Clone(meta.Tier0)
	meta.UpstreamDetail = maps.Clone(meta.UpstreamDetail)
	meta.UpstreamStatus = maps.Clone(meta.UpstreamStatus)
	return meta
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
// picks the level closest to high).
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
