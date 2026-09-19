package responses

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
)

func execCommandTool() map[string]any {
	return map[string]any{
		"type":        "function",
		"name":        "exec_command",
		"description": "Run a command",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"cmd":     map[string]any{"type": "string"},
				"workdir": map[string]any{"type": "string"},
				"shell":   map[string]any{"type": "string", "enum": []any{"bash", "cmd", "powershell"}},
			},
			"required": []any{"cmd"},
		},
	}
}

func forwardedTool(t *testing.T, chat map[string]any, name string) map[string]any {
	t.Helper()
	for _, raw := range jsonx.Slice(chat["tools"]) {
		function := jsonx.Map(jsonx.Map(raw)["function"])
		if jsonx.String(function["name"]) == name {
			return function
		}
	}
	t.Fatalf("tool %q was not forwarded: %#v", name, chat["tools"])
	return nil
}

func TestShellCompatLocksForwardedShellProperty(t *testing.T) {
	body := map[string]any{"model": "cline-pass/test", "input": "跑个命令", "tools": []any{execCommandTool()}}
	chat, _, err := ToChatWithOptions(body, Options{ShellCompat: "powershell"})
	if err != nil {
		t.Fatal(err)
	}
	parameters := jsonx.Map(forwardedTool(t, chat, "exec_command")["parameters"])
	shell := jsonx.Map(jsonx.Map(parameters["properties"])["shell"])
	enum := jsonx.Slice(shell["enum"])
	if len(enum) != 1 || jsonx.String(enum[0]) != "powershell" {
		t.Fatalf("shell enum was not locked: %#v", shell)
	}
	required := false
	for _, raw := range jsonx.Slice(parameters["required"]) {
		if jsonx.String(raw) == "shell" {
			required = true
		}
	}
	if !required {
		t.Fatalf("shell was not marked required: %#v", parameters["required"])
	}
	// The client's own request body must stay untouched.
	raw, _ := json.Marshal(body["tools"])
	if !strings.Contains(string(raw), `"bash"`) {
		t.Fatalf("original request body was mutated: %s", raw)
	}
}

func TestShellCompatLeavesOtherToolsAlone(t *testing.T) {
	other := map[string]any{
		"type": "function", "name": "read_file", "description": "read",
		"parameters": map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
			"required":   []any{"path"},
		},
	}
	body := map[string]any{"model": "cline-pass/test", "input": "hi", "tools": []any{other}}
	chat, _, err := ToChatWithOptions(body, Options{ShellCompat: "powershell"})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(other["parameters"])
	after, _ := json.Marshal(jsonx.Map(forwardedTool(t, chat, "read_file")["parameters"]))
	if string(before) != string(after) {
		t.Fatalf("a tool without a shell property changed:\n%s\n%s", before, after)
	}
}

func TestShellCompatOffValueKeepsSchema(t *testing.T) {
	chat, _, err := ToChatWithOptions(
		map[string]any{"model": "cline-pass/test", "input": "hi", "tools": []any{execCommandTool()}},
		Options{ShellCompat: "off"},
	)
	if err != nil {
		t.Fatal(err)
	}
	parameters := jsonx.Map(forwardedTool(t, chat, "exec_command")["parameters"])
	shell := jsonx.Map(jsonx.Map(parameters["properties"])["shell"])
	if len(jsonx.Slice(shell["enum"])) != 3 {
		t.Fatalf("off must disable the rewrite: %#v", shell)
	}
}
