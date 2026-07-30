package crawler

import (
	"encoding/json"
	"net/url"
	"path"
	"slices"
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

func TestParserRewritesInlineJavaScriptGalleryImages(t *testing.T) {
	source := `<script>
const galleryImages = [
  '/p/86e1ee24627992539f1e5f9b1f7c327560ad9469c5c9959a70f6587443ff7a0d.jpg',
  "/p/430460da69f3162faa07b19a2b9c48d6034c4fbfc09ab4b42cce542207dd2d88.jpg"
];
</script>`
	rewrittenURLs := make([]string, 0, 2)
	parser := Parser{
		RewriteResourceReference: func(rawRef string, baseURL *url.URL, _ int, referenceContext ReferenceContext) string {
			if referenceContext != ReferenceJavaScript {
				t.Fatalf("reference context = %d", referenceContext)
			}
			normalizedURL, blocked := NormalizeURL(rawRef, baseURL, referenceContext)
			if blocked {
				t.Fatalf("inline gallery image was blocked: %q", rawRef)
			}
			rewrittenURLs = append(rewrittenURLs, normalizedURL)
			return "/p/imported-" + path.Base(normalizedURL)
		},
	}

	rewritten := parser.RewriteTextReferences(source, "https://twochicks.ru/products/klatch-transformer-black/", 0)
	if len(rewrittenURLs) != 2 {
		t.Fatalf("rewritten gallery URL count = %d, URLs = %#v", len(rewrittenURLs), rewrittenURLs)
	}
	for _, expectedURL := range []string{
		"https://twochicks.ru/p/86e1ee24627992539f1e5f9b1f7c327560ad9469c5c9959a70f6587443ff7a0d.jpg",
		"https://twochicks.ru/p/430460da69f3162faa07b19a2b9c48d6034c4fbfc09ab4b42cce542207dd2d88.jpg",
	} {
		if !slices.Contains(rewrittenURLs, expectedURL) {
			t.Fatalf("gallery URL was not resolved: %q, URLs = %#v", expectedURL, rewrittenURLs)
		}
		if !strings.Contains(rewritten, "/p/imported-"+path.Base(expectedURL)) {
			t.Fatalf("gallery URL was not rewritten: %q, HTML = %s", expectedURL, rewritten)
		}
	}
}

func TestParserRewritesImagesFromUnknownJavaScriptFormats(t *testing.T) {
	source := `<script type="application/json">
{"image":"https:\/\/cdn.example\/hero.webp?width=1200\u0026format=webp"}
</script>
<script type="module">
const rootImage = "\u002Fmedia\u002Fphoto.avif?size=large";
const relativeImage = ` + "`../assets/module.png?v=2`" + `;
const dynamicImage = ` + "`/images/${imageName}.jpg`" + `;
</script>`
	rewrittenURLs := make([]string, 0, 3)
	parser := Parser{
		RewriteResourceReference: func(rawRef string, baseURL *url.URL, _ int, referenceContext ReferenceContext) string {
			if referenceContext != ReferenceJavaScript {
				t.Fatalf("reference context = %d", referenceContext)
			}
			normalizedURL, blocked := NormalizeURL(rawRef, baseURL, referenceContext)
			if blocked {
				t.Fatalf("JavaScript image was blocked: %q", rawRef)
			}
			rewrittenURLs = append(rewrittenURLs, normalizedURL)
			return "/p/imported-" + path.Base(normalizedURL)
		},
	}

	rewritten := parser.RewriteTextReferences(source, "https://shop.example/products/item/", 0)
	for _, expectedURL := range []string{
		"https://cdn.example/hero.webp?width=1200&format=webp",
		"https://shop.example/media/photo.avif?size=large",
		"https://shop.example/products/assets/module.png?v=2",
	} {
		if !slices.Contains(rewrittenURLs, expectedURL) {
			t.Fatalf("JavaScript image URL was not resolved: %q, URLs = %#v", expectedURL, rewrittenURLs)
		}
		if !strings.Contains(rewritten, "/p/imported-"+path.Base(expectedURL)) {
			t.Fatalf("JavaScript image URL was not rewritten: %q, HTML = %s", expectedURL, rewritten)
		}
	}
	if len(rewrittenURLs) != 3 {
		t.Fatalf("rewritten JavaScript URL count = %d, URLs = %#v", len(rewrittenURLs), rewrittenURLs)
	}
	if !strings.Contains(rewritten, "`${imageName}") && !strings.Contains(rewritten, "${imageName}") {
		t.Fatalf("dynamic JavaScript expression was changed: %s", rewritten)
	}
}

func TestRewriteTextReferencesPreservesLiveSiteBrushAsset(t *testing.T) {
	source := `<script src="/p/static/site_copy.js?v=123" data-sitebrush-live-asset></script>`
	parser := Parser{
		RewriteResourceReference: func(rawRef string, _ *url.URL, _ int, _ ReferenceContext) string {
			return "/p/mirrored.js"
		},
	}
	rewritten := parser.RewriteTextReferences(source, "https://example.com/", 0)
	if rewritten != source {
		t.Fatalf("live SiteBrush asset was rewritten: %s", rewritten)
	}
}

func TestRewriteTextReferencesDoesNotPreserveExternalMarkedScript(t *testing.T) {
	source := `<script src="https://attacker.example/script.js" data-sitebrush-live-asset></script>`
	parser := Parser{
		RewriteResourceReference: func(rawRef string, _ *url.URL, _ int, _ ReferenceContext) string {
			return "/p/mirrored.js"
		},
	}
	rewritten := parser.RewriteTextReferences(source, "https://example.com/", 0)
	if !strings.Contains(rewritten, `src="/p/mirrored.js"`) {
		t.Fatalf("external marked script was preserved: %s", rewritten)
	}
}
