package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	responsesbridge "github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/responses"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/store"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/strx"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/upstream"
)

type Server struct {
	store    *store.Store
	upstream *upstream.Service
	assets   fs.FS
	file     http.Handler
	index    []byte
	csp      string
	shares   *streamShareHub
	throttle *authThrottle
}

type chainResult struct {
	Status  int
	Out     map[string]any
	Routing upstream.Routing
	Account model.Account
	Trace   []model.Trace
	NetErr  string
	Started time.Time
}

func New(st *store.Store, service *upstream.Service, assets fs.FS) (*Server, error) {
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return nil, fmt.Errorf("read embedded index.html: %w", err)
	}
	return &Server{
		store:    st,
		upstream: service,
		assets:   assets,
		file:     http.FileServer(http.FS(assets)),
		index:    index,
		csp:      contentSecurityPolicy(index),
		shares:   newStreamShareHub(),
		throttle: newAuthThrottle(),
	}, nil
}

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Cache-Control", "no-store")
	// The console keeps the proxy key in browser storage, so a script injected
	// into the page could read it straight out. These headers keep the page on
	// its own origin and stop it being framed.
	writer.Header().Set("Content-Security-Policy", s.csp)
	writer.Header().Set("X-Frame-Options", "DENY")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	writer.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	if !s.browserRequestAllowed(request) {
		writeJSON(writer, http.StatusForbidden, map[string]any{"error": map[string]any{"message": "untrusted request origin or host; non-local access requires PROXY_KEY", "type": "access_error"}})
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	if request.Method == http.MethodOptions {
		writer.WriteHeader(http.StatusNoContent)
		return
	}

	path := request.URL.Path
	if request.Method == http.MethodGet && path == "/healthz" {
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if request.Method == http.MethodGet && path == "/api/meta" {
		cfg := s.store.Config()
		writeJSON(writer, http.StatusOK, map[string]any{
			"authRequired": cfg.ProxyKey != "",
			"proxyBase":    s.publicProxyBase(cfg),
			"configured":   s.store.IsConfigured(),
		})
		return
	}
	if s.isProtected(path) && !s.authorizeProtected(writer, request) {
		return
	}

	switch {
	case request.Method == http.MethodGet && path == "/api/models":
		s.handleModels(writer, request)
	case request.Method == http.MethodPost && path == "/api/probe":
		s.handleProbe(writer, request)
	case request.Method == http.MethodPost && path == "/api/test":
		s.handleTest(writer, request)
	case request.Method == http.MethodGet && path == "/api/accounts":
		s.handleGetAccounts(writer, request)
	case request.Method == http.MethodPost && path == "/api/accounts":
		s.handleSaveAccounts(writer, request)
	case request.Method == http.MethodPost && path == "/api/accounts/test":
		s.handleTestAccount(writer, request)
	case request.Method == http.MethodGet && path == "/api/accounts/quota":
		s.handleAccountQuota(writer, request)
	case request.Method == http.MethodGet && path == "/api/security":
		s.handleGetSecurity(writer)
	case request.Method == http.MethodPost && path == "/api/security":
		s.handleSaveSecurity(writer, request)
	case request.Method == http.MethodPost && path == "/api/validate-upstreams":
		s.handleValidate(writer, request)
	case request.Method == http.MethodPost && path == "/api/fetch-official-models":
		s.handleFetchOfficial(writer, request)
	case request.Method == http.MethodPost && path == "/api/models/remove":
		s.handleRemoveModel(writer, request)
	case request.Method == http.MethodGet && path == "/api/history":
		s.handleHistory(writer, request)
	case request.Method == http.MethodPost && path == "/api/history/clear":
		s.handleClearHistory(writer)
	case request.Method == http.MethodGet && path == "/api/config":
		s.handleGetConfig(writer)
	case request.Method == http.MethodPost && path == "/api/config":
		s.handleSaveConfig(writer, request)
	case request.Method == http.MethodGet && isModelsPath(path):
		s.handleListModels(writer, request)
	case request.Method == http.MethodPost && isChatPath(path):
		s.handleChat(writer, request)
	case request.Method == http.MethodPost && isResponsesPath(path):
		s.handleResponses(writer, request)
	case request.Method == http.MethodPost && isResponsesCompactPath(path):
		s.handleResponsesCompact(writer, request)
	default:
		if path == "/api" || path == "/v1" || strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/v1/") {
			writeJSON(writer, http.StatusNotFound, map[string]any{"error": map[string]any{"message": fmt.Sprintf("no route: %s %s", request.Method, path)}})
			return
		}
		if request.Method == http.MethodGet || request.Method == http.MethodHead {
			s.serveStatic(writer, request)
			return
		}
		writeJSON(writer, http.StatusNotFound, map[string]any{
			"error": map[string]any{"message": fmt.Sprintf("no route: %s %s", request.Method, path)},
		})
	}
}

