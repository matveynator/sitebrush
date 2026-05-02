package main

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeGrabTransport struct {
	responses map[string]fakeGrabResponse
}

type fakeGrabResponse struct {
	contentType string
	body        string
}

func (transport fakeGrabTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, found := transport.responses[request.URL.String()]
	if !found {
		return &http.Response{
			StatusCode:    http.StatusNotFound,
			Status:        "404 Not Found",
			Body:          io.NopCloser(strings.NewReader("not found")),
			ContentLength: int64(len("not found")),
			Header:        make(http.Header),
			Request:       request,
		}, nil
	}
	header := make(http.Header)
	header.Set("Content-Type", response.contentType)
	return &http.Response{
		StatusCode:    http.StatusOK,
		Status:        "200 OK",
		Body:          io.NopCloser(strings.NewReader(response.body)),
		ContentLength: int64(len(response.body)),
		Header:        header,
		Request:       request,
	}, nil
}

func TestMirrorRemotePageImportsNestedExternalResources(t *testing.T) {
	assetBaseURL := "https://assets.example"
	pageRawURL := "https://page.example/page"
	sourceHTML := `<!doctype html><html><head>` +
		`<link rel="stylesheet" href="` + assetBaseURL + `/style.css">` +
		`<script type="module" src="` + assetBaseURL + `/app.js"></script>` +
		`</head><body>Imported</body></html>`

	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			assetBaseURL + "/style.css":        {contentType: "text/css", body: `@import url("/nested.css"); body{background:url("/image.png")} @font-face{src:url("/font.eot?v=1#iefix")}`},
			assetBaseURL + "/nested.css":       {contentType: "text/css", body: `.nested{background:url("/nested-image.png")}`},
			assetBaseURL + "/app.js":           {contentType: "application/javascript", body: `import "/module.js"; console.log("app");`},
			assetBaseURL + "/module.js":        {contentType: "application/javascript", body: `import "/deep.js"; export const moduleValue = 1;`},
			assetBaseURL + "/deep.js":          {contentType: "application/javascript", body: `export const deepValue = 1;`},
			assetBaseURL + "/font.eot?v=1":     {contentType: "application/vnd.ms-fontobject", body: "font"},
			assetBaseURL + "/image.png":        {contentType: "image/png", body: "png"},
			assetBaseURL + "/nested-image.png": {contentType: "image/png", body: "nested-png"},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	pageURL, parseErr := url.Parse(pageRawURL)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	previewResources := previewGrabResources(pageURL, sourceHTML)
	if len(previewResources) != 8 {
		t.Fatalf("expected 8 preview resources, got %d: %#v", len(previewResources), previewResources)
	}

	selectedResourceURLs := make(map[string]struct{})
	for _, previewResource := range previewResources {
		selectedResourceURLs[previewResource.URL] = struct{}{}
	}
	application := &App{storagePath: t.TempDir(), grabTracker: newGrabProgressTracker()}
	importedHTML := application.mirrorRemotePage("example.test", pageRawURL, pageURL, sourceHTML, "", selectedResourceURLs)
	if strings.Contains(importedHTML, assetBaseURL) {
		t.Fatalf("imported HTML still references external asset host: %s", importedHTML)
	}
	if !strings.Contains(importedHTML, "/p/") {
		t.Fatalf("imported HTML does not reference local public assets: %s", importedHTML)
	}

	storedFiles, listErr := listStoredFiles(application.domainFilesDirForDomain("example.test"))
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(storedFiles) != len(previewResources) {
		t.Fatalf("expected %d stored files, got %d: %#v", len(previewResources), len(storedFiles), storedFiles)
	}
	storedFontWithCleanExtension := false
	for _, storedFilePath := range storedFiles {
		if filepath.Ext(storedFilePath) == ".eot" {
			storedFontWithCleanExtension = true
		}
		if filepath.Ext(storedFilePath) != ".css" && filepath.Ext(storedFilePath) != ".js" {
			continue
		}
		storedContent, readErr := os.ReadFile(storedFilePath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.Contains(string(storedContent), assetBaseURL) {
			t.Fatalf("stored nested resource still references external host in %s: %s", storedFilePath, string(storedContent))
		}
	}
	if !storedFontWithCleanExtension {
		t.Fatalf("expected font resource to be stored with clean .eot extension: %#v", storedFiles)
	}
}

func TestParseGrabSourceURLAcceptsCommonURLForms(t *testing.T) {
	testCases := map[string]string{
		"https://example.com/path":   "https://example.com/path",
		"http://example.com/path":    "http://example.com/path",
		"sitebrush.com":              "https://sitebrush.com",
		"example.com/path":           "https://example.com/path",
		"127.0.0.1:8080/admin":       "http://127.0.0.1:8080/admin",
		"localhost:18080/admin":      "http://localhost:18080/admin",
		"//cdn.example.com/file.css": "https://cdn.example.com/file.css",
	}
	for rawSourceURL, expectedSourceURL := range testCases {
		parsedSourceURL, parseErr := parseGrabSourceURL(rawSourceURL)
		if parseErr != nil {
			t.Fatalf("parseGrabSourceURL(%q) failed: %v", rawSourceURL, parseErr)
		}
		if parsedSourceURL.String() != expectedSourceURL {
			t.Fatalf("parseGrabSourceURL(%q) = %q, want %q", rawSourceURL, parsedSourceURL.String(), expectedSourceURL)
		}
	}
}

func listStoredFiles(rootPath string) ([]string, error) {
	storedFiles := make([]string, 0)
	walkErr := filepath.WalkDir(rootPath, func(currentPath string, currentEntry os.DirEntry, walkErr error) error {
		if walkErr != nil || currentEntry.IsDir() {
			return walkErr
		}
		storedFiles = append(storedFiles, currentPath)
		return nil
	})
	return storedFiles, walkErr
}
