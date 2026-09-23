package httpapi

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/store"
)

// browserRequestAllowed decides whether a request may reach the API at all.
//
// Without a proxy key the console is protected by the network boundary, not by
// headers: the Host header is attacker-controlled, so it is only used as a
// DNS-rebinding guard after the connection itself has been classified as
// local. A local connection is either a loopback peer, or a peer the operator
// explicitly vouched for (a trusted reverse proxy forwarding a loopback
// client, or a container port mapping published on the host loopback).
//
// Cross-origin requests are unnecessary for the bundled UI (including Vite's
// development proxy). Check the request itself, not just CORS response headers:
// simple cross-origin POSTs can otherwise change configuration without preflight.
func (s *Server) browserRequestAllowed(r *http.Request) bool {
	policy := s.store.AccessPolicy()
	if !policy.AuthEnabled {
		if !localRequestTrusted(r, policy) {
			return false // Non-local and unauthenticated: refuse to expose accounts.
		}
		if !loopbackHost(r.Host) {
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
	if policy.AuthEnabled && policy.PublicBaseURL != "" {
		base, err := url.Parse(policy.PublicBaseURL)
		return err == nil && base.Scheme == u.Scheme && strings.EqualFold(base.Host, u.Host)
	}
	return false
}

// localRequestTrusted reports whether the connection source is allowed to act
// as the local machine while no proxy key is configured.
func localRequestTrusted(r *http.Request, policy store.AccessPolicy) bool {
	peer := remoteIP(r.RemoteAddr)
	if peer == nil {
		return false
	}
	// A hop that may speak for another client - the loopback interface itself
	// or an explicitly trusted proxy - makes its forwarding chain
	// authoritative. Processing it before the plain loopback shortcut matters:
	// a local reverse proxy can front a remote client, and that client must not
	// inherit local trust from the peer address.
	if peer.IsLoopback() || ipTrusted(peer, policy.TrustedProxies) {
		if hasForwardingHeaders(r) {
			client, found := forwardedClientIP(r, policy.TrustedProxies)
			return found && client.IsLoopback()
		}
	}
	if peer.IsLoopback() {
		return true
	}
	// A container cannot observe the host-side port binding. Operators that
	// published the port on the host loopback interface declare it explicitly.
	return policy.TrustLocalPortForward
}

// hasForwardingHeaders reports whether a hop described a client at all. When it
// did, that description decides; an unresolvable chain must not fall back to
// trusting the peer address.
func hasForwardingHeaders(r *http.Request) bool {
	return strings.TrimSpace(r.Header.Get("X-Forwarded-For")) != "" ||
		strings.TrimSpace(r.Header.Get("X-Real-IP")) != ""
}

func ipTrusted(ip net.IP, trusted []string) bool {
	if ip == nil {
		return false
	}
	for _, entry := range trusted {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if _, network, err := net.ParseCIDR(entry); err == nil {
			if network.Contains(ip) {
				return true
			}
			continue
		}
		if candidate := net.ParseIP(entry); candidate != nil && candidate.Equal(ip) {
			return true
		}
	}
	return false
}

func remoteIP(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = strings.Trim(remoteAddr, "[]")
	}
	return net.ParseIP(strings.TrimSpace(host))
}

// forwardedClientIP returns the client address a trusted hop vouched for.
//
// Every proxy appends the address it saw to X-Forwarded-For, so the right-most
// entry is the closest hop and everything further left was supplied by the
// client (and can be spoofed). Walking right to left and skipping our own
// proxies yields the address the trusted hop actually reported; taking the
// left-most entry would accept a prefixed "127.0.0.1" from an attacker.
// X-Real-IP is the fallback used by nginx-style proxies.
func forwardedClientIP(r *http.Request, trustedProxies []string) (net.IP, bool) {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		for index := len(parts) - 1; index >= 0; index-- {
			ip := net.ParseIP(strings.TrimSpace(parts[index]))
			if ip == nil || ipTrusted(ip, trustedProxies) {
				continue
			}
			return ip, true
		}
		return nil, false
	}
	realIP := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP")))
	if realIP == nil || ipTrusted(realIP, trustedProxies) {
		return nil, false
	}
	return realIP, true
}

func loopbackHost(host string) bool {
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
