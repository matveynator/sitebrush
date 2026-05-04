package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
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

func newTestApplication(t *testing.T) (*App, *sql.DB) {
	t.Helper()
	storagePath := t.TempDir()
	rawDB, err := sql.Open("sqlite3", filepath.Join(storagePath, "sitebrush.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = rawDB.Close()
	})
	application := &App{db: rawDB, storagePath: storagePath, grabTracker: newGrabProgressTracker()}
	if err := application.migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return application, rawDB
}

func newAdminSessionCookie(t *testing.T, application *App, email string) *http.Cookie {
	t.Helper()
	sessionRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/", nil)
	sessionResponse := httptest.NewRecorder()
	application.createSession(sessionResponse, sessionRequest, email)
	sessionCookies := sessionResponse.Result().Cookies()
	if len(sessionCookies) == 0 {
		t.Fatal("createSession did not set a cookie")
	}
	return sessionCookies[0]
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

func TestContextMenuUsesDirectEditorProfileAndDeleteActions(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/docs", "/docs", "<html><body>docs</body></html>")
	if err != nil {
		t.Fatalf("insert page: %v", err)
	}
	result, err := rawDB.Exec(`INSERT INTO revisions(domain,page_path,html,created_at,is_active) VALUES(?,?,?,?,1)`, "localhost", "/docs", "<html><body>docs</body></html>", time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("insert revision: %v", err)
	}
	revisionID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("revision id: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs", nil)
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expectedFragment := range []string{"href='?visual'", "href='?text'", "data-sitebrush-action='delete'", "?delete=" + strconv.FormatInt(revisionID, 10), "href='?profile'"} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("context menu missing %q in %s", expectedFragment, body)
		}
	}
	if strings.Contains(body, "href='?edit'") {
		t.Fatalf("context menu still contains intermediate edit link: %s", body)
	}
	for _, expectedFragment := range []string{`window.location.href = targetHref;`, `closest("#SiteBrushMenuBox")`} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("context menu missing navigation guard %q in %s", expectedFragment, body)
		}
	}
}

func TestDeleteRevisionByQueryDisablesRevisionAndAppliesPreviousActiveRevision(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/docs", "/docs", "new")
	if err != nil {
		t.Fatalf("insert page: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO revisions(domain,page_path,html,created_at,is_active) VALUES(?,?,?,?,1)`, "localhost", "/docs", "old", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("insert old revision: %v", err)
	}
	result, err := rawDB.Exec(`INSERT INTO revisions(domain,page_path,html,created_at,is_active) VALUES(?,?,?,?,1)`, "localhost", "/docs", "new", "2026-01-02T00:00:00Z")
	if err != nil {
		t.Fatalf("insert new revision: %v", err)
	}
	revisionID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("revision id: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?delete="+strconv.FormatInt(revisionID, 10), nil)
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}

	var isActive int
	if err := rawDB.QueryRow(`SELECT is_active FROM revisions WHERE id=?`, revisionID).Scan(&isActive); err != nil {
		t.Fatalf("read revision: %v", err)
	}
	if isActive != 0 {
		t.Fatalf("revision is_active = %d, want 0", isActive)
	}
	var pageHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/docs").Scan(&pageHTML); err != nil {
		t.Fatalf("read page: %v", err)
	}
	if pageHTML != "old" {
		t.Fatalf("page html = %q, want old", pageHTML)
	}
}

func TestProfilePageUpdatesAdminEmailAndPassword(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	form := url.Values{}
	form.Set("email", "new@example.com")
	form.Set("password", "new-secret")
	form.Set("password_confirm", "new-secret")
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?profile", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}

	var password string
	if err := rawDB.QueryRow(`SELECT password FROM users WHERE domain=? AND email=?`, "localhost", "new@example.com").Scan(&password); err != nil {
		t.Fatalf("read updated user: %v", err)
	}
	if password != "new-secret" {
		t.Fatalf("password = %q, want new-secret", password)
	}
	profileCookies := response.Result().Cookies()
	if len(profileCookies) == 0 {
		t.Fatal("profile update did not refresh the session cookie")
	}
	authenticatedRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/", nil)
	authenticatedRequest.AddCookie(profileCookies[0])
	if !application.isAdminRequest(authenticatedRequest) {
		t.Fatal("refreshed profile session is not authenticated")
	}
}

