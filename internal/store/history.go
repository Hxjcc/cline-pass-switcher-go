package store

import (
	"crypto/rand"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

func newHistoryID() string { return "hist_" + rand.Text() }

// Old snapshots have no row identities. Assign them once and commit the
// migrated metadata before serving cursors, even when two rows are identical.
func ensureHistoryIDs(meta *model.Metadata) bool {
	changed := false
	seen := make(map[string]bool, len(meta.History))
	for index := range meta.History {
		entry := &meta.History[index]
		if entry.ID == "" || seen[entry.ID] {
			entry.ID = newHistoryID()
			changed = true
		}
		seen[entry.ID] = true
	}
	return changed
}
