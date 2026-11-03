// Package ignite exposes only new-site registration through remote MCP.
package ignite

import (
	"context"
	"encoding/json"
	"net/http"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const Path = "/?ignite"

type Arguments struct {
	Server  string `json:"server,omitempty" jsonschema:"SiteBrush server explicitly selected by the user; keep the same server throughout registration"`
	Name    string `json:"name,omitempty" jsonschema:"Short third-level site name requested by the user"`
	Attempt string `json:"attempt,omitempty" jsonschema:"Registration attempt UUID returned by ignite_begin. The server uses it only to match CAPTCHA and email verification steps; it is not user content or a credential for an existing site"`
	Email   string `json:"email,omitempty" jsonschema:"Email address supplied by the user for the confirmation code"`
	Code    string `json:"code,omitempty" jsonschema:"Six-digit email confirmation code supplied by the user"`
}

type Invoke func(context.Context, string, Arguments) (map[string]any, error)

func NewHandler(version string, invoke Invoke) http.Handler {
	return newHandler(version, "SiteBrush Ignite", "Create a new SiteBrush site on this server. Ask the user to confirm its name before beginning registration. The user must solve CAPTCHA in a browser and provide the email code. After creation, use Plugin Creator when available to create the user's private personal plugin from the returned MCP endpoint; then let the owner install it and authorize editing through OAuth. Ignite cannot edit sites.", invoke)
}

func NewGatewayHandler(version string, invoke Invoke) http.Handler {
	return newHandler(version, "SiteBrush", "Create a new site on sitebrush.com by default, or on another SiteBrush server explicitly chosen by the user. Pass the same server on every registration call. After creation, use Plugin Creator when available to create the user's private personal plugin from the returned MCP endpoint; then let the owner install it and authorize editing through OAuth. Editing belongs to that personal plugin.", invoke)
}

func newHandler(version, name, instructions string, invoke Invoke) http.Handler {
	server := sdk.NewServer(&sdk.Implementation{Name: name, Version: version}, &sdk.ServerOptions{
		Instructions:              instructions,
		SupportedProtocolVersions: []string{"2026-07-28", "2025-11-25", "2025-06-18"},
	})
	tools := []struct{ name, description string }{
		{"ignite_suggest_names", "Suggest available names for a new third-level site from a short name. No site is created."},
		{"ignite_check_name", "Check whether a proposed third-level site name is available. No site is created."},
		{"ignite_begin", "Begin registration for the name the user confirmed. Returns a browser CAPTCHA link."},
		{"ignite_status", "Check whether the browser CAPTCHA is complete for this registration attempt."},
		{"ignite_send_code", "Send a six-digit confirmation code to the user's email after CAPTCHA is complete. Pass the server-issued attempt identifier from ignite_begin to identify this registration."},
		{"ignite_complete", "Verify the email code and create the new site. Pass the server-issued attempt identifier from ignite_begin and the user's code. Returns its personal MCP URL."},
	}
	for _, tool := range tools {
		sdk.AddTool(server, &sdk.Tool{Name: tool.name, Description: tool.description}, func(boundary context.Context, _ *sdk.CallToolRequest, arguments Arguments) (*sdk.CallToolResult, any, error) {
			result, err := invoke(boundary, tool.name, arguments)
			if err != nil {
				return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: err.Error()}}}, nil, nil
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				return nil, nil, err
			}
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(encoded)}}, StructuredContent: result}, nil, nil
		})
	}
	// The outer route verifies HTTPS and the configured host. The SDK's loopback
	// check would reject that host behind a local reverse proxy.
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 16384, DisableLocalhostProtection: true})
	return http.HandlerFunc(func(response http.ResponseWriter, incoming *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		handler.ServeHTTP(response, incoming)
	})
}
