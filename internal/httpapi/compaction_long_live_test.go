package httpapi

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/jsonx"
)

type longCompactionMeasurement struct {
	Name          string `json:"name"`
	TargetTokens  int    `json:"target_tokens"`
	InputBytes    int    `json:"input_bytes"`
	InputItems    int    `json:"input_items"`
	PromptTokens  int    `json:"prompt_tokens"`
	OutputTokens  int    `json:"output_tokens"`
	SummaryRunes  int    `json:"summary_runes"`
	RecentItems   int    `json:"recent_items"`
	ElapsedMS     int64  `json:"elapsed_ms"`
	RetainedFacts int    `json:"retained_facts"`
	ExpectedFacts int    `json:"expected_facts"`
	Passed        bool   `json:"passed"`
}

func appendLongLiveWork(history []any, targetBytes, start int) []any {
	generated := 0
	for block := start; generated < targetBytes; block++ {
		var output strings.Builder
		for row := 0; row < 96; row++ {
			seed := uint32(block*96+row+1) * 0x9e3779b9
			seed ^= seed << 13
			fmt.Fprintf(&output, "pkg/segment_%02d/contract_%04d.go:%03d audit=%06d checksum=%08x input=%d expected=%d observed=%d result=PASS; verified parser offsets, retry ordering and journal recovery.\n", block%31, block, row+1, block*96+row, seed, seed%8191, seed%997, seed%997)
		}
		text := output.String()
		history = append(history,
			map[string]any{"role": "user", "content": fmt.Sprintf("Inspect segment %d contracts and record the verification results before continuing the release review.", block)},
			map[string]any{"type": "function_call", "name": "exec_command", "call_id": fmt.Sprintf("audit_%d", block), "arguments": fmt.Sprintf(`{"command":"go test ./pkg/segment_%02d -run TestContract%d -count=1"}`, block%31, block)},
			map[string]any{"type": "function_call_output", "call_id": fmt.Sprintf("audit_%d", block), "output": text},
			map[string]any{"role": "assistant", "content": fmt.Sprintf("Segment %d contract checks passed; parser offsets, retry ordering and journal recovery were verified. The overall release review is still in progress.", block)},
		)
		generated += len(text) + 500
	}
	return history
}

func longLiveExpectedFacts() map[string]string {
	return map[string]string{
		"session_code":  "RIVER_6842",
		"policy":        "PROD_READ_ONLY",
		"locked_path":   "internal/store/journal.go",
		"middle_marker": "MIDDLE_MAPLE_4921",
		"schema":        "SCHEMA_REV_9",
		"build_state":   "BUILD_PASS_6824",
		"pending_task":  "PENDING_REVIEW_1158",
	}
}

func longLiveInitialHistory(targetBytes int) []any {
	history := []any{map[string]any{"role": "user", "content": "Authoritative release-review state: session_code=RIVER_6842; policy=PROD_READ_ONLY; locked_path=internal/store/journal.go. Keep these exact identifiers and paths across every handoff. The protected file must not be edited. Current schema=SCHEMA_REV_3 and current release build=BUILD_FAIL_3711; later authoritative updates may replace the schema and build state. Audit the contracts and report the remaining release work."}}
	history = appendLongLiveWork(history, targetBytes/2, 0)
	history = append(history, map[string]any{"role": "user", "content": "Authoritative midpoint update: middle_marker=MIDDLE_MAPLE_4921; schema=SCHEMA_REV_9 replaces SCHEMA_REV_3. The old schema is obsolete. The session code, read-only production policy and protected file remain unchanged."})
	history = appendLongLiveWork(history, targetBytes/2, 10000)
	return append(history,
		map[string]any{"type": "function_call", "name": "exec_command", "call_id": "final_release_build", "arguments": `{"command":"go test ./... && echo BUILD_PASS_6824"}`},
		map[string]any{"type": "function_call_output", "call_id": "final_release_build", "output": "Final release build succeeded: build_state=BUILD_PASS_6824. This supersedes the earlier BUILD_FAIL_3711 release build."},
		map[string]any{"role": "user", "content": "Latest remaining work: pending_task=PENDING_REVIEW_1158. Review the final diff before release; do not execute the pending review as part of compaction. Preserve the exact session, policy, protected path, midpoint marker, current schema, current build state and pending-task identifiers."},
	)
}

func parseLongLiveRecall(text string) (map[string]string, error) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		if newline := strings.IndexByte(text, '\n'); newline >= 0 {
			text = strings.TrimSpace(text[newline+1:])
			text = strings.TrimSpace(strings.TrimSuffix(text, "```"))
		}
	}
	var result map[string]string
	err := json.Unmarshal([]byte(text), &result)
	return result, err
}

