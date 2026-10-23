package upstream

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

func gatewayTestCatalog() *model.GatewayCatalog {
	return &model.GatewayCatalog{Model: "deepseek/test", FetchedAt: time.Now().UnixMilli(), Providers: []model.GatewayProvider{{Slug: "deepseek", Name: "DeepSeek", Has: []string{"vision", "implicit-caching", "tool-use"}}, {Slug: "other", Name: "Other", Has: []string{"tool-use"}}}}
}

func writeGatewayTestStream(w http.ResponseWriter, provider string) {
	w.Header().Set("Content-Type", "text/event-stream")
	root := map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "OK", "provider_metadata": map[string]any{"gateway": map[string]any{"routing": map[string]any{"finalProvider": provider, "canonicalSlug": "deepseek/test"}}}}, "finish_reason": "stop"}}, "usage": map[string]any{"cost": 0.000001, "prompt_tokens": 7, "completion_tokens": 1}}
	raw, _ := json.Marshal(root)
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
}

func writeGatewayTestRejection(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	raw, _ := json.Marshal(map[string]any{"error": map[string]any{"message": "No available providers match the 'only' filter: " + pinProbeSlug + ". Available providers are: deepseek, other."}})
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
}

func TestGatewayCatalogDiscoversPrivateModelProvidersWithoutCredential(t *testing.T) {
	var paths []string
	directory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Authorization") != "" {
			t.Error("Cline credential leaked to catalog")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/models" {
			io.WriteString(w, `{"data":[{"id":"deepseek/test"}]}`)
			return
		}
		io.WriteString(w, `{"data":{"endpoints":[{"provider_name":"deepseek","tags":["vision","open-weights","tool-use"],"supports_implicit_caching":true,"context_length":1000000,"max_completion_tokens":384000},{"provider_name":"other","tags":["tool-use"]}]}}`)
	}))
	defer directory.Close()
	previous := gatewayModelsAPI
	gatewayModelsAPI = directory.URL
	t.Cleanup(func() { gatewayModelsAPI = previous })
	st := newStreamTestStore(t, "http://unused.invalid")
	_, _ = st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) {
		meta.CanonicalSlug = "private/test-contributor"
		meta.Pinnable = boolPtr(false)
		meta.PinReason = pinReasonSingleProvider
		meta.Upstreams = []string{privateChannel}
	})
	service := New(st)
	for range 2 {
		catalog, err := service.ReadGatewayCatalog(t.Context(), "cline-pass/test", false)
		if err != nil {
			t.Fatal(err)
		}
		if len(catalog.Providers) != 2 || !slices.Equal(RecommendedGatewayHas(catalog), []string{"implicit-caching", "vision"}) {
			t.Fatalf("catalog=%+v", catalog)
		}
		if slices.Contains(catalog.Providers[0].Has, "open-weights") {
			t.Fatal("unsupported tag became a has option")
		}
	}
	if len(paths) != 2 {
		t.Fatalf("directory should be cached: %v", paths)
	}
	meta := service.RoutingMeta("cline-pass/test", model.PerModelConfig{GatewayHas: []string{"vision"}}, st.ModelMeta("cline-pass/test"))
	if !slices.Equal(meta.Upstreams, []string{"deepseek"}) || *meta.Pinnable {
		t.Fatalf("directory candidates must be selectable but unverified: %+v", meta)
	}
}

func TestGatewayRequirementsSurvivePrivateVerdictAndTightenClientFilters(t *testing.T) {
	st := newStreamTestStore(t, "http://unused.invalid")
	_, _ = st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) {
		meta.Pipeline = "planner"
		meta.Pinnable = boolPtr(false)
		meta.PinReason = pinReasonSingleProvider
		meta.GatewayCatalog = gatewayTestCatalog()
	})
	service := New(st)
	cfg := model.PerModelConfig{Upstreams: []string{"other", "deepseek"}, GatewayHas: []string{"implicit-caching"}}
	body := map[string]any{"model": "cline-pass/test", "providerOptions": map[string]any{"gateway": map[string]any{"only": []any{"deepseek"}, "has": []any{"vision"}, "sort": "cost"}, "deepseek": map[string]any{"keep": true}}}
	attempts := service.BuildRequestAttempts("cline-pass/test", cfg, body)
	if len(attempts) != 1 || attempts[0].Upstream != "deepseek" {
		t.Fatalf("attempts=%+v", attempts)
	}
	send := service.InjectPrefs(body, "cline-pass/test", attempts[0])
	gateway := getMap(getMap(send, "providerOptions"), "gateway")
	if !slices.Equal(gateway["has"].([]string), []string{"implicit-caching", "vision"}) || gateway["sort"] != "cost" {
		t.Fatalf("gateway=%+v", gateway)
	}
	if len(getStringSlice(getMap(getMap(body, "providerOptions"), "gateway"), "has")) != 1 {
		t.Fatal("client request was mutated")
	}
	for _, clientOnly := range [][]any{{"missing"}, {}} {
		body["providerOptions"] = map[string]any{"gateway": map[string]any{"only": clientOnly}}
		attempts = service.BuildRequestAttempts("cline-pass/test", cfg, body)
		if len(attempts) != 1 || attempts[0].RoutingError == nil {
			t.Fatalf("conflicting policy did not fail: %+v", attempts)
		}
	}
	// Capability-only requests must not disappear at InjectPrefs' old early return.
	attempt := service.BuildRequestAttempts("cline-pass/test", model.PerModelConfig{GatewayHas: []string{"vision"}}, map[string]any{})[0]
	if getMap(getMap(service.InjectPrefs(nil, "cline-pass/test", attempt), "providerOptions"), "gateway")["has"] == nil {
		t.Fatal("capability-only routing was dropped")
	}
}

