package responses

import (
	"github.com/Hxjcc/cline-pass-switcher-go/internal/jsonx"
	"maps"
)

// Routing extensions survive conversion, compaction and repair. The upstream
// layer clones the maps again before enforcing the final per-attempt policy.
func CopyRoutingOptions(from, to map[string]any) {
	for _, key := range []string{"providerOptions", "provider"} {
		if value, ok := from[key]; ok {
			if object := jsonx.Map(value); object != nil {
				copy := maps.Clone(object)
				if gateway := jsonx.Map(object["gateway"]); gateway != nil {
					copy["gateway"] = maps.Clone(gateway)
				}
				to[key] = copy
			} else {
				to[key] = value
			}
		}
	}
}
