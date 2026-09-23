package store

import (
	"encoding/json"
	"os"
)

// readConfigKeys reports which top-level keys a config document carried. A
// missing or unreadable file simply yields no keys: the effective-value report
// then attributes every field to the built-in defaults.
func readConfigKeys(path string) map[string]struct{} {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil
	}
	keys := make(map[string]struct{}, len(document))
	for key := range document {
		keys[key] = struct{}{}
	}
	return keys
}

// ConfigFileKeys reports whether config.json carried the given top-level key
// when the store was opened.
func (s *Store) ConfigFileKeys() map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make(map[string]bool, len(s.configFileKeys))
	for key := range s.configFileKeys {
		keys[key] = true
	}
	return keys
}
