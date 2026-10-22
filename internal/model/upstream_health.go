package model

import "strings"

// ObserveUpstreamSuccess replaces an older channel failure with evidence from
// a completed request. It does not establish support for provider pinning.
func (m *ModelMeta) ObserveUpstreamSuccess(provider string, attempts []GatewayAttempt, checkedAt, ms int64) {
	if m.Pinnable == nil || !*m.Pinnable || checkedAt <= 0 {
		return
	}
	// Affinity can keep naming the planned channel after a fallback. Prefer
	// the gateway's last successful attempt over that routing hint.
	for index := len(attempts) - 1; index >= 0; index-- {
		if attempt := attempts[index]; attempt.Success && attempt.Provider != "" {
			provider = attempt.Provider
			break
		}
	}
	key := upstreamHealthKey(provider)
	if key == "" {
		return
	}
	for _, slug := range m.Upstreams {
		if upstreamHealthKey(slug) != key && upstreamHealthKey(m.UpstreamDetail[slug].Name) != key {
			continue
		}
		if m.UpstreamStatus[slug].CheckedAt > checkedAt {
			return
		}
		if m.UpstreamStatus == nil {
			m.UpstreamStatus = map[string]UpstreamStatus{}
		}
		m.UpstreamStatus[slug] = UpstreamStatus{Status: "ok", CheckedAt: checkedAt, MS: ms}
		return
	}
}

func upstreamHealthKey(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, strings.ToLower(value))
}
