package crawler

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
)

func TestCrawlerContentTypeExtensionCoverage(t *testing.T) {
	tests := map[string]string{
		"text/css": ".css",
		"application/javascript": ".js",
		"text/javascript": ".js",
		"application/x-javascript": ".js",
		"application/ecmascript": ".mjs",
		"text/ecmascript": ".mjs",
		"image/png": ".png",
		"image/jpeg": ".jpg",
		"image/gif": ".gif",
		"image/svg+xml": ".svg",
		"image/webp": ".webp",
		"image/x-icon": ".ico",
		"image/vnd.microsoft.icon": ".ico",
		"font/woff": ".woff",
		"font/woff2": ".woff2",
		"font/ttf": ".ttf",
		"application/x-font-ttf": ".ttf",
		"font/otf": ".otf",
		"application/x-font-opentype": ".otf",
		"application/vnd.ms-fontobject": ".eot",
		"video/mp4": ".mp4",
		"video/webm": ".webm",
		"video/quicktime": ".mov",
		"audio/mpeg": ".mp3",
		"audio/ogg": ".ogg",
		"audio/wav": ".wav",
		"audio/wave": ".wav",
		"audio/x-wav": ".wav",
	}
	for contentType, want := range tests {
		if got := ResourceExtensionFromContentType(contentType); got != want {
			t.Errorf("%s -> %q, want %q", contentType, got, want)
		}
	}
	if got := ResourceExtensionFromContentType("application/x-sitebrush-unknown"); got != "" {
		t.Fatalf("unknown content type extension=%q", got)
	}
}

func TestCrawlerPageAndHostClassificationCoverage(t *testing.T) {
	left, _ := url.Parse("https://Example.COM/path")
	right, _ := url.Parse("https://example.com/other")
	if !SameHost(left, right) {
		t.Fatal("same host with case difference was not recognized")
	}
	if SameHost(nil, right) || SameHost(left, nil) {
		t.Fatal("nil URL matched a host")
	}
	for _, raw := range []string{
		"https://example.com/",
		"https://example.com/docs",
		"https://example.com/index.html",
		"https://example.com/page.php",
		"https://example.com/view.aspx",
		"https://example.com/a.jsp",
		"https://example.com/run.cgi",
	} {
		u, _ := url.Parse(raw)
		if !IsPageURL(u) {
			t.Errorf("page URL rejected: %s", raw)
		}
	}
	for _, raw := range []string{"https://example.com/app.js", "https://example.com/a.png", "https://example.com/file.pdf"} {
		u, _ := url.Parse(raw)
		if IsPageURL(u) {
			t.Errorf("resource URL treated as page: %s", raw)
		}
	}
	if IsPageURL(nil) {
		t.Fatal("nil URL treated as page")
	}
}

func TestCrawlerTextDecodeAndEncodingHelpersCoverage(t *testing.T) {
	bomText := append([]byte{0xEF, 0xBB, 0xBF}, []byte("hello")...)
	utf8Result := DecodeText(bomText, "text/plain; charset=utf-8")
	if utf8Result.Text != "hello" || utf8Result.Encoding != "utf-8" {
		t.Fatalf("UTF-8 decode=%#v", utf8Result)
	}
	if got := charsetFromContentType("broken; charset=="); got != "" {
		t.Fatalf("broken content type charset=%q", got)
	}
	if _, ok := decodeWithEncoding([]byte{0xff}, "utf-8", nil); ok {
		t.Fatal("invalid UTF-8 decoded successfully")
	}
	if got := pathExt("/A/FILE.HTML"); got != ".HTML" {
		t.Fatalf("pathExt=%q", got)
	}
}

