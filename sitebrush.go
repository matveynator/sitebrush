package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/net/html"
	"sitebrush/pkg/desktop"
)

//go:embed web/*
var embeddedWebFiles embed.FS
var translationCatalog = loadTranslationCatalog()

var CompileVersion = "dev"

const storageAppName = "sitebrush"
const defaultDBPath = "storage/db/sitebrush.db"
const grabResourceMaxDepth = 64

// App keeps only explicit dependencies to stay readable and easy to swap.
type App struct {
	db               *sql.DB
	storagePath      string
	nativeFileDialog bool
	grabTracker      *grabProgressTracker
}

type grabProgressEvent struct {
	Token                  string `json:"token"`
	Stage                  string `json:"stage"`
	FoundTotal             int    `json:"found_total"`
	DownloadedTotal        int    `json:"downloaded_total"`
	CurrentURL             string `json:"current_url"`
	CurrentPercent         int    `json:"current_percent"`
	CurrentDownloadedBytes int64  `json:"current_downloaded_bytes"`
	CurrentSizeBytes       int64  `json:"current_size_bytes"`
	CompletedPercent       int    `json:"completed_percent"`
}

type grabResourcePreview struct {
	URL       string `json:"url"`
	Kind      string `json:"kind"`
	SizeBytes int64  `json:"size_bytes"`
}

type grabPreviewResponse struct {
	SourceURL     string                `json:"source_url"`
	ResourceCount int                   `json:"resource_count"`
	Resources     []grabResourcePreview `json:"resources"`
}

type nativePickedFile struct {
	Name    string `json:"name"`
	Mime    string `json:"mime"`
	Content string `json:"content"`
}

type nativePickedFilesResponse struct {
	Files []nativePickedFile `json:"files"`
}

type Page struct {
	Domain           string
	Path             string
	Title            string
	HTML             string
	ContentKind      string
	Published        int
	NativeFileDialog bool
}

type Revision struct {
	ID        int
	PagePath  string
	HTML      string
	CreatedAt string
	IsActive  int
}

type ManagedFile struct {
	Name          string
	Size          int64
	SizeLabel     string
	AssetPath     string
	PagePath      string
	MimeType      string
	Extension     string
	CreatedAt     string
	CreatedUnix   int64
	IsImage       bool
	AccessMode    string
	Token         string
	ExpiresAt     string
	SingleUseLeft int
	DownloadCount int64
	TokenUseCount int64
}

type DomainAlias struct {
	Domain            string
	VerificationToken string
	TXTVerified       bool
	ARecordVerified   bool
	IsActive          bool
	IsSelected        bool
	LastCheckedAt     string
}

type ManagedFileAccess struct {
	AccessMode    string
	Token         string
	ExpiresAt     string
	SingleUseLeft int
	TokenUseCount int64
}

type fileMetadata struct {
	PagePath      string
	Size          int64
	MimeType      string
	CreatedAt     string
	DownloadCount int64
}

type statusCapturingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (writer *statusCapturingResponseWriter) WriteHeader(statusCode int) {
	writer.statusCode = statusCode
	writer.ResponseWriter.WriteHeader(statusCode)
}

func (writer *statusCapturingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := writer.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijack is not supported")
	}
	return hijacker.Hijack()
}

func (writer *statusCapturingResponseWriter) Flush() {
	flusher, ok := writer.ResponseWriter.(http.Flusher)
	if ok {
		flusher.Flush()
	}
}

func accessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const (
			colorGreen  = "\033[32m"
			colorBlue   = "\033[34m"
			colorYellow = "\033[33m"
			colorReset  = "\033[0m"
		)
		startedAt := time.Now()
		writer := &statusCapturingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(writer, r)
		contentSource := writer.Header().Get("X-Sitebrush-Source")
		logType := "REQUEST"
		logColor := colorYellow
		if contentSource == "static" {
			logType = "STATIC"
			logColor = colorGreen
		}
		if contentSource == "dynamic" {
			logType = "DYNAMIC"
			logColor = colorBlue
		}
		if contentSource == "" && isLikelyStaticAssetPath(r.URL.Path) {
			logType = "STATIC"
			logColor = colorGreen
		}
		if strings.TrimSpace(r.URL.RawQuery) == "" {
			log.Printf("%s%s%s method=%s path=%s status=%d remote=%s duration=%s", logColor, logType, colorReset, r.Method, r.URL.Path, writer.statusCode, r.RemoteAddr, time.Since(startedAt).String())
			return
		}
		log.Printf("%s%s%s method=%s path=%s query=%s status=%d remote=%s duration=%s", logColor, logType, colorReset, r.Method, r.URL.Path, r.URL.RawQuery, writer.statusCode, r.RemoteAddr, time.Since(startedAt).String())
	})
}

func isLikelyStaticAssetPath(requestPath string) bool {
	if strings.HasPrefix(requestPath, "/p/static/") || strings.HasPrefix(requestPath, "/p/") {
		return true
	}
	fileExtension := strings.ToLower(path.Ext(requestPath))
	switch fileExtension {
	case ".css", ".js", ".mjs", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp", ".woff", ".woff2", ".ttf", ".eot", ".map":
		return true
	default:
		return false
	}
}

func flagWasProvided(flagName string) bool {
	provided := false
	flag.Visit(func(currentFlag *flag.Flag) {
		if currentFlag.Name == flagName {
			provided = true
		}
	})
	return provided
}

func cleanStoragePath(storagePath string) string {
	trimmedStoragePath := strings.TrimSpace(storagePath)
	if trimmedStoragePath == "" {
		return defaultAppStoragePath()
	}
	return filepath.Clean(trimmedStoragePath)
}

func cleanDBPath(dbPath string) string {
	trimmedDBPath := strings.TrimSpace(dbPath)
	if trimmedDBPath == "" {
		return defaultDBPath
	}
	return filepath.Clean(trimmedDBPath)
}

func ensureParentDir(filePath string) error {
	parentDir := filepath.Dir(filePath)
	if parentDir == "." || parentDir == "" {
		return nil
	}
	return os.MkdirAll(parentDir, 0o755)
}

func main() {
	port := flag.Int("port", 80, "HTTP listen port")
	dbType := flag.String("db-type", "sqlite", "database driver (supported: sqlite)")
	storagePath := flag.String("storage-path", defaultAppStoragePath(), "path to the Sitebrush app data directory (defaults to the OS user data folder)")
	dbPath := flag.String("db-path", defaultDBPath, "path to sqlite database file")
	desktopMode := flag.Bool("desktop", desktop.DefaultEnabled(), "enable desktop mode when desktop build tags are used")
	setupMode := flag.Bool("setup", false, "run interactive Linux setup wizard mode")
	flag.Parse()

	if *dbType != "sqlite" {
		log.Fatalf("unsupported -db-type %q, supported: sqlite", *dbType)
	}
	if *setupMode {
		log.Printf("setup mode requested; run the setup wizard build flow for Linux deployment")
	}

	effectiveStoragePath := cleanStoragePath(*storagePath)
	effectiveDBPath := cleanDBPath(*dbPath)
	if !flagWasProvided("db-path") {
		effectiveDBPath = filepath.Join(effectiveStoragePath, defaultDBPath)
	}
	if err := ensureParentDir(effectiveDBPath); err != nil {
		log.Fatal(err)
	}

	database, err := sql.Open("sqlite3", "file:"+effectiveDBPath)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()

	application := &App{db: database, storagePath: effectiveStoragePath, nativeFileDialog: desktop.NativeFileDialogSupported(), grabTracker: newGrabProgressTracker()}
	if err = application.migrate(context.Background()); err != nil {
		log.Fatal(err)
	}

	router := http.NewServeMux()
	staticFiles, err := fs.Sub(embeddedWebFiles, "web/static")
	if err != nil {
		log.Fatal(err)
	}
	router.Handle("/p/static/", http.StripPrefix("/p/static/", http.FileServer(http.FS(staticFiles))))
	router.HandleFunc("/p/", application.servePublicAsset)
	router.HandleFunc("/", application.route)

	listener, listenPort, err := listenOnAvailablePort(*port)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()

	address := "localhost:" + strconv.Itoa(listenPort)
	log.Printf("Sitebrush started on http://%s", address)

	httpHandler := accessLogMiddleware(router)
	if listenPort != 80 {
		log.Printf("Let’s Encrypt HTTP-01 checks need public port 80; current HTTP port is %d", listenPort)
	}
	certificateCacheDir := filepath.Join(application.storageRootDir(), "letsencrypt")
	if mkdirErr := os.MkdirAll(certificateCacheDir, 0o755); mkdirErr != nil {
		log.Printf("failed to create certificate cache: %v", mkdirErr)
	} else {
		certificateManager := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			Cache:      autocert.DirCache(certificateCacheDir),
			HostPolicy: application.autoCertHostPolicy,
		}
		httpHandler = certificateManager.HTTPHandler(httpHandler)
		go serveTLSWithAutoCert(certificateManager, httpHandler)
	}

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- http.Serve(listener, httpHandler)
	}()

	if *desktopMode {
		if err := desktop.RunWebviewWindow(address, CompileVersion); err != nil {
			log.Fatal(err)
		}
		return
	}

	if err := <-serverErrors; err != nil {
		log.Fatal(err)
	}
}

func listenOnAvailablePort(requestedPort int) (net.Listener, int, error) {
	listener, err := net.Listen("tcp", ":"+strconv.Itoa(requestedPort))
	if err == nil {
		return listener, requestedPort, nil
	}
	for fallbackPort := 9898; fallbackPort < 65536; fallbackPort++ {
		listener, err = net.Listen("tcp", ":"+strconv.Itoa(fallbackPort))
		if err == nil {
			log.Printf("port %d is unavailable, using %d", requestedPort, fallbackPort)
			return listener, fallbackPort, nil
		}
	}
	return nil, 0, fmt.Errorf("no available HTTP port after %d: %w", requestedPort, err)
}

func serveTLSWithAutoCert(certificateManager *autocert.Manager, handler http.Handler) {
	tlsListener, err := net.Listen("tcp", ":443")
	if err != nil {
		log.Printf("HTTPS disabled: cannot listen on port 443: %v", err)
		return
	}
	tlsServer := &http.Server{
		Handler:   handler,
		TLSConfig: certificateManager.TLSConfig(),
	}
	log.Printf("Sitebrush HTTPS enabled on port 443 for selected active aliases")
	if serveErr := tlsServer.Serve(tlsListener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		log.Printf("HTTPS server stopped: %v", serveErr)
	}
}

func (a *App) migrate(ctx context.Context) error {
	const legacyDomain = "localhost"
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users(id INTEGER PRIMARY KEY AUTOINCREMENT,domain TEXT,email TEXT,password TEXT,is_admin INTEGER,UNIQUE(domain,email));`,
		`CREATE TABLE IF NOT EXISTS sessions(token TEXT PRIMARY KEY,user_email TEXT,created_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS pages(domain TEXT,path TEXT,title TEXT,html TEXT,published INTEGER,PRIMARY KEY(domain,path));`,
		`CREATE TABLE IF NOT EXISTS published_pages(domain TEXT,path TEXT,title TEXT,html TEXT,PRIMARY KEY(domain,path));`,
		`CREATE TABLE IF NOT EXISTS revisions(id INTEGER PRIMARY KEY AUTOINCREMENT,domain TEXT,page_path TEXT,html TEXT,created_at TEXT,is_active INTEGER DEFAULT 1);`,
		`CREATE TABLE IF NOT EXISTS domain_aliases(primary_domain TEXT,alias_domain TEXT UNIQUE,verification_token TEXT,is_verified INTEGER DEFAULT 0,dns_a_ok INTEGER DEFAULT 0,is_selected INTEGER DEFAULT 0,last_checked_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS domain_states(domain TEXT PRIMARY KEY,is_frozen INTEGER DEFAULT 0);`,
		`CREATE TABLE IF NOT EXISTS file_access_rules(domain TEXT,file_name TEXT,access_mode TEXT,token TEXT,expires_at TEXT,single_use_left INTEGER DEFAULT 0,token_use_count INTEGER DEFAULT 0,PRIMARY KEY(domain,file_name));`,
		`CREATE TABLE IF NOT EXISTS file_metadata(domain TEXT,file_name TEXT,page_path TEXT,size INTEGER,mime_type TEXT,created_at TEXT,updated_at TEXT,source TEXT,download_count INTEGER DEFAULT 0,PRIMARY KEY(domain,file_name));`,
	}
	for _, query := range queries {
		if _, err := a.db.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE users ADD COLUMN domain TEXT`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE pages ADD COLUMN domain TEXT`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE revisions ADD COLUMN domain TEXT`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE revisions ADD COLUMN is_active INTEGER DEFAULT 1`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE domain_aliases ADD COLUMN verification_token TEXT`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE domain_aliases ADD COLUMN is_verified INTEGER DEFAULT 0`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE domain_aliases ADD COLUMN dns_a_ok INTEGER DEFAULT 0`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE domain_aliases ADD COLUMN is_selected INTEGER DEFAULT 0`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE domain_aliases ADD COLUMN last_checked_at TEXT`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE file_access_rules ADD COLUMN token_use_count INTEGER DEFAULT 0`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE file_metadata ADD COLUMN download_count INTEGER DEFAULT 0`)
	_, _ = a.db.ExecContext(ctx, `UPDATE users SET domain=? WHERE domain IS NULL OR TRIM(domain)=''`, legacyDomain)
	_, _ = a.db.ExecContext(ctx, `UPDATE pages SET domain=? WHERE domain IS NULL OR TRIM(domain)=''`, legacyDomain)
	_, _ = a.db.ExecContext(ctx, `UPDATE revisions SET domain=? WHERE domain IS NULL OR TRIM(domain)=''`, legacyDomain)
	a.migrateLoopbackDomainsToLocalhost(ctx)
	_, _ = a.db.ExecContext(ctx, `UPDATE revisions SET is_active=1 WHERE is_active IS NULL`)
	a.assignMissingDomainAliasTokens(ctx)
	_, _ = a.db.ExecContext(ctx, `
		INSERT INTO published_pages(domain,path,title,html)
		SELECT p.domain,p.path,p.title,p.html
		FROM pages AS p
		WHERE p.published=1
		AND NOT EXISTS (
			SELECT 1 FROM published_pages AS pp WHERE pp.domain=p.domain AND pp.path=p.path
		)
	`)
	return nil
}

func (a *App) migrateLoopbackDomainsToLocalhost(ctx context.Context) {
	legacyLoopbackDomains := []string{"127.0.0.1", "::1", "[::1]"}
	placeholders := "(" + strings.TrimSuffix(strings.Repeat("?,", len(legacyLoopbackDomains)), ",") + ")"
	sqlArguments := make([]any, 0, len(legacyLoopbackDomains))
	for _, legacyLoopbackDomain := range legacyLoopbackDomains {
		sqlArguments = append(sqlArguments, legacyLoopbackDomain)
	}
	usersMergeQuery := `INSERT OR IGNORE INTO users(domain,email,password,is_admin)
SELECT 'localhost',email,password,is_admin FROM users WHERE domain IN ` + placeholders
	_, _ = a.db.ExecContext(ctx, usersMergeQuery, sqlArguments...)
	pagesMergeQuery := `INSERT OR IGNORE INTO pages(domain,path,title,html,published)
SELECT 'localhost',path,title,html,published FROM pages WHERE domain IN ` + placeholders
	_, _ = a.db.ExecContext(ctx, pagesMergeQuery, sqlArguments...)
	publishedPagesMergeQuery := `INSERT OR IGNORE INTO published_pages(domain,path,title,html)
SELECT 'localhost',path,title,html FROM published_pages WHERE domain IN ` + placeholders
	_, _ = a.db.ExecContext(ctx, publishedPagesMergeQuery, sqlArguments...)
	fileAccessRulesMergeQuery := `INSERT OR IGNORE INTO file_access_rules(domain,file_name,access_mode,token,expires_at,single_use_left,token_use_count)
SELECT 'localhost',file_name,access_mode,token,expires_at,single_use_left,token_use_count FROM file_access_rules WHERE domain IN ` + placeholders
	_, _ = a.db.ExecContext(ctx, fileAccessRulesMergeQuery, sqlArguments...)
	fileMetadataMergeQuery := `INSERT OR IGNORE INTO file_metadata(domain,file_name,page_path,size,mime_type,created_at,updated_at,source,download_count)
SELECT 'localhost',file_name,page_path,size,mime_type,created_at,updated_at,source,download_count FROM file_metadata WHERE domain IN ` + placeholders
	_, _ = a.db.ExecContext(ctx, fileMetadataMergeQuery, sqlArguments...)
	domainStatesMergeQuery := `INSERT OR IGNORE INTO domain_states(domain,is_frozen)
SELECT 'localhost',is_frozen FROM domain_states WHERE domain IN ` + placeholders
	_, _ = a.db.ExecContext(ctx, domainStatesMergeQuery, sqlArguments...)

	_, _ = a.db.ExecContext(ctx, `UPDATE revisions SET domain='localhost' WHERE domain IN `+placeholders, sqlArguments...)
	_, _ = a.db.ExecContext(ctx, `UPDATE domain_aliases SET primary_domain='localhost' WHERE primary_domain IN `+placeholders, sqlArguments...)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM domain_aliases WHERE alias_domain IN `+placeholders, sqlArguments...)

	_, _ = a.db.ExecContext(ctx, `DELETE FROM users WHERE domain IN `+placeholders, sqlArguments...)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM pages WHERE domain IN `+placeholders, sqlArguments...)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM published_pages WHERE domain IN `+placeholders, sqlArguments...)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM file_access_rules WHERE domain IN `+placeholders, sqlArguments...)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM file_metadata WHERE domain IN `+placeholders, sqlArguments...)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM domain_states WHERE domain IN `+placeholders, sqlArguments...)
}

