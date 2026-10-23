package upstream

import (
	"reflect"
	"slices"
	"testing"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

func TestGatewayRecommendationsFollowSelectedProviderCapabilities(t *testing.T) {
	catalog := model.GatewayCatalog{Model: "deepseek/test", Providers: []model.GatewayProvider{
		{Slug: "other", Has: []string{"tool-use", "reasoning", "free"}},
		{Slug: "deepseek", Has: []string{"vision", "implicit-caching", "reasoning", "tool-use", "structured-output", "open-weights", "quantization:fp8"}},
		{Slug: "vision-only", Has: []string{"vision"}},
	}}
	original := slices.Clone(catalog.Providers[1].Has)
	for _, test := range []struct {
		name    string
		targets []string
		want    []string
	}{
		{"prefer-author-all-capabilities", nil, []string{"implicit-caching", "reasoning", "structured-output", "tool-use", "vision"}},
		{"selected-provider-overrides-author", []string{"other"}, []string{"free", "reasoning", "tool-use"}},
		{"selected-providers-intersection", []string{"deepseek", "other"}, []string{"reasoning", "tool-use"}},
		{"selection-order-does-not-change-intersection", []string{"other", "deepseek"}, []string{"reasoning", "tool-use"}},
		{"no-shared-capability", []string{"other", "vision-only"}, []string{}},
		{"unknown-selection-does-not-fall-back", []string{"deepseek", "missing"}, []string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := RecommendedGatewayHas(catalog, test.targets...); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("recommended=%v, want=%v", got, test.want)
			}
		})
	}
	if !slices.Equal(catalog.Providers[1].Has, original) || catalog.Providers[0].Slug != "other" {
		t.Fatal("recommendations mutated the directory")
	}
	catalog.Model = "missing/test"
	if got := RecommendedGatewayHas(catalog); !slices.Equal(got, []string{"free", "reasoning", "tool-use"}) {
		t.Fatalf("missing author should use first provider with supported capabilities: %v", got)
	}
	catalog.Providers = []model.GatewayProvider{{Slug: "missing", Has: []string{"open-weights"}}, {Slug: "other", Has: []string{"tool-use"}}}
	if got := RecommendedGatewayHas(catalog); !slices.Equal(got, []string{"tool-use"}) {
		t.Fatalf("author without supported tags should fall back: %v", got)
	}
	if got := RecommendedGatewayHas(model.GatewayCatalog{}); len(got) != 0 {
		t.Fatalf("empty catalog produced requirements: %v", got)
	}
}
