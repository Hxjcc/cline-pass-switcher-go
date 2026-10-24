package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/buildinfo"
	"github.com/Hxjcc/cline-pass-switcher-go/internal/jsonx"
	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
	"github.com/Hxjcc/cline-pass-switcher-go/internal/sse"
	"github.com/Hxjcc/cline-pass-switcher-go/internal/strx"
)

type gatewayObservation struct {
	status    int
	actual    string
	canonical string
	err       string
	choices   bool
	generated bool
	terminal  bool
	meta      GatewayMeta
	usage     *model.UsageStats
	ms        int64
}

// Complete the stream before deciding: a 200, role delta or [DONE] can coexist
// with a routing error, and the provider metadata commonly arrives last.
func (s *Service) observeGatewayRoute(ctx context.Context, base, modelID, target string, has []string, account model.Account) (out gatewayObservation) {
	started := time.Now()
	defer func() { out.ms = time.Since(started).Milliseconds() }()
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	body := map[string]any{
		"model": modelID, "messages": []any{map[string]any{"role": "user", "content": "Reply only OK"}},
		"stream": true, "max_tokens": 256,
		"providerOptions": map[string]any{"gateway": map[string]any{"only": []string{target}, "has": has}},
	}
	meta := s.store.ModelMeta(modelID)
	if base, _, ok := datedModelBase(modelID); ok && len(meta.ReasoningEfforts) == 0 {
		meta = s.store.ModelMeta(base)
	}
	if slices.Contains(meta.ReasoningEfforts, "none") {
		body["reasoning_effort"] = "none"
	}
	rawBody, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(rawBody))
	if err != nil {
		out.err = err.Error()
		return
	}
	for key, value := range chatHeaders(account.Key) {
		req.Header.Set(key, value)
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	response, err := s.client.Do(req)
	if err != nil {
		out.err = err.Error()
		return
	}
	defer response.Body.Close()
	out.status = response.StatusCode
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		out.err = err.Error()
		return
	}
	if len(raw) > maxResponseBytes {
		out.err = "验证响应超过大小限制"
		return
	}
	consume := func(root map[string]any) {
		if message := extractError(root); message != "" && out.err == "" {
			out.err = message
		}
		if hasChoices(root) {
			out.choices = true
		}
		if metadata := ParseMeta(root); !metadata.Empty() {
			out.meta = metadata
			out.canonical = metadata.CanonicalSlug
		}
		if usage := getMap(root, "usage"); usage != nil {
			out.usage = &model.UsageStats{PromptTokens: intField(usage, "prompt_tokens"), CompletionTokens: intField(usage, "completion_tokens"), TotalTokens: intField(usage, "total_tokens"), Cost: floatField(usage, "cost")}
		}
		for _, choice := range getSlice(root, "choices") {
			item := jsonx.Map(choice)
			if getString(item, "finish_reason") != "" {
				out.terminal = true
			}
			for _, holder := range []string{"delta", "message"} {
				part := getMap(item, holder)
				if getString(part, "content") != "" || getString(part, "reasoning") != "" || len(getSlice(part, "tool_calls")) > 0 {
					out.generated = true
				}
			}
		}
	}
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "event-stream") {
		var parser sse.Parser
		emit := func(block string) bool {
			payload, found := ssePayload(block)
			if !found {
				return true
			}
			if payload == "[DONE]" {
				out.terminal = true
				return true
			}
			var root map[string]any
			if json.Unmarshal([]byte(payload), &root) != nil {
				if out.err == "" {
					out.err = "验证流包含无法解析的数据"
				}
				return true
			}
			consume(root)
			return true
		}
		if err := parser.Feed(raw, emit); err != nil {
			out.err = err.Error()
		}
		if tail := parser.Finish(); tail != "" {
			emit(tail)
		}
	} else {
		var root map[string]any
		if json.Unmarshal(raw, &root) != nil {
			out.err = "验证响应不是有效的 JSON"
			return
		}
		consume(root)
		out.terminal = out.choices
	}
	for i := len(out.meta.Attempts) - 1; i >= 0; i-- {
		if attempt := out.meta.Attempts[i]; attempt.Success && attempt.Provider != "" {
			out.actual = attempt.Provider
			break
		}
	}
	if out.actual == "" {
		out.actual = firstNonEmptyString(out.meta.FinalProvider, out.meta.ResolvedProvider, out.meta.Provider)
	}
	if out.err == "" && out.status != http.StatusOK {
		out.err = fmt.Sprintf("上游返回 HTTP %d", out.status)
	}
	return
}

