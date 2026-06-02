package crawler

import (
	"net/url"
	"path"
	"strings"
)

func CloneURL(sourceURL *url.URL) *url.URL {
	if sourceURL == nil {
		return nil
	}
	clonedURL := *sourceURL
	return &clonedURL
}

func WholeSitePageKey(pageURL *url.URL) string {
	if pageURL == nil || pageURL.Host == "" {
		return ""
	}
	return strings.ToLower(pageURL.Scheme) + "://" + strings.ToLower(pageURL.Host) + CanonicalWholeSitePagePath(pageURL.Path)
}

func CanonicalWholeSitePagePath(rawPath string) string {
	cleanedPath := CleanPath(rawPath)
	for _, indexName := range []string{"/index.html", "/index.htm", "/index.xhtml"} {
		if strings.HasSuffix(strings.ToLower(cleanedPath), indexName) {
			cleanedPath = cleanedPath[:len(cleanedPath)-len(indexName)]
			if cleanedPath == "" {
				return "/"
			}
			return CleanPath(cleanedPath)
		}
	}
	return cleanedPath
}

func WholeSiteLocalPath(basePath string, startURL, pageURL *url.URL) string {
	basePath = CleanPath(basePath)
	if WholeSitePageKey(startURL) == WholeSitePageKey(pageURL) {
		return basePath
	}
	sourcePath := CanonicalWholeSitePagePath(pageURL.Path)
	if sourcePath == "/" {
		return basePath
	}
	if basePath == "/" {
		return sourcePath
	}
	return CleanPath(strings.TrimRight(basePath, "/") + sourcePath)
}

func WholeSiteLocalLink(basePath string, startURL, pageURL *url.URL) string {
	localPath := WholeSiteLocalPath(basePath, startURL, pageURL)
	if pageURL != nil && strings.TrimSpace(pageURL.RawQuery) != "" {
		return localPath + "?" + pageURL.RawQuery
	}
	return localPath
}

func CurrentWholeSiteImportURL(pageQueue []WholeSitePageJob) string {
	if len(pageQueue) == 0 || pageQueue[0].URL == nil {
		return ""
	}
	return pageQueue[0].URL.String()
}

func PreviewResourceKind(tagName, attributeName, rawRef string) string {
	tag := strings.ToLower(strings.TrimSpace(tagName))
	attribute := strings.ToLower(strings.TrimSpace(attributeName))
	switch tag {
	case "script":
		return "script"
	case "link":
		return "style"
	case "img", "source":
		return "image"
	case "video", "audio":
		return tag
	case "iframe", "embed", "object":
		return "embedded"
	}
	if attribute == "poster" {
		return "image"
	}
	if resourceKind := ResourceKindFromURL(rawRef); resourceKind != "" {
		return resourceKind
	}
	return "file"
}

func CleanPath(rawPath string) string {
	trimmedPath := strings.TrimSpace(rawPath)
	if trimmedPath == "" {
		return "/"
	}
	if !strings.HasPrefix(trimmedPath, "/") {
		trimmedPath = "/" + trimmedPath
	}
	cleanedPath := path.Clean(trimmedPath)
	if cleanedPath == "." || cleanedPath == "" {
		return "/"
	}
	return cleanedPath
}
