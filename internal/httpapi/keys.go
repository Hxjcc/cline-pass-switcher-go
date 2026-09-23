package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

func nowMillis() int64 { return time.Now().UnixMilli() }

func itoa(value int) string { return strconv.Itoa(value) }

// proxyKeyView is one issued client key as the console sees it. The secret is
// only included when the caller explicitly asked to reveal it, mirroring the
// account list.
type proxyKeyView struct {
	ID            string  `json:"id"`
	Name          string  `json:"name,omitempty"`
	Key           string  `json:"key,omitempty"`
	KeyPreview    string  `json:"keyPreview,omitempty"`
	HasKey        bool    `json:"hasKey"`
	Enabled       bool    `json:"enabled"`
	AccountID     string  `json:"accountId,omitempty"`
	SpendLimitUSD float64 `json:"spendLimitUsd,omitempty"`
	Note          string  `json:"note,omitempty"`
	CreatedAt     int64   `json:"createdAt,omitempty"`
	Requests      int64   `json:"requests"`
	SpentUSD      float64 `json:"spentUsd"`
	LastUsed      int64   `json:"lastUsed,omitempty"`
	// PinnedAccount names the bound account for the console, so a row does not
	// have to join the account list to explain itself.
	PinnedAccount string `json:"pinnedAccount,omitempty"`
}

func proxyKeyViews(grants []model.ProxyKeyGrant, usage map[string]model.KeyUsage, reveal bool, accountName func(string) string) []proxyKeyView {
	views := make([]proxyKeyView, 0, len(grants))
	for _, grant := range grants {
		entry := usage[grant.ID]
		view := proxyKeyView{
			ID:            grant.ID,
			Name:          grant.Name,
			KeyPreview:    keyPreview(grant.Key),
			HasKey:        grant.Key != "",
			Enabled:       grant.Enabled,
			AccountID:     grant.AccountID,
			SpendLimitUSD: grant.SpendLimitUSD,
			Note:          grant.Note,
			CreatedAt:     grant.CreatedAt,
			Requests:      entry.Requests,
			SpentUSD:      entry.SpentUSD(),
			LastUsed:      entry.LastUsed,
		}
		if reveal {
			view.Key = grant.Key
		}
		if grant.AccountID != "" && accountName != nil {
			view.PinnedAccount = accountName(grant.AccountID)
		}
		views = append(views, view)
	}
	return views
}

func (s *Server) handleGetKeys(writer http.ResponseWriter, request *http.Request) {
	reveal := request.URL.Query().Get("reveal") == "1"
	accounts := s.store.Accounts()
	nameByID := make(map[string]string, len(accounts))
	for _, account := range accounts {
		nameByID[account.ID] = account.Name
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"keys":    proxyKeyViews(s.store.ProxyKeys(), s.store.KeyUsage(), reveal, func(id string) string { return nameByID[id] }),
		"maxKeys": maxProxyKeys,
	})
}

// maxProxyKeys bounds the list so a runaway console cannot grow the config
// document without limit.
const maxProxyKeys = 100

