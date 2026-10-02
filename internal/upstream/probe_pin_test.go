package upstream

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

// A probe is what the console shows as 最近命中, so it has to ask the way a real
// request asks. Without the pin every pinned model looked like a miss even when
// its traffic routed correctly.
func TestProbeModelSendsTheConfiguredPin(t *testing.T) {
	var bodies []map[string]any
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body := decodeRequestBody(t, request)
		bodies = append(bodies, body)
		writer.Header().Set("Content-Type", "application/json")
		if len(bodies) > 1 {
			// The channel-discovery probe asks for an impossible provider.
			_, _ = io.WriteString(writer, `{"error":{"message":"No allowed providers are available. Available providers: alpha, beta.","type":"upstream_error"}}`)
			return
		}
		_, _ = io.WriteString(writer, `{"id":"chatcmpl-1","model":"cline-pass/test","choices":[{"index":0,"message":{"role":"assistant","content":"OK","provider_metadata":{"gateway":{"routing":{"canonicalSlug":"z-ai/glm-5.3-flash","finalProvider":"z-ai","fallbacksAvailable":["atlas-cloud"],"planningReasoning":"z-ai won tier 0 over atlas-cloud."}}}},"finish_reason":"stop"}]}`)
	}))
	defer upstreamServer.Close()

	st := newStreamTestStore(t, upstreamServer.URL)
	if err := st.UpdateConfig(func(cfg *model.Config) {
		cfg.PerModel = map[string]model.PerModelConfig{
			"cline-pass/test": {Upstreams: []string{"z-ai"}},
		}
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := New(st).ProbeModel(t.Context(), "cline-pass/test"); err != nil {
		t.Fatal(err)
	}
	if len(bodies) < 2 {
		t.Fatalf("expected the routing call and the discovery call, got %d", len(bodies))
	}
	if !asksForChannel(bodies[0], "z-ai") {
		t.Fatalf("probe must carry the configured pin: %#v", bodies[0])
	}
}

// Without a configured pin the probe stays unconstrained, so the console shows
// whatever the gateway picks on its own.
func TestProbeModelWithoutAPinStaysUnconstrained(t *testing.T) {
	var bodies []map[string]any
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		bodies = append(bodies, decodeRequestBody(t, request))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"id":"chatcmpl-1","model":"cline-pass/test","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`)
	}))
	defer upstreamServer.Close()

	if _, err := New(newStreamTestStore(t, upstreamServer.URL)).ProbeModel(t.Context(), "cline-pass/test"); err != nil {
		t.Fatal(err)
	}
	if len(bodies) == 0 {
		t.Fatal("probe did not call the upstream")
	}
	if asksForChannel(bodies[0], "z-ai") {
		t.Fatalf("an unpinned probe must not ask for a channel: %#v", bodies[0])
	}
}

// A pinned probe carries the pin, so its plan only lists that one channel. That
// must not be mistaken for a single-provider model: the channel list still has
// to come from the impossible-provider harvest, and the model stays pinnable.
func TestProbeModelPinnedDoesNotLookSingleProvider(t *testing.T) {
	var calls atomic.Int32
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body := decodeRequestBody(t, request)
		writer.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			if !asksForChannel(body, "deepseek") {
				t.Errorf("main probe must carry the configured pin: %#v", body)
			}
			_, _ = io.WriteString(writer, `{"id":"chatcmpl-1","model":"cline-pass/test","choices":[{"index":0,"message":{"role":"assistant","content":"OK","provider_metadata":{"gateway":{"routing":{"canonicalSlug":"deepseek/deepseek-v4.1-flash","finalProvider":"deepseek","fallbacksAvailable":[],"planningReasoning":"Provider set restricted to: deepseek. System credentials planned for: deepseek. Total execution order: deepseek(system)"}}}},"finish_reason":"stop"}]}`)
			return
		}
		_, _ = io.WriteString(writer, `{"error":{"message":"No allowed providers. Available providers are: deepseek, alibaba, baseten, fireworks.","type":"upstream_error"}}`)
	}))
	defer upstreamServer.Close()

	st := newStreamTestStore(t, upstreamServer.URL)
	if err := st.UpdateConfig(func(cfg *model.Config) {
		cfg.PerModel = map[string]model.PerModelConfig{
			"cline-pass/test": {Upstreams: []string{"deepseek"}},
		}
	}); err != nil {
		t.Fatal(err)
	}

	result, err := New(st).ProbeModel(t.Context(), "cline-pass/test")
	if err != nil {
		t.Fatal(err)
	}
	meta := result.ModelMeta
	if meta.Pinnable == nil || !*meta.Pinnable {
		t.Fatalf("pinned probe must stay pinnable: pinnable=%v reason=%q", meta.Pinnable, meta.PinReason)
	}
	if meta.PinReason != "" {
		t.Fatalf("pinReason = %q, want empty", meta.PinReason)
	}
	assertSameStringSet(t, "upstreams", meta.Upstreams, []string{"deepseek", "alibaba", "baseten", "fireworks"})
}

// asksForChannel reports whether a chat body pins routing to one channel through
// either wire field the gateway understands.
func asksForChannel(body map[string]any, channel string) bool {
	candidates := [][]any{
		jsonx.Slice(jsonx.Map(body["provider"])["only"]),
		jsonx.Slice(jsonx.Map(jsonx.Map(body["providerOptions"])["gateway"])["only"]),
	}
	for _, only := range candidates {
		for _, value := range only {
			if text, ok := value.(string); ok && text == channel {
				return true
			}
		}
	}
	return false
}
