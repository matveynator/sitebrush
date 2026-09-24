package httpsecurity

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestLocalRedirectTargetRejectsCrossOriginAndAmbiguousPaths(t *testing.T) {
	unsafeTargets := []string{
		"https://evil.example/path",
		"//evil.example/path",
		"/\\evil.example/path",
		"%2f%2fevil.example/path",
		"/%5cevil.example/path",
		"/safe\r\nLocation: https://evil.example/",
	}
	for _, unsafeTarget := range unsafeTargets {
		if actualTarget := LocalRedirectTarget(unsafeTarget, "/fallback"); actualTarget != "/fallback" {
			t.Fatalf("LocalRedirectTarget(%q) = %q, want fallback", unsafeTarget, actualTarget)
		}
	}
}

func TestLocalRedirectTargetPreservesSafeQueryAndFragment(t *testing.T) {
	actualTarget := LocalRedirectTarget("/docs/../account/?settings=1#profile", "/")
	if actualTarget != "/account/?settings=1#profile" {
		t.Fatalf("LocalRedirectTarget returned %q", actualTarget)
	}
}

func TestSetSensitiveCookieUsesSecurePolicyForPublicHosts(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://public.example/", nil)
	response := httptest.NewRecorder()
	SetSensitiveCookie(response, request, &http.Cookie{Name: "session", Value: "token"})
	setCookie := response.Header().Get("Set-Cookie")
	for _, attribute := range []string{"Path=/", "HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(setCookie, attribute) {
			t.Fatalf("public cookie %q does not contain %q", setCookie, attribute)
		}
	}
}

func TestSetSensitiveCookieKeepsLocalHTTPCompatibility(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/", nil)
	response := httptest.NewRecorder()
	SetSensitiveCookie(response, request, &http.Cookie{Name: "session", Value: "token"})
	setCookie := response.Header().Get("Set-Cookie")
	if strings.Contains(setCookie, "Secure") {
		t.Fatalf("local cookie unexpectedly requires HTTPS: %q", setCookie)
	}
	if !strings.Contains(setCookie, "HttpOnly") || !strings.Contains(setCookie, "SameSite=Lax") {
		t.Fatalf("local cookie is missing security attributes: %q", setCookie)
	}
}

func TestRedirectHelpersAndHTTPSDetection(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://example.com/start", nil)
	response := httptest.NewRecorder()
	RedirectLocal(response, request, "https://attacker.example", http.StatusFound)
	if response.Header().Get("Location") != "/" {
		t.Fatalf("unsafe local redirect=%q", response.Header().Get("Location"))
	}
	response = httptest.NewRecorder()
	RedirectExternal(response, request, nil, http.StatusFound)
	if response.Header().Get("Location") != "/" {
		t.Fatalf("invalid external redirect=%q", response.Header().Get("Location"))
	}
	response = httptest.NewRecorder()
	RedirectExternal(response, request, mustParseURL(t, "https://outside.example/path"), http.StatusFound)
	if response.Header().Get("Location") != "https://outside.example/path" {
		t.Fatalf("external redirect=%q", response.Header().Get("Location"))
	}
	if UsesHTTPS(nil) {
		t.Fatal("nil request reported HTTPS")
	}
	request.Header.Set("X-Forwarded-Proto", "https, http")
	if !UsesHTTPS(request) {
		t.Fatal("forwarded HTTPS not recognized")
	}
	request.Header.Set("X-Forwarded-Proto", "http")
	if UsesHTTPS(request) {
		t.Fatal("forwarded HTTP reported HTTPS")
	}
	secureRequest := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	if !UsesHTTPS(secureRequest) {
		t.Fatal("TLS request not recognized")
	}
	response = httptest.NewRecorder()
	RedirectHTTPS(response, httptest.NewRequest(http.MethodGet, "http://localhost/path", nil), http.StatusPermanentRedirect)
	if response.Header().Get("Location") != "" {
		t.Fatalf("local HTTP request was redirected: %q", response.Header().Get("Location"))
	}
	response = httptest.NewRecorder()
	RedirectHTTPS(response, request, http.StatusPermanentRedirect)
	if response.Header().Get("Location") != "https://example.com/start" {
		t.Fatalf("public HTTP redirect=%q", response.Header().Get("Location"))
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsedURL, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsedURL
}
