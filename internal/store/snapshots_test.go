package store

import (
	"reflect"
	"sync"
	"testing"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

func TestRequestSnapshotsAreDetached(t *testing.T) {
	pinnable := true
	original := model.ModelMeta{
		ReasoningEfforts: []string{"high"}, InputModalities: []string{"text"}, OutputModalities: []string{"text"},
		AvailableProviders: []string{"a"}, Upstreams: []string{"a"}, Tier0: []string{"a"},
		UpstreamDetail:   map[string]model.UpstreamDetail{"a": {Name: "A"}},
		UpstreamStatus:   map[string]model.UpstreamStatus{"a": {Status: "ok"}},
		Pinnable:         &pinnable,
		GatewayStatusHas: []string{"vision"},
		GatewayCatalog:   &model.GatewayCatalog{Providers: []model.GatewayProvider{{Slug: "a", Has: []string{"vision"}}}},
		RoutingChecks:    map[string]model.RoutingCheck{"profile": {Has: []string{"vision"}, Targets: []string{"a"}, Actual: []string{"a"}}},
	}
	s := &Store{meta: model.Metadata{Models: map[string]model.ModelMeta{"test": original}},
		config: model.Config{ProxyKey: "key", UpstreamBase: "https://example.test", PerModel: map[string]model.PerModelConfig{
			"test": {Upstreams: []string{"a"}, Exclude: []string{"b"}, GatewayHas: []string{"vision"}},
		}}}
	snapshot := s.ModelMeta("test")
	for _, slice := range [][]string{snapshot.ReasoningEfforts, snapshot.InputModalities, snapshot.OutputModalities, snapshot.AvailableProviders, snapshot.Upstreams, snapshot.Tier0} {
		slice[0] = "changed"
	}
	snapshot.UpstreamDetail["a"] = model.UpstreamDetail{Name: "changed"}
	snapshot.UpstreamStatus["a"] = model.UpstreamStatus{Status: "changed"}
	*snapshot.Pinnable = false
	snapshot.GatewayCatalog.Providers[0].Has[0] = "changed"
	snapshot.GatewayStatusHas[0] = "changed"
	check := snapshot.RoutingChecks["profile"]
	check.Has[0] = "changed"
	check.Targets[0] = "changed"
	check.Actual[0] = "changed"
	if !reflect.DeepEqual(s.ModelMeta("test"), original) {
		t.Fatal("model snapshot aliases the store")
	}
	cfg := s.ModelConfig("test")
	cfg.Upstreams[0], cfg.Exclude[0] = "x", "y"
	cfg.GatewayHas[0] = "changed"
	stored := s.ModelConfig("test")
	if stored.Upstreams[0] != "a" || stored.Exclude[0] != "b" || stored.GatewayHas[0] != "vision" {
		t.Fatal("configuration snapshot aliases the store")
	}
	if !reflect.DeepEqual(s.ModelMeta("missing"), model.ModelMeta{}) || !reflect.DeepEqual(s.ModelConfig("missing"), model.PerModelConfig{}) {
		t.Fatal("missing model must have zero values")
	}
	if s.ProxyKey() != "key" || s.UpstreamBase() != "https://example.test" {
		t.Fatal("wrong scalar configuration")
	}
}

func TestModelSnapshotConcurrentUpdates(t *testing.T) {
	s := benchmarkCatalogStore()
	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 200 {
				meta := s.ModelMeta("model-42")
				meta.ReasoningEfforts[0] = "detached"
				delete(meta.UpstreamDetail, "a")
			}
		}()
	}
	for range 200 {
		s.mu.Lock()
		meta := s.meta.Models["model-42"]
		meta.ReasoningEfforts[0] = "updated"
		meta.UpstreamDetail["a"] = model.UpstreamDetail{Name: "updated"}
		s.mu.Unlock()
	}
	workers.Wait()
}
