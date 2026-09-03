package main

import "net/url"

// isResultHostResourceURL identifies resources that already belong to
// the destination site. Re-importing those resources would replace a
// live internal URL with a hashed mirrored asset.
func isResultHostResourceURL(rawURL string, resultURL *url.URL) bool {
	if resultURL == nil {
		return false
	}
	linkedURL, err := url.Parse(rawURL)
	if err != nil || linkedURL == nil {
		return false
	}
	return sameHostname(linkedURL.Hostname(), resultURL.Hostname())
}