func (a *App) autoCertHostPolicy(ctx context.Context, host string) error {
	aliasDomain := normalizeDomainName(host)
	if aliasDomain == "" {
		return fmt.Errorf("invalid certificate host %q", host)
	}
	var activeSelectedCount int
	err := a.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM domain_aliases WHERE alias_domain=? AND is_selected=1 AND is_verified=1 AND dns_a_ok=1`, aliasDomain).Scan(&activeSelectedCount)
	if err != nil {
		return err
	}
	if activeSelectedCount == 0 {
		return fmt.Errorf("certificate host %q is not a selected active alias", aliasDomain)
	}
	return nil
}

func (a *App) assignMissingDomainAliasTokens(ctx context.Context) {
	aliasRows, err := a.db.QueryContext(ctx, `SELECT alias_domain FROM domain_aliases WHERE verification_token IS NULL OR TRIM(verification_token)=''`)
	if err != nil {
		return
	}
	defer aliasRows.Close()
	for aliasRows.Next() {
		var aliasDomain string
		if scanErr := aliasRows.Scan(&aliasDomain); scanErr != nil {
			continue
		}
		_, _ = a.db.ExecContext(ctx, `UPDATE domain_aliases SET verification_token=? WHERE alias_domain=?`, randomAccessToken(), aliasDomain)
	}
}

func (a *App) route(w http.ResponseWriter, r *http.Request) {
	pagePath := r.URL.Path
	if a.isDomainPrefixedPublicAssetPath(r) {
		a.servePublicAsset(w, r)
		return
	}
	if hasQueryFlag(r, "save") {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		a.savePage(w, r)
		return
	}
	if hasQueryFlag(r, "grab_preview") {
		a.grabPreview(w, r)
		return
	}
	if hasQueryFlag(r, "grab_events") {
		a.grabProgressEvents(w, r)
		return
	}
	if hasQueryFlag(r, "grab_ws") {
		a.grabProgressWS(w, r)
		return
	}
	if hasQueryFlag(r, "revision_restore") {
		a.restoreRevision(w, r)
		return
	}
	if hasQueryFlag(r, "revision_delete") {
		a.deleteRevision(w, r)
		return
	}
	if strings.TrimSpace(r.URL.Query().Get("delete")) != "" {
		a.deleteRevisionByQuery(w, r)
		return
	}
	if hasQueryFlag(r, "revision_toggle") {
		a.toggleRevision(w, r)
		return
	}
	if hasQueryFlag(r, "tree") {
		a.siteTreeJSON(w, r)
		return
	}
	if hasQueryFlag(r, "native_pick_files") {
		a.nativePickedFilesJSON(w, r)
		return
	}
	if hasQueryFlag(r, "edit") {
		a.editModePage(w, r)
		return
	}
	if hasQueryFlag(r, "visual") {
		a.editPage(w, r)
		return
	}
	if hasQueryFlag(r, "text") {
		a.editRawPage(w, r)
		return
	}
	if hasQueryFlag(r, "editraw") {
		a.editRawPage(w, r)
		return
	}
	if hasQueryFlag(r, "settings") || hasQueryFlag(r, "properties") {
		a.domainSettingsPage(w, r)
		return
	}
	if hasQueryFlag(r, "profile") {
		a.profilePage(w, r)
		return
	}
	if hasQueryFlag(r, "freeze") {
		a.freezeDomain(w, r)
		return
	}
	if hasQueryFlag(r, "publish") {
		a.publishDomain(w, r)
		return
	}
	if hasQueryFlag(r, "publish_preview") {
		a.publishPreviewJSON(w, r)
		return
	}
	if hasQueryFlag(r, "files") {
		a.filesPage(w, r)
		return
	}
	if hasQueryFlag(r, "revisions") {
		a.revisionsPage(w, r)
		return
	}
	if hasQueryFlag(r, "login") {
		a.login(w, r)
		return
	}
	if hasQueryFlag(r, "logout") {
		a.logout(w, r)
		return
	}
	if hasQueryFlag(r, "register") {
		a.registerPage(w, r)
		return
	}
	if hasQueryFlag(r, "grab") {
		if r.Method == http.MethodPost {
			a.grabPage(w, r)
			return
		}
		a.render(w, r, "missing.html", map[string]any{"Path": pagePath})
		return
	}
	if hasQueryFlag(r, "recover") {
		a.recoverPage(w, r)
		return
	}
	if hasQueryFlag(r, "captcha") {
		a.captchaImage(w, r)
		return
	}
	domain := a.siteDomain(r.Context(), r)
	if !a.hasAdmin(r.Context(), domain) && !hasQueryFlag(r, "register") {
		http.Redirect(w, r, r.URL.Path+"?register", http.StatusFound)
		return
	}
	isAdmin := a.isAdminRequest(r)
	pageRecord, err := a.findPage(r.Context(), domain, pagePath)
	if err == nil && isAdmin {
		a.serveManagedPageContent(w, r, pageRecord.Path, pageRecord.HTML, "db-draft")
		return
	}
	if !isAdmin && a.servePublishedStaticFile(w, r, domain, pagePath) {
		return
	}
	publishedPage, publishedErr := a.findPublishedPage(r.Context(), domain, pagePath)
	if publishedErr == nil {
		a.serveManagedPageContent(w, r, publishedPage.Path, publishedPage.HTML, "db-published-fallback")
		return
	}
	if !isAdmin {
		a.render(w, r, "missing.html", map[string]any{"Path": pagePath, "EditLink": pagePath + "?visual", "IsAdmin": false})
		return
	}
	if isAdmin {
		a.render(w, r, "missing.html", map[string]any{"Path": pagePath, "EditLink": pagePath + "?visual", "IsAdmin": true})
		return
	}
	http.NotFound(w, r)
}

func (a *App) setupAdmin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	domain := a.siteDomain(r.Context(), r)
	if a.hasAdmin(r.Context(), domain) {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	email := r.FormValue("email")
	password := r.FormValue("password")
	if email == "" || password == "" {
		http.Error(w, "email and password required", http.StatusBadRequest)
		return
	}
	_, err := a.db.ExecContext(r.Context(), `INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, domain, email, password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.createSession(w, r, email)
	returnPath := r.FormValue("return_path")
	if returnPath == "" {
		returnPath = requestedReturnPath(r)
	}
	http.Redirect(w, r, returnPath, http.StatusFound)
}

func (a *App) registerPage(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		a.setupAdmin(w, r)
		return
	}
	domain := a.siteDomain(r.Context(), r)
	if a.hasAdmin(r.Context(), domain) {
		http.Redirect(w, r, r.URL.Path+"?login", http.StatusFound)
		return
	}
	a.render(w, r, "setup.html", map[string]any{"Domain": domain})
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	domain := a.siteDomain(r.Context(), r)
	if !a.hasAdmin(r.Context(), domain) {
		http.Redirect(w, r, r.URL.Path+"?register", http.StatusFound)
		return
	}
	if r.Method == http.MethodGet {
		returnPath := loginReturnPathOrDefault(r)
		a.render(w, r, "login.html", map[string]any{"ReturnPath": returnPath, "Domain": domain})
		return
	}
	email := r.FormValue("email")
	password := r.FormValue("password")
	var matchedUsers int
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(1) FROM users WHERE domain=? AND email=? AND password=?`, domain, email, password).Scan(&matchedUsers)
	if matchedUsers == 0 {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	a.createSession(w, r, email)
	returnPath := strings.TrimSpace(r.FormValue("return_path"))
	if returnPath == "" {
		returnPath = loginReturnPathOrDefault(r)
	}
	http.Redirect(w, r, returnPath, http.StatusFound)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "sitebrush_session", Value: "", Path: "/", Expires: time.Unix(0, 0)})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (a *App) editPage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		if !a.hasAdmin(r.Context(), a.siteDomain(r.Context(), r)) {
			http.Redirect(w, r, r.URL.Path+"?register", http.StatusFound)
			return
		}
		http.Redirect(w, r, r.URL.Path+"?login", http.StatusFound)
		return
	}
	pagePath := cleanPath(r.URL.Query().Get("path"))
	if pagePath == "/" && strings.TrimSpace(r.URL.Query().Get("path")) == "" {
		pagePath = cleanPath(r.URL.Path)
	}
	if pagePath == "" {
		pagePath = r.URL.Path
	}
	if pagePath == "" {
		pagePath = "/"
	}
	domain := a.siteDomain(r.Context(), r)
	record, _ := a.findPage(r.Context(), domain, pagePath)
	if record.Path != "" && pageContentKind(record.Path, record.HTML) != "html" {
		http.Redirect(w, r, pagePath+"?text", http.StatusFound)
		return
	}
	if record.Path == "" && pageContentKind(pagePath, "") != "html" {
		http.Redirect(w, r, pagePath+"?text", http.StatusFound)
		return
	}
	if record.Path == "" {
		record = Page{Path: pagePath, Title: pagePath, HTML: a.defaultHTMLForNewPage(r.Context(), domain, pagePath)}
	}
	record.NativeFileDialog = a.nativeFileDialog
	a.render(w, r, "edit.html", record)
}

func (a *App) editModePage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		if !a.hasAdmin(r.Context(), a.siteDomain(r.Context(), r)) {
			http.Redirect(w, r, r.URL.Path+"?register", http.StatusFound)
			return
		}
		http.Redirect(w, r, r.URL.Path+"?login", http.StatusFound)
		return
	}
	pagePath := cleanPath(r.URL.Query().Get("path"))
	if pagePath == "/" && strings.TrimSpace(r.URL.Query().Get("path")) == "" {
		pagePath = cleanPath(r.URL.Path)
	}
	if pagePath == "" {
		pagePath = r.URL.Path
	}
	if pagePath == "" {
		pagePath = "/"
	}
	domain := a.siteDomain(r.Context(), r)
	record, _ := a.findPage(r.Context(), domain, pagePath)
	contentKind := pageContentKind(pagePath, "")
	if record.Path != "" {
		contentKind = pageContentKind(record.Path, record.HTML)
	}
	a.render(w, r, "edit_mode.html", map[string]any{"Path": pagePath, "ContentKind": contentKindLabel(contentKind), "IsHTML": contentKind == "html"})
}

func (a *App) editRawPage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		if !a.hasAdmin(r.Context(), a.siteDomain(r.Context(), r)) {
			http.Redirect(w, r, r.URL.Path+"?register", http.StatusFound)
			return
		}
		http.Redirect(w, r, r.URL.Path+"?login", http.StatusFound)
		return
	}
	pagePath := cleanPath(r.URL.Query().Get("path"))
	if pagePath == "/" && strings.TrimSpace(r.URL.Query().Get("path")) == "" {
		pagePath = cleanPath(r.URL.Path)
	}
	if pagePath == "" {
		pagePath = r.URL.Path
	}
	if pagePath == "" {
		pagePath = "/"
	}
	domain := a.siteDomain(r.Context(), r)
	record, _ := a.findPage(r.Context(), domain, pagePath)
	if record.Path == "" {
		record = Page{Path: pagePath, Title: pagePath, HTML: ""}
	}
	record.ContentKind = contentKindLabel(pageContentKind(record.Path, record.HTML))
	a.render(w, r, "edit_raw.html", record)
}

func (a *App) defaultHTMLForNewPage(ctx context.Context, domain, pagePath string) string {
	ancestorPath := parentPagePath(pagePath)
	for ancestorPath != "" {
		ancestorPage, err := a.findPage(ctx, domain, ancestorPath)
		if err == nil && ancestorPage.Path != "" && strings.TrimSpace(ancestorPage.HTML) != "" {
			return ancestorPage.HTML
		}
		ancestorPath = parentPagePath(ancestorPath)
	}
	return "<h1>New page</h1>"
}

func parentPagePath(pagePath string) string {
	if pagePath == "" || pagePath == "/" {
		return ""
	}
	if strings.HasSuffix(pagePath, "/") {
		trimmedPath := strings.TrimSuffix(pagePath, "/")
		if trimmedPath == "" {
			return "/"
		}
		return trimmedPath
	}
	lastSlashIndex := strings.LastIndex(pagePath, "/")
	if lastSlashIndex <= 0 {
		return "/"
	}
	return pagePath[:lastSlashIndex]
}

func cleanPath(rawPath string) string {
	trimmedPath := strings.TrimSpace(rawPath)
	if trimmedPath == "" {
		return "/"
	}
	if !strings.HasPrefix(trimmedPath, "/") {
		trimmedPath = "/" + trimmedPath
	}
	normalizedPath := path.Clean(trimmedPath)
	if normalizedPath == "." || normalizedPath == "" {
		return "/"
	}
	return normalizedPath
}

func loginReturnPathOrDefault(r *http.Request) string {
	returnPath := strings.TrimSpace(r.URL.Query().Get("return_path"))
	if returnPath != "" {
		return returnPath
	}
	if strings.TrimSpace(r.URL.RawQuery) == "login" {
		return r.URL.Path + "?visual"
	}
	refererURL, parseErr := url.Parse(strings.TrimSpace(r.Referer()))
	if parseErr == nil && refererURL.Path == r.URL.Path {
		refererMode := strings.TrimSpace(refererURL.RawQuery)
		if refererMode == "edit" || refererMode == "visual" || refererMode == "text" {
			return refererURL.Path + "?" + refererMode
		}
	}
	return requestedReturnPath(r)
}

func hasQueryFlag(r *http.Request, flagName string) bool {
	if strings.TrimSpace(r.URL.RawQuery) == flagName {
		return true
	}
	_, hasFlag := r.URL.Query()[flagName]
	return hasFlag
}

func (a *App) saveEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		a.savePage(w, r)
		return
	}
	a.route(w, r)
}

func (a *App) savePage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) || r.Method != http.MethodPost {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	pagePath := r.FormValue("path")
	domain := a.siteDomain(r.Context(), r)
	title := r.FormValue("title")
	html := r.FormValue("html")
	_, _ = a.db.ExecContext(r.Context(), `INSERT OR REPLACE INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, domain, pagePath, title, html)
	if !a.isDomainFrozen(r.Context(), domain) {
		_, _ = a.db.ExecContext(r.Context(), `INSERT OR REPLACE INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, domain, pagePath, title, html)
		a.writePublishedStaticHTML(domain, pagePath, html)
	}
	_, _ = a.db.ExecContext(r.Context(), `INSERT INTO revisions(domain,page_path,html,created_at) VALUES(?,?,?,?)`, domain, pagePath, html, time.Now().Format(time.RFC3339))
	if pageContentKind(pagePath, html) == "html" {
		a.applyTemplatePropagation(r.Context(), domain, html)
	}
	http.Redirect(w, r, pagePath, http.StatusFound)
}

func (a *App) grabPage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) || r.Method != http.MethodPost {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	pagePath := r.FormValue("path")
	if pagePath == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	sourceURL := r.FormValue("source_url")
	if sourceURL == "" {
		http.Error(w, "source_url is required", http.StatusBadRequest)
		return
	}
	sourceIP, err := parseOptionalGrabSourceIP(r.FormValue("source_ip"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	remoteSourceURL, err := parseGrabSourceURLForServerIP(sourceURL, sourceIP)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sourceURL = remoteSourceURL.String()

	htmlBytes, err := downloadGrabSourceHTML(sourceURL, sourceIP)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	domain := a.siteDomain(r.Context(), r)
	progressToken := strings.TrimSpace(r.FormValue("progress_token"))
	selectedResourceURLs := selectedGrabResourceURLs(r)
	html := a.mirrorRemotePage(domain, pagePath, sourceURL, remoteSourceURL, string(htmlBytes), progressToken, selectedResourceURLs, sourceIP)
	_, _ = a.db.ExecContext(r.Context(), `INSERT OR REPLACE INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, domain, pagePath, pagePath, html)
	if !a.isDomainFrozen(r.Context(), domain) {
		_, _ = a.db.ExecContext(r.Context(), `INSERT OR REPLACE INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, domain, pagePath, pagePath, html)
	}
	_, _ = a.db.ExecContext(r.Context(), `INSERT INTO revisions(domain,page_path,html,created_at) VALUES(?,?,?,?)`, domain, pagePath, html, time.Now().Format(time.RFC3339))
	if wantsJSONResponse(r) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"redirect": pagePath + "?visual"})
		return
	}
	http.Redirect(w, r, pagePath+"?visual", http.StatusFound)
}

func (a *App) grabPreview(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) || r.Method != http.MethodPost {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	sourceURL := r.FormValue("source_url")
	if sourceURL == "" {
		http.Error(w, "source_url is required", http.StatusBadRequest)
		return
	}
	sourceIP, err := parseOptionalGrabSourceIP(r.FormValue("source_ip"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	remoteSourceURL, err := parseGrabSourceURLForServerIP(sourceURL, sourceIP)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sourceURL = remoteSourceURL.String()
	htmlBytes, err := downloadGrabSourceHTML(sourceURL, sourceIP)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	resources := previewGrabResources(remoteSourceURL, string(htmlBytes), sourceIP)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(grabPreviewResponse{
		SourceURL:     remoteSourceURL.String(),
		ResourceCount: len(resources),
		Resources:     resources,
	})
}

func parseGrabSourceURL(sourceURL string) (*url.URL, error) {
	return parseGrabSourceURLForServerIP(sourceURL, "")
}

func parseGrabSourceURLForServerIP(sourceURL, sourceIP string) (*url.URL, error) {
	trimmedSourceURL := strings.TrimSpace(sourceURL)
	if trimmedSourceURL == "" {
		return nil, errors.New("source_url is required")
	}
	if strings.HasPrefix(trimmedSourceURL, "//") {
		trimmedSourceURL = "https:" + trimmedSourceURL
	}
	if !strings.Contains(trimmedSourceURL, "://") {
		trimmedSourceURL = defaultGrabSchemeForServerIP(trimmedSourceURL, sourceIP) + "://" + trimmedSourceURL
	}
	remoteSourceURL, err := url.Parse(trimmedSourceURL)
	if err != nil || remoteSourceURL.Hostname() == "" || (remoteSourceURL.Scheme != "http" && remoteSourceURL.Scheme != "https") {
		return nil, errors.New("source_url is invalid")
	}
	return remoteSourceURL, nil
}

func defaultGrabScheme(sourceURL string) string {
	return defaultGrabSchemeForServerIP(sourceURL, "")
}

func defaultGrabSchemeForServerIP(sourceURL, sourceIP string) string {
	if strings.TrimSpace(sourceIP) != "" {
		return "http"
	}
	hostCandidate := sourceURL
	if slashIndex := strings.Index(hostCandidate, "/"); slashIndex >= 0 {
		hostCandidate = hostCandidate[:slashIndex]
	}
	if colonIndex := strings.LastIndex(hostCandidate, ":"); colonIndex >= 0 {
		hostCandidate = hostCandidate[:colonIndex]
	}
	if hostCandidate == "localhost" || net.ParseIP(hostCandidate) != nil {
		return "http"
	}
	return "https"
}

func parseOptionalGrabSourceIP(rawSourceIP string) (string, error) {
	trimmedSourceIP := strings.TrimSpace(rawSourceIP)
	if trimmedSourceIP == "" {
		return "", nil
	}
	parsedIP := net.ParseIP(trimmedSourceIP)
	if parsedIP == nil {
		return "", errors.New("source_ip is invalid")
	}
	return parsedIP.String(), nil
}

func wantsJSONResponse(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/json")
}

func downloadGrabSourceHTML(sourceURL, sourceIP string) ([]byte, error) {
	remoteSourceURL, err := url.Parse(sourceURL)
	if err != nil {
		return nil, errors.New("source_url is invalid")
	}
	response, err := newGrabHTTPClientForServerIP(remoteSourceURL.Hostname(), sourceIP).Get(sourceURL)
	if err != nil {
		return nil, errors.New("failed to download source page")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, errors.New("source page returned non-success status")
	}
	htmlBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, errors.New("failed to read source page")
	}
	return htmlBytes, nil
}

func selectedGrabResourceURLs(r *http.Request) map[string]struct{} {
	if strings.TrimSpace(r.FormValue("import_selection_confirmed")) == "" {
		return nil
	}
	selectedResourceURLs := make(map[string]struct{})
	for _, resourceURL := range r.Form["import_resource_url"] {
		trimmedResourceURL := strings.TrimSpace(resourceURL)
		if trimmedResourceURL == "" {
			continue
		}
		selectedResourceURLs[trimmedResourceURL] = struct{}{}
	}
	return selectedResourceURLs
}

func previewGrabResources(pageURL *url.URL, htmlSource, sourceIP string) []grabResourcePreview {
	spider := newPageSpider("", pageURL, grabResourceMaxDepth, nil, "", sourceIP)
	rootResource := &mirroredResource{url: pageURL.String(), content: []byte(htmlSource)}
	spider.resources[pageURL.String()] = rootResource
	spider.rewriteNestedResources(rootResource, 0, "text/html")
	resources := make([]grabResourcePreview, 0)
	for resourceURL, resource := range spider.resources {
		if resourceURL == pageURL.String() {
			continue
		}
		sizeBytes := int64(-1)
		if resource != nil && resource.content != nil {
			sizeBytes = int64(len(resource.content))
		}
		resources = append(resources, grabResourcePreview{URL: resourceURL, Kind: previewResourceKind("", "", resourceURL), SizeBytes: sizeBytes})
	}
	sort.Slice(resources, func(leftIndex, rightIndex int) bool {
		if resources[leftIndex].Kind == resources[rightIndex].Kind {
			return resources[leftIndex].URL < resources[rightIndex].URL
		}
		return resources[leftIndex].Kind < resources[rightIndex].Kind
	})
	return resources
}

func previewResourceKind(tagName, attributeName, rawRef string) string {
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
	extension := resourceExtension(rawRef)
	switch extension {
	case ".css":
		return "style"
	case ".js", ".mjs":
		return "script"
	case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".ico":
		return "image"
	case ".woff", ".woff2", ".ttf", ".eot", ".otf":
		return "font"
	case ".mp4", ".webm", ".mov":
		return "video"
	case ".mp3", ".ogg", ".wav":
		return "audio"
	default:
		return "file"
	}
}

func (a *App) grabProgressWS(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	progressToken := strings.TrimSpace(r.URL.Query().Get("token"))
	if progressToken == "" {
		http.Error(w, "token is required", http.StatusBadRequest)
		return
	}
	connection, err := upgradeToWebSocket(w, r)
	if err != nil {
		return
	}
	defer connection.Close()
	events := a.grabTracker.subscribe(progressToken)
	defer a.grabTracker.unsubscribe(progressToken, events)
	readyEventJSON, marshalReadyErr := json.Marshal(grabProgressEvent{Token: progressToken, Stage: "ready"})
	if marshalReadyErr != nil {
		return
	}
	if err := connection.WriteText(readyEventJSON); err != nil {
		return
	}
	for {
		event, isOpen := <-events
		if !isOpen {
			return
		}
		eventJSON, marshalErr := json.Marshal(event)
		if marshalErr != nil {
			return
		}
		if err := connection.WriteText(eventJSON); err != nil {
			return
		}
		if event.Stage == "done" || (event.Stage == "error" && strings.TrimSpace(event.CurrentURL) == "") {
			return
		}
	}
}

func (a *App) grabProgressEvents(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	progressToken := strings.TrimSpace(r.URL.Query().Get("token"))
	if progressToken == "" {
		http.Error(w, "token is required", http.StatusBadRequest)
		return
	}
	flusher, canFlush := w.(http.Flusher)
	if !canFlush {
		http.Error(w, "streaming is not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	events := a.grabTracker.subscribe(progressToken)
	defer a.grabTracker.unsubscribe(progressToken, events)
	if !writeGrabProgressEvent(w, flusher, grabProgressEvent{Token: progressToken, Stage: "ready"}) {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case event, isOpen := <-events:
			if !isOpen {
				return
			}
			if !writeGrabProgressEvent(w, flusher, event) {
				return
			}
			if event.Stage == "done" || (event.Stage == "error" && strings.TrimSpace(event.CurrentURL) == "") {
				return
			}
		}
	}
}

func writeGrabProgressEvent(w io.Writer, flusher http.Flusher, event grabProgressEvent) bool {
	eventJSON, marshalErr := json.Marshal(event)
	if marshalErr != nil {
		return false
	}
	if _, writeErr := fmt.Fprintf(w, "data: %s\n\n", eventJSON); writeErr != nil {
		return false
	}
	flusher.Flush()
	return true
}

func (a *App) revisionsPage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	pagePath := cleanPath(r.URL.Query().Get("path"))
	if pagePath == "/" && strings.TrimSpace(r.URL.Query().Get("path")) == "" {
		pagePath = cleanPath(r.URL.Path)
	}
	domain := a.siteDomain(r.Context(), r)
	revisionRows, err := a.db.QueryContext(r.Context(), `SELECT id,page_path,html,created_at,is_active FROM revisions WHERE domain=? AND page_path=? ORDER BY id DESC`, domain, pagePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer revisionRows.Close()
	var revisionList []Revision
	for revisionRows.Next() {
		var current Revision
		_ = revisionRows.Scan(&current.ID, &current.PagePath, &current.HTML, &current.CreatedAt, &current.IsActive)
		revisionList = append(revisionList, current)
	}
	returnPath := r.URL.Query().Get("return")
	if strings.TrimSpace(returnPath) == "" {
		returnPath = pagePath
	}
	a.render(w, r, "revisions.html", map[string]any{"Path": pagePath, "ReturnPath": returnPath, "Revisions": revisionList})
}

func (a *App) applyLatestActiveRevision(ctx context.Context, domain string, pagePath string) {
	var latestActiveHTML string
	err := a.db.QueryRowContext(ctx, `SELECT html FROM revisions WHERE domain=? AND page_path=? AND is_active=1 ORDER BY id DESC LIMIT 1`, domain, pagePath).Scan(&latestActiveHTML)
	if err != nil {
		a.removeManagedPage(ctx, domain, pagePath)
		return
	}
	pageTitle := pagePath
	_ = a.db.QueryRowContext(ctx, `SELECT title FROM pages WHERE domain=? AND path=?`, domain, pagePath).Scan(&pageTitle)
	_, _ = a.db.ExecContext(ctx, `INSERT OR REPLACE INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, domain, pagePath, pageTitle, latestActiveHTML)
	if !a.isDomainFrozen(ctx, domain) {
		_, _ = a.db.ExecContext(ctx, `INSERT OR REPLACE INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, domain, pagePath, pageTitle, latestActiveHTML)
		a.writePublishedStaticHTML(domain, pagePath, latestActiveHTML)
	}
}

func (a *App) removeManagedPage(ctx context.Context, domain string, pagePath string) {
	_, _ = a.db.ExecContext(ctx, `DELETE FROM pages WHERE domain=? AND path=?`, domain, pagePath)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM published_pages WHERE domain=? AND path=?`, domain, pagePath)
	a.removePublishedStaticFile(domain, pagePath)
}

func (a *App) restoreRevision(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) || r.Method != http.MethodPost {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	revisionID, _ := strconv.Atoi(r.FormValue("id"))
	var pagePath, html string
	domain := a.siteDomain(r.Context(), r)
	err := a.db.QueryRowContext(r.Context(), `SELECT page_path,html FROM revisions WHERE id=? AND domain=?`, revisionID, domain).Scan(&pagePath, &html)
	if err != nil {
		http.Error(w, "revision not found", http.StatusNotFound)
		return
	}
	_, _ = a.db.ExecContext(r.Context(), `UPDATE pages SET html=? WHERE domain=? AND path=?`, html, domain, pagePath)
	_, _ = a.db.ExecContext(r.Context(), `INSERT INTO revisions(domain,page_path,html,created_at) VALUES(?,?,?,?)`, domain, pagePath, html, time.Now().Format(time.RFC3339))
	a.applyLatestActiveRevision(r.Context(), domain, pagePath)
	http.Redirect(w, r, pagePath, http.StatusFound)
}

func (a *App) deleteRevision(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) || r.Method != http.MethodPost {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	revisionID, _ := strconv.Atoi(r.FormValue("id"))
	pagePath := r.FormValue("path")
	domain := a.siteDomain(r.Context(), r)
	_, _ = a.db.ExecContext(r.Context(), `UPDATE revisions SET is_active=0 WHERE id=? AND domain=?`, revisionID, domain)
	a.applyLatestActiveRevision(r.Context(), domain, pagePath)
	http.Redirect(w, r, pagePath+"?revisions", http.StatusFound)
}

func (a *App) deleteRevisionByQuery(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) || r.Method != http.MethodPost {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	revisionID, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("delete")))
	if revisionID <= 0 {
		http.Error(w, "revision not found", http.StatusNotFound)
		return
	}
	domain := a.siteDomain(r.Context(), r)
	var pagePath string
	err := a.db.QueryRowContext(r.Context(), `SELECT page_path FROM revisions WHERE id=? AND domain=?`, revisionID, domain).Scan(&pagePath)
	if err != nil {
		http.Error(w, "revision not found", http.StatusNotFound)
		return
	}
	_, _ = a.db.ExecContext(r.Context(), `UPDATE revisions SET is_active=0 WHERE id=? AND domain=?`, revisionID, domain)
	a.applyLatestActiveRevision(r.Context(), domain, pagePath)
	http.Redirect(w, r, pagePath, http.StatusFound)
}

