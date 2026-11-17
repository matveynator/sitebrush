package publicsite

import "net/http"

// Branding follows the admitted site's policy. Unknown policy must not reveal an installation.
func ApplyServerHeader(header http.Header, decision Decision) {
	header.Del("Server")
	if decision.Found && !decision.Stealth {
		header.Set("Server", "SiteBrush")
	}
}
