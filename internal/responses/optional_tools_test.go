package responses

import (
	"fmt"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
)

func TestOptionalHostedToolsDoNotBlockOrdinaryChat(t *testing.T) {
	for _, kind := range []string{"web_search", "web_search_preview", "web_search_preview_2025_03_11", "file_search", "code_interpreter", "image_generation", "computer", "computer_use_preview", "mcp", "tool_search"} {
		for _, choice := range []any{nil, "auto", "none"} {
			t.Run(fmt.Sprintf("%s/%v", kind, choice), func(t *testing.T) {
				chat, _, err := ToChat(map[string]any{"model": "test", "input": "你好", "tool_choice": choice,
					"parallel_tool_calls": true, "tools": []any{map[string]any{"type": kind, "execution": "server"}}})
				if err != nil {
					t.Fatal(err)
				}
				if chat["tools"] != nil || chat["tool_choice"] != nil || chat["parallel_tool_calls"] != nil {
					t.Fatalf("hosted tool leaked to Chat: %#v", chat)
				}
				if jsonx.Map(jsonx.Slice(chat["messages"])[0])["content"] != "你好" {
					t.Fatal("ordinary input changed")
				}
			})
		}
	}
}

func TestOptionalHostedToolsKeepClientToolsAndRequiredChoice(t *testing.T) {
	chat, _, err := ToChat(map[string]any{"model": "test", "input": []any{
		map[string]any{"role": "user", "content": "你好"},
		map[string]any{"type": "additional_tools", "tools": []any{
			map[string]any{"type": "mcp"},
			map[string]any{"type": "namespace", "name": "editor", "tools": []any{
				map[string]any{"type": "file_search"}, map[string]any{"type": "custom", "name": "patch"},
			}},
		}},
	}, "tools": []any{map[string]any{"type": "web_search"}, map[string]any{"type": "function", "name": "read"}}, "tool_choice": "required"})
	if err != nil {
		t.Fatal(err)
	}
	if len(jsonx.Slice(chat["tools"])) != 2 || chat["tool_choice"] != "required" {
		t.Fatalf("client tool selection changed: %#v", chat)
	}
	chatFunctionByName(t, chat, "read")
	chatFunctionByName(t, chat, "editor__patch")
}
