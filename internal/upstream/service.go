package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/apierr"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/store"
)

const openRouterAPI = "https://openrouter.ai/api/v1"

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
}

type ProbeResult struct {
	OK bool  `json:"ok"`
	MS int64 `json:"ms"`
	model.ModelMeta
}

type ValidationResult struct {
	Summary map[string]int
	Results map[string]model.UpstreamStatus
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
	cfg := s.store.Config()
	started := time.Now()
	body := map[string]any{
		"model": modelID,
		"messages": []any{
			map[string]any{"role": "user", "content": "Reply with the word OK"},
		},
		"max_tokens": 256,
	}
	_, raw, err := s.fetchJSON(ctx, http.MethodPost, cfg.UpstreamBase+"/chat/completions", chatHeaders(account.Key), body, 180*time.Second)
	if err != nil {
		return ProbeResult{}, err
	}
	root := asMap(raw)
	if message := extractError(root); message != "" && !hasChoices(root) {
		return ProbeResult{}, errors.New(message)
	}
	routing := ParseRouting(root)
	var harvested []string
	if routing.Pipeline != "" {
		harvested = s.harvestAvailableProviders(ctx, modelID, routing.Pipeline)
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
	var upstreams []string
	if routing.Pipeline == "planner" {
		upstreams = unique(append(append([]string{}, harvested...), routing.Fallbacks...))
	} else {
		keys := make([]string, 0, len(detail))
		for key := range detail {
			keys = append(keys, key)
		}
		upstreams = unique(append(append(routing.Fallbacks, harvested...), keys...))
	}
	tier0 := unique(append(previous.Tier0, parseTier0(routing.Plan)...))
	// The channel list was just (re)built; resolve the hit against it rather
	// than against whatever the previous probe knew.
	routing.FinalProvider = CanonicalProvider(model.ModelMeta{Upstreams: upstreams, UpstreamDetail: detail}, routing.FinalProvider)
	meta, err := s.store.UpdateModelMeta(modelID, func(current *model.ModelMeta) {
		current.OK = true
		current.Pipeline = routing.Pipeline
		current.Pinnable = routing.Pipeline != ""
		current.AvailableProviders = unique(append(harvested, current.AvailableProviders...))
		current.CanonicalSlug = routing.CanonicalSlug
		current.OpenRouterSlug = orSlug
		current.UpstreamDetail = detail
		current.Upstreams = upstreams
		current.Tier0 = tier0
		current.LastProvider = routing.FinalProvider
		current.LastMS = time.Since(started).Milliseconds()
		current.ProbedAt = time.Now().UnixMilli()
	})
	if err != nil {
		return ProbeResult{}, err
	}
	return ProbeResult{OK: true, MS: time.Since(started).Milliseconds(), ModelMeta: meta}, nil
}

func (s *Service) harvestAvailableProviders(ctx context.Context, modelID, pipeline string) []string {
	account := s.store.PickAccount()
	if account.Key == "" {
		return nil
	}
	cfg := s.store.Config()
	base := map[string]any{
		"model": modelID,
		"messages": []any{
			map[string]any{"role": "user", "content": "hi"},
		},
		"max_tokens": 16,
	}
	if pipeline == "planner" {
		base["providerOptions"] = map[string]any{"gateway": map[string]any{"only": []string{"__probe__"}}}
	} else {
		base["provider"] = map[string]any{"only": []string{"__probe__"}}
	}
	_, raw, err := s.fetchJSON(ctx, http.MethodPost, cfg.UpstreamBase+"/chat/completions", chatHeaders(account.Key), base, 60*time.Second)
	if err != nil {
		return nil
	}
	message := extractError(asMap(raw))
	if pipeline == "planner" {
		return parseAvailableProviders(message)
	}
	index := strings.Index(message, "{")
	if index < 0 {
		return nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(message[index:]), &parsed); err != nil {
		return nil
	}
	return getStringSlice(getMap(getMap(parsed, "error"), "metadata"), "available_providers")
}

func (s *Service) orModelList(ctx context.Context) []string {
	meta := s.store.Metadata()
	if len(meta.ORModels) > 0 && time.Now().UnixMilli()-meta.ORModelsFetchedAt < 6*time.Hour.Milliseconds() {
		return meta.ORModels
	}
	_, raw, err := s.fetchJSON(ctx, http.MethodGet, openRouterAPI+"/models", nil, nil, 60*time.Second)
	if err != nil {
		return meta.ORModels
	}
	items := getSlice(asMap(raw), "data")
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if id := getString(asMap(item), "id"); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return meta.ORModels
	}
	_ = s.store.UpdateMetadata(func(current *model.Metadata) {
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
	root := asMap(raw)
	endpoints := getSlice(getMap(root, "data"), "endpoints")
	detail := map[string]model.UpstreamDetail{}
	for _, item := range endpoints {
		endpoint := asMap(item)
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
	meta := s.store.Metadata()
	modelMeta := meta.Models[modelID]
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
			} else if message := extractError(asMap(raw)); message != "" && !hasChoices(asMap(raw)) {
				status = classifyUpstreamError(message)
				note = truncate(message, 160)
			} else if hasChoices(asMap(raw)) {
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
	return ValidationResult{Summary: summary, Results: results}, nil
}

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

	if _, raw, err := s.fetchJSON(ctx, http.MethodGet, "https://api.cline.bot/api/v1/ai/cline/recommended-models", nil, nil, 30*time.Second); err == nil {
		root := asMap(raw)
		list := getSlice(root, "clinePass")
		if len(list) == 0 {
			list = getSlice(getMap(root, "data"), "clinePass")
		}
		if len(list) > 0 {
			for _, item := range list {
				if id, ok := item.(string); ok {
					add(id)
				} else {
					entry := asMap(item)
					id := add(getString(entry, "id"))
					if id != "" {
						capability := capabilities[id]
						if name := getString(entry, "name"); name != "" {
							capability.DisplayName = name
						}
						if description := getString(entry, "description"); description != "" {
							capability.Description = description
						}
						capabilities[id] = capability
					}
				}
			}
			sources = append(sources, "cline.api")
		}
	}

	if _, raw, err := s.fetchJSON(ctx, http.MethodGet, "https://models.dev/api.json", nil, nil, 30*time.Second); err == nil {
		root := asMap(raw)
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
				capability := normalizeModelCapability(id, parseModelCapability(asMap(rawModel), updatedAt))
				capabilities[id] = mergeModelCapability(capabilities[id], capability)
			}
			sources = append(sources, "models.dev")
		}
	}

	if text, err := s.fetchText(ctx, "https://docs.cline.bot/getting-started/clinepass", 30*time.Second); err == nil {
		matches := regexp.MustCompile(`(?i)cline-pass/[a-z0-9._-]+`).FindAllString(text, -1)
		if len(matches) > 0 {
			for _, match := range matches {
				found[strings.ToLower(match)] = struct{}{}
			}
			sources = append(sources, "docs.cline.bot")
		}
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
	var added []string
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
		Sources:     unique(sources),
		Found:       len(valid),
		Added:       added,
		KnownModels: finalConfig.KnownModels,
		TS:          time.Now().UnixMilli(),
		Total:       len(finalConfig.KnownModels),
	}
	_ = s.store.UpdateMetadata(func(meta *model.Metadata) {
		for id, capability := range capabilities {
			if containsID(finalConfig.RemovedModels, id) {
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

func (s *Service) Catalog(ctx context.Context) []string {
	meta := s.store.Metadata()
	if len(meta.Catalog) > 0 && time.Now().UnixMilli()-meta.CatalogFetchedAt < time.Hour.Milliseconds() {
		return meta.Catalog
	}
	account := s.store.PickAccount()
	if account.Key == "" {
		return meta.Catalog
	}
	cfg := s.store.Config()
	_, raw, err := s.fetchJSON(ctx, http.MethodGet, cfg.UpstreamBase+"/models", chatHeaders(account.Key), nil, 60*time.Second)
	if err != nil {
		return meta.Catalog
	}
	items := getSlice(asMap(raw), "data")
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if id := getString(asMap(item), "id"); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return meta.Catalog
	}
	_ = s.store.UpdateMetadata(func(current *model.Metadata) {
		current.Catalog = ids
		current.CatalogFetchedAt = time.Now().UnixMilli()
	})
	return ids
}

func (s *Service) TestAccount(ctx context.Context, key string) AccountTestResult {
	started := time.Now()
	modelID := "cline-pass/glm-5.3-flash"
	cfg := s.store.Config()
	if len(cfg.KnownModels) > 0 {
		modelID = cfg.KnownModels[0]
	}
	body := map[string]any{
		"model": modelID,
		"messages": []any{
			map[string]any{"role": "user", "content": "Say OK"},
		},
		"max_tokens": 512,
	}
	_, raw, err := s.fetchJSON(ctx, http.MethodPost, cfg.UpstreamBase+"/chat/completions", chatHeaders(key), body, 120*time.Second)
	if err != nil {
		return AccountTestResult{OK: false, MS: time.Since(started).Milliseconds(), Model: modelID, Error: err.Error()}
	}
	message := extractError(asMap(raw))
	if message != "" && !hasChoices(asMap(raw)) {
		authFail := regexp.MustCompile(`(?i)unauthorized|re-authenticate|invalid\s*api|401`).MatchString(message)
		if authFail {
			return AccountTestResult{
				OK:    false,
				MS:    time.Since(started).Milliseconds(),
				Model: modelID,
				Error: "密钥无效或未授权：" + truncate(message, 160),
			}
		}
		return AccountTestResult{
			OK:    true,
			MS:    time.Since(started).Milliseconds(),
			Model: modelID,
			Note:  "密钥鉴权通过；网关提示：" + truncate(message, 120),
		}
	}
	return AccountTestResult{OK: true, MS: time.Since(started).Milliseconds(), Model: modelID}
}

func (s *Service) BuildAttempts(modelID string, cfg model.PerModelConfig) []Attempt {
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
	strict := cfg.PinMode != "preferred"
	sortMode := ""
	if cfg.Sort != nil {
		sortMode = *cfg.Sort
	}
	base := Attempt{ExcludeList: cfg.Exclude, Strict: strict, Sort: sortMode}
	if len(wanted) == 0 {
		return []Attempt{base}
	}
	attempts := make([]Attempt, 0, len(wanted))
	for index, upstreamSlug := range wanted {
		attempt := base
		attempt.Upstream = upstreamSlug
		if !strict {
			for otherIndex, other := range wanted {
				if otherIndex != index {
					attempt.OrderRest = append(attempt.OrderRest, other)
				}
			}
		}
		attempts = append(attempts, attempt)
	}
	return attempts
}

func (s *Service) InjectPrefs(body map[string]any, modelID string, attempt Attempt) map[string]any {
	cloned := model.Clone(body)
	modelMeta := s.store.ModelMeta(modelID)
	ensureIncludeReasoning(cloned, modelMeta)
	exclude := make([]string, 0, len(attempt.ExcludeList))
	for _, value := range attempt.ExcludeList {
		if value != attempt.Upstream {
			exclude = append(exclude, value)
		}
	}
	known := modelMeta.Upstreams
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
	if attempt.Upstream == "" && attempt.Sort == "" && len(allowList) == 0 {
		return cloned
	}
	pipeline := modelMeta.Pipeline
	useVercel := pipeline == "planner" || pipeline == ""
	useOpenRouter := pipeline == "direct" || pipeline == ""
	if useVercel {
		providerOptions := asMap(cloned["providerOptions"])
		if providerOptions == nil {
			providerOptions = map[string]any{}
		}
		gateway := asMap(providerOptions["gateway"])
		if gateway == nil {
			gateway = map[string]any{}
		}
		if attempt.Upstream != "" {
			if attempt.Strict {
				gateway["only"] = []string{attempt.Upstream}
			} else {
				gateway["order"] = append([]string{attempt.Upstream}, attempt.OrderRest...)
				if len(allowList) > 0 {
					gateway["only"] = allowList
				}
			}
		} else if len(allowList) > 0 {
			gateway["only"] = allowList
		}
		if attempt.Sort != "" {
			gateway["sort"] = attempt.Sort
		}
		providerOptions["gateway"] = gateway
		cloned["providerOptions"] = providerOptions
	}
	if useOpenRouter {
		provider := asMap(cloned["provider"])
		if provider == nil {
			provider = map[string]any{}
		}
		if attempt.Upstream != "" {
			if attempt.Strict {
				provider["only"] = []string{attempt.Upstream}
			} else {
				provider["order"] = append([]string{attempt.Upstream}, attempt.OrderRest...)
				if len(allowList) > 0 {
					provider["only"] = allowList
				}
			}
		} else if len(allowList) > 0 {
			provider["only"] = allowList
		}
		if attempt.Sort != "" {
			sortValue := attempt.Sort
			switch attempt.Sort {
			case "cost":
				sortValue = "price"
			case "ttft":
				sortValue = "latency"
			case "tps":
				sortValue = "throughput"
			}
			provider["sort"] = sortValue
		}
		cloned["provider"] = provider
	}
	return cloned
}

func (s *Service) AttemptNonStream(ctx context.Context, modelID string, body map[string]any, attempt Attempt) AttemptResult {
	account := s.pickAccount()
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
	root := asMap(raw)
	if details, found := apierr.FromBody(root, status); found && !hasChoices(root) {
		s.noteAccountStatus(account, details.Status)
		return AttemptResult{
			Status:  details.Status,
			Out:     apierr.Body(details),
			NetErr:  details.Message,
			Routing: Routing{},
			Account: account,
		}
	}
	s.noteAccountStatus(account, http.StatusOK)
	output := responseBody(root)
	return AttemptResult{
		Status:  http.StatusOK,
		Out:     output,
		Routing: s.RoutingFor(modelID, root),
		Account: account,
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
}

func firstSSEPayload(raw []byte, includeTrailing bool) (string, bool) {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	blocks := strings.Split(text, "\n\n")
	limit := len(blocks)
	if !includeTrailing && !strings.HasSuffix(text, "\n\n") {
		limit--
	}
	for index := 0; index < limit; index++ {
		var data []string
		for _, line := range strings.Split(blocks[index], "\n") {
			if strings.HasPrefix(line, "data:") {
				data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if len(data) > 0 {
			return strings.Join(data, "\n"), true
		}
	}
	return "", false
}

func readSSEHead(reader io.Reader) ([]byte, string, error) {
	const maxHead = 32 << 10
	result := make([]byte, 0, 4096)
	buffer := make([]byte, 4096)
	for len(result) < maxHead {
		limit := len(buffer)
		if remaining := maxHead - len(result); remaining < limit {
			limit = remaining
		}
		count, err := reader.Read(buffer[:limit])
		if count > 0 {
			result = append(result, buffer[:count]...)
			if payload, found := firstSSEPayload(result, false); found {
				return result, payload, nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if payload, found := firstSSEPayload(result, true); found {
					return result, payload, nil
				}
			}
			return result, "", err
		}
	}
	return result, "", errors.New("stream head exceeds 32 KiB before first data event")
}

func (s *Service) StartStreamAttempt(ctx context.Context, modelID string, body map[string]any, attempt Attempt) StreamAttemptResult {
	account := s.pickAccount()
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
	request, err := http.NewRequestWithContext(attemptContext, http.MethodPost, baseURL+"/chat/completions", strings.NewReader(string(rawBody)))
	if err != nil {
		return StreamAttemptResult{Status: http.StatusBadGateway, NetErr: err.Error(), Account: account}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+account.Key)
	request.Header.Set("User-Agent", "cline-pass-switcher-go/1.0")
	response, err := s.client.Do(request)
	if err != nil {
		details := apierr.Network(guardError(guard, err), "network_error")
		return StreamAttemptResult{Status: details.Status, Out: apierr.Body(details), NetErr: details.Message, Account: account}
	}
	contentType := response.Header.Get("Content-Type")
	if response.StatusCode != http.StatusOK || !strings.Contains(strings.ToLower(contentType), "event-stream") {
		defer response.Body.Close()
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
		if readErr != nil {
			return StreamAttemptResult{Status: http.StatusBadGateway, NetErr: readErr.Error(), Account: account}
		}
		var parsed map[string]any
		if err := json.Unmarshal(raw, &parsed); err != nil {
			parsed = map[string]any{"error": map[string]any{"message": truncate(string(raw), 400), "type": "upstream_error"}}
		}
		if response.StatusCode == http.StatusOK && hasChoices(parsed) {
			s.noteAccountStatus(account, http.StatusOK)
			return StreamAttemptResult{
				Status:  response.StatusCode,
				Header:  response.Header.Clone(),
				Out:     parsed,
				Account: account,
			}
		}
		details, found := apierr.FromBody(parsed, response.StatusCode)
		if !found {
			message := strings.TrimSpace(string(raw))
			if message == "" {
				message = "upstream returned a non-SSE response"
			}
			details = apierr.Details{Status: http.StatusBadGateway, Type: "upstream_error", Message: message}
		}
		s.noteAccountStatus(account, details.Status)
		return StreamAttemptResult{
			Status:  details.Status,
			Header:  response.Header.Clone(),
			Out:     apierr.Body(details),
			NetErr:  details.Message,
			Account: account,
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
		return StreamAttemptResult{Status: details.Status, Out: apierr.Body(details), NetErr: details.Message, Account: account}
	}
	// The response is committed from here on; switch the guard to the silence
	// budget and hand body ownership (including guard release) to the caller.
	guard.Commit()
	handedOff = true
	s.noteAccountStatus(account, http.StatusOK)
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
		_, _ = s.store.UpdateModelMeta(modelID, func(current *model.ModelMeta) {
			if current.UpstreamStatus == nil {
				current.UpstreamStatus = map[string]model.UpstreamStatus{}
			}
			current.UpstreamStatus[attempt.Upstream] = model.UpstreamStatus{
				Status:    status,
				Note:      truncate(message, 160),
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
	_, _ = s.store.UpdateModelMeta(modelID, func(current *model.ModelMeta) {
		current.Upstreams = unique(append(current.Upstreams, providers...))
	})
}

func chatHeaders(key string) map[string]string {
	return map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + key,
	}
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
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
