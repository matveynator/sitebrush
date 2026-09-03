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

func TestParserDoesNotTreatNavigationOrEmbeddedDocumentsAsPageResources(t *testing.T) {
	source := `<a href="/brochure.pdf">Download</a><form action="/search"><button>Search</button></form><iframe src="https://frames.example/widget.html"></iframe><img src="/hero.png">`
	rewrittenResources := make([]string, 0, 1)
	parser := Parser{
		NormalizeURL: NormalizeURL,
		RewriteResourceReference: func(rawRef string, baseURL *url.URL, _ int, referenceContext ReferenceContext) string {
			normalizedURL, blocked := NormalizeURL(rawRef, baseURL, referenceContext)
			if blocked {
				return rawRef
			}
			rewrittenResources = append(rewrittenResources, normalizedURL)
			return "/p/hero.png"
		},
	}

	rewritten := parser.RewriteTextReferences(source, "https://source.example/page", 0)
	if len(rewrittenResources) != 1 || rewrittenResources[0] != "https://source.example/hero.png" {
		t.Fatalf("page resources = %#v, want only hero image", rewrittenResources)
	}
	for _, preservedReference := range []string{`href="/brochure.pdf"`, `action="/search"`, `src="https://frames.example/widget.html"`} {
		if !strings.Contains(rewritten, preservedReference) {
			t.Fatalf("navigation reference %q changed in %s", preservedReference, rewritten)
		}
	}
}

func TestParserRewritesLinkedFileAsDocumentResource(t *testing.T) {
	source := `<a href="/maps/2018-2_map.jpg">Open map</a><a href="/download?id=manual" download>Manual</a><a href="/about.php">About</a><form action="/search"><button>Search</button></form>`
	linkedResources := make([]string, 0, 2)
	parser := Parser{
		NormalizeURL: NormalizeURL,
		RewriteDocumentResourceReference: func(rawRef string, baseURL *url.URL, _ int) string {
			normalizedURL, blocked := NormalizeURL(rawRef, baseURL, ReferenceDocument)
			if blocked {
				return rawRef
			}
			linkedResources = append(linkedResources, normalizedURL)
			return "/p/" + path.Base(normalizedURL)
		},
		DocumentURLRewriter: func(normalizedURL string) (string, bool) {
			return "/local/about.php", strings.HasSuffix(normalizedURL, "/about.php")
		},
	}

	rewritten := parser.RewriteTextReferences(source, "https://kavtrans.ru/", 0)
	if !slices.Equal(linkedResources, []string{"https://kavtrans.ru/maps/2018-2_map.jpg", "https://kavtrans.ru/download?id=manual"}) {
		t.Fatalf("linked resources = %#v", linkedResources)
	}
	for _, expectedReference := range []string{`href="/p/2018-2_map.jpg"`, `href="/p/download?id=manual"`, `href="/local/about.php"`, `action="/search"`} {
		if !strings.Contains(rewritten, expectedReference) {
			t.Fatalf("expected reference %q missing from %s", expectedReference, rewritten)
		}
	}
}