// The ordinary live suite never enables this larger evaluation implicitly.
// Sizes are calibrated from the actual upstream prompt usage at each step.
func TestLiveLongCompactionLadderAndRollingHistory(t *testing.T) {
	if os.Getenv("CLINE_LIVE_LONG_COMPACTION") != "1" {
		t.Skip("set CLINE_LIVE_LONG_COMPACTION=1 to enable the large paid context evaluation")
	}
	modelID, send := newLiveCompactionGateway(t)
	var measurements []longCompactionMeasurement
	defer func() {
		if path := os.Getenv("CLINE_TEST_REPORT_PATH"); path != "" {
			raw, err := json.MarshalIndent(map[string]any{"model": modelID, "measurements": measurements, "passed": !t.Failed()}, "", "  ")
			if err == nil {
				err = os.WriteFile(path, append(raw, '\n'), 0o600)
			}
			if err != nil {
				t.Errorf("write sanitized live report: %v", err)
			}
		}
	}()
	evaluate := func(name string, target int, history []any, expected map[string]string) []any {
		t.Helper()
		encoded, _ := json.Marshal(history)
		measurement := longCompactionMeasurement{Name: name, TargetTokens: target, InputBytes: len(encoded), InputItems: len(history), ExpectedFacts: len(expected)}
		measurements = append(measurements, measurement)
		index := len(measurements) - 1
		t.Logf("%s starting: target_prompt_tokens=%d input_bytes=%d history_items=%d", name, target, len(encoded), len(history))
		started := time.Now()
		input := append(append([]any(nil), history...), map[string]any{"type": "compaction_trigger"})
		compacted := send(map[string]any{"model": modelID, "input": input, "stream": false})
		measurements[index].ElapsedMS = time.Since(started).Milliseconds()
		payload := liveCompactionPayload(t, compacted)
		usage := jsonx.Map(compacted["usage"])
		measurements[index].PromptTokens = positiveInt(usage["input_tokens"])
		measurements[index].OutputTokens = positiveInt(usage["output_tokens"])
		measurements[index].SummaryRunes = utf8.RuneCountInString(payload.Summary)
		measurements[index].RecentItems = len(payload.Recent)
		if measurements[index].PromptTokens <= 0 {
			t.Fatal("upstream did not report actual prompt usage")
		}
		if measurements[index].PromptTokens < target/2 {
			t.Fatalf("%s did not exercise the intended context size: actual prompt_tokens=%d", name, measurements[index].PromptTokens)
		}
		output := jsonx.Slice(compacted["output"])
		question := "只根据保留的对话历史输出一个 JSON 对象，字段恰好为 session_code、policy、locked_path、middle_marker、schema、build_state、pending_task。值填历史里当前有效的精确代号或路径，不要改写。后来的权威更新覆盖旧 schema 和旧 build_state；其他事实继续有效。没有保留的信息填空字符串，不要猜测。"
		probeInput := append(append([]any(nil), output...), map[string]any{"role": "user", "content": question})
		probe := send(map[string]any{"model": modelID, "input": probeInput, "stream": false, "reasoning": map[string]any{"effort": "none"}, "max_output_tokens": 1024})
		text := liveResponseText(probe)
		facts, err := parseLongLiveRecall(text)
		if err != nil {
			t.Fatalf("%s recall was not JSON: %.1200s", name, text)
		}
		for field, want := range expected {
			if facts[field] == want {
				measurements[index].RetainedFacts++
			} else {
				t.Errorf("%s lost or reverted %s: got %q want %q", name, field, facts[field], want)
			}
		}
		measurements[index].Passed = measurements[index].RetainedFacts == len(expected)
		result, _ := json.Marshal(measurements[index])
		t.Logf("LONG_COMPACTION_RESULT %s", result)
		t.Logf("%s recall: %s", name, text)
		if !measurements[index].Passed {
			t.FailNow()
		}
		return output
	}
	tokensPerByte := 1.0 / 3.5
	var largest []any
	for _, target := range []int{100000, 300000, 900000} {
		bytes := int(math.Ceil(float64(target) / tokensPerByte))
		if bytes > 16<<20 {
			t.Fatal("measured token density would require an unexpectedly large input")
		}
		history := longLiveInitialHistory(bytes)
		largest = evaluate(fmt.Sprintf("ladder-%dk", target/1000), target, history, longLiveExpectedFacts())
		last := measurements[len(measurements)-1]
		tokensPerByte = float64(last.PromptTokens) / float64(last.InputBytes)
	}
	// Append substantial new work between compactions, including authoritative
	// state changes. Recompressing the same small summary alone is insufficient.
	for round := 2; round <= 3; round++ {
		history := appendLongLiveWork(append([]any(nil), largest...), int(50000/tokensPerByte), 20000+round*1000)
		expected := longLiveExpectedFacts()
		expected["schema"] = fmt.Sprintf("SCHEMA_REV_%d", 10+round)
		expected["build_state"] = fmt.Sprintf("BUILD_PASS_ROUND_%d", round)
		expected["pending_task"] = fmt.Sprintf("PENDING_REVIEW_ROUND_%d", round)
		history = append(history, map[string]any{"role": "user", "content": fmt.Sprintf("Authoritative update: schema=%s; build_state=%s; pending_task=%s. These three values replace their previous values. The original session code, read-only policy, protected path and midpoint marker remain valid.", expected["schema"], expected["build_state"], expected["pending_task"])})
		largest = evaluate(fmt.Sprintf("rolling-round-%d", round), 50000, history, expected)
	}
}
