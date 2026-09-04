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

func TestDownloadHTMLPageRetriesOnlyWhenClassifierAllows(t *testing.T) {
	pageURL, err := url.Parse("https://example.test/")
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	client := &http.Client{Transport: htmlDownloadRoundTripper(func(request *http.Request) (*http.Response, error) {
		attempts++
		statusCode := http.StatusServiceUnavailable
		body := "unavailable"
		if attempts == 3 {
			statusCode = http.StatusOK
			body = "<html><body>ready</body></html>"
		}
		return &http.Response{StatusCode: statusCode, Status: http.StatusText(statusCode), Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	result, err := DownloadHTMLPageWithRetriesContext(t.Context(), client, pageURL, nil, HTMLDownloadRetryOptions{
		Attempts: 4,
		ShouldRetry: func(result HTMLDownloadResult, downloadErr error) bool {
			return result.StatusCode >= http.StatusInternalServerError
		},
	})
	if err != nil || !result.IsHTML || attempts != 3 {
		t.Fatalf("transient retry result=%#v err=%v attempts=%d", result, err, attempts)
	}

	attempts = 0
	client.Transport = htmlDownloadRoundTripper(func(request *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Header: make(http.Header), Body: io.NopCloser(strings.NewReader("missing")), Request: request}, nil
	})
	_, _ = DownloadHTMLPageWithRetriesContext(t.Context(), client, pageURL, nil, HTMLDownloadRetryOptions{
		Attempts: 4,
		ShouldRetry: func(result HTMLDownloadResult, downloadErr error) bool {
			return result.StatusCode >= http.StatusInternalServerError
		},
	})
	if attempts != 1 {
		t.Fatalf("permanent response attempts=%d, want 1", attempts)
	}
}