func (s *Server) handleModels(writer http.ResponseWriter, request *http.Request) {
	catalog := s.upstream.Catalog(request.Context())
	cfg := s.store.Config()
	meta := s.store.Metadata()
	subscription := make([]map[string]any, 0, len(cfg.KnownModels))
	for _, id := range cfg.KnownModels {
		subscription = append(subscription, map[string]any{
			"id":     id,
			"config": modelConfigView(cfg.PerModel[id]),
			"meta":   meta.Models[id],
		})
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"subscription":  subscription,
		"catalogCount":  len(catalog),
		"catalog":       catalog,
		"proxyBase":     s.publicProxyBase(cfg),
		"officialFetch": meta.OfficialModelsFetch,
	})
}

func (s *Server) handleProbe(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Model string `json:"model"`
	}
	if err := readJSON(request, &body); err != nil {
		writeRequestError(writer, err)
		return
	}
	if strings.TrimSpace(body.Model) == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "model required"})
		return
	}
	result, err := s.upstream.ProbeModel(request.Context(), body.Model)
	if err != nil {
		writeJSON(writer, http.StatusBadGateway, map[string]any{"ok": false, "error": upstream.ErrorMessage(err)})
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) handleGetAccounts(writer http.ResponseWriter, request *http.Request) {
	cfg := s.store.Config()
	meta := s.store.Metadata()
	// Stored keys are opt-in: only an explicit reveal returns them.
	reveal := request.URL.Query().Get("reveal") == "1"
	writeJSON(writer, http.StatusOK, map[string]any{
		"accounts": accountViews(cfg.Accounts, reveal),
		"mode":     cfg.AccountMode,
		"active":   cfg.ActiveAccount,
		"stats":    meta.Stats,
	})
}

func (s *Server) handleSaveAccounts(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Accounts []model.Account `json:"accounts"`
		Mode     string          `json:"mode"`
		Active   int             `json:"active"`
	}
	if err := readJSON(request, &body); err != nil {
		writeRequestError(writer, err)
		return
	}
	accounts := mergeAccounts(s.store.Config().Accounts, body.Accounts)
	if len(accounts) == 0 {
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"message": "至少需要一个有效账号（新账号必须填写 key）"},
		})
		return
	}
	if body.Mode != "roundrobin" {
		body.Mode = "single"
	}
	if body.Active < 0 {
		body.Active = 0
	}
	if body.Active >= len(accounts) {
		body.Active = len(accounts) - 1
	}
	if err := s.store.UpdateConfig(func(cfg *model.Config) {
		cfg.Accounts = accounts
		cfg.AccountMode = body.Mode
		cfg.ActiveAccount = body.Active
	}); err != nil {
		writeInternalError(writer, err)
		return
	}
	s.store.ResetRoundRobin()
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "accounts": len(accounts), "mode": body.Mode, "active": body.Active,
	})
}

func (s *Server) handleTestAccount(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Key string `json:"key"`
		// ID tests the stored credential without revealing it, so the console
		// cannot test a stale cached copy after the key changed.
		ID string `json:"id"`
	}
	if err := readJSON(request, &body); err != nil {
		writeRequestError(writer, err)
		return
	}
	body.Key = strings.TrimSpace(body.Key)
	body.ID = strings.TrimSpace(body.ID)
	if body.Key == "" && body.ID == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "key or account id required"}})
		return
	}
	writeJSON(writer, http.StatusOK, s.upstream.TestAccount(request.Context(), body.Key, body.ID))
}

