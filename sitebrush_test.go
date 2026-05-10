package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"io/fs"
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
	"sitebrush/pkg/diskusage"
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
	application.rebuildDomainStorageUsage(context.Background(), "localhost")
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
	for _, expectedFragment := range []string{"SiteBrushContextMenuVersion'>v.", "SiteBrushMenuStorageUsage", "10.0 GB"} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("context menu missing storage/version fragment %q in %s", expectedFragment, body)
		}
	}
	if strings.Contains(body, "href='?edit'") {
		t.Fatalf("context menu still contains intermediate edit link: %s", body)
	}
	for _, expectedFragment := range []string{`window.location.href = targetHref;`, `closestSitebrushEventElement(browserEvent, "#SiteBrushMenuBox")`, `function closeSitebrushMenu()`, `z-index:2147483647`, `closeSitebrushMenu();`, `data-sitebrush-owned`, `sitebrushContextMenuShadowCSS`, `attachShadow({mode: "open"})`, `menuRoot.appendChild(menuStyleElement)`, `.SiteBrushContextMenuLink:link`, `.SiteBrushContextMenuLink:visited`, `window.addEventListener("contextmenu", onContextMenuOpen, {capture: true, passive: false})`} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("context menu missing navigation guard %q in %s", expectedFragment, body)
		}
	}
}

func TestDomainStorageUsageRebuildsFromActualDiskUsage(t *testing.T) {
	application, rawDB := newTestApplication(t)
	domainFilesDir := application.domainFilesDirForDomain("localhost")
	if err := os.MkdirAll(filepath.Join(domainFilesDir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(domainFilesDir, "assets", "imported.png"), []byte("imported image"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := rawDB.Exec(`INSERT OR REPLACE INTO domain_storage_usage(domain,page_bytes,published_page_bytes,revision_bytes,file_bytes,published_static_bytes,limit_bytes,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		"localhost", 0, 0, 0, 0, 0, defaultDomainStorageLimitBytes, time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("seed stale storage usage: %v", err)
	}

	usage := application.domainStorageUsage(context.Background(), "localhost")
	expectedFileBytes := diskusage.DirectorySize(domainFilesDir)
	if usage.FileBytes != expectedFileBytes {
		t.Fatalf("file bytes = %d, want actual disk usage %d", usage.FileBytes, expectedFileBytes)
	}
}

func TestLoginReturnPathDefaultsToCurrentPageWithoutAutoVisual(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs?login", nil)
	if returnPath := loginReturnPathOrDefault(request); returnPath != "/docs" {
		t.Fatalf("return path = %q, want %q", returnPath, "/docs")
	}
}

func TestProtectedControllerRedirectsToLoginAndPreservesController(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs?files", nil)
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}

	redirectURL, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	if redirectURL.Path != "/docs" {
		t.Fatalf("redirect path = %q, want %q", redirectURL.Path, "/docs")
	}
	if _, hasLogin := redirectURL.Query()["login"]; !hasLogin {
		t.Fatalf("redirect query missing login flag: %q", redirectURL.RawQuery)
	}
	if returnPath := redirectURL.Query().Get("return_path"); returnPath != "/docs?files=" {
		t.Fatalf("return_path = %q, want %q", returnPath, "/docs?files=")
	}
}

func TestLoginPostRedirectsBackToRequestedController(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	form := url.Values{}
	form.Set("email", "admin@example.com")
	form.Set("password", "old")
	form.Set("return_path", "/docs?settings=")
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	application.login(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	if location := response.Header().Get("Location"); location != "/docs?settings=" {
		t.Fatalf("location = %q, want %q", location, "/docs?settings=")
	}
}

func TestFailedLoginAttemptsEscalateToIPBlock(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	for attemptIndex := 1; attemptIndex <= 4; attemptIndex++ {
		form := url.Values{}
		form.Set("email", "admin@example.com")
		form.Set("password", "wrong")
		request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?login", strings.NewReader(form.Encode()))
		request.RemoteAddr = "198.51.100.10:1234"
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()

		application.login(response, request)
		expectedStatus := http.StatusUnauthorized
		if attemptIndex == 4 {
			expectedStatus = http.StatusTooManyRequests
		}
		if response.Code != expectedStatus {
			t.Fatalf("attempt %d status = %d, want %d, body=%q", attemptIndex, response.Code, expectedStatus, response.Body.String())
		}
	}
}

func TestFailedLoginRendersLoginFormWithLocalizedStatus(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	form := url.Values{}
	form.Set("email", "admin@example.com")
	form.Set("password", "wrong")
	form.Set("return_path", "/docs")
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?login", strings.NewReader(form.Encode()))
	request.RemoteAddr = "198.51.100.11:1234"
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept-Language", "ru")
	response := httptest.NewRecorder()

	application.login(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d, body=%q", response.Code, http.StatusUnauthorized, response.Body.String())
	}
	body := response.Body.String()
	for _, expectedFragment := range []string{`<form class="card card-body" method="post" action="?login"`, "Неверный email или пароль.", `value="admin@example.com"`} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("login failure page missing %q in %s", expectedFragment, body)
		}
	}
}

func TestTenthFailedLoginAttemptRequiresRecovery(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO auth_ip_failures(domain,client_ip,failure_count,blocked_until,hard_locked,last_failed_at,last_attempt_at) VALUES(?,?,?,?,?,?,?)`,
		"localhost", "198.51.100.20", 9, "", 0, time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("seed auth failures: %v", err)
	}

	form := url.Values{}
	form.Set("email", "admin@example.com")
	form.Set("password", "wrong")
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?login", strings.NewReader(form.Encode()))
	request.RemoteAddr = "198.51.100.20:1234"
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	application.login(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body=%q", response.Code, http.StatusForbidden, response.Body.String())
	}
}

func TestBlockedLoginPageShowsCountdownInsteadOfForm(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO auth_ip_failures(domain,client_ip,failure_count,blocked_until,hard_locked,last_failed_at,last_attempt_at) VALUES(?,?,?,?,?,?,?)`,
		"localhost", "198.51.100.21", 4, time.Now().UTC().Add(time.Minute).Format(time.RFC3339), 0, time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("seed auth failures: %v", err)
	}

	router := http.NewServeMux()
	router.HandleFunc("/", application.route)
	protectedHandler := application.authAbuseMiddleware(router)

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs?login", nil)
	request.RemoteAddr = "198.51.100.21:1234"
	request.Header.Set("Accept-Language", "ru")
	response := httptest.NewRecorder()
	protectedHandler.ServeHTTP(response, request)

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d, body=%q", response.Code, http.StatusTooManyRequests, response.Body.String())
	}
	body := response.Body.String()
	for _, unexpectedFragment := range []string{`name="password"`, `name="email"`} {
		if strings.Contains(body, unexpectedFragment) {
			t.Fatalf("blocked login page should hide form field %q in %s", unexpectedFragment, body)
		}
	}
	for _, expectedFragment := range []string{"Повторить попытку можно через:", `id="SiteBrushLoginCountdown"`} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("blocked login page missing %q in %s", expectedFragment, body)
		}
	}
}

func TestHardLockedLoginPageShowsRecoveryInsteadOfForm(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO auth_ip_failures(domain,client_ip,failure_count,blocked_until,hard_locked,last_failed_at,last_attempt_at) VALUES(?,?,?,?,?,?,?)`,
		"localhost", "198.51.100.22", 10, "", 1, time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("seed auth failures: %v", err)
	}

	router := http.NewServeMux()
	router.HandleFunc("/", application.route)
	protectedHandler := application.authAbuseMiddleware(router)

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs?login", nil)
	request.RemoteAddr = "198.51.100.22:1234"
	request.Header.Set("Accept-Language", "ru")
	response := httptest.NewRecorder()
	protectedHandler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body=%q", response.Code, http.StatusForbidden, response.Body.String())
	}
	body := response.Body.String()
	for _, unexpectedFragment := range []string{`name="password"`, `name="email"`} {
		if strings.Contains(body, unexpectedFragment) {
			t.Fatalf("hard-locked login page should hide form field %q in %s", unexpectedFragment, body)
		}
	}
	for _, expectedFragment := range []string{"используйте восстановление доступа", `href="?recover"`} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("hard-locked login page missing %q in %s", expectedFragment, body)
		}
	}
}

