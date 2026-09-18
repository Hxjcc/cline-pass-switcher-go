package upstream

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

func TestValidateUpstreamsRecordsLatency(t *testing.T) {
	const delay = 30 * time.Millisecond
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode validation payload: %v", err)
			return
		}
		provider, _ := payload["provider"].(map[string]any)
		only, _ := provider["only"].([]any)
		time.Sleep(delay)
		writer.Header().Set("Content-Type", "application/json")
		// A thinking model spends the 16-token budget on reasoning and the
		// gateway reports the empty content as an error; the channel itself
		// is healthy.
		if len(only) == 1 && only[0] == "thinker" {
			_, _ = io.WriteString(writer, `{"error":{"message":"Provider returned empty response content","code":502}}`)
			return
		}
		_, _ = io.WriteString(writer, `{"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`)
	}))
	defer upstreamServer.Close()

	st := newStreamTestStore(t, upstreamServer.URL)
	if _, err := st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) {
		meta.Upstreams = []string{"talker", "thinker"}
	}); err != nil {
		t.Fatal(err)
	}

	result, err := New(st).ValidateUpstreams(t.Context(), "cline-pass/test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary["ok"] != 2 {
		t.Fatalf("both channels should validate as ok: %#v", result.Summary)
	}
	for _, slug := range []string{"talker", "thinker"} {
		status := result.Results[slug]
		if status.Status != "ok" {
			t.Fatalf("%s: status %q (%s)", slug, status.Status, status.Note)
		}
		if status.MS < delay.Milliseconds() {
			t.Fatalf("%s: latency should cover the upstream round trip, got %d ms", slug, status.MS)
		}
	}
	if result.Results["thinker"].Note == "" {
		t.Fatal("the empty-content note should be kept for the tooltip")
	}
	stored := st.Metadata().Models["cline-pass/test"].UpstreamStatus["talker"]
	if stored.MS != result.Results["talker"].MS {
		t.Fatalf("latency should be persisted with the status: %#v", stored)
	}
}
