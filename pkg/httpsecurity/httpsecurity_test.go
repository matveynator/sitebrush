package httpsecurity

import (
	"net/http"
	"net/http/httptest"
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
