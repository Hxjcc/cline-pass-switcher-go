package httpapi

import (
	"context"
	"encoding/json"
	"maps"
	"strings"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/jsonx"
	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
	"github.com/Hxjcc/cline-pass-switcher-go/internal/strx"
)

type toolFailuresKey struct{}
type clientToolNamesKey struct{}

// Only inspect explicit failure markers in the newly returned tool results.
// Opaque output text and function arguments are never retained or searched.
func withToolFailures(ctx context.Context, body map[string]any) context.Context {
	messages := jsonx.Slice(body["messages"])
	failures := 0
	for i := len(messages) - 1; i >= 0; i-- {
		m := jsonx.Map(messages[i])
		if jsonx.String(m["role"]) != "tool" {
			break
		}
		output := jsonx.Map(m["content"])
		if text, ok := m["content"].(string); ok && len(text) <= 4096 {
			_ = json.Unmarshal([]byte(text), &output)
		}
		if output["is_error"] == true {
			failures++
		}
	}
	return context.WithValue(ctx, toolFailuresKey{}, failures)
}

func cloneToolSummary(v *model.ToolSummary) *model.ToolSummary {
	if v == nil {
		return nil
	}
	n := *v
	n.Names = maps.Clone(v.Names)
	return &n
}

func toolSummaryFromCalls(calls []any) *model.ToolSummary {
	result := &model.ToolSummary{Names: map[string]int{}}
	for _, raw := range calls {
		f := jsonx.Map(jsonx.Map(raw)["function"])
		name := strings.TrimSpace(jsonx.String(f["name"]))
		if name == "" || strings.HasPrefix(name, "vercel:") {
			continue
		}
		name = strx.Truncate(name, 128)
		if _, exists := result.Names[name]; !exists && len(result.Names) >= 64 {
			name = "other"
		}
		result.Names[name]++
		result.Calls++
	}
	result.MaxBatchCalls = result.Calls
	if result.Calls == 0 {
		return nil
	}
	return result
}