func (a *App) toggleRevision(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) || r.Method != http.MethodPost {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	revisionID, _ := strconv.Atoi(r.FormValue("id"))
	pagePath := r.FormValue("path")
	domain := a.siteDomain(r.Context(), r)
	enableRevision := r.FormValue("enable") == "1"
	nextActiveState := 0
	if enableRevision {
		nextActiveState = 1
	}
	_, _ = a.db.ExecContext(r.Context(), `UPDATE revisions SET is_active=? WHERE id=? AND domain=?`, nextActiveState, revisionID, domain)
	a.applyLatestActiveRevision(r.Context(), domain, pagePath)
	http.Redirect(w, r, pagePath+"?revisions", http.StatusFound)
}

func (a *App) profilePage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		if !a.hasAdmin(r.Context(), a.siteDomain(r.Context(), r)) {
			http.Redirect(w, r, r.URL.Path+"?register", http.StatusFound)
			return
		}
		http.Redirect(w, r, r.URL.Path+"?login", http.StatusFound)
		return
	}
	currentEmail, found := a.currentAdminEmail(r)
	if !found {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	status := ""
	if r.Method == http.MethodPost {
		nextEmail := strings.TrimSpace(r.FormValue("email"))
		nextPassword := strings.TrimSpace(r.FormValue("password"))
		confirmPassword := strings.TrimSpace(r.FormValue("password_confirm"))
		switch {
		case nextEmail == "":
			status = "Email is required."
		case nextPassword != "" && nextPassword != confirmPassword:
			status = "Password confirmation does not match."
		default:
			domain := a.siteDomain(r.Context(), r)
			var updateErr error
			if nextPassword == "" {
				_, updateErr = a.db.ExecContext(r.Context(), `UPDATE users SET email=? WHERE domain=? AND email=?`, nextEmail, domain, currentEmail)
			} else {
				_, updateErr = a.db.ExecContext(r.Context(), `UPDATE users SET email=?,password=? WHERE domain=? AND email=?`, nextEmail, nextPassword, domain, currentEmail)
			}
			if updateErr != nil {
				status = updateErr.Error()
				break
			}
			a.createSession(w, r, nextEmail)
			currentEmail = nextEmail
			status = "Account updated."
		}
	}
	translations := translationsForRequest(r)
	a.render(w, r, "profile.html", map[string]any{
		"Email":                currentEmail,
		"Status":               status,
		"Title":                translationOrDefault(translations, "profile_title", "Account"),
		"EmailLabel":           translationOrDefault(translations, "profile_email", "Email"),
		"PasswordLabel":        translationOrDefault(translations, "profile_new_password", "New password"),
		"PasswordConfirmLabel": translationOrDefault(translations, "profile_confirm_password", "Confirm password"),
		"SaveLabel":            translationOrDefault(translations, "profile_save", "Save"),
	})
}

func (a *App) recoverPage(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		a.render(w, r, "recover.html", map[string]any{"Status": "", "ReturnPath": requestedReturnPath(r)})
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	captchaValue := strings.TrimSpace(r.FormValue("captcha"))
	captchaCookie, err := r.Cookie("sitebrush_captcha")
	if err != nil || captchaCookie.Value == "" || captchaCookie.Value != captchaValue {
		a.render(w, r, "recover.html", map[string]any{"Status": "Captcha is invalid", "ReturnPath": requestedReturnPath(r)})
		return
	}
	var userCount int
	domain := a.siteDomain(r.Context(), r)
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(1) FROM users WHERE domain=? AND email=? AND is_admin=1`, domain, email).Scan(&userCount)
	if userCount == 0 {
		http.Redirect(w, r, requestedReturnPath(r), http.StatusFound)
		return
	}
	recoveryCode := fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
	message := "Subject: SiteBrush recovery code\r\n\r\nRecovery code: " + recoveryCode + "\r\n"
	mailError := smtp.SendMail("127.0.0.1:25", nil, "noreply@localhost", []string{email}, []byte(message))
	if mailError != nil {
		a.render(w, r, "recover.html", map[string]any{"Status": "SMTP send failed: " + mailError.Error(), "ReturnPath": requestedReturnPath(r)})
		return
	}
	http.Redirect(w, r, requestedReturnPath(r), http.StatusFound)
}

func (a *App) captchaImage(w http.ResponseWriter, r *http.Request) {
	captchaCode := fmt.Sprintf("%04d", time.Now().UnixNano()%10000)
	http.SetCookie(w, &http.Cookie{Name: "sitebrush_captcha", Value: captchaCode, Path: "/", HttpOnly: true, MaxAge: 300})
	w.Header().Set("Content-Type", "image/svg+xml")
	_, _ = w.Write([]byte("<svg xmlns='http://www.w3.org/2000/svg' width='140' height='40'><rect width='100%' height='100%' fill='#f4f4f4'/><text x='15' y='28' font-size='24' font-family='monospace' fill='#333'>" + captchaCode + "</text></svg>"))
}

func (a *App) filesPage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	currentPath := currentFilesPath(r)
	if r.Method == http.MethodPost {
		fileName := safeRelativeAssetPath(r.FormValue("name"))
		action := r.FormValue("action")
		if action == "upload" {
			a.uploadFiles(w, r, currentPath)
			return
		}
		if action == "save_access" && fileName != "" {
			a.saveFileAccessRule(r.Context(), r, fileName)
		} else if fileName != "" {
			_ = os.Remove(filepath.Join(a.domainFilesDir(r), fileName))
			_, _ = a.db.ExecContext(r.Context(), `DELETE FROM file_access_rules WHERE domain=? AND file_name=?`, domainStorageName(a.siteDomain(r.Context(), r)), fileName)
			_, _ = a.db.ExecContext(r.Context(), `DELETE FROM file_metadata WHERE domain=? AND file_name=?`, domainStorageName(a.siteDomain(r.Context(), r)), fileName)
		}
		http.Redirect(w, r, currentPath+"?files", http.StatusFound)
		return
	}
	fileList, listErr := a.listManagedFiles(r.Context(), r, currentPath)
	if listErr != nil {
		a.render(w, r, "files.html", map[string]any{"Path": currentPath, "Files": []ManagedFile{}})
		return
	}
	accessRulesByName := a.fileAccessRulesByName(r.Context(), r)
	for index := range fileList {
		accessRule := accessRulesByName[fileList[index].Name]
		if strings.TrimSpace(accessRule.AccessMode) == "" {
			accessRule.AccessMode = "public"
		}
		fileList[index].AssetPath = "/p/" + fileList[index].Name
		fileList[index].AccessMode = accessRule.AccessMode
		fileList[index].Token = accessRule.Token
		fileList[index].ExpiresAt = accessRule.ExpiresAt
		fileList[index].SingleUseLeft = accessRule.SingleUseLeft
		fileList[index].TokenUseCount = accessRule.TokenUseCount
	}
	a.render(w, r, "files.html", map[string]any{"Path": currentPath, "Files": fileList})
}

func (a *App) nativePickedFilesJSON(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	selectedPaths, err := desktop.PickFiles()
	if err != nil {
		if errors.Is(err, desktop.ErrNativeDialogCanceled) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(nativePickedFilesResponse{Files: []nativePickedFile{}})
			return
		}
		http.Error(w, err.Error(), http.StatusNotImplemented)
		return
	}
	pickedFiles := make([]nativePickedFile, 0, len(selectedPaths))
	for _, selectedPath := range selectedPaths {
		fileInfo, statErr := os.Stat(selectedPath)
		if statErr != nil || fileInfo.IsDir() {
			continue
		}
		fileBytes, readErr := os.ReadFile(selectedPath)
		if readErr != nil {
			continue
		}
		mimeType := mime.TypeByExtension(path.Ext(selectedPath))
		if mimeType == "" {
			mimeType = http.DetectContentType(fileBytes)
		}
		pickedFiles = append(pickedFiles, nativePickedFile{
			Name:    filepath.Base(selectedPath),
			Mime:    mimeType,
			Content: base64.StdEncoding.EncodeToString(fileBytes),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(nativePickedFilesResponse{Files: pickedFiles})
}

func (a *App) servePublicAsset(w http.ResponseWriter, r *http.Request) {
	fileName := publicAssetFileNameFromPath(r.URL.Path, a.siteDomain(r.Context(), r))
	if fileName == "" {
		http.NotFound(w, r)
		return
	}
	domain := domainStorageName(a.siteDomain(r.Context(), r))
	a.serveAssetFile(w, r, domain, fileName)
}

func (a *App) listManagedFiles(ctx context.Context, r *http.Request, currentPath string) ([]ManagedFile, error) {
	fileList := make([]ManagedFile, 0, 32)
	rootPath := a.domainFilesDir(r)
	domain := domainStorageName(a.siteDomain(ctx, r))
	metadata := a.fileMetadataByName(ctx, domain)
	currentPath = cleanPath(currentPath)
	walkErr := filepath.WalkDir(rootPath, func(filePath string, currentEntry fs.DirEntry, entryErr error) error {
		if entryErr != nil || currentEntry.IsDir() {
			return nil
		}
		relativePath, relErr := filepath.Rel(rootPath, filePath)
		if relErr != nil {
			return nil
		}
		normalizedPath := filepath.ToSlash(relativePath)
		if safeRelativeAssetPath(normalizedPath) == "" {
			return nil
		}
		fileInfo, statErr := currentEntry.Info()
		if statErr != nil {
			return nil
		}
		meta := metadata[normalizedPath]
		if meta.PagePath == "" && currentPath != "/" {
			return nil
		}
		if meta.PagePath != "" && !filePathBelongsToBranch(meta.PagePath, currentPath) {
			return nil
		}
		size := fileInfo.Size()
		if meta.Size > 0 {
			size = meta.Size
		}
		createdUnix := fileInfo.ModTime().Unix()
		createdAt := fileInfo.ModTime().Format("2006-01-02 15:04")
		if meta.CreatedAt != "" {
			if parsedTime, parseErr := time.Parse(time.RFC3339, meta.CreatedAt); parseErr == nil {
				createdUnix = parsedTime.Unix()
				createdAt = parsedTime.Local().Format("2006-01-02 15:04")
			}
		}
		mimeType := meta.MimeType
		if mimeType == "" {
			mimeType = mime.TypeByExtension(path.Ext(normalizedPath))
		}
		fileList = append(fileList, ManagedFile{
			Name:          normalizedPath,
			Size:          size,
			SizeLabel:     formatFileSize(size),
			PagePath:      meta.PagePath,
			MimeType:      mimeType,
			Extension:     fileExtensionLabel(normalizedPath),
			CreatedAt:     createdAt,
			CreatedUnix:   createdUnix,
			IsImage:       isImageFile(normalizedPath, mimeType),
			DownloadCount: meta.DownloadCount,
		})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Slice(fileList, func(leftIndex, rightIndex int) bool {
		return fileList[leftIndex].CreatedUnix > fileList[rightIndex].CreatedUnix
	})
	return fileList, nil
}

func (a *App) uploadFiles(w http.ResponseWriter, r *http.Request, currentPath string) {
	if err := r.ParseMultipartForm(128 << 20); err != nil {
		http.Error(w, "failed to parse uploaded files", http.StatusBadRequest)
		return
	}
	if r.MultipartForm == nil || len(r.MultipartForm.File["upload_files"]) == 0 {
		http.Error(w, "no files selected", http.StatusBadRequest)
		return
	}

	domain := domainStorageName(a.siteDomain(r.Context(), r))
	baseDir := a.domainFilesDir(r)
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		http.Error(w, "failed to create files directory", http.StatusInternalServerError)
		return
	}

	uploadedNames := make([]string, 0, len(r.MultipartForm.File["upload_files"]))
	for _, fileHeader := range r.MultipartForm.File["upload_files"] {
		fileName := safeFileName(fileHeader.Filename)
		if fileName == "" {
			continue
		}
		sourceFile, openErr := fileHeader.Open()
		if openErr != nil {
			continue
		}

		storedName := uniqueUploadedFileName(baseDir, fileName)
		targetPath := filepath.Join(baseDir, storedName)
		targetFile, createErr := os.Create(targetPath)
		if createErr != nil {
			_ = sourceFile.Close()
			continue
		}
		writtenBytes, copyErr := io.Copy(targetFile, sourceFile)
		closeErr := targetFile.Close()
		_ = sourceFile.Close()
		if copyErr != nil || closeErr != nil {
			_ = os.Remove(targetPath)
			continue
		}

		mimeType := strings.TrimSpace(fileHeader.Header.Get("Content-Type"))
		if mimeType == "" {
			mimeType = mime.TypeByExtension(path.Ext(storedName))
		}
		a.upsertFileMetadata(r.Context(), domain, storedName, currentPath, writtenBytes, mimeType, "upload")
		uploadedNames = append(uploadedNames, storedName)
	}

	if wantsJSONResponse(r) {
		uploadedPaths := make([]string, 0, len(uploadedNames))
		for _, uploadedName := range uploadedNames {
			uploadedPaths = append(uploadedPaths, "/p/"+uploadedName)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"uploaded": len(uploadedNames), "files": uploadedNames, "paths": uploadedPaths, "redirect": currentPath + "?files"})
		return
	}
	http.Redirect(w, r, currentPath+"?files", http.StatusFound)
}

func uniqueUploadedFileName(baseDir, requestedName string) string {
	candidate := requestedName
	extension := path.Ext(requestedName)
	stem := strings.TrimSuffix(requestedName, extension)
	for index := 1; ; index++ {
		if _, err := os.Stat(filepath.Join(baseDir, candidate)); os.IsNotExist(err) {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d%s", stem, index, extension)
	}
}

func currentFilesPath(r *http.Request) string {
	currentPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if currentPath == "" {
		currentPath = r.URL.Path
	}
	return cleanPath(currentPath)
}

func (a *App) fileMetadataByName(ctx context.Context, domain string) map[string]fileMetadata {
	metadataByName := make(map[string]fileMetadata)
	if a == nil || a.db == nil {
		return metadataByName
	}
	rows, err := a.db.QueryContext(ctx, `SELECT file_name,page_path,size,mime_type,created_at,download_count FROM file_metadata WHERE domain=?`, domain)
	if err != nil {
		return metadataByName
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var meta fileMetadata
		if scanErr := rows.Scan(&name, &meta.PagePath, &meta.Size, &meta.MimeType, &meta.CreatedAt, &meta.DownloadCount); scanErr != nil {
			continue
		}
		if safeRelativeAssetPath(name) == "" {
			continue
		}
		meta.PagePath = cleanPath(meta.PagePath)
		metadataByName[name] = meta
	}
	return metadataByName
}

func (a *App) upsertFileMetadata(ctx context.Context, domain, fileName, pagePath string, size int64, mimeType, source string) {
	if a == nil || a.db == nil {
		return
	}
	if safeRelativeAssetPath(fileName) == "" {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = a.db.ExecContext(ctx, `INSERT INTO file_metadata(domain,file_name,page_path,size,mime_type,created_at,updated_at,source,download_count)
VALUES(?,?,?,?,?,?,?,?,0)
ON CONFLICT(domain,file_name) DO UPDATE SET page_path=excluded.page_path,size=excluded.size,mime_type=excluded.mime_type,updated_at=excluded.updated_at,source=excluded.source`,
		domain, fileName, cleanPath(pagePath), size, mimeType, now, now, source)
}

func filePathBelongsToBranch(filePagePath, currentPath string) bool {
	filePagePath = cleanPath(filePagePath)
	currentPath = cleanPath(currentPath)
	if currentPath == "/" {
		return true
	}
	return filePagePath == currentPath || strings.HasPrefix(filePagePath, currentPath+"/")
}

func isImageFile(fileName, mimeType string) bool {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(mimeType)), "image/") {
		return true
	}
	switch strings.ToLower(path.Ext(fileName)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".ico", ".bmp":
		return true
	default:
		return false
	}
}

func formatFileSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	}
	if size < 1024*1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
	}
	return fmt.Sprintf("%.1f GB", float64(size)/(1024*1024*1024))
}

func fileExtensionLabel(fileName string) string {
	extension := strings.TrimPrefix(strings.ToUpper(path.Ext(fileName)), ".")
	if extension == "" {
		return "FILE"
	}
	if len(extension) > 6 {
		return extension[:6]
	}
	return extension
}

func randomAccessToken() string {
	var tokenBytes [18]byte
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		return fmt.Sprintf("%x", sha256.Sum256([]byte(time.Now().String())))[:24]
	}
	return base64.RawURLEncoding.EncodeToString(tokenBytes[:])
}

func (a *App) fileAccessRulesByName(ctx context.Context, r *http.Request) map[string]ManagedFileAccess {
	accessRulesByName := make(map[string]ManagedFileAccess)
	rows, err := a.db.QueryContext(ctx, `SELECT file_name,access_mode,token,expires_at,single_use_left,token_use_count FROM file_access_rules WHERE domain=?`, domainStorageName(a.siteDomain(ctx, r)))
	if err != nil {
		return accessRulesByName
	}
	defer rows.Close()
	for rows.Next() {
		var fileName string
		var accessRule ManagedFileAccess
		if scanErr := rows.Scan(&fileName, &accessRule.AccessMode, &accessRule.Token, &accessRule.ExpiresAt, &accessRule.SingleUseLeft, &accessRule.TokenUseCount); scanErr != nil {
			continue
		}
		if safeRelativeAssetPath(fileName) == "" {
			continue
		}
		if strings.TrimSpace(accessRule.AccessMode) == "" {
			accessRule.AccessMode = "public"
		}
		accessRulesByName[fileName] = accessRule
	}
	return accessRulesByName
}

func (a *App) fileAccessRule(ctx context.Context, r *http.Request, fileName string) ManagedFileAccess {
	rule := ManagedFileAccess{AccessMode: "public"}
	_ = a.db.QueryRowContext(ctx, `SELECT access_mode,token,expires_at,single_use_left,token_use_count FROM file_access_rules WHERE domain=? AND file_name=?`, domainStorageName(a.siteDomain(ctx, r)), fileName).Scan(&rule.AccessMode, &rule.Token, &rule.ExpiresAt, &rule.SingleUseLeft, &rule.TokenUseCount)
	if strings.TrimSpace(rule.AccessMode) == "" {
		rule.AccessMode = "public"
	}
	return rule
}

func (a *App) saveFileAccessRule(ctx context.Context, r *http.Request, fileName string) {
	accessMode := r.FormValue("access_mode")
	if accessMode != "token" && accessMode != "timer" {
		accessMode = "public"
	}
	token := strings.TrimSpace(r.FormValue("access_token"))
	if accessMode == "token" && token == "" {
		token = randomAccessToken()
	}
	accessDaysValue := strings.TrimSpace(r.FormValue("access_days"))
	days, _ := strconv.Atoi(accessDaysValue)
	if days < 0 {
		days = 0
	}
	singleUseLeft, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("single_use_left")))
	if singleUseLeft < 0 {
		singleUseLeft = 0
	}

	expiresAt := ""
	if accessMode == "public" {
		token = ""
		singleUseLeft = 0
	} else if accessMode == "timer" {
		token = ""
		singleUseLeft = 0
		if accessDaysValue == "" {
			_ = a.db.QueryRowContext(ctx, `SELECT expires_at FROM file_access_rules WHERE domain=? AND file_name=?`, domainStorageName(a.siteDomain(ctx, r)), fileName).Scan(&expiresAt)
		} else if days > 0 {
			expiresAt = time.Now().Add(time.Duration(days) * 24 * time.Hour).UTC().Format(time.RFC3339)
		}
	} else if accessMode == "token" && accessDaysValue == "" {
		_ = a.db.QueryRowContext(ctx, `SELECT expires_at FROM file_access_rules WHERE domain=? AND file_name=?`, domainStorageName(a.siteDomain(ctx, r)), fileName).Scan(&expiresAt)
	} else if accessMode == "token" && days > 0 {
		expiresAt = time.Now().Add(time.Duration(days) * 24 * time.Hour).UTC().Format(time.RFC3339)
	}
	_, _ = a.db.ExecContext(ctx, `INSERT INTO file_access_rules(domain,file_name,access_mode,token,expires_at,single_use_left,token_use_count)
VALUES(?,?,?,?,?,?,0)
ON CONFLICT(domain,file_name) DO UPDATE SET access_mode=excluded.access_mode,token=excluded.token,expires_at=excluded.expires_at,single_use_left=excluded.single_use_left,token_use_count=CASE WHEN file_access_rules.token=excluded.token THEN file_access_rules.token_use_count ELSE 0 END`,
		domainStorageName(a.siteDomain(ctx, r)), fileName, accessMode, token, expiresAt, singleUseLeft)
}

func (a *App) serveAssetFile(w http.ResponseWriter, r *http.Request, domain, fileName string) {
	rule := ManagedFileAccess{AccessMode: "public"}
	_ = a.db.QueryRowContext(r.Context(), `SELECT access_mode,token,expires_at,single_use_left,token_use_count FROM file_access_rules WHERE domain=? AND file_name=?`, domain, fileName).Scan(&rule.AccessMode, &rule.Token, &rule.ExpiresAt, &rule.SingleUseLeft, &rule.TokenUseCount)
	if strings.TrimSpace(rule.AccessMode) == "timer" {
		if strings.TrimSpace(rule.ExpiresAt) != "" {
			expiresAt, parseErr := time.Parse(time.RFC3339, rule.ExpiresAt)
			if parseErr == nil && time.Now().UTC().After(expiresAt) {
				http.Error(w, "timer expired", http.StatusForbidden)
				return
			}
		}
	}
	if strings.TrimSpace(rule.AccessMode) == "token" {
		requestedToken := strings.TrimSpace(r.URL.Query().Get("token"))
		if requestedToken == "" || requestedToken != rule.Token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if strings.TrimSpace(rule.ExpiresAt) != "" {
			expiresAt, parseErr := time.Parse(time.RFC3339, rule.ExpiresAt)
			if parseErr == nil && time.Now().UTC().After(expiresAt) {
				http.Error(w, "token expired", http.StatusForbidden)
				return
			}
		}
		if rule.SingleUseLeft > 0 {
			_, _ = a.db.ExecContext(r.Context(), `UPDATE file_access_rules SET single_use_left=single_use_left-1 WHERE domain=? AND file_name=? AND single_use_left>0`, domain, fileName)
		}
		_, _ = a.db.ExecContext(r.Context(), `UPDATE file_access_rules SET token_use_count=token_use_count+1 WHERE domain=? AND file_name=?`, domain, fileName)
	}
	filePath := filepath.Join(a.filesRootDir(), domain, filepath.FromSlash(fileName))
	if _, err := os.Stat(filePath); err != nil && !strings.HasPrefix(fileName, "p/") {
		legacyPath := filepath.Join(a.filesRootDir(), domain, "p", filepath.FromSlash(fileName))
		if _, legacyErr := os.Stat(legacyPath); legacyErr == nil {
			filePath = legacyPath
		}
	}
	_, _ = a.db.ExecContext(r.Context(), `UPDATE file_metadata SET download_count=download_count+1 WHERE domain=? AND file_name=?`, domain, fileName)
	http.ServeFile(w, r, filePath)
}

func publicAssetFileNameFromPath(requestPath, domain string) string {
	cleanedPath := path.Clean("/" + strings.TrimSpace(requestPath))
	if strings.HasPrefix(cleanedPath, "/p/") {
		return safeRelativeAssetPath(strings.TrimPrefix(cleanedPath, "/p/"))
	}
	domainPrefix := "/" + domainStorageName(domain) + "/p/"
	if strings.HasPrefix(cleanedPath, domainPrefix) {
		return safeRelativeAssetPath(strings.TrimPrefix(cleanedPath, domainPrefix))
	}
	return ""
}

func (a *App) isDomainPrefixedPublicAssetPath(r *http.Request) bool {
	return publicAssetFileNameFromPath(r.URL.Path, a.siteDomain(r.Context(), r)) != "" && !strings.HasPrefix(path.Clean(r.URL.Path), "/p/")
}

func (a *App) findPage(ctx context.Context, domain, pagePath string) (Page, error) {
	var current Page
	err := a.db.QueryRowContext(ctx, `SELECT domain,path,title,html,published FROM pages WHERE domain=? AND path=?`, domain, pagePath).Scan(&current.Domain, &current.Path, &current.Title, &current.HTML, &current.Published)
	return current, err
}

func (a *App) hasAdmin(ctx context.Context, domain string) bool {
	var adminCount int
	_ = a.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM users WHERE domain=? AND is_admin=1`, domain).Scan(&adminCount)
	return adminCount > 0
}

