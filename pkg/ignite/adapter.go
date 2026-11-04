// Package ignite exposes only new-site registration through remote MCP.
package ignite

import (
	"context"
	"encoding/json"
	"net/http"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const Path = "/?ignite"

const conversationGuide = "At the start of an Ignite conversation, explain in the user's language what SiteBrush does and offer both creation paths before asking for a site name. Call ignite_get_started when available. " +
	"SiteBrush creates and edits websites and can also copy an existing design. " +
	"The choices are: create a public site on this server with Ignite, or download the SiteBrush server application and create a site on the user's own server with your guidance. " +
	"For the own-server choice, first ask whether the user has a server; if so, ask its operating system, architecture, and domain, point to https://github.com/matveynator/sitebrush#downloads and https://github.com/matveynator/sitebrush#install-the-server-version, then guide installation and site creation step by step. " +
	"Do not claim to have server access or start registration on the own-server choice until a read-only name check verifies the selected server's Ignite endpoint. " +
	"For hosted creation, explain the steps: choose an address, solve CAPTCHA in a browser, enter the email code on the returned SiteBrush confirmation page, then connect the new site's private editor. " +
	"If the user selects an existing SiteBrush server, pass its domain in the server argument on every registration call and verify that ignite_check_name returns a domain under that server before beginning registration. " +
	"A name check does not reserve a name, and a site does not exist until ignite_complete returns created or ignite_status returns complete after browser confirmation. Explain what happened and what remains at each step. " +
	"After ignite_send_code, give the user confirmation_url and ask them to enter the code there; then use ignite_status to confirm completion. Do not ask for the code in chat when this link is available. " +
	"Never repeat an email code in a reply or ask the user to resend it after a blocked tool call. " +
	"If completion is blocked or times out, check ignite_status for the same attempt, offer its confirmation_url when available, and report the actual state before suggesting another action."

const personalEditorGuide = " After creation, wait until ignite_status reports mcp_ready. Invoke Plugin Creator in this conversation when available: reuse an existing private plugin for the same site, or create one with the exact returned mcp_endpoint as a streamable HTTP server, OAuth, and the returned domain as its display name. Never embed credentials. Give the owner the plugin link and guide installation and OAuth consent. Once connected, discover the personal plugin's tools, verify its site with sitebrush_get_access, read the starter homepage, and ask what the first page should contain. Use that plugin to make the requested edit, then explain how the owner can select the domain plugin from ChatGPT's @ menu to request future page edits and publication. If Plugin Creator is unavailable, give manual connection steps and a ready-to-send prompt for continuing in a ChatGPT chat with Plugin Creator. Do not claim a plugin or page edit exists without tool confirmation."

type Arguments struct {
	Server  string `json:"server,omitempty" jsonschema:"SiteBrush server explicitly selected by the user; keep the same server throughout registration"`
	Name    string `json:"name,omitempty" jsonschema:"Short third-level site name requested by the user"`
	Attempt string `json:"attempt,omitempty" jsonschema:"Registration attempt UUID returned by ignite_begin. The server uses it only to match CAPTCHA and email verification steps; it is not user content or a credential for an existing site"`
	Email   string `json:"email,omitempty" jsonschema:"Email address supplied by the user for the confirmation code"`
	Code    string `json:"code,omitempty" jsonschema:"Six-digit email confirmation code supplied by the user"`
}

type Invoke func(context.Context, string, Arguments) (map[string]any, error)

func NewHandler(version string, invoke Invoke) http.Handler {
	return newHandler(version, "SiteBrush Ignite", conversationGuide+" Ask the user to confirm the selected name before ignite_begin."+personalEditorGuide+" Ignite cannot edit sites.", invoke)
}

func NewGatewayHandler(version string, invoke Invoke) http.Handler {
	return newHandler(version, "SiteBrush", conversationGuide+" Online creation uses sitebrush.com by default, or another SiteBrush server explicitly chosen by the user. Pass the same server on every registration call. Ask the user to confirm the selected name before ignite_begin."+personalEditorGuide+" Editing belongs to that personal plugin.", invoke)
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
		{"ignite_status", "Check the registration attempt after CAPTCHA or browser email confirmation. When an email code is pending, returns a SiteBrush confirmation_url where the user can enter the code privately."},
		{"ignite_send_code", "Send a six-digit confirmation code to the user's email after CAPTCHA is complete. Pass the server-issued attempt identifier from ignite_begin. Returns confirmation_url for private browser code entry; the site is still uncreated."},
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
