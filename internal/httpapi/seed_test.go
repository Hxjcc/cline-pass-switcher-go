package httpapi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/store"
)

// The e2e harness seeds a fresh data directory with a config.json that only
// names the models it drives. A partial config has to survive loading,
// environment overrides and normalization with that list intact.
func TestConfigFileSeedsTheSubscription(t *testing.T) {
	for _, name := range []string{"CLINE_PASS_KEY", "PROXY_KEY", "ADMIN_KEY"} {
		t.Setenv(name, "")
	}
	dir := t.TempDir()
	seed := filepath.Join(dir, "config.json")
	if err := os.WriteFile(seed, []byte(`{"knownModels":["cline-pass/seeded"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	known := st.Config().KnownModels
	if len(known) != 1 || known[0] != "cline-pass/seeded" {
		t.Fatalf("a seeded subscription must survive loading: %#v", known)
	}
}
