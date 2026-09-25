package crawler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSecurityBoundaryNormalizeURLBlocksExecutableAndOpaqueSchemes(t *testing.T) {
	base, _ := url.Parse("https://example.com/docs/page.html")
	for _, raw := range []string{"", "#fragment", "javascript:alert(1)", "data:text/html,<script>alert(1)</script>", "blob:https://example.com/id", "mailto:attacker@example.net", "tel:+123", "file:///etc/passwd", "ftp://example.com/file", "${attacker}/file.js"} {
		if normalized, blocked := NormalizeURL(raw, base, ReferenceDocument); !blocked || normalized != "" {
			t.Fatalf("SECURITY: unsafe crawler reference %q normalized as %q blocked=%v", raw, normalized, blocked)
		}
	}
}

func TestSecurityBoundaryExtractPageLinksStaysOnOriginalHost(t *testing.T) {
	base, _ := url.Parse("https://example.com/docs/page.html")
	site, _ := url.Parse("https://example.com/")
	htmlSource := "<a href=\"/safe\">safe</a><a href=\"https://evil.example/steal\">evil</a><a href=\"//evil.example/protocol-relative\">evil2</a><a href=\"javascript:alert(1)\">js</a><form action=\"/submit\"></form><iframe src=\"https://evil.example/frame\"></iframe>"
	links := ExtractPageLinks(htmlSource, base, site)
	if len(links) != 2 {
		t.Fatalf("same-origin links = %#v, want only /safe and /submit", links)
	}
	for _, link := range links {
		if !SameHost(site, link) {
			t.Fatalf("SECURITY: cross-host link escaped crawler boundary: %s", link)
		}
	}
}

func TestResourceAttackPageLinkExtractionDeduplicatesAndStopsAtBound(t *testing.T) {
	base, _ := url.Parse("https://example.com/index.html")
	site, _ := url.Parse("https://example.com/")
	duplicateLinks := strings.Repeat(`<a href="/same.html">same</a>`, 1000)
	sourceHTML := duplicateLinks + `<a href="/one.html">one</a><a href="/two.html">two</a><a href="/three.html">three</a><a href="/four.html">four</a>`
	pageURLs, truncated := ExtractPageLinksWithLimit(sourceHTML, base, site, 3)
	if len(pageURLs) != 3 || !truncated {
		t.Fatalf("bounded page links = %d truncated=%t, want 3 and true", len(pageURLs), truncated)
	}
	if pageURLs[0].Path != "/same.html" || pageURLs[1].Path != "/one.html" || pageURLs[2].Path != "/two.html" {
		t.Fatalf("page links did not preserve first-seen order: %#v", pageURLs)
	}
	withoutOverflow := duplicateLinks + `<a href="/one.html">one</a><a href="/two.html">two</a>`
	pageURLs, truncated = ExtractPageLinksWithLimit(withoutOverflow, base, site, 3)
	if len(pageURLs) != 3 || truncated {
		t.Fatalf("exactly bounded page links = %d truncated=%t, want 3 and false", len(pageURLs), truncated)
	}
}

func TestResourceAttackPageLinkOffsetsResumeWithoutLosingLinks(t *testing.T) {
	base, _ := url.Parse("https://source.example/")
	var source strings.Builder
	for pageNumber := 0; pageNumber < 5; pageNumber++ {
		source.WriteString(`<a href="/page-`)
		source.WriteString(strconv.Itoa(pageNumber))
		source.WriteString(`">page</a>`)
	}
	firstPageBatch, nextOffset, hasRemainingPages := ExtractPageLinksFromOffset(source.String(), base, base, 0, 2)
	if len(firstPageBatch) != 2 || nextOffset != 2 || !hasRemainingPages {
		t.Fatalf("first page-link batch = %d links, offset=%d remaining=%v", len(firstPageBatch), nextOffset, hasRemainingPages)
	}
	secondPageBatch, nextOffset, hasRemainingPages := ExtractPageLinksFromOffset(source.String(), base, base, nextOffset, 2)
	if len(secondPageBatch) != 2 || nextOffset != 4 || !hasRemainingPages {
		t.Fatalf("second page-link batch = %d links, offset=%d remaining=%v", len(secondPageBatch), nextOffset, hasRemainingPages)
	}
	lastPageBatch, nextOffset, hasRemainingPages := ExtractPageLinksFromOffset(source.String(), base, base, nextOffset, 2)
	if len(lastPageBatch) != 1 || nextOffset != 5 || hasRemainingPages {
		t.Fatalf("last page-link batch = %d links, offset=%d remaining=%v", len(lastPageBatch), nextOffset, hasRemainingPages)
	}
	if firstPageBatch[0].Path == secondPageBatch[0].Path || secondPageBatch[1].Path == lastPageBatch[0].Path {
		t.Fatal("link continuation repeated or skipped a page")
	}
}