func (s *Server) handleSaveKeys(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Keys []proxyKeyInput `json:"keys"`
	}
	if err := readJSON(request, &body); err != nil {
		writeRequestError(writer, err)
		return
	}
	if len(body.Keys) > maxProxyKeys {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"error": map[string]any{
			"message": "代理密钥数量超出上限", "type": "invalid_request_error"}})
		return
	}
	current := s.store.ProxyKeys()
	stored := make(map[string]model.ProxyKeyGrant, len(current))
	for _, grant := range current {
		stored[grant.ID] = grant
	}
	accounts := make(map[string]struct{})
	for _, account := range s.store.Accounts() {
		accounts[account.ID] = struct{}{}
	}

	grants := make([]model.ProxyKeyGrant, 0, len(body.Keys))
	for index, raw := range body.Keys {
		grant := model.ProxyKeyGrant{
			ID:            strings.TrimSpace(raw.ID),
			Name:          strings.TrimSpace(raw.Name),
			Enabled:       true,
			AccountID:     strings.TrimSpace(raw.AccountID),
			SpendLimitUSD: raw.SpendLimitUSD,
			Note:          strings.TrimSpace(raw.Note),
			CreatedAt:     raw.CreatedAt,
		}
		if raw.Enabled != nil {
			grant.Enabled = *raw.Enabled
		}
		if existing, found := stored[grant.ID]; found {
			if grant.CreatedAt == 0 {
				grant.CreatedAt = existing.CreatedAt
			}
		} else {
			grant.ID = ""
		}
		// An empty key with a known id means "keep the stored secret", which is
		// what the console sends when the row was not edited.
		grant.Key = strings.TrimSpace(raw.Key)
		if grant.Key == "" {
			if existing, found := stored[strings.TrimSpace(raw.ID)]; found {
				grant.Key = existing.Key
			}
		}
		if grant.ID == "" {
			grant.ID = model.NewProxyKeyID()
		}
		if grant.CreatedAt == 0 {
			grant.CreatedAt = nowMillis()
		}
		if grant.Key == "" {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": map[string]any{
				"message": "第 " + itoa(index+1) + " 行缺少密钥", "type": "invalid_request_error"}})
			return
		}
		if grant.SpendLimitUSD < 0 {
			grant.SpendLimitUSD = 0
		}
		if grant.AccountID != "" {
			if _, found := accounts[grant.AccountID]; !found {
				writeJSON(writer, http.StatusBadRequest, map[string]any{"error": map[string]any{
					"message": "第 " + itoa(index+1) + " 行绑定的账号不存在", "type": "invalid_request_error"}})
				return
			}
		}
		grants = append(grants, grant)
	}

	if err := s.store.UpdateConfig(func(cfg *model.Config) {
		cfg.ProxyKeys = grants
	}); err != nil {
		writeInternalError(writer, err)
		return
	}
	// Counters follow the grants that still exist; a deleted row stops
	// consuming metadata.
	live := make(map[string]struct{}, len(grants))
	for _, grant := range grants {
		live[grant.ID] = struct{}{}
	}
	if err := s.store.PruneKeyUsage(live); err != nil {
		writeInternalError(writer, err)
		return
	}

	accountsByID := make(map[string]string)
	for _, account := range s.store.Accounts() {
		accountsByID[account.ID] = account.Name
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"keys": proxyKeyViews(s.store.ProxyKeys(), s.store.KeyUsage(), false,
			func(id string) string { return accountsByID[id] }),
		"maxKeys": maxProxyKeys,
	})
}

// handleResetKeyUsage clears the accumulated spend of one key (or all of them
// with {"all": true}) so a limit can be re-opened without rotating the secret.
func (s *Server) handleResetKeyUsage(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		ID  string `json:"id"`
		All bool   `json:"all"`
	}
	if err := readJSON(request, &body); err != nil {
		writeRequestError(writer, err)
		return
	}
	id := strings.TrimSpace(body.ID)
	if !body.All && id == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"error": map[string]any{
			"message": "缺少密钥 id", "type": "invalid_request_error"}})
		return
	}
	if err := s.store.ResetKeyUsage(id, body.All); err != nil {
		writeInternalError(writer, err)
		return
	}
	accountsByID := make(map[string]string)
	for _, account := range s.store.Accounts() {
		accountsByID[account.ID] = account.Name
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true,
		"keys": proxyKeyViews(s.store.ProxyKeys(), s.store.KeyUsage(), false,
			func(keyAccountID string) string { return accountsByID[keyAccountID] }),
	})
}

// proxyKeyInput is the wire shape the console saves. Enabled is a pointer so a
// body written by an older client cannot silently disable every key.
type proxyKeyInput struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Key           string  `json:"key"`
	Enabled       *bool   `json:"enabled"`
	AccountID     string  `json:"accountId"`
	SpendLimitUSD float64 `json:"spendLimitUsd"`
	Note          string  `json:"note"`
	CreatedAt     int64   `json:"createdAt"`
}
