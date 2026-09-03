package crawler

import (
	"net/url"
	"strings"
	"testing"
)

func TestDetectedResourceContentTypeFallbackBranches(t *testing.T) {
	testCases := []struct {
		name     string
		url      string
		declared string
		body     []byte
		want     string
	}{
		{
			name:     "empty body keeps declared type",
			url:      "https://example.test/photo.jpg",
			declared: "image/jpeg; charset=binary",
			body:     nil,
			want:     "image/jpeg",
		},
		{
			name:     "plain text without declaration",
			url:      "https://example.test/resource",
			declared: "",
			body:     []byte("plain text without markup"),
			want:     "text/plain",
		},
		{
			name:     "generic detection keeps useful declaration",
			url:      "https://example.test/data.bin",
			declared: "application/x-custom",
			body:     []byte{0x00, 0x01, 0x02, 0x03},
			want:     "application/x-custom",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := DetectedResourceContentType(testCase.url, testCase.declared, testCase.body); got != testCase.want {
				t.Fatalf("DetectedResourceContentType() = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestSVGAndGenericXMLHelpersFallbacks(t *testing.T) {
	if isSVGContent([]byte(`<?xml version="1.0"?><catalog><entry/></catalog>`)) {
		t.Fatal("ordinary XML was classified as SVG")
	}
	if isSVGContent([]byte(`<not-closed`)) {
		t.Fatal("malformed XML was classified as SVG")
	}
	if !isGenericXMLContentType("application/xml") {
		t.Fatal("application/xml should be treated as generic XML")
	}
	if isGenericXMLContentType("image/jpeg") {
		t.Fatal("image/jpeg should not be treated as generic XML")
	}
}

func TestDownloadedResourceExtensionGenericBranches(t *testing.T) {
	testCases := []struct {
		name        string
		resourceURL string
		contentType string
		want        string
	}{
		{"empty content type", "https://example.test/archive.zip", "", ".zip"},
		{"binary octet stream", "https://example.test/archive.tar", "binary/octet-stream", ".tar"},
		{"known MIME overrides URL", "https://example.test/download.php", "image/png", ".png"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := DownloadedResourceExtension(testCase.resourceURL, testCase.contentType); got != testCase.want {
				t.Fatalf("DownloadedResourceExtension() = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestOpenGraphRewriteFallbackBranches(t *testing.T) {
	source := `<head>` +
		`<meta property="og:title" content="Trip">` +
		`<meta property="og:image">` +
		`<meta content='/cover.jpg' property='OG:IMAGE'>` +
		`</head>`

	if got := (Parser{}).RewriteTextReferences(source, "https://example.test/", 0); got != source {
		t.Fatalf("parser without OG rewriter changed metadata: %s", got)
	}

	var calls int
	parser := Parser{
		RewriteOpenGraphReference: func(propertyName, rawRef string, _ *url.URL, _ int) string {
			calls++
			if propertyName != "og:image" || rawRef != "/cover.jpg" {
				t.Fatalf("unexpected OG callback: %q %q", propertyName, rawRef)
			}
			return "/p/cover.jpg"
		},
	}
	got := parser.RewriteTextReferences(source, "https://example.test/", 0)
	if calls != 1 {
		t.Fatalf("OG callback count = %d, want 1", calls)
	}
	if !strings.Contains(got, `content='/p/cover.jpg'`) {
		t.Fatalf("OG image was not rewritten: %s", got)
	}
}

func TestDownloadAttributeMakesNavigationLinkAResource(t *testing.T) {
	testCases := []string{
		`<a href="/download?id=1" download>file</a>`,
		`<a href="/download?id=1" download="report">file</a>`,
		`<area href="/download?id=1" download='report'>`,
	}

	for _, source := range testCases {
		var calls int
		parser := Parser{
			NormalizeURL: NormalizeURL,
			RewriteDocumentResourceReference: func(rawRef string, _ *url.URL, _ int) string {
				calls++
				return "/p/downloaded.bin"
			},
		}
		got := parser.RewriteTextReferences(source, "https://example.test/page", 0)
		if calls != 1 {
			t.Fatalf("document resource callback count = %d for %s", calls, source)
		}
		if !strings.Contains(got, `href="/p/downloaded.bin"`) {
			t.Fatalf("download link was not rewritten: %s", got)
		}
	}
}

func TestLinkedResourceAttributeRejectsNonNavigationTags(t *testing.T) {
	if isLinkedResourceDocumentAttribute("form", "action", `<form action="/report.pdf">`, "https://example.test/report.pdf") {
		t.Fatal("form action must not be treated as a linked resource")
	}
	if isLinkedResourceDocumentAttribute("a", "title", `<a title="x">`, "https://example.test/report.pdf") {
		t.Fatal("non-href anchor attribute must not be treated as a linked resource")
	}
}
