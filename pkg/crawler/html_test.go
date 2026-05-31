package crawler

import (
	"net/url"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

func TestDecodeHTMLDetectsWindows1251WithoutDeclaration(t *testing.T) {
	sourceHTML := `<!doctype html><html><head><title>Искусство</title></head><body>Русский текст для проверки кодировки страницы</body></html>`
	sourceBytes, encodeErr := charmap.Windows1251.NewEncoder().Bytes([]byte(sourceHTML))
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}

	decodedHTML := DecodeHTML(sourceBytes, "text/html")
	if !strings.Contains(decodedHTML.Text, "Русский текст") {
		t.Fatalf("decoded text = %q, encoding=%s", decodedHTML.Text, decodedHTML.Encoding)
	}
	if decodedHTML.Encoding != "windows-1251" {
		t.Fatalf("encoding = %s", decodedHTML.Encoding)
	}
}

func TestDecodeHTMLPrefersReadableTextOverWrongHeader(t *testing.T) {
	sourceHTML := `<!doctype html><html><body>Новости и статьи на русском языке</body></html>`
	sourceBytes, encodeErr := charmap.Windows1251.NewEncoder().Bytes([]byte(sourceHTML))
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}

	decodedHTML := DecodeHTML(sourceBytes, "text/html; charset=windows-1252")
	if !strings.Contains(decodedHTML.Text, "Новости и статьи") {
		t.Fatalf("decoded text = %q, encoding=%s", decodedHTML.Text, decodedHTML.Encoding)
	}
}

func TestDecodeHTMLKeepsValidUTF8(t *testing.T) {
	sourceHTML := `<!doctype html><html><body>Русский UTF-8 текст</body></html>`

	decodedHTML := DecodeHTML([]byte(sourceHTML), "text/html")
	if decodedHTML.Text != sourceHTML {
		t.Fatalf("decoded text = %q", decodedHTML.Text)
	}
	if decodedHTML.Encoding != "utf-8" {
		t.Fatalf("encoding = %s", decodedHTML.Encoding)
	}
}

func TestExtractPageLinksKeepsSameSitePages(t *testing.T) {
	siteURL, _ := url.Parse("https://example.test/root/")
	baseURL, _ := url.Parse("https://example.test/root/index.html")
	sourceHTML := `<a href="/about">About</a><a href="https://other.test/page.html">Other</a><img src="/image.png">`

	pageLinks := ExtractPageLinks(sourceHTML, baseURL, siteURL)
	if len(pageLinks) != 1 || pageLinks[0].String() != "https://example.test/about" {
		t.Fatalf("page links = %#v", pageLinks)
	}
}
