package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/jsonx"
	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

func TestResponsesImageDetailUsesModelCapabilityInEveryPath(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, route := range []string{"buffered", "stream", "compact", "compaction-trigger"} {
			t.Run(fmt.Sprintf("%s/enabled=%v", route, enabled), func(t *testing.T) {
				want := "high"
				if enabled {
					want = "original"
				}
				calls := 0
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					_ = json.NewDecoder(r.Body).Decode(&body)
					for _, message := range jsonx.Slice(body["messages"]) {
						for _, raw := range jsonx.Slice(jsonx.Map(message)["content"]) {
							part := jsonx.Map(raw)
							if part["type"] == "image_url" {
								calls++
								image := jsonx.Map(part["image_url"])
								if image["detail"] != want || image["url"] != "data:image/png;base64,AAAA" {
									t.Errorf("upstream image does not match advertised capability: %+v", image)
								}
							}
						}
					}
					writeJSON(w, http.StatusOK, map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "## Objective\nDescribe the image.\n## Work State\nImage inspected.\n## Next Move\nContinue.\n## Relevant Files\nNone."}, "finish_reason": "stop"}}})
				}))
				defer upstream.Close()
				st, server := newTestServer(t)
				configureKeys(t, st, upstream.URL, func(c *model.Config) { c.ProxyKey = "proxy-key" })
				if _, err := st.UpdateModelMeta("cline-pass/test", func(m *model.ModelMeta) {
					m.InputModalities = []string{"text"}
					if enabled {
						m.InputModalities = append(m.InputModalities, "image")
					}
				}); err != nil {
					t.Fatal(err)
				}
				body := map[string]any{"model": "cline-pass/test", "input": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_image", "image_url": "data:image/png;base64,AAAA", "detail": "original"}}}}}
				path := "/v1/responses"
				switch route {
				case "stream":
					body["stream"] = true
				case "compact":
					path += "/compact"
				case "compaction-trigger":
					body["input"] = append(body["input"].([]any), map[string]any{"type": "compaction_trigger"})
				}
				raw, _ := json.Marshal(body)
				w := httptest.NewRecorder()
				r := localRequest(http.MethodPost, path, strings.NewReader(string(raw)))
				r.Header.Set("Authorization", "Bearer proxy-key")
				server.ServeHTTP(w, r)
				if w.Code != http.StatusOK || calls != 1 {
					t.Fatalf("HTTP %d, images sent=%d, response=%s", w.Code, calls, w.Body)
				}
			})
		}
	}
}