// handleAccountQuota reports the plan utilization of the configured accounts.
// The upstream endpoints are read-only and never consume inference quota, and
// the result only feeds the console: retries and routing ignore it.
func (s *Server) handleAccountQuota(writer http.ResponseWriter, request *http.Request) {
	refresh := request.URL.Query().Get("refresh") == "1"
	ids := make([]string, 0, 1)
	if id := strings.TrimSpace(request.URL.Query().Get("id")); id != "" {
		ids = append(ids, id)
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"accounts": s.upstream.ProbeQuotas(request.Context(), ids, refresh),
	})
}

func (s *Server) handleGetSecurity(writer http.ResponseWriter) {
	cfg := s.store.Config()
	writeJSON(writer, http.StatusOK, map[string]any{
		"proxyKey":      cfg.ProxyKey,
		"publicBaseUrl": cfg.PublicBaseURL,
		"authRequired":  cfg.ProxyKey != "",
		"exposeCatalog": cfg.ExposeCatalog,
	})
}

func (s *Server) handleSaveSecurity(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		ProxyKey      *string `json:"proxyKey"`
		PublicBaseURL *string `json:"publicBaseUrl"`
		ExposeCatalog *bool   `json:"exposeCatalog"`
	}
	if err := readJSON(request, &body); err != nil {
		writeRequestError(writer, err)
		return
	}
	if err := s.store.UpdateConfig(func(cfg *model.Config) {
		if body.ProxyKey != nil {
			cfg.ProxyKey = strings.TrimSpace(*body.ProxyKey)
		}
		if body.PublicBaseURL != nil {
			cfg.PublicBaseURL = strings.TrimRight(strings.TrimSpace(*body.PublicBaseURL), "/")
		}
		if body.ExposeCatalog != nil {
			cfg.ExposeCatalog = *body.ExposeCatalog
		}
	}); err != nil {
		writeInternalError(writer, err)
		return
	}
	cfg := s.store.Config()
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok":            true,
		"proxyKey":      cfg.ProxyKey,
		"publicBaseUrl": cfg.PublicBaseURL,
		"authRequired":  cfg.ProxyKey != "",
		"proxyBase":     s.publicProxyBase(cfg),
		"exposeCatalog": cfg.ExposeCatalog,
	})
}

func (s *Server) handleValidate(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Model string `json:"model"`
	}
	if err := readJSON(request, &body); err != nil {
		writeRequestError(writer, err)
		return
	}
	if body.Model == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "model required"}})
		return
	}
	result, err := s.upstream.ValidateUpstreams(request.Context(), body.Model)
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	meta := s.store.ModelMeta(body.Model)
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "supported": result.Supported, "reason": result.Reason,
		"summary": result.Summary, "results": result.Results, "upstreams": meta.Upstreams,
	})
}

func (s *Server) handleFetchOfficial(writer http.ResponseWriter, request *http.Request) {
	result, err := s.upstream.FetchOfficialModels(request.Context())
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "sources": result.Sources, "found": result.Found, "added": result.Added, "knownModels": result.KnownModels, "ts": result.TS, "total": result.Total})
}

func (s *Server) handleRemoveModel(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Model string `json:"model"`
	}
	if err := readJSON(request, &body); err != nil {
		writeRequestError(writer, err)
		return
	}
	modelID := strings.TrimSpace(body.Model)
	if modelID == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "model required"}})
		return
	}
	if err := s.store.RemoveModel(modelID); err != nil {
		writeInternalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "total": len(s.store.Config().KnownModels)})
}

const (
	defaultHistoryPage = 50
	maxHistoryPage     = 200
)

// handleHistory serves the newest-first request log with paging and filtering.
// The store keeps model.HistoryLimit entries; the console only asks for what it
// shows, so a long log does not turn every refresh into a full dump.
func (s *Server) handleHistory(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	entries := filterHistory(s.store.Metadata().History, strings.TrimSpace(query.Get("q")), query.Get("result"))
	limit := clampQueryInt(query.Get("limit"), defaultHistoryPage, 1, maxHistoryPage)
	offset := clampQueryInt(query.Get("offset"), 0, 0, len(entries))
	end := min(offset+limit, len(entries))
	page := entries[offset:end]
	if page == nil {
		page = []model.HistoryEntry{}
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"history": page,
		"total":   len(entries),
		"offset":  offset,
		"limit":   limit,
		"hasMore": end < len(entries),
	})
}

