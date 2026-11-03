// Package ignite exposes only new-site registration through remote MCP.
package ignite

import (
	"context"
	"encoding/json"
	"net/http"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const Path = "/?ignite"

const conversationGuide = "When the user asks to create a site without choosing where, call ignite_get_started first and present its two choices in the user's language before asking for a name. " +
	"Explain that SiteBrush creates and edits websites and can also copy an existing design. " +
	"The choices are: create a public site on this server with Ignite, or download the SiteBrush server application and create a site on the user's own server with your guidance. " +
	"For the own-server choice, first ask whether the user has a server; if so, ask its operating system and architecture, point to https://github.com/matveynator/sitebrush#downloads and https://github.com/matveynator/sitebrush#install-the-server-version, then guide installation and site creation step by step. " +
	"Do not claim to have server access or call Ignite registration tools for the own-server choice unless that server's Ignite endpoint is actually connected and selected by the user. " +
	"For hosted creation, explain the steps: choose an address, solve CAPTCHA in a browser, confirm an email code, then connect the new site's private editor. " +
	"A name check does not reserve a name, and a site does not exist until ignite_complete returns created. Explain what happened and what remains at each step. " +
	"Never repeat an email code in a reply or ask the user to resend it after a blocked tool call. " +
	"If completion is blocked or times out, check ignite_status for the same attempt and report the actual state before suggesting another action."

type Arguments struct {
	Server  string `json:"server,omitempty" jsonschema:"SiteBrush server explicitly selected by the user; keep the same server throughout registration"`
	Name    string `json:"name,omitempty" jsonschema:"Short third-level site name requested by the user"`
	Attempt string `json:"attempt,omitempty" jsonschema:"Registration attempt UUID returned by ignite_begin. The server uses it only to match CAPTCHA and email verification steps; it is not user content or a credential for an existing site"`
	Email   string `json:"email,omitempty" jsonschema:"Email address supplied by the user for the confirmation code"`
	Code    string `json:"code,omitempty" jsonschema:"Six-digit email confirmation code supplied by the user"`
}

type Invoke func(context.Context, string, Arguments) (map[string]any, error)

func NewHandler(version string, invoke Invoke) http.Handler {
	return newHandler(version, "SiteBrush Ignite", conversationGuide+" Ask the user to confirm the selected name before ignite_begin. After creation, use Plugin Creator when available to create the user's private personal plugin from the returned MCP endpoint; then let the owner install it and authorize editing through OAuth. Ignite cannot edit sites.", invoke)
}

func NewGatewayHandler(version string, invoke Invoke) http.Handler {
	return newHandler(version, "SiteBrush", conversationGuide+" Online creation uses sitebrush.com by default, or another SiteBrush server explicitly chosen by the user. Pass the same server on every registration call. Ask the user to confirm the selected name before ignite_begin. After creation, use Plugin Creator when available to create the user's private personal plugin from the returned MCP endpoint; then let the owner install it and authorize editing through OAuth. Editing belongs to that personal plugin.", invoke)
}

func newHandler(version, name, instructions string, invoke Invoke) http.Handler {
	server := sdk.NewServer(&sdk.Implementation{Name: name, Version: version}, &sdk.ServerOptions{
		Instructions:              instructions,
		SupportedProtocolVersions: []string{"2026-07-28", "2025-11-25", "2025-06-18"},
	})
	tools := []struct{ name, description string }{
		{"ignite_get_started", "Explain SiteBrush and the two ways to create a site: on this server with Ignite or on the user's own server. Read this before asking for a name when the user has not chosen a path. No site is created."},
		{"ignite_suggest_names", "Suggest available names for a new third-level site from a short name. No site is created."},
		{"ignite_check_name", "Check whether a proposed third-level site name is available. This does not reserve the name or create a site."},
		{"ignite_begin", "Begin online registration for the name the user confirmed. Returns a browser CAPTCHA link; no site is created yet."},
		{"ignite_status", "Check whether the browser CAPTCHA is complete for this registration attempt."},
		{"ignite_send_code", "Send a six-digit confirmation code to the user's email after CAPTCHA is complete. Pass the server-issued attempt identifier from ignite_begin. The site is still uncreated."},
		{"ignite_complete", "Verify the email code and create the new site. Pass the server-issued attempt identifier from ignite_begin and the user's code. Returns its personal MCP URL only after creation."},
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
