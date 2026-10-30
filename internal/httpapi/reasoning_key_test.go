package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

func TestReasoningKeySurvivesServerRestart(t *testing.T) {
	dir := t.TempDir()
	a, err := loadReasoningCodec(dir)
	if err != nil {
		t.Fatal(err)
	}
	envelope := a.Scoped("master").Encode("resume after restart")
	b, err := loadReasoningCodec(dir)
	if err != nil {
		t.Fatal(err)
	}
	if text, err := b.Scoped("master").Decode(envelope); err != nil || text != "resume after restart" {
		t.Fatal("restart invalidated reasoning", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reasoning.key"), []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadReasoningCodec(dir); err == nil {
		t.Fatal("invalid key file accepted")
	}
}

func TestEncryptedReasoningHTTPReplayIsBoundToIssuedClient(t *testing.T) {
	u := &keyedUpstream{body: `{"choices":[{"message":{"role":"assistant","content":"OK","reasoning_content":"keep client-specific reasoning"},"finish_reason":"stop"}]}`}
	up := u.server(t)
	st, server := newTestServer(t)
	configureKeys(t, st, up.URL, func(c *model.Config) {
		c.AdminKey = "console"
		c.ProxyKeys = []model.ProxyKeyGrant{{ID: "a", Key: "key-a", Enabled: true}, {ID: "b", Key: "key-b", Enabled: true}}
	})
	send := func(key string, body []byte) *httptest.ResponseRecorder {
		r := localRequest("POST", "/v1/responses", strings.NewReader(string(body)))
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	first := send("key-a", []byte(`{"model":"cline-pass/test","input":"hello","include":["reasoning.encrypted_content"]}`))
	if first.Code != 200 {
		t.Fatalf("encrypted reasoning include rejected: %d %s", first.Code, first.Body)
	}
	var response map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	input := response["output"].([]any)
	found := false
	for _, raw := range input {
		item := raw.(map[string]any)
		if item["type"] == "reasoning" {
			found = true
			if item["encrypted_content"] == nil {
				t.Fatal("encrypted reasoning missing")
			}
			delete(item, "summary")
			delete(item, "content")
		}
	}
	if !found {
		t.Fatal("fixture produced no reasoning")
	}
	input = append(input, map[string]any{"role": "user", "content": "continue"})
	body, _ := json.Marshal(map[string]any{"model": "cline-pass/test", "input": input})
	if second := send("key-a", body); second.Code != 200 {
		t.Fatalf("same client replay failed: %d %s", second.Code, second.Body)
	}
	if other := send("key-b", body); other.Code != 400 || !strings.Contains(other.Body.String(), "invalid_reasoning_envelope") {
		t.Fatalf("cross-client replay was accepted: %d %s", other.Code, other.Body)
	}
	if len(u.credentials()) != 2 {
		t.Fatal("rejected replay reached the upstream")
	}
}
