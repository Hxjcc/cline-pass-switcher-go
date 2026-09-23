package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

const (
	checkpointInterval = 100
	// Request records reach the page cache on every commit but are fsynced in
	// batches: a power loss can drop the last few records, while a process
	// crash loses nothing because recovery replays the journal. Configuration
	// and model changes cannot be reconstructed, so they always wait for disk.
	journalSyncInterval = 32
)

// The journal is the commit point for both configuration and metadata. JSON
// snapshots are materialized views: interrupted two-file writes are replayed
// from the last metadata sequence, without duplicating history or counters.
type journalEntry struct {
	Sequence  uint64              `json:"sequence"`
	Kind      string              `json:"kind"`
	Config    *model.Config       `json:"config,omitempty"`
	Metadata  *model.Metadata     `json:"metadata,omitempty"`
	ModelID   string              `json:"modelID,omitempty"`
	ModelMeta *model.ModelMeta    `json:"modelMeta,omitempty"`
	Record    *model.HistoryEntry `json:"record,omitempty"`
}

func (s *Store) commitLocked(entry journalEntry) error {
	if s.closed {
		return errors.New("data store is closed")
	}
	if s.writeErr != nil {
		return s.writeErr
	}
	entry.Sequence = s.meta.StoreSequence + 1
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	// Admin operations wait for the disk; unlike request history they cannot be
	// rebuilt, so they must not be lost to a power failure.
	durable := entry.Kind != "record"
	// Materializing the JSON snapshots rewrites both files, so only the
	// operations that change configuration or the whole metadata document do it
	// immediately. A probed model row is safe in the journal: it is fsynced
	// above and replayed on the next start, which is what makes bulk probing
	// stop rewriting metadata.json once per model.
	snapshotNow := durable && entry.Kind != "model"
	if err = s.appendJournalLocked(append(raw, '\n'), durable); err != nil {
		return err
	}
	// Detach caller-owned maps and pointers before publishing committed state.
	s.applyEntry(model.Clone(entry))
	s.pending++
	if !durable {
		s.unsynced++
		if s.unsynced >= journalSyncInterval {
			if err := s.syncJournalLocked(); err != nil {
				// The entry is published and present in the journal; keep both
				// sides consistent and let the sticky error stop new commits.
				return err
			}
		}
	}
	if snapshotNow || s.pending >= checkpointInterval {
		if err := s.checkpointLocked(); err != nil {
			// The change is already durable. Keep the journal for retry/recovery.
			log.Printf("store checkpoint failed (committed journal retained): %v", err)
		}
	}
	return nil
}

// journalHandleLocked keeps one append handle open: reopening and closing the
// file for every commit was two extra syscalls per request for no benefit.
func (s *Store) journalHandleLocked() (*os.File, error) {
	if s.journal != nil {
		return s.journal, nil
	}
	f, err := os.OpenFile(s.journalPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open store journal: %w", err)
	}
	s.journal = f
	return f, nil
}

func (s *Store) appendJournalLocked(raw []byte, syncNow bool) error {
	f, err := s.journalHandleLocked()
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		return err
	}
	n, err := f.Write(raw)
	if err == nil && n != len(raw) {
		err = io.ErrShortWrite
	}
	if err == nil && syncNow {
		err = s.syncJournalLocked()
	}
	if err == nil {
		return nil
	}
	rollbackErr := f.Truncate(info.Size())
	if rollbackErr == nil {
		rollbackErr = f.Sync()
	}
	if rollbackErr != nil {
		s.writeErr = fmt.Errorf("journal rollback failed; restart to recover: %w", rollbackErr)
	}
	return errors.Join(fmt.Errorf("commit store journal: %w", err), s.writeErr)
}

func (s *Store) syncJournalLocked() error {
	f, err := s.journalHandleLocked()
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		s.writeErr = fmt.Errorf("store journal sync failed; restart to recover: %w", err)
		return s.writeErr
	}
	s.journalSyncs++
	s.unsynced = 0
	return nil
}

func (s *Store) closeJournalLocked() error {
	if s.journal == nil {
		return nil
	}
	err := s.journal.Close()
	s.journal = nil
	return err
}

