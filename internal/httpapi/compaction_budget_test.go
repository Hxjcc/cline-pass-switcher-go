package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

func TestCompactionBudgetRespectsKnownModelOutputLimit(t *testing.T) {
	for _, tc := range []struct{ limit, requested, want int }{
		{0, 0, 16384}, {8192, 0, 8192}, {8192, 65536, 8192}, {32768, 0, 16384}, {512, 0, 512},
	} {
		st, server := newTestServer(t)
		if _, err := st.UpdateModelMeta("test", func(meta *model.ModelMeta) { meta.OutputLimit = tc.limit }); err != nil {
			t.Fatal(err)
		}
		body := map[string]any{"model": "test", "max_tokens": tc.requested}
		server.ensureCompactionBudget(body)
		if got := positiveInt(body["max_tokens"]); got != tc.want {
			t.Fatalf("limit=%d requested=%d: got budget %d, want %d", tc.limit, tc.requested, got, tc.want)
		}
	}
}

func TestCompactionAtModelOutputCapDoesNotRepeatAnIdenticalStarvedRequest(t *testing.T) {
	recorder := &upstreamRecorder{}
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/users/me/plan") {
			http.NotFound(w, r)
			return
		}
		var payload map[string]any
		if err := readJSON(r, &payload); err != nil {
			t.Error(err)
		}
		recorder.add(payload)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"empty response content"}`)
	}))
	defer upstreamServer.Close()
	server, st := configureCompactionServer(t, upstreamServer.URL)
	if _, err := st.UpdateModelMeta("cline-pass/test", func(meta *model.ModelMeta) { meta.OutputLimit = 8192 }); err != nil {
		t.Fatal(err)
	}
	r := localRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"cline-pass/test","input":[{"role":"user","content":"continue"},{"type":"compaction_trigger"}]}`))
	w := httptest.NewRecorder()
	server.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("bounded starvation did not degrade: %d", w.Code)
	}
	if calls := recorder.all(); len(calls) != 1 || positiveInt(calls[0]["max_tokens"]) != 8192 {
		t.Fatalf("model at its output cap should get one bounded attempt, got %#v", calls)
	}
}
