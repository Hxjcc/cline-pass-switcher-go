package store

import "github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"

// ResetKeyUsage clears the accumulated spend of one issued key, or of every
// key when all is true. The secret does not have to be rotated to re-open a
// limit: the operator may simply forgive what a holder already burned.
func (s *Store) ResetKeyUsage(id string, all bool) error {
	return s.UpdateMetadata(func(meta *model.Metadata) {
		if meta.KeyUsage == nil {
			meta.KeyUsage = map[string]model.KeyUsage{}
			return
		}
		if all {
			meta.KeyUsage = map[string]model.KeyUsage{}
			return
		}
		delete(meta.KeyUsage, id)
	})
}

// PruneKeyUsage drops the counters of keys that no longer exist, keeping the
// metadata document bounded by the number of live grants.
func (s *Store) PruneKeyUsage(live map[string]struct{}) error {
	return s.UpdateMetadata(func(meta *model.Metadata) {
		if len(meta.KeyUsage) == 0 {
			return
		}
		kept := make(map[string]model.KeyUsage, len(live))
		for id, usage := range meta.KeyUsage {
			if _, found := live[id]; found {
				kept[id] = usage
			}
		}
		meta.KeyUsage = kept
	})
}
