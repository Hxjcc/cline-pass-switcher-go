package responses

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/jsonx"
)

func TestCompactToolOutputPreservesErrorSignalsAndTail(t *testing.T) {
	var input strings.Builder
	for index := 0; index < 600; index++ {
		fmt.Fprintf(&input, "noise line %03d: ........................................\n", index)
	}
	input.WriteString("--- FAIL: TestCompaction (0.01s)\n")
	input.WriteString("panic: boom\n")
	input.WriteString("expected 42, got 7\n")
	for index := 0; index < 600; index++ {
		fmt.Fprintf(&input, "tail line %03d: ........................................\n", index)
	}

	got := compactToolOutput(input.String(), 3000)
	if len([]rune(got)) > 3000 {
		t.Fatalf("compact tool output exceeded its budget: %d runes", len([]rune(got)))
	}
	if len([]rune(got)) < 2900 {
		t.Fatalf("compact tool output wasted its budget: %d runes", len([]rune(got)))
	}
	for _, expected := range []string{
		"noise line 000", "FAIL: TestCompaction", "panic: boom",
		"expected 42, got 7", "tail line 599", "middle omitted",
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("compact tool output lost %q:\n%s", expected, got)
		}
	}
}

func TestCleanCompactionToolOutputRemovesTerminalNoise(t *testing.T) {
	input := "\x1b[31mERROR\x1b[0m: boom\r\n" +
		"downloading 10%\rdownloading 100%\rDONE\n" +
		"repeat\nrepeat\nrepeat\nrepeat\nrepeat\nrepeat\nrepeat\nrepeat\n\n\n\n" +
		"\x1b]0;window title\aend"
	got := cleanCompactionToolOutput(input)
	if strings.ContainsAny(got, "\x1b\a\r") {
		t.Fatalf("terminal controls survived cleaning: %q", got)
	}
	if strings.Contains(got, "downloading 10%") {
		t.Fatalf("obsolete progress frame survived: %q", got)
	}
	if strings.Count(got, "repeat") != 3 || !strings.Contains(got, "5 identical lines omitted") {
		t.Fatalf("consecutive duplicate lines were not collapsed: %q", got)
	}
	if strings.Contains(got, "\n\n\n") {
		t.Fatalf("repeated blank lines survived: %q", got)
	}
	for _, expected := range []string{"ERROR: boom", "DONE", "end"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("cleaned output lost %q: %q", expected, got)
		}
	}
}

func TestCompactToolOutputHonoursSmallBudgets(t *testing.T) {
	value := strings.Repeat("x", 5000)
	for _, limit := range []int{0, 1, 20, 100, 500, 2000} {
		got := compactToolOutput(value, limit)
		if len([]rune(got)) > limit {
			t.Fatalf("limit %d returned %d runes", limit, len([]rune(got)))
		}
	}
}

func TestCompactToolOutputKeepsChineseFailure(t *testing.T) {
	input := strings.Repeat("普通日志行\n", 2000) +
		"错误：数据库连接超时\n" +
		strings.Repeat("尾部日志行\n", 2000)
	got := compactToolOutput(input, 2000)
	if !strings.Contains(got, "错误：数据库连接超时") {
		t.Fatalf("Chinese failure marker was lost: %q", got)
	}
}

