package responses

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/jsonx"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func (c *Context) compileToolSchemas() error {
	if len(c.chatTools) > 1024 {
		return unsupported("tools", "more than 1024 effective tools")
	}
	c.toolSchemas = map[string]*jsonschema.Schema{}
	for _, raw := range c.chatTools {
		f := jsonx.Map(jsonx.Map(raw)["function"])
		if f["strict"] != true {
			continue
		}
		data, err := json.Marshal(f["parameters"])
		if err != nil || len(data) > 256<<10 {
			return &RequestError{Param: "tools", Code: "invalid_tool_schema", Message: "strict tool schema must be at most 256 KiB"}
		}
		schema, err := compileOutputSchema(map[string]any{"format": map[string]any{"type": "json_schema", "strict": true, "schema": f["parameters"]}})
		if err != nil {
			return &RequestError{Param: "tools", Code: "invalid_tool_schema", Message: strings.ReplaceAll(err.Error(), "text.format.schema", "strict tool parameters")}
		}
		c.toolSchemas[jsonx.String(f["name"])] = schema
	}
	return nil
}

func (c *Context) strictTool(name string) bool { return c != nil && c.toolSchemas[name] != nil }
func (c *Context) validateToolArguments(name, arguments string) error {
	if !c.strictTool(name) {
		return nil
	}
	value, err := jsonschema.UnmarshalJSON(strings.NewReader(arguments))
	if err == nil {
		err = c.toolSchemas[name].Validate(value)
	}
	if err != nil {
		return errors.New("upstream strict tool arguments do not match the declared schema for " + name)
	}
	return nil
}

// CompileChatToolContext applies the same local guarantees to Chat clients.
func CompileChatToolContext(tools any) (*Context, error) {
	c := &Context{chatTools: jsonx.Slice(tools)}
	for _, raw := range c.chatTools {
		f := jsonx.Map(jsonx.Map(raw)["function"])
		if value, found := f["strict"]; found {
			if _, ok := value.(bool); !ok {
				return nil, &RequestError{Param: "tools", Message: "tool strict must be a boolean"}
			}
		}
	}
	if err := c.compileToolSchemas(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Context) HasStrictTools() bool { return len(c.toolSchemas) > 0 }
func (c *Context) ValidateChatTools(chat map[string]any) error {
	for _, raw := range jsonx.Slice(chat["choices"]) {
		for _, call := range jsonx.Slice(jsonx.Map(jsonx.Map(raw)["message"])["tool_calls"]) {
			f := jsonx.Map(jsonx.Map(call)["function"])
			if err := c.validateToolArguments(jsonx.String(f["name"]), jsonx.String(f["arguments"])); err != nil {
				return err
			}
		}
	}
	return nil
}

// Names only: do not retain tool descriptions, parameters or result bodies.
func (c *Context) ClientToolNames() map[string]string {
	names := map[string]string{}
	for name, b := range c.bindings {
		original := b.Name
		if b.Namespace != "" {
			original = b.Namespace + "." + original
		}
		names[name] = original
	}
	for name := range c.providerTools {
		names[name] = ""
	}
	return names
}
