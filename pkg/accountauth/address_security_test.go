package accountauth

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityBoundaryClientIPIgnoresUntrustedForwardingHeaders(t *testing.T) {
	request := httptest.NewRequest("GET", "https://example.com/", nil)
	request.RemoteAddr = "198.51.100.10:4321"
	request.Header.Set("X-Forwarded-For", "203.0.113.1")
	request.Header.Set("Forwarded", `for=203.0.113.2`)
	if got := ClientIP(request, ""); got != "198.51.100.10" {
		t.Fatalf("SECURITY: untrusted peer spoofed client IP: %q", got)
	}
}

func TestSecurityBoundaryClientIPWalksTrustedProxyChainFromRight(t *testing.T) {
	request := httptest.NewRequest("GET", "https://example.com/", nil)
	request.RemoteAddr = "127.0.0.1:4321"
	request.Header.Set("X-Forwarded-For", "203.0.113.20, 10.0.0.2")
	if got := ClientIP(request, "127.0.0.1,10.0.0.0/8"); got != "203.0.113.20" {
		t.Fatalf("trusted proxy chain client IP = %q", got)
	}

	request.Header.Del("X-Forwarded-For")
	request.Header.Set("Forwarded", `for="203.0.113.21:5432";proto=https, for="[10.0.0.3]:443"`)
	if got := ClientIP(request, "127.0.0.1,10.0.0.0/8"); got != "203.0.113.21" {
		t.Fatalf("Forwarded proxy chain client IP = %q", got)
	}
}

func TestSecurityBoundaryClientIPRejectsMalformedTrustedChain(t *testing.T) {
	for _, forwarded := range []string{
		"203.0.113.1, not-an-ip",
		`for=203.0.113.1, for=unknown`,
	} {
		request := httptest.NewRequest("GET", "https://example.com/", nil)
		request.RemoteAddr = "127.0.0.1:4321"
		if strings.HasPrefix(forwarded, "for=") {
			request.Header.Set("Forwarded", forwarded)
		} else {
			request.Header.Set("X-Forwarded-For", forwarded)
		}
		if got := ClientIP(request, "127.0.0.1"); got != "" {
			t.Fatalf("SECURITY: malformed trusted forwarding chain produced IP %q", got)
		}
	}

	request := httptest.NewRequest("GET", "https://example.com/", nil)
	request.RemoteAddr = "not-an-ip"
	if got := ClientIP(request, ""); got != "" {
		t.Fatalf("invalid transport peer produced IP %q", got)
	}
}

func TestSecurityBoundarySafeQueryRedactsEveryCredentialLikeField(t *testing.T) {
	raw := "page=2&Token=abc&auth_code=123&password=p&client_secret=s&challenge=c&resume=r&email_confirm=e&return_path=%2Fadmin&safe=value"
	got := SafeQuery(raw)
	for _, secret := range []string{"abc", "123", "password=p", "client_secret=s", "challenge=c", "resume=r", "email_confirm=e", "return_path=%2Fadmin"} {
		if strings.Contains(got, secret) {
			t.Fatalf("SECURITY: query log leaked secret fragment %q in %q", secret, got)
		}
	}
	if !strings.Contains(got, "safe=value") || !strings.Contains(got, "%5Bredacted%5D") {
		t.Fatalf("safe query output = %q", got)
	}
	if SafeQuery("") != "" {
		t.Fatal("empty query did not remain empty")
	}
	if SafeQuery("%zz") != "[redacted]" {
		t.Fatal("malformed query was not fully redacted")
	}
}