func (s *Service) recordGatewayObservation(modelID, target string, has []string, account model.Account, out gatewayObservation, expectedRejection bool) error {
	var message *string
	if out.err != "" && !expectedRejection {
		value := strx.Truncate(out.err, 2000)
		message = &value
	}
	status := "unknown"
	if expectedRejection {
		status = "rejected"
	} else if out.err == "" && SameProvider(target, out.actual) {
		status = "matched"
	} else if out.actual != "" {
		status = "mismatch"
	}
	if message == nil && !expectedRejection && (status == "mismatch" || !out.terminal || !out.choices || out.actual == "") {
		value := "验证未取得匹配的完整路由证据"
		if status == "mismatch" {
			value = "目标 " + target + "，实际 " + out.actual
		}
		message = &value
	}
	return s.store.Record(model.HistoryEntry{TS: time.Now().UnixMilli(), Model: modelID, Provider: out.actual, Resolved: out.actual, Canonical: out.canonical, MS: out.ms, Stream: true, Kind: "routing-check", Account: account.Name, AccountID: account.ID, Error: message, Usage: out.usage, GatewayHas: slices.Clone(has), TargetProviders: []string{target}, RoutingStatus: status, GatewayAttempts: out.meta.Attempts, GenerationID: out.meta.GenerationID, Trace: []model.Trace{{Upstream: target, Status: out.status, MS: out.ms, Note: strx.Truncate(out.err, 200)}}})
}

