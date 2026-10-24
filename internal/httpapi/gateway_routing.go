package httpapi

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
	"github.com/Hxjcc/cline-pass-switcher-go/internal/upstream"
)

func normalizeRoutingConfig(cfg model.PerModelConfig) model.PerModelConfig {
	cfg.Upstream = ""
	cfg.Upstreams = normalizeList(cfg.Upstreams)
	cfg.Exclude = normalizeList(cfg.Exclude)
	cfg.GatewayHas = model.NormalizeGatewayHas(cfg.GatewayHas)
	wanted := []string{}
	for _, value := range cfg.Upstreams {
		if !slices.Contains(cfg.Exclude, value) {
			wanted = append(wanted, value)
		}
	}
	cfg.Upstreams = wanted
	return cfg
}

func routingConfigEqual(a, b model.PerModelConfig) bool {
	a = normalizeRoutingConfig(a)
	b = normalizeRoutingConfig(b)
	return model.RoutingProfile("", "", model.Account{}, a) == model.RoutingProfile("", "", model.Account{}, b)
}

func (s *Server) handleGatewayCapabilities(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model     string   `json:"model"`
		Refresh   bool     `json:"refresh"`
		Upstreams []string `json:"upstreams"`
	}
	if err := readJSON(r, &body); err != nil {
		writeRequestError(w, err)
		return
	}
	if strings.TrimSpace(body.Model) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "model required"})
		return
	}
	catalog, err := s.upstream.ReadGatewayCatalog(r.Context(), body.Model, body.Refresh)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "catalog": catalog, "recommendedHas": upstream.RecommendedGatewayHas(catalog, body.Upstreams...)})
}

func (s *Server) handleVerifyRouting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model       string               `json:"model"`
		Config      model.PerModelConfig `json:"config"`
		AccountID   string               `json:"accountId"`
		AllAccounts bool                 `json:"allAccounts"`
		Apply       bool                 `json:"apply"`
		Force       bool                 `json:"force"`
		Adopt       bool                 `json:"adopt"`
	}
	if err := readJSON(r, &body); err != nil {
		writeRequestError(w, err)
		return
	}
	if strings.TrimSpace(body.Model) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "model required"})
		return
	}
	body.Config = normalizeRoutingConfig(body.Config)
	if err := model.ValidateGatewayHas(body.Config.GatewayHas); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if len(body.Config.Upstreams) > 10 || len(body.Config.Exclude) > 10 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "优先与排除渠道分别最多 10 个"})
		return
	}
	accounts := []model.Account{}
	if body.AllAccounts {
		for _, account := range s.store.Accounts() {
			if account.Enabled && account.Key != "" {
				accounts = append(accounts, account)
			}
		}
	} else if body.AccountID != "" {
		accounts = append(accounts, s.store.FindAccount(body.AccountID))
	} else {
		accounts = append(accounts, s.store.PickAccount())
	}
	if len(accounts) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "没有启用的账号"})
		return
	}
	for _, account := range accounts {
		if !account.Enabled || account.Key == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "验证账号不可用"})
			return
		}
	}
	previous := s.store.ModelConfig(body.Model)
	if body.Adopt {
		meta := s.upstream.RoutingMeta(body.Model, previous, s.store.ModelMeta(body.Model))
		d := meta.RoutingDiscovery
		if !body.Apply || body.Force || body.AllAccounts || d == nil || !d.Current || d.Status != "available" || len(accounts) != 1 || accounts[0].ID != d.AccountID || !routingConfigEqual(body.Config, d.Config) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "可采用的方案已改变或过期，请重新探测"})
			return
		}
	}
	checks := []model.RoutingCheck{}
	ok := true
	for _, account := range accounts {
		check, cached := s.upstream.CachedRoutingCheck(body.Model, body.Config, account)
		var err error
		if body.Adopt && !cached {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "验证记录已失效，请重新探测"})
			return
		}
		if body.Force || !cached {
			check, err = s.upstream.VerifyGatewayRouting(r.Context(), body.Model, body.Config, account)
		}
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		checks = append(checks, check)
		ok = ok && check.OK
	}
	applied := false
	if err := r.Context().Err(); err != nil {
		return
	}
	if body.Apply && ok {
		err := s.store.UpdateConfig(func(cfg *model.Config) {
			if !routingConfigEqual(previous, cfg.PerModel[body.Model]) {
				return
			}
			// Rotation or disabling during the probe invalidates its evidence.
			for _, check := range checks {
				if !upstream.RoutingCheckFresh(check) {
					return
				}
				valid := false
				for _, account := range cfg.Accounts {
					if account.Enabled && account.Key != "" && check.Profile == model.RoutingProfile(body.Model, cfg.UpstreamBase, account, body.Config) {
						valid = true
						break
					}
				}
				if !valid {
					return
				}
			}
			cfg.PerModel[body.Model] = body.Config
			applied = true
		})
		if err != nil {
			writeInternalError(w, err)
			return
		}
		if !applied {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "验证期间账号或路由配置已改变，请重新验证"})
			return
		}
	}
	if applied {
		for i := range checks {
			checks[i].Current = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "applied": applied, "checks": checks})
}

// Only routing requirements are retained, never authentication or messages.
type recordedRoutingKey struct{}
type recordedRouting struct {
	Has, Targets []string
	Attempts     []upstream.Attempt
}

func routedRequirements(service *upstream.Service, modelID string, cfg model.PerModelConfig, body map[string]any) recordedRouting {
	view := recordedRouting{Attempts: service.BuildRequestAttempts(modelID, cfg, body)}
	for _, attempt := range view.Attempts {
		view.Has = model.NormalizeGatewayHas(append(view.Has, attempt.GatewayHas...))
		if attempt.Upstream != "" && !slices.Contains(view.Targets, attempt.Upstream) {
			view.Targets = append(view.Targets, attempt.Upstream)
		}
		if attempt.Upstream == "" {
			for _, target := range attempt.AllowList {
				if !slices.Contains(view.Targets, target) {
					view.Targets = append(view.Targets, target)
				}
			}
		}
	}
	return view
}

func (s *Server) withRoutingRecord(ctx context.Context, modelID string, cfg model.PerModelConfig, body map[string]any) context.Context {
	return context.WithValue(ctx, recordedRoutingKey{}, routedRequirements(s.upstream, modelID, cfg, body))
}
