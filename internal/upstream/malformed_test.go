package upstream

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
	"github.com/Hxjcc/cline-pass-switcher-go/internal/store"
)

func TestAttemptNonStreamRejectsSuccessWithoutChoices(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"raw":"not a chat completion"}`))
	}))
	defer upstreamServer.Close()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpdateConfig(func(cfg *model.Config) {
		cfg.UpstreamBase = upstreamServer.URL
		cfg.Accounts = []model.Account{{Name: "test", Key: "key", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}

	result := New(st).AttemptNonStream(t.Context(), "cline-pass/test", map[string]any{
		"model":    "cline-pass/test",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}, Attempt{})
	if result.Status != http.StatusBadGateway || !strings.Contains(result.NetErr, "no completion choices") {
		t.Fatalf("malformed 200 became %d: %#v", result.Status, result)
	}
}