func (s *Service) VerifyGatewayRouting(ctx context.Context, modelID string, cfg model.PerModelConfig, account model.Account) (model.RoutingCheck, error) {
	started := time.Now()
	base := s.store.UpstreamBase()
	cfg.GatewayHas = model.NormalizeGatewayHas(cfg.GatewayHas)
	if err := model.ValidateGatewayHas(cfg.GatewayHas); err != nil {
		return model.RoutingCheck{}, err
	}
	if len(cfg.GatewayHas) == 0 {
		return model.RoutingCheck{}, fmt.Errorf("请先选择能力过滤条件")
	}
	if account.Key == "" || !account.Enabled {
		return model.RoutingCheck{}, errNoAccount
	}
	if s.store.ModelMeta(modelID).Pipeline == "direct" {
		return model.RoutingCheck{}, fmt.Errorf("OpenRouter 线路不能使用 Vercel 能力过滤")
	}
	catalog, err := s.ReadGatewayCatalog(ctx, modelID, false)
	if err != nil {
		return model.RoutingCheck{}, err
	}
	targets := []string{}
	for _, target := range cfg.Upstreams {
		if !containsProvider(cfg.Exclude, target) {
			targets = append(targets, target)
		}
	}
	if len(targets) == 0 {
		for _, provider := range catalog.Providers {
			if provider.Matches(cfg.GatewayHas) && !containsProvider(cfg.Exclude, provider.Slug) {
				targets = append(targets, provider.Slug)
			}
		}
		author := strings.SplitN(catalog.Model, "/", 2)[0]
		if containsProvider(targets, author) {
			targets = []string{author}
		} else if len(targets) > 0 {
			targets = targets[:1]
		}
	}
	if len(targets) == 0 {
		return model.RoutingCheck{}, fmt.Errorf("没有满足所选能力的候选供应商")
	}
	check := model.RoutingCheck{Profile: model.RoutingProfile(modelID, base, account, cfg), AccountID: account.ID, Account: account.Name, Has: slices.Clone(cfg.GatewayHas), Targets: targets, Actual: []string{}, Matched: true}
	for _, target := range targets {
		out := s.observeGatewayRoute(ctx, base, modelID, target, cfg.GatewayHas, account)
		if err := s.recordGatewayObservation(modelID, target, cfg.GatewayHas, account, out, false); err != nil {
			return check, err
		}
		check.Actual = append(check.Actual, out.actual)
		if out.err != "" || !out.choices || !out.terminal || !SameProvider(target, out.actual) {
			check.Matched = false
			check.Code = "unconfirmed"
			if out.err == "" && out.choices && out.terminal && out.actual != "" && !SameProvider(target, out.actual) {
				check.Code = "preference_ignored"
			}
			if (out.status == 200 || out.status == 400 || out.status == 404 || out.status == 422) && out.err != "" && routingRejectionRE.MatchString(out.err) && (strings.Contains(strings.ToLower(out.err), "only") || strings.Contains(strings.ToLower(out.err), "filter")) {
				check.Code = "no_matching_provider"
			}
			check.Error = out.err
			if check.Error == "" {
				check.Error = fmt.Sprintf("目标 %s，实际 %s；缺少完整路由证据时无法确认钉住", target, firstNonEmptyString(out.actual, "未知"))
			}
			break
		}
	}
	if check.Matched && ctx.Err() == nil {
		out := s.observeGatewayRoute(ctx, base, modelID, pinProbeSlug, cfg.GatewayHas, account)
		// Only the specific impossible-provider rejection counts. Authentication,
		// capability mismatches, timeouts and a local allow-list error do not.
		check.Rejected = (out.status == 200 || out.status == 400 || out.status == 404 || out.status == 422) && out.err != "" && strings.Contains(out.err, pinProbeSlug) && strings.Contains(strings.ToLower(out.err), "only") && routingRejectionRE.MatchString(out.err) && out.actual == "" && !out.generated
		if err := s.recordGatewayObservation(modelID, pinProbeSlug, cfg.GatewayHas, account, out, check.Rejected); err != nil {
			return check, err
		}
		if !check.Rejected {
			check.Code = "unconfirmed"
			if out.actual != "" || out.generated {
				check.Code = "preference_ignored"
			}
			check.Error = "无效上游未被明确拒绝，无法确认 only 生效"
			if out.err != "" {
				check.Error = out.err
			}
		}
	}
	check.OK = check.Matched && check.Rejected
	check.CheckedAt = time.Now().UnixMilli()
	check.MS = time.Since(started).Milliseconds()
	if err := ctx.Err(); err != nil {
		return check, err
	}
	_, err = s.store.UpdateModelMeta(modelID, func(meta *model.ModelMeta) {
		if meta.RoutingChecks == nil {
			meta.RoutingChecks = map[string]model.RoutingCheck{}
		}
		if len(meta.RoutingChecks) >= 64 {
			oldest := ""
			var ts int64
			for key, c := range meta.RoutingChecks {
				if oldest == "" || c.CheckedAt < ts {
					oldest = key
					ts = c.CheckedAt
				}
			}
			delete(meta.RoutingChecks, oldest)
		}
		meta.RoutingChecks[check.Profile] = check
		if check.OK {
			if !slices.Equal(meta.GatewayStatusHas, cfg.GatewayHas) {
				meta.UpstreamStatus = map[string]model.UpstreamStatus{}
			}
			meta.GatewayStatusHas = slices.Clone(cfg.GatewayHas)
			if meta.UpstreamStatus == nil {
				meta.UpstreamStatus = map[string]model.UpstreamStatus{}
			}
			for _, target := range targets {
				meta.UpstreamStatus[target] = model.UpstreamStatus{Status: "ok", CheckedAt: check.CheckedAt, MS: check.MS}
			}
		}
	})
	return check, err
}

func (s *Service) HasVerifiedRouting(modelID string, cfg model.PerModelConfig) bool {
	meta := s.RoutingMeta(modelID, cfg, s.store.ModelMeta(modelID))
	for _, check := range meta.RoutingChecks {
		if check.OK && check.Current {
			return true
		}
	}
	return false
}
