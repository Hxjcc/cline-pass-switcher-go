package httpapi

import (
	"strconv"
	"strings"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/strx"
)

// accountView is the wire shape of one account. The stored key stays out of it
// unless the caller explicitly asked to reveal it, so the console, its cached
// snapshot and anything that logs a response never carry secrets by default.
type accountView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Key        string `json:"key"`
	KeyPreview string `json:"keyPreview"`
	HasKey     bool   `json:"hasKey"`
	Enabled    bool   `json:"enabled"`
}

func accountViews(accounts []model.Account, reveal bool) []accountView {
	views := make([]accountView, 0, len(accounts))
	for _, account := range accounts {
		view := accountView{
			ID:         account.ID,
			Name:       account.Name,
			KeyPreview: keyPreview(account.Key),
			HasKey:     account.Key != "",
			Enabled:    account.Enabled,
		}
		if reveal {
			view.Key = account.Key
		}
		views = append(views, view)
	}
	return views
}

// keyPreview keeps a short hint so two stored keys can be told apart without
// the API handing out the key itself.
func keyPreview(key string) string {
	runes := []rune(strings.TrimSpace(key))
	switch {
	case len(runes) == 0:
		return ""
	case len(runes) <= 12:
		return strings.Repeat("*", len(runes))
	default:
		return string(runes[:6]) + "…" + string(runes[len(runes)-4:])
	}
}

// mergeAccounts rebuilds the account list from a save request. An account that
// is still identified (by id, or by name for clients written before ids
// existed) but carries no key keeps the stored one; every other entry needs a
// key of its own or it is dropped.
func mergeAccounts(existing, incoming []model.Account) []model.Account {
	byID := make(map[string]model.Account, len(existing))
	byName := make(map[string]model.Account, len(existing))
	for _, account := range existing {
		if account.ID != "" {
			byID[account.ID] = account
		}
		if account.Name != "" {
			if _, found := byName[account.Name]; !found {
				byName[account.Name] = account
			}
		}
	}
	used := make(map[string]struct{}, len(incoming))
	merged := make([]model.Account, 0, len(incoming))
	for index, account := range incoming {
		account.Name = strx.Truncate(strings.TrimSpace(account.Name), 50)
		if account.Name == "" {
			account.Name = "账号" + strconv.Itoa(index+1)
		}
		account.Key = strings.TrimSpace(account.Key)
		account.ID = strings.TrimSpace(account.ID)

		var match model.Account
		found := false
		switch {
		case account.ID != "":
			if _, taken := used[account.ID]; !taken {
				match, found = byID[account.ID]
			}
		default:
			if previous, ok := byName[account.Name]; ok {
				if _, taken := used[previous.ID]; !taken {
					match, found = previous, true
				}
			}
		}
		if found {
			used[match.ID] = struct{}{}
		}
		if account.Key == "" {
			if !found || match.Key == "" {
				continue
			}
			account.Key = match.Key
		}
		if found {
			account.ID = match.ID
		} else {
			// Normalization assigns the identity for a brand new account.
			account.ID = ""
		}
		merged = append(merged, account)
	}
	return merged
}
