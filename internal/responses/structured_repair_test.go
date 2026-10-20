package responses

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
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
		{"markdown fence", "```json\n{\"title\":\"ok\"}\n```", titleSchema, "", false},
		{"bom title", "\uFEFFhello", titleSchema, `{"title":"hello"}`, true},
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

func TestStructuredRepairBodyMapsEffortAndPreservesUTF8(t *testing.T) {
	text := strings.Repeat("中", 8001)
	body := StructuredRepairBody(
		map[string]any{"model": "test"},
		text,
		map[string]any{"type": "object"},
		[]string{"none", "max"},
	)
	if body["reasoning_effort"] != "none" {
		t.Fatalf("repair effort = %#v, want none for a model that only supports none/max", body["reasoning_effort"])
	}
	messages := body["messages"].([]any)
	prompt := messages[0].(map[string]any)["content"].(string)
	if !utf8.ValidString(prompt) {
		t.Fatal("repair prompt cut through a UTF-8 sequence")
	}
	if strings.Count(prompt, "中") > 8000 {
		t.Fatal("repair prompt was not truncated")
	}
}

func TestStructuredRepairBudgetsAndEffort(t *testing.T) {
	for _, tc := range []struct {
		name           string
		limit          any
		originalEffort string
		supported      []string
		wantLimit      int64
		wantEffort     string
	}{
		{"explicit small cap", json.Number("16"), "none", []string{"none", "max"}, 16, "none"},
		{"bounded default", nil, "", []string{"none", "max"}, 4096, "none"},
		{"large cap", 100000, "high", []string{"low", "high"}, 4096, "high"},
		{"unknown capabilities", 512, "", nil, 512, "low"},
		{"preserve disabled reasoning", 128, "none", []string{"high"}, 128, "none"},
		{"unspecified effort", nil, "", []string{" HIGH ", "MINIMAL"}, 4096, "minimal"},
		{"preserve high effort", nil, "high", []string{" HIGH ", "MINIMAL"}, 4096, "high"},
		{"preserve max effort", nil, "max", []string{"none", "max"}, 4096, "max"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := map[string]any{"model": "test", "max_tokens": tc.limit, "reasoning_effort": tc.originalEffort}
			repaired := StructuredRepairBody(original, "invalid JSON", map[string]any{"type": "object"}, tc.supported)
			if repaired["max_tokens"] != tc.wantLimit || repaired["reasoning_effort"] != tc.wantEffort {
				t.Fatalf("repair budget/effort = %v/%v, want %d/%s", repaired["max_tokens"], repaired["reasoning_effort"], tc.wantLimit, tc.wantEffort)
			}
			if original["max_tokens"] != tc.limit || original["reasoning_effort"] != tc.originalEffort {
				t.Fatal("repair mutated the original request")
			}
		})
	}
}

func TestStructuredRepairPreservesReasoningOptions(t *testing.T) {
	original := map[string]any{
		"model": "test", "reasoning": map[string]any{"effort": "high", "exclude": true}, "include_reasoning": false,
	}
	repaired := StructuredRepairBody(original, "invalid JSON", map[string]any{"type": "object"}, []string{"none", "high"})
	if repaired["reasoning_effort"] != "high" || repaired["include_reasoning"] != false {
		t.Fatalf("repair changed reasoning settings: %#v", repaired)
	}
	if reasoning := repaired["reasoning"].(map[string]any); reasoning["effort"] != "high" || reasoning["exclude"] != true {
		t.Fatalf("repair dropped reasoning options: %#v", reasoning)
	}
}

func TestStructuredRepairBodyStripsMarkdownFence(t *testing.T) {
	body := StructuredRepairBody(
		map[string]any{"model": "test"},
		"```json\n{\"answer\":\"ok\"}\n```",
		map[string]any{"type": "object"},
		nil,
	)
	messages := body["messages"].([]any)
	prompt := messages[0].(map[string]any)["content"].(string)
	if strings.Contains(prompt, "```") || !strings.Contains(prompt, `{"answer":"ok"}`) {
		t.Fatalf("repair prompt did not strip the fence: %s", prompt)
	}
}

func TestStructuredRepairBodyKeepsOnlyTextAndSchema(t *testing.T) {
	chatBody := map[string]any{
		"model":           "cline-pass/glm-5.3-flash",
		"messages":        []any{map[string]any{"role": "user", "content": "long conversation"}},
		"response_format": map[string]any{"type": "json_schema"},
		"stream":          true,
	}
	body := StructuredRepairBody(chatBody, "抱歉，我无法协助这个请求。", map[string]any{"type": "object"}, nil)
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
