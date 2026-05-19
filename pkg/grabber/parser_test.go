package grabber

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestParserResolvesRootAssetJavaScriptReferences(t *testing.T) {
	baseURL, parseErr := url.Parse("https://karman.cafe/assets/entries/entry-server-routing.js")
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	normalizedURL, blocked := NormalizeURL("assets/chunks/chunk-BXl3LOEh.js", baseURL, ReferenceJavaScript)
	if blocked {
		t.Fatal("javascript resource was blocked")
	}
	if normalizedURL != "https://karman.cafe/assets/chunks/chunk-BXl3LOEh.js" {
		t.Fatalf("normalized URL = %q", normalizedURL)
	}
}

func TestParserRewritesDataManifestRelativeURLs(t *testing.T) {
	baseURL, parseErr := url.Parse("https://karman.cafe/")
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	manifest := `{"name":"Karman","start_url":"/","scope":"/","id":"/","icons":[{"src":"./assets/apple-touch-icon.png"}],"shortcuts":[{"name":"Menu","url":"/menu","icons":[{"src":"./assets/menu.png"}]}],"share_target":{"action":"/share"}}`
	source := `<link rel="manifest" href="data:application/manifest+json,` + url.PathEscape(manifest) + `">`
	parser := Parser{}

	rewritten := parser.RewriteTextReferences(source, baseURL.String(), 0)
	hrefStart := strings.Index(rewritten, `href="`)
	if hrefStart < 0 {
		t.Fatalf("manifest href not found: %s", rewritten)
	}
	hrefStart += len(`href="`)
	hrefEnd := strings.Index(rewritten[hrefStart:], `"`)
	if hrefEnd < 0 {
		t.Fatalf("manifest href is not closed: %s", rewritten)
	}
	_, payload, ok := SplitDataURL(rewritten[hrefStart : hrefStart+hrefEnd])
	if !ok {
		t.Fatalf("rewritten href is not a data URL: %s", rewritten)
	}
	decodedPayload, decodeErr := url.PathUnescape(payload)
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	var manifestObject map[string]any
	if err := json.Unmarshal([]byte(decodedPayload), &manifestObject); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	for _, forbiddenField := range []string{"start_url", "scope", "id", "share_target"} {
		if _, found := manifestObject[forbiddenField]; found {
			t.Fatalf("manifest keeps origin-bound field %q: %#v", forbiddenField, manifestObject)
		}
	}
	icons := manifestObject["icons"].([]any)
	icon := icons[0].(map[string]any)
	if icon["src"] != "https://karman.cafe/assets/apple-touch-icon.png" {
		t.Fatalf("icon src = %#v", icon["src"])
	}
	shortcuts := manifestObject["shortcuts"].([]any)
	shortcut := shortcuts[0].(map[string]any)
	if _, found := shortcut["url"]; found {
		t.Fatalf("shortcut keeps origin-bound URL: %#v", shortcut)
	}
	shortcutIcons := shortcut["icons"].([]any)
	shortcutIcon := shortcutIcons[0].(map[string]any)
	if shortcutIcon["src"] != "https://karman.cafe/assets/menu.png" {
		t.Fatalf("shortcut icon src = %#v", shortcutIcon["src"])
	}
}
