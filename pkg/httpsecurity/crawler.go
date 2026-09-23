package httpsecurity

import (
	"net/http"
	"strings"
)

// IsIndexingCrawlerRequest recognizes read-only requests from well-known search,
// AI, and public-web indexing crawlers. User-Agent is only a crawl-rate hint:
// callers must still run concrete exploit and sensitive-path detection.
func IsIndexingCrawlerRequest(request *http.Request) bool {
	if request == nil {
		return false
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		return false
	}
	return IsIndexingCrawlerAgent(request.UserAgent())
}

// IsIndexingCrawlerAgent intentionally uses a narrow allowlist of published
// crawler names instead of generic "bot" or "crawler" substrings.
func IsIndexingCrawlerAgent(agent string) bool {
	lowered := strings.ToLower(strings.TrimSpace(agent))
	if lowered == "" {
		return false
	}
	for _, token := range []string{
		"googlebot",
		"google-extended",
		"bingbot",
		"yandexbot",
		"duckduckbot",
		"baiduspider",
		"applebot",
		"petalbot",
		"amazonbot",
		"gptbot",
		"oai-searchbot",
		"chatgpt-user",
		"claudebot",
		"claude-user",
		"anthropic-ai",
		"perplexitybot",
		"cohere-ai",
		"meta-externalagent",
		"facebookbot",
		"bytespider",
		"ccbot",
	} {
		if strings.Contains(lowered, token) {
			return true
		}
	}
	return false
}
