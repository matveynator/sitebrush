package httpsecurity

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIndexingCrawlerRequestIsNarrowAndReadOnly(t *testing.T) {
	for _, agent := range []string{
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
		"Mozilla/5.0 AppleWebKit/537.36; compatible; GPTBot/1.2",
		"CCBot/2.0 (https://commoncrawl.org/faq/)",
		"ClaudeBot/1.0",
		"PerplexityBot/1.0",
	} {
		request := httptest.NewRequest(http.MethodGet, "https://example.com/docs/", nil)
		request.Header.Set("User-Agent", agent)
		if !IsIndexingCrawlerRequest(request) {
			t.Fatalf("crawler not recognized: %q", agent)
		}
	}

	for _, agent := range []string{"curl/8.0", "Go-http-client/1.1", "generic-crawler", "sqlmap/1.8"} {
		request := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
		request.Header.Set("User-Agent", agent)
		if IsIndexingCrawlerRequest(request) {
			t.Fatalf("generic automation received crawler exemption: %q", agent)
		}
	}

	post := httptest.NewRequest(http.MethodPost, "https://example.com/", nil)
	post.Header.Set("User-Agent", "Googlebot/2.1")
	if IsIndexingCrawlerRequest(post) {
		t.Fatal("write request received crawler exemption")
	}
}
