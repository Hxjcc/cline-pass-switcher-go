package upstream

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

func writePrivateProbe(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"choices":[{"message":{"content":"OK","provider_metadata":{"gateway":{"routing":{"finalProvider":"openai-compatible-private","canonicalSlug":"private/test-contributor"}}}},"finish_reason":"stop"}]}`)
}

func TestSmartProbeSeparatesDefaultRouteAndCachesOneVerifiedPlan(t *testing.T) {
	var calls atomic.Int32
	var credentials []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		credentials = append(credentials, r.Header.Get("Authorization"))
		body := decodeRequestBody(t, r)
		gateway := getMap(getMap(body, "providerOptions"), "gateway")
		if len(getStringSlice(gateway, "has")) == 0 {
			if requestedOnlyProvider(body) != "" && requestedOnlyProvider(body) != pinProbeSlug {
				t.Error("saved pin leaked into default probe")
			}
			if len(getStringSlice(gateway, "ignore")) != 0 {
				t.Error("saved exclusions leaked into default probe")
			}
			writePrivateProbe(w)
		} else if requestedOnlyProvider(body) == pinProbeSlug {
			writeGatewayTestRejection(w)
		} else {
			writeGatewayTestStream(w, "deepseek")
		}
	}))
	defer upstream.Close()
	directory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("credential leaked to directory")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/models" {
			io.WriteString(w, `{"data":[{"id":"deepseek/test"}]}`)
		} else {
			io.WriteString(w, `{"data":{"endpoints":[{"provider_name":"deepseek","tags":["vision","tool-use"],"supports_implicit_caching":true}]}}`)
		}
	}))
	defer directory.Close()
	previousAPI := gatewayModelsAPI
	gatewayModelsAPI = directory.URL
	t.Cleanup(func() { gatewayModelsAPI = previousAPI })
	st := newStreamTestStore(t, upstream.URL)
	saved := model.PerModelConfig{Upstreams: []string{"old"}, Exclude: []string{"blocked"}, GatewayHas: []string{"vision"}}
	if err := st.UpdateConfig(func(c *model.Config) { c.PerModel["cline-pass/test"] = saved }); err != nil {
		t.Fatal(err)
	}
	saved = st.ModelConfig("cline-pass/test")
	_, _ = st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) {
		meta.GatewayCatalog = gatewayTestCatalog()
		meta.Pipeline = "planner"
		meta.LastProvider = "recent-real-request"
		meta.CanonicalSlug = "private/active-test"
		meta.Upstreams = []string{"old"}
	})
	service := New(st)
	result, err := service.SmartProbeModel(t.Context(), "cline-pass/test", false)
	if err != nil {
		t.Fatal(err)
	}
	d := result.RoutingDiscovery
	if d == nil || d.Status != "available" || !d.Current || d.Cached || len(d.Config.Upstreams) != 1 || d.Config.Upstreams[0] != "deepseek" {
		t.Fatalf("discovery=%+v", d)
	}
	if calls.Load() != 4 {
		t.Fatalf("need baseline, default negative, positive and negative; calls=%d", calls.Load())
	}
	if !reflect.DeepEqual(saved, st.ModelConfig("cline-pass/test")) {
		t.Fatal("discovery applied configuration")
	}
	if !result.DefaultRoute.Private || !slices.Equal(result.DefaultRoute.Providers, []string{privateChannel}) || st.ModelMeta("cline-pass/test").LastProvider != "recent-real-request" || st.ModelMeta("cline-pass/test").CanonicalSlug != "private/active-test" {
		t.Fatalf("default route overwrote active metadata: %+v", result.ModelMeta)
	}
	for _, credential := range credentials {
		if credential != credentials[0] {
			t.Fatal("controls used different accounts")
		}
	}
	result, err = service.SmartProbeModel(t.Context(), "cline-pass/test", false)
	if err != nil || !result.RoutingDiscovery.Cached || calls.Load() != 4 {
		t.Fatalf("cache miss: %+v, %v, calls=%d", result, err, calls.Load())
	}
	result, err = service.SmartProbeModel(t.Context(), "cline-pass/test", true)
	if err != nil || result.RoutingDiscovery.Cached || calls.Load() != 8 {
		t.Fatalf("forced refresh: %+v, %v, calls=%d", result, err, calls.Load())
	}
}

func TestSmartProbeClassifiesControlsConservatively(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		respond      func(http.ResponseWriter, *http.Request)
	}{
		{"ignored", "unavailable", func(w http.ResponseWriter, _ *http.Request) { writeGatewayTestStream(w, privateChannel) }},
		{"auth", "unconfirmed", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(401)
			io.WriteString(w, `{"error":{"message":"unauthorized"}}`)
		}},
		{"timeout", "unconfirmed", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(504)
			io.WriteString(w, `{"error":{"message":"upstream timed out"}}`)
		}},
		{"missing-metadata", "unconfirmed", func(w http.ResponseWriter, _ *http.Request) {
			io.WriteString(w, `{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`)
		}},
		{"no-matching-provider", "unavailable", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":{"message":"No available providers match the 'only' filter: deepseek"}}`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); tc.respond(w, r) }))
			defer server.Close()
			st := newStreamTestStore(t, server.URL)
			account := st.Accounts()[0]
			_, _ = st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) {
				meta.Pipeline = "planner"
				meta.GatewayCatalog = gatewayTestCatalog()
				meta.DefaultRoute = &model.DefaultRouteProbe{Profile: model.RoutingProfile("cline-pass/test", server.URL, account, model.PerModelConfig{}), Pipeline: "planner", Private: true, Providers: []string{privateChannel}, CheckedAt: time.Now().UnixMilli()}
			})
			service := New(st)
			result, err := service.SmartProbeModel(t.Context(), "cline-pass/test", false)
			if err != nil || result.RoutingDiscovery.Status != tc.status || !result.RoutingDiscovery.Current {
				t.Fatalf("discovery=%+v err=%v", result.RoutingDiscovery, err)
			}
			_, err = service.SmartProbeModel(t.Context(), "cline-pass/test", false)
			if err != nil || calls.Load() != 1 {
				t.Fatalf("recent failure was not cached: %v calls=%d", err, calls.Load())
			}
			_, _ = st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) {
				meta.RoutingDiscovery.CheckedAt = time.Now().Add(-16 * time.Minute).UnixMilli()
			})
			_, err = service.SmartProbeModel(t.Context(), "cline-pass/test", false)
			if err != nil || calls.Load() != 2 {
				t.Fatalf("old failure was not retried: %v calls=%d", err, calls.Load())
			}
		})
	}
}

