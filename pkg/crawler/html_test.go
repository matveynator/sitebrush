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

func TestDecodeHTMLKeepsLargeDeclaredUTF8Page(t *testing.T) {
	pageText := strings.Repeat(`<section><h3>Для дома</h3><p>Выбирайте услуги и тарифы ДЛЯ ДОМА самостоятельно</p></section>`, 3000)
	sourceHTML := `<!doctype html><html><head><meta charset="utf-8"></head><body>` + pageText + `</body></html>`

	decodedHTML := DecodeHTML([]byte(sourceHTML), "text/html; charset=UTF-8")
	if decodedHTML.Encoding != "utf-8" || decodedHTML.EncodingSource != "http" || !decodedHTML.Certain {
		t.Fatalf("decode result = %#v", decodedHTML)
	}
	if !strings.Contains(decodedHTML.Text, "Для дома") || strings.Contains(decodedHTML.Text, "п■п╩я▐") {
		t.Fatalf("decoded text was corrupted: %.200s", decodedHTML.Text)
	}
}

func TestDecodeHTMLTreatsMySQLUTF8CollationAsUTF8Declaration(t *testing.T) {
	sourceHTML := `<html><head><meta charset="utf8mb4_unicode_ci"></head><body>Для дома</body></html>`

	decodedHTML := DecodeHTML([]byte(sourceHTML), "text/html; charset=utf8mb4_unicode_ci")
	if decodedHTML.Encoding != "utf-8" || decodedHTML.Text != sourceHTML {
		t.Fatalf("decode result = %#v", decodedHTML)
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
