package httpsecurity

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityBoundarySameOriginMutationAllowed(t *testing.T) {
	testCases := []struct {
		name    string
		origin  string
		referer string
		fetch   string
		allowed bool
	}{
		{name: "same origin", origin: "https://example.com", allowed: true},
		{name: "different origin", origin: "https://attacker.example", allowed: false},
		{name: "same referer", referer: "https://example.com/settings", allowed: true},
		{name: "different referer", referer: "https://attacker.example/form", allowed: false},
		{name: "cross site fetch", fetch: "cross-site", allowed: false},
		{name: "same site sibling fetch", fetch: "same-site", allowed: false},
		{name: "same origin fetch", fetch: "same-origin", allowed: true},
		{name: "legacy client without source headers", allowed: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "https://example.com/action", nil)
			if testCase.origin != "" {
				request.Header.Set("Origin", testCase.origin)
			}
			if testCase.referer != "" {
				request.Header.Set("Referer", testCase.referer)
			}
			if testCase.fetch != "" {
				request.Header.Set("Sec-Fetch-Site", testCase.fetch)
			}
			if allowed := SameOriginMutationAllowed(request); allowed != testCase.allowed {
				t.Fatalf("allowed=%v want=%v", allowed, testCase.allowed)
			}
		})
	}
}
