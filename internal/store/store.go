package store

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

type Store struct {
	mu          sync.RWMutex
	configPath  string
	metaPath    string
	config      model.Config
	meta        model.Metadata
	rrCounter   uint64
	journalPath string
	lock        *os.File
	// journal stays open between commits; unsynced counts request records that
	// are in the page cache but not yet fsynced.
	journal      *os.File
	unsynced     int
	journalSyncs uint64
	pending      int
	closed       bool
	writeErr     error
}

func Open(dataDir string) (*Store, error) {
	if err := model.EnsureDataDir(dataDir); err != nil {
		return nil, err
	}
	lock, err := lockDirectory(dataDir)
	if err != nil {
		return nil, err
	}
	opened := false
	defer func() {
		if !opened {
			_ = lock.Close()
		}
	}()
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
		configPath:  configPath,
		metaPath:    metaPath,
		config:      cfg,
		meta:        meta,
		journalPath: filepath.Join(dataDir, "store.journal"),
		lock:        lock,
	}
	if err := store.recoverJournal(); err != nil {
		return nil, err
	}
	model.ApplyEnvironment(&store.config)
	model.NormalizeConfig(&store.config)
	if migrateStatsToIDs(store.config, &store.meta) {
		// The rewrite has to be part of the durable state before any new
		// journal record is written. Otherwise a crash can leave a snapshot
		// that still counts under the old name while the journal counts under
		// the identity, and recovery would only see one of the two halves.
		// Replaying this migration is safe: it merges equal counters.
		if err := store.UpdateMetadata(func(*model.Metadata) {}); err != nil {
			return nil, err
		}
	}
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		if err := store.writeConfigLocked(); err != nil {
			return nil, err
		}
	}
	opened = true
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
	next := model.Clone(s.config)
	update(&next)
	model.NormalizeConfig(&next)
	return s.commitLocked(journalEntry{Kind: "config", Config: &next})
}

func (s *Store) UpdateMetadata(update func(*model.Metadata)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := model.Clone(s.meta)
	update(&next)
	model.NormalizeMetadata(&next)
	return s.commitLocked(journalEntry{Kind: "metadata", Metadata: &next})
}

func (s *Store) UpdateModelMeta(modelID string, update func(*model.ModelMeta)) (model.ModelMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := model.Clone(s.meta.Models[modelID])
	if current.UpstreamDetail == nil {
		current.UpstreamDetail = map[string]model.UpstreamDetail{}
	}
	if current.UpstreamStatus == nil {
		current.UpstreamStatus = map[string]model.UpstreamStatus{}
	}
	update(&current)
	if err := s.commitLocked(journalEntry{Kind: "model", ModelID: modelID, ModelMeta: &current}); err != nil {
		return model.ModelMeta{}, err
	}
	return model.Clone(current), nil
}

func (s *Store) Record(entry model.HistoryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.commitLocked(journalEntry{Kind: "record", Record: &entry})
}

// RemoveModel drops a model from the subscription list together with its
// pin configuration and probe data, and remembers the removal so the
// official catalog sync does not bring it straight back.
func (s *Store) RemoveModel(modelID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.commitLocked(journalEntry{Kind: "remove", ModelID: modelID})
}

// ClearHistory forgets the request log; per-account counters are kept.
func (s *Store) ClearHistory() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commitLocked(journalEntry{Kind: "clear"})
}

func (s *Store) PickAccount() model.Account {
	return s.PickAccountExcluding(nil)
}

// PickAccountExcluding applies the configured selection mode while skipping
// the given account IDs (typically ones cooling down after 401/403/429). When
// every usable account is excluded the exclusion is ignored rather than
// returning nothing, so a single-account setup keeps working. Callers that
// need at least one account must check the returned Key: an empty account
// means nothing is configured, and the legacy top-level apiKey is never a
// runtime fallback.
func (s *Store) PickAccountExcluding(excluded map[string]struct{}) model.Account {
	s.mu.Lock()
	defer s.mu.Unlock()

	usable := func(account model.Account) bool {
		return account.Key != "" && account.Enabled
	}
	allowed := func(account model.Account) bool {
		_, skip := excluded[account.ID]
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
		return model.Account{}
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

// FindAccount returns one configured account by its stable identity.
func (s *Store) FindAccount(id string) model.Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, account := range s.config.Accounts {
		if id != "" && account.ID == id {
			return account
		}
	}
	return model.Account{}
}

func (s *Store) IsConfigured() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, account := range s.config.Accounts {
		if account.Key != "" && account.Enabled {
			return true
		}
	}
	return false
}

// migrateStatsToIDs rekeys counters written before per-account identities
// existed, which used the (mutable) account name. The rewrite only happens
// when a name identifies exactly one account, so duplicate names keep their
// old keys instead of merging two counters. It reports whether anything
// changed, so the caller can commit the rewrite durably.
//
// Counts under both keys are summed: they belong to the same account, and a
// crash can leave a name-keyed snapshot next to id-keyed journal records.
func migrateStatsToIDs(config model.Config, meta *model.Metadata) bool {
	if len(meta.Stats) == 0 || len(config.Accounts) == 0 {
		return false
	}
	counts := make(map[string]int, len(config.Accounts))
	ids := make(map[string]struct{}, len(config.Accounts))
	for _, account := range config.Accounts {
		if account.Name != "" {
			counts[account.Name]++
		}
		if account.ID != "" {
			ids[account.ID] = struct{}{}
		}
	}
	changed := false
	for _, account := range config.Accounts {
		if account.ID == "" || account.Name == "" || counts[account.Name] != 1 {
			continue
		}
		if _, nameIsAnIdentity := ids[account.Name]; nameIsAnIdentity {
			// A legacy name that is another account's identity is ambiguous;
			// leave it alone instead of merging two different accounts.
			continue
		}
		legacy, found := meta.Stats[account.Name]
		if !found {
			continue
		}
		meta.Stats[account.ID] = mergeAccountStats(meta.Stats[account.ID], legacy)
		delete(meta.Stats, account.Name)
		changed = true
	}
	return changed
}

// mergeAccountStats combines a legacy name-keyed counter with the identity
// keyed one, keeping the most recent usage and error.
func mergeAccountStats(current, legacy model.AccountStats) model.AccountStats {
	merged := current
	merged.Requests = current.Requests + legacy.Requests
	if legacy.LastUsed > current.LastUsed {
		merged.LastUsed, merged.LastError = legacy.LastUsed, legacy.LastError
	}
	return merged
}

func (s *Store) ResetRoundRobin() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rrCounter = 0
}

func (s *Store) writeConfigLocked() error {
	return writeJSON(s.configPath, s.config)
}

func (s *Store) writeMetaLocked() error {
	return writeJSON(s.metaPath, s.meta)
}
