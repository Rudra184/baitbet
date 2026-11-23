package netguard

import (
	"net"
	"testing"
)

func withResolver(t *testing.T, answers map[string][]net.IP) {
	t.Helper()
	orig := resolveHost
	resolveHost = func(host string) ([]net.IP, error) {
		if ips, ok := answers[host]; ok {
			return ips, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host}
	}
	t.Cleanup(func() { resolveHost = orig })
}

func TestValidateURL_RejectsNonHTTP(t *testing.T) {
	if err := ValidateURL("ftp://example.com/x"); err == nil {
		t.Fatal("expected ftp:// to be rejected")
	}
	if err := ValidateURL("file:///etc/passwd"); err == nil {
		t.Fatal("expected file:// to be rejected")
	}
}

func TestValidateURL_RejectsEmbeddedCredentials(t *testing.T) {
	if err := ValidateURL("https://user:pass@example.com"); err == nil {
		t.Fatal("expected embedded credentials to be rejected")
	}
}

func TestValidateURL_RejectsLocalhost(t *testing.T) {
	for _, u := range []string{"http://localhost/", "http://foo.localhost/", "http://LOCALHOST:8080/"} {
		if err := ValidateURL(u); err == nil {
			t.Fatalf("expected %q to be rejected", u)
		}
	}
}

func TestValidateURL_RejectsIPLiteralsByClass(t *testing.T) {
	cases := []string{
		"http://127.0.0.1/",       // loopback
		"http://10.0.0.5/",        // private
		"http://192.168.1.1/",     // private
		"http://172.16.0.1/",      // private
		"http://169.254.169.254/", // link-local / cloud metadata
		"http://100.64.0.1/",      // CGNAT
		"http://0.0.0.0/",         // unspecified
		"http://[::1]/",           // IPv6 loopback
		"http://[fe80::1]/",       // IPv6 link-local
		"http://[::ffff:127.0.0.1]/", // IPv4-mapped loopback
	}
	for _, u := range cases {
		if err := ValidateURL(u); err == nil {
			t.Fatalf("expected %q to be rejected", u)
		}
	}
}

func TestValidateURL_AcceptsPublicIPLiteral(t *testing.T) {
	if err := ValidateURL("https://8.8.8.8/"); err != nil {
		t.Fatalf("expected public IP literal to pass, got %v", err)
	}
}

func TestValidateURL_ResolvesHostnameAndRejectsIfAnyAnswerIsPrivate(t *testing.T) {
	withResolver(t, map[string][]net.IP{
		"rebind.example": {net.ParseIP("8.8.8.8"), net.ParseIP("10.0.0.1")},
	})
	if err := ValidateURL("https://rebind.example/"); err == nil {
		t.Fatal("expected rejection when any resolved answer is private")
	}
}

func TestValidateURL_AcceptsHostnameResolvingOnlyToPublicIPs(t *testing.T) {
	withResolver(t, map[string][]net.IP{
		"public.example": {net.ParseIP("93.184.216.34")},
	})
	if err := ValidateURL("https://public.example/path"); err != nil {
		t.Fatalf("expected public hostname to pass, got %v", err)
	}
}

func TestValidateURL_RejectsUnresolvableHost(t *testing.T) {
	withResolver(t, map[string][]net.IP{})
	if err := ValidateURL("https://does-not-resolve.example/"); err == nil {
		t.Fatal("expected unresolvable host to be rejected")
	}
}
