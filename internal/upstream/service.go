package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/apierr"
	"github.com/Hxjcc/cline-pass-switcher-go/internal/buildinfo"
	"github.com/Hxjcc/cline-pass-switcher-go/internal/jsonx"
	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
	"github.com/Hxjcc/cline-pass-switcher-go/internal/sse"
	"github.com/Hxjcc/cline-pass-switcher-go/internal/store"
	"github.com/Hxjcc/cline-pass-switcher-go/internal/strx"
)

// External catalog sources. Variables so tests (and a future self-hosted
// mirror) can point them at a local server.
var (
	openRouterAPI                = "https://openrouter.ai/api/v1"
	officialModelsDevURL         = "https://models.dev/api.json"
	officialRecommendedModelsURL = "https://api.cline.bot/api/v1/ai/cline/recommended-models"
)

type Service struct {
	store  *store.Store
	client *http.Client

	// Stream attempts have no overall deadline. The guard enforces a first-event
	// budget and then a mid-stream silence budget; both are stored as
	// nanoseconds for atomic access.
	streamHeadNanos int64
	streamIdleNanos int64
	// Buffered (non-stream) attempts can only be bounded by total time.
	nonStreamNanos int64

	accounts accountHealth

	// Quota snapshots are shared by the console and account selection. A full
	// window becomes a hold; a failed probe is remembered briefly so a down
	// endpoint cannot sit on the request path.
	quotaMu     sync.Mutex
	quotaState  map[string]quotaState
	quotaHold   map[string]quotaHold
	quotaFlight map[string]*quotaFlight
	// quotaBudget overrides quotaSelectionBudget for one service. Zero keeps
	// the production budget; tests shorten it.
	quotaBudget time.Duration

	// sticks remembers which account and pinned channel last served a conversation.
	stickMu sync.Mutex
	sticks  map[string]sessionStick
	// stickSweepAt throttles the expired-stick sweep. Guarded by stickMu.
	stickSweepAt time.Time
}

type ProbeResult struct {
	OK bool  `json:"ok"`
	MS int64 `json:"ms"`
	model.ModelMeta
}

// routingProbe records what the impossible-provider probe proved about the
// gateway's support for the pipeline's routing preference.
type routingProbe struct {
	providers []string
	supported bool
	ignored   bool
}

const (
	pinReasonGatewayIgnores = "gateway_ignores_provider_preferences"
	pinReasonSingleProvider = "single_provider"
	pinReasonProbeFailed    = "probe_failed"
	pinReasonNoChannels     = "no_channels"
	pinReasonUnsupported    = "unsupported_pipeline"
)

// privateChannel is the one endpoint Cline uses to serve the models it hosts
// itself (the VMC/contributor deployments). Those models have no other usable
// channel: the OpenRouter providers the gateway names in an impossible-pin
// rejection are fallbacks it does not take, so a channel preference cannot
// move the request and must not be advertised as pinning.
const privateChannel = "openai-compatible-private"

// servedPrivately reports whether the gateway answered this completion from
// its own private endpoint. Cline reports the route both as the serving
// channel and, for contributor models, as the canonical slug's namespace.
func servedPrivately(routing Routing) bool {
	if providerKey(routing.FinalProvider) == providerKey(privateChannel) {
		return true
	}
	return strings.HasPrefix(routing.CanonicalSlug, "private/")
}

// privateRouteChannel files a private route under the slug the model's channel
// list uses, so the console and the saved pin configuration name it the same
// way even when the gateway spelled the endpoint differently.
func privateRouteChannel(routing Routing, planned []string, detail map[string]model.UpstreamDetail) string {
	known := model.ModelMeta{Upstreams: planned, UpstreamDetail: detail}
	if slug := CanonicalProvider(known, routing.FinalProvider); providerKey(slug) == providerKey(privateChannel) {
		return slug
	}
	return CanonicalProvider(known, privateChannel)
}

type ValidationResult struct {
	Supported bool
	Reason    string
	Summary   map[string]int
	Results   map[string]model.UpstreamStatus
}

type OfficialResult struct {
	Sources     []string `json:"sources"`
	Found       int      `json:"found"`
	Added       []string `json:"added"`
	KnownModels []string `json:"knownModels"`
	TS          int64    `json:"ts"`
	Total       int      `json:"total"`
}

type AccountTestResult struct {
	OK    bool   `json:"ok"`
	MS    int64  `json:"ms"`
	Model string `json:"model,omitempty"`
	Note  string `json:"note,omitempty"`
	Error string `json:"error,omitempty"`
}

