package publicsite

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityClaimRequiresAuthenticatedPrivateIngress(t *testing.T) {
	original := CheckedRequest(httptest.NewRequest(http.MethodGet, "https://example.org/", nil))
	encoded := proxyMetadata(original)
	for _, test := range []struct {
		address, secret string
		expected        int
	}{
		{"203.0.113.8:1234", "private", 404},
		{"127.0.0.1:1234", "forged", 404},
		{"127.0.0.1:1234", "private", 200},
	} {
		request := httptest.NewRequest(http.MethodGet, "http://backend/", nil)
		request.RemoteAddr = test.address
		request.Header.Set(ProxyHeader, test.secret)
		request.Header.Set(MetadataHeader, encoded)
		request.Header.Set(RemoteHeader, "203.0.113.8:1234")
		request.Header.Set(SchemeHeader, "https")
		if SecurityChecked(request) {
			t.Fatal("public headers granted an enforcement claim")
		}
		response := httptest.NewRecorder()
		RestoreRequest(http.HandlerFunc(func(writer http.ResponseWriter, restored *http.Request) {
			if !SecurityChecked(restored) || restored.RemoteAddr != "203.0.113.8:1234" {
				t.Fatal("authenticated proxy did not restore enforcement")
			}
			writer.WriteHeader(http.StatusOK)
		}), "private").ServeHTTP(response, request)
		if response.Code != test.expected {
			t.Fatal(test, response.Code)
		}
	}
}

func TestEmptyProxyMetadataDoesNotRequireEncoding(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://example.org/", nil)
	if encoded := proxyMetadata(request); encoded != "" {
		t.Fatal("empty metadata was encoded", encoded)
	}
}
