package upstream

import (
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

type bytewiseReader struct {
	data []byte
	pos  int
}

func (reader *bytewiseReader) Read(buffer []byte) (int, error) {
	if reader.pos >= len(reader.data) {
		return 0, io.EOF
	}
	buffer[0] = reader.data[reader.pos]
	reader.pos++
	return 1, nil
}

func TestReadSSEHeadHandlesCommentsAndBytewiseChunks(t *testing.T) {
	reader := &bytewiseReader{data: []byte(": keep-alive\r\n\r\ndata: {\"choices\":[]}\r\n\r\nrest")}
	raw, payload, err := readSSEHead(reader)
	if err != nil {
		t.Fatal(err)
	}
	if payload != `{"choices":[]}` {
		t.Fatalf("unexpected first payload: %q", payload)
	}
	if len(raw) == 0 {
		t.Fatal("expected buffered stream head")
	}
}

func TestReadSSEHeadAcceptsLargeFirstEvent(t *testing.T) {
	payload := `{"choices":[{"delta":{"content":"` + strings.Repeat("x", 64<<10) + `"},"finish_reason":null}]}`
	raw, got, err := readSSEHead(strings.NewReader("data: " + payload + "\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got != payload {
		t.Fatalf("large first payload changed: got %d bytes", len(got))
	}
	if len(raw) <= 32<<10 {
		t.Fatalf("test payload did not cross the old head cap: %d bytes", len(raw))
	}
}

func TestReadSSEHeadRecognizesEveryLineEnding(t *testing.T) {
	const payload = `{"choices":[{"delta":{"content":"ok"}}]}`
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		for _, bytewise := range []bool{false, true} {
			t.Run(fmt.Sprintf("ending=%q/bytewise=%v", ending, bytewise), func(t *testing.T) {
				wire := ": comment" + ending + ending + "data: " + payload + ending + ending + "data: [DONE]" + ending + ending
				var reader io.Reader = strings.NewReader(wire)
				if bytewise {
					reader = &bytewiseReader{data: []byte(wire)}
				}
				_, got, err := readSSEHead(reader)
				if err != nil || got != payload {
					t.Fatalf("SSE head framing changed: got=%q err=%v", got, err)
				}
				if got, ok := firstSSEPayload([]byte(wire), false); !ok || got != payload {
					t.Fatalf("first payload framing changed: %q %v", got, ok)
				}
			})
		}
	}
}

func TestParseModelCapabilityReadsReasoningAndVision(t *testing.T) {
	capability := parseModelCapability(map[string]any{
		"name":              "DeepSeek V4.1 Flash",
		"reasoning":         true,
		"attachment":        true,
		"tool_call":         true,
		"structured_output": true,
		"temperature":       true,
		"reasoning_options": []any{map[string]any{
			"type": "effort", "values": []any{"none", "low", "medium", "high", "xhigh"},
		}},
		"modalities": map[string]any{"input": []any{"text", "image"}, "output": []any{"text"}},
		"limit":      map[string]any{"context": float64(1_000_000), "output": float64(384_000)},
	}, 123)
	if !capability.CapabilitiesKnown || !capability.Reasoning || !capability.Attachment {
		t.Fatalf("capability flags missing: %#v", capability)
	}
	if len(capability.ReasoningEfforts) != 5 || capability.ReasoningEfforts[4] != "xhigh" {
		t.Fatalf("unexpected reasoning efforts: %#v", capability.ReasoningEfforts)
	}
	if len(capability.InputModalities) != 2 || capability.InputModalities[1] != "image" {
		t.Fatalf("unexpected input modalities: %#v", capability.InputModalities)
	}
	if capability.ContextWindow != 1_000_000 || capability.OutputLimit != 384_000 {
		t.Fatalf("unexpected limits: %#v", capability)
	}
}

func TestNormalizeModelCapabilityUsesDistinctTiers(t *testing.T) {
	tests := []struct {
		modelID string
		want    []string
	}{
		{"cline-pass/deepseek-v4-flash", []string{"none", "low", "high", "max"}},
		{"cline-pass/deepseek-v4.1-flash", []string{"none", "low", "high", "max"}},
		{"cline-pass/deepseek-v4-pro", []string{"none", "low", "high", "max"}},
		{"cline-pass/glm-5.2", []string{"none", "high", "max"}},
		{"cline-pass/glm-5.3", []string{"low", "high", "max"}},
		{"cline-pass/glm-5.3-flash", []string{"low", "high", "max"}},
		{"cline-pass/kimi-k3", []string{"low", "high", "max"}},
		{"cline-pass/qwen3.8-max", []string{"none", "low", "medium", "xhigh"}},
		{"cline-pass/kimi-k2.6", []string{"none", "high"}},
		{"cline-pass/minimax-m3", []string{"none", "high"}},
		{"cline-pass/mimo-v2.5", []string{"none", "high"}},
		{"cline-pass/mimo-v2.5-pro", []string{"none", "high"}},
	}
	for _, test := range tests {
		t.Run(test.modelID, func(t *testing.T) {
			capability := normalizeModelCapability(test.modelID, model.ModelMeta{
				Reasoning:        true,
				ReasoningEfforts: []string{"none", "minimal", "low", "medium", "high", "xhigh"},
			})
			if !reflect.DeepEqual(capability.ReasoningEfforts, test.want) {
				t.Fatalf("unexpected normalized efforts: got %#v want %#v", capability.ReasoningEfforts, test.want)
			}
		})
	}
}
