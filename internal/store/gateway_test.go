package store

import (
	"slices"
	"testing"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

func TestGatewayRoutingSurvivesJournalRecovery(t *testing.T) {
	s, dir := testStore(t)
	if err := s.UpdateConfig(func(cfg *model.Config) {
		cfg.Accounts = []model.Account{{Name: "one", Key: "placeholder", Enabled: true}}
		cfg.PerModel["test"] = model.PerModelConfig{Upstreams: []string{"deepseek"}, GatewayHas: []string{"vision", "implicit-caching"}}
	}); err != nil {
		t.Fatal(err)
	}
	account := s.Accounts()[0]
	cfg := s.ModelConfig("test")
	profile := model.RoutingProfile("test", s.UpstreamBase(), account, cfg)
	_, err := s.UpdateModelMeta("test", func(meta *model.ModelMeta) {
		meta.GatewayCatalog = &model.GatewayCatalog{Model: "deepseek/test", Providers: []model.GatewayProvider{{Slug: "deepseek", Has: []string{"vision", "implicit-caching"}}}}
		meta.RoutingChecks = map[string]model.RoutingCheck{profile: {Profile: profile, AccountID: account.ID, Has: cfg.GatewayHas, Targets: []string{"deepseek"}, Actual: []string{"deepseek"}, OK: true, Matched: true, Rejected: true}}
		meta.DefaultRoute = &model.DefaultRouteProbe{Providers: []string{"openai-compatible-private"}, Private: true}
		meta.RoutingDiscovery = &model.RoutingDiscovery{Status: "available", Config: cfg, CheckProfile: profile}
	})
	if err != nil {
		t.Fatal(err)
	}
	s.simulateCrash(t)
	recovered, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	saved := recovered.ModelConfig("test")
	if !slices.Equal(saved.GatewayHas, []string{"implicit-caching", "vision"}) {
		t.Fatalf("filter lost on recovery: %+v", saved)
	}
	meta := recovered.ModelMeta("test")
	if len(meta.GatewayCatalog.Providers) != 1 || !meta.RoutingChecks[profile].OK {
		t.Fatalf("catalog or verification lost on recovery: %+v", meta)
	}
	if meta.DefaultRoute == nil || !meta.DefaultRoute.Private || meta.RoutingDiscovery == nil || meta.RoutingDiscovery.CheckProfile != profile || !slices.Equal(meta.RoutingDiscovery.Config.GatewayHas, cfg.GatewayHas) {
		t.Fatalf("discovery lost on recovery: %+v", meta)
	}
	if model.RoutingProfile("test", recovered.UpstreamBase(), recovered.Accounts()[0], saved) != profile {
		t.Fatal("verification did not remain valid after recovery")
	}
}
