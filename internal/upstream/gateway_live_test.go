package upstream

import (
	"os"
	"slices"
	"testing"
)

// Public directory only: no inference and no real credentials are required.
func TestLiveGatewayDirectory(t *testing.T) {
	if os.Getenv("CLINE_ROUTING_LIVE_DIRECTORY") != "1" {
		t.Skip("set CLINE_ROUTING_LIVE_DIRECTORY=1 to read the public Vercel model directory")
	}
	service := New(newStreamTestStore(t, "http://unused.invalid"))
	catalog, err := service.ReadGatewayCatalog(t.Context(), "cline-pass/deepseek-v4.1-flash-1017", true)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, provider := range catalog.Providers {
		if provider.Slug == "deepseek" {
			found = true
			if !slices.Contains(provider.Has, "vision") || !slices.Contains(provider.Has, "implicit-caching") {
				t.Fatalf("current DeepSeek directory lacks the required tags: %+v", provider)
			}
		}
	}
	if !found {
		t.Fatal("DeepSeek provider missing from current directory")
	}
	t.Logf("model=%s suppliers=%d recommended=%v", catalog.Model, len(catalog.Providers), RecommendedGatewayHas(catalog))
}