func (a *App) isDomainFrozen(ctx context.Context, domain string) bool {
	var isFrozen int
	_ = a.db.QueryRowContext(ctx, `SELECT is_frozen FROM domain_states WHERE domain=?`, domain).Scan(&isFrozen)
	return isFrozen == 1
}

func requestedReturnPath(r *http.Request) string {
	if r.URL.Path == "" {
		return "/"
	}
	return r.URL.Path
}

func (a *App) createSession(w http.ResponseWriter, r *http.Request, email string) {
	token := fmt.Sprintf("%x", sha256.Sum256([]byte(email+time.Now().String())))
	_, _ = a.db.ExecContext(r.Context(), `INSERT OR REPLACE INTO sessions(token,user_email,created_at) VALUES(?,?,?)`, token, a.siteDomain(r.Context(), r)+"|"+email, time.Now().Format(time.RFC3339))
	http.SetCookie(w, &http.Cookie{Name: "sitebrush_session", Value: token, Path: "/", HttpOnly: true})
}

func (a *App) isAdminRequest(r *http.Request) bool {
	cookie, err := r.Cookie("sitebrush_session")
	if err != nil {
		return false
	}
	var sessionCount int
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(1) FROM sessions s JOIN users u ON (u.domain||'|'||u.email)=s.user_email WHERE s.token=? AND u.domain=? AND u.is_admin=1`, cookie.Value, a.siteDomain(r.Context(), r)).Scan(&sessionCount)
	return sessionCount > 0
}

func (a *App) currentAdminEmail(r *http.Request) (string, bool) {
	cookie, err := r.Cookie("sitebrush_session")
	if err != nil {
		return "", false
	}
	var email string
	err = a.db.QueryRowContext(r.Context(), `SELECT u.email FROM sessions s JOIN users u ON (u.domain||'|'||u.email)=s.user_email WHERE s.token=? AND u.domain=? AND u.is_admin=1 LIMIT 1`, cookie.Value, a.siteDomain(r.Context(), r)).Scan(&email)
	if err != nil || strings.TrimSpace(email) == "" {
		return "", false
	}
	return email, true
}

func (a *App) latestActiveRevisionID(ctx context.Context, domain string, pagePath string) int {
	var revisionID int
	_ = a.db.QueryRowContext(ctx, `SELECT id FROM revisions WHERE domain=? AND page_path=? AND is_active=1 ORDER BY id DESC LIMIT 1`, domain, pagePath).Scan(&revisionID)
	return revisionID
}

func (a *App) revisionCount(ctx context.Context, domain string, pagePath string) int {
	var revisionCount int
	_ = a.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM revisions WHERE domain=? AND page_path=?`, domain, pagePath).Scan(&revisionCount)
	return revisionCount
}

func (a *App) serveManagedPageContent(w http.ResponseWriter, r *http.Request, pagePath, content, sourceType string) {
	a.logContentDelivery(w, sourceType)
	if pageContentKind(pagePath, content) == "html" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(a.injectContextMenu(r, pagePath, content)))
		return
	}
	w.Header().Set("Content-Type", contentTypeForManagedPage(pagePath, content))
	_, _ = w.Write([]byte(content))
}

func (a *App) injectContextMenu(r *http.Request, pagePath, html string) string {
	domain := a.siteDomain(r.Context(), r)
	revisionID := 0
	revisionCount := 0
	if a.isAdminRequest(r) {
		revisionID = a.latestActiveRevisionID(r.Context(), domain, pagePath)
		revisionCount = a.revisionCount(r.Context(), domain, pagePath)
	}
	menuScript := buildContextMenuScript(a.isAdminRequest(r), a.isDomainFrozen(r.Context(), domain), pagePath, domain, revisionID, revisionCount, translationsForRequest(r))
	if strings.Contains(strings.ToLower(html), "</body>") {
		bodyClosePattern := regexp.MustCompile(`(?i)</body>`)
		return bodyClosePattern.ReplaceAllString(html, menuScript+"</body>")
	}
	return html + menuScript
}

func pageContentKind(pagePath, content string) string {
	extension := strings.ToLower(path.Ext(strings.TrimSpace(pagePath)))
	switch extension {
	case ".htm", ".html":
		return "html"
	case ".txt", ".text", ".md", ".markdown", ".css", ".js", ".mjs", ".json", ".xml", ".csv", ".tsv", ".yml", ".yaml", ".toml", ".ini", ".svg":
		return "text"
	}
	if extension != "" {
		return "file"
	}
	trimmedContent := strings.TrimSpace(strings.ToLower(content))
	if trimmedContent == "" {
		return "html"
	}
	if strings.HasPrefix(trimmedContent, "<!doctype html") || strings.HasPrefix(trimmedContent, "<html") || strings.Contains(trimmedContent, "</body>") {
		return "html"
	}
	if strings.Contains(trimmedContent, "<script") || strings.Contains(trimmedContent, "<style") || looksLikeHTMLFragment(trimmedContent) {
		return "html"
	}
	return "text"
}

func looksLikeHTMLFragment(trimmedContent string) bool {
	for _, tagName := range []string{"<div", "<section", "<article", "<main", "<header", "<footer", "<nav", "<p", "<h1", "<h2", "<h3", "<ul", "<ol", "<table", "<form", "<img", "<a "} {
		if strings.Contains(trimmedContent, tagName) {
			return true
		}
	}
	return false
}

func contentKindLabel(kind string) string {
	switch kind {
	case "html":
		return "HTML page"
	case "text":
		return "Text document"
	default:
		return "File"
	}
}

func contentTypeForManagedPage(pagePath, content string) string {
	extension := strings.ToLower(path.Ext(strings.TrimSpace(pagePath)))
	if extension != "" {
		if contentType := mime.TypeByExtension(extension); contentType != "" {
			if strings.HasPrefix(contentType, "text/") && !strings.Contains(strings.ToLower(contentType), "charset=") {
				return contentType + "; charset=utf-8"
			}
			return contentType
		}
	}
	if strings.TrimSpace(content) == "" {
		return "text/plain; charset=utf-8"
	}
	detectedContentType := http.DetectContentType([]byte(content))
	if strings.HasPrefix(detectedContentType, "text/") && !strings.Contains(strings.ToLower(detectedContentType), "charset=") {
		return detectedContentType + "; charset=utf-8"
	}
	return detectedContentType
}

