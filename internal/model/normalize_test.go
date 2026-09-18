package model

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigMigratesLegacyFields(t *testing.T) {
	t.Setenv("CLINE_PASS_KEY", "")
	t.Setenv("PROXY_KEY", "")
	t.Setenv("PUBLIC_BASE_URL", "")
	t.Setenv("PORT", "")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := []byte(`{
	  "apiKey": "legacy-key",
	  "perModel": {
	    "cline-pass/glm-5.3": {
	      "upstream": "alibaba",
	      "pinMode": "preferred"
	    }
	  }
	}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Accounts) != 1 {
		t.Fatalf("expected one migrated account, got %d", len(config.Accounts))
	}
	if config.Accounts[0].Key != "legacy-key" || !config.Accounts[0].Enabled {
		t.Fatalf("unexpected migrated account: %#v", config.Accounts[0])
	}
	modelConfig := config.PerModel["cline-pass/glm-5.3"]
	if len(modelConfig.Upstreams) != 1 || modelConfig.Upstreams[0] != "alibaba" {
		t.Fatalf("expected legacy upstream migration, got %#v", modelConfig.Upstreams)
	}
	if modelConfig.PinMode != "preferred" {
		t.Fatalf("expected preferred mode, got %q", modelConfig.PinMode)
	}
	if config.Port != 3123 || config.UpstreamBase != DefaultUpstreamBase {
		t.Fatalf("expected defaults to survive partial config: %#v", config)
	}
}

func TestNormalizeConfigAssignsStableAccountIDs(t *testing.T) {
	config := DefaultConfig()
	config.APIKey = "legacy-key"
	NormalizeConfig(&config)
	if len(config.Accounts) != 1 || config.Accounts[0].ID == "" {
		t.Fatalf("expected an identity for the migrated account: %#v", config.Accounts)
	}
	assigned := config.Accounts[0].ID
	NormalizeConfig(&config)
	if config.Accounts[0].ID != assigned {
		t.Fatalf("identity must survive repeated normalization: %#v", config.Accounts)
	}
	config.Accounts = append(config.Accounts, Account{ID: assigned, Name: "copy", Key: "other"})
	NormalizeConfig(&config)
	if len(config.Accounts) != 2 {
		t.Fatalf("accounts lost: %#v", config.Accounts)
	}
	if config.Accounts[0].ID != assigned {
		t.Fatalf("first identity changed: %#v", config.Accounts)
	}
	if config.Accounts[1].ID == "" || config.Accounts[1].ID == assigned {
		t.Fatalf("duplicate identity was not replaced: %#v", config.Accounts)
	}
}
func TestNormalizeConfigExcludeWins(t *testing.T) {
	sortMode := "tps"
	config := DefaultConfig()
	config.PerModel["model"] = PerModelConfig{
		Upstreams: []string{"a", "b", "a"},
		Exclude:   []string{"b"},
		PinMode:   "unknown",
		Sort:      &sortMode,
	}
	NormalizeConfig(&config)
	value := config.PerModel["model"]
	if len(value.Upstreams) != 1 || value.Upstreams[0] != "a" {
		t.Fatalf("exclude should win over selected upstreams: %#v", value.Upstreams)
	}
	if value.Upstream != "a" {
		t.Fatalf("legacy mirror should point at first upstream, got %q", value.Upstream)
	}
	if value.PinMode != "strict" {
		t.Fatalf("unknown pin mode should normalize to strict, got %q", value.PinMode)
	}
}
