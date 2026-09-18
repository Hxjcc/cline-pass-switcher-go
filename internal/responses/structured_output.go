package responses

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type localSchemaOnly struct{}

func (localSchemaOnly) Load(string) (any, error) {
	return nil, errors.New("external schema references are not supported; include definitions in the request schema")
}

// Compile once per request using the same effective strict setting forwarded
// to Chat. Plain text, JSON mode and explicit strict:false keep their behavior.
func compileOutputSchema(text any) (*jsonschema.Schema, error) {
	format := asMap(asMap(text)["format"])
	if asString(format["type"]) != "json_schema" {
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
	if asMap(format["schema"]) == nil {
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
		output := asMap(item)
		switch asString(output["type"]) {
		case "function_call", "custom_tool_call", "tool_search_call":
			return nil
		case "message":
			for _, raw := range asSlice(output["content"]) {
				part := asMap(raw)
				switch asString(part["type"]) {
				case "refusal":
					return nil
				case "output_text":
					text.WriteString(asString(part["text"]))
				}
			}
		}
	}
	value, err := jsonschema.UnmarshalJSON(strings.NewReader(text.String()))
	if err != nil {
		return errors.New("upstream structured output is not a single valid JSON value")
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
