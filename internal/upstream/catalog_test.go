package upstream

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

// useOpenRouterServer points the OpenRouter endpoints at a local server so the
// catalog aggregation can be exercised without the real one.
func useOpenRouterServer(t *testing.T, url string) {
	t.Helper()
	previous := openRouterAPI
	openRouterAPI = url
	t.Cleanup(func() { openRouterAPI = previous })
}

// The harvested channel list arrives in whatever order the gateway maps are
// walked, so compare it as a set.
func assertSameStringSet(t *testing.T, label string, got, want []string) {
	t.Helper()
	gotSorted, wantSorted := slices.Clone(got), slices.Clone(want)
	slices.Sort(gotSorted)
	slices.Sort(wantSorted)
	if !slices.Equal(gotSorted, wantSorted) {
		t.Fatalf("%s = %#v, want %#v", label, got, want)
	}
}

func decodeRequestBody(t *testing.T, request *http.Request) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		t.Errorf("decode request body: %v", err)
		return nil
	}
	return payload
}

// A planner-pipeline completion does not carry the channel list, so the probe
// asks the gateway for it with an impossible provider pin and parses the
// rejection. Both halves have to line up or the console shows no channels.
func TestProbeModelHarvestsPlannerChannels(t *testing.T) {
	var probes, harvests atomic.Int32
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/chat/completions" {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer sk_test" {
			t.Errorf("probe must use the account key, got %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		if _, harvesting := decodeRequestBody(t, request)["providerOptions"]; harvesting {
			harvests.Add(1)
			_, _ = io.WriteString(writer, `{"error":{"message":"No allowed providers. Available providers are: alpha, beta.","type":"upstream_error"}}`)
			return
		}
		probes.Add(1)
		_, _ = io.WriteString(writer, `{"id":"chatcmpl-1","model":"cline-pass/test","choices":[{"index":0,"message":{"role":"assistant","content":"OK","provider_metadata":{"gateway":{"routing":{"canonicalSlug":"z-ai/glm-5.3-flash","finalProvider":"Z.AI","fallbacksAvailable":["atlas-cloud"],"planningReasoning":"Z.AI won tier 0 over atlas-cloud."}}}},"finish_reason":"stop"}]}`)
	}))
	defer upstreamServer.Close()

	result, err := New(newStreamTestStore(t, upstreamServer.URL)).ProbeModel(t.Context(), "cline-pass/test")
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatal("a completion with routing metadata should probe as ok")
	}
	if probes.Load() != 1 || harvests.Load() != 1 {
		t.Fatalf("probe calls = %d, harvest calls = %d", probes.Load(), harvests.Load())
	}
	meta := result.ModelMeta
	if meta.Pipeline != "planner" || !meta.Pinnable {
		t.Fatalf("pipeline = %q, pinnable = %v", meta.Pipeline, meta.Pinnable)
	}
	assertSameStringSet(t, "upstreams", meta.Upstreams, []string{"alpha", "beta", "atlas-cloud"})
	assertSameStringSet(t, "availableProviders", meta.AvailableProviders, []string{"alpha", "beta"})
	// The gateway names the winner ("Z.AI"); pins and traces use its slug.
	if meta.LastProvider != "z-ai" {
		t.Fatalf("lastProvider = %q, want z-ai", meta.LastProvider)
	}
	if !slices.Contains(meta.Tier0, "Z.AI") {
		t.Fatalf("tier0 should keep the planning winner: %#v", meta.Tier0)
	}
	if meta.ProbedAt == 0 {
		t.Fatalf("probe timestamp missing: %#v", meta)
	}
}