func New(st *store.Store) *Service {
	return &Service{
		store: st,
		client: &http.Client{
			// Every request carries its own context deadline (probe timeouts,
			// the stream head/idle guard, the non-stream budget), so the
			// transport only bounds connection setup: a hung dial or TLS
			// handshake must fail over quickly instead of eating the whole
			// per-attempt budget.
			Transport: &http.Transport{
				Proxy:               http.ProxyFromEnvironment,
				DialContext:         (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				TLSHandshakeTimeout: 15 * time.Second,
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 20,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

func (s *Service) ProbeModel(ctx context.Context, modelID string) (ProbeResult, error) {
	account := s.store.PickAccount()
	if account.Key == "" {
		return ProbeResult{}, errNoAccount
	}
	started := time.Now()
	body := map[string]any{
		"model": modelID,
		"messages": []any{
			map[string]any{"role": "user", "content": "Reply with the word OK"},
		},
		"max_tokens": 256,
		"stream":     false,
	}
	// The probe reproduces the channel preference a real request carries: its
	// result is what the console shows as 最近命中, and asking without the pin
	// made every pinned model look like a miss. Channel discovery is unaffected
	// - that comes from the impossible-provider probe below. Remember whether
	// the request carried our own restriction: a restricted plan only names
	// the allowed channel, so it cannot be used to count the model's channels.
	probeRestricted := false
	if attempts := s.BuildAttempts(modelID, s.store.ModelConfig(modelID)); len(attempts) > 0 {
		attempt := attempts[0]
		body = s.InjectPrefs(body, modelID, attempt)
		probeRestricted = attempt.Upstream != "" || len(attempt.ExcludeList) > 0
	}
	_, raw, err := s.fetchJSON(ctx, http.MethodPost, s.store.UpstreamBase()+"/chat/completions", chatHeaders(account.Key), body, 180*time.Second)
	if err != nil {
		return ProbeResult{}, err
	}
	root := jsonx.Map(raw)
	if message := extractError(root); message != "" && !hasChoices(root) {
		return ProbeResult{}, errors.New(message)
	}
	if !hasChoices(root) {
		return ProbeResult{}, errors.New("上游没有返回可用的补全结果，无法探测模型")
	}
	observedAt := time.Now().UnixMilli()
	responseMS := time.Since(started).Milliseconds()
	routing := ParseRouting(root)
	var planned []string
	var probe routingProbe
	var probeErr error
	if routing.Pipeline != "" {
		planned = parsePlannedProviders(routing.Plan)
		probe, probeErr = s.probeRoutingPreference(ctx, modelID, routing.Pipeline)
	}
	var endpoints []model.UpstreamDetail
	orSlug := ""
	if routing.Pipeline != "planner" && routing.CanonicalSlug != "" {
		endpoints, orSlug = s.orEndpoints(ctx, routing.CanonicalSlug)
	}
	previous := s.store.ModelMeta(modelID)
	detail := previous.UpstreamDetail
	if detail == nil {
		detail = map[string]model.UpstreamDetail{}
	}
	for _, endpoint := range endpoints {
		detail[endpoint.Slug] = endpoint
	}
	// A model Cline hosts itself is served by the private endpoint alone. The
	// OpenRouter names the gateway returns for an impossible pin are fallbacks
	// it never takes, so the channel list is just that endpoint: keeping the
	// rest would offer channels nobody can use, and nobody can pin to.
	privateRoute := servedPrivately(routing)
	var upstreams []string
	switch {
	case privateRoute:
		upstreams = []string{privateRouteChannel(routing, planned, detail)}
	case routing.Pipeline == "planner":
		finalSlug := CanonicalProvider(model.ModelMeta{Upstreams: planned, UpstreamDetail: detail}, routing.FinalProvider)
		upstreams = strx.Unique(append(append(append(append([]string{}, planned...), finalSlug), routing.Fallbacks...), probe.providers...))
	default:
		keys := make([]string, 0, len(detail))
		for key := range detail {
			keys = append(keys, key)
		}
		upstreams = strx.Unique(append(append(append(append([]string{}, routing.Fallbacks...), routing.FinalProvider), probe.providers...), keys...))
	}
	pinnable := false
	pinReason := ""
	// A plan that our own pin/exclusion list restricted proves nothing about
	// how many channels the model has; only an unrestricted one-entry plan is
	// authoritative. Otherwise the harvested channel list decides.
	singleProvider := len(upstreams) == 1 || (!probeRestricted && len(planned) == 1)
	switch {
	case routing.Pipeline == "":
		pinReason = pinReasonUnsupported
	// The check has to come first: a saved pin makes the probe restricted,
	// which disables the one-entry-plan shortcut above, and the gateway's
	// OpenRouter fallback list would otherwise read as "pinnable".
	case privateRoute:
		pinReason = pinReasonSingleProvider
	case routing.Pipeline == "planner" && singleProvider:
		pinReason = pinReasonSingleProvider
	case len(upstreams) == 0:
		pinReason = pinReasonNoChannels
	case probeErr != nil:
		pinReason = pinReasonProbeFailed
	case probe.supported:
		pinnable = true
	case probe.ignored:
		pinReason = pinReasonGatewayIgnores
	default:
		pinReason = pinReasonProbeFailed
	}
	var tier0 []string
	if !privateRoute {
		tier0 = strx.Unique(append(previous.Tier0, parseTier0(routing.Plan)...))
	}
	// The channel list was just (re)built; resolve the hit against it rather
	// than against whatever the previous probe knew.
	routing.FinalProvider = CanonicalProvider(model.ModelMeta{Upstreams: upstreams, UpstreamDetail: detail}, routing.FinalProvider)
	meta, err := s.store.UpdateModelMeta(modelID, func(current *model.ModelMeta) {
		current.OK = true
		current.Pipeline = routing.Pipeline
		current.Pinnable = &pinnable
		current.PinReason = pinReason
		current.CanonicalSlug = routing.CanonicalSlug
		current.OpenRouterSlug = orSlug
		current.Upstreams = upstreams
		current.LastProvider = routing.FinalProvider
		current.LastMS = time.Since(started).Milliseconds()
		current.ProbedAt = time.Now().UnixMilli()
		if privateRoute {
			// The model has one channel and no OpenRouter endpoints, so
			// whatever an earlier probe harvested for it is stale.
			current.AvailableProviders = slices.Clone(upstreams)
			current.UpstreamDetail = nil
			current.Tier0 = nil
		} else {
			current.AvailableProviders = strx.Unique(append(probe.providers, current.AvailableProviders...))
			current.UpstreamDetail = detail
			current.Tier0 = tier0
		}
		if !pinnable {
			// A previous probe may have stored a green board from when the
			// gateway still honoured pins; stale data is worse than none.
			current.UpstreamStatus = nil
			current.ValidatedAt = 0
		} else {
			current.ObserveUpstreamSuccess(routing.FinalProvider, ParseMeta(root).Attempts, observedAt, responseMS)
		}
	})
	if err != nil {
		return ProbeResult{}, err
	}
	return ProbeResult{OK: true, MS: time.Since(started).Milliseconds(), ModelMeta: meta}, nil
}

// probeRoutingPreference checks whether the upstream still honours the
// provider-routing field for the model's pipeline. A routing rejection proves
// the field is read and also yields the candidate list; a normal completion
// means the gateway silently ignored the impossible pin.
func (s *Service) probeRoutingPreference(ctx context.Context, modelID, pipeline string) (routingProbe, error) {
	account := s.store.PickAccount()
	if account.Key == "" {
		return routingProbe{}, errNoAccount
	}
	cfg := s.store.Config()
	body := map[string]any{
		"model": modelID,
		"messages": []any{
			map[string]any{"role": "user", "content": "hi"},
		},
		"max_tokens": 16,
		"stream":     false,
	}
	if pipeline == "planner" {
		body["providerOptions"] = map[string]any{"gateway": map[string]any{"only": []string{pinProbeSlug}}}
	} else {
		body["provider"] = map[string]any{"only": []string{pinProbeSlug}}
	}
	_, raw, err := s.fetchJSON(ctx, http.MethodPost, cfg.UpstreamBase+"/chat/completions", chatHeaders(account.Key), body, 60*time.Second)
	if err != nil {
		return routingProbe{}, err
	}
	root := jsonx.Map(raw)
	if hasChoices(root) {
		// The impossible provider was accepted, so the preference was ignored
		// rather than enforced.
		return routingProbe{ignored: true}, nil
	}
	message := extractError(root)
	var providers []string
	if pipeline == "planner" {
		providers = parseAvailableProviders(message)
	} else if index := strings.Index(message, "{"); index >= 0 {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(message[index:]), &parsed); err == nil {
			providers = getStringSlice(getMap(getMap(parsed, "error"), "metadata"), "available_providers")
		}
	}
	if len(providers) > 0 || routingRejectionRE.MatchString(message) {
		return routingProbe{providers: providers, supported: true}, nil
	}
	// Any other response - a 200 completion, an empty-content error, a model
	// error that is not about provider routing - means the impossible provider
	// did not trigger a routing rejection, so the preference was not enforced.
	return routingProbe{ignored: true}, nil
}

func (s *Service) orModelList(ctx context.Context) []string {
	cached, fetchedAt := s.store.ORModelList()
	if len(cached) > 0 && time.Now().UnixMilli()-fetchedAt < 6*time.Hour.Milliseconds() {
		return cached
	}
	_, raw, err := s.fetchJSON(ctx, http.MethodGet, openRouterAPI+"/models", nil, nil, 60*time.Second)
	if err != nil {
		return cached
	}
	items := getSlice(jsonx.Map(raw), "data")
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if id := getString(jsonx.Map(item), "id"); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return cached
	}
	s.updateMetadata(func(current *model.Metadata) {
		current.ORModels = ids
		current.ORModelsFetchedAt = time.Now().UnixMilli()
	})
	return ids
}

func (s *Service) orEndpoints(ctx context.Context, slug string) ([]model.UpstreamDetail, string) {
	ids := s.orModelList(ctx)
	realSlug := ""
	for _, id := range ids {
		if id == slug {
			realSlug = id
			break
		}
	}
	if realSlug == "" {
		target := normalizeSlug(slug)
		for _, id := range ids {
			if normalizeSlug(id) == target {
				realSlug = id
				break
			}
		}
	}
	if realSlug == "" {
		return nil, ""
	}
	_, raw, err := s.fetchJSON(ctx, http.MethodGet, openRouterAPI+"/models/"+realSlug+"/endpoints", nil, nil, 60*time.Second)
	if err != nil {
		return nil, realSlug
	}
	root := jsonx.Map(raw)
	endpoints := getSlice(getMap(root, "data"), "endpoints")
	detail := map[string]model.UpstreamDetail{}
	for _, item := range endpoints {
		endpoint := jsonx.Map(item)
		providerSlug := strings.SplitN(getString(endpoint, "tag"), "/", 2)[0]
		if providerSlug == "" {
			providerSlug = strings.ReplaceAll(strings.ToLower(getString(endpoint, "provider_name")), " ", "-")
		}
		current := detail[providerSlug]
		current.Slug = providerSlug
		current.Name = getString(endpoint, "provider_name")
		current.Endpoints++
		if value := formatInt(endpoint["context_length"]); value > current.Context {
			current.Context = value
		}
		if value := formatInt(endpoint["uptime_last_30m"]); value > current.Uptime {
			current.Uptime = value
		}
		detail[providerSlug] = current
	}
	result := make([]model.UpstreamDetail, 0, len(detail))
	for _, value := range detail {
		result = append(result, value)
	}
	return result, realSlug
}

func (s *Service) ValidateUpstreams(ctx context.Context, modelID string) (ValidationResult, error) {
	modelMeta := s.store.ModelMeta(modelID)
	if modelMeta.Pinnable == nil || !*modelMeta.Pinnable || len(modelMeta.Upstreams) == 0 {
		reason := modelMeta.PinReason
		if reason == "" {
			reason = pinReasonNoChannels
		}
		// Per-channel validation is meaningless when the gateway ignores the
		// pin. Clear any older all-green board instead of leaving it behind.
		if _, err := s.store.UpdateModelMeta(modelID, func(current *model.ModelMeta) {
			current.UpstreamStatus = nil
			current.ValidatedAt = 0
		}); err != nil {
			return ValidationResult{}, err
		}
		return ValidationResult{
			Supported: false,
			Reason:    reason,
			Summary:   map[string]int{},
			Results:   map[string]model.UpstreamStatus{},
		}, nil
	}
	account := s.store.PickAccount()
	if account.Key == "" {
		return ValidationResult{}, errNoAccount
	}
	cfg := s.store.Config()
	results := make(map[string]model.UpstreamStatus, len(modelMeta.Upstreams))
	var mutex sync.Mutex
	semaphore := make(chan struct{}, 5)
	var waitGroup sync.WaitGroup
	for _, upstreamSlug := range modelMeta.Upstreams {
		upstreamSlug := upstreamSlug
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}
			started := time.Now()
			body := map[string]any{
				"model": modelID,
				"messages": []any{
					map[string]any{"role": "user", "content": "hi"},
				},
				"max_tokens": 16,
			}
			if modelMeta.Pipeline == "planner" {
				body["providerOptions"] = map[string]any{"gateway": map[string]any{"only": []string{upstreamSlug}}}
			} else {
				body["provider"] = map[string]any{"only": []string{upstreamSlug}}
			}
			status := "unknown"
			note := ""
			_, raw, err := s.fetchJSON(ctx, http.MethodPost, cfg.UpstreamBase+"/chat/completions", chatHeaders(account.Key), body, 60*time.Second)
			if err != nil {
				note = err.Error()
			} else if message := extractError(jsonx.Map(raw)); message != "" && !hasChoices(jsonx.Map(raw)) {
				status = classifyUpstreamError(message)
				note = strx.Truncate(message, 160)
			} else if hasChoices(jsonx.Map(raw)) {
				status = "ok"
			}
			mutex.Lock()
			results[upstreamSlug] = model.UpstreamStatus{
				Status:    status,
				Note:      note,
				CheckedAt: time.Now().UnixMilli(),
				MS:        time.Since(started).Milliseconds(),
			}
			mutex.Unlock()
		}()
	}
	waitGroup.Wait()

	_, err := s.store.UpdateModelMeta(modelID, func(current *model.ModelMeta) {
		if current.UpstreamStatus == nil {
			current.UpstreamStatus = map[string]model.UpstreamStatus{}
		}
		for key, value := range results {
			current.UpstreamStatus[key] = value
		}
		current.ValidatedAt = time.Now().UnixMilli()
	})
	if err != nil {
		return ValidationResult{}, err
	}
	summary := map[string]int{"ok": 0, "limited": 0, "bad": 0, "auth": 0, "unknown": 0}
	for _, result := range results {
		summary[result.Status]++
	}
	return ValidationResult{Supported: true, Summary: summary, Results: results}, nil
}

// FetchOfficialModels adds Cline's own recommended cline-pass models plus every
// cline-pass model models.dev publishes. The recommended list is the freshest
// source and includes dated snapshots such as deepseek-v4.1-flash-1017;
// models.dev supplies the capability row (name, context window, reasoning
// tiers). A failed models.dev fetch is reported instead of looking like
// "nothing new"; the optional recommended-list fetch is best effort.
func (s *Service) FetchOfficialModels(ctx context.Context) (OfficialResult, error) {
	found := map[string]struct{}{}
	capabilities := map[string]model.ModelMeta{}
	var sources []string
	updatedAt := time.Now().UnixMilli()
	add := func(value string) string {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && !strings.HasPrefix(value, "cline-pass/") {
			value = "cline-pass/" + value
		}
		if !strings.HasPrefix(value, "cline-pass/") {
			return ""
		}
		found[value] = struct{}{}
		return value
	}

	// Cline's recommended list is the authoritative subscription catalog and
	// updates before community directories. It is optional so a temporary
	// outage does not hide the models.dev capability data.
	if status, raw, fetchErr := s.fetchJSON(ctx, http.MethodGet, officialRecommendedModelsURL, nil, nil, 30*time.Second); fetchErr == nil && status >= 200 && status < 300 {
		root := jsonx.Map(raw)
		foundRecommended := 0
		for _, rawModel := range getSlice(root, "clinePass") {
			item := jsonx.Map(rawModel)
			id := add(getString(item, "id"))
			if id == "" {
				continue
			}
			foundRecommended++
			capabilities[id] = mergeModelCapability(capabilities[id], recommendedModelCapability(item))
		}
		if foundRecommended > 0 {
			sources = append(sources, "cline.api")
		}
	}

	status, raw, err := s.fetchJSON(ctx, http.MethodGet, officialModelsDevURL, nil, nil, 30*time.Second)
	if err != nil {
		return OfficialResult{}, fmt.Errorf("models.dev: %w", err)
	}
	if status < 200 || status >= 300 {
		return OfficialResult{}, fmt.Errorf("models.dev: unexpected status %d", status)
	}
	root := jsonx.Map(raw)
	clinePass := getMap(getMap(root, "providers"), "cline-pass")
	if clinePass == nil {
		clinePass = getMap(root, "cline-pass")
	}
	models := getMap(clinePass, "models")
	if len(models) > 0 {
		for rawID, rawModel := range models {
			id := add(rawID)
			if id == "" {
				continue
			}
			capability := normalizeModelCapability(id, parseModelCapability(jsonx.Map(rawModel), updatedAt))
			capabilities[id] = mergeModelCapability(capabilities[id], capability)
		}
		sources = append(sources, "models.dev")
	}

	// Recommended-only entries have no models.dev row. For a dated snapshot,
	// copy the base model's capabilities and keep the snapshot suffix visible.
	for id := range found {
		if capabilities[id].CapabilitiesKnown {
			continue
		}
		base, suffix, ok := datedModelBase(id)
		if !ok {
			continue
		}
		baseCapability, exists := capabilities[base]
		if !exists || !baseCapability.CapabilitiesKnown {
			continue
		}
		capability := normalizeModelCapability(id, baseCapability)
		if capability.DisplayName != "" {
			capability.DisplayName = strings.TrimSpace(capability.DisplayName + " " + suffix)
		}
		capabilities[id] = mergeModelCapability(capabilities[id], capability)
	}

	valid := make([]string, 0, len(found))
	for id := range found {
		valid = append(valid, id)
	}
	config := s.store.Config()
	known := make(map[string]struct{}, len(config.KnownModels)+len(config.RemovedModels))
	for _, id := range config.KnownModels {
		known[id] = struct{}{}
	}
	// A model the user deleted stays deleted across syncs.
	for _, id := range config.RemovedModels {
		known[id] = struct{}{}
	}
	// Always a slice: the console reads added.length, and JSON null would
	// crash it when a sync finds nothing new.
	added := make([]string, 0, len(valid))
	for _, id := range valid {
		if _, exists := known[id]; !exists {
			added = append(added, id)
		}
	}
	if len(added) > 0 {
		if err := s.store.UpdateConfig(func(current *model.Config) {
			current.KnownModels = append(current.KnownModels, added...)
		}); err != nil {
			return OfficialResult{}, err
		}
	}
	finalConfig := s.store.Config()
	result := OfficialResult{
		Sources:     strx.Unique(sources),
		Found:       len(valid),
		Added:       added,
		KnownModels: finalConfig.KnownModels,
		TS:          time.Now().UnixMilli(),
		Total:       len(finalConfig.KnownModels),
	}
	s.updateMetadata(func(meta *model.Metadata) {
		for id, capability := range capabilities {
			if slices.Contains(finalConfig.RemovedModels, id) {
				continue
			}
			meta.Models[id] = mergeModelCapability(meta.Models[id], capability)
		}
		meta.OfficialModelsFetch = &model.OfficialFetch{
			TS:      result.TS,
			Sources: result.Sources,
			Found:   result.Found,
			Added:   result.Added,
			Total:   result.Total,
		}
	})
	return result, nil
}

// TestAccount probes one credential. The stored key may be addressed by
// account ID instead of being resubmitted, so the console never has to cache a
// revealed key to test a saved account.
func (s *Service) TestAccount(ctx context.Context, key, accountID string) AccountTestResult {
	started := time.Now()
	modelID := "cline-pass/glm-5.3-flash"
	if known := s.store.KnownModels(); len(known) > 0 {
		modelID = known[0]
	}
	if strings.TrimSpace(key) == "" && strings.TrimSpace(accountID) != "" {
		account := s.store.FindAccount(strings.TrimSpace(accountID))
		if account.Key == "" {
			return AccountTestResult{OK: false, MS: time.Since(started).Milliseconds(), Model: modelID, Error: "找不到该账号的密钥，请重新保存后再测试"}
		}
		key = account.Key
	}
	if strings.TrimSpace(key) == "" {
		return AccountTestResult{OK: false, MS: time.Since(started).Milliseconds(), Model: modelID, Error: "key required"}
	}
	body := map[string]any{
		"model": modelID,
		"messages": []any{
			map[string]any{"role": "user", "content": "Say OK"},
		},
		"max_tokens": 512,
	}
	status, raw, err := s.fetchJSON(ctx, http.MethodPost, s.store.UpstreamBase()+"/chat/completions", chatHeaders(key), body, 120*time.Second)
	if err != nil {
		return AccountTestResult{OK: false, MS: time.Since(started).Milliseconds(), Model: modelID, Error: err.Error()}
	}
	root := jsonx.Map(raw)
	details, found := apierr.FromBody(root, status)
	if found && hasChoices(root) {
		// A completed generation with a provider warning still proves the
		// credential works.
		found = false
	}
	if found {
		message := strings.TrimSpace(details.Message)
		if rawText := strings.TrimSpace(jsonx.String(root["raw"])); rawText != "" && (message == "" || message == http.StatusText(details.Status)) {
			message = strx.Truncate(rawText, 160)
		}
		if message == "" {
			message = http.StatusText(details.Status)
		}
		if message == "" {
			message = "upstream error"
		}
		authFailure := details.Status == http.StatusUnauthorized || details.Status == http.StatusForbidden ||
			details.Type == "authentication_error" || details.Type == "permission_error"
		if authFailure {
			return AccountTestResult{
				OK:    false,
				MS:    time.Since(started).Milliseconds(),
				Model: modelID,
				Error: "密钥无效或未授权：" + strx.Truncate(message, 160),
			}
		}
		return AccountTestResult{
			OK:    false,
			MS:    time.Since(started).Milliseconds(),
			Model: modelID,
			Error: fmt.Sprintf("上游返回 %d：%s", details.Status, strx.Truncate(message, 160)),
		}
	}
	if !hasChoices(root) {
		// A 2xx without a completion is not proof that the key works.
		return AccountTestResult{
			OK:    false,
			MS:    time.Since(started).Milliseconds(),
			Model: modelID,
			Error: "上游没有返回可用的补全结果，无法确认密钥状态",
		}
	}
	return AccountTestResult{OK: true, MS: time.Since(started).Milliseconds(), Model: modelID}
}

// AutoRoute reports a probed model whose gateway ignores provider pins.
// GLM-style routes stay pinnable. An unprobed model is not auto: saved pins
// are left as configured until a probe says otherwise.
func (s *Service) AutoRoute(modelID string) bool {
	meta := s.store.ModelMeta(modelID)
	return meta.Pinnable != nil && !*meta.Pinnable
}

func (s *Service) BuildAttempts(modelID string, cfg model.PerModelConfig) []Attempt {
	if s.AutoRoute(modelID) {
		return []Attempt{{}}
	}
	excluded := make(map[string]struct{}, len(cfg.Exclude))
	for _, upstreamSlug := range cfg.Exclude {
		excluded[upstreamSlug] = struct{}{}
	}
	wanted := make([]string, 0, len(cfg.Upstreams))
	for _, upstreamSlug := range cfg.Upstreams {
		if _, found := excluded[upstreamSlug]; !found {
			wanted = append(wanted, upstreamSlug)
		}
	}
	base := Attempt{ExcludeList: cfg.Exclude}
	if len(wanted) == 0 {
		return []Attempt{base}
	}
	attempts := make([]Attempt, 0, len(wanted))
	for _, upstreamSlug := range wanted {
		attempt := base
		attempt.Upstream = upstreamSlug
		attempts = append(attempts, attempt)
	}
	return attempts
}

// knownChannels is the channel universe an exclude list is translated
// against: the probed channel list when one exists, otherwise the channels the
// user pinned for the model before the first probe.
func (s *Service) knownChannels(modelID string) []string {
	if upstreams := s.store.ModelMeta(modelID).Upstreams; len(upstreams) > 0 {
		return upstreams
	}
	return s.store.ModelConfig(modelID).Upstreams
}

// RoutingFailure reports a pin/exclude combination that leaves no channel to
// send to. InjectPrefs can only express "allow exactly these channels"; when
// that allow list comes out empty an unconstrained request would silently use
// the channels the user excluded, so the attempt must fail instead.
func (s *Service) RoutingFailure(modelID string, attempt Attempt) *apierr.Details {
	if len(attempt.ExcludeList) == 0 {
		return nil
	}
	if attempt.Upstream != "" {
		// A pin names the single channel allowed to serve the request,
		// and BuildAttempts never selects an excluded channel.
		return nil
	}
	known := s.knownChannels(modelID)
	if len(known) == 0 {
		return &apierr.Details{
			Status:  http.StatusBadRequest,
			Type:    "invalid_request_error",
			Code:    "channel_list_unknown",
			Message: "尚未探测到该模型的渠道列表，无法应用排除规则；请先探测模型或清空排除项",
		}
	}
	excluded := make(map[string]struct{}, len(attempt.ExcludeList))
	for _, upstreamSlug := range attempt.ExcludeList {
		excluded[upstreamSlug] = struct{}{}
	}
	for _, upstreamSlug := range known {
		if _, found := excluded[upstreamSlug]; !found {
			return nil
		}
	}
	return &apierr.Details{
		Status:  http.StatusBadRequest,
		Type:    "invalid_request_error",
		Code:    "no_allowed_channels",
		Message: "排除规则排除了全部渠道，没有可用的上游渠道",
	}
}

func (s *Service) InjectPrefs(body map[string]any, modelID string, attempt Attempt) map[string]any {
	cloned := cloneChatRequest(body)
	modelMeta := s.store.ModelMeta(modelID)
	ensureIncludeReasoning(cloned, modelMeta)
	exclude := make([]string, 0, len(attempt.ExcludeList))
	for _, value := range attempt.ExcludeList {
		if value != attempt.Upstream {
			exclude = append(exclude, value)
		}
	}
	known := s.knownChannels(modelID)
	var allowList []string
	if len(exclude) > 0 {
		excluded := make(map[string]struct{}, len(exclude))
		for _, value := range exclude {
			excluded[value] = struct{}{}
		}
		for _, upstreamSlug := range known {
			if _, found := excluded[upstreamSlug]; !found {
				allowList = append(allowList, upstreamSlug)
			}
		}
	}
	// No exclusion rules means no constraint; an empty allow list while rules
	// exist is a conflict the attempt layer rejects before reaching here.
	if attempt.Upstream == "" && len(attempt.ExcludeList) == 0 {
		return cloned
	}
	pipeline := modelMeta.Pipeline
	useVercel := pipeline == "planner" || pipeline == ""
	useOpenRouter := pipeline == "direct" || pipeline == ""
	if useVercel {
		providerOptions := jsonx.Map(cloned["providerOptions"])
		if providerOptions == nil {
			providerOptions = map[string]any{}
		}
		gateway := jsonx.Map(providerOptions["gateway"])
		if gateway == nil {
			gateway = map[string]any{}
		}
		if attempt.Upstream != "" {
			gateway["only"] = []string{attempt.Upstream}
		} else if len(allowList) > 0 {
			gateway["only"] = allowList
		}
		providerOptions["gateway"] = gateway
		cloned["providerOptions"] = providerOptions
	}
	if useOpenRouter {
		provider := jsonx.Map(cloned["provider"])
		if provider == nil {
			provider = map[string]any{}
		}
		if attempt.Upstream != "" {
			provider["only"] = []string{attempt.Upstream}
		} else if len(allowList) > 0 {
			provider["only"] = allowList
		}
		cloned["provider"] = provider
	}
	return cloned
}

// cloneChatRequest copies the small top-level routing fields InjectPrefs may
// change without re-encoding the messages and tools. A JSON round-trip here
// was both expensive for large Codex contexts and lossy: numbers decoded with
// UseNumber became float64, which can silently round large schema values.
func cloneChatRequest(body map[string]any) map[string]any {
	cloned := maps.Clone(body)
	if cloned == nil {
		cloned = map[string]any{}
	}
	if providerOptions := jsonx.Map(body["providerOptions"]); providerOptions != nil {
		options := maps.Clone(providerOptions)
		if gateway := jsonx.Map(providerOptions["gateway"]); gateway != nil {
			options["gateway"] = maps.Clone(gateway)
		}
		cloned["providerOptions"] = options
	}
	if provider := jsonx.Map(body["provider"]); provider != nil {
		cloned["provider"] = maps.Clone(provider)
	}
	return cloned
}

func (s *Service) AttemptNonStream(ctx context.Context, modelID string, body map[string]any, attempt Attempt) AttemptResult {
	account := s.pickAccount(ctx)
	if account.Key == "" {
		details := apierr.Details{Status: http.StatusServiceUnavailable, Type: "configuration_error", Code: "no_account", Message: errNoAccount.Error()}
		return AttemptResult{Status: details.Status, Out: apierr.Body(details), NetErr: details.Message, Account: account, Fatal: true}
	}
	if details := s.RoutingFailure(modelID, attempt); details != nil {
		return AttemptResult{Status: details.Status, Out: apierr.Body(*details), NetErr: details.Message, Account: account, Fatal: true}
	}
	baseURL := s.store.UpstreamBase()
	send := s.InjectPrefs(body, modelID, attempt)
	status, raw, err := s.fetchJSON(ctx, http.MethodPost, baseURL+"/chat/completions", chatHeaders(account.Key), send, s.NonStreamTimeout())
	if err != nil {
		details := apierr.Network(err, "network_error")
		return AttemptResult{
			Status:  details.Status,
			Out:     apierr.Body(details),
			NetErr:  details.Message,
			Account: account,
		}
	}
	root := jsonx.Map(raw)
	output := responseBody(root)
	if details, found := apierr.FromBody(root, status); found && !hasChoices(root) {
		s.noteAccountStatus(account, details.Status)
		s.observeStick(ctx, account, attempt.Upstream, details.Status)
		return AttemptResult{
			Status:  details.Status,
			Out:     apierr.Body(details),
			NetErr:  details.Message,
			Routing: Routing{},
			Account: account,
			Usage:   output["usage"],
			// A pinned key has no second account to move to, so repeating the
			// same rejected credential only burns time: report it once.
			Fatal: pinBlocksFailover(ctx, details.Status),
		}
	}
	if !hasChoices(root) {
		details := apierr.Details{
			Status:  http.StatusBadGateway,
			Type:    "upstream_error",
			Code:    "invalid_upstream_response",
			Message: "upstream returned no completion choices",
		}
		return AttemptResult{
			Status:  details.Status,
			Out:     apierr.Body(details),
			NetErr:  details.Message,
			Routing: Routing{},
			Account: account,
			Usage:   output["usage"],
		}
	}
	s.noteAccountStatus(account, http.StatusOK)
	s.observeStick(ctx, account, attempt.Upstream, http.StatusOK)
	return AttemptResult{
		Status:  http.StatusOK,
		Out:     output,
		Routing: s.RoutingFor(modelID, root),
		Account: account,
		Usage:   output["usage"],
	}
}

type StreamAttemptResult struct {
	SSE        bool
	Status     int
	Header     http.Header
	Body       io.ReadCloser
	FirstChunk []byte
	Out        map[string]any
	NetErr     string
	Account    model.Account
	// Usage retains billed work even when an error body replaces the response.
	Usage any
	// Fatal marks a failure no other channel or account can fix.
	Fatal bool
}

func firstSSEPayload(raw []byte, includeTrailing bool) (string, bool) {
	var parser sse.Parser
	var payload string
	found := false
	_ = parser.Feed(raw, func(block string) bool {
		payload, found = ssePayload(block)
		return !found
	})
	if !found && includeTrailing {
		payload, found = ssePayload(parser.Finish())
	}
	return payload, found
}

func ssePayload(block string) (string, bool) {
	var data []string
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	return strings.Join(data, "\n"), len(data) > 0
}

func readSSEHead(reader io.Reader) ([]byte, string, error) {
	// The first event is part of the normal SSE event budget. Bound it by the
	// same limit the parser enforces, not by an undocumented smaller head cap.
	const maxHead = sse.MaxEventBytes
	result := make([]byte, 0, 4096)
	buffer := make([]byte, 4096)
	var parser sse.Parser
	for len(result) < maxHead {
		limit := len(buffer)
		if remaining := maxHead - len(result); remaining < limit {
			limit = remaining
		}
		count, err := reader.Read(buffer[:limit])
		if count > 0 {
			result = append(result, buffer[:count]...)
			var payload string
			found := false
			parseErr := parser.Feed(buffer[:count], func(block string) bool {
				payload, found = ssePayload(block)
				return !found
			})
			if found {
				return result, payload, nil
			}
			if parseErr != nil {
				return result, "", parseErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if payload, found := ssePayload(parser.Finish()); found {
					return result, payload, nil
				}
			}
			return result, "", err
		}
	}
	return result, "", errors.New("stream head exceeds 8 MiB before first data event")
}

func (s *Service) StartStreamAttempt(ctx context.Context, modelID string, body map[string]any, attempt Attempt) StreamAttemptResult {
	account := s.pickAccount(ctx)
	if account.Key == "" {
		details := apierr.Details{Status: http.StatusServiceUnavailable, Type: "configuration_error", Code: "no_account", Message: errNoAccount.Error()}
		return StreamAttemptResult{Status: details.Status, Out: apierr.Body(details), NetErr: details.Message, Account: account, Fatal: true}
	}
	if details := s.RoutingFailure(modelID, attempt); details != nil {
		return StreamAttemptResult{Status: details.Status, Out: apierr.Body(*details), NetErr: details.Message, Account: account, Fatal: true}
	}
	baseURL := s.store.UpstreamBase()
	send := s.InjectPrefs(body, modelID, attempt)

	// The guard owns both streaming phases: a bounded wait for the first event
	// and, once committed, a silence budget on the response body. Callers pass
	// their request context and must not layer an additional deadline on top,
	// otherwise long reasoning responses are cut off mid-stream.
	attemptContext, guard := newIdleGuard(ctx, s.streamHeadTimeout(), s.streamIdleTimeout())
	handedOff := false
	defer func() {
		if !handedOff {
			guard.release()
		}
	}()

	rawBody, err := json.Marshal(send)
	if err != nil {
		return StreamAttemptResult{Status: http.StatusBadGateway, NetErr: err.Error(), Account: account}
	}
	request, err := http.NewRequestWithContext(attemptContext, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(rawBody))
	if err != nil {
		return StreamAttemptResult{Status: http.StatusBadGateway, NetErr: err.Error(), Account: account}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+account.Key)
	request.Header.Set("User-Agent", buildinfo.UserAgent())
	response, err := s.client.Do(request)
	if err != nil {
		details := apierr.Network(guardError(guard, err), "network_error")
		return StreamAttemptResult{Status: details.Status, Out: apierr.Body(details), NetErr: details.Message, Account: account}
	}
	contentType := response.Header.Get("Content-Type")
	if response.StatusCode != http.StatusOK || !strings.Contains(strings.ToLower(contentType), "event-stream") {
		defer response.Body.Close()
		limit := int64(maxResponseBytes)
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			limit = maxErrorResponseBytes
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, limit))
		if readErr != nil {
			return StreamAttemptResult{Status: http.StatusBadGateway, NetErr: readErr.Error(), Account: account}
		}
		var parsed map[string]any
		if err := json.Unmarshal(raw, &parsed); err != nil {
			parsed = map[string]any{"error": map[string]any{"message": strx.Truncate(string(raw), 400), "type": "upstream_error"}}
		}
		if response.StatusCode == http.StatusOK && hasChoices(parsed) {
			s.noteAccountStatus(account, http.StatusOK)
			s.observeStick(ctx, account, attempt.Upstream, http.StatusOK)
			return StreamAttemptResult{
				Status:  response.StatusCode,
				Header:  response.Header.Clone(),
				Out:     parsed,
				Account: account,
				Usage:   responseBody(parsed)["usage"],
			}
		}
		details, found := apierr.FromBody(parsed, response.StatusCode)
		if !found {
			message := strings.TrimSpace(string(raw))
			if message == "" {
				message = "upstream returned a non-SSE response"
			}
			message = strx.Truncate(message, 2000)
			details = apierr.Details{Status: http.StatusBadGateway, Type: "upstream_error", Message: message}
		}
		s.noteAccountStatus(account, details.Status)
		s.observeStick(ctx, account, attempt.Upstream, details.Status)
		return StreamAttemptResult{
			Status:  details.Status,
			Header:  response.Header.Clone(),
			Out:     apierr.Body(details),
			NetErr:  details.Message,
			Account: account,
			Usage:   responseBody(parsed)["usage"],
			// See the non-streaming path: a pinned key has nothing to fail
			// over to.
			Fatal: pinBlocksFailover(ctx, details.Status),
		}
	}

	firstChunk, payload, readErr := readSSEHead(response.Body)
	if readErr != nil {
		response.Body.Close()
		details := apierr.Network(guardError(guard, readErr), "stream_error")
		return StreamAttemptResult{Status: details.Status, Out: apierr.Body(details), NetErr: details.Message, Account: account}
	}
	if payload == "" {
		response.Body.Close()
		details := apierr.Details{Status: http.StatusBadGateway, Type: "upstream_error", Code: "stream_empty", Message: "stream returned no data event"}
		return StreamAttemptResult{Status: details.Status, Out: apierr.Body(details), NetErr: details.Message, Account: account}
	}
	var firstEvent map[string]any
	if json.Unmarshal([]byte(payload), &firstEvent) == nil && extractError(firstEvent) != "" && !hasChoices(firstEvent) {
		response.Body.Close()
		details, _ := apierr.FromBody(firstEvent, 0)
		// The transport answered 200 before the error arrived, but the
		// account-level verdict is the same one the non-SSE branch records.
		s.noteAccountStatus(account, details.Status)
		s.observeStick(ctx, account, attempt.Upstream, details.Status)
		return StreamAttemptResult{
			Status: details.Status, Out: apierr.Body(details), NetErr: details.Message, Account: account,
			Usage: responseBody(firstEvent)["usage"],
			Fatal: pinBlocksFailover(ctx, details.Status),
		}
	}
	// The response is committed from here on; switch the guard to the silence
	// budget and hand body ownership (including guard release) to the caller.
	guard.Commit()
	handedOff = true
	s.noteAccountStatus(account, http.StatusOK)
	// Affinity is finalized by the caller after the stream reaches a clean
	// terminal. Committing the account/channel here would make a truncated
	// stream sticky even though the request did not succeed.
	return StreamAttemptResult{
		SSE:        true,
		Status:     http.StatusOK,
		Header:     response.Header.Clone(),
		Body:       &idleStreamBody{ReadCloser: response.Body, guard: guard},
		FirstChunk: firstChunk,
		Account:    account,
	}
}

