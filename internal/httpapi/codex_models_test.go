package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

func TestCodexCatalogMatchesSubscriptionAndCapabilities(t *testing.T) {
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(c *model.Config) {
		c.ProxyKey = "catalog-key"
		c.KnownModels = []string{"test/reasoning", "test/plain", "test/unknown"}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateModelMeta("test/reasoning", func(m *model.ModelMeta) {
		m.DisplayName = "Reasoning model"
		m.Description = "Catalog fixture"
		m.CapabilitiesKnown = true
		m.ToolCall = true
		m.Reasoning = true
		m.ReasoningEfforts = []string{" MAX ", "none", "max", "not-a-level"}
		m.InputModalities = []string{"text", "image", "audio"}
		m.ContextWindow = 1000000
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateModelMeta("test/plain", func(m *model.ModelMeta) {
		m.CapabilitiesKnown = true
		m.ToolCall = false
		m.Attachment = true
		m.InputModalities = []string{"text", "audio"}
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/models", "/models", "/api/v1/models"} {
		t.Run(path, func(t *testing.T) {
			r := localRequest(http.MethodGet, path, nil)
			r.Header.Set("Authorization", "Bearer catalog-key")
			w := httptest.NewRecorder()
			server.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("catalog request failed: %d %s", w.Code, w.Body)
			}
			var out struct {
				Object string `json:"object"`
				Data   []struct{ ID, Object string }
				Models []codexModelInfo
			}
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if out.Object != "list" || len(out.Data) != 3 || len(out.Models) != 3 {
				t.Fatalf("subscription differs between model catalogs: %s", w.Body)
			}
			for i, m := range out.Models {
				if m.Slug != out.Data[i].ID || out.Data[i].Object != "model" || m.Priority != i {
					t.Fatalf("catalog order/identity mismatch: %+v", m)
				}
			}
			known := out.Models[0]
			if !known.SupportsImageDetailOriginal || out.Models[1].SupportsImageDetailOriginal || out.Models[2].SupportsImageDetailOriginal {
				t.Fatal("original image detail must follow the model's image input capability")
			}
			if known.DisplayName != "Reasoning model" || known.ContextWindow == nil || *known.ContextWindow != 1000000 || !known.SupportsSearchTool || !slices.Equal(known.InputModalities, []string{"text", "image"}) {
				t.Fatalf("lost model capability: %+v", known)
			}
			if known.DefaultReasoningLevel == nil || *known.DefaultReasoningLevel != "max" || len(known.SupportedReasoningLevels) != 2 {
				t.Fatalf("unsupported or duplicate reasoning tier: %+v", known)
			}
			if out.Models[1].SupportsSearchTool || out.Models[1].ContextWindow != nil || len(out.Models[1].SupportedReasoningLevels) != 0 || !slices.Equal(out.Models[1].InputModalities, []string{"text"}) {
				t.Fatalf("invented capability for known plain model: %+v", out.Models[1])
			}
			unknown := out.Models[2]
			if !unknown.SupportsSearchTool || unknown.ContextWindow != nil || unknown.DefaultReasoningLevel != nil || !slices.Equal(unknown.InputModalities, []string{"text"}) {
				t.Fatalf("unsafe fallback metadata: %+v", unknown)
			}
			if strings.Contains(w.Body.String(), "catalog-key") || strings.Contains(w.Body.String(), "gpt-6") || strings.Contains(w.Body.String(), "service_tiers") {
				t.Fatal("catalog leaked credentials or advertised unrelated vendor capabilities")
			}
		})
	}
}

func TestCodexCatalogEmptyAndSubscriptionUpdates(t *testing.T) {
	st, server := newTestServer(t)
	read := func() map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		server.ServeHTTP(w, localRequest("GET", "/v1/models", nil))
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := read(); len(out["models"].([]any)) != 0 || len(out["data"].([]any)) != 0 {
		t.Fatalf("empty catalog invalid: %+v", out)
	}
	if err := st.UpdateConfig(func(c *model.Config) { c.KnownModels = []string{"test/new"} }); err != nil {
		t.Fatal(err)
	}
	if out := read(); len(out["models"].([]any)) != 1 || out["models"].([]any)[0].(map[string]any)["slug"] != "test/new" {
		t.Fatalf("new subscription not visible: %+v", out)
	}
	if _, err := st.UpdateModelMeta("test/new", func(m *model.ModelMeta) {
		m.ContextWindow = 64000
		m.ReasoningEfforts = []string{"low", "medium"}
		m.InputModalities = []string{"text", "image"}
	}); err != nil {
		t.Fatal(err)
	}
	updated := read()["models"].([]any)[0].(map[string]any)
	if updated["context_window"] != float64(64000) || updated["default_reasoning_level"] != "medium" || updated["supports_image_detail_original"] != true {
		t.Fatalf("updated model capabilities not visible: %+v", updated)
	}
	if err := st.RemoveModel("test/new"); err != nil {
		t.Fatal(err)
	}
	if out := read(); len(out["models"].([]any)) != 0 {
		t.Fatal("removed model remained in Codex catalog")
	}
}
