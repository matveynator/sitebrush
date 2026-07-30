package crawler

import (
	"context"
	"net/url"
)

// SourceOptions carry transport-level source overrides shared by previews and imports.
type SourceOptions struct {
	IP           string
	LanguageCode string
}

// ImportRequest is the application-facing input for single page and whole site imports.
type ImportRequest struct {
	Domain               string
	PagePath             string
	SourceURL            string
	RemoteSourceURL      *url.URL
	HTML                 string
	Context              context.Context
	ProgressToken        string
	DownloadTotal        int
	DownloadTotalBytes   int64
	SelectedResourceURLs map[string]struct{}
	SourceOptions        SourceOptions
}

// ImportResult is returned after the importer has persisted everything it could fetch.
type ImportResult struct {
	RedirectPath  string
	FailedTotal   int
	FailedURLs    []string
	FailedReasons map[string]string
}

// ResourcePreview describes one downloadable remote asset before import confirmation.
type ResourcePreview struct {
	URL       string `json:"url"`
	Kind      string `json:"kind"`
	SizeBytes int64  `json:"size_bytes"`
}

// ImportedPage is a crawled remote document mapped to a local SiteBrush page.
type ImportedPage struct {
	SourceURL string
	LocalPath string
	HTML      string
}

// WholeSitePageJob is the queue entry used while crawling a whole remote site.
type WholeSitePageJob struct {
	URL  *url.URL
	HTML string
}