// filterHistory matches the free-text term against the fields the table shows.
// "result" accepts "error" or "ok" to split failures from successes.
func filterHistory(entries []model.HistoryEntry, term, result string) []model.HistoryEntry {
	onlyErrors := result == "error"
	onlyOK := result == "ok"
	needle := strings.ToLower(term)
	if needle == "" && !onlyErrors && !onlyOK {
		return entries
	}
	filtered := make([]model.HistoryEntry, 0, len(entries))
	for _, entry := range entries {
		if onlyErrors && entry.Error == nil {
			continue
		}
		if onlyOK && entry.Error != nil {
			continue
		}
		if needle != "" && !historyMatches(entry, needle) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func historyMatches(entry model.HistoryEntry, needle string) bool {
	fields := []string{entry.Model, entry.Provider, entry.Canonical, entry.Account, entry.Kind, entry.Effort}
	if entry.Error != nil {
		fields = append(fields, *entry.Error)
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), needle) {
			return true
		}
	}
	return false
}

func clampQueryInt(raw string, fallback, low, high int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func (s *Server) handleClearHistory(writer http.ResponseWriter) {
	if err := s.store.ClearHistory(); err != nil {
		writeInternalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleGetConfig(writer http.ResponseWriter) {
	cfg := s.store.Config()
	writeJSON(writer, http.StatusOK, map[string]any{
		"port": cfg.Port, "perModel": cfg.PerModel, "knownModels": cfg.KnownModels,
	})
}

func (s *Server) handleSaveConfig(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		PerModel map[string]model.PerModelConfig `json:"perModel"`
	}
	if err := readJSON(request, &body); err != nil {
		writeRequestError(writer, err)
		return
	}
	if err := s.store.UpdateConfig(func(cfg *model.Config) {
		for modelID, value := range body.PerModel {
			value.Upstreams = normalizeList(value.Upstreams)
			value.Exclude = normalizeList(value.Exclude)
			excluded := make(map[string]struct{}, len(value.Exclude))
			for _, item := range value.Exclude {
				excluded[item] = struct{}{}
			}
			filtered := value.Upstreams[:0]
			for _, item := range value.Upstreams {
				if _, found := excluded[item]; !found {
					filtered = append(filtered, item)
				}
			}
			value.Upstreams = filtered
			value.Upstream = ""
			if len(value.Upstreams) > 0 {
				value.Upstream = value.Upstreams[0]
			}
			if value.PinMode != "preferred" {
				value.PinMode = "strict"
			}
			if value.Sort != nil {
				switch *value.Sort {
				case "cost", "ttft", "tps":
				default:
					value.Sort = nil
				}
			}
			cfg.PerModel[modelID] = value
		}
	}); err != nil {
		writeInternalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleListModels(writer http.ResponseWriter, request *http.Request) {
	cfg := s.store.Config()
	ids := append([]string{}, cfg.KnownModels...)
	for modelID := range cfg.PerModel {
		ids = append(ids, modelID)
	}
	if cfg.ExposeCatalog {
		ids = append(ids, s.upstream.Catalog(request.Context())...)
	}
	ids = strx.UniqueTrimmed(ids)
	data := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		data = append(data, map[string]any{"id": id, "object": "model"})
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"object": "list",
		"data":   data,
		// Current Codex clients additionally decode a provider-specific
		// {"models": [...]} catalog. An empty list keeps model refresh valid
		// while allowing custom Cline IDs to use Codex's fallback metadata.
		"models": []any{},
	})
}

func (s *Server) handleTest(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Model     string    `json:"model"`
		Upstream  *string   `json:"upstream"`
		Upstreams *[]string `json:"upstreams"`
		Exclude   *[]string `json:"exclude"`
	}
	if err := readJSON(request, &body); err != nil {
		writeRequestError(writer, err)
		return
	}
	if body.Model == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "model required"})
		return
	}
	cfg := s.store.Config()
	modelConfig := cfg.PerModel[body.Model]
	if body.Upstreams != nil {
		modelConfig.Upstreams = normalizeList(*body.Upstreams)
	} else if body.Upstream != nil {
		if *body.Upstream == "" {
			modelConfig.Upstreams = []string{}
		} else {
			modelConfig.Upstreams = []string{*body.Upstream}
		}
	}
	if body.Exclude != nil {
		modelConfig.Exclude = normalizeList(*body.Exclude)
	}
	started := time.Now()
	chatBody := map[string]any{
		"model": body.Model,
		"messages": []any{
			map[string]any{"role": "user", "content": "Reply with the word OK"},
		},
		"max_tokens": 256,
	}
	result := s.runNonStreamChain(request.Context(), body.Model, chatBody, modelConfig, 180*time.Second)
	if result.Status != http.StatusOK {
		message := chainErrorMessage(result)
		writeJSON(writer, http.StatusOK, map[string]any{
			"ok": false, "error": strx.Truncate(message, 400), "targets": modelConfig.Upstreams, "exclude": modelConfig.Exclude, "trace": result.Trace,
		})
		return
	}
	routing := s.upstream.RoutingFor(body.Model, result.Out)
	entry := model.HistoryEntry{
		TS:        time.Now().UnixMilli(),
		Model:     body.Model,
		Provider:  routing.FinalProvider,
		Canonical: routing.CanonicalSlug,
		MS:        time.Since(started).Milliseconds(),
		Stream:    false,
		Kind:      "test",
		Effort:    effortFromChatBody(chatBody),
		Account:   result.Account.Name,
		AccountID: result.Account.ID,
		Attempts:  traceUpstreams(result.Trace),
		Trace:     result.Trace,
	}
	applyChatStats(&entry, result.Out, entry.MS)
	s.record(entry)
	modelMeta := s.store.ModelMeta(body.Model)
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "ms": time.Since(started).Milliseconds(), "targets": modelConfig.Upstreams,
		"exclude": modelConfig.Exclude, "actual": routing.FinalProvider, "actualName": routing.FinalProviderName,
		"pipeline": routing.Pipeline, "pinnable": modelMeta.Pinnable != nil && *modelMeta.Pinnable, "canonicalSlug": routing.CanonicalSlug,
		"fallbacks": routing.Fallbacks, "content": strx.Truncate(routing.Content, 120), "account": result.Account.Name,
		"trace": result.Trace,
	})
}

