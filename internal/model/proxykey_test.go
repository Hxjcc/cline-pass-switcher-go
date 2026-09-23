package model

import "testing"

// Config normalisation has to keep the issued keys usable: identities are
// assigned once, secrets are de-duplicated, and the master keys can never
// appear twice (which would double-count their spend and let a grant shadow
// the console credential).
func TestNormalizeConfigCleansProxyKeys(t *testing.T) {
	cfg := Config{
		ProxyKey: "master",
		AdminKey: "console",
		ProxyKeys: []ProxyKeyGrant{
			{Name: "fresh", Key: "sk-a", Enabled: true},
			{Name: "no key", Key: "  "},
			{Name: "duplicate secret", Key: "sk-a", Enabled: true},
			{Name: "master again", Key: "master", Enabled: true},
			{Name: "negative limit", Key: "sk-b", Enabled: true, SpendLimitUSD: -3},
		},
	}
	NormalizeConfig(&cfg)
	if len(cfg.ProxyKeys) != 2 {
		t.Fatalf("expected two usable grants, got %#v", cfg.ProxyKeys)
	}
	if cfg.ProxyKeys[0].ID == "" || cfg.ProxyKeys[1].ID == "" || cfg.ProxyKeys[0].ID == cfg.ProxyKeys[1].ID {
		t.Fatalf("identities must be assigned and unique: %#v", cfg.ProxyKeys)
	}
	if cfg.ProxyKeys[1].SpendLimitUSD != 0 {
		t.Fatalf("a negative limit must be clamped: %#v", cfg.ProxyKeys[1])
	}
}

// An existing id must survive a console round-trip, otherwise every save would
// orphan the counters that enforce a spend limit.
func TestNormalizeConfigKeepsGrantIdentity(t *testing.T) {
	cfg := Config{ProxyKeys: []ProxyKeyGrant{{ID: "key_keep", Name: "x", Key: "sk-x", Enabled: true}}}
	NormalizeConfig(&cfg)
	if len(cfg.ProxyKeys) != 1 || cfg.ProxyKeys[0].ID != "key_keep" {
		t.Fatalf("identity was not preserved: %#v", cfg.ProxyKeys)
	}
	if generated := NewProxyKeyValue(); len(generated) < 16 || generated[:3] != "sk-" {
		t.Fatalf("generated keys must be sk- prefixed: %q", generated)
	}
}
