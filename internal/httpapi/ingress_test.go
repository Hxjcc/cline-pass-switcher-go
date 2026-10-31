package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

type blockedJSONReader struct {
	started, unblock chan struct{}
	once             sync.Once
	reader           io.Reader
}

func (r *blockedJSONReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.unblock
	return r.reader.Read(p)
}

type unreadableBody struct{}

func (unreadableBody) Read([]byte) (int, error) {
	panic("oversized Content-Length must be rejected before reading")
}

func limitedTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"maxRequestBytes":128,"maxConcurrentDecodes":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, server := newTestServerInDir(t, dir)
	return server
}

func TestIngressRejectsOversizedKnownAndChunkedBodies(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/chat/completions", "/v1/responses/compact", "/api/probe"} {
		server := limitedTestServer(t)
		r := localRequest("POST", path, nil)
		r.Body = io.NopCloser(unreadableBody{})
		r.ContentLength = 129
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		if w.Code != 413 {
			t.Fatalf("%s Content-Length: %d", path, w.Code)
		}
		// Include an unterminated field and valid JSON with excessive trailing
		// whitespace. The second case catches decoders that stop after {}.
		for _, body := range []string{`{"model":"` + strings.Repeat("x", 256), `{}` + strings.Repeat(" ", 256)} {
			r = localRequest("POST", path, strings.NewReader(body))
			r.ContentLength = -1
			w = httptest.NewRecorder()
			server.ServeHTTP(w, r)
			if w.Code != 413 {
				t.Fatalf("%s chunked: %d %s", path, w.Code, w.Body)
			}
		}
	}
}

func TestDecodeAdmissionPrecedesBodyReadAndReleases(t *testing.T) {
	server := limitedTestServer(t)
	slow := &blockedJSONReader{started: make(chan struct{}), unblock: make(chan struct{}), reader: strings.NewReader(`{}`)}
	var release sync.Once
	defer release.Do(func() { close(slow.unblock) })
	first := localRequest("POST", "/v1/responses", slow)
	done := make(chan struct{})
	go func() { server.ServeHTTP(httptest.NewRecorder(), first); close(done) }()
	select {
	case <-slow.started:
	case <-time.After(3 * time.Second):
		t.Fatal("first decode never started")
	}
	second := localRequest("POST", "/v1/responses", nil)
	second.Body = io.NopCloser(unreadableBody{})
	w := httptest.NewRecorder()
	server.ServeHTTP(w, second)
	if w.Code != 429 || !strings.Contains(w.Body.String(), "decode_concurrency_limit") {
		t.Fatalf("decode admission: %d %s", w.Code, w.Body)
	}
	release.Do(func() { close(slow.unblock) })
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("decode did not finish")
	}
	w = httptest.NewRecorder()
	server.ServeHTTP(w, localRequest("POST", "/v1/responses", strings.NewReader(`{}`)))
	if w.Code != 400 {
		t.Fatalf("decode permit leaked: %d", w.Code)
	}
}

func TestRemoteConsoleRequiresIndependentAdmin(t *testing.T) {
	st, server := newTestServer(t)
	for _, admin := range []string{"", "master", "separate"} {
		if err := st.UpdateConfig(func(c *model.Config) { c.ProxyKey = "master"; c.AdminKey = admin }); err != nil {
			t.Fatal(err)
		}
		r := localRequest("GET", "/api/accounts", nil)
		r.RemoteAddr = "203.0.113.4:1234"
		r.Header.Set("X-Admin-Key", firstNonEmpty(admin, "master"))
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		want := 403
		if admin == "separate" {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("admin %q: %d", admin, w.Code)
		}
	}
}

func TestReplayStartupCleansOnlyOwnedFiles(t *testing.T) {
	dir := t.TempDir()
	replay, err := prepareReplayDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(replay, "cline-pass-replay-stale")
	keep := filepath.Join(replay, "keep.txt")
	for _, path := range []string{stale, keep} {
		if err := os.WriteFile(path, []byte("private"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := prepareReplayDirectory(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale replay survived")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("unrelated file was removed")
	}
}

func TestUnboundedIdentifiersAreRejected(t *testing.T) {
	_, server := newTestServer(t)
	for _, key := range []string{"model", "prompt_cache_key", "safety_identifier"} {
		body := `{"model":"test","input":"hi","` + key + `":"` + strings.Repeat("界", 257) + `"}`
		w := httptest.NewRecorder()
		server.ServeHTTP(w, localRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
		if w.Code != 400 || !strings.Contains(w.Body.String(), key) {
			t.Fatalf("%s: %d %s", key, w.Code, w.Body)
		}
	}
}