func TestSecurityBoundaryHTMLBodyLimitRejectsOversizedResponse(t *testing.T) {
	payload := strings.Repeat("x", 65)
	if _, err := readHTMLBodyWithLimit(strings.NewReader(payload), 64); err == nil {
		t.Fatal("SECURITY: oversized HTML response exceeded configured crawler limit")
	}
	body, err := readHTMLBodyWithLimit(strings.NewReader(payload), 0)
	if err != nil || string(body) != payload {
		t.Fatalf("unlimited read = %d bytes, %v", len(body), err)
	}
}

type securityRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip securityRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestSecurityBoundaryCrawlerRejectsPrivateTargetsBeforeTransport(t *testing.T) {
	called := false
	client := NewSessionClient(time.Second, securityRoundTripper(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("transport should not run")
	}))
	for _, raw := range []string{"http://127.0.0.1/admin", "http://10.0.0.1/private", "http://[::1]/"} {
		target, _ := url.Parse(raw)
		if _, err := DownloadHTMLPageContext(context.Background(), client, target, nil); err == nil {
			t.Fatalf("SECURITY: crawler accepted private target %s", raw)
		}
	}
	if called {
		t.Fatal("SECURITY: transport was reached for private crawler target")
	}
}

func TestSSRFAttackCrawlerClientStopsPublicToLoopbackRedirect(t *testing.T) {
	transportCalls := 0
	client := NewSessionClient(time.Second, securityRoundTripper(func(request *http.Request) (*http.Response, error) {
		transportCalls++
		if transportCalls == 1 {
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"http://127.0.0.1/private"}},
				Body:       io.NopCloser(strings.NewReader("redirect")),
				Request:    request,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/html"}},
			Body:       io.NopCloser(strings.NewReader("<html>private data</html>")),
			Request:    request,
		}, nil
	}))
	publicURL, _ := url.Parse("https://public.example/import")
	if _, err := DownloadHTMLPageContext(context.Background(), client, publicURL, nil); err == nil {
		t.Fatal("SECURITY: crawler followed a public redirect to loopback")
	}
	if transportCalls != 1 {
		t.Fatalf("SECURITY: crawler transport received %d requests, want only the initial public request", transportCalls)
	}
}

func TestCrawlerRetryCancellationAndNoRetryBranches(t *testing.T) {
	publicURL, _ := url.Parse("https://example.com/page")
	client := NewSessionClient(time.Second, securityRoundTripper(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network down")
	}))
	attempts := 0
	retries := 0
	_, err := DownloadHTMLPageWithRetriesContext(context.Background(), client, publicURL, nil, HTMLDownloadRetryOptions{
		Attempts:    3,
		Delay:       -time.Second,
		OnAttempt:   func(_, _ int, _ *url.URL) { attempts++ },
		OnRetry:     func(_, _ int, _ *url.URL, _ error, _ time.Duration) { retries++ },
		ShouldRetry: func(HTMLDownloadResult, error) bool { return false },
	})
	if err == nil || attempts != 1 || retries != 0 {
		t.Fatalf("retry stop = attempts %d retries %d err %v", attempts, retries, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = DownloadHTMLPageWithRetriesContext(ctx, client, publicURL, nil, HTMLDownloadRetryOptions{Attempts: 2, Delay: time.Second})
	if err == nil {
		t.Fatal("cancelled crawler retry unexpectedly succeeded")
	}
}
