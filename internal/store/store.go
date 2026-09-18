package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

type Store struct {
	mu         sync.RWMutex
	configPath string
	metaPath   string
	config     model.Config
	meta       model.Metadata
	rrCounter  uint64
}

func Open(dataDir string) (*Store, error) {
	if err := model.EnsureDataDir(dataDir); err != nil {
		return nil, err
	}
	configPath := filepath.Join(dataDir, "config.json")
	metaPath := filepath.Join(dataDir, "metadata.json")
	cfg, err := model.LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	meta, err := model.LoadMetadata(metaPath)
	if err != nil {
		return nil, err
	}
	store := &Store{
		configPath: configPath,
		metaPath:   metaPath,
		config:     cfg,
		meta:       meta,
	}
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		if err := store.writeConfigLocked(); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func (s *Store) Config() model.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return model.Clone(s.config)
}

func (s *Store) Metadata() model.Metadata {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return model.Clone(s.meta)
}

func (s *Store) UpdateConfig(update func(*model.Config)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	update(&s.config)
	model.NormalizeConfig(&s.config)
	return s.writeConfigLocked()
}

func (s *Store) UpdateMetadata(update func(*model.Metadata)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	update(&s.meta)
	model.NormalizeMetadata(&s.meta)
	return s.writeMetaLocked()
}

func (s *Store) UpdateModelMeta(modelID string, update func(*model.ModelMeta)) (model.ModelMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.meta.Models[modelID]
	if current.UpstreamDetail == nil {
		current.UpstreamDetail = map[string]model.UpstreamDetail{}
	}
	if current.UpstreamStatus == nil {
		current.UpstreamStatus = map[string]model.UpstreamStatus{}
	}
	update(&current)
	s.meta.Models[modelID] = current
	if err := s.writeMetaLocked(); err != nil {
		return model.ModelMeta{}, err
	}
	return model.Clone(current), nil
}

func (s *Store) Record(entry model.HistoryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// A Cline Pass model that answered successfully belongs in the
	// subscription list, whether the request was streamed or not. This also
	// resurrects a model the user removed and then used again.
	if entry.Error == nil && strings.HasPrefix(entry.Model, "cline-pass/") && !containsString(s.config.KnownModels, entry.Model) {
		s.config.KnownModels = append(s.config.KnownModels, entry.Model)
		model.NormalizeConfig(&s.config)
		if err := s.writeConfigLocked(); err != nil {
			return err
		}
	}

	current := s.meta.Models[entry.Model]
	current.LastProvider = entry.Provider
	current.LastMS = entry.MS
	if entry.Canonical != "" {
		current.CanonicalSlug = entry.Canonical
	}
	s.meta.Models[entry.Model] = current
	s.meta.History = append([]model.HistoryEntry{entry}, s.meta.History...)
	if len(s.meta.History) > 100 {
		s.meta.History = s.meta.History[:100]
	}
	if entry.Account != "" {
		stats := s.meta.Stats[entry.Account]
		stats.Requests++
		stats.LastUsed = entry.TS
		stats.LastError = entry.Error
		s.meta.Stats[entry.Account] = stats
	}
	return s.writeMetaLocked()
}

// RemoveModel drops a model from the subscription list together with its
// pin configuration and probe data, and remembers the removal so the
// official catalog sync does not bring it straight back.
func (s *Store) RemoveModel(modelID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	known := make([]string, 0, len(s.config.KnownModels))
	for _, id := range s.config.KnownModels {
		if id != modelID {
			known = append(known, id)
		}
	}
	s.config.KnownModels = known
	delete(s.config.PerModel, modelID)
	s.config.RemovedModels = append(s.config.RemovedModels, modelID)
	model.NormalizeConfig(&s.config)
	if err := s.writeConfigLocked(); err != nil {
		return err
	}
	delete(s.meta.Models, modelID)
	return s.writeMetaLocked()
}

// ClearHistory forgets the request log; per-account counters are kept.
func (s *Store) ClearHistory() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.meta.History = []model.HistoryEntry{}
	return s.writeMetaLocked()
}

func (s *Store) PickAccount() model.Account {
	return s.PickAccountExcluding(nil)
}

// PickAccountExcluding applies the configured selection mode while skipping
// the named accounts (typically ones cooling down after 401/403/429). When
// every usable account is excluded the exclusion is ignored rather than
// returning nothing, so a single-account setup keeps working.
func (s *Store) PickAccountExcluding(excluded map[string]struct{}) model.Account {
	s.mu.Lock()
	defer s.mu.Unlock()

	usable := func(account model.Account) bool {
		return account.Key != "" && account.Enabled
	}
	allowed := func(account model.Account) bool {
		_, skip := excluded[account.Name]
		return usable(account) && !skip
	}
	enabled := make([]model.Account, 0, len(s.config.Accounts))
	for _, account := range s.config.Accounts {
		if allowed(account) {
			enabled = append(enabled, account)
		}
	}
	if len(enabled) == 0 {
		allowed = usable
		for _, account := range s.config.Accounts {
			if usable(account) {
				enabled = append(enabled, account)
			}
		}
	}
	if len(enabled) == 0 {
		return model.Account{Name: "默认", Key: s.config.APIKey, Enabled: true}
	}
	if s.config.AccountMode == "roundrobin" && len(enabled) > 1 {
		account := enabled[s.rrCounter%uint64(len(enabled))]
		s.rrCounter = (s.rrCounter + 1) % 1_000_000_000
		return account
	}
	if s.config.ActiveAccount >= 0 && s.config.ActiveAccount < len(s.config.Accounts) {
		account := s.config.Accounts[s.config.ActiveAccount]
		if allowed(account) {
			return account
		}
	}
	return enabled[0]
}

func (s *Store) IsConfigured() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config.APIKey != "" {
		return true
	}
	for _, account := range s.config.Accounts {
		if account.Key != "" && account.Enabled {
			return true
		}
	}
	return false
}

func (s *Store) ResetRoundRobin() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rrCounter = 0
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (s *Store) writeConfigLocked() error {
	return writeJSON(s.configPath, s.config)
}

func (s *Store) writeMetaLocked() error {
	return writeJSON(s.metaPath, s.meta)
}

func writeJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}
