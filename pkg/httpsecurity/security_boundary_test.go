package httpsecurity

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSecurityBoundaryRedirectRejectsSchemeAndAuthoritySmuggling(t *testing.T) {
	inputs := []string{
		"javascript:alert(1)",
		"data:text/html,owned",
		"http:////evil.example/",
		"//user@evil.example/",
		"/%2f%2fevil.example/",
		"/%5c%5cevil.example/",
		"/safe%0d%0aLocation:%20https://evil.example/",
		"\\evil.example\share",
	}
	for _, input := range inputs {
		if got := LocalRedirectTarget(input, "/safe"); got != "/safe" {
			t.Fatalf("SECURITY: redirect target %q escaped current origin as %q", input, got)
		}
	}
}

func TestSecurityBoundaryExternalRedirectRejectsCredentialsAndUnsafeSchemes(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "https://example.com/start", nil)
	tests := []*url.URL{
		{Scheme: "javascript", Host: "example.com", Path: "/x"},
		{Scheme: "ftp", Host: "example.com", Path: "/x"},
		{Scheme: "https", Host: ""},
		{Scheme: "https", Host: "example.com", User: url.UserPassword("user", "secret"), Path: "/x"},
	}
	for _, target := range tests {
		response := httptest.NewRecorder()
		RedirectExternal(response, request, target, http.StatusFound)
		if got := response.Header().Get("Location"); got != "/" {
			t.Fatalf("SECURITY: unsafe external redirect was accepted: %#v -> %q", target, got)
		}
	}
}

func TestSecurityBoundaryLocalRequestRejectsHostSpoofing(t *testing.T) {
	unsafeHosts := []string{
		"127.0.0.1.evil.example",
		"localhost.evil.example",
		"127.0.0.1@evil.example",
		"evil.example:80",
	}
	for _, host := range unsafeHosts {
		request := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
		request.Host = host
		if IsLocalRequest(request) {
			t.Fatalf("SECURITY: spoofed Host %q was treated as local", host)
		}
	}

	for _, host := range []string{"localhost", "sub.localhost", "127.0.0.1:8080", "[::1]:8080"} {
		request := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
		request.Host = host
		if !IsLocalRequest(request) {
			t.Fatalf("expected local host %q to be recognized", host)
		}
	}
}

func TestSecurityBoundaryForwardedProtoRequiresExactHTTPSValue(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	for _, spoofed := range []string{"https.evil", "xhttps", "https http", "http, https"} {
		request.Header.Set("X-Forwarded-Proto", spoofed)
		if UsesHTTPS(request) {
			t.Fatalf("SECURITY: spoofed X-Forwarded-Proto %q enabled HTTPS trust", spoofed)
		}
	}
	request.Header.Set("X-Forwarded-Proto", " HTTPS , http")
	if !UsesHTTPS(request) {
		t.Fatal("canonical first forwarded HTTPS value was not accepted")
	}
}

func TestSecurityBoundarySensitiveCookieDoesNotTrustForwardedProtoForPublicHTTP(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://public.example/", nil)
	request.Header.Set("X-Forwarded-Proto", "https")
	response := httptest.NewRecorder()
	SetSensitiveCookie(response, request, &http.Cookie{Name: "session", Value: "token"})

	cookie := response.Header().Get("Set-Cookie")
	if !strings.Contains(cookie, "Secure") || !strings.Contains(cookie, "HttpOnly") {
		t.Fatalf("SECURITY: sensitive public cookie lost transport protections: %q", cookie)
	}
}

func TestSecurityBoundaryNilCookieAndNilHTTPSRedirectAreSafe(t *testing.T) {
	response := httptest.NewRecorder()
	SetSensitiveCookie(response, nil, nil)
	if got := response.Header().Get("Set-Cookie"); got != "" {
		t.Fatalf("nil cookie unexpectedly emitted header %q", got)
	}
	RedirectHTTPS(response, nil, http.StatusPermanentRedirect)
	if got := response.Header().Get("Location"); got != "" {
		t.Fatalf("nil request unexpectedly emitted redirect %q", got)
	}
}
