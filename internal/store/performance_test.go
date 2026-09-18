package store

import (
	"fmt"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

var benchmarkModelMeta model.ModelMeta

func benchmarkCatalogStore() *Store {
	meta := model.EmptyMetadata()
	for i := range 1000 {
		meta.Models[fmt.Sprintf("model-%d", i)] = model.ModelMeta{
			Description:      "Example model capability and routing metadata",
			ReasoningEfforts: []string{"low", "high", "max"}, Upstreams: []string{"a", "b"},
			UpstreamDetail: map[string]model.UpstreamDetail{"a": {Name: "Provider A", Context: 128000}},
		}
	}
	return &Store{meta: meta}
}

func BenchmarkRequestModelMetadata(b *testing.B) {
	s := benchmarkCatalogStore()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		benchmarkModelMeta = s.ModelMeta("model-42")
	}
}
