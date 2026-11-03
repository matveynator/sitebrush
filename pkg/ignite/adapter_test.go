package ignite

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIgniteMCPExposesRegistrationOnly(t *testing.T) {
	invoke := func(_ context.Context, operation string, arguments Arguments) (map[string]any, error) {
		return map[string]any{"operation": operation, "name": arguments.Name}, nil
	}
	for _, test := range []struct{ body, expected string }{
		{`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`, "SiteBrush Ignite"},
		{`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`, "ignite_complete"},
		{`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ignite_check_name","arguments":{"name":"shop"}}}`, "shop"},
	} {
		request := httptest.NewRequest(http.MethodPost, "http://localhost/?ignite", strings.NewReader(test.body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("MCP-Protocol-Version", "2025-11-25")
		response := httptest.NewRecorder()
		NewHandler("test", invoke).ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), test.expected) || strings.Contains(response.Body.String(), "sitebrush_publish") {
			t.Fatalf("Ignite response: status=%d body=%s", response.Code, response.Body.String())
		}
		if strings.Contains(test.body, `"method":"tools/list"`) && !strings.Contains(response.Body.String(), "Registration attempt UUID") {
			t.Fatal("Ignite did not explain the server-issued attempt field")
		}
	}
}