func TestBlockedIPMiddlewareLeavesNonLoginRequestsAvailable(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO auth_ip_failures(domain,client_ip,failure_count,blocked_until,hard_locked,last_failed_at,last_attempt_at) VALUES(?,?,?,?,?,?,?)`,
		"localhost", "198.51.100.30", 4, time.Now().UTC().Add(time.Minute).Format(time.RFC3339), 0, time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("seed auth failures: %v", err)
	}

	router := http.NewServeMux()
	staticFiles, err := fs.Sub(embeddedWebFiles, "web/static")
	if err != nil {
		t.Fatalf("static subfs: %v", err)
	}
	router.Handle("/p/static/", http.StripPrefix("/p/static/", http.FileServer(http.FS(staticFiles))))
	router.HandleFunc("/p/", application.servePublicAsset)
	router.HandleFunc("/", application.route)
	protectedHandler := application.authAbuseMiddleware(router)

	dynamicRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/", nil)
	dynamicRequest.RemoteAddr = "198.51.100.30:1234"
	dynamicResponse := httptest.NewRecorder()
	protectedHandler.ServeHTTP(dynamicResponse, dynamicRequest)
	if dynamicResponse.Code == http.StatusTooManyRequests || dynamicResponse.Code == http.StatusForbidden {
		t.Fatalf("dynamic status = %d, want non-login request to remain available, body=%q", dynamicResponse.Code, dynamicResponse.Body.String())
	}

	staticRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/p/static/jodit.min.js", nil)
	staticRequest.RemoteAddr = "198.51.100.30:1234"
	staticResponse := httptest.NewRecorder()
	protectedHandler.ServeHTTP(staticResponse, staticRequest)
	if staticResponse.Code != http.StatusOK {
		t.Fatalf("static status = %d, want %d", staticResponse.Code, http.StatusOK)
	}
}

func TestBlockedLoginPageUsesSameTimerFromAnyURI(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO auth_ip_failures(domain,client_ip,failure_count,blocked_until,hard_locked,last_failed_at,last_attempt_at) VALUES(?,?,?,?,?,?,?)`,
		"localhost", "198.51.100.31", 4, time.Now().UTC().Add(2*time.Minute).Format(time.RFC3339), 0, time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("seed auth failures: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/address/book?login", nil)
	request.RemoteAddr = "198.51.100.31:1234"
	request.Header.Set("Accept-Language", "ru")
	response := httptest.NewRecorder()

	application.route(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d, body=%q", response.Code, http.StatusTooManyRequests, response.Body.String())
	}
	body := response.Body.String()
	for _, expectedFragment := range []string{
		"Повторить попытку можно через:",
		`id="SiteBrushLoginCountdown"`,
	} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("blocked login page missing %q in %s", expectedFragment, body)
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

func TestSavePagePropagatesSiteBrushTemplateToOtherPagesAndPublishedOutputs(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	navigationBefore := `<nav class="SiteBrush-Template shared-nav"><a href="/">Home</a></nav>`
	navigationAfter := `<nav class="SiteBrush-Template shared-nav"><a href="/">Updated</a></nav>`
	homeHTML := "<html><body>" + navigationBefore + `<main>Home</main></body></html>`
	aboutHTML := "<html><body><header>About</header>" + navigationBefore + `<main>About</main></body></html>`
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1),(?,?,?,?,1)`,
		"localhost", "/", "Home", homeHTML,
		"localhost", "/about", "About", aboutHTML,
	)
	if err != nil {
		t.Fatalf("insert pages: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES(?,?,?,?),(?,?,?,?)`,
		"localhost", "/", "Home", homeHTML,
		"localhost", "/about", "About", aboutHTML,
	)
	if err != nil {
		t.Fatalf("insert published pages: %v", err)
	}
	application.writePublishedStaticHTML("localhost", "/", homeHTML)
	application.writePublishedStaticHTML("localhost", "/about", aboutHTML)

	form := url.Values{}
	form.Set("path", "/")
	form.Set("title", "Home")
	form.Set("html", "<html><body>"+navigationAfter+`<main>Home</main></body></html>`)
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?save", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}

	var updatedAboutHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/about").Scan(&updatedAboutHTML); err != nil {
		t.Fatalf("read about page: %v", err)
	}
	if !strings.Contains(updatedAboutHTML, navigationAfter) {
		t.Fatalf("about page did not receive propagated template: %s", updatedAboutHTML)
	}

	var updatedPublishedAboutHTML string
	if err := rawDB.QueryRow(`SELECT html FROM published_pages WHERE domain=? AND path=?`, "localhost", "/about").Scan(&updatedPublishedAboutHTML); err != nil {
		t.Fatalf("read published about page: %v", err)
	}
	if !strings.Contains(updatedPublishedAboutHTML, navigationAfter) {
		t.Fatalf("published about page did not receive propagated template: %s", updatedPublishedAboutHTML)
	}

	aboutStaticHTML, readErr := os.ReadFile(filepath.Join(application.domainStaticDir("localhost"), staticRelativePathForPage("/about")))
	if readErr != nil {
		t.Fatalf("read static about page: %v", readErr)
	}
	if !strings.Contains(string(aboutStaticHTML), navigationAfter) {
		t.Fatalf("static about page did not receive propagated template: %s", string(aboutStaticHTML))
	}

	var aboutRevisionCount int
	if err := rawDB.QueryRow(`SELECT COUNT(*) FROM revisions WHERE domain=? AND page_path=?`, "localhost", "/about").Scan(&aboutRevisionCount); err != nil {
		t.Fatalf("count about revisions: %v", err)
	}
	if aboutRevisionCount != 1 {
		t.Fatalf("about revision count = %d, want 1", aboutRevisionCount)
	}
}

