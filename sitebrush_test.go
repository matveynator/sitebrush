package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type fakeGrabTransport struct {
	responses map[string]fakeGrabResponse
}

type fakeGrabResponse struct {
	contentType string
	body        string
}

type fakeHijackResponseWriter struct {
	header http.Header
	conn   *fakeHijackConn
}

type fakeHijackConn struct {
	bytes.Buffer
	closed bool
}

type fakeAddr string

func (writer *fakeHijackResponseWriter) Header() http.Header {
	return writer.header
}

func (writer *fakeHijackResponseWriter) Write(payload []byte) (int, error) {
	return writer.conn.Write(payload)
}

func (writer *fakeHijackResponseWriter) WriteHeader(int) {}

func (writer *fakeHijackResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return writer.conn, bufio.NewReadWriter(bufio.NewReader(writer.conn), bufio.NewWriter(writer.conn)), nil
}

func (conn *fakeHijackConn) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (conn *fakeHijackConn) Close() error {
	conn.closed = true
	return nil
}

func (conn *fakeHijackConn) LocalAddr() net.Addr {
	return fakeAddr("local")
}

func (conn *fakeHijackConn) RemoteAddr() net.Addr {
	return fakeAddr("remote")
}

func (conn *fakeHijackConn) SetDeadline(time.Time) error {
	return nil
}

func (conn *fakeHijackConn) SetReadDeadline(time.Time) error {
	return nil
}

func (conn *fakeHijackConn) SetWriteDeadline(time.Time) error {
	return nil
}

func (addr fakeAddr) Network() string {
	return string(addr)
}

func (addr fakeAddr) String() string {
	return string(addr)
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
	importedHTML := application.mirrorRemotePage("example.test", "/imported", pageRawURL, pageURL, sourceHTML, "", selectedResourceURLs)
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

func TestStatusCapturingResponseWriterSupportsWebSocketHijack(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/grab/ws?token=test", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

	baseWriter := &fakeHijackResponseWriter{header: make(http.Header), conn: &fakeHijackConn{}}
	wrappedWriter := &statusCapturingResponseWriter{ResponseWriter: baseWriter, statusCode: http.StatusOK}

	connection, err := upgradeToWebSocket(wrappedWriter, request)
	if err != nil {
		t.Fatalf("upgrade through logging writer failed: %v", err)
	}
	defer connection.Close()

	handshake := baseWriter.conn.String()
	if !strings.Contains(handshake, "HTTP/1.1 101 Switching Protocols") {
		t.Fatalf("handshake missing 101 response: %q", handshake)
	}
	if !strings.Contains(handshake, "Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=") {
		t.Fatalf("handshake missing websocket accept header: %q", handshake)
	}
}

func TestPublicImportedAssetsServeFromCanonicalAndDomainPrefixedPaths(t *testing.T) {
	storagePath := t.TempDir()
	rawDB, err := sql.Open("sqlite3", filepath.Join(storagePath, "sitebrush.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	application := &App{db: rawDB, storagePath: storagePath, grabTracker: newGrabProgressTracker()}
	if err := application.migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	canonicalDir := application.domainFilesDirForDomain("localhost")
	if err := os.MkdirAll(canonicalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonicalDir, "asset.png"), []byte("canonical"), 0o644); err != nil {
		t.Fatal(err)
	}

	canonicalRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/p/asset.png", nil)
	canonicalResponse := httptest.NewRecorder()
	application.servePublicAsset(canonicalResponse, canonicalRequest)
	if canonicalResponse.Code != http.StatusOK {
		t.Fatalf("canonical asset status = %d, want 200", canonicalResponse.Code)
	}
	if canonicalResponse.Body.String() != "canonical" {
		t.Fatalf("canonical asset body = %q", canonicalResponse.Body.String())
	}

	legacyDir := filepath.Join(canonicalDir, "p")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "legacy.png"), []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}

	legacyRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/localhost/p/legacy.png", nil)
	legacyResponse := httptest.NewRecorder()
	application.route(legacyResponse, legacyRequest)
	if legacyResponse.Code != http.StatusOK {
		t.Fatalf("legacy asset status = %d, want 200", legacyResponse.Code)
	}
	if legacyResponse.Body.String() != "legacy" {
		t.Fatalf("legacy asset body = %q", legacyResponse.Body.String())
	}
}

func TestFilesPageUsesPublicAssetPrefix(t *testing.T) {
	storagePath := t.TempDir()
	rawDB, err := sql.Open("sqlite3", filepath.Join(storagePath, "sitebrush.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	application := &App{db: rawDB, storagePath: storagePath, grabTracker: newGrabProgressTracker()}
	if err := application.migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	domainDir := application.domainFilesDirForDomain("localhost")
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(domainDir, "55a5.jpg"), []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/?files", nil)
	files, err := application.listManagedFiles(request.Context(), request, "/")
	if err != nil {
		t.Fatalf("list files: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("listed files = %#v, want one file", files)
	}
	files[0].AssetPath = "/p/" + files[0].Name
	if files[0].AssetPath != "/p/55a5.jpg" {
		t.Fatalf("asset path = %q, want /p/55a5.jpg", files[0].AssetPath)
	}
}

func TestManagedFilesVisibleForCurrentURIAndDescendants(t *testing.T) {
	storagePath := t.TempDir()
	rawDB, err := sql.Open("sqlite3", filepath.Join(storagePath, "sitebrush.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	application := &App{db: rawDB, storagePath: storagePath, grabTracker: newGrabProgressTracker()}
	if err := application.migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	domainDir := application.domainFilesDirForDomain("localhost")
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, fileName := range []string{"docs.png", "child.png", "other.png", "legacy.png"} {
		if err := os.WriteFile(filepath.Join(domainDir, fileName), []byte(fileName), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	application.upsertFileMetadata(context.Background(), "localhost", "docs.png", "/docs", 8, "image/png", "test")
	application.upsertFileMetadata(context.Background(), "localhost", "child.png", "/docs/child", 9, "image/png", "test")
	application.upsertFileMetadata(context.Background(), "localhost", "other.png", "/other", 9, "image/png", "test")

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs?files", nil)
	files, err := application.listManagedFiles(request.Context(), request, "/docs")
	if err != nil {
		t.Fatalf("list files: %v", err)
	}

	names := make(map[string]bool)
	for _, file := range files {
		names[file.Name] = true
	}
	if !names["docs.png"] || !names["child.png"] {
		t.Fatalf("expected docs branch files, got %#v", names)
	}
	if names["other.png"] || names["legacy.png"] {
		t.Fatalf("unexpected out-of-scope files, got %#v", names)
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
