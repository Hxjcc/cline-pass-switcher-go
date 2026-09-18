package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	responsesbridge "github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/responses"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/store"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/upstream"
)

type Server struct {
	store    *store.Store
	upstream *upstream.Service
	assets   fs.FS
	file     http.Handler
	index    []byte
	shares   *streamShareHub
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
		shares:   newStreamShareHub(),
	}, nil
}

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	writer.Header().Set("Access-Control-Allow-Headers", "*")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
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
	if s.isProtected(path) && !s.authOK(request) {
		writeJSON(writer, http.StatusUnauthorized, map[string]any{
			"error": map[string]any{"message": "unauthorized: 代理密钥缺失或错误", "type": "auth_error"},
		})
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
		s.handleGetAccounts(writer)
	case request.Method == http.MethodPost && path == "/api/accounts":
		s.handleSaveAccounts(writer, request)
	case request.Method == http.MethodPost && path == "/api/accounts/test":
		s.handleTestAccount(writer, request)
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
		s.handleHistory(writer)
	case request.Method == http.MethodPost && path == "/api/history/clear":
		s.handleClearHistory(writer)
	case request.Method == http.MethodGet && path == "/api/config":
		s.handleGetConfig(writer)
	case request.Method == http.MethodPost && path == "/api/config":
		s.handleSaveConfig(writer, request)
	case request.Method == http.MethodGet && (path == "/v1/models" || path == "/api/v1/models" || path == "/models"):
		s.handleListModels(writer, request)
	case request.Method == http.MethodPost && isChatPath(path):
		s.handleChat(writer, request)
	case request.Method == http.MethodPost && isResponsesPath(path):
		s.handleResponses(writer, request)
	case request.Method == http.MethodPost && isResponsesCompactPath(path):
		s.handleResponsesCompact(writer, request)
	default:
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
		var modelMeta any
		if value, found := meta.Models[id]; found {
			modelMeta = value
		}
		subscription = append(subscription, map[string]any{
			"id":     id,
			"config": cfg.PerModel[id],
			"meta":   modelMeta,
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

func (s *Server) handleGetAccounts(writer http.ResponseWriter) {
	cfg := s.store.Config()
	meta := s.store.Metadata()
	writeJSON(writer, http.StatusOK, map[string]any{
		"accounts": cfg.Accounts,
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
	accounts := make([]model.Account, 0, len(body.Accounts))
	for index, account := range body.Accounts {
		account.Name = truncate(strings.TrimSpace(account.Name), 50)
		if account.Name == "" {
			account.Name = "账号" + strconv.Itoa(index+1)
		}
		account.Key = strings.TrimSpace(account.Key)
		if account.Key != "" {
			accounts = append(accounts, account)
		}
	}
	if len(accounts) == 0 {
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"message": "至少需要一个有效账号（key 非空）"},
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
	}
	if err := readJSON(request, &body); err != nil {
		writeRequestError(writer, err)
		return
	}
	body.Key = strings.TrimSpace(body.Key)
	if body.Key == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "key required"}})
		return
	}
	writeJSON(writer, http.StatusOK, s.upstream.TestAccount(request.Context(), body.Key))
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
		"ok": true, "summary": result.Summary, "results": result.Results, "upstreams": meta.Upstreams,
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

func (s *Server) handleHistory(writer http.ResponseWriter) {
	writeJSON(writer, http.StatusOK, map[string]any{"history": s.store.Metadata().History})
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
	ids = uniqueStrings(ids)
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
			"ok": false, "error": truncate(message, 400), "targets": modelConfig.Upstreams, "exclude": modelConfig.Exclude, "trace": result.Trace,
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
		Attempts:  traceUpstreams(result.Trace),
		Trace:     result.Trace,
	}
	applyChatStats(&entry, result.Out, entry.MS)
	_ = s.store.Record(entry)
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "ms": time.Since(started).Milliseconds(), "targets": modelConfig.Upstreams,
		"exclude": modelConfig.Exclude, "actual": routing.FinalProvider, "actualName": routing.FinalProviderName,
		"pipeline": routing.Pipeline, "pinnable": routing.Pipeline != "", "canonicalSlug": routing.CanonicalSlug,
		"fallbacks": routing.Fallbacks, "content": truncate(routing.Content, 120), "account": result.Account.Name,
		"trace": result.Trace,
	})
}

func (s *Server) runNonStreamChain(ctx context.Context, modelID string, body map[string]any, modelConfig model.PerModelConfig, timeout time.Duration) chainResult {
	attempts := s.upstream.BuildAttempts(modelID, modelConfig)
	result := chainResult{Status: http.StatusBadGateway, Started: time.Now()}
	for _, attempt := range attempts {
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
			Note:     truncate(note, 160),
		})
		result.Status = response.Status
		result.Out = response.Out
		result.Routing = response.Routing
		result.Account = response.Account
		result.NetErr = response.NetErr
		if response.Status != http.StatusOK {
			s.upstream.LearnFailure(modelID, attempt, extractAttemptError(response.Out))
			if s.stopFailover(response.Status) {
				break
			}
			continue
		}
		break
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
		Attempts:  traceUpstreams(result.Trace),
		Trace:     result.Trace,
	}
	if result.Status == http.StatusOK {
		applyChatStats(&entry, result.Out, entry.MS)
	}
	_ = s.store.Record(entry)

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
		isChatPath(path) || isResponsesPath(path) || isResponsesCompactPath(path)
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

func (s *Server) publicProxyBase(cfg model.Config) string {
	if cfg.PublicBaseURL != "" {
		return strings.TrimRight(cfg.PublicBaseURL, "/") + "/v1"
	}
	return "http://127.0.0.1:" + strconv.Itoa(cfg.Port) + "/v1"
}

func readJSON(request *http.Request, target any) error {
	defer request.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(request.Body, (50<<20)+1))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request body must contain a single JSON value")
	}
	return nil
}

func writeRequestError(writer http.ResponseWriter, err error) {
	writeJSON(writer, http.StatusBadRequest, map[string]any{
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

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
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
	}
	return result
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
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
	result := truncate(strings.TrimSpace(builder.String()), 80)
	if result == "" {
		return "-"
	}
	return result
}

func isChatPath(path string) bool {
	switch path {
	case "/chat/completions", "/v1/chat/completions", "/api/v1/chat/completions":
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

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