func TestParserDoesNotFetchExternalNavigationLinks(t *testing.T) {
	externalLinks := []string{
		"http://arhyz-resort.ru/",
		"http://hotel.krutizna.ru",
		"http://hotelcheget.ru",
		"http://www.alexika.ru/catalog/palatki-tracking-freedom-2-new.html",
		"http://www.dombayclub.ru/index.php?showuser=3737",
		"http://www.gosuslugi.ru/43708",
		"http://www.hotelscazka.ru/main.html",
		"http://www.kogutai.ru",
		"http://www.m-hotel.info",
		"http://www.narodnakra.ru",
		"http://www.pikevropy.ru",
		"http://www.winterparadize.ru",
		"https://binged.it/2iq7S0l",
		"https://esbyt.elseti.ru/index.php/13-pyatigorsk/russkoe-geograficheskoe-obshchestvo/publikatsii/33-kali-050607",
		"https://sakrusenergo.ge/ru/%D0%BA%D0%B0%D0%B2%D0%BA%D0%B0%D1%81%D0%B8%D0%BE%D0%BD%D0%B8-500kv/",
		"https://t.me/kavtrans",
		"https://t.me/kavtrans_ru",
		"https://www.rgo.ru/ru/article/pervyy-v-gorah-kavkaza-otmechaem-yubiley-chlena-rgo-andreya-pastuhova",
	}
	var source strings.Builder
	for _, externalLink := range externalLinks {
		source.WriteString(`<a href="` + externalLink + `">External page</a>`)
	}
	parser := Parser{
		NormalizeURL: NormalizeURL,
		RewriteResourceReference: func(rawRef string, _ *url.URL, _ int, _ ReferenceContext) string {
			t.Fatalf("external navigation link was processed as a static resource: %s", rawRef)
			return rawRef
		},
		RewriteDocumentResourceReference: func(rawRef string, _ *url.URL, _ int) string {
			t.Fatalf("external navigation link was treated as a resource: %s", rawRef)
			return rawRef
		},
	}

	rewritten := parser.RewriteTextReferences(source.String(), "https://kavtrans.ru/", 0)
	if rewritten != source.String() {
		t.Fatalf("external navigation links changed: %s", rewritten)
	}
}

func TestParserStillFetchesImageResourcesWithFailedSourceURLs(t *testing.T) {
	source := `<img src="http://kavtrans.ru/overlays/02.png"><img src="http://kavtrans.ru/timthumb.php?src=gallery/2018-2/07.jpg&amp;w=880&amp;zc=1&amp;q=75">`
	rewrittenResources := make([]string, 0, 2)
	parser := Parser{
		NormalizeURL: NormalizeURL,
		RewriteResourceReference: func(rawRef string, baseURL *url.URL, _ int, referenceContext ReferenceContext) string {
			normalizedURL, blocked := NormalizeURL(rawRef, baseURL, referenceContext)
			if blocked {
				t.Fatalf("image resource was blocked: %s", rawRef)
			}
			rewrittenResources = append(rewrittenResources, normalizedURL)
			return rawRef
		},
	}

	parser.RewriteTextReferences(source, "http://kavtrans.ru/", 0)
	if len(rewrittenResources) != 2 {
		t.Fatalf("image resources = %#v, want both source URLs", rewrittenResources)
	}
}

func TestParserRewritesOpenGraphURLAndImageMetadata(t *testing.T) {
	source := `<head><META CONTENT="http://kavtrans.ru/Trip.php" PROPERTY="OG:URL"><meta property='og:image' content='/gallery/cover.jpg'><meta property="og:title" content="Trip"></head>`
	metadataReferences := make([]string, 0, 2)
	resourceReferences := make([]string, 0)
	parser := Parser{
		NormalizeURL: NormalizeURL,
		RewriteOpenGraphReference: func(propertyName, rawRef string, _ *url.URL, _ int) string {
			metadataReferences = append(metadataReferences, propertyName+"="+rawRef)
			switch propertyName {
			case "og:url":
				return "https://copy.example/Trip.php"
			case "og:image":
				return "https://copy.example/p/cover.jpg"
			default:
				return rawRef
			}
		},
		RewriteResourceReference: func(rawRef string, _ *url.URL, _ int, _ ReferenceContext) string {
			resourceReferences = append(resourceReferences, rawRef)
			return rawRef
		},
	}

	rewritten := parser.RewriteTextReferences(source, "http://kavtrans.ru/", 0)
	if !slices.Equal(metadataReferences, []string{"og:url=http://kavtrans.ru/Trip.php", "og:image=/gallery/cover.jpg"}) {
		t.Fatalf("metadata references = %#v", metadataReferences)
	}
	if len(resourceReferences) != 0 {
		t.Fatalf("rewritten Open Graph values entered generic resource handling: %#v", resourceReferences)
	}
	for _, expectedReference := range []string{`CONTENT="https://copy.example/Trip.php"`, `content='https://copy.example/p/cover.jpg'`} {
		if !strings.Contains(rewritten, expectedReference) {
			t.Fatalf("expected Open Graph reference %q missing from %s", expectedReference, rewritten)
		}
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
