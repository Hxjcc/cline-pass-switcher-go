package httpapi

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

func TestChatStrictToolsRejectInvalidArgumentsAndKeepUsage(t *testing.T) {
	const invalid = `{"choices":[{"message":{"tool_calls":[{"id":"call1","type":"function","function":{"name":"exec_command","arguments":"{\"cmd\":42}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"cost":0.25}}`
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			u := &keyedUpstream{body: invalid}
			up := u.server(t)
			st, server := newTestServer(t)
			configureKeys(t, st, up.URL, func(*model.Config) {})
			body := fmt.Sprintf(`{"model":"test","messages":[{"role":"user","content":"hi"}],"stream":%v,"tools":[{"type":"function","function":{"name":"exec_command","strict":true,"parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"],"additionalProperties":false}}}]}`, stream)
			w := httptest.NewRecorder()
			server.ServeHTTP(w, localRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
			if w.Code != 502 || !strings.Contains(w.Body.String(), "upstream_tool_schema_validation_failed") || strings.Contains(w.Body.String(), "42") {
				t.Fatalf("invalid strict result leaked: %d %s", w.Code, w.Body)
			}
			history := st.History()
			if len(history) != 1 || history[0].Usage == nil || history[0].Usage.Cost == nil || *history[0].Usage.Cost != 0.25 {
				t.Fatalf("rejected generation lost billing usage: %#v", history)
			}
		})
	}
}