func TestCompactionToolBudgetsPreferNewestResults(t *testing.T) {
	oldestOutput := strings.Repeat("oldest-output ", 2000)
	oldOutput := strings.Repeat("old-output ", 2000)
	newOutput := strings.Repeat("new-output ", 2000)
	messages := []any{
		map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{
			map[string]any{"id": "oldest", "type": "function", "function": map[string]any{"name": "exec", "arguments": "{}"}},
		}},
		map[string]any{"role": "tool", "tool_call_id": "oldest", "content": oldestOutput},
		map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{
			map[string]any{"id": "old", "type": "function", "function": map[string]any{"name": "exec", "arguments": "{}"}},
		}},
		map[string]any{"role": "tool", "tool_call_id": "old", "content": oldOutput},
		map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{
			map[string]any{"id": "new", "type": "function", "function": map[string]any{"name": "exec", "arguments": "{}"}},
		}},
		map[string]any{"role": "tool", "tool_call_id": "new", "content": newOutput},
	}

	tail := selectCompactionTail(messages, 100000)
	var oldestText, oldText, newText string
	for _, message := range tail {
		switch {
		case strings.HasPrefix(message.Text, "[tool result oldest]"):
			oldestText = message.Text
		case strings.HasPrefix(message.Text, "[tool result old]"):
			oldText = message.Text
		case strings.HasPrefix(message.Text, "[tool result new]"):
			newText = message.Text
		}
	}
	if oldestText == "" || oldText == "" || newText == "" {
		t.Fatalf("tool results missing from tail: %#v", tail)
	}
	if !(len([]rune(newText)) > len([]rune(oldText)) && len([]rune(oldText)) > len([]rune(oldestText))) {
		t.Fatalf("tool budgets are not recency-weighted: oldest=%d old=%d new=%d",
			len([]rune(oldestText)), len([]rune(oldText)), len([]rune(newText)))
	}
	if len([]rune(newText)) > maxCompactionLatestToolResultRunes+40 {
		t.Fatalf("newest tool result exceeded its budget: %d", len([]rune(newText)))
	}
	if len([]rune(oldText)) > maxCompactionSecondToolResultRunes+40 {
		t.Fatalf("older tool result exceeded its budget: %d", len([]rune(oldText)))
	}
	if len([]rune(oldestText)) > maxCompactionOlderToolResultRunes+40 {
		t.Fatalf("oldest tool result exceeded its budget: %d", len([]rune(oldestText)))
	}
}

func TestCompactionReplayKeepsToolOutputBudget(t *testing.T) {
	replayed := "[tool result call_1]\n" + strings.Repeat("output ", 5000)
	tail := selectCompactionTail([]any{
		map[string]any{"role": "user", "content": replayed},
	}, 100000)
	if len(tail) != 1 {
		t.Fatalf("replayed tool result was dropped: %#v", tail)
	}
	if len([]rune(tail[0].Text)) <= maxCompactionRecentMessageRunes {
		t.Fatalf("replayed tool result was truncated as ordinary user text: %d runes", len([]rune(tail[0].Text)))
	}
}

func TestCompactionEnvelopeKeepsLargeToolFailure(t *testing.T) {
	var output strings.Builder
	for index := 0; index < 800; index++ {
		fmt.Fprintf(&output, "INFO step=%d ................................................\n", index)
	}
	output.WriteString("--- FAIL: TestImportantCase\n")
	output.WriteString("panic: expected 42, got 7\n")
	for index := 0; index < 800; index++ {
		fmt.Fprintf(&output, "INFO tail=%d ................................................\n", index)
	}
	body := map[string]any{
		"model": "test",
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": "Run the tests."},
			map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call_big", "arguments": `{"cmd":"go test ./..."}`},
			map[string]any{"type": "function_call_output", "call_id": "call_big", "output": output.String()},
			map[string]any{"type": "message", "role": "assistant", "content": "Checking the failure."},
		},
	}
	_, context, err := ToCompactionChatWithOptions(body, Options{RecentCompactionTokens: 16000})
	if err != nil {
		t.Fatal(err)
	}
	response, err := CompactionResponse(map[string]any{
		"choices": []any{map[string]any{
			"message":       map[string]any{"content": "## Objective\nfix\n\n## Work State\nfailed\n\n## Next Move\nretry\n\n## Relevant Files\nunknown"},
			"finish_reason": "stop",
		}},
	}, context)
	if err != nil {
		t.Fatal(err)
	}
	items := jsonx.Slice(response["output"])
	item := jsonx.Map(items[len(items)-1])
	payload, ok := DecodeCompactionEnvelope(jsonx.String(item["encrypted_content"]))
	if !ok {
		t.Fatal("compaction envelope did not decode")
	}
	var toolText string
	for _, message := range payload.Recent {
		if strings.HasPrefix(message.Text, "[tool result call_big]") {
			toolText = message.Text
			break
		}
	}
	if toolText == "" {
		t.Fatalf("large tool result missing from envelope: %#v", payload.Recent)
	}
	if !strings.Contains(toolText, "panic: expected 42, got 7") {
		t.Fatalf("failure in the middle of the tool output was lost: %q", toolText)
	}
	if !strings.Contains(toolText, "middle omitted") {
		t.Fatalf("tool output was not visibly truncated: %q", toolText)
	}
	if len([]rune(toolText)) < maxCompactionLatestToolResultRunes-20 {
		t.Fatalf("latest tool result wasted its budget: %d runes", len([]rune(toolText)))
	}
}
