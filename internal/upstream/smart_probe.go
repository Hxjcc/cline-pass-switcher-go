package upstream

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

const routingEvidenceTTL = 6 * time.Hour

func recentRoutingEvidence(ts int64, ttl time.Duration) bool {
	age := time.Since(time.UnixMilli(ts))
	return ts > 0 && age >= 0 && age < ttl
}

func routingConfigProfile(cfg model.PerModelConfig) string {
	return model.RoutingProfile("", "", model.Account{}, cfg)
}

func (s *Service) CachedRoutingCheck(modelID string, cfg model.PerModelConfig, account model.Account) (model.RoutingCheck, bool) {
	profile := model.RoutingProfile(modelID, s.store.UpstreamBase(), account, cfg)
	check, found := s.store.ModelMeta(modelID).RoutingChecks[profile]
	return check, found && check.Profile == profile && account.Enabled && account.Key != "" && RoutingCheckFresh(check)
}

func RoutingCheckFresh(check model.RoutingCheck) bool {
	return check.OK && check.Matched && check.Rejected && recentRoutingEvidence(check.CheckedAt, routingEvidenceTTL)
}

// SmartProbeModel discovers the default route independently of the active
// configuration. A suitable single-channel planner route gets one bounded
// capability control pair. Discovery never writes PerModel configuration.
func (s *Service) SmartProbeModel(ctx context.Context, modelID string, force bool) (ProbeResult, error) {
	started := time.Now()
	account := s.store.PickAccount()
	if account.Key == "" {
		return ProbeResult{}, errNoAccount
	}
	saved := s.store.ModelConfig(modelID)
	profile := model.RoutingProfile(modelID, s.store.UpstreamBase(), account, model.PerModelConfig{})
	meta := s.store.ModelMeta(modelID)
	baseline := meta.DefaultRoute
	if !force && (baseline == nil || baseline.Profile != profile || !recentRoutingEvidence(baseline.CheckedAt, routingEvidenceTTL)) {
		if previous := meta.RoutingDiscovery; previous != nil && previous.Profile == profile && previous.SourceConfig == routingConfigProfile(saved) && previous.Status == "unconfirmed" && recentRoutingEvidence(previous.CheckedAt, time.Minute) {
			return ProbeResult{}, fmt.Errorf("%s", previous.Reason)
		}
	}
	if force || baseline == nil || baseline.Profile != profile || !recentRoutingEvidence(baseline.CheckedAt, routingEvidenceTTL) {
		result, err := s.probeModel(ctx, modelID, account, true)
		if err != nil {
			if ctx.Err() != nil {
				return ProbeResult{}, ctx.Err()
			}
			d := model.RoutingDiscovery{Profile: profile, SourceConfig: routingConfigProfile(saved), AccountID: account.ID, Account: account.Name, Status: "unconfirmed", Reason: err.Error(), CheckedAt: time.Now().UnixMilli()}
			_, storeErr := s.store.UpdateModelMeta(modelID, func(current *model.ModelMeta) { current.RoutingDiscovery = &d })
			if storeErr != nil {
				return ProbeResult{}, storeErr
			}
			return ProbeResult{}, err
		}
		meta = result.ModelMeta
		baseline = meta.DefaultRoute
	}
	if baseline == nil {
		return ProbeResult{}, fmt.Errorf("未取得默认路由结果")
	}
	d := model.RoutingDiscovery{Profile: profile, SourceConfig: routingConfigProfile(saved), AccountID: account.ID, Account: account.Name, CheckedAt: time.Now().UnixMilli()}
	finish := func() (ProbeResult, error) {
		if err := ctx.Err(); err != nil {
			return ProbeResult{}, err
		}
		updated, err := s.store.UpdateModelMeta(modelID, func(current *model.ModelMeta) { current.RoutingDiscovery = &d })
		if err != nil {
			return ProbeResult{}, err
		}
		return ProbeResult{OK: true, MS: time.Since(started).Milliseconds(), ModelMeta: s.RoutingMeta(modelID, s.store.ModelConfig(modelID), updated)}, nil
	}
	if baseline.Pinnable {
		d.Status = "native"
		d.CheckedAt = baseline.CheckedAt
		return finish()
	}
	eligible := baseline.Pipeline == "planner" && (baseline.Private || len(baseline.Providers) == 1 || baseline.Reason == pinReasonSingleProvider)
	if !eligible {
		d.Status = "not_applicable"
		d.Reason = "当前线路沿用普通渠道设置，无需能力路由补测"
		if baseline.Pipeline == "" {
			d.Status = "unconfirmed"
			d.Reason = "未取得可识别的路由元数据，请重新探测"
		}
		return finish()
	}
	if previous := meta.RoutingDiscovery; !force && previous != nil && previous.Profile == profile && (previous.SourceConfig == routingConfigProfile(saved) || routingConfigProfile(previous.Config) == routingConfigProfile(saved)) {
		ttl := time.Minute
		if previous.Status == "unavailable" {
			ttl = 15 * time.Minute
		}
		if previous.Status == "available" {
			if _, valid := s.CachedRoutingCheck(modelID, previous.Config, account); valid {
				d = *previous
				d.Cached = true
				return finish()
			}
		} else if recentRoutingEvidence(previous.CheckedAt, ttl) {
			d = *previous
			d.Cached = true
			return finish()
		}
	}
	catalog, err := s.ReadGatewayCatalog(ctx, modelID, force)
	if err != nil {
		d.Status = "unconfirmed"
		d.Reason = err.Error()
		return finish()
	}
	providers := slices.Clone(catalog.Providers)
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
	for _, provider := range providers {
		if containsProvider(saved.Exclude, provider.Slug) {
			continue
		}
		has := RecommendedGatewayHas(catalog, provider.Slug)
		if len(has) == 0 {
			continue
		}
		d.Config = model.PerModelConfig{Upstreams: []string{provider.Slug}, Exclude: slices.Clone(saved.Exclude), GatewayHas: has}
		break
	}
	if len(d.Config.Upstreams) == 0 {
		d.Status = "unavailable"
		d.Reason = "目录没有未被排除且具备可推荐能力的候选供应商"
		return finish()
	}
	check, cached := s.CachedRoutingCheck(modelID, d.Config, account)
	if force || !cached {
		check, err = s.VerifyGatewayRouting(ctx, modelID, d.Config, account)
		if err != nil {
			d.Status = "unconfirmed"
			d.Reason = err.Error()
			return finish()
		}
	}
	d.Cached = cached && !force
	d.CheckedAt = check.CheckedAt
	d.CheckProfile = check.Profile
	d.Status = "unconfirmed"
	d.Reason = check.Error
	if check.OK {
		d.Status = "available"
		d.Reason = "正反对照通过，可采用该供应商路由"
	}
	if !check.OK && (check.Code == "preference_ignored" || check.Code == "no_matching_provider") {
		d.Status = "unavailable"
	}
	return finish()
}

func (s *Service) discoveryCurrent(modelID string, cfg model.PerModelConfig, discovery *model.RoutingDiscovery, accounts []model.Account, base string) bool {
	if discovery == nil || !recentRoutingEvidence(discovery.CheckedAt, routingEvidenceTTL) {
		return false
	}
	if discovery.Status != "native" && discovery.Status != "not_applicable" && discovery.SourceConfig != routingConfigProfile(cfg) && routingConfigProfile(discovery.Config) != routingConfigProfile(cfg) {
		return false
	}
	for _, account := range accounts {
		if account.Enabled && account.Key != "" && discovery.Profile == model.RoutingProfile(modelID, base, account, model.PerModelConfig{}) {
			if discovery.Status == "available" {
				check, valid := s.CachedRoutingCheck(modelID, discovery.Config, account)
				return valid && check.Profile == discovery.CheckProfile
			}
			return true
		}
	}
	return false
}
