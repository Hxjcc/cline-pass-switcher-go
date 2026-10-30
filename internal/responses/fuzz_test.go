package responses

import (
	"encoding/json"
	"testing"
)

func FuzzResponsesConversion(f *testing.F) {
	for _, seed := range []string{`{"model":"test","input":"hello"}`, `{"model":"test","input":[{"type":"function_call_output","call_id":"x","output":"done"}]}`, `{"model":"test","tools":[` + strictToolDefinition + `],"input":"hello"}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 32<<10 {
			t.Skip()
		}
		var body map[string]any
		if json.Unmarshal(data, &body) != nil {
			return
		}
		chat, ctx, err := ToChat(body)
		if err != nil {
			return
		}
		if chat == nil || ctx == nil {
			t.Fatal("successful conversion returned nil")
		}
		if _, err := json.Marshal(chat); err != nil {
			t.Fatal("conversion produced invalid JSON", err)
		}
	})
}

func FuzzCompactionEnvelope(f *testing.F) {
	for _, s := range []string{CompactionEnvelope("summary"), encodeCompactionPayload(CompactionPayload{Summary: "summary", Recent: []CompactionRecentMessage{{Role: "user", Text: "keep this"}}}), "invalid"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if len(value) > 64<<10 {
			t.Skip()
		}
		payload, ok := DecodeCompactionEnvelope(value)
		if !ok {
			return
		}
		second, ok := DecodeCompactionEnvelope(encodeCompactionPayload(payload))
		if !ok || payload.Summary != second.Summary || len(payload.Recent) != len(second.Recent) {
			t.Fatal("compaction payload changed on replay")
		}
	})
}