func TestFrozenSavePublishUpdatesPublishedStaticForGuests(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	oldHTML := "<html><body><h1>Old public page</h1></body></html>"
	newHTML := "<html><body><h1>New frozen edit</h1></body></html>"
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/docs", "Docs", oldHTML)
	if err != nil {
		t.Fatalf("insert page: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, "localhost", "/docs", "Docs", oldHTML)
	if err != nil {
		t.Fatalf("insert published page: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO revisions(domain,page_path,html,created_at,is_active) VALUES(?,?,?,?,1)`, "localhost", "/docs", oldHTML, time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("insert revision: %v", err)
	}
	application.writePublishedStaticHTML("localhost", "/docs", oldHTML)
	application.setDomainFrozenState(context.Background(), "localhost", 1)

	saveForm := url.Values{}
	saveForm.Set("path", "/docs")
	saveForm.Set("title", "Docs")
	saveForm.Set("html", newHTML)
	saveRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?save", strings.NewReader(saveForm.Encode()))
	saveRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	saveRequest.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	saveResponse := httptest.NewRecorder()
	application.route(saveResponse, saveRequest)
	if saveResponse.Code != http.StatusFound {
		t.Fatalf("save status = %d, body=%q", saveResponse.Code, saveResponse.Body.String())
	}

	frozenGuestRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs", nil)
	frozenGuestResponse := httptest.NewRecorder()
	application.route(frozenGuestResponse, frozenGuestRequest)
	if !strings.Contains(frozenGuestResponse.Body.String(), "Old public page") {
		t.Fatalf("frozen guest did not keep old static page: %s", frozenGuestResponse.Body.String())
	}
	if strings.Contains(frozenGuestResponse.Body.String(), "New frozen edit") {
		t.Fatalf("frozen guest saw draft edit before publish: %s", frozenGuestResponse.Body.String())
	}

	publishRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?publish", nil)
	publishRequest.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	publishResponse := httptest.NewRecorder()
	application.route(publishResponse, publishRequest)
	if publishResponse.Code != http.StatusFound {
		t.Fatalf("publish status = %d, body=%q", publishResponse.Code, publishResponse.Body.String())
	}

	var publishedHTML string
	if err := rawDB.QueryRow(`SELECT html FROM published_pages WHERE domain=? AND path=?`, "localhost", "/docs").Scan(&publishedHTML); err != nil {
		t.Fatalf("read published page: %v", err)
	}
	if publishedHTML != newHTML {
		t.Fatalf("published html = %q, want %q", publishedHTML, newHTML)
	}
	staticHTML, readErr := os.ReadFile(filepath.Join(application.domainStaticDir("localhost"), staticRelativePathForPage("/docs")))
	if readErr != nil {
		t.Fatalf("read static page: %v", readErr)
	}
	if string(staticHTML) != newHTML {
		t.Fatalf("static html = %q, want %q", string(staticHTML), newHTML)
	}
	if application.isDomainFrozen(context.Background(), "localhost") {
		t.Fatal("domain remained frozen after publish")
	}

	publishedGuestRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs", nil)
	publishedGuestResponse := httptest.NewRecorder()
	application.route(publishedGuestResponse, publishedGuestRequest)
	if !strings.Contains(publishedGuestResponse.Body.String(), "New frozen edit") {
		t.Fatalf("published guest did not see new static page: %s", publishedGuestResponse.Body.String())
	}
}

func TestSavePagePropagatesLegacySitebrushTemplateClass(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	legacyBefore := `<div class="sitebrush-template-footer">Old footer</div>`
	legacyAfter := `<div class="sitebrush-template-footer">New footer</div>`
	homeHTML := "<html><body>" + legacyBefore + `</body></html>`
	contactsHTML := "<html><body><main>Contacts</main>" + legacyBefore + `</body></html>`
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1),(?,?,?,?,1)`,
		"localhost", "/", "Home", homeHTML,
		"localhost", "/contacts", "Contacts", contactsHTML,
	)
	if err != nil {
		t.Fatalf("insert pages: %v", err)
	}

	form := url.Values{}
	form.Set("path", "/")
	form.Set("title", "Home")
	form.Set("html", "<html><body>"+legacyAfter+`</body></html>`)
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?save", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}

	var updatedContactsHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/contacts").Scan(&updatedContactsHTML); err != nil {
		t.Fatalf("read contacts page: %v", err)
	}
	if !strings.Contains(updatedContactsHTML, legacyAfter) {
		t.Fatalf("legacy template did not propagate: %s", updatedContactsHTML)
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
			assetBaseURL + "/module.js":        {contentType: "application/javascript", body: `console.log("module");`},
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
	previewResources := previewGrabResources(pageURL, sourceHTML, nil, "", "")
	if len(previewResources) != 7 {
		t.Fatalf("expected 7 preview resources, got %d: %#v", len(previewResources), previewResources)
	}

	selectedResourceURLs := make(map[string]struct{})
	for _, previewResource := range previewResources {
		selectedResourceURLs[previewResource.URL] = struct{}{}
	}
	application, _ := newTestApplication(t)
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
	usage := application.domainStorageUsage(context.Background(), "example.test")
	expectedFileBytes := diskusage.DirectorySize(application.domainFilesDirForDomain("example.test"))
	if usage.FileBytes != expectedFileBytes {
		t.Fatalf("imported asset bytes = %d, want actual disk usage %d", usage.FileBytes, expectedFileBytes)
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

func TestMirrorRemotePageImportsCrossDomainAssetsWithoutURLExtensions(t *testing.T) {
	pageRawURL := "https://page.example/page"
	sourceHTML := `<!doctype html><html><head>` +
		`<link rel="stylesheet" href="https://cdn.example/styles?id=42">` +
		`</head><body><img src="https://img.example/render?asset=hero"></body></html>`

	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			"https://cdn.example/styles?id=42":      {contentType: "text/css; charset=utf-8", body: `@font-face{src:url("https://fonts.example/open?id=7")} body{background:url("https://img.example/bg?asset=1")}`},
			"https://fonts.example/open?id=7":       {contentType: "font/woff2", body: "font"},
			"https://img.example/bg?asset=1":        {contentType: "image/png", body: "bg"},
			"https://img.example/render?asset=hero": {contentType: "image/png", body: "hero"},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	pageURL, parseErr := url.Parse(pageRawURL)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	previewResources := previewGrabResources(pageURL, sourceHTML, nil, "", "")
	if len(previewResources) != 4 {
		t.Fatalf("expected 4 preview resources, got %d: %#v", len(previewResources), previewResources)
	}

	selectedResourceURLs := make(map[string]struct{}, len(previewResources))
	for _, previewResource := range previewResources {
		selectedResourceURLs[previewResource.URL] = struct{}{}
	}
	application, _ := newTestApplication(t)
	importedHTML := application.mirrorRemotePage("example.test", "/imported", pageRawURL, pageURL, sourceHTML, "", selectedResourceURLs, "")
	if strings.Contains(importedHTML, "cdn.example") || strings.Contains(importedHTML, "img.example") || strings.Contains(importedHTML, "fonts.example") {
		t.Fatalf("imported HTML still references external extensionless assets: %s", importedHTML)
	}
	if !strings.Contains(importedHTML, "/p/") {
		t.Fatalf("imported HTML does not reference local assets: %s", importedHTML)
	}

	storedFiles, listErr := listStoredFiles(application.domainFilesDirForDomain("example.test"))
	if listErr != nil {
		t.Fatal(listErr)
	}
	expectedExtensions := map[string]bool{".css": false, ".woff2": false, ".png": false}
	for _, storedFilePath := range storedFiles {
		if _, tracked := expectedExtensions[filepath.Ext(storedFilePath)]; tracked {
			expectedExtensions[filepath.Ext(storedFilePath)] = true
		}
	}
	for expectedExtension, found := range expectedExtensions {
		if !found {
			t.Fatalf("expected imported extensionless asset with extension %s, stored files: %#v", expectedExtension, storedFiles)
		}
	}
}

func TestMirrorRemotePageImportsDocumentMediaAndArchiveLinks(t *testing.T) {
	pageRawURL := "https://page.example/page"
	sourceHTML := `<!doctype html><html><body>` +
		`<a href="https://cdn.example/download?id=manual">Manual</a>` +
		`<a href="https://cdn.example/archive.zip">Archive</a>` +
		`<a href="https://cdn.example/feed.json">Feed</a>` +
		`<video controls src="https://cdn.example/media/intro.mp4"></video>` +
		`<audio controls src="https://cdn.example/audio/theme.mp3"></audio>` +
		`</body></html>`

	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			"https://cdn.example/download?id=manual": {contentType: "application/pdf", body: "%PDF-1.7"},
			"https://cdn.example/archive.zip":        {contentType: "application/zip", body: "zip"},
			"https://cdn.example/feed.json":          {contentType: "application/json", body: `{"ok":true}`},
			"https://cdn.example/media/intro.mp4":    {contentType: "video/mp4", body: "mp4"},
			"https://cdn.example/audio/theme.mp3":    {contentType: "audio/mpeg", body: "mp3"},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	pageURL, parseErr := url.Parse(pageRawURL)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	previewResources := previewGrabResources(pageURL, sourceHTML, nil, "", "")
	if len(previewResources) != 5 {
		t.Fatalf("expected 5 preview resources, got %d: %#v", len(previewResources), previewResources)
	}

	selectedResourceURLs := make(map[string]struct{}, len(previewResources))
	for _, previewResource := range previewResources {
		selectedResourceURLs[previewResource.URL] = struct{}{}
	}

	application, _ := newTestApplication(t)
	importedHTML := application.mirrorRemotePage("example.test", "/imported", pageRawURL, pageURL, sourceHTML, "", selectedResourceURLs, "")
	for _, forbiddenFragment := range []string{"cdn.example/download?id=manual", "cdn.example/archive.zip", "cdn.example/feed.json", "cdn.example/media/intro.mp4", "cdn.example/audio/theme.mp3"} {
		if strings.Contains(importedHTML, forbiddenFragment) {
			t.Fatalf("imported HTML still references external resource %q: %s", forbiddenFragment, importedHTML)
		}
	}
	if strings.Count(importedHTML, `/p/`) < 5 {
		t.Fatalf("imported HTML does not contain local references for all resources: %s", importedHTML)
	}

	storedFiles, listErr := listStoredFiles(application.domainFilesDirForDomain("example.test"))
	if listErr != nil {
		t.Fatal(listErr)
	}
	expectedExtensions := map[string]bool{
		".pdf":  false,
		".zip":  false,
		".json": false,
		".mp4":  false,
		".mp3":  false,
	}
	for _, storedFilePath := range storedFiles {
		if _, tracked := expectedExtensions[filepath.Ext(storedFilePath)]; tracked {
			expectedExtensions[filepath.Ext(storedFilePath)] = true
		}
	}
	for expectedExtension, found := range expectedExtensions {
		if !found {
			t.Fatalf("expected imported resource with extension %s, stored files: %#v", expectedExtension, storedFiles)
		}
	}
}

func TestGrabPreviewReportsQuotaAndGrabRejectsOversizedImport(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	application.ensureDomainStorageUsageRow(context.Background(), "localhost")
	_, err = rawDB.Exec(`UPDATE domain_storage_usage SET limit_bytes=? WHERE domain=?`, 120, "localhost")
	if err != nil {
		t.Fatalf("update storage limit: %v", err)
	}

	sourceURL := "https://quota.example/page"
	sourceHTML := `<!doctype html><html><body><a href="https://quota.example/manual.pdf">Manual</a></body></html>`

	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			sourceURL:                          {contentType: "text/html", body: sourceHTML},
			"https://quota.example/manual.pdf": {contentType: "application/pdf", body: strings.Repeat("P", 80)},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	previewForm := url.Values{}
	previewForm.Set("path", "/quota")
	previewForm.Set("source_url", sourceURL)
	previewRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/quota?grab_preview", strings.NewReader(previewForm.Encode()))
	previewRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	previewRequest.Header.Set("Accept", "application/json")
	previewRequest.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	previewResponse := httptest.NewRecorder()
	application.route(previewResponse, previewRequest)
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body=%q", previewResponse.Code, previewResponse.Body.String())
	}

	var previewPayload grabPreviewResponse
	if err := json.Unmarshal(previewResponse.Body.Bytes(), &previewPayload); err != nil {
		t.Fatalf("decode preview payload: %v", err)
	}
	if previewPayload.FitsQuota {
		t.Fatalf("expected preview to exceed quota, payload=%+v", previewPayload)
	}
	if previewPayload.ProjectedUsedBytes <= previewPayload.LimitBytes {
		t.Fatalf("expected projected usage %d to exceed limit %d", previewPayload.ProjectedUsedBytes, previewPayload.LimitBytes)
	}
	if previewPayload.SelectedResourceBytes < 80 {
		t.Fatalf("expected selected resource bytes to include pdf, payload=%+v", previewPayload)
	}

	grabForm := url.Values{}
	grabForm.Set("path", "/quota")
	grabForm.Set("source_url", sourceURL)
	grabRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/quota?grab", strings.NewReader(grabForm.Encode()))
	grabRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	grabRequest.Header.Set("Accept", "application/json")
	grabRequest.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	grabResponse := httptest.NewRecorder()
	application.route(grabResponse, grabRequest)
	if grabResponse.Code != http.StatusInsufficientStorage {
		t.Fatalf("grab status = %d, body=%q", grabResponse.Code, grabResponse.Body.String())
	}
	if !strings.Contains(grabResponse.Body.String(), "storage limit reached:") {
		t.Fatalf("grab body does not mention storage limit: %q", grabResponse.Body.String())
	}
}

func TestRewriteJSResourceReferencesLeavesLibraryCodeIntact(t *testing.T) {
	pageRawURL := "https://page.example/page"
	sourceHTML := `<!doctype html><html><body><script src="/js/app.js"></script></body></html>`

	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			"https://page.example/js/app.js": {contentType: "application/javascript", body: `
				var analyticsURL = ('https:' == document.location.protocol ? 'https://ssl' : 'http://www') + '.google-analytics.com/ga.js';
				var selectorOperator = "*=";
				var imagePath = "/images/logo.png";
			`},
			"https://page.example/images/logo.png": {contentType: "image/png", body: "logo"},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	pageURL, parseErr := url.Parse(pageRawURL)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	previewResources := previewGrabResources(pageURL, sourceHTML, nil, "", "")
	selectedResourceURLs := make(map[string]struct{}, len(previewResources))
	for _, previewResource := range previewResources {
		selectedResourceURLs[previewResource.URL] = struct{}{}
	}

	application, _ := newTestApplication(t)
	application.mirrorRemotePage("example.test", "/imported", pageRawURL, pageURL, sourceHTML, "", selectedResourceURLs, "")

	storedFiles, listErr := listStoredFiles(application.domainFilesDirForDomain("example.test"))
	if listErr != nil {
		t.Fatal(listErr)
	}
	var storedScript string
	for _, storedFilePath := range storedFiles {
		if filepath.Ext(storedFilePath) != ".js" {
			continue
		}
		storedBytes, readErr := os.ReadFile(storedFilePath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		storedScript = string(storedBytes)
		break
	}
	if storedScript == "" {
		t.Fatalf("expected rewritten JS file, stored files: %#v", storedFiles)
	}
	if !strings.Contains(storedScript, `'.google-analytics.com/ga.js'`) {
		t.Fatalf("analytics suffix string was unexpectedly rewritten: %s", storedScript)
	}
	if !strings.Contains(storedScript, `"*="`) {
		t.Fatalf("selector operator string was unexpectedly rewritten: %s", storedScript)
	}
	if !strings.Contains(storedScript, `/p/`) {
		t.Fatalf("real JS asset path was not rewritten to local resource: %s", storedScript)
	}
}

func TestGrabPageCanCopyWholeExternalSiteUnderLocalPath(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	sourceBaseURL := "https://source.example"
	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			sourceBaseURL + "/":                                         {contentType: "text/html", body: `<!doctype html><html><head><link rel="stylesheet" href="/style.css"><link rel="stylesheet" href="https://fonts.googleapis.com/css?family=PT+Sans+Narrow&v1"><link rel="stylesheet" href="https://fonts.googleapis.com/css?family=Monoton"><script src="/app.js"></script></head><body><a href="/about">About</a><a href="https://outside.example/x">Outside</a><img src="/images/logo.png"></body></html>`},
			sourceBaseURL + "/about":                                    {contentType: "text/html", body: `<!doctype html><html><body><a href="/">Home</a><a href="contact.html">Contact</a><img src="about.png"><iframe src="/contact.html"></iframe></body></html>`},
			sourceBaseURL + "/contact.html":                             {contentType: "text/html", body: `<!doctype html><html><body><a href="/about">About</a></body></html>`},
			sourceBaseURL + "/style.css":                                {contentType: "text/css", body: `@import url("/nested.css"); body{background:url("/images/bg.png")}`},
			sourceBaseURL + "/nested.css":                               {contentType: "text/css", body: `.nested{background:url("/fonts/font.woff2")}`},
			sourceBaseURL + "/app.js":                                   {contentType: "application/javascript", body: `import "/chunk.js"; const icon = "/icons/icon.svg";`},
			sourceBaseURL + "/chunk.js":                                 {contentType: "application/javascript", body: `console.log("chunk");`},
			sourceBaseURL + "/images/logo.png":                          {contentType: "image/png", body: "logo"},
			sourceBaseURL + "/images/bg.png":                            {contentType: "image/png", body: "bg"},
			sourceBaseURL + "/about.png":                                {contentType: "image/png", body: "about"},
			sourceBaseURL + "/fonts/font.woff2":                         {contentType: "font/woff2", body: "font"},
			sourceBaseURL + "/icons/icon.svg":                           {contentType: "image/svg+xml", body: "<svg/>"},
			"https://fonts.googleapis.com/css?family=PT+Sans+Narrow&v1": {contentType: "text/css; charset=utf-8", body: `@font-face{src:url("https://fonts.gstatic.com/s/ptsansnarrow.woff2")}`},
			"https://fonts.googleapis.com/css?family=Monoton":           {contentType: "text/css; charset=utf-8", body: `@font-face{src:url("https://fonts.gstatic.com/s/monoton.woff2")}`},
			"https://fonts.gstatic.com/s/ptsansnarrow.woff2":            {contentType: "font/woff2", body: "ptsans"},
			"https://fonts.gstatic.com/s/monoton.woff2":                 {contentType: "font/woff2", body: "monoton"},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	previewForm := url.Values{}
	previewForm.Set("source_url", sourceBaseURL+"/")
	previewForm.Set("copy_whole_site", "1")
	previewForm.Set("progress_token", "preview-token")
	previewRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/URI?grab_preview", strings.NewReader(previewForm.Encode()))
	previewRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	previewRequest.Header.Set("Accept", "application/json")
	previewRequest.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	previewResponse := httptest.NewRecorder()
	application.route(previewResponse, previewRequest)
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("whole-site preview status = %d, body=%q", previewResponse.Code, previewResponse.Body.String())
	}
	var previewPayload grabPreviewResponse
	if err := json.Unmarshal(previewResponse.Body.Bytes(), &previewPayload); err != nil {
		t.Fatalf("decode whole-site preview: %v", err)
	}
	if previewPayload.PageCount != 3 {
		t.Fatalf("whole-site preview page count = %d, want 3", previewPayload.PageCount)
	}
	if previewPayload.ResourceCount < 8 {
		t.Fatalf("whole-site preview resource count = %d, want at least 8: %#v", previewPayload.ResourceCount, previewPayload.Resources)
	}
	for _, previewResource := range previewPayload.Resources {
		if strings.Contains(previewResource.URL, "outside.example") {
			t.Fatalf("whole-site preview included external document link as resource: %#v", previewPayload.Resources)
		}
	}

	form := url.Values{}
	form.Set("path", "/URI")
	form.Set("source_url", sourceBaseURL+"/")
	form.Set("copy_whole_site", "1")
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/URI?grab", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("whole-site import status = %d, body=%q", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"/URI"`) || strings.Contains(response.Body.String(), `?visual`) {
		t.Fatalf("whole-site import redirect does not point to local base path: %s", response.Body.String())
	}

	expectedPages := []string{"/URI", "/URI/about", "/URI/contact.html"}
	for _, expectedPagePath := range expectedPages {
		var pageHTML string
		if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", expectedPagePath).Scan(&pageHTML); err != nil {
			t.Fatalf("missing imported page %s: %v", expectedPagePath, err)
		}
		if strings.Contains(pageHTML, sourceBaseURL) {
			t.Fatalf("imported page %s still references source host: %s", expectedPagePath, pageHTML)
		}
	}

	var rootHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/URI").Scan(&rootHTML); err != nil {
		t.Fatalf("read root imported page: %v", err)
	}
	for _, expectedFragment := range []string{`href="/URI/about"`, `href="https://outside.example/x"`, `href="/URI/p/`, `src="/URI/p/`} {
		if !strings.Contains(rootHTML, expectedFragment) {
			t.Fatalf("root imported page missing %q in %s", expectedFragment, rootHTML)
		}
	}
	var aboutHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/URI/about").Scan(&aboutHTML); err != nil {
		t.Fatalf("read about imported page: %v", err)
	}
	for _, expectedFragment := range []string{`href="/URI"`, `href="/URI/contact.html"`, `src="/URI/contact.html"`} {
		if !strings.Contains(aboutHTML, expectedFragment) {
			t.Fatalf("about imported page missing %q in %s", expectedFragment, aboutHTML)
		}
	}

	storedFiles, listErr := listStoredFiles(application.domainFilesDirForDomain("localhost"))
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(storedFiles) < 8 {
		t.Fatalf("expected imported resources to be stored locally, got %d: %#v", len(storedFiles), storedFiles)
	}
	foundRewrittenJS := false
	foundRewrittenCSS := false
	for _, storedFilePath := range storedFiles {
		storedBytes, readErr := os.ReadFile(storedFilePath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		storedText := string(storedBytes)
		if filepath.Ext(storedFilePath) == ".js" && strings.Contains(storedText, "/URI/p/") {
			foundRewrittenJS = true
		}
		if filepath.Ext(storedFilePath) == ".css" && strings.Contains(storedText, "/URI/p/") {
			foundRewrittenCSS = true
		}
	}
	if !foundRewrittenJS {
		t.Fatalf("imported JS did not rewrite nested local resources: %#v", storedFiles)
	}
	if !foundRewrittenCSS {
		t.Fatalf("imported CSS did not rewrite nested local resources: %#v", storedFiles)
	}

	assetPrefixIndex := strings.Index(rootHTML, "/URI/p/")
	if assetPrefixIndex < 0 {
		t.Fatalf("root imported page does not contain base-prefixed asset path: %s", rootHTML)
	}
	assetPathEnd := assetPrefixIndex
	for assetPathEnd < len(rootHTML) && rootHTML[assetPathEnd] != '"' && rootHTML[assetPathEnd] != '\'' && rootHTML[assetPathEnd] != ' ' {
		assetPathEnd++
	}
	assetRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080"+rootHTML[assetPrefixIndex:assetPathEnd], nil)
	assetResponse := httptest.NewRecorder()
	application.route(assetResponse, assetRequest)
	if assetResponse.Code != http.StatusOK {
		t.Fatalf("base-prefixed asset status = %d, path=%q", assetResponse.Code, rootHTML[assetPrefixIndex:assetPathEnd])
	}
}

