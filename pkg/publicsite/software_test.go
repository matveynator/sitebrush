package publicsite

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerSoftwareTracksSiteStealthPolicy(t *testing.T) {
	header := make(http.Header)
	for _, test := range []struct {
		decision Decision
		want     string
	}{
		{Decision{Found: true}, "SiteBrush"},
		{Decision{Found: true, Stealth: true}, ""},
		{Decision{Known: true}, ""},
		{Decision{}, ""},
		{Decision{Found: true}, "SiteBrush"},
	} {
		header.Set("Server", "SiteBrush/secret-version")
		ApplyServerHeader(header, test.decision)
		if actual := header.Get("Server"); actual != test.want {
			t.Fatal(actual, test.want)
		}
	}
}

func TestProxyNormalizesServerSoftwareWithoutLeakingBackendVersion(t *testing.T) {
	proxy := newProxy(&Front{})
	for _, software := range []string{"SiteBrush", ""} {
		request := httptest.NewRequest(http.MethodGet, "https://example.org/", nil)
		request = request.WithContext(context.WithValue(request.Context(), proxyServerHeaderKey{}, software))
		response := &http.Response{Header: make(http.Header), Request: request}
		response.Header.Set("Server", "backend/123")
		if err := proxy.ModifyResponse(response); err != nil {
			t.Fatal(err)
		}
		if response.Header.Get("Server") != software || strings.Contains(response.Header.Get("Server"), "/") {
			t.Fatal(response.Header)
		}
	}
}