func (s *Store) applyEntry(entry journalEntry) {
	switch entry.Kind {
	case "config":
		s.config = *entry.Config
	case "metadata":
		s.meta = *entry.Metadata
	case "model":
		s.meta.Models[entry.ModelID] = *entry.ModelMeta
	case "record":
		e := *entry.Record
		if e.Error == nil && strings.HasPrefix(e.Model, "cline-pass/") && !slices.Contains(s.config.KnownModels, e.Model) {
			s.config.KnownModels = append(s.config.KnownModels, e.Model)
			model.NormalizeConfig(&s.config)
		}
		current := s.meta.Models[e.Model]
		current.LastProvider, current.LastMS = e.Provider, e.MS
		if e.Canonical != "" {
			current.CanonicalSlug = e.Canonical
		}
		s.meta.Models[e.Model] = current
		s.meta.History = append([]model.HistoryEntry{e}, s.meta.History...)
		if len(s.meta.History) > model.HistoryLimit {
			s.meta.History = s.meta.History[:model.HistoryLimit]
		}
		// Counters follow the stable account identity; entries written by
		// older versions only carry a name and keep working through the
		// fallback.
		statsKey := e.AccountID
		if statsKey == "" {
			statsKey = e.Account
		}
		if statsKey != "" {
			stats := s.meta.Stats[statsKey]
			stats.Requests++
			stats.LastUsed, stats.LastError = e.TS, e.Error
			s.meta.Stats[statsKey] = stats
		}
		// The same journal record carries the spend of an issued key. It is
		// replayed (and therefore survives restarts) exactly like the account
		// counters above; only upstream-reported cost is counted, so a limit
		// can never be enforced against a number we made up.
		if e.KeyID != "" {
			if s.meta.KeyUsage == nil {
				s.meta.KeyUsage = map[string]model.KeyUsage{}
			}
			usage := s.meta.KeyUsage[e.KeyID]
			usage.Requests++
			usage.LastUsed = e.TS
			if e.Usage != nil && e.Usage.Cost != nil && *e.Usage.Cost > 0 {
				usage.SpentMicroUSD += int64(math.Round(*e.Usage.Cost * 1e6))
			}
			s.meta.KeyUsage[e.KeyID] = usage
		}
	case "remove":
		known := make([]string, 0, len(s.config.KnownModels))
		for _, id := range s.config.KnownModels {
			if id != entry.ModelID {
				known = append(known, id)
			}
		}
		s.config.KnownModels = known
		delete(s.config.PerModel, entry.ModelID)
		s.config.RemovedModels = append(s.config.RemovedModels, entry.ModelID)
		model.NormalizeConfig(&s.config)
		delete(s.meta.Models, entry.ModelID)
	case "clear":
		s.meta.History = []model.HistoryEntry{}
	}
	s.meta.StoreSequence = entry.Sequence
}

func (entry journalEntry) valid() bool {
	switch entry.Kind {
	case "config":
		return entry.Config != nil
	case "metadata":
		return entry.Metadata != nil
	case "model":
		return entry.ModelMeta != nil
	case "record":
		return entry.Record != nil
	case "remove", "clear":
		return true
	default:
		return false
	}
}

func (s *Store) recoverJournal() error {
	f, err := os.OpenFile(s.journalPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	var offset int64
	for {
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			if len(line) > 0 {
				// A killed/failed append can leave only the last entry incomplete.
				if err := f.Truncate(offset); err != nil {
					return err
				}
				if err := f.Sync(); err != nil {
					return err
				}
				log.Print("store recovery discarded an incomplete journal tail")
			}
			break
		}
		if err != nil {
			return err
		}
		var entry journalEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return fmt.Errorf("invalid store journal at byte %d: %w", offset, err)
		}
		if !entry.valid() {
			return fmt.Errorf("invalid store journal operation at byte %d", offset)
		}
		if entry.Sequence > s.meta.StoreSequence {
			if entry.Sequence != s.meta.StoreSequence+1 {
				return fmt.Errorf("store journal sequence gap at byte %d", offset)
			}
			s.applyEntry(entry)
			s.pending++
		}
		offset += int64(len(line))
	}
	model.NormalizeConfig(&s.config)
	model.NormalizeMetadata(&s.meta)
	return syncDirectory(filepath.Dir(s.journalPath))
}

func (s *Store) checkpointLocked() error {
	if s.unsynced > 0 {
		if err := s.syncJournalLocked(); err != nil {
			return err
		}
	}
	if err := s.writeConfigLocked(); err != nil {
		return err
	}
	if err := s.writeMetaLocked(); err != nil {
		return err
	}
	// The snapshots now contain every record, so the journal can be replaced.
	// Close the append handle first: Windows refuses to rename over an open file.
	if err := s.closeJournalLocked(); err != nil {
		return err
	}
	// Metadata now includes the sequence, so replay remains idempotent if the
	// process stops before clearing the old journal.
	if err := writeAtomic(s.journalPath, nil); err != nil {
		return err
	}
	s.pending = 0
	return nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var err error
	if s.writeErr != nil {
		err = s.writeErr
	} else {
		err = s.checkpointLocked()
	}
	// A failed checkpoint must still release the append handle and fsync what
	// has been written, so a normal shutdown never drops buffered records.
	if s.unsynced > 0 {
		err = errors.Join(err, s.syncJournalLocked())
	}
	err = errors.Join(err, s.closeJournalLocked())
	if s.lock != nil {
		err = errors.Join(err, s.lock.Close())
	}
	return err
}

func writeJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(raw, '\n'))
}

func writeAtomic(path string, raw []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".store-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}