func (s *Server) runNonStreamChain(ctx context.Context, modelID string, body map[string]any, modelConfig model.PerModelConfig, timeout time.Duration) chainResult {
	attempts := s.upstream.BuildAttempts(modelID, modelConfig)
	result := chainResult{Status: http.StatusBadGateway, Started: time.Now()}
	budget := newAttemptBudget(len(attempts), s.upstream.AccountAttemptLimit())
	for _, attempt := range attempts {
		succeeded := false
		fatal := false
		// Account failures are retried against the same channel: an HTTP 401
		// is a property of the key, not of the pinned provider, and with a
		// single channel the old loop had no way to reach a healthy account.
		for accountsUsed := 1; ; accountsUsed++ {
			if !budget.acquire() {
				return result
			}
			started := time.Now()
			attemptContext, cancel := context.WithTimeout(ctx, timeout)
			response := s.upstream.AttemptNonStream(attemptContext, modelID, body, attempt)
			cancel()
			note := response.NetErr
			if note == "" && response.Status != http.StatusOK {
				note = extractAttemptError(response.Out)
			}
			if note == "" && response.Status == http.StatusOK {
				note = "ok"
			}
			result.Trace = append(result.Trace, model.Trace{
				Upstream: attempt.Upstream,
				Status:   response.Status,
				MS:       time.Since(started).Milliseconds(),
				Note:     strx.Truncate(note, 160),
			})
			result.Status = response.Status
			result.Out = response.Out
			result.Routing = response.Routing
			result.Account = response.Account
			result.NetErr = response.NetErr
			if response.Status == http.StatusOK {
				succeeded = true
				break
			}
			s.upstream.LearnFailure(modelID, attempt, extractAttemptError(response.Out))
			if response.Fatal {
				fatal = true
				break
			}
			if ctx.Err() != nil || !s.accountRetryAllowed(response.Status, accountsUsed, budget) {
				break
			}
		}
		if succeeded {
			break
		}
		if fatal || ctx.Err() != nil || s.stopFailover(result.Status) {
			break
		}
	}
	return result
}

