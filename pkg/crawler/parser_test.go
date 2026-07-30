package crawler

import (
	"encoding/json"
	"net/url"
	"path"
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

func TestParserRewritesUppercaseScriptSource(t *testing.T) {
	source := `<SCRIPT language="JavaScript" type="text/javascript" src="js/CurrentTime.js"></SCRIPT>`
	var normalizedScriptURL string
	parser := Parser{
		RewriteResourceReference: func(rawRef string, baseURL *url.URL, depth int, referenceContext ReferenceContext) string {
			normalizedURL, blocked := NormalizeURL(rawRef, baseURL, referenceContext)
			if blocked {
				t.Fatalf("script source was blocked")
			}
			normalizedScriptURL = normalizedURL
			return "/p/current-time.js"
		},
	}

	rewritten := parser.RewriteTextReferences(source, "http://oldkmv.uprof.info/", 0)
	if normalizedScriptURL != "http://oldkmv.uprof.info/js/CurrentTime.js" {
		t.Fatalf("normalized script URL = %q", normalizedScriptURL)
	}
	if !strings.Contains(rewritten, `src="/p/current-time.js"`) {
		t.Fatalf("script source was not rewritten: %s", rewritten)
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

func TestParserRewritesLazyImageReferences(t *testing.T) {
	source := `<img data-src="/hero.jpg" data-original="https://cdn.example/original.jpg">` +
		`<source data-srcset="/small.jpg 1x, /large.jpg 2x">` +
		`<img data-lazy-src="/lazy.jpg" data-lazy-srcset="/lazy-small.jpg 400w, /lazy-large.jpg 800w">` +
		`<video data-bg="url('/poster.jpg')" data-background-image="/background.jpg"></video>`
	parser := Parser{
		RewriteResourceReference: func(rawRef string, _ *url.URL, _ int, _ ReferenceContext) string {
			normalizedReference := strings.Trim(strings.TrimSpace(rawRef), `"'`)
			parsedReference, parseErr := url.Parse(normalizedReference)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			return "/p/" + path.Base(parsedReference.Path)
		},
	}

	rewritten := parser.RewriteTextReferences(source, "https://page.example/", 0)
	for _, expectedReference := range []string{
		`data-src="/p/hero.jpg"`,
		`data-original="/p/original.jpg"`,
		`data-srcset="/p/small.jpg 1x, /p/large.jpg 2x"`,
		`data-lazy-src="/p/lazy.jpg"`,
		`data-lazy-srcset="/p/lazy-small.jpg 400w, /p/lazy-large.jpg 800w"`,
		`data-bg="url('/p/poster.jpg')"`,
		`data-background-image="/p/background.jpg"`,
	} {
		if !strings.Contains(rewritten, expectedReference) {
			t.Fatalf("lazy image reference %q was not rewritten: %s", expectedReference, rewritten)
		}
	}
}
