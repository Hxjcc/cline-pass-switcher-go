package responses

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/jsonx"
)

func longCompactionHistory() []any {
	var input []any
	for index := 0; index < 40; index++ {
		input = append(input,
			map[string]any{"role": "user", "content": fmt.Sprintf("old request %d ", index) + strings.Repeat("u", 2100)},
			map[string]any{"role": "assistant", "content": fmt.Sprintf("old answer %d ", index) + strings.Repeat("a", 2100)},
		)
	}
	return input
}

func TestCompactionSummarizesTextBeyondTheRecentExcerpt(t *testing.T) {
	for _, role := range []string{"user", "assistant"} {
		t.Run(role, func(t *testing.T) {
			const marker = "KEEP_THE_DATABASE_READ_ONLY"
			input := append(longCompactionHistory(), map[string]any{"role": role, "content": strings.Repeat("x", 2200) + marker})
			chat, context, err := ToCompactionChatWithOptions(map[string]any{"model": "test", "input": input}, Options{RecentCompactionTokens: 16000})
			if err != nil {
				t.Fatal(err)
			}
			prompt, _ := json.Marshal(chat["messages"])
			recent, _ := json.Marshal(context.compactionRecent)
			if !strings.Contains(string(prompt), marker) {
				t.Fatal("the end of the retained long message never reaches the summarizer")
			}
			if strings.Contains(string(recent), marker) {
				t.Fatal("fixture did not exercise text beyond the recent excerpt")
			}
		})
	}
}

func TestCompactionSummarizesImagesInTheRecentTail(t *testing.T) {
	const imageURL = "data:image/png;base64,aGVsbG8="
	input := append(longCompactionHistory(), map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "input_text", "text": "Inspect this screenshot before continuing."},
		map[string]any{"type": "input_image", "image_url": imageURL},
	}})
	chat, _, err := ToCompactionChatWithOptions(map[string]any{"model": "test", "input": input}, Options{RecentCompactionTokens: 16000})
	if err != nil {
		t.Fatal(err)
	}
	prompt, _ := json.Marshal(chat["messages"])
	if !strings.Contains(string(prompt), imageURL) {
		t.Fatal("the image was excluded from both the summary input and the text-only recent tail")
	}
}

func TestCompactionSummaryBoundaryKeepsToolCallsAndResultsTogether(t *testing.T) {
	const marker = "ARGUMENT_AFTER_EXCERPT"
	input := append(longCompactionHistory(),
		map[string]any{"type": "function_call", "name": "exec", "call_id": "large-args", "arguments": `{"command":"` + strings.Repeat("x", 600) + marker + `"}`},
		map[string]any{"type": "function_call_output", "call_id": "large-args", "output": "COMMAND_COMPLETED"},
		map[string]any{"role": "user", "content": "Continue."},
	)
	chat, _, err := ToCompactionChatWithOptions(map[string]any{"model": "test", "input": input}, Options{RecentCompactionTokens: 16000})
	if err != nil {
		t.Fatal(err)
	}
	messages := jsonx.Slice(chat["messages"])
	if err := validateChatToolHistory(messages); err != nil {
		t.Fatalf("summarizing a partial tool group broke Chat history: %v", err)
	}
	prompt, _ := json.Marshal(messages)
	if !strings.Contains(string(prompt), marker) || !strings.Contains(string(prompt), "COMMAND_COMPLETED") {
		t.Fatal("the summarizer did not receive the full call and its result")
	}
}

func TestDegradedCompactionRetainsTheLastSuccessfulSummaryAcrossFailures(t *testing.T) {
	const previous = "LAST_VALID_SUMMARY: never delete the database."
	item := map[string]any{"type": "compaction", "encrypted_content": CompactionEnvelope(previous)}
	for round := 0; round < 3; round++ {
		_, context, err := ToCompactionChatWithOptions(map[string]any{"model": "test", "input": []any{
			item, map[string]any{"role": "user", "content": "Continue."},
		}}, Options{RecentCompactionTokens: 16000})
		if err != nil {
			t.Fatal(err)
		}
		response := DegradedCompactionTriggerResponse(context, "upstream unavailable", "")
		item = jsonx.Map(jsonx.Slice(response["output"])[0])
		payload, ok := DecodeCompactionEnvelope(jsonx.String(item["encrypted_content"]))
		if !ok || !payload.Degraded || payload.PreviousSummary != previous {
			t.Fatalf("round %d lost the last valid summary: %#v", round, payload)
		}
		if strings.Count(payload.Summary, previous) != 1 || strings.Count(payload.Summary, "[compaction degraded]") != 1 {
			t.Fatalf("round %d nested degraded summaries instead of the last valid summary: %s", round, payload.Summary)
		}
	}
}

func TestSuccessfulCompactionReplacesThePreviousFallbackSummary(t *testing.T) {
	const latest = "LATEST_VALID_SUMMARY"
	_, context, err := ToCompactionChatWithOptions(map[string]any{"model": "test", "input": []any{
		map[string]any{"type": "compaction", "encrypted_content": CompactionEnvelope("OLD_SUMMARY")},
		map[string]any{"role": "user", "content": "Continue."},
	}}, Options{RecentCompactionTokens: 16000})
	if err != nil {
		t.Fatal(err)
	}
	success, err := CompactionTriggerResponse(map[string]any{"choices": []any{map[string]any{
		"message": map[string]any{"content": latest}, "finish_reason": "stop",
	}}}, context)
	if err != nil {
		t.Fatal(err)
	}
	_, next, err := ToCompactionChatWithOptions(map[string]any{"model": "test", "input": success["output"]}, Options{RecentCompactionTokens: 16000})
	if err != nil {
		t.Fatal(err)
	}
	if next.compactionPreviousSummary != latest {
		t.Fatalf("the successful summary did not become the new fallback: %q", next.compactionPreviousSummary)
	}
}

func TestCompactionToolTranscriptPrefersLatestActivityInChronologicalOrder(t *testing.T) {
	var messages []any
	for index := 0; index < 20; index++ {
		messages = append(messages, map[string]any{"role": "tool", "tool_call_id": fmt.Sprintf("old-%d", index), "content": strings.Repeat("old output ", 60)})
	}
	messages = append(messages,
		map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"function": map[string]any{"name": "LATEST_COMMAND", "arguments": "{}"}}}},
		map[string]any{"role": "tool", "tool_call_id": "latest", "content": "LATEST_COMMAND_SUCCEEDED"},
	)
	transcript := compactionToolTranscript(messages)
	call, result := strings.Index(transcript, "assistant called LATEST_COMMAND"), strings.Index(transcript, "LATEST_COMMAND_SUCCEEDED")
	if call < 0 || result <= call || strings.Contains(transcript, "tool result (old-0)") {
		t.Fatalf("the transcript did not prioritize the latest activity in order: %s", transcript)
	}
	if len([]rune(transcript)) > 6000 {
		t.Fatal("tool transcript exceeded its total budget")
	}
}
