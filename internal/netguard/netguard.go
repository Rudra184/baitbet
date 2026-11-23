// Package netguard rejects URLs that resolve to a private/internal
// address (SSRF guard), before any clone or render pipeline fetches them.
// DNS rebinding after the check is a residual risk; network isolation of
// the browser-clone container is the real backstop for that path.
package netguard

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ErrUnsafeURL reports that a URL failed validation and must not be fetched.
type ErrUnsafeURL struct{ Reason string }

func (e *ErrUnsafeURL) Error() string { return e.Reason }

func unsafe(format string, a ...any) error {
	return &ErrUnsafeURL{Reason: fmt.Sprintf(format, a...)}
}

// resolveHost is overridable in tests.
var resolveHost = func(host string) ([]net.IP, error) {
	return net.LookupIP(host)
}

// ValidateURL rejects the URL unless every resolved address is public.
// Call again on every redirect hop and client-side navigation.
func ValidateURL(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil {
		return unsafe("could not parse URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return unsafe("only http:// and https:// URLs are allowed")
	}
	host := u.Hostname()
	if host == "" {
		return unsafe("URL has no hostname")
	}
	if u.User != nil {
		return unsafe("URLs with embedded credentials are not allowed")
	}
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return unsafe("localhost URLs are not allowed")
	}

	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		ips, err = resolveHost(host)
		if err != nil {
			return unsafe("could not resolve host %q", host)
		}
	}
	if len(ips) == 0 {
		return unsafe("host %q did not resolve to any address", host)
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return unsafe("host %q resolves to a private/internal address and cannot be fetched", host)
		}
	}
	return nil
}

var cgnat = mustParseCIDR("100.64.0.0/10")

func mustParseCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// isPublicIP rejects private, loopback, link-local (cloud metadata
// included), multicast, unspecified and CGNAT addresses.
func isPublicIP(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	switch {
	case ip.IsPrivate(),
		ip.IsLoopback(),
		ip.IsLinkLocalUnicast(),
		ip.IsLinkLocalMulticast(),
		ip.IsMulticast(),
		ip.IsUnspecified(),
		ip.IsInterfaceLocalMulticast():
		return false
	}
	if cgnat.Contains(ip) {
		return false
	}
	return true
}
