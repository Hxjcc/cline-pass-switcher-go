package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/jsonx"
	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
	responsesbridge "github.com/Hxjcc/cline-pass-switcher-go/internal/responses"
)

// These opt-in evaluations make real model requests and are skipped by the
// normal suite. Credentials stay in the proxy's memory; the test store only
// contains a placeholder account key.
func newLiveCompactionGateway(t *testing.T) (string, func(map[string]any) map[string]any) {
	t.Helper()
	if os.Getenv("CLINE_LIVE_COMPACTION") != "1" {
		t.Skip("set CLINE_LIVE_COMPACTION=1 and the three CLINE_TEST_* variables to run paid live evaluation")
	}
	base, key, modelID := strings.TrimRight(os.Getenv("CLINE_TEST_BASE_URL"), "/"), os.Getenv("CLINE_TEST_API_KEY"), os.Getenv("CLINE_TEST_MODEL")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || key == "" || modelID == "" {
		t.Fatal("CLINE_TEST_BASE_URL must be an HTTPS API base, and CLINE_TEST_API_KEY / CLINE_TEST_MODEL are required")
	}
	timeout := 150 * time.Second
	if raw := os.Getenv("CLINE_TEST_TIMEOUT_SECONDS"); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil || seconds < 30 || seconds > 600 {
			t.Fatal("CLINE_TEST_TIMEOUT_SECONDS must be between 30 and 600")
		}
		timeout = time.Duration(seconds) * time.Second
	}
	client := &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var requests, promptTokens, outputTokens, costMicroUSD atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read live proxy request", http.StatusBadGateway)
			return
		}
		var check struct{ Model string }
		if json.Unmarshal(body, &check) != nil || check.Model != modelID {
			http.Error(w, "live evaluation model mismatch", http.StatusBadRequest)
			return
		}
		request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			http.Error(w, "construct live proxy request", http.StatusBadGateway)
			return
		}
		request.Header.Set("Authorization", "Bearer "+key)
		request.Header.Set("Content-Type", "application/json")
		call := requests.Add(1)
		started := time.Now()
		response, err := client.Do(request)
		if err != nil {
			http.Error(w, "live model transport failed", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 16<<20))
		raw = bytes.ReplaceAll(raw, []byte(key), []byte("[redacted]"))
		var billing struct {
			Usage struct {
				Prompt  int64   `json:"prompt_tokens"`
				Output  int64   `json:"completion_tokens"`
				Cost    float64 `json:"cost"`
				Details struct {
					Cached int64 `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
			}
		}
		if json.Unmarshal(raw, &billing) == nil {
			promptTokens.Add(billing.Usage.Prompt)
			outputTokens.Add(billing.Usage.Output)
			costMicroUSD.Add(int64(math.Round(billing.Usage.Cost * 1e6)))
			t.Logf("live call=%d status=%d prompt_tokens=%d cached_tokens=%d completion_tokens=%d elapsed_ms=%d cost_usd=%.8f", call, response.StatusCode, billing.Usage.Prompt, billing.Usage.Details.Cached, billing.Usage.Output, time.Since(started).Milliseconds(), billing.Usage.Cost)
		}
		if response.StatusCode != http.StatusOK {
			t.Logf("live upstream HTTP %d: %.400s", response.StatusCode, raw)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(raw)
	}))
	t.Cleanup(proxy.Close)
	st, server := newTestServer(t)
	configureKeys(t, st, proxy.URL, func(c *model.Config) { c.Accounts[0].Key = "live-test-placeholder" })
	if _, err := st.UpdateModelMeta(modelID, func(meta *model.ModelMeta) { meta.ReasoningEfforts = []string{"none", "max"} }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		t.Logf("live calls=%d, prompt_tokens=%d, completion_tokens=%d, upstream_reported_cost_usd=%.6f", requests.Load(), promptTokens.Load(), outputTokens.Load(), float64(costMicroUSD.Load())/1e6)
	})
	send := func(body map[string]any) map[string]any {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout+30*time.Second)
		defer cancel()
		request := localRequest(http.MethodPost, "/v1/responses", bytes.NewReader(raw)).WithContext(ctx)
		writer := httptest.NewRecorder()
		server.ServeHTTP(writer, request)
		if writer.Code != http.StatusOK {
			t.Fatalf("live request returned HTTP %d", writer.Code)
		}
		var response map[string]any
		if err := json.Unmarshal(writer.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	return modelID, send
}

func liveCompactionHistory() []any {
	var history []any
	for index := 0; index < 40; index++ {
		history = append(history,
			map[string]any{"role": "user", "content": fmt.Sprintf("Old completed task %d: ", index) + strings.Repeat("background detail ", 145)},
			map[string]any{"role": "assistant", "content": fmt.Sprintf("Old verified result %d: ", index) + strings.Repeat("completed detail ", 145)},
		)
	}
	return history
}

func liveResponseText(response map[string]any) string {
	var text strings.Builder
	for _, raw := range jsonx.Slice(response["output"]) {
		for _, part := range jsonx.Slice(jsonx.Map(raw)["content"]) {
			text.WriteString(jsonx.String(jsonx.Map(part)["text"]))
		}
	}
	return text.String()
}

func liveCompactionPayload(t *testing.T, response map[string]any) responsesbridge.CompactionPayload {
	t.Helper()
	output := jsonx.Slice(response["output"])
	if len(output) != 1 || jsonx.String(jsonx.Map(output[0])["type"]) != "compaction" {
		t.Fatal("live compaction did not return exactly one compaction item")
	}
	payload, ok := responsesbridge.DecodeCompactionEnvelope(jsonx.String(jsonx.Map(output[0])["encrypted_content"]))
	if !ok {
		t.Fatal("live compaction returned an unreadable summary")
	}
	if payload.Degraded {
		t.Fatalf("live compaction degraded: %.600s", payload.Summary)
	}
	return payload
}

func TestLiveCompactionRetainsFactsAcrossThreeRounds(t *testing.T) {
	modelID, send := newLiveCompactionGateway(t)
	history := liveCompactionHistory()
	history = append(history,
		map[string]any{"role": "user", "content": "Current task marker: ALPHA_7351. " + strings.Repeat("Routine implementation note. ", 120) + " Mandatory final constraint: never delete database OMEGA_9427."},
		map[string]any{"type": "function_call", "name": "exec_command", "call_id": "live-build", "arguments": `{"command":"echo BUILD_GREEN_8642"}`},
		map[string]any{"type": "function_call_output", "call_id": "live-build", "output": "The final build completed successfully: BUILD_GREEN_8642."},
	)
	for round := 1; round <= 3; round++ {
		t.Logf("round %d: compacting with %s", round, modelID)
		input := append(append([]any(nil), history...), map[string]any{"type": "compaction_trigger"})
		compacted := send(map[string]any{"model": modelID, "input": input, "stream": false})
		liveCompactionPayload(t, compacted)
		output := jsonx.Slice(compacted["output"])
		probeInput := append(append([]any(nil), output...), map[string]any{"role": "user", "content": "只依据历史输出 JSON，键为 task_marker、database_marker、build_marker，值仅填写当前任务代号、禁止删除的数据库代号、最后构建成功标识。缺失填 null，不要猜测。"})
		probe := send(map[string]any{"model": modelID, "input": probeInput, "stream": false, "reasoning": map[string]any{"effort": "none"}, "max_output_tokens": 512})
		text := liveResponseText(probe)
		t.Logf("round %d recall: %s", round, text)
		for _, marker := range []string{"ALPHA_7351", "OMEGA_9427", "BUILD_GREEN_8642"} {
			if !strings.Contains(text, marker) {
				t.Fatalf("round %d lost %s", round, marker)
			}
		}
		history = append(append([]any(nil), output...), map[string]any{"role": "user", "content": fmt.Sprintf("Checkpoint %d completed; continue under the original task and database constraints.", round)})
	}
}

func TestLiveCompactionPreservesImageKnowledge(t *testing.T) {
	modelID, send := newLiveCompactionGateway(t)
	picture := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			picture.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	imageURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
	history := append(liveCompactionHistory(), map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "input_text", "text": "Current screenshot marker SCREEN_DELTA_6132. Remember the primary color of the attached screenshot."},
		map[string]any{"type": "input_image", "image_url": imageURL},
	}}, map[string]any{"role": "assistant", "content": "The screenshot is relevant to the current task."}, map[string]any{"type": "compaction_trigger"})
	t.Logf("compacting an image with %s", modelID)
	compacted := send(map[string]any{"model": modelID, "input": history, "stream": false})
	payload := liveCompactionPayload(t, compacted)
	description := strings.ToLower(payload.Summary)
	if !strings.Contains(description, "red") && !strings.Contains(description, "红") && !strings.Contains(description, "crimson") && !strings.Contains(description, "scarlet") {
		t.Fatal("the successful summary did not describe the screenshot's color")
	}
	input := append(append([]any(nil), jsonx.Slice(compacted["output"])...), map[string]any{"role": "user", "content": "只依据保留的历史，SCREEN_DELTA_6132 对应截图的主要颜色是什么？只回复颜色；不知道就回复未知，不要猜测。"})
	probe := send(map[string]any{"model": modelID, "input": input, "stream": false, "reasoning": map[string]any{"effort": "none"}, "max_output_tokens": 128})
	text := strings.ToLower(strings.TrimSpace(liveResponseText(probe)))
	t.Logf("image recall: %s", text)
	if text != "红色" && text != "红" && text != "red" {
		t.Fatal("the compacted context did not retain the screenshot's color")
	}
}
