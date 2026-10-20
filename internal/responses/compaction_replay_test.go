package responses

import (
	"slices"
	"strings"
	"testing"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/jsonx"
)

func compactionReplayMessages(chat map[string]any) []string {
	var result []string
	for _, raw := range jsonx.Slice(chat["messages"]) {
		message := jsonx.Map(raw)
		role := jsonx.String(message["role"])
		if role != "system" {
			result = append(result, role+":"+strings.TrimSpace(collectPartText(message["content"])))
		}
	}
	return result
}

func TestCompactionRoundTripPreservesTurnOrder(t *testing.T) {
	body := map[string]any{"model": "test", "input": []any{
		map[string]any{"role": "user", "content": "first request"},
		map[string]any{"role": "assistant", "content": "first answer"},
		map[string]any{"role": "user", "content": "latest request"},
		map[string]any{"role": "assistant", "content": "latest answer"},
	}}
	_, context, err := ToCompactionChatWithOptions(body, Options{RecentCompactionTokens: 16000})
	if err != nil {
		t.Fatal(err)
	}
	summarized := map[string]any{"choices": []any{map[string]any{
		"message": map[string]any{"content": "summary"}, "finish_reason": "stop",
	}}}
	for _, degraded := range []bool{false, true} {
		name := "completed"
		if degraded {
			name = "degraded"
		}
		t.Run(name, func(t *testing.T) {
			var compacted map[string]any
			if degraded {
				compacted = DegradedCompactionResponse(context, "summary failed", "")
			} else {
				var err error
				compacted, err = CompactionResponse(summarized, context)
				if err != nil {
					t.Fatal(err)
				}
			}
			input := append(slices.Clone(jsonx.Slice(compacted["output"])), map[string]any{"role": "user", "content": "continue"})
			replayed, _, err := ToChat(map[string]any{"model": "test", "input": input})
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"user:first request", "assistant:first answer", "user:latest request", "assistant:latest answer", "user:continue"}
			if got := compactionReplayMessages(replayed); !slices.Equal(got, want) {
				t.Fatalf("compaction reordered or duplicated turns:\ngot  %v\nwant %v", got, want)
			}
		})
	}
}

func TestCompactionReplayMatchesOccurrencesBeforeItsBoundary(t *testing.T) {
	for _, kind := range []string{"compaction", "context_compaction"} {
		t.Run(kind, func(t *testing.T) {
			envelope := encodeCompactionPayload(CompactionPayload{Summary: "summary", Recent: []CompactionRecentMessage{
				{Role: "user", Text: "continue"}, {Role: "assistant", Text: "first answer"},
				{Role: "user", Text: "continue"}, {Role: "assistant", Text: "latest answer"},
			}})
			chat, _, err := ToChat(map[string]any{"model": "test", "input": []any{
				map[string]any{"role": "user", "content": "continue"},
				map[string]any{"role": "user", "content": "continue"},
				map[string]any{"role": "user", "content": "continue"},
				map[string]any{"type": kind, "encrypted_content": envelope},
				map[string]any{"role": "user", "content": "continue"},
			}})
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"user:continue", "user:continue", "assistant:first answer", "user:continue", "assistant:latest answer", "user:continue"}
			if got := compactionReplayMessages(chat); !slices.Equal(got, want) {
				t.Fatalf("repeated requests lost their order or multiplicity:\ngot  %v\nwant %v", got, want)
			}
		})
	}
}

func TestCompactionReplayKeepsOriginalMultimodalUser(t *testing.T) {
	const imageURL = "data:image/png;base64,aGVsbG8="
	envelope := encodeCompactionPayload(CompactionPayload{Summary: "summary", Recent: []CompactionRecentMessage{
		{Role: "user", Text: "inspect this image"}, {Role: "assistant", Text: "image answer"},
	}})
	chat, _, err := ToChat(map[string]any{"model": "test", "input": []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "input_text", "text": "inspect this image"},
			map[string]any{"type": "input_image", "image_url": imageURL},
		}},
		map[string]any{"type": "compaction", "encrypted_content": envelope},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := compactionReplayMessages(chat); !slices.Equal(got, []string{"user:inspect this image", "assistant:image answer"}) {
		t.Fatalf("multimodal user was duplicated or reordered: %v", got)
	}
	messages := jsonx.Slice(chat["messages"])
	parts := jsonx.Slice(jsonx.Map(messages[1])["content"])
	if len(parts) != 2 || jsonx.String(jsonx.Map(jsonx.Map(parts[1])["image_url"])["url"]) != imageURL {
		t.Fatalf("matching the tail replaced the original image with text: %#v", messages)
	}
}

func TestCompactionReplayWithoutAnchorsFollowsEarlierHistory(t *testing.T) {
	for _, recent := range [][]CompactionRecentMessage{
		{{Role: "user", Text: "recent request"}, {Role: "assistant", Text: "recent answer"}},
		{{Role: "assistant", Text: "recent answer"}}, // Older envelopes may contain only the answer.
	} {
		envelope := encodeCompactionPayload(CompactionPayload{Summary: "summary", Recent: recent})
		chat, _, err := ToChat(map[string]any{"model": "test", "input": []any{
			map[string]any{"role": "user", "content": "earlier request"},
			map[string]any{"type": "compaction", "encrypted_content": envelope},
			map[string]any{"role": "user", "content": "new request"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"user:earlier request"}
		for _, message := range recent {
			want = append(want, message.Role+":"+message.Text)
		}
		want = append(want, "user:new request")
		if got := compactionReplayMessages(chat); !slices.Equal(got, want) {
			t.Fatalf("unmatched tail moved outside its history boundary:\ngot  %v\nwant %v", got, want)
		}
	}
}
