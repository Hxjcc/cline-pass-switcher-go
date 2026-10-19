package upstream

import (
	"encoding/json"
	"testing"
)

func TestCloneChatRequestPreservesLargeNumbersAndDetachesRouting(t *testing.T) {
	body := map[string]any{
		"model": "cline-pass/test",
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
		"seed": json.Number("9007199254740993"),
		"providerOptions": map[string]any{
			"gateway": map[string]any{"only": []any{"keep-me"}},
		},
		"provider": map[string]any{"only": []any{"keep-me-too"}},
	}

	cloned := cloneChatRequest(body)
	if cloned["seed"] != json.Number("9007199254740993") {
		t.Fatalf("large number changed type or value: %#v", cloned["seed"])
	}
	cloned["providerOptions"].(map[string]any)["gateway"].(map[string]any)["only"] = []any{"changed"}
	cloned["provider"].(map[string]any)["only"] = []any{"changed"}

	originalOptions := body["providerOptions"].(map[string]any)["gateway"].(map[string]any)["only"].([]any)
	if originalOptions[0] != "keep-me" {
		t.Fatalf("nested provider options were aliased: %#v", originalOptions)
	}
	originalProvider := body["provider"].(map[string]any)["only"].([]any)
	if originalProvider[0] != "keep-me-too" {
		t.Fatalf("provider map was aliased: %#v", originalProvider)
	}
}