func buildContextMenuScript(isAdmin bool, isFrozen bool, pagePath, domain string, revisionID int, revisionCount int, translations map[string]string) string {
	escapedPath := template.JSEscapeString(pagePath)
	escapedDomain := template.JSEscapeString(domain)
	confirmFreezePrompt := template.JSEscapeString(translationOrDefault(translations, "confirm_freeze_prompt", "Freeze domain now?"))
	confirmPublishPrompt := template.JSEscapeString(translationOrDefault(translations, "confirm_publish_prompt", "Publish website changes now?"))
	confirmDeletePrompt := template.JSEscapeString(translationOrDefault(translations, "confirm_delete_revision_prompt", "Delete this revision?"))
	publishConfirmWithChangesLabel := template.JSEscapeString(translationOrDefault(translations, "publish_confirm_with_changes", "Publish the changes made to the site?"))
	publishConfirmWithoutChangesLabel := template.JSEscapeString(translationOrDefault(translations, "publish_confirm_without_changes", "No changes were made. Unfreeze the site?"))
	publishPreviewLoadingLabel := template.JSEscapeString(translationOrDefault(translations, "publish_preview_loading", "Checking changes to publish..."))
	publishPreviewSummaryLabel := template.JSEscapeString(translationOrDefault(translations, "publish_preview_summary", "Changes:"))
	confirmYesLabel := template.JSEscapeString(translationOrDefault(translations, "confirm_yes", "Yes"))
	confirmNoLabel := template.JSEscapeString(translationOrDefault(translations, "confirm_no", "No"))
	editLabel := template.JSEscapeString(translationOrDefault(translations, "menu_edit", "Edit"))
	textEditLabel := template.JSEscapeString(translationOrDefault(translations, "menu_text_edit", "Edit as text"))
	deleteLabel := template.JSEscapeString(translationOrDefault(translations, "menu_delete", "Delete"))
	revisionsLabel := template.JSEscapeString(fmt.Sprintf("%s: %d", translationOrDefault(translations, "menu_revisions", "Revisions"), revisionCount))
	filesLabel := template.JSEscapeString(translationOrDefault(translations, "menu_files", "Files"))
	treeLabel := template.JSEscapeString(translationOrDefault(translations, "menu_tree", "Site tree"))
	freezeLabel := template.JSEscapeString(translationOrDefault(translations, "menu_freeze", "Freeze"))
	publishLabel := template.JSEscapeString(translationOrDefault(translations, "menu_publish", "Publish"))
	settingsLabel := template.JSEscapeString(translationOrDefault(translations, "menu_domain_settings", "Domain settings"))
	profileLabel := template.JSEscapeString(translationOrDefault(translations, "menu_profile", "Account"))
	logoutLabel := template.JSEscapeString(translationOrDefault(translations, "menu_logout", "Sign out"))
	loginLabel := template.JSEscapeString(translationOrDefault(translations, "menu_login", "Sign in"))
	treeModalTitle := template.JSEscapeString(translationOrDefault(translations, "tree_modal_title", "Site tree"))
	treeLoadingLabel := template.JSEscapeString(translationOrDefault(translations, "tree_loading", "Loading site tree..."))
	treeLoadErrorLabel := template.JSEscapeString(translationOrDefault(translations, "tree_load_error", "Failed to load site tree."))
	stableReleaseURL := "https://github.com/matveynator/sitebrush/releases/tag/stable-release"
	compiledVersionLabel := template.JSEscapeString(CompileVersion)
	copyrightMenuEntry := fmt.Sprintf("<li class='SiteBrushContextMenu ContextMenuCopyright'><a href='%s' class='SiteBrushContextMenuLink' target='_blank' rel='noopener noreferrer'>sitebrush <span class='SiteBrushContextMenuVersion'>%s</span></a></li>", stableReleaseURL, compiledVersionLabel)
	if isAdmin {
		deleteActionEntry := ""
		if revisionID > 0 {
			deleteActionEntry = "<li class='SiteBrushContextMenu'><button type='button' data-sitebrush-action='delete' class='SiteBrushContextMenuLink SiteBrushContextMenuButton'><img src='/p/static/cross.png' class='SiteBrushMenuIcon' alt=''>" + deleteLabel + "</button></li>"
		}
		freezeActionEntry := "<li class='SiteBrushContextMenu'><button type='button' data-sitebrush-action='freeze' class='SiteBrushContextMenuLink SiteBrushContextMenuButton'><img src='/p/static/freeze.png' class='SiteBrushMenuIcon' alt=''>" + freezeLabel + "</button></li>"
		if isFrozen {
			freezeActionEntry = "<li class='SiteBrushContextMenu'><button type='button' data-sitebrush-action='publish' class='SiteBrushContextMenuLink SiteBrushContextMenuButton'><img src='/p/static/publish.png' class='SiteBrushMenuIcon' alt=''>" + publishLabel + "</button></li>"
		}
		return contextMenuStylesAndHelpers() + `<script>
(function initializeSitebrushContextMenuForAdmin() {
  if (window.__sitebrushContextMenuInitialized) {
    return;
  }
  window.__sitebrushContextMenuInitialized = true;
  const currentPagePath = "` + escapedPath + `";
  const currentDomainName = "` + escapedDomain + `";
  const isDomainFrozen = ` + strconv.FormatBool(isFrozen) + `;
  const actionConfigByName = {
    delete: { path: "?delete=` + strconv.Itoa(revisionID) + `", message: "` + confirmDeletePrompt + `" },
    freeze: { path: "?freeze", message: "` + confirmFreezePrompt + `" },
    publish: { path: "?publish", message: "` + confirmPublishPrompt + `" }
  };
  const confirmYesLabel = "` + confirmYesLabel + `";
  const confirmNoLabel = "` + confirmNoLabel + `";
  function openConfirmationDialog(confirmMessageText, onConfirm) {
    closeSitebrushMenu();
    const overlayElement = document.createElement("div");
    overlayElement.className = "SiteBrushConfirmOverlay";
    const modalElement = document.createElement("div");
    modalElement.className = "SiteBrushConfirmModal";
    const textElement = document.createElement("p");
    textElement.className = "SiteBrushConfirmText";
    textElement.textContent = confirmMessageText;
    const actionRowElement = document.createElement("div");
    actionRowElement.className = "SiteBrushConfirmActions";
    const confirmButtonElement = document.createElement("button");
    confirmButtonElement.type = "button";
    confirmButtonElement.className = "SiteBrushConfirmButton";
    confirmButtonElement.textContent = confirmYesLabel;
    const cancelButtonElement = document.createElement("button");
    cancelButtonElement.type = "button";
    cancelButtonElement.className = "SiteBrushCancelButton";
    cancelButtonElement.textContent = confirmNoLabel;
    actionRowElement.appendChild(confirmButtonElement);
    actionRowElement.appendChild(cancelButtonElement);
    modalElement.appendChild(textElement);
    modalElement.appendChild(actionRowElement);
    overlayElement.appendChild(modalElement);
    document.body.appendChild(overlayElement);
    function closeDialog() { overlayElement.remove(); }
    cancelButtonElement.addEventListener("click", closeDialog);
    confirmButtonElement.addEventListener("click", function onConfirmClick() { closeDialog(); onConfirm(); });
  }
  function openPublishConfirmationDialog(confirmMessageText, changedPagePaths, onConfirm) {
    closeSitebrushMenu();
    const overlayElement = document.createElement("div");
    overlayElement.className = "SiteBrushConfirmOverlay";
    const modalElement = document.createElement("div");
    modalElement.className = "SiteBrushConfirmModal";
    const textElement = document.createElement("p");
    textElement.className = "SiteBrushConfirmText";
    textElement.textContent = confirmMessageText;
    modalElement.appendChild(textElement);
    if (Array.isArray(changedPagePaths) && changedPagePaths.length > 0) {
      const listElement = document.createElement("ul");
      listElement.className = "SiteBrushPublishPreviewList";
      for (const changedPagePath of changedPagePaths) {
        const itemElement = document.createElement("li");
        itemElement.className = "SiteBrushPublishPreviewListItem";
        const linkElement = document.createElement("a");
        linkElement.className = "SiteBrushPublishPreviewLink";
        linkElement.href = changedPagePath;
        linkElement.textContent = changedPagePath;
        itemElement.appendChild(linkElement);
        listElement.appendChild(itemElement);
      }
      modalElement.appendChild(listElement);
    }
    const actionRowElement = document.createElement("div");
    actionRowElement.className = "SiteBrushConfirmActions";
    const confirmButtonElement = document.createElement("button");
    confirmButtonElement.type = "button";
    confirmButtonElement.className = "SiteBrushConfirmButton";
    confirmButtonElement.textContent = confirmYesLabel;
    const cancelButtonElement = document.createElement("button");
    cancelButtonElement.type = "button";
    cancelButtonElement.className = "SiteBrushCancelButton";
    cancelButtonElement.textContent = confirmNoLabel;
    actionRowElement.appendChild(confirmButtonElement);
    actionRowElement.appendChild(cancelButtonElement);
    modalElement.appendChild(actionRowElement);
    overlayElement.appendChild(modalElement);
    document.body.appendChild(overlayElement);
    function closeDialog() { overlayElement.remove(); }
    cancelButtonElement.addEventListener("click", closeDialog);
    confirmButtonElement.addEventListener("click", function onConfirmClick() { closeDialog(); onConfirm(); });
  }
  document.addEventListener("click", function onActionClick(browserEvent) {
    const actionButtonElement = browserEvent.target && browserEvent.target.closest && browserEvent.target.closest("[data-sitebrush-action]");
    if (!actionButtonElement) {
      return;
    }
    browserEvent.preventDefault();
    const actionName = actionButtonElement.getAttribute("data-sitebrush-action");
    if (actionName === "tree") {
      openSiteTreeDialog();
      return;
    }
    const selectedActionConfig = actionConfigByName[actionName];
    if (!selectedActionConfig) {
      return;
    }
    if (actionName === "publish") {
      fetch(currentPagePath + "?publish_preview", { headers: { "Accept": "application/json" } })
        .then(function parsePublishPreview(previewResponse) {
          if (!previewResponse.ok) { throw new Error("publish preview failed"); }
          return previewResponse.json();
        })
	        .then(function confirmPublishWithPreview(previewPayload) {
		          let summaryText = "` + publishPreviewSummaryLabel + `" + " " + previewPayload.changed + " / " + previewPayload.total;
		          let confirmQuestionText = "` + publishConfirmWithChangesLabel + `";
		          if (previewPayload.changed === 0) {
		            summaryText = "";
		            confirmQuestionText = "` + publishConfirmWithoutChangesLabel + `";
		          }
		          const dialogMessage = summaryText === "" ? confirmQuestionText : confirmQuestionText + "\n\n" + summaryText;
		          openPublishConfirmationDialog(dialogMessage, previewPayload.paths || [], submitConfirmedAction);
		        })
        .catch(function fallbackPublishConfirmation() {
          openConfirmationDialog(selectedActionConfig.message + "\n\n" + "` + publishPreviewLoadingLabel + `", submitConfirmedAction);
        });
      return;
    }
    openConfirmationDialog(selectedActionConfig.message, submitConfirmedAction);
    function submitConfirmedAction() {
      const actionFormElement = document.createElement("form");
      actionFormElement.method = "POST";
      actionFormElement.action = selectedActionConfig.path;
      document.body.appendChild(actionFormElement);
      actionFormElement.submit();
    }
  }, {capture: true});
  document.addEventListener("contextmenu", function onContextMenuOpen(browserEvent) {
    if (browserEvent.ctrlKey || browserEvent.defaultPrevented) {
      return;
    }
    const clickedInsideSitebrushMenu = browserEvent.target && browserEvent.target.closest && browserEvent.target.closest("#SiteBrushMenuBox");
    if (clickedInsideSitebrushMenu) {
      return;
    }
    browserEvent.preventDefault();
    const menuHtmlEntries = [
      "<ul class='SiteBrushMenuList'>",
      "<li class='SiteBrushContextMenu SiteBrushDomainMenuItem'><a href='/' class='SiteBrushContextMenuLink'>" + currentDomainName + "</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?visual' class='SiteBrushContextMenuLink'><img src='/p/static/pencil.png' class='SiteBrushMenuIcon' alt=''>" + "` + editLabel + `" + "</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?text' class='SiteBrushContextMenuLink'><img src='/p/static/pencil-text.png' class='SiteBrushMenuIcon' alt=''>" + "` + textEditLabel + `" + "</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?revisions' class='SiteBrushContextMenuLink'><img src='/p/static/revisions.png' class='SiteBrushMenuIcon' alt=''>" + "` + revisionsLabel + `" + "</a></li>",
      "` + deleteActionEntry + `",
      "<li class='SiteBrushContextMenu'><a href='?files' class='SiteBrushContextMenuLink'><img src='/p/static/upload.png' class='SiteBrushMenuIcon' alt=''>" + "` + filesLabel + `" + "</a></li>",
      "<li class='SiteBrushContextMenu'><button type='button' data-sitebrush-action='tree' class='SiteBrushContextMenuLink SiteBrushContextMenuButton'><img src='/p/static/tree.png' class='SiteBrushMenuIcon' alt=''>" + "` + treeLabel + `" + "</button></li>",
      "` + freezeActionEntry + `",
      "<li class='SiteBrushContextMenu'><a href='?settings' class='SiteBrushContextMenuLink'><img src='/p/static/settings.png' class='SiteBrushMenuIcon' alt=''>" + "` + settingsLabel + `" + "</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?profile' class='SiteBrushContextMenuLink'><img src='/p/static/profile.png' class='SiteBrushMenuIcon' alt=''>" + "` + profileLabel + `" + "</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?logout' class='SiteBrushContextMenuLink'><img src='/p/static/sign-out.png' class='SiteBrushMenuIcon' alt=''>" + "` + logoutLabel + `" + "</a></li>",
      "` + copyrightMenuEntry + `",
      "</ul>"
    ];
    showSitebrushMenu(browserEvent, menuHtmlEntries, currentPagePath, isDomainFrozen);
	  }, {capture: false, passive: false});
  function openSiteTreeDialog() {
    closeSitebrushMenu();
    const overlayElement = document.createElement("div");
    overlayElement.className = "SiteBrushTreeOverlay";
    const modalElement = document.createElement("div");
    modalElement.className = "SiteBrushTreeModal";
    const titleElement = document.createElement("h3");
    titleElement.className = "SiteBrushTreeTitle";
    titleElement.textContent = "` + treeModalTitle + `";
    const contentElement = document.createElement("div");
    contentElement.className = "SiteBrushTreeContent";
    contentElement.textContent = "` + treeLoadingLabel + `";
    const closeButtonElement = document.createElement("button");
    closeButtonElement.type = "button";
    closeButtonElement.className = "SiteBrushCancelButton";
    closeButtonElement.textContent = "` + confirmNoLabel + `";
    modalElement.appendChild(titleElement);
    modalElement.appendChild(contentElement);
    modalElement.appendChild(closeButtonElement);
    overlayElement.appendChild(modalElement);
    document.body.appendChild(overlayElement);
    closeButtonElement.addEventListener("click", function closeTreeDialog() { overlayElement.remove(); });
    fetch(currentPagePath + "?tree", { headers: { "Accept": "application/json" } })
      .then(function parseTreeResponse(treeResponse) {
        if (!treeResponse.ok) { throw new Error("tree request failed"); }
        return treeResponse.json();
      })
      .then(function renderTree(treeData) { renderSiteTree(contentElement, treeData); })
      .catch(function showTreeError() { contentElement.textContent = "` + treeLoadErrorLabel + `"; });
  }
})();
	</script>`
	}
	return contextMenuStylesAndHelpers() + `<script>
(function initializeSitebrushContextMenuForGuests() {
  if (window.__sitebrushContextMenuInitialized) {
    return;
  }
  window.__sitebrushContextMenuInitialized = true;
  const currentPagePath = "` + escapedPath + `";
  const currentDomainName = "` + escapedDomain + `";
  document.addEventListener("contextmenu", function onContextMenuOpen(browserEvent) {
    if (browserEvent.ctrlKey || browserEvent.defaultPrevented) {
      return;
    }
    const clickedInsideSitebrushMenu = browserEvent.target && browserEvent.target.closest && browserEvent.target.closest("#SiteBrushMenuBox");
    if (clickedInsideSitebrushMenu) {
      return;
    }
    browserEvent.preventDefault();
    const menuHtmlEntries = [
      "<ul class='SiteBrushMenuList'>",
      "<li class='SiteBrushContextMenu SiteBrushDomainMenuItem'><a href='/' class='SiteBrushContextMenuLink'>" + currentDomainName + "</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?login' class='SiteBrushContextMenuLink'><img src='/p/static/login.png' class='SiteBrushMenuIcon' alt=''>" + "` + loginLabel + `" + "</a></li>",
      "` + copyrightMenuEntry + `",
      "</ul>"
    ];
    showSitebrushMenu(browserEvent, menuHtmlEntries, currentPagePath, false);
	  }, {capture: false, passive: false});
})();
	</script>`
}

func contextMenuStylesAndHelpers() string {
	return `<style>
.SiteBrushMenuBox,.SiteBrushMenuBox *{all:initial;box-sizing:border-box}
.SiteBrushMenuBox{position:fixed;background:#fff url(/p/static/bg.png) repeat-x top;border:1px solid #8ea4c1;z-index:2147483646;padding:2px;min-width:240px;box-shadow:0 2px 12px rgba(0,0,0,0.2);font-family:Arial,Helvetica,sans-serif}
.SiteBrushMenuBox.SiteBrushMenuBoxFrozen{background:#e9f5ff;border-color:#6da6d4}
.SiteBrushMenuList{list-style:none;margin:0;padding:0}
.SiteBrushContextMenu{margin:0;padding:0}
.SiteBrushContextMenuLink{display:flex;align-items:center;gap:8px;padding:8px 10px;color:#1f3f6f;text-decoration:none;font-family:Arial,Helvetica,sans-serif;font-size:14px;cursor:pointer}
.SiteBrushContextMenuLink:hover{background:#eef5ff}
.SiteBrushContextMenuButton{width:100%;border:0;background:transparent;text-align:left}
.SiteBrushDomainMenuItem .SiteBrushContextMenuLink{font-weight:700;border-bottom:1px solid #c8d5e7}
.ContextMenuCopyright .SiteBrushContextMenuLink{font-size:12px;color:#5b6f8b;border-top:1px solid #c8d5e7;margin-top:2px;padding-top:7px}.SiteBrushMenuIcon{width:18px;height:18px;flex:0 0 18px}
.SiteBrushContextMenuVersion{font-weight:700;margin-left:4px}
.SiteBrushConfirmOverlay{position:fixed;inset:0;background:rgba(0,0,0,.45);display:flex;align-items:center;justify-content:center;z-index:2147483647}
.SiteBrushConfirmModal{background:#fff;border:1px solid #8ea4c1;min-width:260px;max-width:340px;padding:16px;font-family:Arial,Helvetica,sans-serif}
.SiteBrushConfirmText{margin:0 0 14px 0;color:#1f3f6f;font-size:14px}
.SiteBrushPublishPreviewList{list-style:none;margin:0 0 12px 0;padding:0;max-height:180px;overflow:auto}
.SiteBrushPublishPreviewListItem{margin:0 0 4px 0}
.SiteBrushPublishPreviewLink{color:#1f3f6f;text-decoration:underline;font-size:13px}
.SiteBrushConfirmActions{display:flex;gap:8px;justify-content:flex-end}
.SiteBrushConfirmButton,.SiteBrushCancelButton{border:1px solid #8ea4c1;background:#f2f7ff;padding:6px 12px;cursor:pointer;font-size:13px}
.SiteBrushTreeOverlay{position:fixed;inset:0;background:rgba(0,0,0,.45);display:flex;align-items:center;justify-content:center;z-index:2147483647}
.SiteBrushTreeModal{background:#fff;border:1px solid #8ea4c1;min-width:320px;max-width:700px;max-height:80vh;overflow:auto;padding:16px;font-family:Arial,Helvetica,sans-serif}
.SiteBrushTreeTitle{margin:0 0 12px 0;color:#1f3f6f;font-size:18px}
.SiteBrushTreeContent{margin:0 0 12px 0;color:#1f3f6f;font-size:14px}
.SiteBrushTreeList{list-style:none;margin:0;padding-left:16px}
.SiteBrushTreeLink{color:#1f3f6f;text-decoration:none;font-size:14px;line-height:1.6}
.SiteBrushTreeCurrent{font-weight:700;text-decoration:underline}
@media (prefers-color-scheme: dark){
  .SiteBrushMenuBox{background:#172235;border-color:#2f405d}
  .SiteBrushMenuBox.SiteBrushMenuBoxFrozen{background:#13263d;border-color:#4a6f99}
  .SiteBrushContextMenuLink{color:#dbe8ff}
  .SiteBrushContextMenuLink:hover{background:#24344d}
  .SiteBrushDomainMenuItem .SiteBrushContextMenuLink{border-bottom-color:#2f405d}
  .ContextMenuCopyright .SiteBrushContextMenuLink{color:#a7bbd8;border-top-color:#2f405d}
  .SiteBrushConfirmModal{background:#172235;border-color:#2f405d}
  .SiteBrushConfirmText,.SiteBrushPublishPreviewLink{color:#dbe8ff}
  .SiteBrushConfirmButton,.SiteBrushCancelButton{background:#22324a;color:#dbe8ff;border-color:#405674}
  .SiteBrushTreeModal{background:#172235;border-color:#2f405d}
  .SiteBrushTreeTitle,.SiteBrushTreeContent,.SiteBrushTreeLink{color:#dbe8ff}
}
</style>
<script>
function closeSitebrushMenu() {
  const existingMenuBox = document.getElementById("SiteBrushMenuBox");
  if (existingMenuBox) {
    existingMenuBox.remove();
  }
}
function normalizeSitebrushMenuLinks(menuBoxElement, currentPagePath) {
  const menuLinkElements = menuBoxElement.querySelectorAll("a[href]");
  for (const menuLinkElement of menuLinkElements) {
    const originalHref = menuLinkElement.getAttribute("href");
    if (!originalHref || !originalHref.startsWith("?")) {
      continue;
    }
    menuLinkElement.setAttribute("href", currentPagePath + originalHref);
  }
}
function showSitebrushMenu(browserEvent, menuHtmlEntries, currentPagePath, frozenMenuEnabled) {
  closeSitebrushMenu();
  const menuBoxElement = document.createElement("div");
  menuBoxElement.id = "SiteBrushMenuBox";
  menuBoxElement.className = "SiteBrushMenuBox";
  if (frozenMenuEnabled) {
    menuBoxElement.classList.add("SiteBrushMenuBoxFrozen");
  }
  menuBoxElement.innerHTML = menuHtmlEntries.join("");
  normalizeSitebrushMenuLinks(menuBoxElement, currentPagePath);
  menuBoxElement.style.left = browserEvent.clientX + "px";
  menuBoxElement.style.top = browserEvent.clientY + "px";
  document.body.appendChild(menuBoxElement);
  menuBoxElement.addEventListener("click", function onMenuClick(browserEvent) {
    const menuLinkElement = browserEvent.target && browserEvent.target.closest && browserEvent.target.closest("a[href]");
    if (!menuLinkElement) {
      return;
    }
    const targetHref = menuLinkElement.getAttribute("href");
    if (!targetHref) {
      return;
    }
    browserEvent.preventDefault();
    browserEvent.stopPropagation();
    window.location.href = targetHref;
  });
  document.addEventListener("click", function closeMenuOnClick(browserEvent) {
    const openMenu = document.getElementById("SiteBrushMenuBox");
    if (!openMenu) {
      return;
    }
    if (browserEvent.target && browserEvent.target.closest && browserEvent.target.closest("#SiteBrushMenuBox")) {
      return;
    }
    openMenu.remove();
  }, { once: true });
}
function buildSiteTreeNodeName(fullPath) {
  if (fullPath === "/") { return "/"; }
  if (fullPath.endsWith("/")) {
    const trimmedPath = fullPath.slice(0, -1);
    const slashIndex = trimmedPath.lastIndexOf("/");
    return trimmedPath.slice(slashIndex + 1) + "/";
  }
  const slashIndex = fullPath.lastIndexOf("/");
  return fullPath.slice(slashIndex + 1);
}
function parentTreePath(fullPath) {
  if (fullPath === "/") { return ""; }
  if (fullPath.endsWith("/")) { return fullPath.slice(0, -1); }
  const slashIndex = fullPath.lastIndexOf("/");
  if (slashIndex <= 0) { return "/"; }
  return fullPath.slice(0, slashIndex);
}
function buildSiteTreeStateFromPaths(pagePathList, currentPagePath) {
  const rootNode = { name: "/", fullPath: "/", childrenByName: {}, childList: [] };
  const nodeByPath = { "/": rootNode };
  const ensureNode = function ensureTreeNode(pathValue) {
    if (nodeByPath[pathValue]) { return nodeByPath[pathValue]; }
    const createdNode = { name: buildSiteTreeNodeName(pathValue), fullPath: pathValue, childrenByName: {}, childList: [] };
    nodeByPath[pathValue] = createdNode;
    return createdNode;
  };
  const connectChildToParent = function connectTreeChild(parentNode, childNode) {
    if (parentNode.childrenByName[childNode.fullPath]) { return; }
    parentNode.childrenByName[childNode.fullPath] = childNode;
    parentNode.childList.push(childNode);
  };
  for (const fullPathEntry of pagePathList) {
    const exactPath = fullPathEntry || "/";
    let childNode = ensureNode(exactPath);
    let parentPath = parentTreePath(exactPath);
    while (parentPath) {
      const parentNode = ensureNode(parentPath);
      connectChildToParent(parentNode, childNode);
      childNode = parentNode;
      parentPath = parentTreePath(parentNode.fullPath);
    }
  }
  const sortBranch = function sortBranchNodes(branchNode) {
    branchNode.childList.sort(function compareBranchNames(leftBranch, rightBranch) { return leftBranch.name.localeCompare(rightBranch.name); });
    for (const childBranch of branchNode.childList) { sortBranch(childBranch); }
  };
  sortBranch(rootNode);
  rootNode.currentPath = currentPagePath;
  return rootNode;
}
function renderSiteTree(hostElement, treeData) {
  hostElement.textContent = "";
  const treeState = buildSiteTreeStateFromPaths(treeData.paths || [], treeData.current_path || "/");
  const branchRootList = document.createElement("ul");
  branchRootList.className = "SiteBrushTreeList";
  hostElement.appendChild(branchRootList);
  const renderBranchNode = function renderBranchNodeRecursive(branchNode, parentListElement) {
    const branchListItemElement = document.createElement("li");
    const branchLinkElement = document.createElement("a");
    branchLinkElement.className = "SiteBrushTreeLink";
    branchLinkElement.href = branchNode.fullPath;
    branchLinkElement.textContent = branchNode.fullPath;
    if (branchNode.fullPath === treeState.currentPath) { branchLinkElement.classList.add("SiteBrushTreeCurrent"); }
    branchListItemElement.appendChild(branchLinkElement);
    parentListElement.appendChild(branchListItemElement);
    if (branchNode.childList.length === 0) { return; }
    const childListElement = document.createElement("ul");
    childListElement.className = "SiteBrushTreeList";
    branchListItemElement.appendChild(childListElement);
    for (const childNode of branchNode.childList) { renderBranchNodeRecursive(childNode, childListElement); }
  };
  renderBranchNode(treeState, branchRootList);
}
</script>`
}

