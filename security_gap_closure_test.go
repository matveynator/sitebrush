package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityBoundaryClientIPAddressIgnoresForwardingHeadersFromPublicPeer(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	request.RemoteAddr = "198.51.100.44:4567"
	request.Header.Set("Forwarded", "for=203.0.113.99")
	request.Header.Set("X-Forwarded-For", "203.0.113.98")

	if got := clientIPAddress(request); got != "198.51.100.44" {
		t.Fatalf("SECURITY: public peer spoofed client IP through forwarding headers: %q", got)
	}
}

func TestSecurityBoundaryClientIPAddressRejectsMalformedForwardingHeaders(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	request.RemoteAddr = "127.0.0.1:4567"
	request.Header.Set("Forwarded", "for=not-an-ip")
	request.Header.Set("X-Forwarded-For", "also-not-an-ip")

	if got := clientIPAddress(request); got != "localhost" {
		t.Fatalf("SECURITY: malformed forwarding headers changed client identity: %q", got)
	}
}

func TestSecurityBoundaryClientIPAddressAcceptsForwardingOnlyFromTrustedBoundary(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	request.RemoteAddr = "127.0.0.1:4567"
	request.Header.Set("Forwarded", `for="203.0.113.77"`)

	if got := clientIPAddress(request); got != "203.0.113.77" {
		t.Fatalf("trusted forwarding boundary resolved client IP as %q", got)
	}
}

func TestSecurityBoundaryMissingPageEscapesReflectedPath(t *testing.T) {
	application, _ := newTestApplication(t)
	injectedPath := `/<img src=x onerror=alert(1)>`
	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/missing", nil)
	response := httptest.NewRecorder()

	application.renderMissingPage(response, request, injectedPath, false)

	body := response.Body.String()
	if strings.Contains(body, injectedPath) || strings.Contains(body, "<img src=x onerror=alert(1)>") {
		t.Fatalf("SECURITY: missing page reflected executable path markup: %s", body)
	}
	if !strings.Contains(body, "&lt;img") {
		t.Fatalf("missing page did not visibly escape reflected path: %s", body)
	}
}

func TestSecurityBoundaryServiceEndpointsRejectMalformedJSON(t *testing.T) {
	for _, endpoint := range []struct {
		name string
		call func(*App, http.ResponseWriter, *http.Request)
	}{
		{name: "service mail", call: func(application *App, response http.ResponseWriter, request *http.Request) {
			application.serviceMailRelayEndpoint(response, request)
		}},
		{name: "hosting snapshot", call: func(application *App, response http.ResponseWriter, request *http.Request) {
			application.hostingSnapshotEndpoint(response, request)
		}},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			application, _ := newTestApplication(t)
			request := httptest.NewRequest(http.MethodPost, "http://localhost/", strings.NewReader(`{"broken":`))
			response := httptest.NewRecorder()

			endpoint.call(application, response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("SECURITY: malformed JSON status=%d body=%q", response.Code, response.Body.String())
			}
		})
	}
}

func TestSecurityBoundaryHostingSnapshotRejectsOversizedBody(t *testing.T) {
	application := &App{}
	request := httptest.NewRequest(
		http.MethodPost,
		"http://localhost/?hosting_snapshot",
		strings.NewReader(strings.Repeat("x", int(serviceMailRelayBodyLimitBytes)+1)),
	)
	response := httptest.NewRecorder()

	application.hostingSnapshotEndpoint(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("SECURITY: oversized hosting snapshot status=%d body=%q", response.Code, response.Body.String())
	}
}
