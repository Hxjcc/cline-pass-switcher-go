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

// The Host header is client-controlled, so the unauthenticated guard has to
// classify the connection source instead of trusting it.
func TestNoKeyAccessRequiresALocalConnection(t *testing.T) {
	const key = "fake-test-secret"
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(c *model.Config) {
		c.Accounts = []model.Account{{Name: "main", Key: key, Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		remote  string
		host    string
		allowed bool
	}{
		{name: "loopback peer", remote: "127.0.0.1:5555", host: "localhost", allowed: true},
		{name: "loopback ipv6 peer", remote: "[::1]:5555", host: "localhost", allowed: true},
		{name: "external peer with spoofed host", remote: "203.0.113.9:5555", host: "localhost"},
		{name: "local peer with attacker host", remote: "127.0.0.1:5555", host: "attacker.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://"+tc.host+"/api/accounts?reveal=1", nil)
			request.RemoteAddr = tc.remote
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if tc.allowed {
				if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), key) {
					t.Fatalf("local request rejected: %d %s", response.Code, response.Body.String())
				}
				return
			}
			if response.Code != http.StatusForbidden {
				t.Fatalf("non-local request accepted: %d %s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), key) {
				t.Fatalf("refused response leaked the key: %s", response.Body.String())
			}
		})
	}
}

func TestTrustedProxyAndLocalPortForwardPolicy(t *testing.T) {
	const key = "fake-test-secret"
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(c *model.Config) {
		c.Accounts = []model.Account{{Name: "main", Key: key, Enabled: true}}
		c.TrustedProxies = []string{"172.18.0.0/16"}
	}); err != nil {
		t.Fatal(err)
	}
	send := func(remote, forwarded string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "http://localhost/api/accounts?reveal=1", nil)
		request.RemoteAddr = remote
		if forwarded != "" {
			request.Header.Set("X-Forwarded-For", forwarded)
		}
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response
	}
	if response := send("172.18.0.5:4444", "127.0.0.1"); response.Code != http.StatusOK {
		t.Fatalf("trusted proxy reporting a loopback client rejected: %d %s", response.Code, response.Body.String())
	}
	if response := send("172.18.0.5:4444", "203.0.113.9"); response.Code != http.StatusForbidden {
		t.Fatalf("trusted proxy reporting a remote client accepted: %d", response.Code)
	}
	if response := send("203.0.113.9:4444", "127.0.0.1"); response.Code != http.StatusForbidden {
		t.Fatalf("untrusted peer vouched for itself: %d", response.Code)
	}

	// A container cannot observe the host-side binding, so publishing on the
	// host loopback has to be declared explicitly.
	if err := st.UpdateConfig(func(c *model.Config) { c.TrustLocalPortForward = true }); err != nil {
		t.Fatal(err)
	}
	if response := send("172.17.0.1:4444", ""); response.Code != http.StatusOK {
		t.Fatalf("declared local port forward rejected: %d %s", response.Code, response.Body.String())
	}
	request := httptest.NewRequest(http.MethodGet, "http://attacker.example/api/accounts?reveal=1", nil)
	request.RemoteAddr = "172.17.0.1:4444"
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("port-forward trust must still require a loopback host: %d", response.Code)
	}
}

// Forwarding headers are only as trustworthy as the hop that produced them:
// the client-controlled prefix of X-Forwarded-For must never decide locality.
func TestForwardedClientAddressComesFromTheTrustedHop(t *testing.T) {
	const key = "fake-test-secret"
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(c *model.Config) {
		c.Accounts = []model.Account{{Name: "main", Key: key, Enabled: true}}
		c.TrustedProxies = []string{"172.18.0.0/16"}
	}); err != nil {
		t.Fatal(err)
	}
	send := func(remote, forwarded string) int {
		request := httptest.NewRequest(http.MethodGet, "http://localhost/api/accounts?reveal=1", nil)
		request.RemoteAddr = remote
		if forwarded != "" {
			request.Header.Set("X-Forwarded-For", forwarded)
		}
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code == http.StatusOK && !strings.Contains(response.Body.String(), key) {
			t.Fatalf("accepted response did not carry the console payload: %s", response.Body.String())
		}
		return response.Code
	}
	for _, tc := range []struct {
		name      string
		remote    string
		forwarded string
		allowed   bool
	}{
		{name: "trusted proxy, spoofed loopback prefix", remote: "172.18.0.5:4444", forwarded: "127.0.0.1, 203.0.113.9"},
		{name: "trusted proxy, remote client", remote: "172.18.0.5:4444", forwarded: "203.0.113.9"},
		{name: "trusted proxy behind another trusted hop", remote: "172.18.0.5:4444", forwarded: "203.0.113.9, 172.18.0.6"},
		{name: "local reverse proxy with remote client", remote: "127.0.0.1:4444", forwarded: "203.0.113.9"},
		{name: "untrusted peer vouching for itself", remote: "203.0.113.9:4444", forwarded: "127.0.0.1"},
		{name: "chain of trusted proxies only", remote: "127.0.0.1:4444", forwarded: "172.18.0.5"},
		{name: "trusted proxy with loopback client", remote: "172.18.0.5:4444", forwarded: "127.0.0.1", allowed: true},
		{name: "local reverse proxy with local client", remote: "127.0.0.1:4444", forwarded: "127.0.0.1", allowed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := send(tc.remote, tc.forwarded)
			if tc.allowed && code != http.StatusOK {
				t.Fatalf("local client rejected: %d", code)
			}
			if !tc.allowed && code != http.StatusForbidden {
				t.Fatalf("forwarded chain was spoofed: %d", code)
			}
		})
	}
}