// A direct-pipeline completion names its provider and canonical model, which
// the console needs as slugs to compare against the endpoints OpenRouter
// reports. The model list is cached, the per-model endpoint list is not.
func TestProbeModelResolvesDirectProviderAgainstOpenRouterEndpoints(t *testing.T) {
	var modelLists, endpoints, probes, harvests atomic.Int32
	openRouterServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(request.URL.Path, "/endpoints"):
			endpoints.Add(1)
			_, _ = io.WriteString(writer, `{"data":{"endpoints":[`+
				`{"tag":"z-ai/glm-5.3-flash","provider_name":"Z.AI","context_length":200000,"uptime_last_30m":99.5},`+
				`{"provider_name":"Atlas Cloud","context_length":128000,"uptime_last_30m":97}]}}`)
		case request.URL.Path == "/models":
			modelLists.Add(1)
			_, _ = io.WriteString(writer, `{"data":[{"id":"z-ai/glm-5.3-flash"}]}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer openRouterServer.Close()
	useOpenRouterServer(t, openRouterServer.URL)

	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// A direct-pipeline probe also harvests the channel list, with the same
		// impossible pin expressed through the "provider" field.
		if provider, ok := decodeRequestBody(t, request)["provider"].(map[string]any); ok {
			if only, _ := provider["only"].([]any); len(only) == 1 && only[0] == "__probe__" {
				harvests.Add(1)
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, `{"error":{"message":"no provider matches __probe__","type":"upstream_error"}}`)
				return
			}
		}
		probes.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"id":"gen-1","model":"z-ai/glm-5.3-flash","provider":"Z.AI","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`)
	}))
	defer upstreamServer.Close()

	st := newStreamTestStore(t, upstreamServer.URL)
	service := New(st)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := service.ProbeModel(t.Context(), "cline-pass/test"); err != nil {
			t.Fatal(err)
		}
	}
	if probes.Load() != 2 || harvests.Load() != 2 {
		t.Fatalf("probe requests = %d, harvest requests = %d, want 2 each", probes.Load(), harvests.Load())
	}
	if modelLists.Load() != 1 {
		t.Fatalf("the OpenRouter model list should be cached, requests = %d", modelLists.Load())
	}
	if endpoints.Load() != 2 {
		t.Fatalf("endpoints should be resolved per probe, requests = %d", endpoints.Load())
	}

	meta := st.ModelMeta("cline-pass/test")
	if meta.Pipeline != "direct" || meta.OpenRouterSlug != "z-ai/glm-5.3-flash" {
		t.Fatalf("pipeline = %q, openrouterSlug = %q", meta.Pipeline, meta.OpenRouterSlug)
	}
	if meta.LastProvider != "z-ai" {
		t.Fatalf("lastProvider = %q, want z-ai", meta.LastProvider)
	}
	assertSameStringSet(t, "upstreams", meta.Upstreams, []string{"z-ai", "atlas-cloud"})
	if detail := meta.UpstreamDetail["z-ai"]; detail.Name != "Z.AI" || detail.Context != 200000 || detail.Uptime != 99 || detail.Endpoints != 1 {
		t.Fatalf("z-ai detail = %#v", detail)
	}
	// A missing tag is common; the display name has to slugify the same way the
	// pin list does.
	if detail := meta.UpstreamDetail["atlas-cloud"]; detail.Name != "Atlas Cloud" || detail.Context != 128000 {
		t.Fatalf("atlas-cloud detail = %#v", detail)
	}
}

func TestCatalogUsesTheOneHourCache(t *testing.T) {
	var requests atomic.Int32
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/models" {
			http.NotFound(writer, request)
			return
		}
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"data":[{"id":"cline-pass/first"},{"id":"cline-pass/second"}]}`)
	}))
	defer upstreamServer.Close()

	st := newStreamTestStore(t, upstreamServer.URL)
	service := New(st)
	for attempt := 0; attempt < 2; attempt++ {
		ids := service.Catalog(t.Context())
		if len(ids) != 2 || ids[0] != "cline-pass/first" || ids[1] != "cline-pass/second" {
			t.Fatalf("catalog = %#v", ids)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("catalog should be cached for an hour, upstream requests = %d", requests.Load())
	}
	if st.Metadata().CatalogFetchedAt == 0 {
		t.Fatal("the fetched catalog should be recorded in metadata")
	}
}

// A failing catalog refresh must not erase the list the console is showing.
func TestCatalogKeepsTheStoredListWhenUpstreamFails(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstreamServer.Close()

	st := newStreamTestStore(t, upstreamServer.URL)
	if err := st.UpdateMetadata(func(meta *model.Metadata) {
		meta.Catalog = []string{"cline-pass/known"}
	}); err != nil {
		t.Fatal(err)
	}
	if ids := New(st).Catalog(t.Context()); len(ids) != 1 || ids[0] != "cline-pass/known" {
		t.Fatalf("catalog = %#v", ids)
	}
}

// Quota probing is display-only and never consumes inference quota. The console
// draws a meter for every row, disabled ones included, so the probe has to
// cover every account that still has a credential.
func TestProbeQuotasCoversEveryKeyedAccount(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/users/me/plan":
			_, _ = io.WriteString(writer, quotaPlanBody)
		case "/users/me/plan/usage-limits":
			_, _ = io.WriteString(writer, quotaLimitsBody)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer upstreamServer.Close()

	st := newStreamTestStore(t, upstreamServer.URL)
	if err := st.UpdateConfig(func(cfg *model.Config) {
		cfg.Accounts = append(cfg.Accounts,
			model.Account{Name: "keyless", Enabled: true},
			model.Account{Name: "disabled", Key: "sk_disabled", Enabled: false},
		)
	}); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, account := range st.Config().Accounts {
		ids[account.Name] = account.ID
	}

	service := New(st)
	quota := service.ProbeQuotas(t.Context(), nil, false)
	if len(quota) != 2 {
		t.Fatalf("probe should visit every account with a key: %#v", quota)
	}
	byName := map[string]AccountQuota{}
	for _, entry := range quota {
		byName[entry.Account] = entry
	}
	for _, name := range []string{"main", "disabled"} {
		if entry, found := byName[name]; !found || !entry.OK {
			t.Fatalf("%s should report its plan: %#v", name, quota)
		}
	}
	if byName["main"].Caps == nil || byName["main"].Caps.Monthly != 5000000000 {
		t.Fatalf("caps = %#v", byName["main"].Caps)
	}
	if got := service.ProbeQuotas(t.Context(), []string{ids["keyless"]}, false); len(got) != 0 {
		t.Fatalf("an account without a key must be skipped: %#v", got)
	}
}
