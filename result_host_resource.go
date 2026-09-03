package main

import (
	"net/url"
	"strings"
)

// isResultHostResourceURL reports whether a resource already belongs
// to the imported site's destination domain. Such live internal links
// must not be replaced with a hashed mirrored asset path.
func (spider *pageSpider) isResultHostResourceURL(rawURL string) bool {
	if spider == nil {
		return false
	}
	resultHost := canonicalLocalDomain(spider.domain)
	if resultHost == "" {
		return false
	}
	linkedURL, err := url.Parse(rawURL)
	if err != nil || linkedURL == nil {
		return false
	}
	return strings.EqualFold(linkedURL.Hostname(), resultHost)
}
