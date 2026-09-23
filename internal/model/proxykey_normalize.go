package model

import "strings"

// normalizeProxyKeys cleans the issued-key list. Rows without a usable key are
// dropped, identities are filled in and de-duplicated so counters survive a
// console round-trip, and the master keys are never duplicated as grants -
// otherwise the same secret would be counted twice and a grant could shadow
// the console key.
func normalizeProxyKeys(grants []ProxyKeyGrant, proxyKey, adminKey string) []ProxyKeyGrant {
	if len(grants) == 0 {
		return nil
	}
	cleaned := make([]ProxyKeyGrant, 0, len(grants))
	seenID := make(map[string]struct{}, len(grants))
	seenKey := make(map[string]struct{}, len(grants))
	if proxyKey != "" {
		seenKey[proxyKey] = struct{}{}
	}
	if adminKey != "" {
		seenKey[adminKey] = struct{}{}
	}
	for _, grant := range grants {
		grant.ID = strings.TrimSpace(grant.ID)
		grant.Key = strings.TrimSpace(grant.Key)
		grant.Name = strings.TrimSpace(grant.Name)
		grant.AccountID = strings.TrimSpace(grant.AccountID)
		grant.Note = strings.TrimSpace(grant.Note)
		if grant.Key == "" {
			continue
		}
		if _, duplicate := seenKey[grant.Key]; duplicate {
			continue
		}
		seenKey[grant.Key] = struct{}{}
		if grant.ID == "" {
			grant.ID = NewProxyKeyID()
		}
		if _, duplicate := seenID[grant.ID]; duplicate {
			grant.ID = NewProxyKeyID()
		}
		seenID[grant.ID] = struct{}{}
		if grant.SpendLimitUSD < 0 {
			grant.SpendLimitUSD = 0
		}
		if grant.CreatedAt < 0 {
			grant.CreatedAt = 0
		}
		cleaned = append(cleaned, grant)
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}