func TestCrawlerCSSAndManifestHelperCoverage(t *testing.T) {
	rewrite := func(raw string) string { return "/local/" + strings.TrimLeft(raw, "/") }
	css := `@import "theme.css"; @import url("/base.css"); .x{background:url("image.png")}`
	got := RewriteCSSImportReferences(css, rewrite)
	if !strings.Contains(got, "/local/theme.css") {
		t.Fatalf("CSS import not rewritten: %s", got)
	}
	got = RewriteCSSURLReferences(got, rewrite)
	if !strings.Contains(got, "/local/image.png") {
		t.Fatalf("CSS url not rewritten: %s", got)
	}
	if strings.Contains(got, "/local//local/base.css") {
		t.Fatalf("@import url was rewritten twice: %s", got)
	}

	header, payload, ok := SplitDataURL(" data:application/manifest+json,%7B%22name%22%3A%22x%22%7D ")
	if !ok || !strings.Contains(header, "manifest+json") || payload == "" {
		t.Fatalf("SplitDataURL=%q %q %v", header, payload, ok)
	}
	if _, _, ok := SplitDataURL("https://example.com/manifest.json"); ok {
		t.Fatal("ordinary URL treated as data URL")
	}
	if _, _, ok := SplitDataURL("data:application/json"); ok {
		t.Fatal("data URL without comma accepted")
	}

	decoded, err := decodeDataURLPayload("data:application/json", "%7B%22x%22%3A1%7D")
	if err != nil || string(decoded) != `{"x":1}` {
		t.Fatalf("decoded data payload=%q err=%v", decoded, err)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(`{"x":1}`))
	decoded, err = decodeDataURLPayload("data:application/json;base64", encoded)
	if err != nil || string(decoded) != `{"x":1}` {
		t.Fatalf("decoded base64 payload=%q err=%v", decoded, err)
	}
	if _, err := decodeDataURLPayload("data:application/json;base64", "%%%"); err == nil {
		t.Fatal("invalid base64 data URL accepted")
	}
	if got := manifestDataURLHeader("data:application/json;base64"); strings.Contains(strings.ToLower(got), "base64") {
		t.Fatalf("manifest header retained base64 after rewrite: %q", got)
	}
	if got := manifestDataURLHeader("data:"); got != "data:application/manifest+json" {
		t.Fatalf("default manifest header=%q", got)
	}
}

func TestCrawlerWholeSiteAndPreviewCoverage(t *testing.T) {
	start, _ := url.Parse("https://example.com/index.html")
	same, _ := url.Parse("https://EXAMPLE.com/")
	other, _ := url.Parse("https://example.com/docs/index.html?x=1")
	if WholeSitePageKey(nil) != "" {
		t.Fatal("nil page key was nonempty")
	}
	if WholeSitePageKey(start) != WholeSitePageKey(same) {
		t.Fatal("canonical index page key mismatch")
	}
	if got := WholeSiteLocalPath("/mirror", start, other); got != "/mirror/docs" {
		t.Fatalf("local path=%q", got)
	}
	if got := WholeSiteLocalLink("/mirror", start, other); got != "/mirror/docs?x=1" {
		t.Fatalf("local link=%q", got)
	}
	if got := WholeSitePageKey(&url.URL{Path:"/x"}); got != "" {
		t.Fatalf("hostless page key=%q", got)
	}
	if CleanPath(" docs/../safe ") != "/safe" || CleanPath("") != "/" {
		t.Fatal("CleanPath normalization failed")
	}

	tests := []struct{ tag, attr, ref, want string }{
		{"script", "src", "x", "script"},
		{"link", "href", "x", "style"},
		{"img", "src", "x", "image"},
		{"source", "src", "x", "image"},
		{"video", "src", "x", "video"},
		{"audio", "src", "x", "audio"},
		{"iframe", "src", "x", "embedded"},
		{"embed", "src", "x", "embedded"},
		{"object", "data", "x", "embedded"},
		{"div", "poster", "x", "image"},
		{"a", "href", "file.pdf", "file"},
		{"a", "href", "image.png", "image"},
	}
	for _, tc := range tests {
		if got := PreviewResourceKind(tc.tag, tc.attr, tc.ref); got != tc.want {
			t.Errorf("PreviewResourceKind(%s,%s,%s)=%q want %q", tc.tag, tc.attr, tc.ref, got, tc.want)
		}
	}
}

func TestCrawlerSecurityRejectsSuspiciousAndKeepsStaticReferences(t *testing.T) {
	for _, ref := range []string{"javascript:alert(1)", "data:text/html,<script>x</script>", "vbscript:msgbox(1)"} {
		if !IsSuspiciousReference(ref) {
			t.Errorf("SECURITY: suspicious reference accepted: %q", ref)
		}
	}
	if IsSuspiciousReference("/assets/app.js") {
		t.Fatal("normal static reference marked suspicious")
	}
	if got := FirstPathSegment(" /assets/images/x.png "); got != "assets" {
		t.Fatalf("first segment=%q", got)
	}
	if got := FirstPathSegment("/"); got != "" {
		t.Fatalf("root first segment=%q", got)
	}
}
