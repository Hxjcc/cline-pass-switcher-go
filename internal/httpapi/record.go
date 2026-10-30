package httpapi

import (
	"context"
	"log"
	"strings"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
	"github.com/Hxjcc/cline-pass-switcher-go/internal/strx"
	"github.com/Hxjcc/cline-pass-switcher-go/internal/upstream"
)

// maxRecordedErrorRunes bounds what one upstream failure can add to the
// journal, the metadata snapshot and the console. The response itself may
// still carry a longer diagnostic; history is not a log sink.
const maxRecordedErrorRunes = 2000

// record persists one request. The admitting key travels in the context so the
// store can charge the spend of an issued key in the same journal entry; the
// console and master-key traffic stamp nothing and stay uncapped.
func (s *Server) record(ctx context.Context, entry model.HistoryEntry) {
	if policy, ok := ctx.Value(recordedRoutingKey{}).(recordedRouting); ok {
		entry.GatewayHas = policy.Has
		entry.TargetProviders = policy.Targets
		if len(policy.Has) > 0 {
			entry.RoutingStatus = "unknown"
			actual := firstNonEmpty(entry.Resolved, entry.Provider)
			if actual != "" && len(policy.Targets) > 0 {
				entry.RoutingStatus = "mismatch"
				for _, target := range policy.Targets {
					if upstream.SameProvider(actual, target) {
						entry.RoutingStatus = "matched"
						break
					}
				}
				if entry.RoutingStatus == "mismatch" && entry.Error == nil {
					message := "实际供应商不满足本次路由要求：" + actual
					entry.Error = &message
				}
			}
		}
	}
	entry = stampCallerKey(ctx, entry)
	if names, ok := ctx.Value(clientToolNamesKey{}).(map[string]string); ok && entry.Tools != nil {
		summary := &model.ToolSummary{Names: map[string]int{}, MaxBatchCalls: entry.Tools.MaxBatchCalls}
		for name, n := range entry.Tools.Names {
			if original, found := names[name]; found {
				name = original
			}
			if name != "" {
				summary.Names[name] += n
				summary.Calls += n
			}
		}
		entry.Tools = summary
		entry.Tools.MaxBatchCalls = min(entry.Tools.MaxBatchCalls, entry.Tools.Calls)
	}
	if n, ok := ctx.Value(toolFailuresKey{}).(int); ok && n > 0 {
		entry.Tools = cloneToolSummary(entry.Tools)
		if entry.Tools == nil {
			entry.Tools = &model.ToolSummary{}
		}
		entry.Tools.ReportedFailures = n
	}
	if entry.Session == "" {
		entry.Session = recordedSession(ctx)
	}
	if entry.Error != nil {
		message := strx.Truncate(*entry.Error, maxRecordedErrorRunes)
		entry.Error = &message
	}
	entry.DegradeReason = strx.Truncate(entry.DegradeReason, maxRecordedErrorRunes)
	if err := s.store.Record(entry); err != nil {
		log.Printf("persist request history: %v", err)
	}
}

type recordedSessionKey struct{}

// withRecordedSession remembers the client's conversation id so a history row
// can be traced back to the thread that produced it.
func withRecordedSession(ctx context.Context, body map[string]any) context.Context {
	id, _ := body["prompt_cache_key"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, recordedSessionKey{}, id)
}

func recordedSession(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(recordedSessionKey{}).(string)
	return id
}

// sessionIDFromBody reads the conversation id straight from a request body, for
// the handlers that build their history row without the stick context.
func sessionIDFromBody(body map[string]any) string {
	id, _ := body["prompt_cache_key"].(string)
	return strings.TrimSpace(id)
}
