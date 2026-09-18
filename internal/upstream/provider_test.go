package upstream

import (
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

func TestCanonicalProviderMatchesGatewayNamesToPinSlugs(t *testing.T) {
	meta := model.ModelMeta{
		Upstreams: []string{"z-ai", "atlas-cloud", "inference-net", "siliconflow"},
		UpstreamDetail: map[string]model.UpstreamDetail{
			"atlas-cloud": {Slug: "atlas-cloud", Name: "AtlasCloud"},
			"gmicloud":    {Slug: "gmicloud", Name: "GMI Cloud"},
		},
	}
	cases := map[string]string{
		// OpenRouter display names as they appear in the completion.
		"Z.AI":          "z-ai",
		"AtlasCloud":    "atlas-cloud",
		"Inference.net": "inference-net",
		"SiliconFlow":   "siliconflow",
		"GMI Cloud":     "gmicloud",
		// Vercel already reports slugs; they pass through unchanged.
		"z-ai":                      "z-ai",
		"openai-compatible-private": "openai-compatible-private",
		// Unknown names fall back to slug conventions.
		"Some New Vendor": "some-new-vendor",
		"io.net":          "io-net",
		"":                "",
	}
	for raw, want := range cases {
		if got := CanonicalProvider(meta, raw); got != want {
			t.Errorf("CanonicalProvider(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestParseRoutingSlugifiesDirectProvider(t *testing.T) {
	routing := ParseRouting(map[string]any{
		"provider": "Z.AI",
		"model":    "z-ai/glm-5.3-flash",
		"choices": []any{
			map[string]any{"message": map[string]any{"content": "OK"}},
		},
	})
	if routing.Pipeline != "direct" {
		t.Fatalf("pipeline = %q", routing.Pipeline)
	}
	if routing.FinalProvider != "z-ai" || routing.FinalProviderName != "Z.AI" {
		t.Fatalf("provider = %q / %q", routing.FinalProvider, routing.FinalProviderName)
	}
	if routing.CanonicalSlug != "z-ai/glm-5.3-flash" {
		t.Fatalf("canonical = %q", routing.CanonicalSlug)
	}
}
