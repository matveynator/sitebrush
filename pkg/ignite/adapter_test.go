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
		if strings.Contains(test.body, `"method":"initialize"`) && (!strings.Contains(response.Body.String(), "ignite_get_started") || !strings.Contains(response.Body.String(), "old site") || !strings.Contains(response.Body.String(), "Add custom MCP server") || !strings.Contains(response.Body.String(), "Plugin Creator") || !strings.Contains(response.Body.String(), "normal Markdown hyperlink")) {
			t.Fatal("Ignite did not introduce the owner's goal and connection choices")
		}
		if strings.Contains(test.body, `"method":"tools/list"`) && !strings.Contains(response.Body.String(), "ignite_get_started") {
			t.Fatal("Ignite did not expose its introductory tool")
		}
	}
}

func TestIgniteClientGuidanceAndUnknownFallback(t *testing.T) {
	invoke := func(_ context.Context, operation string, _ Arguments) (map[string]any, error) {
		return map[string]any{"domain": "shop.sitebrush.com", "mcp_endpoint": "https://shop.sitebrush.com/?mcp", "status": "created"}, nil
	}
	for _, test := range []struct {
		agent, client, hint string
	}{
		{"ChatGPT MCP", "ChatGPT", "Add custom MCP server"},
		{"Claude Code", "Claude", "claude mcp add"},
		{"Grok", "Grok", "grok.com/connectors"},
		{"xAI MCP", "Grok", "grok.com/connectors"},
		{"Gemini CLI", "Gemini", "/mcp auth"},
		{"Qwen Code", "Qwen", "qwen mcp add"},
		{"Kimi Code", "Kimi", "kimi mcp auth"},
		{"generic-mcp-client", "Unknown", "remote HTTP MCP server"},
	} {
		request := httptest.NewRequest(http.MethodPost, "http://localhost/?ignite", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ignite_complete","arguments":{}}}`))
		request.Header.Set("User-Agent", test.agent)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("MCP-Protocol-Version", "2025-11-25")
		client := ClientNameFromRequest(request)
		response := httptest.NewRecorder()
		NewHandler("test", invoke, client).ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"client":"`+test.client+`"`) || !strings.Contains(response.Body.String(), test.hint) || !strings.Contains(response.Body.String(), "https://shop.sitebrush.com/?mcp") {
			t.Fatalf("client %s: status=%d body=%s", test.agent, response.Code, response.Body.String())
		}
	}
}

func TestIgniteInitializeUsesClientInfoGreeting(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://localhost/?ignite", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"Claude Code","version":"1"}}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2025-11-25")
	client := ClientNameFromRequest(request)
	response := httptest.NewRecorder()
	NewHandler("test", func(context.Context, string, Arguments) (map[string]any, error) { return nil, nil }, client).ServeHTTP(response, request)
	if client != "Claude" || response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Welcome to SiteBrush in Claude") {
		t.Fatalf("initialize greeting: client=%s status=%d body=%s", client, response.Code, response.Body.String())
	}
}
