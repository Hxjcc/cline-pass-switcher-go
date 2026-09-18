package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

func TestCrossSiteRequestsCannotReadOrChangeKeys(t *testing.T) {
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(c *model.Config) {
		c.Accounts = []model.Account{{Name: "test", Key: "fake-test-secret", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", "proxy-test"} {
		if err := st.UpdateConfig(func(c *model.Config) { c.ProxyKey = key }); err != nil {
			t.Fatal(err)
		}
		for _, method := range []string{"GET", "POST", "OPTIONS"} {
			for _, origin := range []string{"https://evil.example", "null", "http://localhost.evil.example", "http://localhost:9999"} {
				r := localRequest(method, "/api/accounts", strings.NewReader(`{"accounts":[]}`))
				r.Header.Set("Origin", origin)
				r.Header.Set("X-Admin-Key", key)
				w := httptest.NewRecorder()
				server.ServeHTTP(w, r)
				if w.Code != 403 || w.Header().Get("Access-Control-Allow-Origin") != "" || strings.Contains(w.Body.String(), "fake-test-secret") {
					t.Fatalf("%s %s: %d", method, origin, w.Code)
				}
			}
		}
	}
	if len(st.Config().Accounts) != 1 {
		t.Fatal("cross-origin request mutated accounts")
	}
}

func TestLocalOnboardingAndNativeAuthentication(t *testing.T) {
	st, server := newTestServer(t)
	for _, origin := range []string{"", "http://localhost"} {
		r := localRequest("GET", "/api/accounts", nil)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	r := httptest.NewRequest("GET", "http://attacker.example/api/accounts", nil)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("DNS rebinding host accepted")
	}
	if err := st.UpdateConfig(func(c *model.Config) { c.ProxyKey = "test-key"; c.PublicBaseURL = "https://console.example" }); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("GET", "http://console.example/api/accounts", nil)
	w = httptest.NewRecorder()
	server.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	r.Header.Set("Authorization", "Bearer test-key")
	r.Header.Set("Origin", "https://console.example")
	w = httptest.NewRecorder()
	server.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	r.Header.Del("Origin")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w = httptest.NewRecorder()
	server.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}

func TestUnknownAPIRoutesNeverServeSPA(t *testing.T) {
	_, server := newTestServer(t)
	for _, path := range []string{"/v1/missing", "/api/missing", "/api/v1/missing", "/api", "/v1"} {
		for _, method := range []string{"GET", "HEAD", "POST"} {
			w := httptest.NewRecorder()
			server.ServeHTTP(w, localRequest(method, path, nil))
			if w.Code != 404 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
				t.Fatalf("%s %s: %d", method, path, w.Code)
			}
		}
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, localRequest("GET", "/dashboard", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<!doctype") {
		t.Fatal("SPA navigation broken")
	}
}

type repeatedByte struct{ value byte }

func (r repeatedByte) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.value
	}
	return len(p), nil
}

func TestOversizeJSONReturns413(t *testing.T) {
	_, server := newTestServer(t)
	for _, prefix := range []string{`{"model":"`, `{}`} {
		value := byte('x')
		if prefix == `{}` {
			value = ' '
		}
		body := io.MultiReader(strings.NewReader(prefix), io.LimitReader(repeatedByte{value}, maxRequestBytes+1))
		r := localRequest(http.MethodPost, "/api/probe", body)
		r.ContentLength = -1 // Also cover chunked bodies without a trustworthy length.
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		if w.Code != 413 {
			t.Fatalf("prefix %q: %d %s", prefix, w.Code, w.Body.String())
		}
	}
}
