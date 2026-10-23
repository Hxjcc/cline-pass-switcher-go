package upstream

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/apierr"
	"github.com/Hxjcc/cline-pass-switcher-go/internal/jsonx"
	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

func routingList(value any) ([]string, error) {
	var result []string
	switch list := value.(type) {
	case []string:
		result = slices.Clone(list)
	case []any:
		for _, item := range list {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("路由参数必须是字符串数组")
			}
			result = append(result, text)
		}
	default:
		return nil, fmt.Errorf("路由参数必须是字符串数组")
	}
	for _, text := range result {
		if strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("路由参数不能包含空值")
		}
	}
	return result, nil
}

// BuildRequestAttempts resolves the policy once from the configuration snapshot
// and client extension fields. Client constraints can only tighten it.
func (s *Service) BuildRequestAttempts(modelID string, cfg model.PerModelConfig, body map[string]any) []Attempt {
	fail := func(message string) []Attempt {
		return []Attempt{{RoutingError: &apierr.Details{Status: http.StatusBadRequest, Type: "invalid_request_error", Code: "routing_conflict", Message: message}}}
	}
	failCode := func(message, code string) []Attempt {
		result := fail(message)
		result[0].RoutingError.Code = code
		return result
	}
	gateway := jsonx.Map(jsonx.Map(body["providerOptions"])["gateway"])
	if value, present := gateway["has"]; present {
		list, err := routingList(value)
		if err != nil {
			return fail(err.Error())
		}
		cfg.GatewayHas = append(slices.Clone(cfg.GatewayHas), list...)
	}
	cfg.GatewayHas = model.NormalizeGatewayHas(cfg.GatewayHas)
	if err := model.ValidateGatewayHas(cfg.GatewayHas); err != nil {
		return fail(err.Error())
	}
	if len(cfg.GatewayHas) > 0 && s.store.ModelMeta(modelID).Pipeline == "direct" {
		return fail("该模型走 OpenRouter 线路，不能应用 Vercel 能力过滤")
	}
	var only []string
	hasOnly := false
	for _, holder := range []map[string]any{gateway, jsonx.Map(body["provider"])} {
		if value, present := holder["only"]; present {
			list, err := routingList(value)
			if err != nil {
				return fail(err.Error())
			}
			if !hasOnly {
				only = list
			} else {
				only = intersectProviders(only, list)
			}
			hasOnly = true
		}
	}
	if hasOnly && len(only) == 0 {
		return fail("上游允许列表没有交集")
	}
	attempts := s.BuildAttempts(modelID, cfg)
	result := make([]Attempt, 0, len(attempts))
	for _, attempt := range attempts {
		attempt.PolicyResolved = true
		if attempt.Upstream == "" && len(attempt.ExcludeList) > 0 {
			known := s.knownChannels(modelID)
			if len(attempt.GatewayHas) > 0 {
				known = GatewayChannels(s.store.ModelMeta(modelID), attempt.GatewayHas)
			}
			if len(known) == 0 {
				return failCode("尚无该条件下的供应商列表，无法应用排除规则；请先读取能力或探测模型", "channel_list_unknown")
			}
			for _, target := range known {
				if !containsProvider(attempt.ExcludeList, target) {
					attempt.AllowList = append(attempt.AllowList, target)
				}
			}
			if len(attempt.AllowList) == 0 {
				return failCode("排除规则排除了全部渠道，没有可用的上游渠道", "no_allowed_channels")
			}
		}
		if hasOnly {
			if attempt.Upstream != "" && !containsProvider(only, attempt.Upstream) {
				continue
			}
			allowed := []string{}
			for _, provider := range only {
				if !containsProvider(attempt.ExcludeList, provider) {
					allowed = append(allowed, provider)
				}
			}
			if len(allowed) == 0 {
				continue
			}
			if len(attempt.AllowList) > 0 {
				allowed = intersectProviders(attempt.AllowList, allowed)
			}
			if len(allowed) == 0 {
				continue
			}
			attempt.AllowList = allowed
		}
		result = append(result, attempt)
	}
	if len(result) == 0 {
		return fail("客户端允许的上游与模型优先、排除设置没有交集")
	}
	return result
}

func containsProvider(values []string, value string) bool {
	for _, candidate := range values {
		if SameProvider(candidate, value) {
			return true
		}
	}
	return false
}

func intersectProviders(first, second []string) []string {
	var result []string
	for _, value := range first {
		if containsProvider(second, value) {
			result = append(result, value)
		}
	}
	return result
}

func ServingProvider(meta GatewayMeta) string {
	for i := len(meta.Attempts) - 1; i >= 0; i-- {
		if attempt := meta.Attempts[i]; attempt.Success && attempt.Provider != "" {
			return attempt.Provider
		}
	}
	return firstNonEmptyString(meta.ResolvedProvider, meta.FinalProvider, meta.Provider)
}
