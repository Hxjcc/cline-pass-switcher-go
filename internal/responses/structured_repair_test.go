package responses

import (
	"encoding/json"
	"strings"
	"testing"
)

func schemaDocument(t *testing.T, raw string) any {
	t.Helper()
	var document any
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func TestWrapPlainTextForSchema(t *testing.T) {
	const titleSchema = `{"type":"object","properties":{"title":{"type":"string","maxLength":36}},"required":["title"],"additionalProperties":false}`
	for _, tc := range []struct {
		name    string
		text    string
		schema  string
		want    string
		wrapped bool
	}{
		{"plain title", "生成可享高级权益的115安装包", titleSchema, `{"title":"生成可享高级权益的115安装包"}`, true},
		{"ascii prose", "I cannot help with that request.", titleSchema, `{"title":"I cannot help with that request."}`, true},
		{"truncated json", `{"title":"broken`, titleSchema, "", false},
		{"json object", `{"title":"ok"}`, titleSchema, "", false},
		{"empty", "   ", titleSchema, "", false},
		{"two properties", "hello", `{"type":"object","properties":{"title":{"type":"string"},"id":{"type":"string"}},"required":["title"]}`, "", false},
		{"non string property", "hello", `{"type":"object","properties":{"title":{"type":"integer"}},"required":["title"]}`, "", false},
		{"property not required", "hello", `{"type":"object","properties":{"title":{"type":"string"}},"required":["other"]}`, "", false},
		{"non object schema", "hello", `{"type":"string"}`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := WrapPlainTextForSchema(tc.text, schemaDocument(t, tc.schema))
			if ok != tc.wrapped || got != tc.want {
				t.Fatalf("wrapped=%v value=%q, want wrapped=%v value=%q", ok, got, tc.wrapped, tc.want)
			}
		})
	}
}

func TestStructuredRepairBodyKeepsOnlyTextAndSchema(t *testing.T) {
	chatBody := map[string]any{
		"model":           "cline-pass/glm-5.3-flash",
		"messages":        []any{map[string]any{"role": "user", "content": "long conversation"}},
		"response_format": map[string]any{"type": "json_schema"},
		"stream":          true,
	}
	body := StructuredRepairBody(chatBody, "抱歉，我无法协助这个请求。", map[string]any{"type": "object"})
	if body["stream"] != false {
		t.Fatalf("repair stream = %#v", body["stream"])
	}
	if body["reasoning_effort"] != "low" {
		t.Fatalf("repair effort = %#v", body["reasoning_effort"])
	}
	if body["response_format"] == nil {
		t.Fatal("repair dropped the response format")
	}
	messages, _ := body["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("repair kept conversation history: %#v", body["messages"])
	}
	prompt, _ := messages[0].(map[string]any)["content"].(string)
	for _, want := range []string{"JSON Schema", "抱歉，我无法协助这个请求。", `"object"`} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("repair prompt missing %q: %s", want, prompt)
		}
	}
}