func TestGatewayVerificationUsesOneAccountAndInvalidatesRotatedKey(t *testing.T) {
	var mu sync.Mutex
	var credentials []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		credentials = append(credentials, r.Header.Get("Authorization"))
		mu.Unlock()
		body := decodeRequestBody(t, r)
		if body["model"] != "cline-pass/test" {
			t.Error("request lost cline-pass prefix")
		}
		if requestedOnlyProvider(body) == pinProbeSlug {
			writeGatewayTestRejection(w)
		} else {
			writeGatewayTestStream(w, "deepseek")
		}
	}))
	defer server.Close()
	st := newStreamTestStore(t, server.URL)
	if err := st.UpdateConfig(func(cfg *model.Config) {
		cfg.AccountMode = "roundrobin"
		cfg.Accounts = append(cfg.Accounts, model.Account{Name: "second", Key: "sk_second", Enabled: true})
	}); err != nil {
		t.Fatal(err)
	}
	_, _ = st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) {
		meta.GatewayCatalog = gatewayTestCatalog()
		meta.Pinnable = boolPtr(false)
		meta.PinReason = pinReasonSingleProvider
	})
	service := New(st)
	account := st.Accounts()[0]
	cfg := model.PerModelConfig{Upstreams: []string{"deepseek"}, GatewayHas: []string{"vision", "implicit-caching"}}
	check, err := service.VerifyGatewayRouting(t.Context(), "cline-pass/test", cfg, account)
	if err != nil || !check.OK {
		t.Fatalf("check=%+v err=%v", check, err)
	}
	if len(credentials) != 2 || credentials[0] != credentials[1] || credentials[0] != "Bearer "+account.Key {
		t.Fatalf("different accounts used for controls: %v", credentials)
	}
	if err := st.UpdateConfig(func(c *model.Config) { c.PerModel["cline-pass/test"] = cfg }); err != nil {
		t.Fatal(err)
	}
	meta := service.RoutingMeta("cline-pass/test", st.ModelConfig("cline-pass/test"), st.ModelMeta("cline-pass/test"))
	if !*meta.Pinnable || len(meta.Upstreams) != 1 {
		t.Fatalf("verified single filtered provider must be pinnable: %+v", meta)
	}
	if len(st.History()) != 2 || st.History()[1].Usage == nil {
		t.Fatal("verification calls and usage were not recorded")
	}
	if err := st.UpdateConfig(func(c *model.Config) { c.Accounts[0].Key = "rotated" }); err != nil {
		t.Fatal(err)
	}
	if service.HasVerifiedRouting("cline-pass/test", cfg) {
		t.Fatal("rotation reused stale evidence")
	}
}

func TestGatewayVerificationRejectsFalsePositiveEvidence(t *testing.T) {
	for _, scenario := range []string{"wrong-provider", "missing-metadata", "late-error", "invalid-provider-accepted", "negative-generated", "auth"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := decodeRequestBody(t, r)
				negative := requestedOnlyProvider(body) == pinProbeSlug
				if scenario == "auth" {
					w.WriteHeader(401)
					io.WriteString(w, `{"error":"Unauthorized"}`)
					return
				}
				if negative {
					if scenario == "invalid-provider-accepted" {
						writeGatewayTestStream(w, "deepseek")
						return
					}
					if scenario == "negative-generated" {
						w.Header().Set("Content-Type", "text/event-stream")
						io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\n")
					}
					writeGatewayTestRejection(w)
					return
				}
				switch scenario {
				case "wrong-provider":
					writeGatewayTestStream(w, privateChannel)
				case "missing-metadata":
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				case "late-error":
					writeGatewayTestStream(w, "deepseek")
					io.WriteString(w, "data: {\"error\":{\"message\":\"late failure\"}}\n\n")
				default:
					writeGatewayTestStream(w, "deepseek")
				}
			}))
			defer server.Close()
			st := newStreamTestStore(t, server.URL)
			_, _ = st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) { meta.GatewayCatalog = gatewayTestCatalog() })
			check, err := New(st).VerifyGatewayRouting(t.Context(), "cline-pass/test", model.PerModelConfig{Upstreams: []string{"deepseek"}, GatewayHas: []string{"vision"}}, st.Accounts()[0])
			if err != nil {
				t.Fatal(err)
			}
			if check.OK || strings.TrimSpace(check.Error) == "" {
				t.Fatalf("invalid evidence became success: %+v", check)
			}
		})
	}
}