func (s *Service) LearnFailure(modelID string, attempt Attempt, message string) {
	if attempt.Upstream != "" {
		status := classifyUpstreamError(message)
		// Authentication failures belong to the account (see accounts.go),
		// not to the provider channel; marking the channel would make the
		// UI blame the wrong thing.
		if status == "unknown" || status == "auth" {
			return
		}
		s.updateModelMeta(modelID, func(current *model.ModelMeta) {
			if current.UpstreamStatus == nil {
				current.UpstreamStatus = map[string]model.UpstreamStatus{}
			}
			current.UpstreamStatus[attempt.Upstream] = model.UpstreamStatus{
				Status:    status,
				Note:      strx.Truncate(message, 160),
				CheckedAt: time.Now().UnixMilli(),
			}
		})
		return
	}
	if len(attempt.ExcludeList) == 0 {
		return
	}
	providers := parseAvailableProviders(message)
	if len(providers) == 0 {
		return
	}
	s.updateModelMeta(modelID, func(current *model.ModelMeta) {
		current.Upstreams = strx.Unique(append(current.Upstreams, providers...))
	})
}

func chatHeaders(key string) map[string]string {
	return map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + key,
	}
}

func ErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, errNoAccount) {
		return err.Error()
	}
	return fmt.Sprint(err)
}
