package upstream

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/jsonx"
	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

var gatewayModelsAPI = "https://ai-gateway.vercel.sh/v1"

const gatewayCatalogTTL = 6 * time.Hour

// ReadGatewayCatalog is a public directory read; it never submits inference or
// sends a Cline credential to the directory. Failed refreshes retain old data.
func (s *Service) ReadGatewayCatalog(ctx context.Context, modelID string, refresh bool) (model.GatewayCatalog, error) {
	meta := s.store.ModelMeta(modelID)
	if cached := meta.GatewayCatalog; !refresh && cached != nil && time.Since(time.UnixMilli(cached.FetchedAt)) < gatewayCatalogTTL {
		return *cached, nil
	}
	canonical := ""
	if meta.GatewayCatalog != nil {
		canonical = meta.GatewayCatalog.Model
	}
	if canonical == "" && !strings.HasPrefix(meta.CanonicalSlug, "private/") && !strings.HasPrefix(meta.CanonicalSlug, "vmc/") && !strings.HasPrefix(meta.CanonicalSlug, "cline-pass/") {
		canonical = meta.CanonicalSlug
	}
	if canonical == "" {
		if !strings.HasPrefix(modelID, "cline-pass/") {
			canonical = modelID
		} else {
			status, raw, err := s.fetchJSON(ctx, http.MethodGet, gatewayModelsAPI+"/models", nil, nil, 30*time.Second)
			if err != nil {
				return model.GatewayCatalog{}, err
			}
			if status != http.StatusOK {
				return model.GatewayCatalog{}, fmt.Errorf("读取 Vercel 模型目录失败（HTTP %d）", status)
			}
			var matches []string
			for _, item := range getSlice(jsonx.Map(raw), "data") {
				id := getString(jsonx.Map(item), "id")
				if strings.HasSuffix(id, "/"+strings.TrimPrefix(modelID, "cline-pass/")) {
					matches = append(matches, id)
				}
			}
			if len(matches) != 1 {
				return model.GatewayCatalog{}, fmt.Errorf("无法唯一确定 %s 的 Vercel 模型；请先拉取目录或探测模型", modelID)
			}
			canonical = matches[0]
		}
	}
	parts := strings.Split(canonical, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return model.GatewayCatalog{}, fmt.Errorf("无效的规范模型名")
	}
	endpoint := gatewayModelsAPI + "/models/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/endpoints"
	status, raw, err := s.fetchJSON(ctx, http.MethodGet, endpoint, nil, nil, 30*time.Second)
	if err != nil {
		return model.GatewayCatalog{}, err
	}
	if status != http.StatusOK {
		return model.GatewayCatalog{}, fmt.Errorf("读取供应商能力失败（HTTP %d）", status)
	}
	root := jsonx.Map(raw)
	data := getMap(root, "data")
	if data == nil {
		data = root
	}
	catalog := model.GatewayCatalog{Model: canonical, Source: endpoint, FetchedAt: time.Now().UnixMilli(), Providers: []model.GatewayProvider{}}
	seen := map[string]bool{}
	for _, rawEndpoint := range getSlice(data, "endpoints") {
		item := jsonx.Map(rawEndpoint)
		name := getString(item, "provider_name")
		slug := slugify(name)
		if combined := getString(item, "name"); strings.Contains(combined, " | ") {
			slug = strings.TrimSpace(strings.SplitN(combined, " | ", 2)[0])
		}
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		var capabilities []string
		for _, tag := range getStringSlice(item, "tags") {
			if model.ValidateGatewayHas([]string{tag}) == nil {
				capabilities = append(capabilities, tag)
			}
		}
		if getBool(item, "supports_implicit_caching") {
			capabilities = append(capabilities, "implicit-caching")
		}
		catalog.Providers = append(catalog.Providers, model.GatewayProvider{
			Slug: slug, Name: name, Has: model.NormalizeGatewayHas(capabilities),
			Quantization: strings.ToLower(getString(item, "quantization")),
			Context:      formatInt(item["context_length"]), Output: formatInt(item["max_completion_tokens"]),
		})
	}
	if len(catalog.Providers) == 0 {
		return model.GatewayCatalog{}, fmt.Errorf("目录没有返回供应商能力数据")
	}
	slices.SortFunc(catalog.Providers, func(a, b model.GatewayProvider) int { return strings.Compare(a.Slug, b.Slug) })
	_, err = s.store.UpdateModelMeta(modelID, func(current *model.ModelMeta) { current.GatewayCatalog = &catalog })
	return catalog, err
}

