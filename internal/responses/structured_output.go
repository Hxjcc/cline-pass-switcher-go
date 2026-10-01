package responses

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type localSchemaOnly struct{}

func (localSchemaOnly) Load(string) (any, error) {
	return nil, errors.New("external schema references are not supported; include definitions in the request schema")
}

// Compile once per request using the same effective strict setting forwarded
// to Chat. Plain text, JSON mode and explicit strict:false keep their behavior.
func compileOutputSchema(text any) (*jsonschema.Schema, error) {
	format := jsonx.Map(jsonx.Map(text)["format"])
	if jsonx.String(format["type"]) != "json_schema" {
		return nil, nil
	}
	if strict, found := format["strict"]; found && strict != nil {
		if _, ok := strict.(bool); !ok {
			return nil, &RequestError{Code: "invalid_json_schema", Param: "text.format.strict", Message: "text.format.strict must be a boolean"}
		}
	}
	// Preserve the bridge's existing strict:true default when omitted.
	if !boolValue(format["strict"], true) {
		return nil, nil
	}
	invalid := func(message string) (*jsonschema.Schema, error) {
		return nil, &RequestError{Code: "invalid_json_schema", Param: "text.format.schema", Message: message}
	}
	if jsonx.Map(format["schema"]) == nil {
		return invalid("text.format.schema must be a JSON Schema object for strict output")
	}
	raw, err := json.Marshal(format["schema"])
	if err != nil {
		return invalid("text.format.schema is not valid JSON")
	}
	// Normalize Go numeric types without rounding large integer constraints.
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return invalid("text.format.schema is not valid JSON")
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiler.UseLoader(localSchemaOnly{})
	const location = "https://cline-proxy.invalid/response-schema.json"
	if err := compiler.AddResource(location, doc); err != nil {
		return invalid("invalid text.format.schema: " + boundedSchemaError(err))
	}
	schema, err := compiler.Compile(location)
	if err != nil {
		return invalid("invalid text.format.schema: " + boundedSchemaError(err))
	}
	return schema, nil
}

func boundedSchemaError(err error) string {
	message := []rune(err.Error())
	if len(message) > 512 {
		return string(message[:512]) + "..."
	}
	return string(message)
}

// Validate only a final text response. A refusal is a distinct response type;
// tool calls hand control back to the client before a final structured answer.
func (state *StreamState) validateStructuredOutput() error {
	if state.context.outputSchema == nil {
		return nil
	}
	var text strings.Builder
	for _, item := range state.outputItems() {
		output := jsonx.Map(item)
		switch jsonx.String(output["type"]) {
		case "function_call", "custom_tool_call", "tool_search_call":
			return nil
		case "message":
			for _, raw := range jsonx.Slice(output["content"]) {
				part := jsonx.Map(raw)
				switch jsonx.String(part["type"]) {
				case "refusal":
					return nil
				case "output_text":
					text.WriteString(jsonx.String(part["text"]))
				}
			}
		}
	}
	value, err := jsonschema.UnmarshalJSON(strings.NewReader(text.String()))
	if err != nil {
		return errors.New("upstream structured output is not a single valid JSON value: " + describeStructuredFailure(text.String()))
	}
	if err := state.context.outputSchema.Validate(value); err != nil {
		// Include the failed keyword/location, not the generated data values,
		// so request history does not acquire a copy of the response content.
		var validation *jsonschema.ValidationError
		if errors.As(err, &validation) {
			for len(validation.Causes) > 0 {
				validation = validation.Causes[0]
			}
			path := ""
			for _, token := range validation.InstanceLocation {
				path += "/" + strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
			}
			keyword := strings.Join(validation.ErrorKind.KeywordPath(), "/")
			return errors.New(boundedSchemaError(fmt.Errorf("upstream structured output does not match text.format.schema at %q (keyword: %s)", path, keyword)))
		}
		return errors.New("upstream structured output does not match text.format.schema")
	}
	return nil
}

// decodeFirstJSONValue decodes one value and reports the exact byte range it
// occupied, so callers can slice the original text without re-encoding it.
func decodeFirstJSONValue(text string) (value string, rest string, ok bool) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return "", "", false
	}
	end := int(decoder.InputOffset())
	if end <= 0 || end > len(text) {
		return "", "", false
	}
	return strings.TrimSpace(text[:end]), text[end:], true
}

// describeStructuredFailure explains why the final text was not one JSON value
// without copying generated data into the error stored in request history.
func describeStructuredFailure(text string) string {
	trimmed := strings.TrimSpace(strings.TrimPrefix(text, "\ufeff"))
	switch {
	case trimmed == "":
		return "output is empty"
	case strings.HasPrefix(trimmed, "```"):
		return "output starts with a Markdown code fence"
	}
	if _, rest, ok := decodeFirstJSONValue(trimmed); ok {
		if strings.TrimSpace(rest) == "" {
			return "output is a single JSON value"
		}
		return "output has trailing text after the first JSON value"
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return "output is not valid JSON: " + boundedSchemaError(err)
	}
	return "output is not a single JSON value"
}

// dumpStructuredFailure writes the raw upstream text plus the schema and
// failure reason to SCHEMA_FAIL_DUMP (a directory) when strict schema
// validation fails. The switch is off by default: the dump contains generated
// content and exists for local diagnosis only.
func (state *StreamState) dumpStructuredFailure(reason error) {
	dir := strings.TrimSpace(os.Getenv("SCHEMA_FAIL_DUMP"))
	switch strings.ToLower(dir) {
	case "", "0", "off", "false", "no":
		return
	}
	text := state.structuredFailureText()
	payload := map[string]any{
		"time":          time.Now().Format(time.RFC3339Nano),
		"model":         state.context.Model,
		"response_id":   state.responseID,
		"finish_reason": state.finishReason,
		"reason":        reason.Error(),
		"text_length":   len(text),
		"text":          text,
		"schema":        jsonx.Map(jsonx.Map(state.context.ResponseText)["format"])["schema"],
	}
	if state.context.Metadata != nil {
		payload["metadata"] = state.context.Metadata
	}
	if state.reasoning != nil {
		payload["reasoning"] = state.reasoning.Text.String()
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	name := fmt.Sprintf("schema-failure-%d.json", time.Now().UnixNano())
	_ = os.WriteFile(filepath.Join(dir, name), data, 0o600)
}

// structuredFailureText concatenates the final message text the validator
// looked at, so a dump shows exactly what the upstream produced.
func (state *StreamState) structuredFailureText() string {
	var text strings.Builder
	for _, item := range state.outputItems() {
		output := jsonx.Map(item)
		if jsonx.String(output["type"]) != "message" {
			continue
		}
		for _, raw := range jsonx.Slice(output["content"]) {
			part := jsonx.Map(raw)
			if jsonx.String(part["type"]) == "output_text" {
				text.WriteString(jsonx.String(part["text"]))
			}
		}
	}
	return text.String()
}
