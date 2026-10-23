package responses

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestRoutingExtensionsSurviveConversionCompactionAndRepair(t *testing.T) {
	options := map[string]any{"gateway": map[string]any{"only": []any{"deepseek"}, "has": []any{"vision", "implicit-caching"}, "custom": json.Number("9007199254740993")}, "deepseek": map[string]any{"keep": true}}
	request := map[string]any{"model": "cline-pass/test", "input": "hello", "providerOptions": options, "provider": map[string]any{"only": []any{"deepseek"}}}
	chat, _, err := ToChat(request)
	if err != nil {
		t.Fatal(err)
	}
	compact, _, err := ToCompactionChatWithOptions(request, Options{})
	if err != nil {
		t.Fatal(err)
	}
	repair := StructuredRepairBody(chat, "invalid", map[string]any{"type": "object"}, nil)
	for name, body := range map[string]map[string]any{"chat": chat, "compact": compact, "repair": repair} {
		if !reflect.DeepEqual(body["providerOptions"], options) || !reflect.DeepEqual(body["provider"], request["provider"]) {
			t.Fatalf("%s dropped routing extensions: %+v", name, body)
		}
	}
}
