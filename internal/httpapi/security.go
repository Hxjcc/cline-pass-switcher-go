package httpapi

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Cross-origin requests are unnecessary for the bundled UI (including Vite's
// development proxy). Check the request itself, not just CORS response headers:
// simple cross-origin POSTs can otherwise change configuration without preflight.
func (s *Server) browserRequestAllowed(r *http.Request) bool {
	key, publicBase := s.store.AccessSettings()
	if key == "" {
		host := r.Host
		if parsed, _, err := net.SplitHostPort(host); err == nil {
			host = parsed
		}
		host = strings.Trim(host, "[]")
		ip := net.ParseIP(host)
		if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
			return false // DNS rebinding protection for unauthenticated local use.
		}
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	} // Native API clients and same-origin GETs.
	u, err := url.Parse(origin)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if strings.EqualFold(u.Host, r.Host) && u.Scheme == scheme {
		return true
	}
	// Explicit external URL supports TLS termination without trusting forwarded headers.
	if key != "" && publicBase != "" {
		base, err := url.Parse(publicBase)
		return err == nil && base.Scheme == u.Scheme && strings.EqualFold(base.Host, u.Host)
	}
	return false
}