func TestGrabPageCanCopyWholeExternalSiteWithCrossDomainAssetsWithoutURLExtensions(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	sourceBaseURL := "https://source.example"
	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			sourceBaseURL + "/":                     {contentType: "text/html", body: `<!doctype html><html><head><link rel="stylesheet" href="https://cdn.example/theme?id=5"></head><body><img src="https://img.example/render?asset=hero"><a href="/about">About</a></body></html>`},
			sourceBaseURL + "/about":                {contentType: "text/html", body: `<!doctype html><html><body><a href="/">Home</a></body></html>`},
			"https://cdn.example/theme?id=5":        {contentType: "text/css; charset=utf-8", body: `@font-face{src:url("https://fonts.example/family?id=7")} body{background:url("https://img.example/bg?asset=1")}`},
			"https://fonts.example/family?id=7":     {contentType: "font/woff2", body: "font"},
			"https://img.example/bg?asset=1":        {contentType: "image/png", body: "bg"},
			"https://img.example/render?asset=hero": {contentType: "image/png", body: "hero"},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	form := url.Values{}
	form.Set("path", "/URI")
	form.Set("source_url", sourceBaseURL+"/")
	form.Set("copy_whole_site", "1")
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/URI?grab", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("whole-site import status = %d, body=%q", response.Code, response.Body.String())
	}

	var rootHTML string
	if err := rawDB.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", "/URI").Scan(&rootHTML); err != nil {
		t.Fatalf("read root imported page: %v", err)
	}
	if strings.Contains(rootHTML, "cdn.example") || strings.Contains(rootHTML, "img.example") || strings.Contains(rootHTML, "fonts.example") {
		t.Fatalf("whole-site import still references external extensionless assets: %s", rootHTML)
	}
	for _, expectedFragment := range []string{`href="/URI/p/`, `src="/URI/p/`} {
		if !strings.Contains(rootHTML, expectedFragment) {
			t.Fatalf("whole-site import missing local asset fragment %q in %s", expectedFragment, rootHTML)
		}
	}

	storedFiles, listErr := listStoredFiles(application.domainFilesDirForDomain("localhost"))
	if listErr != nil {
		t.Fatal(listErr)
	}
	expectedExtensions := map[string]bool{".css": false, ".woff2": false, ".png": false}
	for _, storedFilePath := range storedFiles {
		if _, tracked := expectedExtensions[filepath.Ext(storedFilePath)]; tracked {
			expectedExtensions[filepath.Ext(storedFilePath)] = true
		}
	}
	for expectedExtension, found := range expectedExtensions {
		if !found {
			t.Fatalf("expected whole-site imported extensionless asset with extension %s, stored files: %#v", expectedExtension, storedFiles)
		}
	}
}