func RecommendedGatewayHas(catalog model.GatewayCatalog, targets ...string) []string {
	providers := slices.Clone(catalog.Providers)
	if len(targets) > 0 {
		providers = nil
		for _, target := range targets {
			index := slices.IndexFunc(catalog.Providers, func(p model.GatewayProvider) bool { return p.Slug == target })
			if index < 0 {
				return []string{}
			}
			providers = append(providers, catalog.Providers[index])
		}
		// Every selected provider must satisfy the recommended requirements.
		result := recommendableGatewayHas(providers[0])
		for _, p := range providers[1:] {
			has := recommendableGatewayHas(p)
			result = slices.DeleteFunc(result, func(value string) bool { return !slices.Contains(has, value) })
		}
		return result
	}
	// With no selection, prefer the model author's provider, then the first
	// provider with supported capability tags. Do not combine unrelated providers.
	author := strings.SplitN(catalog.Model, "/", 2)[0]
	slices.SortStableFunc(providers, func(a, b model.GatewayProvider) int {
		if a.Slug == author && b.Slug != author {
			return -1
		}
		if b.Slug == author && a.Slug != author {
			return 1
		}
		return 0
	})
	for _, p := range providers {
		result := recommendableGatewayHas(p)
		if len(result) > 0 {
			return result
		}
	}
	return []string{}
}

func recommendableGatewayHas(provider model.GatewayProvider) []string {
	result := []string{}
	for _, capability := range model.NormalizeGatewayHas(provider.Has) {
		// Quantization remains an explicit advanced setting; unknown directory
		// tags such as open-weights cannot become routing requirements.
		if !strings.Contains(capability, ":") && model.ValidateGatewayHas([]string{capability}) == nil {
			result = append(result, capability)
		}
	}
	return result
}

func GatewayChannels(meta model.ModelMeta, has []string) []string {
	var channels []string
	if meta.GatewayCatalog != nil {
		for _, p := range meta.GatewayCatalog.Providers {
			if p.Matches(has) {
				channels = append(channels, p.Slug)
			}
		}
	}
	return channels
}

func (s *Service) RoutingMeta(modelID string, cfg model.PerModelConfig, meta model.ModelMeta) model.ModelMeta {
	accounts := s.store.Accounts()
	base := s.store.UpstreamBase()
	verified := false
	profiles := make(map[string]string, len(accounts))
	for _, account := range accounts {
		if account.Enabled && account.Key != "" {
			profiles[model.RoutingProfile(modelID, base, account, cfg)] = account.Name
		}
	}
	for key, check := range meta.RoutingChecks {
		check.Current = false
		if name, current := profiles[check.Profile]; current {
			check.Current = true
			check.Account = name
			verified = verified || check.OK
		}
		meta.RoutingChecks[key] = check
	}
	if len(cfg.GatewayHas) > 0 {
		meta.Upstreams = GatewayChannels(meta, cfg.GatewayHas)
		meta.AvailableProviders = slices.Clone(meta.Upstreams)
		meta.Pipeline = "planner"
		meta.Pinnable = &verified
		meta.PinReason = "routing_unverified"
		if verified {
			meta.PinReason = ""
		}
		if meta.GatewayCatalog != nil {
			meta.CanonicalSlug = meta.GatewayCatalog.Model
		}
		if !verified || !slices.Equal(model.NormalizeGatewayHas(meta.GatewayStatusHas), model.NormalizeGatewayHas(cfg.GatewayHas)) {
			meta.UpstreamStatus = nil
		}
	}
	return meta
}