// stopFailover reports whether walking the remaining upstream channels is
// pointless. A 401/403 is about the account key, not the provider: it is only
// worth another attempt when a different account can be picked for it.
func (s *Server) stopFailover(status int) bool {
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		return false
	}
	return !s.upstream.AccountFailoverAvailable()
}

// maxChainAttempts bounds the total number of upstream calls one client
// request may make. Channel failover multiplies with per-channel account
// failover, so without a ceiling a large pool could amplify one request.
const maxChainAttempts = 16

// attemptBudget holds the remaining upstream calls for one client request.
type attemptBudget struct {
	remaining int
}

func newAttemptBudget(channels, accountAttempts int) *attemptBudget {
	if channels < 1 {
		channels = 1
	}
	if accountAttempts < 1 {
		accountAttempts = 1
	}
	limit := channels * accountAttempts
	if limit > maxChainAttempts {
		limit = maxChainAttempts
	}
	return &attemptBudget{remaining: limit}
}

func (b *attemptBudget) acquire() bool {
	if b == nil || b.remaining <= 0 {
		return false
	}
	b.remaining--
	return true
}

// accountRetryAllowed reports whether the failed status should be retried on
// the same channel with another account. The failed account is already cooling
// down, so pickAccount returns a different one while a healthy account is left.
func (s *Server) accountRetryAllowed(status, accountsUsed int, budget *attemptBudget) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
	default:
		return false
	}
	if accountsUsed >= s.upstream.AccountAttemptLimit() || budget == nil || budget.remaining <= 0 {
		return false
	}
	return s.upstream.AccountFailoverAvailable()
}

func (s *Server) handleChat(writer http.ResponseWriter, request *http.Request) {
	var body map[string]any
	if err := readJSON(request, &body); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "invalid JSON body"}})
		return
	}
	modelID, _ := body["model"].(string)
	if strings.TrimSpace(modelID) == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "model is required"}})
		return
	}
	modelConfig := s.store.ModelConfig(modelID)
	stream, _ := body["stream"].(bool)
	if stream {
		s.handleStreamingChat(writer, request, modelID, body, modelConfig)
		return
	}
	result := s.runNonStreamChain(request.Context(), modelID, body, modelConfig, s.upstream.NonStreamTimeout())
	if result.Out == nil {
		writeJSON(writer, http.StatusBadGateway, map[string]any{"error": map[string]any{"message": "no upstream response", "type": "upstream_error"}})
		return
	}
	errorMessage := (*string)(nil)
	if result.Status != http.StatusOK {
		message := chainErrorMessage(result)
		errorMessage = &message
	}
	entry := model.HistoryEntry{
		TS:        time.Now().UnixMilli(),
		Model:     modelID,
		Provider:  result.Routing.FinalProvider,
		Canonical: result.Routing.CanonicalSlug,
		MS:        time.Since(result.Started).Milliseconds(),
		Stream:    false,
		Kind:      "chat",
		Effort:    effortFromChatBody(body),
		Error:     errorMessage,
		Account:   result.Account.Name,
		AccountID: result.Account.ID,
		Attempts:  traceUpstreams(result.Trace),
		Trace:     result.Trace,
	}
	if result.Status == http.StatusOK {
		applyChatStats(&entry, result.Out, entry.MS)
	}
	s.record(entry)

	targets := attemptTargets(s.upstream.BuildAttempts(modelID, modelConfig))
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("X-Cline-Target-Upstream", targetHeader(targets))
	writer.Header().Set("X-Cline-Actual-Upstream", firstNonEmpty(result.Routing.FinalProvider, "unknown"))
	writer.Header().Set("X-Cline-Canonical-Model", result.Routing.CanonicalSlug)
	writer.Header().Set("X-Cline-Attempts", strconv.Itoa(len(result.Trace)))
	writer.Header().Set("X-Cline-Account", headerSafe(result.Account.Name))
	writer.WriteHeader(result.Status)
	if result.Status == http.StatusOK && result.Out != nil {
		responsesbridge.AliasChatReasoning(result.Out)
	}
	_ = json.NewEncoder(writer).Encode(result.Out)
}

