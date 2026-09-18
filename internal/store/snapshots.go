package store

import (
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

func (s *Store) ModelConfig(modelID string) model.PerModelConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg := s.config.PerModel[modelID]
	cfg.Upstreams = slices.Clone(cfg.Upstreams)
	cfg.Exclude = slices.Clone(cfg.Exclude)
	if cfg.Sort != nil {
		value := *cfg.Sort
		cfg.Sort = &value
	}
	return cfg
}

func (s *Store) ProxyKey() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.ProxyKey
}

func (s *Store) AccessSettings() (key, publicBase string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.ProxyKey, s.config.PublicBaseURL
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

func (s *Store) UpstreamBase() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.UpstreamBase
}
