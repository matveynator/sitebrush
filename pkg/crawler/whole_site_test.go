package crawler

import (
	"net/url"
	"testing"
)

func TestWholeSiteLocalPathKeepsStartPageAtBasePath(t *testing.T) {
	startURL := mustParseTestURL(t, "https://example.com/index.html")

	if localPath := WholeSiteLocalPath("/copied", startURL, startURL); localPath != "/copied" {
		t.Fatalf("expected start page to stay at base path, got %q", localPath)
	}
}

func TestWholeSiteLocalPathMapsLinkedPagesUnderBasePath(t *testing.T) {
	startURL := mustParseTestURL(t, "https://example.com/index.html")
	pageURL := mustParseTestURL(t, "https://example.com/catalog/item/index.html?ref=menu")

	if localPath := WholeSiteLocalPath("/copied", startURL, pageURL); localPath != "/copied/catalog/item" {
		t.Fatalf("unexpected local path: %q", localPath)
	}
	if localLink := WholeSiteLocalLink("/copied", startURL, pageURL); localLink != "/copied/catalog/item?ref=menu" {
		t.Fatalf("unexpected local link: %q", localLink)
	}
}

func TestPreviewResourceKindUsesTagAndURL(t *testing.T) {
	if resourceKind := PreviewResourceKind("img", "src", "/hero"); resourceKind != "image" {
		t.Fatalf("expected image kind from img tag, got %q", resourceKind)
	}
	if resourceKind := PreviewResourceKind("", "", "/app.css"); resourceKind != "style" {
		t.Fatalf("expected style kind from CSS URL, got %q", resourceKind)
	}
}

func mustParseTestURL(t *testing.T, rawURL string) *url.URL {
	t.Helper()
	parsedURL, parseErr := url.Parse(rawURL)
	if parseErr != nil {
		t.Fatalf("parse test URL: %v", parseErr)
	}
	return parsedURL
}
