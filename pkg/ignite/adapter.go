// Package ignite exposes only new-site registration through remote MCP.
package ignite

import (
	"context"
	"encoding/json"
	"net/http"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const Path = "/?ignite"

const conversationGuide = "Start in the user's language with one simple question about their goal: a new site, a copy of an old site (including WordPress), or a redesign of their old site. Ask for an old-site URL or a design example when useful. Call ignite_get_started when available. " +
	"Offer sitebrush.com as the easy default with an initial subdomain and the option to add the user's own domain later. A self-hosted SiteBrush server is for control over data, rules, and capacity; both paths can create and edit sites. " +
	"The same SiteBrush server workflow has native builds for Windows, macOS, Linux, FreeBSD, and OpenBSD on supported CPUs. For self-hosting, first ask whether the user already has a server and which OS it runs; use the matching architecture-detecting installation block at https://github.com/matveynator/sitebrush#install-the-server-version. Do not require the user to know their CPU architecture. Many older Linux distributions work, but verify actual compatibility rather than promising every machine will work. " +
	"Do not claim to have server access or start registration on the own-server choice until a read-only name check verifies the selected server's Ignite endpoint. " +
	"For hosted creation, explain the steps simply: choose an address, solve CAPTCHA in a browser, enter the email code on the returned SiteBrush page, then connect the site's editor. Ask only for information needed for the next step. " +
	"If the user selects an existing SiteBrush server, pass its domain in the server argument on every registration call and verify that ignite_check_name returns a domain under that server before beginning registration. " +
	"A name check does not reserve a name, and a site does not exist until ignite_complete returns created or ignite_status returns complete after browser confirmation. Explain what happened and what remains at each step. " +
	"After ignite_begin, show captcha_url as a normal Markdown hyperlink and include the full URL as a fallback. Let the owner click the link; do not call a browser-open tool, use a popup button, or run window.open for CAPTCHA. The browser controls whether the link opens in a new tab. " +
	"After ignite_send_code, give the user confirmation_url and ask them to enter the code there; then use ignite_status to confirm completion. Do not ask for the code in chat when this link is available. " +
	"Never repeat an email code in a reply or ask the user to resend it after a blocked tool call. " +
	"If completion is blocked or times out, check ignite_status for the same attempt, offer its confirmation_url when available, and report the actual state before suggesting another action."

const personalEditorGuide = " After creation, wait until ignite_status reports mcp_ready. Offer two ways to connect the exact returned mcp_endpoint: the owner can add it manually with ChatGPT Plugins > Add custom MCP server, using the full domain as its name and OAuth; or the assistant can use Plugin Creator to prepare a private plugin for that endpoint. Ask which way the owner prefers only if they have not already chosen. " +
	"For the Plugin Creator choice, use its available tools to inspect and reuse a plugin already bound to this endpoint, or create a private plugin with the exact mcp_endpoint in mcp.json and the full domain as its display name. Do not place credentials or tokens in the package. Install it through the host UI when possible; otherwise give its direct plugin link and the remaining install action. The owner must complete SiteBrush login and OAuth consent in either flow. Plugin Creator cannot approve OAuth for the owner. If Plugin Creator is unavailable, explain that and offer the manual steps. " +
	"Once connected, verify the domain with sitebrush_get_access. Continue the new-site, old-site copy, or redesign goal already established; ask only if it is still unknown. Read the starter homepage, save a useful first draft from the established goal, and read it back; ask one concrete content question first only if the goal lacks enough detail. Publish only when requested, after accounting for pending drafts. For old-site copying, use sitebrush_import_site on the personal MCP and poll sitebrush_get_import_status; inspect partial results and preserve content actually retrieved. Use the browser import only if the connection lacks the import tool. Use SiteBrush-Template identities for repeated sections across pages. Offer design inspiration from awwwards.com and awwwards.com/websites, dribbble.com/tags/web-design-inspiration, readymag.com/examples, and siteinspire.com, creating a distinct design rather than a pixel copy. Explain how to use the domain-named plugin with @ in ChatGPT Work. Do not claim a connection, import, edit, or analytics result without tool confirmation."

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
		{"ignite_begin", "Begin online registration for the name the user confirmed. Show its captcha_url as a normal Markdown hyperlink and full URL; do not open a browser popup. No site is created yet."},
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