func translationOrDefault(translations map[string]string, key, fallback string) string {
	translatedValue := strings.TrimSpace(translations[key])
	if translatedValue == "" {
		return fallback
	}
	return translatedValue
}

func (a *App) applyTemplatePropagation(ctx context.Context, domain, sourceHTML string) {
	templateBlockByID := extractTemplateBlocks(sourceHTML)
	if len(templateBlockByID) == 0 {
		return
	}

	pageRows, err := a.db.QueryContext(ctx, `SELECT path,title,html FROM pages WHERE domain=?`, domain)
	if err != nil {
		return
	}
	type storedPage struct {
		path  string
		title string
		html  string
	}
	pageList := make([]storedPage, 0)
	for pageRows.Next() {
		var currentPage storedPage
		if scanErr := pageRows.Scan(&currentPage.path, &currentPage.title, &currentPage.html); scanErr != nil {
			continue
		}
		pageList = append(pageList, currentPage)
	}
	_ = pageRows.Close()

	frozenDomain := a.isDomainFrozen(ctx, domain)
	for _, currentPage := range pageList {
		updatedHTML, changed := replaceTemplateBlocks(currentPage.html, templateBlockByID)
		if !changed || updatedHTML == currentPage.html {
			continue
		}

		_, _ = a.db.ExecContext(ctx, `UPDATE pages SET html=? WHERE domain=? AND path=?`, updatedHTML, domain, currentPage.path)
		if !frozenDomain {
			_, _ = a.db.ExecContext(ctx, `INSERT OR REPLACE INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, domain, currentPage.path, currentPage.title, updatedHTML)
			a.writePublishedStaticHTML(domain, currentPage.path, updatedHTML)
		}
		_, _ = a.db.ExecContext(ctx, `INSERT INTO revisions(domain,page_path,html,created_at) VALUES(?,?,?,?)`, domain, currentPage.path, updatedHTML, time.Now().Format(time.RFC3339))
	}
}

type templateMatch struct {
	start int
	end   int
	id    string
	block string
}

type templateOpenElement struct {
	tagName    string
	start      int
	templateID string
}

func extractTemplateBlocks(sourceHTML string) map[string]string {
	matchList := scanTemplateMatches(sourceHTML)
	if len(matchList) == 0 {
		return nil
	}

	templateBlockByID := make(map[string]string, len(matchList))
	for _, currentMatch := range matchList {
		templateBlockByID[currentMatch.id] = currentMatch.block
	}
	return templateBlockByID
}

func replaceTemplateBlocks(pageHTML string, templateBlockByID map[string]string) (string, bool) {
	matchList := scanTemplateMatches(pageHTML)
	if len(matchList) == 0 {
		return pageHTML, false
	}

	var updatedHTML strings.Builder
	updatedHTML.Grow(len(pageHTML))
	changed := false
	previousEnd := 0

	for _, currentMatch := range matchList {
		replacementBlock, found := templateBlockByID[currentMatch.id]
		if !found {
			continue
		}
		updatedHTML.WriteString(pageHTML[previousEnd:currentMatch.start])
		updatedHTML.WriteString(replacementBlock)
		previousEnd = currentMatch.end
		changed = true
	}

	if !changed {
		return pageHTML, false
	}
	updatedHTML.WriteString(pageHTML[previousEnd:])
	return updatedHTML.String(), true
}

func scanTemplateMatches(pageHTML string) []templateMatch {
	tokenizer := html.NewTokenizer(strings.NewReader(pageHTML))
	openElementStack := make([]templateOpenElement, 0)
	matchList := make([]templateMatch, 0)
	offset := 0

	for {
		tokenType := tokenizer.Next()
		rawToken := tokenizer.Raw()
		tokenStart := offset
		tokenEnd := tokenStart + len(rawToken)
		offset = tokenEnd

		switch tokenType {
		case html.ErrorToken:
			return matchList
		case html.StartTagToken:
			token := tokenizer.Token()
			openElementStack = append(openElementStack, templateOpenElement{
				tagName:    token.Data,
				start:      tokenStart,
				templateID: templateIdentifierFromAttributes(token.Attr),
			})
		case html.SelfClosingTagToken:
			token := tokenizer.Token()
			templateID := templateIdentifierFromAttributes(token.Attr)
			if templateID == "" {
				continue
			}
			matchList = append(matchList, templateMatch{
				start: tokenStart,
				end:   tokenEnd,
				id:    templateID,
				block: pageHTML[tokenStart:tokenEnd],
			})
		case html.EndTagToken:
			token := tokenizer.Token()
			for len(openElementStack) > 0 {
				lastIndex := len(openElementStack) - 1
				openElement := openElementStack[lastIndex]
				openElementStack = openElementStack[:lastIndex]
				if !strings.EqualFold(openElement.tagName, token.Data) {
					continue
				}
				if openElement.templateID == "" {
					break
				}
				matchList = append(matchList, templateMatch{
					start: openElement.start,
					end:   tokenEnd,
					id:    openElement.templateID,
					block: pageHTML[openElement.start:tokenEnd],
				})
				break
			}
		}
	}
}

func templateIdentifierFromAttributes(attributeList []html.Attribute) string {
	for _, attribute := range attributeList {
		if !strings.EqualFold(attribute.Key, "class") {
			continue
		}
		return templateIdentifierFromClassList(attribute.Val)
	}
	return ""
}

func templateIdentifierFromClassList(classValue string) string {
	classNameList := strings.Fields(classValue)
	for classIndex, className := range classNameList {
		if strings.EqualFold(className, "SiteBrush-Template") {
			if classIndex+1 < len(classNameList) {
				return classNameList[classIndex+1]
			}
			return ""
		}
		lowerClassName := strings.ToLower(className)
		if strings.HasPrefix(lowerClassName, "sitebrush-template-") {
			return className[len("sitebrush-template-"):]
		}
	}
	return ""
}

func (a *App) render(w http.ResponseWriter, r *http.Request, templateName string, templateData any) {
	fileBytes, err := fs.ReadFile(embeddedWebFiles, "web/"+templateName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	parsedTemplate := template.Must(template.New(templateName).Parse(string(fileBytes)))
	envelope := map[string]any{"Domain": a.siteDomain(r.Context(), r), "T": translationsForRequest(r), "CompileVersion": CompileVersion}
	mergeTemplateData(envelope, templateData)
	_ = parsedTemplate.Execute(w, envelope)
}

func mergeTemplateData(envelope map[string]any, templateData any) {
	if templateData == nil {
		return
	}
	if values, ok := templateData.(map[string]any); ok {
		for key, value := range values {
			envelope[key] = value
		}
		return
	}

	reflectedValue := reflect.ValueOf(templateData)
	if reflectedValue.Kind() == reflect.Pointer {
		if reflectedValue.IsNil() {
			return
		}
		reflectedValue = reflectedValue.Elem()
	}
	if reflectedValue.Kind() == reflect.Struct {
		reflectedType := reflectedValue.Type()
		for fieldIndex := 0; fieldIndex < reflectedValue.NumField(); fieldIndex++ {
			structField := reflectedType.Field(fieldIndex)
			if structField.PkgPath != "" {
				continue
			}
			envelope[structField.Name] = reflectedValue.Field(fieldIndex).Interface()
		}
		return
	}

	envelope["Data"] = templateData
}

func loadTranslationCatalog() map[string]map[string]string {
	translationBytes, err := fs.ReadFile(embeddedWebFiles, "web/translations.json")
	if err != nil {
		return map[string]map[string]string{}
	}
	var catalog map[string]map[string]string
	if json.Unmarshal(translationBytes, &catalog) != nil {
		return map[string]map[string]string{}
	}
	return catalog
}

func translationsForRequest(r *http.Request) map[string]string {
	languageCode := preferredLanguageCode(r.Header.Get("Accept-Language"))
	selectedTranslations, found := translationCatalog[languageCode]
	if !found {
		selectedTranslations = translationCatalog["en"]
	}
	return selectedTranslations
}

func preferredLanguageCode(acceptLanguageHeader string) string {
	normalizedHeader := strings.ToLower(strings.TrimSpace(acceptLanguageHeader))
	if normalizedHeader == "" {
		return "en"
	}
	bestLanguageCode := "en"
	bestWeight := -1.0
	supportedLanguageCodes := map[string]struct{}{
		"en": {}, "fr": {}, "ru": {}, "ja": {}, "it": {}, "sv": {}, "fi": {}, "mn": {},
		"zh": {}, "he": {}, "fa": {}, "de": {}, "tr": {}, "kk": {}, "es": {}, "pt": {},
	}
	for _, languageEntry := range strings.Split(normalizedHeader, ",") {
		parts := strings.Split(languageEntry, ";")
		baseCode := strings.TrimSpace(strings.Split(strings.TrimSpace(parts[0]), "-")[0])
		if _, supported := supportedLanguageCodes[baseCode]; !supported {
			continue
		}
		weight := 1.0
		for _, parameterEntry := range parts[1:] {
			parameter := strings.TrimSpace(parameterEntry)
			if !strings.HasPrefix(parameter, "q=") {
				continue
			}
			parsedWeight, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(parameter, "q=")), 64)
			if err != nil || parsedWeight < 0 || parsedWeight > 1 {
				continue
			}
			weight = parsedWeight
			break
		}
		if weight > bestWeight {
			bestWeight = weight
			bestLanguageCode = baseCode
		}
	}
	return bestLanguageCode
}

func safeFileName(rawName string) string {
	if rawName == "" {
		return ""
	}
	cleaned := path.Base(rawName)
	if cleaned == "." || strings.Contains(cleaned, "..") {
		return ""
	}
	return cleaned
}

func safeRelativeAssetPath(rawPath string) string {
	cleanedPath := path.Clean("/" + strings.TrimSpace(rawPath))
	cleanedPath = strings.TrimPrefix(cleanedPath, "/")
	if cleanedPath == "" || cleanedPath == "." || strings.Contains(cleanedPath, "..") {
		return ""
	}
	return cleanedPath
}

func contentHashName(fileBytes []byte, extension string) (string, error) {
	if extension == "" {
		return "", errors.New("missing extension")
	}
	hashedBytes := sha256.Sum256(fileBytes)
	return hex.EncodeToString(hashedBytes[:]) + extension, nil
}

func resourceExtension(rawRef string) string {
	parsedRef, err := url.Parse(strings.TrimSpace(rawRef))
	if err == nil && parsedRef.Path != "" {
		return strings.ToLower(path.Ext(parsedRef.Path))
	}
	withoutFragment := strings.SplitN(rawRef, "#", 2)[0]
	withoutQuery := strings.SplitN(withoutFragment, "?", 2)[0]
	return strings.ToLower(path.Ext(withoutQuery))
}

func (a *App) mirrorRemotePage(domain, pagePath, sourceURL string, pageURL *url.URL, fallbackHTML, progressToken string, selectedResourceURLs map[string]struct{}, sourceIP string) string {
	spider := newPageSpider(domain, pageURL, grabResourceMaxDepth, a.grabTracker, progressToken, sourceIP)
	spider.selectedResourceURLs = selectedResourceURLs
	rootResource := &mirroredResource{url: sourceURL, content: []byte(fallbackHTML)}
	spider.resources[sourceURL] = rootResource
	spider.rewriteNestedResources(rootResource, 0, "text/html")
	spider.fetchSelectedResources()
	_ = a.persistSpiderAssets(spider, pagePath)
	a.grabTracker.publish(grabProgressEvent{Token: progressToken, Stage: "done", FoundTotal: spider.foundTotal, DownloadedTotal: spider.downloadedTotal, CompletedPercent: 100})
	return string(rootResource.content)
}

func (a *App) persistSpiderAssets(spider *pageSpider, pagePath string) error {
	baseDir := a.domainFilesDirForDomain(spider.domain)
	_ = os.MkdirAll(baseDir, 0o755)
	ownerPath := cleanPath(pagePath)
	for _, resource := range spider.resources {
		if !resource.persist || strings.TrimSpace(resource.assetPath) == "" {
			continue
		}
		assetReference := resource.assetPath
		if strings.HasPrefix(assetReference, "/p/") {
			assetReference = strings.TrimPrefix(assetReference, "/p/")
		}
		assetReference = safeRelativeAssetPath(assetReference)
		if assetReference == "" {
			continue
		}
		targetFilePath := filepath.Join(baseDir, filepath.FromSlash(assetReference))
		_ = os.MkdirAll(filepath.Dir(targetFilePath), 0o755)
		_ = os.WriteFile(targetFilePath, resource.content, 0o644)
		a.upsertFileMetadata(context.Background(), domainStorageName(spider.domain), assetReference, ownerPath, int64(len(resource.content)), mime.TypeByExtension(path.Ext(assetReference)), "import")
	}
	return nil
}

type mirroredResource struct {
	url       string
	content   []byte
	assetPath string
	persist   bool
}

type pageSpider struct {
	domain               string
	pageURL              *url.URL
	maxDepth             int
	client               *http.Client
	sourceIP             string
	resources            map[string]*mirroredResource
	inFlight             map[string]bool
	selectedResourceURLs map[string]struct{}
	tracker              *grabProgressTracker
	progressToken        string
	foundTotal           int
	downloadedTotal      int
}

var (
	htmlResourcePattern = regexp.MustCompile(`(?is)<(link|script|img|source|video|audio|iframe|embed|object)\b[^>]*(href|src|poster|data)\s*=\s*["']([^"']+)["']`)
	htmlSrcSetPattern   = regexp.MustCompile(`(?is)\bsrcset\s*=\s*["']([^"']+)["']`)
	cssURLPattern       = regexp.MustCompile(`(?is)url\(\s*['"]?([^'")]+)['"]?\s*\)`)
	cssImportPattern    = regexp.MustCompile(`(?is)@import\s+(?:url\(\s*)?['"]?([^'")\s;]+)['"]?`)
	newGrabHTTPClient   = func() *http.Client {
		return &http.Client{Timeout: 20 * time.Second}
	}
)

func newGrabHTTPClientForServerIP(sourceHost, sourceIP string) *http.Client {
	trimmedSourceIP := strings.TrimSpace(sourceIP)
	if trimmedSourceIP == "" {
		return newGrabHTTPClient()
	}
	parsedIP := net.ParseIP(trimmedSourceIP)
	if parsedIP == nil {
		return newGrabHTTPClient()
	}
	sourceHost = strings.TrimSpace(strings.ToLower(sourceHost))
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, splitErr := net.SplitHostPort(address)
		if splitErr == nil && strings.EqualFold(host, sourceHost) {
			address = net.JoinHostPort(parsedIP.String(), port)
		}
		return dialer.DialContext(ctx, network, address)
	}
	return &http.Client{Timeout: 20 * time.Second, Transport: transport}
}

func newPageSpider(domain string, pageURL *url.URL, maxDepth int, tracker *grabProgressTracker, progressToken, sourceIP string) *pageSpider {
	return &pageSpider{
		domain:        domain,
		pageURL:       pageURL,
		maxDepth:      maxDepth,
		client:        newGrabHTTPClientForServerIP(pageURL.Hostname(), sourceIP),
		sourceIP:      sourceIP,
		resources:     make(map[string]*mirroredResource),
		inFlight:      make(map[string]bool),
		tracker:       tracker,
		progressToken: progressToken,
	}
}

func (spider *pageSpider) fetchResource(rawURL string, baseURL *url.URL, depth int, persist bool) (*mirroredResource, error) {
	if depth > spider.maxDepth {
		return nil, errors.New("max depth reached")
	}
	normalizedURL, blocked := spider.normalizeURL(rawURL, baseURL)
	if blocked || normalizedURL == "" {
		return nil, errors.New("unsupported resource url")
	}
	if spider.shouldSkipMirrorResource(normalizedURL) {
		return nil, errors.New("unsupported resource url")
	}
	persist = persist && spider.shouldPersistResource(normalizedURL)
	if existing, found := spider.resources[normalizedURL]; found {
		if persist {
			existing.persist = true
		}
		return existing, nil
	}
	spider.foundTotal++
	spider.publishProgress("found", normalizedURL, 0)
	if spider.inFlight[normalizedURL] {
		return nil, errors.New("resource is already in flight")
	}
	spider.inFlight[normalizedURL] = true
	defer delete(spider.inFlight, normalizedURL)
	request, _ := http.NewRequest(http.MethodGet, normalizedURL, nil)
	response, err := spider.client.Do(request)
	if err != nil {
		spider.resources[normalizedURL] = &mirroredResource{url: normalizedURL, persist: persist}
		spider.publishResourceProgress("error", normalizedURL, 0, 0, -1)
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		spider.resources[normalizedURL] = &mirroredResource{url: normalizedURL, persist: persist}
		spider.publishResourceProgress("error", normalizedURL, 0, 0, response.ContentLength)
		return nil, fmt.Errorf("resource download failed: %s", response.Status)
	}
	if !spider.isAllowedResourceContentType(normalizedURL, response.Header.Get("Content-Type")) {
		spider.resources[normalizedURL] = &mirroredResource{url: normalizedURL, persist: persist}
		spider.publishResourceProgress("error", normalizedURL, 0, 0, response.ContentLength)
		return nil, fmt.Errorf("resource content-type rejected: %s", response.Header.Get("Content-Type"))
	}
	body, err := spider.readResourceBody(response.Body, normalizedURL, response.ContentLength)
	if err != nil {
		spider.resources[normalizedURL] = &mirroredResource{url: normalizedURL, persist: persist}
		spider.publishResourceProgress("error", normalizedURL, 0, 0, response.ContentLength)
		return nil, err
	}
	resource := &mirroredResource{url: normalizedURL, content: body, persist: persist}
	if persist {
		assetPath := spider.assetPathFor(body, normalizedURL)
		if assetPath != "" {
			resource.assetPath = assetPath
		}
	}
	spider.resources[normalizedURL] = resource
	spider.downloadedTotal++
	spider.publishResourceProgress("downloaded", normalizedURL, 100, int64(len(body)), response.ContentLength)
	spider.rewriteNestedResources(resource, depth+1, response.Header.Get("Content-Type"))
	return resource, nil
}

func (spider *pageSpider) publishProgress(stage, currentURL string, currentPercent int) {
	spider.publishResourceProgress(stage, currentURL, currentPercent, 0, -1)
}

func (spider *pageSpider) publishResourceProgress(stage, currentURL string, currentPercent int, downloadedBytes, sizeBytes int64) {
	if spider.tracker == nil || strings.TrimSpace(spider.progressToken) == "" {
		return
	}
	completedPercent := 0
	if spider.foundTotal > 0 {
		completedPercent = spider.downloadedTotal * 100 / spider.foundTotal
	}
	spider.tracker.publish(grabProgressEvent{
		Token: spider.progressToken, Stage: stage, FoundTotal: spider.foundTotal, DownloadedTotal: spider.downloadedTotal,
		CurrentURL: currentURL, CurrentPercent: currentPercent, CurrentDownloadedBytes: downloadedBytes, CurrentSizeBytes: sizeBytes, CompletedPercent: completedPercent,
	})
}

func (spider *pageSpider) readResourceBody(reader io.Reader, resourceURL string, sizeBytes int64) ([]byte, error) {
	var bodyBuffer bytes.Buffer
	buffer := make([]byte, 32*1024)
	downloadedBytes := int64(0)
	lastPercent := -1
	spider.publishResourceProgress("downloading", resourceURL, 0, 0, sizeBytes)
	for {
		readCount, readErr := reader.Read(buffer)
		if readCount > 0 {
			downloadedBytes += int64(readCount)
			if _, writeErr := bodyBuffer.Write(buffer[:readCount]); writeErr != nil {
				return nil, writeErr
			}
			currentPercent := resourcePercent(downloadedBytes, sizeBytes)
			if currentPercent != lastPercent {
				spider.publishResourceProgress("downloading", resourceURL, currentPercent, downloadedBytes, sizeBytes)
				lastPercent = currentPercent
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	return bodyBuffer.Bytes(), nil
}

func resourcePercent(downloadedBytes, sizeBytes int64) int {
	if sizeBytes <= 0 {
		return 0
	}
	percent := int(downloadedBytes * 100 / sizeBytes)
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

func (spider *pageSpider) fetchSelectedResources() {
	if spider.selectedResourceURLs == nil || len(spider.selectedResourceURLs) == 0 {
		return
	}
	selectedURLs := make([]string, 0, len(spider.selectedResourceURLs))
	for selectedURL := range spider.selectedResourceURLs {
		selectedURLs = append(selectedURLs, selectedURL)
	}
	sort.Strings(selectedURLs)
	for _, selectedURL := range selectedURLs {
		if existing, found := spider.resources[selectedURL]; found && existing.persist {
			continue
		}
		_, _ = spider.fetchResource(selectedURL, spider.pageURL, 0, true)
	}
}

func (spider *pageSpider) rewriteNestedResources(resource *mirroredResource, depth int, contentType string) {
	isHTML := strings.Contains(contentType, "text/html")
	isCSS := strings.Contains(contentType, "text/css")
	if !(isHTML || isCSS) {
		return
	}
	source := string(resource.content)
	rewritten := spider.rewriteTextReferences(source, resource.url, depth)
	resource.content = []byte(rewritten)
}

func (spider *pageSpider) rewriteTextReferences(source, baseRawURL string, depth int) string {
	baseURL, _ := url.Parse(baseRawURL)
	rewriteSingle := func(rawRef string) string {
		normalizedURL, blocked := spider.normalizeURL(rawRef, baseURL)
		if blocked || normalizedURL == "" {
			return rawRef
		}
		if !spider.shouldPersistResource(normalizedURL) {
			return normalizedURL
		}
		dependency, err := spider.fetchResource(rawRef, baseURL, depth, true)
		if err != nil || dependency == nil || dependency.assetPath == "" {
			return normalizedURL
		}
		return dependency.assetPath
	}
	rewritten := htmlResourcePattern.ReplaceAllStringFunc(source, func(match string) string {
		parts := htmlResourcePattern.FindStringSubmatch(match)
		if len(parts) != 4 {
			return match
		}
		tagName := strings.ToLower(strings.TrimSpace(parts[1]))
		normalizedURL, blocked := spider.normalizeURL(parts[3], baseURL)
		if !blocked && spider.shouldBlankEmbeddedDocumentReference(tagName, normalizedURL) {
			return strings.Replace(match, parts[3], "about:blank", 1)
		}
		return strings.Replace(match, parts[3], rewriteSingle(parts[3]), 1)
	})
	rewritten = htmlSrcSetPattern.ReplaceAllStringFunc(rewritten, func(match string) string {
		parts := htmlSrcSetPattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		candidates := strings.Split(parts[1], ",")
		for index, candidate := range candidates {
			fields := strings.Fields(strings.TrimSpace(candidate))
			if len(fields) == 0 {
				continue
			}
			fields[0] = rewriteSingle(fields[0])
			candidates[index] = strings.Join(fields, " ")
		}
		return strings.Replace(match, parts[1], strings.Join(candidates, ", "), 1)
	})
	rewritten = cssImportPattern.ReplaceAllStringFunc(rewritten, func(match string) string {
		parts := cssImportPattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		return strings.Replace(match, parts[1], rewriteSingle(parts[1]), 1)
	})
	rewritten = rewriteCSSURLReferences(rewritten, rewriteSingle)
	return rewritten
}

func (spider *pageSpider) shouldBlankEmbeddedDocumentReference(tagName, normalizedURL string) bool {
	switch tagName {
	case "iframe", "embed", "object":
	default:
		return false
	}
	if normalizedURL == "" || spider.shouldSkipMirrorResource(normalizedURL) {
		return false
	}
	parsedURL, err := url.Parse(normalizedURL)
	if err != nil {
		return false
	}
	if spider.pageURL == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(parsedURL.Hostname()), strings.TrimSpace(spider.pageURL.Hostname())) {
		return false
	}
	extension := strings.ToLower(path.Ext(parsedURL.Path))
	if extension == "" {
		return true
	}
	switch extension {
	case ".htm", ".html", ".xhtml", ".php", ".asp", ".aspx", ".jsp", ".cgi":
		return true
	default:
		return false
	}
}

func rewriteCSSURLReferences(source string, rewriteSingle func(string) string) string {
	matches := cssURLPattern.FindAllStringSubmatchIndex(source, -1)
	if len(matches) == 0 {
		return source
	}
	var rewritten strings.Builder
	lastEnd := 0
	for _, match := range matches {
		if len(match) != 4 {
			continue
		}
		matchStart := match[0]
		matchEnd := match[1]
		referenceStart := match[2]
		referenceEnd := match[3]
		rewritten.WriteString(source[lastEnd:matchStart])
		if isCSSImportURL(source, matchStart) {
			rewritten.WriteString(source[matchStart:matchEnd])
		} else {
			rewritten.WriteString(source[matchStart:referenceStart])
			rewritten.WriteString(rewriteSingle(source[referenceStart:referenceEnd]))
			rewritten.WriteString(source[referenceEnd:matchEnd])
		}
		lastEnd = matchEnd
	}
	rewritten.WriteString(source[lastEnd:])
	return rewritten.String()
}

func isCSSImportURL(source string, urlStart int) bool {
	prefixStart := strings.LastIndexAny(source[:urlStart], ";{}")
	statementPrefix := strings.ToLower(strings.TrimSpace(source[prefixStart+1 : urlStart]))
	return strings.HasPrefix(statementPrefix, "@import")
}

func (spider *pageSpider) normalizeURL(rawRef string, baseURL *url.URL) (string, bool) {
	trimmedRef := strings.TrimSpace(rawRef)
	if trimmedRef == "" || strings.HasPrefix(trimmedRef, "#") {
		return "", true
	}
	if isSuspiciousGrabReference(trimmedRef) {
		return "", true
	}
	loweredRef := strings.ToLower(trimmedRef)
	for _, blockedPrefix := range []string{"mailto:", "tel:", "javascript:", "data:", "blob:"} {
		if strings.HasPrefix(loweredRef, blockedPrefix) {
			return "", true
		}
	}
	parsedRef, err := url.Parse(trimmedRef)
	if err != nil {
		return "", true
	}
	resolved := baseURL.ResolveReference(parsedRef)
	if resolved == nil || resolved.Scheme == "" {
		return "", true
	}
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return "", true
	}
	resolved.Fragment = ""
	resolved.ForceQuery = false
	return resolved.String(), false
}

func isSuspiciousGrabReference(rawRef string) bool {
	loweredRef := strings.ToLower(strings.TrimSpace(rawRef))
	if loweredRef == "" {
		return true
	}
	if strings.Contains(loweredRef, "${") || strings.ContainsAny(loweredRef, "+()[],") {
		return true
	}
	for _, blockedFragment := range []string{"this.", ".src", ".url", "params", "videoid", "void"} {
		if strings.Contains(loweredRef, blockedFragment) {
			return true
		}
	}
	return false
}

func (spider *pageSpider) shouldSkipMirrorResource(resourceURL string) bool {
	parsedURL, err := url.Parse(resourceURL)
	if err != nil {
		return true
	}
	hostName := strings.ToLower(strings.TrimSpace(parsedURL.Hostname()))
	pathName := strings.ToLower(strings.TrimSpace(parsedURL.Path))
	if hostName == "" {
		return false
	}
	for _, blockedHostFragment := range []string{"youtube.com", "youtu.be", "youtube-nocookie.com", "googlevideo.com", "doubleclick.net", "googletagmanager.com", "google-analytics.com", "adservice.google.com", "connect.facebook.net", "facebook.com", "clarity.ms", "hotjar.com", "segment.com", "mixpanel.com", "matomo", "outbrain.com", "taboola.com"} {
		if strings.Contains(hostName, blockedHostFragment) {
			return true
		}
	}
	for _, blockedPathFragment := range []string{"/analytics", "/gtag", "/gtm", "/ads", "/adservice", "/pixel", "/tracking"} {
		if strings.Contains(pathName, blockedPathFragment) {
			return true
		}
	}
	return false
}

func (spider *pageSpider) isAllowedResourceContentType(resourceURL, contentTypeHeader string) bool {
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(contentTypeHeader, ";")[0]))
	if contentType == "" {
		return hasAllowedGrabResourceExtension(resourceURL)
	}
	switch resourceKindFromURL(resourceURL) {
	case "style":
		return contentType == "text/css"
	case "script":
		return strings.Contains(contentType, "javascript") || strings.Contains(contentType, "ecmascript")
	case "font":
		return strings.HasPrefix(contentType, "font/") || strings.Contains(contentType, "font") || strings.Contains(contentType, "octet-stream")
	case "image":
		return strings.HasPrefix(contentType, "image/")
	case "video":
		return strings.HasPrefix(contentType, "video/")
	case "audio":
		return strings.HasPrefix(contentType, "audio/")
	default:
		return false
	}
}

func resourceKindFromURL(resourceURL string) string {
	extension := resourceExtension(resourceURL)
	switch extension {
	case ".css":
		return "style"
	case ".js", ".mjs":
		return "script"
	case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".ico":
		return "image"
	case ".woff", ".woff2", ".ttf", ".eot", ".otf":
		return "font"
	case ".mp4", ".webm", ".mov":
		return "video"
	case ".mp3", ".ogg", ".wav":
		return "audio"
	default:
		return ""
	}
}

func hasAllowedGrabResourceExtension(resourceURL string) bool {
	return resourceKindFromURL(resourceURL) != ""
}

func (spider *pageSpider) shouldPersistResource(normalizedURL string) bool {
	if spider.selectedResourceURLs == nil {
		return true
	}
	if len(spider.selectedResourceURLs) == 0 {
		return false
	}
	_, selected := spider.selectedResourceURLs[normalizedURL]
	return selected
}

func (spider *pageSpider) assetPathFor(fileBytes []byte, sourceURL string) string {
	extension := resourceExtension(sourceURL)
	if extension == "" {
		extension = ".bin"
	}
	hashedFileName, err := contentHashName(fileBytes, extension)
	if err != nil {
		return ""
	}
	return "/p/" + hashedFileName
}

type grabTrackerRequest struct {
	action string
	token  string
	stream chan grabProgressEvent
	event  grabProgressEvent
}

type grabProgressTracker struct {
	requests chan grabTrackerRequest
}

type webSocketTextWriter struct {
	connection net.Conn
}

func newGrabProgressTracker() *grabProgressTracker {
	tracker := &grabProgressTracker{requests: make(chan grabTrackerRequest)}
	go tracker.loop()
	return tracker
}

func (tracker *grabProgressTracker) subscribe(token string) chan grabProgressEvent {
	stream := make(chan grabProgressEvent, 32)
	tracker.requests <- grabTrackerRequest{action: "subscribe", token: token, stream: stream}
	return stream
}
func (tracker *grabProgressTracker) unsubscribe(token string, stream chan grabProgressEvent) {
	tracker.requests <- grabTrackerRequest{action: "unsubscribe", token: token, stream: stream}
}
func (tracker *grabProgressTracker) publish(event grabProgressEvent) {
	tracker.requests <- grabTrackerRequest{action: "publish", token: event.Token, event: event}
}
func (tracker *grabProgressTracker) loop() {
	subscribersByToken := make(map[string]map[chan grabProgressEvent]struct{})
	for request := range tracker.requests {
		switch request.action {
		case "subscribe":
			if _, exists := subscribersByToken[request.token]; !exists {
				subscribersByToken[request.token] = make(map[chan grabProgressEvent]struct{})
			}
			subscribersByToken[request.token][request.stream] = struct{}{}
		case "unsubscribe":
			group := subscribersByToken[request.token]
			delete(group, request.stream)
			close(request.stream)
		case "publish":
			for stream := range subscribersByToken[request.token] {
				select {
				case stream <- request.event:
				default:
				}
			}
		}
	}
}

func upgradeToWebSocket(w http.ResponseWriter, r *http.Request) (*webSocketTextWriter, error) {
	if !strings.EqualFold(r.Header.Get("Connection"), "Upgrade") && !strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
		return nil, errors.New("missing websocket upgrade")
	}
	webSocketKey := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if webSocketKey == "" {
		return nil, errors.New("missing websocket key")
	}
	acceptSeed := webSocketKey + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	acceptHash := sha1.Sum([]byte(acceptSeed))
	acceptValue := base64.StdEncoding.EncodeToString(acceptHash[:])
	hijacker, isHijacker := w.(http.Hijacker)
	if !isHijacker {
		return nil, errors.New("hijack is not supported")
	}
	connection, bufferedReadWriter, err := hijacker.Hijack()
	if err != nil {
		return nil, err
	}
	if _, err := bufferedReadWriter.WriteString("HTTP/1.1 101 Switching Protocols\r\n"); err != nil {
		connection.Close()
		return nil, err
	}
	_, _ = bufferedReadWriter.WriteString("Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + acceptValue + "\r\n\r\n")
	_ = bufferedReadWriter.Flush()
	return &webSocketTextWriter{connection: connection}, nil
}

func (writer *webSocketTextWriter) Close() error { return writer.connection.Close() }
func (writer *webSocketTextWriter) WriteText(payload []byte) error {
	header := []byte{0x81}
	payloadLength := len(payload)
	if payloadLength < 126 {
		header = append(header, byte(payloadLength))
	} else if payloadLength <= 65535 {
		header = append(header, 126, 0, 0)
		binary.BigEndian.PutUint16(header[len(header)-2:], uint16(payloadLength))
	} else {
		header = append(header, 127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[len(header)-8:], uint64(payloadLength))
	}
	if _, err := writer.connection.Write(header); err != nil {
		return err
	}
	_, err := writer.connection.Write(payload)
	return err
}

func domainFromRequest(r *http.Request) string {
	host := strings.TrimSpace(r.Host)
	if parsedHost, _, splitErr := net.SplitHostPort(host); splitErr == nil {
		host = parsedHost
	} else if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.Trim(host, "[]")
	}
	host = canonicalLocalDomain(host)
	if host == "" {
		return "localhost"
	}
	return host
}

func canonicalLocalDomain(rawDomain string) string {
	cleanDomain := strings.ToLower(strings.TrimSpace(rawDomain))
	cleanDomain = strings.Trim(cleanDomain, "[]")
	if cleanDomain == "" {
		return ""
	}
	if cleanDomain == "localhost" {
		return "localhost"
	}
	parsedIP := net.ParseIP(cleanDomain)
	if parsedIP != nil && parsedIP.IsLoopback() {
		return "localhost"
	}
	return cleanDomain
}

func (a *App) siteDomain(ctx context.Context, r *http.Request) string {
	requestDomain := domainFromRequest(r)
	var primaryDomain string
	err := a.db.QueryRowContext(ctx, `SELECT primary_domain FROM domain_aliases WHERE alias_domain=? AND is_verified=1 AND dns_a_ok=1`, requestDomain).Scan(&primaryDomain)
	if err != nil || strings.TrimSpace(primaryDomain) == "" {
		return requestDomain
	}
	return primaryDomain
}

var lookupTXTRecords = net.LookupTXT
var lookupIPRecords = net.LookupIP
var lookupServerExternalIP = detectServerExternalIP

func detectServerExternalIP(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("external IP service returned %s", response.Status)
	}
	bodyBytes, err := io.ReadAll(io.LimitReader(response.Body, 128))
	if err != nil {
		return "", err
	}
	externalIP := strings.TrimSpace(string(bodyBytes))
	if net.ParseIP(externalIP) == nil {
		return "", fmt.Errorf("external IP service returned invalid address %q", externalIP)
	}
	return externalIP, nil
}

func normalizeDomainName(rawDomain string) string {
	cleanDomain := strings.ToLower(strings.TrimSpace(rawDomain))
	if cleanDomain == "" {
		return ""
	}
	if strings.Contains(cleanDomain, "://") {
		parsedURL, err := url.Parse(cleanDomain)
		if err == nil {
			cleanDomain = parsedURL.Host
		}
	}
	cleanDomain = strings.TrimPrefix(cleanDomain, "//")
	if strings.Contains(cleanDomain, "/") {
		cleanDomain = strings.Split(cleanDomain, "/")[0]
	}
	if strings.Contains(cleanDomain, "@") {
		cleanDomain = cleanDomain[strings.LastIndex(cleanDomain, "@")+1:]
	}
	if host, _, splitErr := net.SplitHostPort(cleanDomain); splitErr == nil {
		cleanDomain = host
	}
	cleanDomain = strings.Trim(cleanDomain, ". ")
	if cleanDomain == "" || strings.ContainsAny(cleanDomain, " \t\r\n") {
		return ""
	}
	if net.ParseIP(cleanDomain) != nil {
		return ""
	}
	domainParts := strings.Split(cleanDomain, ".")
	if len(domainParts) < 2 {
		return ""
	}
	for _, domainPart := range domainParts {
		if domainPart == "" || len(domainPart) > 63 {
			return ""
		}
		for _, domainRune := range domainPart {
			if (domainRune >= 'a' && domainRune <= 'z') || (domainRune >= '0' && domainRune <= '9') || domainRune == '-' {
				continue
			}
			return ""
		}
		if strings.HasPrefix(domainPart, "-") || strings.HasSuffix(domainPart, "-") {
			return ""
		}
	}
	return cleanDomain
}

func (a *App) handleDomainSettingsPost(ctx context.Context, r *http.Request, siteDomain string, externalIP string) {
	action := strings.TrimSpace(r.FormValue("action"))
	switch action {
	case "add_alias":
		aliasDomain := normalizeDomainName(r.FormValue("alias_domain"))
		if aliasDomain == "" || aliasDomain == siteDomain {
			return
		}
		if a.domainAliasCount(ctx, siteDomain) >= 10 {
			return
		}
		token := randomAccessToken()
		_, _ = a.db.ExecContext(ctx, `INSERT INTO domain_aliases(primary_domain,alias_domain,verification_token,is_verified,dns_a_ok,is_selected,last_checked_at)
VALUES(?,?,?,?,?,?,?)
ON CONFLICT(alias_domain) DO UPDATE SET
	primary_domain=excluded.primary_domain,
	verification_token=CASE WHEN domain_aliases.primary_domain=excluded.primary_domain AND TRIM(COALESCE(domain_aliases.verification_token,''))<>'' THEN domain_aliases.verification_token ELSE excluded.verification_token END,
	is_verified=0,
	dns_a_ok=0,
	is_selected=0,
	last_checked_at=''`,
			siteDomain, aliasDomain, token, 0, 0, 0, "")
		a.refreshDomainAliasVerification(ctx, siteDomain, aliasDomain, externalIP)
	case "delete_alias":
		aliasDomain := normalizeDomainName(r.FormValue("alias_domain"))
		_, _ = a.db.ExecContext(ctx, `DELETE FROM domain_aliases WHERE primary_domain=? AND alias_domain=?`, siteDomain, aliasDomain)
	case "select_alias":
		aliasDomain := normalizeDomainName(r.FormValue("alias_domain"))
		a.refreshDomainAliasVerification(ctx, siteDomain, aliasDomain, externalIP)
		if a.domainAliasIsActive(ctx, siteDomain, aliasDomain) {
			_, _ = a.db.ExecContext(ctx, `UPDATE domain_aliases SET is_selected=0 WHERE primary_domain=?`, siteDomain)
			_, _ = a.db.ExecContext(ctx, `UPDATE domain_aliases SET is_selected=1 WHERE primary_domain=? AND alias_domain=?`, siteDomain, aliasDomain)
		}
	case "select_primary":
		_, _ = a.db.ExecContext(ctx, `UPDATE domain_aliases SET is_selected=0 WHERE primary_domain=?`, siteDomain)
	case "check_alias":
		aliasDomain := normalizeDomainName(r.FormValue("alias_domain"))
		a.refreshDomainAliasVerification(ctx, siteDomain, aliasDomain, externalIP)
	case "check_all":
		a.refreshDomainAliases(ctx, siteDomain, externalIP)
	}
}

func (a *App) domainAliasCount(ctx context.Context, siteDomain string) int {
	var aliasCount int
	_ = a.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM domain_aliases WHERE primary_domain=?`, siteDomain).Scan(&aliasCount)
	return aliasCount
}

func (a *App) domainAliasIsActive(ctx context.Context, siteDomain string, aliasDomain string) bool {
	var activeCount int
	_ = a.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM domain_aliases WHERE primary_domain=? AND alias_domain=? AND is_verified=1 AND dns_a_ok=1`, siteDomain, aliasDomain).Scan(&activeCount)
	return activeCount > 0
}

func (a *App) refreshDomainAliases(ctx context.Context, siteDomain string, externalIP string) {
	aliasRows, err := a.db.QueryContext(ctx, `SELECT alias_domain FROM domain_aliases WHERE primary_domain=? ORDER BY alias_domain`, siteDomain)
	if err != nil {
		return
	}
	defer aliasRows.Close()
	for aliasRows.Next() {
		var aliasDomain string
		if scanErr := aliasRows.Scan(&aliasDomain); scanErr == nil {
			a.refreshDomainAliasVerification(ctx, siteDomain, aliasDomain, externalIP)
		}
	}
}

func (a *App) refreshDomainAliasVerification(ctx context.Context, siteDomain string, aliasDomain string, externalIP string) {
	if aliasDomain == "" {
		return
	}
	var verificationTokenValue sql.NullString
	err := a.db.QueryRowContext(ctx, `SELECT verification_token FROM domain_aliases WHERE primary_domain=? AND alias_domain=?`, siteDomain, aliasDomain).Scan(&verificationTokenValue)
	if err != nil {
		return
	}
	verificationToken := verificationTokenValue.String
	if strings.TrimSpace(verificationToken) == "" {
		verificationToken = randomAccessToken()
		_, _ = a.db.ExecContext(ctx, `UPDATE domain_aliases SET verification_token=? WHERE primary_domain=? AND alias_domain=?`, verificationToken, siteDomain, aliasDomain)
	}
	txtVerified := domainTXTRecordMatches(aliasDomain, verificationToken)
	aRecordVerified := domainARecordMatches(aliasDomain, externalIP)
	lastCheckedAt := time.Now().Format(time.RFC3339)
	_, _ = a.db.ExecContext(ctx, `UPDATE domain_aliases SET is_verified=?, dns_a_ok=?, last_checked_at=? WHERE primary_domain=? AND alias_domain=?`,
		boolToInt(txtVerified), boolToInt(aRecordVerified), lastCheckedAt, siteDomain, aliasDomain)
	if !txtVerified || !aRecordVerified {
		_, _ = a.db.ExecContext(ctx, `UPDATE domain_aliases SET is_selected=0 WHERE primary_domain=? AND alias_domain=?`, siteDomain, aliasDomain)
	}
}

func domainTXTRecordMatches(aliasDomain string, verificationToken string) bool {
	txtRecords, err := lookupTXTRecords(aliasDomain)
	if err != nil {
		return false
	}
	requiredRecord := "sitebrush=" + strings.TrimSpace(verificationToken)
	for _, txtRecord := range txtRecords {
		if strings.TrimSpace(txtRecord) == requiredRecord {
			return true
		}
	}
	return false
}

func domainARecordMatches(aliasDomain string, externalIP string) bool {
	parsedExternalIP := net.ParseIP(strings.TrimSpace(externalIP))
	if parsedExternalIP == nil {
		return false
	}
	ipRecords, err := lookupIPRecords(aliasDomain)
	if err != nil {
		return false
	}
	for _, ipRecord := range ipRecords {
		if ipRecord.Equal(parsedExternalIP) {
			return true
		}
	}
	return false
}

func (a *App) listDomainAliases(ctx context.Context, siteDomain string) ([]DomainAlias, error) {
	aliasRows, err := a.db.QueryContext(ctx, `SELECT alias_domain,verification_token,is_verified,dns_a_ok,is_selected,last_checked_at FROM domain_aliases WHERE primary_domain=? ORDER BY alias_domain`, siteDomain)
	if err != nil {
		return nil, err
	}
	defer aliasRows.Close()
	domainAliases := make([]DomainAlias, 0, 10)
	for aliasRows.Next() {
		var domainAlias DomainAlias
		var verificationToken sql.NullString
		var lastCheckedAt sql.NullString
		var txtVerified int
		var aRecordVerified int
		var isSelected int
		if scanErr := aliasRows.Scan(&domainAlias.Domain, &verificationToken, &txtVerified, &aRecordVerified, &isSelected, &lastCheckedAt); scanErr != nil {
			return nil, scanErr
		}
		domainAlias.VerificationToken = verificationToken.String
		domainAlias.LastCheckedAt = lastCheckedAt.String
		domainAlias.TXTVerified = txtVerified == 1
		domainAlias.ARecordVerified = aRecordVerified == 1
		domainAlias.IsSelected = isSelected == 1
		domainAlias.IsActive = domainAlias.TXTVerified && domainAlias.ARecordVerified
		domainAliases = append(domainAliases, domainAlias)
	}
	return domainAliases, aliasRows.Err()
}

func boolToInt(state bool) int {
	if state {
		return 1
	}
	return 0
}

func (a *App) domainSettingsPage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	siteDomain := a.siteDomain(r.Context(), r)
	returnPath := r.FormValue("return")
	if strings.TrimSpace(returnPath) == "" {
		returnPath = r.URL.Query().Get("return")
	}
	if strings.TrimSpace(returnPath) == "" {
		returnPath = requestedReturnPath(r)
	}
	if r.Method == http.MethodPost {
		externalIP := ""
		action := strings.TrimSpace(r.FormValue("action"))
		if action == "add_alias" || action == "select_alias" || action == "check_alias" || action == "check_all" {
			externalIP, _ = lookupServerExternalIP(r.Context())
		}
		a.handleDomainSettingsPost(r.Context(), r, siteDomain, externalIP)
		http.Redirect(w, r, returnPath+"?settings", http.StatusFound)
		return
	}
	externalIP, externalIPErr := lookupServerExternalIP(r.Context())
	domainAliases, err := a.listDomainAliases(r.Context(), siteDomain)
	if err != nil {
		http.Error(w, "failed to load domain aliases", http.StatusInternalServerError)
		return
	}
	selectedDomain := siteDomain
	for _, domainAlias := range domainAliases {
		if domainAlias.IsSelected && domainAlias.IsActive {
			selectedDomain = domainAlias.Domain
			break
		}
	}
	externalIPError := ""
	if externalIPErr != nil {
		externalIPError = externalIPErr.Error()
	}
	a.render(w, r, "domain_settings.html", map[string]any{
		"Domain":          siteDomain,
		"SelectedDomain":  selectedDomain,
		"Aliases":         domainAliases,
		"AliasCount":      len(domainAliases),
		"CanAddAlias":     len(domainAliases) < 10,
		"ReturnPath":      returnPath,
		"ExternalIP":      externalIP,
		"ExternalIPError": externalIPError,
	})
}

func (a *App) freezeDomain(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	domain := a.siteDomain(r.Context(), r)
	a.setDomainFrozenState(r.Context(), domain, 1)
	http.Redirect(w, r, requestedReturnPath(r), http.StatusFound)
}

func (a *App) publishDomain(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	domain := a.siteDomain(r.Context(), r)
	log.Printf("publish started domain=%s", domain)
	revisionRows, err := a.db.QueryContext(r.Context(), `SELECT page_path,html FROM revisions WHERE domain=? AND is_active=1 ORDER BY page_path ASC, id DESC`, domain)
	if err == nil {
		defer revisionRows.Close()
		latestRevisionByPath := make(map[string]string)
		for revisionRows.Next() {
			var pagePath string
			var pageHTML string
			if scanErr := revisionRows.Scan(&pagePath, &pageHTML); scanErr != nil {
				continue
			}
			if _, alreadyStored := latestRevisionByPath[pagePath]; alreadyStored {
				continue
			}
			latestRevisionByPath[pagePath] = pageHTML
		}
		pageRows, pageQueryErr := a.db.QueryContext(r.Context(), `SELECT path,title,html FROM pages WHERE domain=? ORDER BY path ASC`, domain)
		if pageQueryErr == nil {
			defer pageRows.Close()
			updatedPagesCount := 0
			skippedPagesCount := 0
			for pageRows.Next() {
				var pagePath string
				var pageTitle string
				var draftHTML string
				if scanErr := pageRows.Scan(&pagePath, &pageTitle, &draftHTML); scanErr != nil {
					continue
				}
				pageHTMLToPublish := draftHTML
				if latestActiveHTML, foundLatestActiveRevision := latestRevisionByPath[pagePath]; foundLatestActiveRevision {
					pageHTMLToPublish = latestActiveHTML
				}
				if !a.shouldUpdatePublishedPageFile(domain, pagePath, pageHTMLToPublish) {
					skippedPagesCount++
					continue
				}
				_, _ = a.db.ExecContext(r.Context(), `INSERT OR REPLACE INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, domain, pagePath, pageTitle, pageHTMLToPublish)
				a.writePublishedStaticHTML(domain, pagePath, pageHTMLToPublish)
				updatedPagesCount++
				log.Printf("publish page updated domain=%s path=%s", domain, pagePath)
			}
			log.Printf("publish pages processed domain=%s updated=%d unchanged=%d", domain, updatedPagesCount, skippedPagesCount)
		}
	}
	if packErr := a.generateDomainPack(domain); packErr != nil {
		log.Printf("publish pack failed domain=%s error=%v", domain, packErr)
	} else {
		log.Printf("publish pack updated domain=%s", domain)
	}
	a.setDomainFrozenState(r.Context(), domain, 0)
	log.Printf("publish completed domain=%s", domain)
	http.Redirect(w, r, requestedReturnPath(r), http.StatusFound)
}