func TestSmartProbeNativeRouteIgnoresSavedPinWhenCountingChannels(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeRequestBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			if requestedOnlyProvider(body) != "" {
				t.Error("saved pin restricted the baseline")
			}
			io.WriteString(w, `{"choices":[{"message":{"content":"OK","provider_metadata":{"gateway":{"routing":{"canonicalSlug":"deepseek/test","finalProvider":"deepseek","fallbacksAvailable":["other"],"planningReasoning":"System credentials planned for: deepseek, other. Total execution order: deepseek(system) → other(system)"}}}},"finish_reason":"stop"}]}`)
		} else {
			io.WriteString(w, `{"error":{"message":"No available providers match the 'only' filter. Available providers are: deepseek, other."}}`)
		}
	}))
	defer server.Close()
	st := newStreamTestStore(t, server.URL)
	_ = st.UpdateConfig(func(c *model.Config) {
		c.PerModel["cline-pass/test"] = model.PerModelConfig{Upstreams: []string{"deepseek"}}
	})
	result, err := New(st).SmartProbeModel(t.Context(), "cline-pass/test", false)
	if err != nil || result.RoutingDiscovery.Status != "native" || len(result.DefaultRoute.Providers) != 2 || calls.Load() != 2 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, calls.Load())
	}
}

func TestSmartProbeCachesTransientBaselineFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(503) }))
	defer server.Close()
	st := newStreamTestStore(t, server.URL)
	service := New(st)
	for range 2 {
		if _, err := service.SmartProbeModel(t.Context(), "cline-pass/test", false); err == nil {
			t.Fatal("baseline failure was hidden")
		}
	}
	if calls.Load() != 1 || st.ModelMeta("cline-pass/test").RoutingDiscovery.Status != "unconfirmed" {
		t.Fatal("transient failure repeated inference or became unavailable")
	}
}

