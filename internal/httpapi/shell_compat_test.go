package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

// The forwarded tool schema must lock the shell argument upstream, while the
// declaration echoed back to the client stays exactly as it was sent.
func TestShellCompatReachesUpstreamToolSchema(t *testing.T) {
	received := make(chan map[string]any, 1)
	up := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		raw, _ := io.ReadAll(request.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		received <- body
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"id":"chatcmpl-1","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer up.Close()

	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(config *model.Config) {
		config.UpstreamBase = up.URL
		config.Accounts = []model.Account{{Name: "main", Key: "key", Enabled: true}}
		config.ShellCompat = "powershell"
	}); err != nil {
		t.Fatal(err)
	}

	body := `{"model":"cline-pass/test","input":"跑个命令","tools":[{"type":"function","name":"exec_command","description":"run","parameters":{"type":"object","properties":{"cmd":{"type":"string"},"shell":{"type":"string","enum":["bash","cmd","powershell"]}},"required":["cmd"]}}]}`
	response := httptest.NewRecorder()
	server.ServeHTTP(response, localRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
	if response.Code != http.StatusOK {
		t.Fatalf("request failed: %d %s", response.Code, response.Body.String())
	}

	forwarded := <-received
	var forwardedShell map[string]any
	for _, raw := range jsonx.Slice(forwarded["tools"]) {
		function := jsonx.Map(jsonx.Map(raw)["function"])
		if jsonx.String(function["name"]) != "exec_command" {
			continue
		}
		forwardedShell = jsonx.Map(jsonx.Map(jsonx.Map(function["parameters"])["properties"])["shell"])
	}
	if forwardedShell == nil {
		t.Fatalf("exec_command was not forwarded: %#v", forwarded["tools"])
	}
	enum := jsonx.Slice(forwardedShell["enum"])
	if len(enum) != 1 || jsonx.String(enum[0]) != "powershell" {
		t.Fatalf("upstream schema was not locked: %#v", forwardedShell)
	}

	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	echoed := jsonx.Slice(payload["tools"])
	if len(echoed) != 1 {
		t.Fatalf("response should echo the client's tools: %#v", payload["tools"])
	}
	echoedShell := jsonx.Map(jsonx.Map(jsonx.Map(jsonx.Map(echoed[0])["parameters"])["properties"])["shell"])
	if len(jsonx.Slice(echoedShell["enum"])) != 3 {
		t.Fatalf("the echo must keep the client's schema: %#v", echoedShell)
	}
}
