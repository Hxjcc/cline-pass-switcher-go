package responses

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/jsonx"
)

func TestOriginalImageDetailAcrossUserToolAndCompactionInputs(t *testing.T) {
	const url = "data:image/png;base64,AAAA"
	for _, source := range []string{"user", "nested-image-url", "base64", "tool"} {
		for _, enabled := range []bool{false, true} {
			for _, compact := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/enabled=%v/compact=%v", source, enabled, compact), func(t *testing.T) {
					image := map[string]any{"type": "input_image", "image_url": url, "detail": "original"}
					if source == "nested-image-url" {
						image = map[string]any{"type": "image_url", "image_url": map[string]any{"url": url, "detail": "original"}}
					}
					if source == "base64" {
						image = map[string]any{"type": "image", "data": "AAAA", "mimeType": "image/png", "detail": "original"}
					}
					input := []any{map[string]any{"role": "user", "content": []any{image}}}
					if source == "tool" {
						input = []any{
							map[string]any{"type": "function_call", "call_id": "image-call", "name": "view_image", "arguments": "{}"},
							map[string]any{"type": "function_call_output", "call_id": "image-call", "output": []any{image}},
						}
					}
					body := map[string]any{"model": "test/image", "input": input}
					before, _ := json.Marshal(body)
					convert := ToChatWithOptions
					if compact {
						convert = ToCompactionChatWithOptions
					}
					chat, _, err := convert(body, Options{SupportsImageDetailOriginal: enabled})
					if err != nil {
						t.Fatal(err)
					}
					want := "high"
					if enabled {
						want = "original"
					}
					images := 0
					for _, message := range jsonx.Slice(chat["messages"]) {
						for _, raw := range jsonx.Slice(jsonx.Map(message)["content"]) {
							part := jsonx.Map(raw)
							if part["type"] != "image_url" {
								continue
							}
							images++
							imageURL := jsonx.Map(part["image_url"])
							if imageURL["url"] != url || imageURL["detail"] != want {
								t.Fatalf("image changed or detail mismatches capability: %+v", imageURL)
							}
						}
					}
					if images != 1 {
						t.Fatalf("got %d images, want exactly one", images)
					}
					after, _ := json.Marshal(body)
					if string(before) != string(after) {
						t.Fatal("image fallback mutated the original request")
					}
				})
			}
		}
	}
}
