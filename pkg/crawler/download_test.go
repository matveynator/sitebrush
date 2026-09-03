package crawler

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type htmlDownloadRoundTripper func(*http.Request) (*http.Response, error)

func (roundTripper htmlDownloadRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTripper(request)
}

func TestDownloadHTMLPageDetectsHTMLBehindPHPContentType(t *testing.T) {
	pageURL, err := url.Parse("https://example.test/article.php")
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: htmlDownloadRoundTripper(func(request *http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("Content-Type", "application/x-httpd-php")
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     header,
			Body:       io.NopCloser(strings.NewReader("<!doctype html><html><body>Article</body></html>")),
			Request:    request,
		}, nil
	})}

	result, err := DownloadHTMLPageContext(t.Context(), client, pageURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsHTML || !strings.Contains(result.HTML, "Article") {
		t.Fatalf("download result = %#v", result)
	}
}

func TestDownloadHTMLPageRejectsBinaryBehindPageExtension(t *testing.T) {
	pageURL, err := url.Parse("https://example.test/download.php")
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: htmlDownloadRoundTripper(func(request *http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("Content-Type", "text/html")
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     header,
			Body:       io.NopCloser(strings.NewReader("\xff\xd8\xff\xe0JFIF")),
			Request:    request,
		}, nil
	})}

	result, err := DownloadHTMLPageContext(t.Context(), client, pageURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsHTML {
		t.Fatalf("binary response was classified as HTML: %#v", result)
	}
}

func TestReadHTMLBodyWithLimitRejectsOversizedBody(t *testing.T) {
	if _, err := readHTMLBodyWithLimit(strings.NewReader("abcdef"), 5); err == nil {
		t.Fatal("oversized html body was allowed")
	}
	body, err := readHTMLBodyWithLimit(strings.NewReader("abcde"), 5)
	if err != nil {
		t.Fatalf("html body at limit was rejected: %v", err)
	}
	if string(body) != "abcde" {
		t.Fatalf("html body = %q", string(body))
	}
}
