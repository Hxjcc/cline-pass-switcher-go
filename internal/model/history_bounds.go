package model

import "github.com/Hxjcc/cline-pass-switcher-go/internal/strx"

// Bound every externally supplied field at the durable storage boundary.
func BoundedHistory(e HistoryEntry) HistoryEntry {
	if e.Tools != nil {
		v := *e.Tools
		v.Names = map[string]int{}
		for name, n := range e.Tools.Names {
			if len(v.Names) >= 64 {
				break
			}
			v.Names[strx.Truncate(name, 128)] = n
		}
		e.Tools = &v
	}
	// Stable accounting identities are validated at configuration admission,
	// never rewritten here: truncating one would charge a different ledger key.
	for _, p := range []*string{&e.Model, &e.Provider, &e.Resolved, &e.Canonical, &e.Account, &e.KeyName, &e.Session, &e.Kind, &e.Effort, &e.RequestedEffort, &e.FinishReason, &e.GenerationID, &e.RoutingStatus, &e.FallbackReason} {
		*p = strx.Truncate(*p, 256)
	}
	if e.Error != nil {
		v := strx.Truncate(*e.Error, 2000)
		e.Error = &v
	}
	e.DegradeReason = strx.Truncate(e.DegradeReason, 2000)
	bound := func(v []string) []string {
		v = append([]string(nil), v[:min(len(v), 64)]...)
		for i := range v {
			v[i] = strx.Truncate(v[i], 256)
		}
		return v
	}
	e.Attempts = bound(e.Attempts)
	e.GatewayHas = bound(e.GatewayHas)
	e.TargetProviders = bound(e.TargetProviders)
	e.MissingSummarySections = bound(e.MissingSummarySections)
	e.Trace = append([]Trace(nil), e.Trace[:min(len(e.Trace), 64)]...)
	for i := range e.Trace {
		e.Trace[i].Upstream = strx.Truncate(e.Trace[i].Upstream, 256)
		e.Trace[i].Note = strx.Truncate(e.Trace[i].Note, 2000)
	}
	e.GatewayAttempts = append([]GatewayAttempt(nil), e.GatewayAttempts[:min(len(e.GatewayAttempts), 64)]...)
	for i := range e.GatewayAttempts {
		a := &e.GatewayAttempts[i]
		a.Provider = strx.Truncate(a.Provider, 256)
		a.RequestID = strx.Truncate(a.RequestID, 256)
		a.ResponseID = strx.Truncate(a.ResponseID, 256)
		a.Error = strx.Truncate(a.Error, 2000)
	}
	return e
}