func TestMirrorRemotePageImportsNestedExternalResources(t *testing.T) {
	assetBaseURL := "https://assets.example"
	pageRawURL := "https://page.example/page"
	sourceHTML := `<!doctype html><html><head>` +
		`<link rel="stylesheet" href="` + assetBaseURL + `/style.css">` +
		`<script type="module" src="` + assetBaseURL + `/app.js"></script>` +
		`<script>const assetURL = "${l}"; const assetPath = "+e.url+"; const dynamicValue = this.videoId;</script>` +
		`<iframe src="https://www.youtube.com/embed/demo"></iframe>` +
		`</head><body>Imported</body></html>`

	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			assetBaseURL + "/style.css":        {contentType: "text/css", body: `@import url("/nested.css"); body{background:url("/image.png")} @font-face{src:url("/font.eot?v=1#iefix")}`},
			assetBaseURL + "/nested.css":       {contentType: "text/css", body: `.nested{background:url("/nested-image.png")}`},
			assetBaseURL + "/app.js":           {contentType: "application/javascript", body: `import "/module.js"; console.log("app");`},
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
	previewResources := previewGrabResources(pageURL, sourceHTML, "")
	if len(previewResources) != 6 {
		t.Fatalf("expected 6 preview resources, got %d: %#v", len(previewResources), previewResources)
	}

	selectedResourceURLs := make(map[string]struct{})
	for _, previewResource := range previewResources {
		selectedResourceURLs[previewResource.URL] = struct{}{}
	}
	application := &App{storagePath: t.TempDir(), grabTracker: newGrabProgressTracker()}
	importedHTML := application.mirrorRemotePage("example.test", "/imported", pageRawURL, pageURL, sourceHTML, "", selectedResourceURLs, "")
	if strings.Contains(importedHTML, assetBaseURL) {
		t.Fatalf("imported HTML still references external asset host: %s", importedHTML)
	}
	if !strings.Contains(importedHTML, "youtube.com/embed/demo") {
		t.Fatalf("imported HTML lost external iframe reference: %s", importedHTML)
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
	for _, forbiddenFragment := range []string{"/module.js", "/deep.js", "youtube.com/embed/demo"} {
		if strings.Contains(importedHTML, forbiddenFragment) && forbiddenFragment != "youtube.com/embed/demo" {
			t.Fatalf("imported HTML still contains forbidden fragment %q: %s", forbiddenFragment, importedHTML)
		}
	}
}

func TestNormalizeURLRejectsSuspiciousDynamicReferences(t *testing.T) {
	pageURL, parseErr := url.Parse("https://example.com/page")
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	spider := newPageSpider("", pageURL, grabResourceMaxDepth, nil, "", "")
	testCases := []string{
		"${l}",
		"+e.url+",
		"this.videoId",
		"videoId",
		"/assets/app.js?x=1+2",
	}
	for _, rawRef := range testCases {
		if normalizedURL, blocked := spider.normalizeURL(rawRef, pageURL); !blocked || normalizedURL != "" {
			t.Fatalf("normalizeURL(%q) = (%q, %v), want blocked", rawRef, normalizedURL, blocked)
		}
	}
}

func TestMirrorRemotePageBlanksSameOriginEmbeddedHTMLFrames(t *testing.T) {
	pageRawURL := "https://example.test/imported"
	sourceHTML := `<!doctype html><html><body><iframe src="/imported"></iframe><iframe src="https://www.youtube.com/embed/demo"></iframe></body></html>`

	pageURL, parseErr := url.Parse(pageRawURL)
	if parseErr != nil {
		t.Fatal(parseErr)
	}

	application := &App{storagePath: t.TempDir(), grabTracker: newGrabProgressTracker()}
	importedHTML := application.mirrorRemotePage("example.test", "/imported", pageRawURL, pageURL, sourceHTML, "", nil, "")

	if strings.Contains(importedHTML, `src="https://example.test/imported"`) || strings.Contains(importedHTML, `src="/imported"`) {
		t.Fatalf("imported HTML still contains recursive same-origin iframe: %s", importedHTML)
	}
	if !strings.Contains(importedHTML, `src="about:blank"`) {
		t.Fatalf("imported HTML did not blank recursive iframe: %s", importedHTML)
	}
	if !strings.Contains(importedHTML, "youtube.com/embed/demo") {
		t.Fatalf("imported HTML lost allowed external embed: %s", importedHTML)
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

func TestParseGrabSourceURLUsesHTTPDefaultWhenServerIPIsProvided(t *testing.T) {
	parsedSourceURL, parseErr := parseGrabSourceURLForServerIP("expired.example/page", "127.0.0.1")
	if parseErr != nil {
		t.Fatalf("parseGrabSourceURLForServerIP failed: %v", parseErr)
	}
	if parsedSourceURL.String() != "http://expired.example/page" {
		t.Fatalf("parsed URL = %q, want http://expired.example/page", parsedSourceURL.String())
	}
}

func TestDownloadGrabSourceHTMLCanDialSourceIPWithDomainHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.Host, "expired.example:") {
			t.Fatalf("Host = %q, want expired.example with server port", request.Host)
		}
		_, _ = response.Write([]byte("<html>expired domain copy</html>"))
	}))
	defer server.Close()

	serverURL, parseErr := url.Parse(server.URL)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	_, serverPort, splitErr := net.SplitHostPort(serverURL.Host)
	if splitErr != nil {
		t.Fatal(splitErr)
	}
	sourceURL := "http://expired.example:" + serverPort + "/page"
	htmlBytes, downloadErr := downloadGrabSourceHTML(sourceURL, "127.0.0.1")
	if downloadErr != nil {
		t.Fatalf("download with source IP failed: %v", downloadErr)
	}
	if string(htmlBytes) != "<html>expired domain copy</html>" {
		t.Fatalf("downloaded HTML = %q", string(htmlBytes))
	}
}

