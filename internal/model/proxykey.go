package model

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"
)

// ProxyKeyGrant is one client key that can be handed to somebody else. A grant
// may be pinned to a single upstream account and capped at a spend limit, so a
// key that leaves this machine cannot drain the whole account pool.
type ProxyKeyGrant struct {
	ID string `json:"id"`
	// Name is what the console shows; the key itself stays masked.
	Name    string `json:"name,omitempty"`
	Key     string `json:"key"`
	Enabled bool   `json:"enabled"`
	// AccountID pins the grant to one account pool entry. Empty means the key
	// may use any usable account, exactly like the master proxy key.
	AccountID string `json:"accountId,omitempty"`
	// SpendLimitUSD caps the upstream-reported spend this key may accumulate.
	// Zero means unlimited.
	SpendLimitUSD float64 `json:"spendLimitUsd,omitempty"`
	Note          string  `json:"note,omitempty"`
	CreatedAt     int64   `json:"createdAt,omitempty"`
}

func (g *ProxyKeyGrant) UnmarshalJSON(data []byte) error {
	type grantAlias struct {
		ID            string  `json:"id"`
		Name          string  `json:"name"`
		Key           string  `json:"key"`
		Enabled       *bool   `json:"enabled"`
		AccountID     string  `json:"accountId"`
		SpendLimitUSD float64 `json:"spendLimitUsd"`
		Note          string  `json:"note"`
		CreatedAt     int64   `json:"createdAt"`
	}
	var raw grantAlias
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	g.ID = raw.ID
	g.Name = raw.Name
	g.Key = raw.Key
	g.Enabled = true
	if raw.Enabled != nil {
		g.Enabled = *raw.Enabled
	}
	g.AccountID = raw.AccountID
	g.SpendLimitUSD = raw.SpendLimitUSD
	g.Note = raw.Note
	g.CreatedAt = raw.CreatedAt
	return nil
}

// KeyUsage is the durable accounting behind one issued key: how many requests
// it carried and what the upstream reported for them. Spend is kept in
// micro-dollars so repeated float additions cannot drift.
type KeyUsage struct {
	Requests      int64 `json:"requests,omitempty"`
	SpentMicroUSD int64 `json:"spentMicroUsd,omitempty"`
	LastUsed      int64 `json:"lastUsed,omitempty"`
}

// SpentUSD converts the stored micro-dollars back for display and limits.
func (u KeyUsage) SpentUSD() float64 {
	return float64(u.SpentMicroUSD) / 1e6
}

// NewProxyKeyID returns the stable identity of a grant, matching the account
// identity scheme so renames and reordering keep their counters.
func NewProxyKeyID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "key_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return "key_" + hex.EncodeToString(raw[:])
}

// NewProxyKeyValue mints the client secret itself. Callers may also supply
// their own value; this is only the convenience generator behind the console's
// "随机生成" button and the API.
func NewProxyKeyValue() string {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "sk-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return "sk-" + hex.EncodeToString(raw[:])
}
