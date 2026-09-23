package httpapi

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/store"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/upstream"
)

// Production only permits loopback hosts before a proxy key is configured.
func localRequest(method, target string, body io.Reader) *http.Request {
	if strings.HasPrefix(target, "/") {
		target = "http://localhost" + target
	}
	request := httptest.NewRequest(method, target, body)
	// httptest.NewRequest defaults RemoteAddr to TEST-NET-1. The access guard
	// classifies the connection source, so a local test has to look local.
	request.RemoteAddr = "127.0.0.1:54321"
	return request
}

func newTestServer(t *testing.T) (*store.Store, *Server) {
	t.Helper()
	// Every switch a developer might have exported while debugging: the tests
	// set what they need, and nothing else should leak in from the shell.
	for _, name := range []string{
		"CLINE_PASS_KEY", "PROXY_KEY", "ADMIN_KEY", "PUBLIC_BASE_URL", "PORT",
		"COMPACTION_RECENT_TOKENS", "COMPACTION_REASONING_EFFORT", "COMPACTION_MIN_OUTPUT_TOKENS",
		"SHELL_COMPAT", "SHELL_COMPAT_ENFORCE", "WEB_SEARCH_UPSTREAM", "WEB_FETCH_UPSTREAM",
	} {
		t.Setenv(name, "")
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	service := upstream.New(st)
	assets := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>test</title>"), Mode: 0o644},
	}
	server, err := New(st, service, fs.FS(assets))
	if err != nil {
		t.Fatal(err)
	}
	return st, server
}

// accountStats reads the per-account counters by account name, resolving the
// stable identity the store now keys them by.
func accountStats(t *testing.T, st *store.Store, name string) model.AccountStats {
	t.Helper()
	for _, account := range st.Config().Accounts {
		if account.Name == name {
			return st.Metadata().Stats[account.ID]
		}
	}
	return st.Metadata().Stats[name]
}