func TestGrabPageRedirectsToImportedPageView(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			"https://source.example/page": {contentType: "text/html", body: "<!doctype html><html><body>Imported page</body></html>"},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	form := url.Values{}
	form.Set("path", "/docs")
	form.Set("source_url", "https://source.example/page")
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?grab", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("grab status = %d, body=%q", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"/docs"`) || strings.Contains(response.Body.String(), `?visual`) {
		t.Fatalf("grab redirect should open imported page view, got %s", response.Body.String())
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

func TestMirrorRemotePageInjectsRuntimeGuardBeforeImportedScripts(t *testing.T) {
	pageRawURL := "https://sitebrush.com/"
	sourceHTML := `<!doctype html><html><head><script>window.loopStarted = true;</script></head><body>` +
		`<script>document.body.insertAdjacentHTML("beforeend", "<iframe src='/'>" + document.documentElement.outerHTML + "</iframe>");</script>` +
		`</body></html>`

	pageURL, parseErr := url.Parse(pageRawURL)
	if parseErr != nil {
		t.Fatal(parseErr)
	}

	application := &App{storagePath: t.TempDir(), grabTracker: newGrabProgressTracker()}
	importedHTML := application.mirrorRemotePage("localhost", "/", pageRawURL, pageURL, sourceHTML, "", nil, "")

	guardIndex := strings.Index(importedHTML, "data-sitebrush-import-runtime-guard")
	importedScriptIndex := strings.Index(importedHTML, "window.loopStarted = true")
	if guardIndex < 0 {
		t.Fatalf("imported HTML does not include runtime guard: %s", importedHTML)
	}
	if importedScriptIndex < 0 {
		t.Fatalf("imported HTML lost imported script: %s", importedHTML)
	}
	if guardIndex > importedScriptIndex {
		t.Fatalf("runtime guard was inserted after imported script: %s", importedHTML)
	}
	for _, expectedFragment := range []string{
		"processedElements = new WeakSet",
		"seenSourceKeys = new Set",
		"maximumElementDepth",
		"maximumInsertedNodeCount",
		"maximumMutationRecordCount",
		"maximumGuardRunMilliseconds",
		"Node.prototype.appendChild",
		"Element.prototype.insertAdjacentHTML",
		"isSitebrushOwnedNode",
		"sitebrushOwnedAttributeName",
		"data-sitebrush-blank-embedded-document",
		"element.style.pointerEvents = \"none\"",
		"innerHTML",
		"MutationObserver",
		"about:blank",
	} {
		if !strings.Contains(importedHTML, expectedFragment) {
			t.Fatalf("runtime guard missing expected fragment %q: %s", expectedFragment, importedHTML)
		}
	}
}

func TestImportedPageRuntimeGuardIsNotInjectedTwice(t *testing.T) {
	sourceHTML := `<html><head>` + importedPageRuntimeGuardScript + `</head><body>Imported</body></html>`
	importedHTML := injectImportedPageRuntimeGuard(sourceHTML)

	if guardCount := strings.Count(importedHTML, "<script data-sitebrush-import-runtime-guard>"); guardCount != 1 {
		t.Fatalf("runtime guard count = %d, want 1: %s", guardCount, importedHTML)
	}
}

func TestMirrorRemotePageRemovesLegacySiteBrushMenuChrome(t *testing.T) {
	pageRawURL := "https://legacy.example/rotorway4"
	sourceHTML := `<!DOCTYPE html>
	<!-- Powered by SiteBrush | http://sitebrush.com/ -->
<html lang="en" id="SiteBrush">
<head>
<script src="https://legacy.example/p/js/jquery/jquery.js" type="text/javascript"></script>
<script type="text/javascript">
$.fn.contextMenu = function(id, options) { return this; };
jQuery(function($) { $('div.contextMenu').hide(); });
</script>
<style type="text/css">
.SiteBrushContextMenu { font-size:14px; }
.ContextMenuCopyright { font-size:10px; }
.SiteBrushMenu { visibility:hidden; }
</style>
<script type="text/javascript">
jQuery(document).ready(function($) { $('#SiteBrush').contextMenu('SiteBrushMenu', {}); });
</script>
</head>
<body>
<div style="visibility:hidden" class="contextMenu SiteBrushMenu" id="SiteBrushMenu">
<ul>
<li id="close" class="SiteBrushContextMenu">&nbsp;<img src="https://legacy.example/p/static/lock.png" /> <a href="https://legacy.example/rotorway4?login" class="SiteBrushContextMenu">Войти</a></li>
<li class="SiteBrushContextMenu ContextMenuCopyright"><a href="http://sitebrush.com" class="SiteBrushContextMenu ContextMenuCopyright">sitebrush</a></li>
</ul>
</div>
<main><img src="https://legacy.example/content.jpg"><script>$(function(){ window.pageReady = true; });</script></main>
</body></html>`

	previousGrabHTTPClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: fakeGrabTransport{responses: map[string]fakeGrabResponse{
			"https://legacy.example/p/js/jquery/jquery.js": {contentType: "application/javascript", body: `window.jQuery = function(){}; window.$ = window.jQuery;`},
			"https://legacy.example/content.jpg":           {contentType: "image/jpeg", body: "image"},
		}}}
	}
	defer func() {
		newGrabHTTPClient = previousGrabHTTPClient
	}()

	pageURL, parseErr := url.Parse(pageRawURL)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	previewResources := previewGrabResources(pageURL, sourceHTML, nil, "", "")
	if len(previewResources) != 2 {
		t.Fatalf("expected only content resources after legacy menu cleanup, got %d: %#v", len(previewResources), previewResources)
	}
	for _, previewResource := range previewResources {
		if strings.Contains(previewResource.URL, "lock.png") || strings.Contains(previewResource.URL, "?login") {
			t.Fatalf("legacy sitebrush menu resource leaked into preview: %#v", previewResources)
		}
	}

	selectedResourceURLs := make(map[string]struct{}, len(previewResources))
	for _, previewResource := range previewResources {
		selectedResourceURLs[previewResource.URL] = struct{}{}
	}
	application, rawDB := newTestApplication(t)
	importedHTML := application.mirrorRemotePage("localhost", "/rotorway4", pageRawURL, pageURL, sourceHTML, "", selectedResourceURLs, "")
	for _, forbiddenFragment := range []string{"Powered by SiteBrush", `id="SiteBrushMenu"`, "SiteBrushContextMenu", "ContextMenuCopyright", "rotorway4?login", "jqContextMenu", "$.fn.contextMenu"} {
		if strings.Contains(importedHTML, forbiddenFragment) {
			t.Fatalf("legacy sitebrush chrome fragment %q remained in imported HTML: %s", forbiddenFragment, importedHTML)
		}
	}
	for _, expectedFragment := range []string{"/p/", "window.pageReady = true"} {
		if !strings.Contains(importedHTML, expectedFragment) {
			t.Fatalf("imported HTML missing expected fragment %q: %s", expectedFragment, importedHTML)
		}
	}

	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/rotorway4", "/rotorway4", importedHTML)
	if err != nil {
		t.Fatalf("insert imported page: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/rotorway4", nil)
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("serve imported page status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "SiteBrushMenuBox") {
		t.Fatalf("served imported page does not contain current sitebrush menu: %s", body)
	}
	if strings.Contains(body, `id="SiteBrushMenu"`) || strings.Contains(body, "rotorway4?login") {
		t.Fatalf("served imported page still exposes legacy sitebrush menu: %s", body)
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

func TestLogoutRedirectsToSameURIWithoutLogoutFlag(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	testCases := map[string]string{
		"http://localhost:8080/docs?logout":                "/docs",
		"http://localhost:8080/docs?logout=&files=":        "/docs?files=",
		"http://localhost:8080/overmobile/doc?a=1&logout=": "/overmobile/doc?a=1",
		"http://localhost:8080/?logout=&settings=&x=1":     "/?settings=&x=1",
	}
	for requestURL, expectedLocation := range testCases {
		request := httptest.NewRequest(http.MethodGet, requestURL, nil)
		request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
		response := httptest.NewRecorder()
		application.route(response, request)
		if response.Code != http.StatusFound {
			t.Fatalf("logout status for %s = %d, body=%q", requestURL, response.Code, response.Body.String())
		}
		if location := response.Header().Get("Location"); location != expectedLocation {
			t.Fatalf("logout location for %s = %q, want %q", requestURL, location, expectedLocation)
		}
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
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing page status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `name="source_ip"`) {
		t.Fatalf("missing page import form does not include source_ip field: %s", body)
	}
	if !strings.Contains(body, `name="copy_whole_site"`) {
		t.Fatalf("missing page import form does not include whole-site checkbox: %s", body)
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

func TestFilesPageUploadButtonOpensPickerAndSelectedFilesUploadImmediately(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/docs?files", nil)
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("files status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	expectedSnippets := []string{
		`const currentFilesPath = "/docs";`,
		"requestFileSelection();",
		"uploadInputElement.addEventListener('change'",
		"uploadSelectedFiles(Array.from(uploadInputElement.files));",
		"request.open('POST', currentFilesPath + '?files');",
		"currentFilesPath + '?native_pick_files'",
	}
	for _, expectedSnippet := range expectedSnippets {
		if !strings.Contains(body, expectedSnippet) {
			t.Fatalf("files page does not contain %q", expectedSnippet)
		}
	}
	if strings.Contains(body, "new FormData(uploadFormElement)") {
		t.Fatalf("files page still posts the whole form instead of selected file list")
	}
}

func TestUploadFilesRejectsWhenDomainStorageLimitIsExceeded(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT OR REPLACE INTO domain_storage_usage(domain,page_bytes,published_page_bytes,revision_bytes,file_bytes,published_static_bytes,limit_bytes,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		"localhost", 0, 0, 0, 0, 0, 5, time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("seed storage usage: %v", err)
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
	if response.Code != http.StatusInsufficientStorage {
		t.Fatalf("upload status = %d, body=%q", response.Code, response.Body.String())
	}
	if _, statErr := os.Stat(filepath.Join(application.domainFilesDirForDomain("localhost"), "manual.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("file was stored despite storage limit: %v", statErr)
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

func TestReplaceTemplateBlocksHandlesNestedTemplateMatches(t *testing.T) {
	sourceHTML := `<html><body><div class="SiteBrush-Template outer"><section>before<div class="SiteBrush-Template inner">new inner</div>after</section></div></body></html>`
	targetHTML := `<html><body><div class="SiteBrush-Template outer"><section>before<div class="SiteBrush-Template inner">old inner</div>after</section></div><p>tail</p></body></html>`

	updatedHTML, changed := replaceTemplateBlocks(targetHTML, extractTemplateBlocks(sourceHTML))
	if !changed {
		t.Fatal("changed = false, want true")
	}

	expectedHTML := `<html><body><div class="SiteBrush-Template outer"><section>before<div class="SiteBrush-Template inner">new inner</div>after</section></div><p>tail</p></body></html>`
	if updatedHTML != expectedHTML {
		t.Fatalf("updated html = %q, want %q", updatedHTML, expectedHTML)
	}
}

func TestMovedPageRedirectsFromOldPathToNewPath(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/contacts", "Contacts", "<h1>Contacts</h1>")
	if err != nil {
		t.Fatalf("insert page: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, "localhost", "/contacts", "Contacts", "<h1>Contacts</h1>")
	if err != nil {
		t.Fatalf("insert published page: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO revisions(domain,page_path,html,created_at,is_active) VALUES(?,?,?,?,1)`, "localhost", "/contacts", "<h1>Contacts</h1>", time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("insert revision: %v", err)
	}

	saveForm := url.Values{}
	saveForm.Set("path", "/address")
	saveForm.Set("previous_path", "/contacts")
	saveForm.Set("title", "Address")
	saveForm.Set("html", "<h1>Address</h1>")
	saveRequest := httptest.NewRequest(http.MethodPost, "http://localhost:8080/address?save", strings.NewReader(saveForm.Encode()))
	saveRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	saveRequest.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	saveResponse := httptest.NewRecorder()
	application.route(saveResponse, saveRequest)
	if saveResponse.Code != http.StatusFound {
		t.Fatalf("save status = %d, body=%q", saveResponse.Code, saveResponse.Body.String())
	}

	oldPathRequest := httptest.NewRequest(http.MethodGet, "http://localhost:8080/contacts", nil)
	oldPathResponse := httptest.NewRecorder()
	application.route(oldPathResponse, oldPathRequest)
	if oldPathResponse.Code != http.StatusMovedPermanently {
		t.Fatalf("old path status = %d, body=%q", oldPathResponse.Code, oldPathResponse.Body.String())
	}
	if location := oldPathResponse.Header().Get("Location"); location != "/address" {
		t.Fatalf("old path location = %q, want %q", location, "/address")
	}
}

func TestMissingPageReturns404ForGuest(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/missing", nil)
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "создайте эту страницу") {
		t.Fatalf("missing page did not offer create action: %s", body)
	}
	if strings.Contains(body, `method="post" action="/missing?grab" data-grab-form`) {
		t.Fatalf("guest missing page unexpectedly offers copy form: %s", body)
	}
}

func TestMissingPageReturns404ForAdminWithCopyOption(t *testing.T) {
	application, rawDB := newTestApplication(t)
	_, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/missing", nil)
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `method="post" action="/missing?grab" data-grab-form`) {
		t.Fatalf("admin missing page does not offer copy form: %s", body)
	}
	if !strings.Contains(body, `name="copy_whole_site"`) {
		t.Fatalf("admin missing page does not offer whole-site copy option: %s", body)
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

func TestStaticArchivePathForURI(t *testing.T) {
	testCases := []struct {
		pageURI          string
		expectedFilePath string
	}{
		{pageURI: "/", expectedFilePath: "index.html"},
		{pageURI: "/catalog/product", expectedFilePath: "catalog/product/index.html"},
		{pageURI: "/blog/2024/post", expectedFilePath: "blog/2024/post/index.html"},
		{pageURI: "/assets/style.css", expectedFilePath: "assets/style.css"},
		{pageURI: "/page.html", expectedFilePath: "page.html"},
	}
	for _, testCase := range testCases {
		actualFilePath := staticArchivePathForURI(testCase.pageURI)
		if actualFilePath != testCase.expectedFilePath {
			t.Fatalf("staticArchivePathForURI(%q) = %q, want %q", testCase.pageURI, actualFilePath, testCase.expectedFilePath)
		}
	}
}

func TestBackupExportWritesStaticStructureFromURI(t *testing.T) {
	application, rawDB := newTestApplication(t)
	homeHTML := `<h1>home</h1><a href="/catalog/product">Product</a><a href="/page.html?view=1#top">Page</a><a href="http://localhost/blog/2024/post">Same host</a><img src="/p/assets/logo.png"><img srcset="/p/assets/logo.png 1x, /p/assets/logo@2x.png 2x"><link rel="stylesheet" href="/p/assets/style.css"><script src="/p/assets/app.js"></script><a href="https://external.test/path">External</a>`
	productRevisionHTML := `<h1>revision</h1><a href="/">Home</a><a href="/blog/2024/post#read">Post</a><img src="/p/assets/logo.png">`
	_, err := rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/", "Home", homeHTML)
	if err != nil {
		t.Fatalf("insert /: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/catalog/product", "Product", "<h1>draft</h1>")
	if err != nil {
		t.Fatalf("insert /catalog/product: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/page.html", "Page", "<h1>page</h1>")
	if err != nil {
		t.Fatalf("insert /page.html: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", "/blog/2024/post", "Post", "<h1>post</h1>")
	if err != nil {
		t.Fatalf("insert /blog/2024/post: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO revisions(domain,page_path,html,created_at,is_active) VALUES(?,?,?,?,1)`, "localhost", "/catalog/product", productRevisionHTML, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("insert revision /catalog/product: %v", err)
	}

	domainFilesDir := application.domainFilesDirForDomain("localhost")
	if err := os.MkdirAll(filepath.Join(domainFilesDir, "assets"), 0o755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	styleCSS := `@import url("/p/assets/fonts.css"); body{background:url("/p/assets/logo.png")} @font-face{src:url("/p/assets/font.woff2")}`
	if err := os.WriteFile(filepath.Join(domainFilesDir, "assets", "style.css"), []byte(styleCSS), 0o644); err != nil {
		t.Fatalf("write style.css: %v", err)
	}
	if err := os.WriteFile(filepath.Join(domainFilesDir, "assets", "fonts.css"), []byte(`@font-face{src:url("/p/assets/font.woff2")}`), 0o644); err != nil {
		t.Fatalf("write fonts.css: %v", err)
	}
	appJS := `const logoPath = "/p/assets/logo.png"; const productPath = "/catalog/product"; const externalPath = "https://external.test/app.js";`
	if err := os.WriteFile(filepath.Join(domainFilesDir, "assets", "app.js"), []byte(appJS), 0o644); err != nil {
		t.Fatalf("write app.js: %v", err)
	}
	for _, assetName := range []string{"logo.png", "logo@2x.png", "font.woff2"} {
		if err := os.WriteFile(filepath.Join(domainFilesDir, "assets", assetName), []byte(assetName), 0o644); err != nil {
			t.Fatalf("write %s: %v", assetName, err)
		}
	}

	var archiveBuffer bytes.Buffer
	if err := application.writeDomainBackupZIP(context.Background(), "localhost", &archiveBuffer); err != nil {
		t.Fatalf("writeDomainBackupZIP: %v", err)
	}
	archiveReader, err := zip.NewReader(bytes.NewReader(archiveBuffer.Bytes()), int64(archiveBuffer.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	archiveFileByName := make(map[string]*zip.File, len(archiveReader.File))
	for _, archiveFile := range archiveReader.File {
		archiveFileByName[archiveFile.Name] = archiveFile
	}
	for _, expectedFileName := range []string{
		"backup.json",
		"index.html",
		"catalog/product/index.html",
		"page.html",
		"blog/2024/post/index.html",
		"p/assets/style.css",
		"p/assets/fonts.css",
		"p/assets/app.js",
		"p/assets/logo.png",
	} {
		if _, exists := archiveFileByName[expectedFileName]; !exists {
			t.Fatalf("backup archive missing %q", expectedFileName)
		}
	}

	rootPageBody := readZipTextFile(t, archiveFileByName, "index.html")
	for _, expectedFragment := range []string{
		`href="catalog/product/index.html"`,
		`href="page.html?view=1#top"`,
		`href="blog/2024/post/index.html"`,
		`src="p/assets/logo.png"`,
		`srcset="p/assets/logo.png 1x, p/assets/logo@2x.png 2x"`,
		`href="p/assets/style.css"`,
		`src="p/assets/app.js"`,
		`href="https://external.test/path"`,
	} {
		if !strings.Contains(rootPageBody, expectedFragment) {
			t.Fatalf("index.html missing rewritten fragment %q in %q", expectedFragment, rootPageBody)
		}
	}

	productPageBody := readZipTextFile(t, archiveFileByName, "catalog/product/index.html")
	for _, expectedFragment := range []string{
		`<h1>revision</h1>`,
		`href="../../index.html"`,
		`href="../../blog/2024/post/index.html#read"`,
		`src="../../p/assets/logo.png"`,
	} {
		if !strings.Contains(productPageBody, expectedFragment) {
			t.Fatalf("catalog/product/index.html missing rewritten fragment %q in %q", expectedFragment, productPageBody)
		}
	}

	styleBody := readZipTextFile(t, archiveFileByName, "p/assets/style.css")
	for _, expectedFragment := range []string{
		`@import url("fonts.css")`,
		`url("logo.png")`,
		`url("font.woff2")`,
	} {
		if !strings.Contains(styleBody, expectedFragment) {
			t.Fatalf("p/assets/style.css missing rewritten fragment %q in %q", expectedFragment, styleBody)
		}
	}

	scriptBody := readZipTextFile(t, archiveFileByName, "p/assets/app.js")
	for _, expectedFragment := range []string{
		`const logoPath = "logo.png"`,
		`const productPath = "../../catalog/product/index.html"`,
		`const externalPath = "https://external.test/app.js"`,
	} {
		if !strings.Contains(scriptBody, expectedFragment) {
			t.Fatalf("p/assets/app.js missing rewritten fragment %q in %q", expectedFragment, scriptBody)
		}
	}
}

func readZipTextFile(t *testing.T, archiveFileByName map[string]*zip.File, fileName string) string {
	t.Helper()
	archiveFile, exists := archiveFileByName[fileName]
	if !exists {
		t.Fatalf("backup archive missing %s", fileName)
	}
	archiveFileReader, err := archiveFile.Open()
	if err != nil {
		t.Fatalf("open %s: %v", fileName, err)
	}
	fileBytes, readErr := io.ReadAll(archiveFileReader)
	_ = archiveFileReader.Close()
	if readErr != nil {
		t.Fatalf("read %s: %v", fileName, readErr)
	}
	return string(fileBytes)
}
