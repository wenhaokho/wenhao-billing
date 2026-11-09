// Package mcp holds the DNS-rebinding guard for the MCP transport.
// Python's FastMCP HostOriginGuard rejects a Host outside the allowlist
// (plus loopback) with 421 Misdirected Request. The guard here does the same
// for Host and, when present, Origin.
package mcp

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// hostGuard rejects requests whose Host (and Origin, when present) is not
// allowed. It mirrors Python's _transport_allowlist plus FastMCP's built-in
// loopback hosts.
type hostGuard struct {
	hosts   map[string]bool
	origins map[string]bool
}

func newHostGuard(cfg platform.Config) *hostGuard {
	hosts, origins := platform.TransportAllowlist(cfg.MCPPublicBaseURL)
	g := &hostGuard{
		hosts:   make(map[string]bool, len(hosts)),
		origins: make(map[string]bool, len(origins)),
	}
	for _, h := range hosts {
		g.hosts[strings.ToLower(h)] = true
	}
	for _, o := range origins {
		g.origins[strings.ToLower(o)] = true
	}
	return g
}

// wrap returns next with the guard in front. A bad Host (or bad Origin)
// is 421 "Misdirected Request" and never reaches auth or the tool.
func (g *hostGuard) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.hostOK(r.Host) {
			http.Error(w, "Misdirected Request", http.StatusMisdirectedRequest)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !g.originOK(origin) {
			http.Error(w, "Misdirected Request", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (g *hostGuard) hostOK(host string) bool {
	if host == "" {
		return false
	}
	h := strings.ToLower(host)
	if g.hosts[h] {
		return true
	}
	// Loopback is always allowed, with or without a port, as FastMCP's
	// DEFAULT_HOSTS allows. The Host of httptest servers carries a random
	// port, so the check must be on the hostname, not the exact host:port.
	return isLoopbackHost(hostnameOf(h))
}

func (g *hostGuard) originOK(origin string) bool {
	if g.origins[strings.ToLower(origin)] {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return isLoopbackHost(strings.ToLower(u.Hostname()))
}

// hostnameOf strips an optional port. Host may be "example.com:8000",
// "127.0.0.1:1234" or "[::1]:8000".
func hostnameOf(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	// No port, or a bare IPv6 literal without brackets. Trim brackets for
	// the "[::1]" spelling.
	return strings.Trim(host, "[]")
}

func isLoopbackHost(h string) bool {
	switch h {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}
