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

func responsesToolCallChunk(name, arguments string, finishReason any) map[string]any {
	function := map[string]any{"arguments": arguments}
	if name != "" {
		function["name"] = name
	}
	return map[string]any{"choices": []any{map[string]any{
		"delta": map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "id": "call_1", "function": function,
		}}},
		"finish_reason": finishReason,
	}}}
}

func feedResponsesToolCall(t *testing.T, context *Context, chunks ...map[string]any) []Event {
	t.Helper()
	var builder strings.Builder
	for _, chunk := range chunks {
		raw, err := json.Marshal(chunk)
		if err != nil {
			t.Fatal(err)
		}
		builder.WriteString("data: ")
		builder.Write(raw)
		builder.WriteString("\n\n")
	}
	builder.WriteString("data: [DONE]\n\n")
	return NewStreamAdapter(context).Feed([]byte(builder.String()))
}

func toolCallArgumentsFromEvents(t *testing.T, events []Event) (string, string, string) {
	t.Helper()
	var delta, done, item string
	for _, event := range events {
		switch event.Type {
		case "response.function_call_arguments.delta":
			delta += jsonx.String(event.Data["delta"])
		case "response.function_call_arguments.done":
			done = jsonx.String(event.Data["arguments"])
		case "response.output_item.done":
			output := jsonx.Map(event.Data["item"])
			if jsonx.String(output["type"]) == "function_call" {
				item = jsonx.String(output["arguments"])
			}
		}
	}
	return delta, done, item
}

func parseToolArguments(t *testing.T, value string) map[string]any {
	t.Helper()
	var arguments map[string]any
	if err := json.Unmarshal([]byte(value), &arguments); err != nil {
		t.Fatalf("parse tool arguments %q: %v", value, err)
	}
	return arguments
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

func TestShellCompatEnforceRewritesStreamedArguments(t *testing.T) {
	body := map[string]any{"model": "cline-pass/test", "input": "run", "stream": true, "tools": []any{execCommandTool()}}
	_, context, err := ToChatWithOptions(body, Options{ShellCompat: "powershell", ShellCompatEnforce: true})
	if err != nil {
		t.Fatal(err)
	}
	events := feedResponsesToolCall(t, context,
		responsesToolCallChunk("exec_command", `{"cmd":"Get-ChildItem","workdir":"C:\\tmp","shell":"ba`, nil),
		responsesToolCallChunk("", `sh"}`, "tool_calls"),
	)
	delta, done, item := toolCallArgumentsFromEvents(t, events)
	if delta == "" || delta != done || done != item {
		t.Fatalf("rewritten arguments differed across events:\ndelta=%q\ndone=%q\nitem=%q", delta, done, item)
	}
	arguments := parseToolArguments(t, item)
	if arguments["cmd"] != "Get-ChildItem" || arguments["workdir"] != `C:\tmp` || arguments["shell"] != "powershell" {
		t.Fatalf("unexpected rewritten arguments: %#v", arguments)
	}
	deltas := 0
	for _, event := range events {
		if event.Type != "response.function_call_arguments.delta" {
			continue
		}
		deltas++
		if strings.Contains(jsonx.String(event.Data["delta"]), "bash") {
			t.Fatalf("partial bash arguments leaked to the client: %#v", event.Data)
		}
	}
	if deltas != 1 {
		t.Fatalf("shell arguments should be buffered into one delta, got %d", deltas)
	}
	if terminal := events[len(events)-1]; terminal.Type != "response.completed" {
		t.Fatalf("shell rewrite should complete the turn: %#v", events)
	}
}

func TestShellCompatEnforceAddsMissingShell(t *testing.T) {
	body := map[string]any{"model": "cline-pass/test", "input": "run", "stream": true, "tools": []any{execCommandTool()}}
	_, context, err := ToChatWithOptions(body, Options{ShellCompat: "powershell", ShellCompatEnforce: true})
	if err != nil {
		t.Fatal(err)
	}
	events := feedResponsesToolCall(t, context,
		responsesToolCallChunk("exec_command", `{"cmd":"Get-ChildItem"}`, "tool_calls"),
	)
	_, done, item := toolCallArgumentsFromEvents(t, events)
	if item != done || parseToolArguments(t, item)["shell"] != "powershell" {
		t.Fatalf("missing shell was not added: done=%q item=%q", done, item)
	}
}

func TestShellCompatEnforceLeavesToolsWithoutShellAlone(t *testing.T) {
	readTool := map[string]any{
		"type": "function", "name": "read_file", "description": "read",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
			"required": []any{"path"},
		},
	}
	body := map[string]any{"model": "cline-pass/test", "input": "read", "stream": true, "tools": []any{readTool}}
	_, context, err := ToChatWithOptions(body, Options{ShellCompat: "powershell", ShellCompatEnforce: true})
	if err != nil {
		t.Fatal(err)
	}
	events := feedResponsesToolCall(t, context,
		responsesToolCallChunk("read_file", `{"path":"x","shell":"bash"}`, "tool_calls"),
	)
	_, done, item := toolCallArgumentsFromEvents(t, events)
	if done != item || parseToolArguments(t, item)["shell"] != "bash" {
		t.Fatalf("tool without a shell schema was rewritten: done=%q item=%q", done, item)
	}
}

func TestShellCompatEnforceRequiresSwitch(t *testing.T) {
	body := map[string]any{"model": "cline-pass/test", "input": "run", "stream": true, "tools": []any{execCommandTool()}}
	_, context, err := ToChatWithOptions(body, Options{ShellCompat: "powershell"})
	if err != nil {
		t.Fatal(err)
	}
	events := feedResponsesToolCall(t, context,
		responsesToolCallChunk("exec_command", `{"cmd":"Get-ChildItem","shell":"bash"}`, "tool_calls"),
	)
	_, done, item := toolCallArgumentsFromEvents(t, events)
	if done != item || parseToolArguments(t, item)["shell"] != "bash" {
		t.Fatalf("enforcement changed arguments while disabled: done=%q item=%q", done, item)
	}
}

func TestFromChatShellCompatEnforceRewritesArguments(t *testing.T) {
	body := map[string]any{"model": "cline-pass/test", "input": "run", "tools": []any{execCommandTool()}}
	_, context, err := ToChatWithOptions(body, Options{ShellCompat: "powershell", ShellCompatEnforce: true})
	if err != nil {
		t.Fatal(err)
	}
	response, err := FromChat(map[string]any{
		"choices": []any{map[string]any{
			"finish_reason": "tool_calls",
			"message": map[string]any{"tool_calls": []any{map[string]any{
				"id": "call_1",
				"function": map[string]any{
					"name": "exec_command", "arguments": `{"cmd":"Get-ChildItem","shell":"bash"}`,
				},
			}}},
		}},
	}, context)
	if err != nil {
		t.Fatal(err)
	}
	output := jsonx.Slice(response["output"])
	if len(output) != 1 {
		t.Fatalf("unexpected output: %#v", output)
	}
	item := jsonx.Map(output[0])
	if jsonx.String(item["type"]) != "function_call" ||
		parseToolArguments(t, jsonx.String(item["arguments"]))["shell"] != "powershell" {
		t.Fatalf("buffered response was not rewritten: %#v", item)
	}
}
