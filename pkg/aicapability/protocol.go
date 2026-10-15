package aicapability

import _ "embed"

const Protocol = "sitebrush-ai-editor/v1"

func Operations() []string {
	return []string{"list_pages", "read_page", "create_page", "update_page", "list_files", "upload_file", "list_revisions", "publish", "rollback", "get_site_report", "get_analytics_summary", "get_funnels", "get_paths", "get_page_behaviour", "get_forms", "get_sources", "get_page_impacts", "get_insights"}
}

// Documentation is public and provider-neutral. Secrets are exchanged only in
// request bodies and headers, never embedded in the discovery document.
//
//go:embed guide.md
var documentation string

func Documentation() string {
	return documentation
}

func OpenAPI(origin string) map[string]any {
	queryAction := map[string]any{"name": "ai", "in": "query", "required": true, "schema": map[string]any{"type": "string", "enum": append([]string{"manifest", "documentation", "openapi", "pair", "token", "exchange", "access", "pages", "page", "files", "file", "revisions", "publish", "rollback", "revoke"}, Operations()[9:]...)}}
	body := map[string]any{"type": "object", "properties": map[string]any{
		"device_code": map[string]string{"type": "string"}, "operation": map[string]any{"type": "string", "enum": []string{"create_page", "update_page"}},
		"path": map[string]string{"type": "string"}, "title": map[string]string{"type": "string"}, "html": map[string]string{"type": "string"},
		"expected_version": map[string]string{"type": "string"}, "revision_id": map[string]string{"type": "string"},
		"file_name": map[string]string{"type": "string"}, "file_content": map[string]string{"type": "string", "format": "byte"},
		"image_width": map[string]string{"type": "integer"}, "image_height": map[string]string{"type": "integer"}, "image_fit": map[string]any{"type": "string", "enum": []string{"cover", "contain", "fill"}},
	}}
	parameters := []any{queryAction}
	for _, name := range []string{"path", "days", "limit", "offset"} {
		parameterType := "integer"
		if name == "path" {
			parameterType = "string"
		}
		parameters = append(parameters, map[string]any{"name": name, "in": "query", "schema": map[string]string{"type": parameterType}})
	}
	responses := map[string]any{}
	for code, description := range map[string]string{"200": "Result; see protocol documentation for action-specific payload", "202": "authorization_pending", "400": "Invalid request or expired device code", "401": "Invalid or revoked credentials", "403": "Missing scope or access_denied", "409": "Revision conflict", "429": "Polling too fast or pairing capacity reached; respect Retry-After", "503": "Service unavailable"} {
		responses[code] = map[string]any{"description": description}
	}
	return map[string]any{
		"openapi": "3.0.3", "info": map[string]string{"title": "SiteBrush site capability", "version": "1", "description": Documentation()},
		"servers": []map[string]string{{"url": origin}}, "externalDocs": map[string]string{"url": origin + "/?ai=documentation"},
		"components": map[string]any{"securitySchemes": map[string]any{"Capability": map[string]string{"type": "apiKey", "in": "header", "name": "X-SiteBrush-Capability"}, "Session": map[string]string{"type": "http", "scheme": "bearer"}}},
		"paths": map[string]any{"/": map[string]any{
			"get":  map[string]any{"operationId": "sitebrushRead", "parameters": parameters, "security": []any{map[string]any{}, map[string]any{"Capability": []string{}, "Session": []string{}}}, "responses": responses},
			"post": map[string]any{"operationId": "sitebrushWrite", "parameters": []any{queryAction}, "security": []any{map[string]any{}, map[string]any{"Capability": []string{}}, map[string]any{"Capability": []string{}, "Session": []string{}}}, "requestBody": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": body}}}, "responses": responses},
		}},
	}
}
