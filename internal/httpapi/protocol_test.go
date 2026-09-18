package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

func TestUnsupportedResponsesNeverReachUpstream(t *testing.T) {
	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(500) }))
	defer up.Close()
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(c *model.Config) {
		c.UpstreamBase = up.URL
		c.Accounts = []model.Account{{Name: "test", Key: "test", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/responses", "/v1/responses/compact"} {
		for _, body := range []string{
			`{"model":"test","input":"hi","conversation":"conv_123","stream":true}`,
			`{"model":"test","input":"hi","tools":[{"type":"web_search"}]}`,
			`{"model":"test","input":[{"role":"user","content":[{"type":"input_file","file_id":"file_123"}]}]}`,
		} {
			w := httptest.NewRecorder()
			server.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(body)))
			var payload struct {
				Error struct{ Type, Code, Param string }
			}
			if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if w.Code != 400 || payload.Error.Type != "invalid_request_error" || payload.Error.Code != "unsupported_feature" || payload.Error.Param == "" {
				t.Fatalf("incorrect error: %d %s", w.Code, w.Body.String())
			}
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("unsupported requests reached upstream %d times", hits.Load())
	}
}
