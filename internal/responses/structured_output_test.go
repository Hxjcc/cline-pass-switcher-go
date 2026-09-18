package responses

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
)

func strictContext(t *testing.T, schema string) *Context {
	t.Helper()
	var doc any
	decoder := json.NewDecoder(strings.NewReader(schema))
	decoder.UseNumber()
	if err := decoder.Decode(&doc); err != nil {
		t.Fatal(err)
	}
	_, context, err := ToChat(map[string]any{"model": "test", "input": "hi",
		"text": map[string]any{"format": map[string]any{"type": "json_schema", "strict": true, "schema": doc}}})
	if err != nil {
		t.Fatal(err)
	}
	return context
}

func completedChat(content string) map[string]any {
	return map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}, "finish_reason": "stop"}}}
}

func TestStrictStructuredOutputSchemaSemantics(t *testing.T) {
	const object = `{"type":"object","properties":{"token":{"type":"string"},"color":{"enum":["red","blue"]}},"required":["token","color"],"additionalProperties":false}`
	for _, tc := range []struct {
		name, schema, text string
		valid              bool
	}{
		{"valid unchanged", object, " \n{\"token\":\"COBALT-742\", \"color\":\"red\"}\n", true},
		{"renamed field", object, `{"token":"COBALT-742","image_color":"red"}`, false},
		{"extra field", object, `{"token":"COBALT-742","color":"red","status":"done"}`, false},
		{"wrong type", object, `{"token":12,"color":"red"}`, false},
		{"enum mismatch", object, `{"token":"COBALT-742","color":"green"}`, false},
		{"code fence", object, "```json\n{\"token\":\"COBALT-742\",\"color\":\"red\"}\n```", false},
		{"multiple documents", object, `{"token":"a","color":"red"}{"token":"b","color":"blue"}`, false},
		{"invalid JSON", object, `{"token":`, false},
		{"null object", object, `null`, false},
		{"nested array", `{"type":"object","properties":{"rows":{"type":"array","items":{"type":"integer"},"minItems":2}},"required":["rows"]}`, `{"rows":[1,2.0]}`, true},
		{"invalid nested array", `{"type":"object","properties":{"rows":{"type":"array","items":{"type":"integer"}}}}`, `{"rows":[1,2.5]}`, false},
		{"nullable anyOf", `{"type":"object","properties":{"value":{"anyOf":[{"type":"string"},{"type":"null"}]}},"required":["value"]}`, `{"value":null}`, true},
		{"invalid anyOf", `{"type":"object","properties":{"value":{"anyOf":[{"type":"string"},{"type":"null"}]}},"required":["value"]}`, `{"value":42}`, false},
		{"local ref", `{"$defs":{"value":{"type":"integer","minimum":2}},"type":"object","properties":{"x":{"$ref":"#/$defs/value"}},"required":["x"]}`, `{"x":2}`, true},
		{"local ref invalid", `{"$defs":{"value":{"type":"integer","minimum":2}},"type":"object","properties":{"x":{"$ref":"#/$defs/value"}},"required":["x"]}`, `{"x":1}`, false},
		{"recursive ref", `{"$defs":{"node":{"type":"object","properties":{"children":{"type":"array","items":{"$ref":"#/$defs/node"}}},"required":["children"],"additionalProperties":false}},"$ref":"#/$defs/node"}`, `{"children":[{"children":[]}]}`, true},
		{"recursive invalid", `{"$defs":{"node":{"type":"object","properties":{"children":{"type":"array","items":{"$ref":"#/$defs/node"}}},"required":["children"],"additionalProperties":false}},"$ref":"#/$defs/node"}`, `{"children":[{"children":"bad"}]}`, false},
		{"precise integer", `{"type":"object","properties":{"id":{"const":9007199254740993}},"required":["id"]}`, `{"id":9007199254740993}`, true},
		{"neighbor integer", `{"type":"object","properties":{"id":{"const":9007199254740993}},"required":["id"]}`, `{"id":9007199254740992}`, false},
		{"pattern", `{"type":"object","properties":{"id":{"type":"string","pattern":"^[A-Z]{3}$"}},"required":["id"]}`, `{"id":"ABC"}`, true},
		{"pattern mismatch", `{"type":"object","properties":{"id":{"type":"string","pattern":"^[A-Z]{3}$"}},"required":["id"]}`, `{"id":"abc"}`, false},
		{"format mismatch", `{"type":"object","properties":{"email":{"type":"string","format":"email"}},"required":["email"]}`, `{"email":"invalid"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			context := strictContext(t, tc.schema)
			out, err := FromChat(completedChat(tc.text), context)
			if tc.valid {
				if err != nil || out["status"] != "completed" || responseOutputText(out) != strings.TrimSpace(tc.text) {
					t.Fatalf("valid output rejected or changed: %#v %v", out, err)
				}
				if textFromParts(jsonx.Map(jsonx.Slice(out["output"])[0])["content"]) != tc.text {
					t.Fatal("validation rewrote original text")
				}
			} else {
				var failure *ChatFailure
				if out != nil || !errors.As(err, &failure) || failure.Code != "upstream_schema_validation_failed" {
					t.Fatalf("invalid output accepted: %#v %v", out, err)
				}
				if strings.Contains(failure.Message, "COBALT-742") {
					t.Fatal("error copied response values into history")
				}
			}
		})
	}
}

func TestStrictStreamValidatesBeforeCompleted(t *testing.T) {
	context := strictContext(t, `{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`)
	for _, valid := range []bool{false, true} {
		parts := []string{`{"ok":`, `true}`}
		if !valid {
			parts[1] = `"yes"}`
		}
		adapter := NewStreamAdapter(context)
		var events []Event
		for _, part := range parts {
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": part}}}})
			events = append(events, adapter.Feed([]byte("data: "+string(chunk)+"\n\n"))...)
		}
		if OutcomeFromEvents(events).Status != "" {
			t.Fatal("schema validation terminated before complete text")
		}
		events = append(events, adapter.Feed([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))...)
		outcome := OutcomeFromEvents(events)
		if valid && outcome.Status != "completed" {
			t.Fatalf("valid stream failed: %#v", outcome)
		}
		if !valid && (outcome.Status != "failed" || outcome.Code != "upstream_schema_validation_failed") {
			t.Fatalf("invalid stream completed: %#v", outcome)
		}
		for _, event := range events {
			if !valid && event.Type == "response.completed" {
				t.Fatal("invalid JSON was reported completed")
			}
		}
		if len(adapter.Finish(nil)) != 0 {
			t.Fatal("duplicate terminal on Finish")
		}
	}
}

func TestStrictSchemaPreservesOtherOutcomes(t *testing.T) {
	context := strictContext(t, `{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"]}`)
	for _, tc := range []struct {
		name, finish, status string
		message              map[string]any
	}{
		{"refusal", "stop", "completed", map[string]any{"refusal": "Cannot comply"}},
		{"refusal with text", "stop", "completed", map[string]any{"content": "Sorry", "refusal": "Cannot comply"}},
		{"tools", "tool_calls", "completed", map[string]any{"content": "Checking now", "tool_calls": []any{map[string]any{"id": "call1", "function": map[string]any{"name": "read", "arguments": "{}"}}}}},
		{"length", "length", "incomplete", map[string]any{"content": `{"ok":`}},
		{"filter", "content_filter", "incomplete", map[string]any{"content": "partial"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := FromChat(map[string]any{"choices": []any{map[string]any{"message": tc.message, "finish_reason": tc.finish}}}, context)
			if err != nil || out["status"] != tc.status {
				t.Fatalf("original outcome lost: %#v %v", out, err)
			}
		})
	}
	adapter := NewStreamAdapter(context)
	adapter.Feed([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"))
	if outcome := OutcomeFromEvents(adapter.Finish(nil)); outcome.Code != "stream_truncated" {
		t.Fatalf("truncation error masked: %#v", outcome)
	}
}

func TestStrictSchemaDoesNotChangeOptOutOrCompact(t *testing.T) {
	for _, format := range []map[string]any{
		{"type": "text"}, {"type": "json_object"}, {"type": "json_schema", "strict": false, "schema": map[string]any{"type": "object"}},
	} {
		_, context, err := ToChat(map[string]any{"model": "test", "input": "hi", "text": map[string]any{"format": format}})
		if err != nil {
			t.Fatal(err)
		}
		if out, err := FromChat(completedChat("plain output"), context); err != nil || out["status"] != "completed" {
			t.Fatalf("non-strict mode changed: %v", err)
		}
	}
	_, compactContext, err := ToCompactionChatWithOptions(map[string]any{"model": "test", "input": "hi", "text": map[string]any{"format": map[string]any{"type": "json_schema", "strict": true, "schema": map[string]any{"type": "object"}}}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CompactionResponse(completedChat("Complete visible summary"), compactContext); err != nil {
		t.Fatalf("compact summary incorrectly validated as JSON: %v", err)
	}
	_, context, err := ToChat(map[string]any{"model": "test", "input": "hi", "text": map[string]any{"format": map[string]any{"type": "json_schema", "schema": map[string]any{"type": "object"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FromChat(completedChat("plain output"), context); err == nil {
		t.Fatal("omitted strict differs from existing forwarded strict:true default")
	}
}

func TestInvalidOutputSchemaRejectedBeforeGeneration(t *testing.T) {
	for _, schema := range []any{nil, "not a schema", map[string]any{"type": "invalid"}, map[string]any{"required": "field"}, map[string]any{"$ref": "#/$defs/missing"}} {
		_, _, err := ToChat(map[string]any{"model": "test", "input": "hi", "text": map[string]any{"format": map[string]any{"type": "json_schema", "strict": true, "schema": schema}}})
		var requestError *RequestError
		if !errors.As(err, &requestError) || requestError.Code != "invalid_json_schema" || requestError.Param != "text.format.schema" {
			t.Fatalf("invalid schema not rejected: %#v %v", schema, err)
		}
	}
}

func TestSchemaReferencesNeverLoadNetworkOrFiles(t *testing.T) {
	var hits atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.Write([]byte(`{"type":"object"}`)) }))
	defer remote.Close()
	localFile := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(localFile, []byte(`{"type":"object"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{remote.URL, "file:///" + filepath.ToSlash(localFile)} {
		_, _, err := ToChat(map[string]any{"model": "test", "input": "hi", "text": map[string]any{"format": map[string]any{"type": "json_schema", "strict": true, "schema": map[string]any{"$ref": ref}}}})
		if err == nil || !strings.Contains(err.Error(), "external schema references") {
			t.Fatalf("external reference allowed: %s %v", ref, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("schema compiler made a network request")
	}
}