func (a *App) publishPreviewJSON(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	domain := a.siteDomain(r.Context(), r)
	totalPagesCount, changedPagesCount, changedPagePaths := a.countPublishChanges(r.Context(), domain)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"total": totalPagesCount, "changed": changedPagesCount, "unchanged": totalPagesCount - changedPagesCount, "paths": changedPagePaths})
}

func (a *App) countPublishChanges(ctx context.Context, domain string) (int, int, []string) {
	totalPagesCount := 0
	changedPagesCount := 0
	changedPagePaths := make([]string, 0)
	revisionRows, err := a.db.QueryContext(ctx, `SELECT page_path,html FROM revisions WHERE domain=? AND is_active=1 ORDER BY page_path ASC, id DESC`, domain)
	if err != nil {
		return 0, 0, changedPagePaths
	}
	defer revisionRows.Close()
	latestRevisionByPath := make(map[string]string)
	for revisionRows.Next() {
		var pagePath string
		var pageHTML string
		if scanErr := revisionRows.Scan(&pagePath, &pageHTML); scanErr != nil {
			continue
		}
		if _, alreadyStored := latestRevisionByPath[pagePath]; !alreadyStored {
			latestRevisionByPath[pagePath] = pageHTML
		}
	}
	pageRows, pageQueryErr := a.db.QueryContext(ctx, `SELECT path,title,html FROM pages WHERE domain=? ORDER BY path ASC`, domain)
	if pageQueryErr != nil {
		return 0, 0, changedPagePaths
	}
	defer pageRows.Close()
	for pageRows.Next() {
		totalPagesCount++
		var pagePath, pageTitle, draftHTML string
		if scanErr := pageRows.Scan(&pagePath, &pageTitle, &draftHTML); scanErr != nil {
			continue
		}
		pageHTMLToPublish := draftHTML
		if latestActiveHTML, foundLatestActiveRevision := latestRevisionByPath[pagePath]; foundLatestActiveRevision {
			pageHTMLToPublish = latestActiveHTML
		}
		if a.shouldUpdatePublishedPageFile(domain, pagePath, pageHTMLToPublish) {
			changedPagesCount++
			changedPagePaths = append(changedPagePaths, pagePath)
		}
	}
	return totalPagesCount, changedPagesCount, changedPagePaths
}