func (s *Server) serveStatic(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/")
	if strings.HasPrefix(path, "assets/") {
		writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		writer.Header().Set("Cache-Control", "no-cache")
	}
	if path == "" {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.WriteHeader(http.StatusOK)
		if request.Method != http.MethodHead {
			_, _ = writer.Write(s.index)
		}
		return
	}
	if info, err := fs.Stat(s.assets, path); err == nil && !info.IsDir() {
		s.file.ServeHTTP(writer, request)
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = writer.Write(s.index)
	}
}

func (s *Server) isProtected(path string) bool {
	return strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/v1/") ||
		isModelsPath(path) || isChatPath(path) || isResponsesPath(path) || isResponsesCompactPath(path)
}

func (s *Server) authOK(request *http.Request) bool {
	expected := s.store.ProxyKey()
	if expected == "" {
		return true
	}
	header := request.Header.Get("Authorization")
	if len(header) >= 7 && strings.EqualFold(header[:7], "Bearer ") {
		header = strings.TrimSpace(header[7:])
	} else {
		header = ""
	}
	admin := strings.TrimSpace(request.Header.Get("X-Admin-Key"))
	return constantTimeEqual(header, expected) || constantTimeEqual(admin, expected)
}

// authorizeProtected gates every protected route and writes the refusal itself.
// A source address that has exhausted its attempts is refused before the key is
// compared at all, so guessing cannot continue at full speed; addresses that
// present the right key clear their counter.
func (s *Server) authorizeProtected(writer http.ResponseWriter, request *http.Request) bool {
	client := clientKey(request.RemoteAddr)
	if delay, blocked := s.throttle.blocked(client); blocked {
		writeThrottled(writer, delay)
		return false
	}
	if s.authOK(request) {
		s.throttle.succeed(client)
		return true
	}
	if delay := s.throttle.fail(client); delay > 0 {
		writeThrottled(writer, delay)
		return false
	}
	writeJSON(writer, http.StatusUnauthorized, map[string]any{
		"error": map[string]any{"message": "unauthorized: 代理密钥缺失或错误", "type": "auth_error"},
	})
	return false
}

func writeThrottled(writer http.ResponseWriter, delay time.Duration) {
	if delay < time.Second {
		delay = time.Second
	}
	seconds := int((delay + time.Second - 1) / time.Second)
	writer.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeJSON(writer, http.StatusTooManyRequests, map[string]any{
		"error": map[string]any{
			"message": fmt.Sprintf("代理密钥尝试次数过多，请在 %d 秒后重试", seconds),
			"type":    "rate_limit_error",
			"code":    "auth_throttled",
		},
	})
}

func (s *Server) publicProxyBase(cfg model.Config) string {
	if cfg.PublicBaseURL != "" {
		return strings.TrimRight(cfg.PublicBaseURL, "/") + "/v1"
	}
	return "http://127.0.0.1:" + strconv.Itoa(cfg.Port) + "/v1"
}

// A model without saved routing preferences still reports its lists as empty
// arrays: JSON null would make every consumer branch on two shapes.
func modelConfigView(config model.PerModelConfig) model.PerModelConfig {
	if config.Upstreams == nil {
		config.Upstreams = []string{}
	}
	if config.Exclude == nil {
		config.Exclude = []string{}
	}
	return config
}

const maxRequestBytes = 50 << 20

func readJSON(request *http.Request, target any) error {
	defer request.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(nil, request.Body, maxRequestBytes))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return err
		}
		return errors.New("request body must contain a single JSON value")
	}
	return nil
}

func writeRequestError(writer http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		status = http.StatusRequestEntityTooLarge
	}
	writeJSON(writer, status, map[string]any{
		"error": map[string]any{"message": err.Error()},
	})
}

