package responses

import (
	"encoding/json"
	"strings"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
)

// StrictOutput reports whether the request asked for a strict JSON Schema
// output that the bridge validates locally.
func (context *Context) StrictOutput() bool {
	return context != nil && context.outputSchema != nil
}

// StructuredSchema returns the request's raw JSON Schema, if it declared one.
func (context *Context) StructuredSchema() any {
	if context == nil {
		return nil
	}
	return jsonx.Map(jsonx.Map(context.ResponseText)["format"])["schema"]
}

// WrapPlainTextForSchema wraps a plain-text answer into the object shape when
// the schema has exactly one string property. Conversation titles are the
// motivating case: GLM answers with the bare title and ignores the forwarded
// response_format. Only text that does not look like a JSON attempt is
// wrapped, so a truncated object is never turned into a bogus string field;
// the caller still validates the wrapped value against the schema.
func WrapPlainTextForSchema(text string, schema any) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || looksLikeJSONAttempt(trimmed) {
		return "", false
	}
	document := jsonx.Map(schema)
	if jsonx.String(document["type"]) != "object" {
		return "", false
	}
	properties := jsonx.Map(document["properties"])
	if len(properties) != 1 {
		return "", false
	}
	name := ""
	var property any
	for key, value := range properties {
		name, property = key, value
	}
	if jsonx.String(jsonx.Map(property)["type"]) != "string" {
		return "", false
	}
	if required := jsonx.Slice(document["required"]); required != nil {
		found := false
		for _, raw := range required {
			if jsonx.String(raw) == name {
				found = true
				break
			}
		}
		if !found {
			return "", false
		}
	}
	raw, err := json.Marshal(map[string]any{name: trimmed})
	if err != nil {
		return "", false
	}
	return string(raw), true
}

func looksLikeJSONAttempt(text string) bool {
	switch text[0] {
	case '{', '[', '"', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', 't', 'f', 'n':
		return true
	}
	return false
}

// StructuredRepairBody builds the one-shot reformatting request: the failed
// text and the schema only, without the original conversation. The task is
// reformatting rather than reasoning, so it runs at low effort.
func StructuredRepairBody(chatBody map[string]any, text string, schema any) map[string]any {
	const maxText = 8000
	if len(text) > maxText {
		text = text[:maxText] + "..."
	}
	rawSchema, err := json.Marshal(schema)
	if err != nil {
		rawSchema = []byte("{}")
	}
	prompt := "Your previous reply was not a valid JSON value. Rewrite the content below as a single JSON value that conforms to this JSON Schema. " +
		"Keep the original language and prefer a short, natural summary over a placeholder. " +
		"Output only the JSON value, with no explanation and no Markdown code fences.\n\nJSON Schema:\n" +
		string(rawSchema) + "\n\nContent to rewrite:\n" + text
	body := map[string]any{
		"model":            chatBody["model"],
		"stream":           false,
		"messages":         []any{map[string]any{"role": "user", "content": prompt}},
		"reasoning_effort": "low",
	}
	if format := chatBody["response_format"]; format != nil {
		body["response_format"] = format
	}
	return body
}