func normalizePublishedHTML(html string) string {
	return strings.TrimSpace(strings.ReplaceAll(html, "\r\n", "\n"))
}

func (a *App) shouldUpdatePublishedPageFile(domain, pagePath, nextRenderedHTML string) bool {
	staticFilePath := filepath.Join(a.domainStaticDir(domain), staticRelativePathForPage(pagePath))
	previousRenderedHTMLBytes, readErr := os.ReadFile(staticFilePath)
	if readErr != nil {
		return true
	}
	return normalizePublishedHTML(string(previousRenderedHTMLBytes)) != normalizePublishedHTML(nextRenderedHTML)
}

func (a *App) generateDomainPack(domain string) error {
	domainDirName := domainStorageName(domain)
	packsDirPath := a.packsDir()
	if makeErr := os.MkdirAll(packsDirPath, 0o755); makeErr != nil {
		return makeErr
	}
	packFilePath := filepath.Join(packsDirPath, domainDirName+".zip")
	packFile, createErr := os.Create(packFilePath)
	if createErr != nil {
		return createErr
	}
	defer packFile.Close()
	zipWriter := zip.NewWriter(packFile)
	if addStaticErr := addDirectoryToZip(zipWriter, a.domainStaticDir(domain), filepath.Join("static", domainDirName)); addStaticErr != nil {
		_ = zipWriter.Close()
		return addStaticErr
	}
	if addFilesErr := addDirectoryToZip(zipWriter, a.domainFilesDirForDomain(domain), filepath.Join("files", domainDirName)); addFilesErr != nil {
		_ = zipWriter.Close()
		return addFilesErr
	}
	if closeZipErr := zipWriter.Close(); closeZipErr != nil {
		return closeZipErr
	}
	return nil
}

func addDirectoryToZip(zipWriter *zip.Writer, sourceDirPath, archiveDirPrefix string) error {
	directoryInfo, statErr := os.Stat(sourceDirPath)
	if statErr != nil || !directoryInfo.IsDir() {
		return nil
	}
	return filepath.WalkDir(sourceDirPath, func(currentPath string, currentEntry fs.DirEntry, walkErr error) error {
		if walkErr != nil || currentEntry.IsDir() {
			return walkErr
		}
		relativePath, relErr := filepath.Rel(sourceDirPath, currentPath)
		if relErr != nil {
			return relErr
		}
		archivePath := filepath.ToSlash(filepath.Join(archiveDirPrefix, relativePath))
		archiveFileWriter, createErr := zipWriter.Create(archivePath)
		if createErr != nil {
			return createErr
		}
		sourceFile, openErr := os.Open(currentPath)
		if openErr != nil {
			return openErr
		}
		defer sourceFile.Close()
		_, copyErr := io.Copy(archiveFileWriter, sourceFile)
		return copyErr
	})
}

func (a *App) findPublishedPage(ctx context.Context, domain, pagePath string) (Page, error) {
	var current Page
	err := a.db.QueryRowContext(ctx, `SELECT domain,path,title,html FROM published_pages WHERE domain=? AND path=?`, domain, pagePath).Scan(&current.Domain, &current.Path, &current.Title, &current.HTML)
	return current, err
}

func (a *App) siteTreeJSON(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	domain := a.siteDomain(r.Context(), r)
	pageRows, err := a.db.QueryContext(r.Context(), `SELECT path FROM pages WHERE domain=? ORDER BY path ASC`, domain)
	if err != nil {
		http.Error(w, "failed to read site tree", http.StatusInternalServerError)
		return
	}
	defer pageRows.Close()
	pathList := []string{"/"}
	for pageRows.Next() {
		var pagePath string
		if scanErr := pageRows.Scan(&pagePath); scanErr != nil {
			continue
		}
		pathList = append(pathList, pagePath)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"current_path": requestedReturnPath(r), "paths": pathList})
}

func (a *App) setDomainFrozenState(ctx context.Context, domain string, frozenState int) {
	updateResult, updateErr := a.db.ExecContext(ctx, `UPDATE domain_states SET is_frozen=? WHERE domain=?`, frozenState, domain)
	if updateErr == nil {
		updatedRowsCount, rowsErr := updateResult.RowsAffected()
		if rowsErr == nil && updatedRowsCount > 0 {
			return
		}
	}
	_, _ = a.db.ExecContext(ctx, `INSERT INTO domain_states(domain,is_frozen) VALUES(?,?)`, domain, frozenState)
}

func (a *App) servePublishedStaticFile(w http.ResponseWriter, r *http.Request, domain, pagePath string) bool {
	if a.isAdminRequest(r) {
		return false
	}
	// Serve static page only when there is an active revision for this path.
	var activeRevisionCount int
	countErr := a.db.QueryRowContext(r.Context(), `SELECT COUNT(1) FROM revisions WHERE domain=? AND page_path=? AND is_active=1`, domain, pagePath).Scan(&activeRevisionCount)
	if countErr != nil || activeRevisionCount == 0 {
		return false
	}
	staticFilePath := filepath.Join(a.domainStaticDir(domain), staticRelativePathForPage(pagePath))
	if _, statErr := os.Stat(staticFilePath); statErr != nil {
		return false
	}
	if pageContentKind(pagePath, "") != "html" {
		a.logContentDelivery(w, "static-file")
		http.ServeFile(w, r, staticFilePath)
		return true
	}
	staticContent, readErr := os.ReadFile(staticFilePath)
	if readErr != nil {
		return false
	}
	a.serveManagedPageContent(w, r, pagePath, string(staticContent), "static-file")
	return true
}

func (a *App) writePublishedStaticHTML(domain, pagePath, html string) {
	staticFilePath := filepath.Join(a.domainStaticDir(domain), staticRelativePathForPage(pagePath))
	_ = os.MkdirAll(filepath.Dir(staticFilePath), 0755)
	_ = os.WriteFile(staticFilePath, []byte(html), 0644)
}

func (a *App) removePublishedStaticFile(domain, pagePath string) {
	staticFilePath := filepath.Join(a.domainStaticDir(domain), staticRelativePathForPage(pagePath))
	_ = os.Remove(staticFilePath)
}

func staticRelativePathForPage(pagePath string) string {
	normalizedPath := strings.TrimPrefix(pagePath, "/")
	if normalizedPath == "" {
		return "index.html"
	}
	if strings.HasSuffix(normalizedPath, "/") {
		return normalizedPath + "index.html"
	}
	if extension := strings.ToLower(path.Ext(normalizedPath)); extension != "" {
		return normalizedPath
	}
	return normalizedPath + ".html"
}

func (a *App) domainStaticDir(domain string) string {
	return filepath.Join(a.staticRootDir(), domainStorageName(domain))
}

func (a *App) logContentDelivery(w http.ResponseWriter, sourceType string) {
	contentSource := "dynamic"
	if sourceType == "static-file" {
		contentSource = "static"
	}
	w.Header().Set("X-Sitebrush-Source", contentSource)
}
func domainStorageName(domain string) string {
	return strings.NewReplacer("/", "_", "\\", "_", ":", "_", "..", "_").Replace(domain)
}

func (a *App) domainFilesDir(r *http.Request) string {
	return a.domainFilesDirForDomain(a.siteDomain(r.Context(), r))
}

func (a *App) domainFilesDirForDomain(domain string) string {
	return filepath.Join(a.filesRootDir(), domainStorageName(domain))
}

func (a *App) staticRootDir() string {
	return filepath.Join(a.storageRootDir(), "static")
}

func (a *App) filesRootDir() string {
	return filepath.Join(a.storageRootDir(), "files")
}

func (a *App) packsDir() string {
	return filepath.Join(a.storageRootDir(), "packs")
}

func (a *App) storageRootDir() string {
	storagePath := a.storagePath
	if strings.TrimSpace(storagePath) == "" {
		storagePath = defaultAppStoragePath()
	}
	return filepath.Join(storagePath, "storage")
}

func defaultAppStoragePath() string {
	basePath := defaultBaseAppDataPath()
	return filepath.Join(basePath, storageAppName)
}

func defaultBaseAppDataPath() string {
	switch runtime.GOOS {
	case "darwin":
		homeDir, err := os.UserHomeDir()
		if err == nil && strings.TrimSpace(homeDir) != "" {
			return filepath.Join(homeDir, "Library", "Application Support")
		}
	case "windows":
		localAppDataDir := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if localAppDataDir != "" {
			return localAppDataDir
		}
		roamingAppDataDir := strings.TrimSpace(os.Getenv("APPDATA"))
		if roamingAppDataDir != "" {
			return roamingAppDataDir
		}
	default:
		homeDir, err := os.UserHomeDir()
		if err == nil && strings.TrimSpace(homeDir) != "" {
			return filepath.Join(homeDir, ".sitebrush")
		}
		xdgDataHomeDir := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
		if xdgDataHomeDir != "" {
			return filepath.Join(xdgDataHomeDir, storageAppName)
		}
	}
	if configDir, err := os.UserConfigDir(); err == nil && strings.TrimSpace(configDir) != "" {
		return configDir
	}
	if homeDir, err := os.UserHomeDir(); err == nil && strings.TrimSpace(homeDir) != "" {
		return homeDir
	}
	return "."
}