func writeInternalError(writer http.ResponseWriter, err error) {
	writeJSON(writer, http.StatusInternalServerError, map[string]any{
		"error": map[string]any{"message": err.Error()},
	})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func normalizeList(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, found := seen[value]; found {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
		if len(result) == 10 {
			break
		}
	}
	return result
}

func traceUpstreams(trace []model.Trace) []string {
	result := make([]string, 0, len(trace))
	for _, item := range trace {
		if item.Upstream == "" {
			result = append(result, "auto")
		} else {
			result = append(result, item.Upstream)
		}
	}
	return result
}

func attemptTargets(attempts []upstream.Attempt) []string {
	result := make([]string, 0, len(attempts))
	for _, attempt := range attempts {
		if attempt.Upstream != "" {
			result = append(result, attempt.Upstream)
		}
	}
	return result
}

func targetHeader(targets []string) string {
	if len(targets) == 0 {
		return "auto"
	}
	return strings.Join(targets, ">")
}

func chainErrorMessage(result chainResult) string {
	if result.NetErr != "" {
		return result.NetErr
	}
	return extractAttemptError(result.Out)
}

func extractAttemptError(value map[string]any) string {
	if value == nil {
		return "upstream error"
	}
	raw, found := value["error"]
	if !found {
		return "upstream error"
	}
	switch typed := raw.(type) {
	case string:
		return typed
	case map[string]any:
		message, _ := typed["message"].(string)
		if message == "" {
			message = "upstream error"
		}
		prefix, _ := typed["type"].(string)
		if prefix == "" {
			if code := fmt.Sprint(typed["code"]); code != "" && code != "<nil>" {
				prefix = code
			}
		}
		if prefix != "" {
			return prefix + ": " + message
		}
		return message
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(encoded)
	}
}

func constantTimeEqual(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func headerSafe(value string) string {
	var builder strings.Builder
	for _, character := range value {
		if character >= 0x20 && character <= 0x7e {
			builder.WriteRune(character)
		}
	}
	result := strx.Truncate(strings.TrimSpace(builder.String()), 80)
	if result == "" {
		return "-"
	}
	return result
}

var inlineScriptRE = regexp.MustCompile(`(?is)<script(\s[^>]*)?>(.*?)</script>`)

// contentSecurityPolicy locks the console to its own origin. The two inline
// boot scripts in index.html (pre-paint theme, saved DOM snapshot) are allowed
// by SHA-256 of their exact text, computed from the embedded file at startup:
// editing the HTML updates the policy instead of silently breaking the page.
// An external <script src> is covered by 'self' and is not hashed.
func contentSecurityPolicy(index []byte) string {
	hashes := make([]string, 0, 2)
	for _, match := range inlineScriptRE.FindAllSubmatch(index, -1) {
		if strings.Contains(strings.ToLower(string(match[1])), "src=") {
			continue
		}
		sum := sha256.Sum256(match[2])
		hashes = append(hashes, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	script := "'self'"
	if len(hashes) > 0 {
		script += " " + strings.Join(hashes, " ")
	}
	return strings.Join([]string{
		"default-src 'self'",
		"script-src " + script,
		// Tailwind and React both set style attributes, so the style policy
		// cannot be tightened to hashes without rewriting the components.
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob:",
		"font-src 'self' data:",
		"connect-src 'self'",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}, "; ")
}

func isChatPath(path string) bool {
	switch path {
	case "/chat/completions", "/v1/chat/completions", "/api/v1/chat/completions":
		return true
	default:
		return false
	}
}

// The model list is served under three prefixes for client compatibility. The
// bare alias is named here instead of relying on a path prefix: /models does
// not start with /api/ or /v1/, so a prefix rule would leave it reachable
// without the proxy key while /v1/models stayed protected.
func isModelsPath(path string) bool {
	switch path {
	case "/models", "/v1/models", "/api/v1/models":
		return true
	default:
		return false
	}
}

func isResponsesPath(path string) bool {
	switch path {
	case "/responses", "/v1/responses", "/api/v1/responses":
		return true
	default:
		return false
	}
}

func isResponsesCompactPath(path string) bool {
	switch path {
	case "/responses/compact", "/v1/responses/compact", "/api/v1/responses/compact":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