func TestSmartProbeSkipsOrdinaryModelsAndRespectsExclusions(t *testing.T) {
	for _, tc := range []struct {
		name, pipeline, status, target string
		pinnable, private              bool
		exclude                        []string
	}{
		{name: "native", pipeline: "planner", status: "native", pinnable: true},
		{name: "ordinary-ignored", pipeline: "planner", status: "not_applicable"},
		{name: "direct", pipeline: "direct", status: "not_applicable"},
		{name: "unknown", status: "unconfirmed"},
		{name: "exclude-author", pipeline: "planner", status: "available", private: true, exclude: []string{"deepseek"}, target: "other"},
		{name: "exclude-all", pipeline: "planner", status: "unavailable", private: true, exclude: []string{"deepseek", "other"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var targets []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				target := requestedOnlyProvider(decodeRequestBody(t, r))
				targets = append(targets, target)
				if target == pinProbeSlug {
					writeGatewayTestRejection(w)
				} else {
					writeGatewayTestStream(w, target)
				}
			}))
			defer server.Close()
			st := newStreamTestStore(t, server.URL)
			_ = st.UpdateConfig(func(c *model.Config) { c.PerModel["cline-pass/test"] = model.PerModelConfig{Exclude: tc.exclude} })
			account := st.Accounts()[0]
			_, _ = st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) {
				meta.Pipeline = tc.pipeline
				meta.GatewayCatalog = gatewayTestCatalog()
				meta.DefaultRoute = &model.DefaultRouteProbe{Profile: model.RoutingProfile("cline-pass/test", server.URL, account, model.PerModelConfig{}), Pipeline: tc.pipeline, Private: tc.private, Pinnable: tc.pinnable, Providers: []string{"deepseek", "other"}, CheckedAt: time.Now().UnixMilli()}
			})
			result, err := New(st).SmartProbeModel(t.Context(), "cline-pass/test", false)
			if err != nil || result.RoutingDiscovery.Status != tc.status {
				t.Fatalf("result=%+v err=%v", result.RoutingDiscovery, err)
			}
			if tc.target == "" && len(targets) != 0 {
				t.Fatalf("unnecessary inference: %v", targets)
			}
			if tc.target != "" && !slices.Equal(targets, []string{tc.target, pinProbeSlug}) {
				t.Fatalf("targets=%v", targets)
			}
		})
	}
}

func TestCachedRoutingEvidenceRequiresExactRecentCredentialAndPlan(t *testing.T) {
	st := newStreamTestStore(t, "https://example.test")
	account := st.Accounts()[0]
	cfg := model.PerModelConfig{Upstreams: []string{"deepseek"}, Exclude: []string{"blocked"}, GatewayHas: []string{"vision"}}
	profile := model.RoutingProfile("cline-pass/test", st.UpstreamBase(), account, cfg)
	check := model.RoutingCheck{Profile: profile, OK: true, Matched: true, Rejected: true, CheckedAt: time.Now().UnixMilli()}
	_, _ = st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) { meta.RoutingChecks = map[string]model.RoutingCheck{profile: check} })
	service := New(st)
	if _, ok := service.CachedRoutingCheck("cline-pass/test", cfg, account); !ok {
		t.Fatal("fresh evidence not reused")
	}
	for _, changed := range []model.PerModelConfig{
		{Upstreams: []string{"other"}, Exclude: cfg.Exclude, GatewayHas: cfg.GatewayHas},
		{Upstreams: cfg.Upstreams, Exclude: []string{"another"}, GatewayHas: cfg.GatewayHas},
		{Upstreams: cfg.Upstreams, Exclude: cfg.Exclude, GatewayHas: []string{"tool-use"}},
	} {
		if _, ok := service.CachedRoutingCheck("cline-pass/test", changed, account); ok {
			t.Fatal("changed plan reused evidence")
		}
	}
	rotated := account
	rotated.Key = "changed"
	if _, ok := service.CachedRoutingCheck("cline-pass/test", cfg, rotated); ok {
		t.Fatal("rotated credential reused evidence")
	}
	if _, ok := service.CachedRoutingCheck("cline-pass/other", cfg, account); ok {
		t.Fatal("other model reused evidence")
	}
	_, _ = st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) {
		c := meta.RoutingChecks[profile]
		c.CheckedAt = time.Now().Add(-7 * time.Hour).UnixMilli()
		meta.RoutingChecks[profile] = c
	})
	if _, ok := service.CachedRoutingCheck("cline-pass/test", cfg, account); ok {
		t.Fatal("expired evidence reused")
	}
}

func TestCanceledSmartProbePreservesPreviousDiscoveryAndProof(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { cancel(); w.WriteHeader(503) }))
	defer server.Close()
	st := newStreamTestStore(t, server.URL)
	_, _ = st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) {
		meta.GatewayCatalog = gatewayTestCatalog()
		meta.RoutingDiscovery = &model.RoutingDiscovery{Status: "available", CheckedAt: 1}
		meta.RoutingChecks = map[string]model.RoutingCheck{"previous": {OK: true}}
	})
	before, _ := json.Marshal(st.ModelMeta("cline-pass/test"))
	if _, err := New(st).SmartProbeModel(ctx, "cline-pass/test", true); err != context.Canceled {
		t.Fatalf("err=%v", err)
	}
	after, _ := json.Marshal(st.ModelMeta("cline-pass/test"))
	if string(before) != string(after) {
		t.Fatal("cancellation replaced previous evidence")
	}
}