func TestMissingPageGrabFormIncludesSourceIPOverride(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/missing", nil)
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("missing page status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `name="source_ip"`) {
		t.Fatalf("missing page import form does not include source_ip field: %s", body)
	}
}

func TestStatusCapturingResponseWriterSupportsWebSocketHijack(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/?grab_ws&token=test", nil)
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

func TestStatusCapturingResponseWriterSupportsFlush(t *testing.T) {
	baseWriter := httptest.NewRecorder()
	wrappedWriter := &statusCapturingResponseWriter{ResponseWriter: baseWriter, statusCode: http.StatusOK}
	flusher, ok := any(wrappedWriter).(http.Flusher)
	if !ok {
		t.Fatal("logging response writer does not expose http.Flusher")
	}

	flusher.Flush()
	if !baseWriter.Flushed {
		t.Fatal("flush was not forwarded to the wrapped response writer")
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

func TestFilesPageDoesNotAutoLoadImageAssets(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	domainDir := application.domainFilesDirForDomain("localhost")
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(domainDir, "imported.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	application.upsertFileMetadata(context.Background(), "localhost", "imported.png", "/docs", 3, "image/png", "import")

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs?files", nil)
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("files page status = %d, body=%q", response.Code, response.Body.String())
	}

	body := response.Body.String()
	if strings.Contains(body, `<img class="file-thumb" src="/p/imported.png"`) {
		t.Fatalf("files page still auto-loads imported image asset: %s", body)
	}
	for _, expectedFragment := range []string{`class="file-preview-trigger"`, `data-preview-src="/p/imported.png"`} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("files page missing %q in %s", expectedFragment, body)
		}
	}
}

func TestAssetServingCountsDownloadsAndTokenUse(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(domainDir, "token.png"), []byte("token image"), 0o644); err != nil {
		t.Fatal(err)
	}
	application.upsertFileMetadata(context.Background(), "localhost", "token.png", "/docs", 11, "image/png", "test")
	_, err = rawDB.Exec(`INSERT INTO file_access_rules(domain,file_name,access_mode,token,expires_at,single_use_left,token_use_count) VALUES(?,?,?,?,?,?,?)`, "localhost", "token.png", "token", "abc", "", 0, 0)
	if err != nil {
		t.Fatalf("insert token rule: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/p/token.png?token=abc", nil)
	response := httptest.NewRecorder()
	application.servePublicAsset(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("token asset status = %d, want 200", response.Code)
	}

	var downloadCount int64
	if err := rawDB.QueryRow(`SELECT download_count FROM file_metadata WHERE domain=? AND file_name=?`, "localhost", "token.png").Scan(&downloadCount); err != nil {
		t.Fatalf("read download count: %v", err)
	}
	if downloadCount != 1 {
		t.Fatalf("download count = %d, want 1", downloadCount)
	}

	var tokenUseCount int64
	if err := rawDB.QueryRow(`SELECT token_use_count FROM file_access_rules WHERE domain=? AND file_name=?`, "localhost", "token.png").Scan(&tokenUseCount); err != nil {
		t.Fatalf("read token count: %v", err)
	}
	if tokenUseCount != 1 {
		t.Fatalf("token use count = %d, want 1", tokenUseCount)
	}
}

func TestUploadFilesStoresFilesForCurrentURI(t *testing.T) {
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

	var body bytes.Buffer
	multipartWriter := multipart.NewWriter(&body)
	fileWriter, err := multipartWriter.CreateFormFile("upload_files", "manual.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fileWriter.Write([]byte("manual upload")); err != nil {
		t.Fatal(err)
	}
	if err := multipartWriter.WriteField("action", "upload"); err != nil {
		t.Fatal(err)
	}
	if err := multipartWriter.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?files", &body)
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	request.Header.Set("Accept", "application/json")
	response := httptest.NewRecorder()

	application.uploadFiles(response, request, "/docs")
	if response.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body=%q", response.Code, response.Body.String())
	}

	storedPath := filepath.Join(application.domainFilesDirForDomain("localhost"), "manual.txt")
	storedBytes, err := os.ReadFile(storedPath)
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if string(storedBytes) != "manual upload" {
		t.Fatalf("stored file = %q", string(storedBytes))
	}

	var pagePath string
	if err := rawDB.QueryRow(`SELECT page_path FROM file_metadata WHERE domain=? AND file_name=?`, "localhost", "manual.txt").Scan(&pagePath); err != nil {
		t.Fatalf("read upload metadata: %v", err)
	}
	if pagePath != "/docs" {
		t.Fatalf("uploaded page path = %q, want /docs", pagePath)
	}
	if !strings.Contains(response.Body.String(), "manual.txt") {
		t.Fatalf("upload response does not include filename: %q", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "/p/manual.txt") {
		t.Fatalf("upload response does not include public path: %q", response.Body.String())
	}
}

func TestVisualEditorUsesLocalJoditAssetsAndServerImageUpload(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/docs", "/docs", "<p>docs</p>")
	if err != nil {
		t.Fatalf("insert page: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs?visual", nil)
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("visual editor status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expectedFragment := range []string{`href="/p/static/jodit.min.css"`, `src="/p/static/jodit.min.js"`, "/p/static/files.png", "/p/static/save-page.png", "/p/static/exit-editor.png", "chooseAndUploadFiles", "document.body.appendChild(fileInputElement)", "fallbackHashFileName", "sitebrush.visualUploadResizeMode", `value="600"`, `value="800"`, `value="1200"`, `value="2000"`, "width=", "height=", "currentPagePath + '?files'", "currentPagePath + '?native_pick_files'", "window.location.href = currentPagePath"} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("visual editor missing %q in %s", expectedFragment, body)
		}
	}
	if strings.Contains(body, "cdn.jsdelivr.net") {
		t.Fatalf("visual editor still references CDN: %s", body)
	}
}

func TestNormalizeDomainNameAcceptsBareDomainsAndRejectsInvalidNames(t *testing.T) {
	testCases := map[string]string{
		"sitebrush.com":                  "sitebrush.com",
		"https://www.sitebrush.com/path": "www.sitebrush.com",
		"www.sitebrush.com:443":          "www.sitebrush.com",
		" localhost ":                    "",
		"127.0.0.1":                      "",
		"bad domain.com":                 "",
	}
	for rawDomain, expectedDomain := range testCases {
		actualDomain := normalizeDomainName(rawDomain)
		if actualDomain != expectedDomain {
			t.Fatalf("normalizeDomainName(%q) = %q, want %q", rawDomain, actualDomain, expectedDomain)
		}
	}
}

func TestDomainFromRequestCanonicalizesLoopbackHosts(t *testing.T) {
	testCases := map[string]string{
		"localhost:8080": "localhost",
		"127.0.0.1:8080": "localhost",
		"[::1]:8080":     "localhost",
		"EXAMPLE.com:80": "example.com",
	}
	for hostHeader, expectedDomain := range testCases {
		request := httptest.NewRequest(http.MethodGet, "http://"+hostHeader+"/", nil)
		actualDomain := domainFromRequest(request)
		if actualDomain != expectedDomain {
			t.Fatalf("domainFromRequest(%q) = %q, want %q", hostHeader, actualDomain, expectedDomain)
		}
	}
}

func TestMigrateMergesLegacyLoopbackDomainsIntoLocalhost(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "127.0.0.1", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert loopback user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "127.0.0.1", "/docs", "/docs", "<p>docs</p>")
	if err != nil {
		t.Fatalf("insert loopback page: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO domain_states(domain,is_frozen) VALUES(?,?)`, "127.0.0.1", 1)
	if err != nil {
		t.Fatalf("insert loopback domain state: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO domain_aliases(primary_domain,alias_domain,verification_token,is_verified,dns_a_ok,is_selected,last_checked_at) VALUES(?,?,?,?,?,?,?)`,
		"127.0.0.1", "example.com", "token", 1, 1, 0, "")
	if err != nil {
		t.Fatalf("insert loopback alias: %v", err)
	}

	if err := application.migrate(context.Background()); err != nil {
		t.Fatalf("re-run migrate: %v", err)
	}

	var migratedUsers int
	if err := rawDB.QueryRow(`SELECT COUNT(1) FROM users WHERE domain='localhost' AND email='admin@example.com'`).Scan(&migratedUsers); err != nil {
		t.Fatalf("count migrated users: %v", err)
	}
	if migratedUsers != 1 {
		t.Fatalf("migrated users = %d, want 1", migratedUsers)
	}
	var legacyUsers int
	if err := rawDB.QueryRow(`SELECT COUNT(1) FROM users WHERE domain='127.0.0.1' AND email='admin@example.com'`).Scan(&legacyUsers); err != nil {
		t.Fatalf("count legacy users: %v", err)
	}
	if legacyUsers != 0 {
		t.Fatalf("legacy users = %d, want 0", legacyUsers)
	}

	var pageDomain string
	if err := rawDB.QueryRow(`SELECT domain FROM pages WHERE path='/docs'`).Scan(&pageDomain); err != nil {
		t.Fatalf("select migrated page: %v", err)
	}
	if pageDomain != "localhost" {
		t.Fatalf("migrated page domain = %q, want localhost", pageDomain)
	}

	var aliasPrimaryDomain string
	if err := rawDB.QueryRow(`SELECT primary_domain FROM domain_aliases WHERE alias_domain='example.com'`).Scan(&aliasPrimaryDomain); err != nil {
		t.Fatalf("select migrated alias: %v", err)
	}
	if aliasPrimaryDomain != "localhost" {
		t.Fatalf("migrated alias primary_domain = %q, want localhost", aliasPrimaryDomain)
	}
}

func TestDomainAliasesRequireDNSVerificationBeforeResolving(t *testing.T) {
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

	previousTXTLookup := lookupTXTRecords
	previousIPLookup := lookupIPRecords
	defer func() {
		lookupTXTRecords = previousTXTLookup
		lookupIPRecords = previousIPLookup
	}()

	lookupTXTRecords = func(string) ([]string, error) {
		return nil, os.ErrNotExist
	}
	lookupIPRecords = func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.10")}, nil
	}

	addForm := url.Values{}
	addForm.Set("action", "add_alias")
	addForm.Set("alias_domain", "example.com")
	addRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?settings", strings.NewReader(addForm.Encode()))
	addRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	application.handleDomainSettingsPost(addRequest.Context(), addRequest, "localhost", "203.0.113.10")

	inactiveRequest := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	if domain := application.siteDomain(inactiveRequest.Context(), inactiveRequest); domain != "example.com" {
		t.Fatalf("inactive alias resolved to %q, want request domain", domain)
	}

	var verificationToken string
	if err := rawDB.QueryRow(`SELECT verification_token FROM domain_aliases WHERE primary_domain=? AND alias_domain=?`, "localhost", "example.com").Scan(&verificationToken); err != nil {
		t.Fatalf("read verification token: %v", err)
	}
	lookupTXTRecords = func(string) ([]string, error) {
		return []string{"sitebrush=" + verificationToken}, nil
	}

	checkForm := url.Values{}
	checkForm.Set("action", "check_alias")
	checkForm.Set("alias_domain", "example.com")
	checkRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?settings", strings.NewReader(checkForm.Encode()))
	checkRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	application.handleDomainSettingsPost(checkRequest.Context(), checkRequest, "localhost", "203.0.113.10")

	activeRequest := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	if domain := application.siteDomain(activeRequest.Context(), activeRequest); domain != "localhost" {
		t.Fatalf("active alias resolved to %q, want primary domain", domain)
	}

	selectForm := url.Values{}
	selectForm.Set("action", "select_alias")
	selectForm.Set("alias_domain", "example.com")
	selectRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?settings", strings.NewReader(selectForm.Encode()))
	selectRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	application.handleDomainSettingsPost(selectRequest.Context(), selectRequest, "localhost", "203.0.113.10")

	var selectedCount int
	if err := rawDB.QueryRow(`SELECT COUNT(1) FROM domain_aliases WHERE primary_domain=? AND alias_domain=? AND is_selected=1`, "localhost", "example.com").Scan(&selectedCount); err != nil {
		t.Fatalf("read selected alias: %v", err)
	}
	if selectedCount != 1 {
		t.Fatalf("selected aliases = %d, want 1", selectedCount)
	}
}

func TestDomainAliasLimitIsTenDomains(t *testing.T) {
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

	for aliasIndex := 0; aliasIndex < 11; aliasIndex++ {
		addForm := url.Values{}
		addForm.Set("action", "add_alias")
		addForm.Set("alias_domain", "alias"+strconv.Itoa(aliasIndex)+".example.com")
		addRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?settings", strings.NewReader(addForm.Encode()))
		addRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		application.handleDomainSettingsPost(addRequest.Context(), addRequest, "localhost", "")
	}

	if aliasCount := application.domainAliasCount(context.Background(), "localhost"); aliasCount != 10 {
		t.Fatalf("alias count = %d, want 10", aliasCount)
	}
}

func TestListenOnAvailablePortFallsBackWhenRequestedPortIsBusy(t *testing.T) {
	busyListener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer busyListener.Close()

	_, portText, err := net.SplitHostPort(busyListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	busyPort, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}

	fallbackListener, fallbackPort, err := listenOnAvailablePort(busyPort)
	if err != nil {
		t.Fatal(err)
	}
	defer fallbackListener.Close()

	if fallbackPort == busyPort {
		t.Fatalf("fallback port = busy port %d", busyPort)
	}
	if fallbackPort < 9898 {
		t.Fatalf("fallback port = %d, want 9898 or higher", fallbackPort)
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
