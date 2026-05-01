package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"goup/pkg/desktop"
)

//go:embed web/*
var embeddedWebFiles embed.FS
var translationCatalog = loadTranslationCatalog()

const appVersion = "dev"

// App keeps only explicit dependencies to stay readable and easy to swap.
type App struct {
	db *sql.DB
}

type Page struct {
	Domain    string
	Path      string
	Title     string
	HTML      string
	Published int
}

type Revision struct {
	ID        int
	PagePath  string
	HTML      string
	CreatedAt string
	IsActive  int
}

type ManagedFile struct {
	Name string
	Size int64
}

type statusCapturingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (writer *statusCapturingResponseWriter) WriteHeader(statusCode int) {
	writer.statusCode = statusCode
	writer.ResponseWriter.WriteHeader(statusCode)
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
	if strings.HasPrefix(requestPath, "/p/static/") || strings.HasPrefix(requestPath, "/assets/") {
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

func main() {
	port := flag.Int("port", 8080, "HTTP listen port")
	dbType := flag.String("db-type", "sqlite", "database driver (supported: sqlite)")
	dbPath := flag.String("db-path", "sitebrush.db", "path to sqlite database file")
	desktopMode := flag.Bool("desktop", desktop.DefaultEnabled(), "enable desktop mode when desktop build tags are used")
	setupMode := flag.Bool("setup", false, "run interactive Linux setup wizard mode")
	flag.Parse()

	if *dbType != "sqlite" {
		log.Fatalf("unsupported -db-type %q, supported: sqlite", *dbType)
	}
	if *setupMode {
		log.Printf("setup mode requested; run the setup wizard build flow for Linux deployment")
	}

	database, err := sql.Open("sqlite3", "file:"+*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()

	application := &App{db: database}
	if err = application.migrate(context.Background()); err != nil {
		log.Fatal(err)
	}

	router := http.NewServeMux()
	staticFiles, err := fs.Sub(embeddedWebFiles, "web/static")
	if err != nil {
		log.Fatal(err)
	}
	router.Handle("/p/static/", http.StripPrefix("/p/static/", http.FileServer(http.FS(staticFiles))))
	router.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("storage/files"))))
	router.HandleFunc("/setup", application.setupAdmin)
	router.HandleFunc("/login", application.login)
	router.HandleFunc("/logout", application.logout)
	router.HandleFunc("/edit", application.editPage)
	router.HandleFunc("/edit/raw", application.editRawPage)
	router.HandleFunc("/edit/mode", application.editModePage)
	router.HandleFunc("/grab", application.grabPage)
	router.HandleFunc("/files", application.filesPage)
	router.HandleFunc("/save", application.savePage)
	router.HandleFunc("/domain/settings", application.domainSettingsPage)
	router.HandleFunc("/revisions", application.revisionsPage)
	router.HandleFunc("/revision/restore", application.restoreRevision)
	router.HandleFunc("/revision/delete", application.deleteRevision)
	router.HandleFunc("/revision/toggle", application.toggleRevision)
	router.HandleFunc("/", application.route)

	address := "127.0.0.1:" + strconv.Itoa(*port)
	log.Printf("Sitebrush started on http://%s", address)

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- http.ListenAndServe(":"+strconv.Itoa(*port), accessLogMiddleware(router))
	}()

	if *desktopMode {
		if err := desktop.RunWebviewWindow(address, appVersion); err != nil {
			log.Fatal(err)
		}
		return
	}

	if err := <-serverErrors; err != nil {
		log.Fatal(err)
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
		`CREATE TABLE IF NOT EXISTS domain_aliases(primary_domain TEXT,alias_domain TEXT UNIQUE);`,
		`CREATE TABLE IF NOT EXISTS domain_states(domain TEXT PRIMARY KEY,is_frozen INTEGER DEFAULT 0);`,
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
	_, _ = a.db.ExecContext(ctx, `UPDATE users SET domain=? WHERE domain IS NULL OR TRIM(domain)=''`, legacyDomain)
	_, _ = a.db.ExecContext(ctx, `UPDATE pages SET domain=? WHERE domain IS NULL OR TRIM(domain)=''`, legacyDomain)
	_, _ = a.db.ExecContext(ctx, `UPDATE revisions SET domain=? WHERE domain IS NULL OR TRIM(domain)=''`, legacyDomain)
	_, _ = a.db.ExecContext(ctx, `UPDATE revisions SET is_active=1 WHERE is_active IS NULL`)
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

func (a *App) route(w http.ResponseWriter, r *http.Request) {
	pagePath := r.URL.Path
	if hasQueryFlag(r, "tree") {
		a.siteTreeJSON(w, r)
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
		a.logContentDelivery(w, "db-draft")
		_, _ = w.Write([]byte(a.wrapWithMenu(r, pageRecord.Path, pageRecord.HTML)))
		return
	}
	if !isAdmin && a.servePublishedStaticFile(w, r, domain, pagePath) {
		return
	}
	if !isAdmin {
		a.render(w, r, "missing.html", map[string]any{"Path": pagePath, "EditLink": pagePath + "?edit", "IsAdmin": false})
		return
	}
	publishedPage, publishedErr := a.findPublishedPage(r.Context(), domain, pagePath)
	if publishedErr == nil {
		a.logContentDelivery(w, "db-published-fallback")
		_, _ = w.Write([]byte(a.wrapWithMenu(r, publishedPage.Path, publishedPage.HTML)))
		return
	}
	if isAdmin {
		a.render(w, r, "missing.html", map[string]any{"Path": pagePath, "EditLink": pagePath + "?edit", "IsAdmin": true})
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
	if record.Path == "" {
		record = Page{Path: pagePath, Title: pagePath, HTML: a.defaultHTMLForNewPage(r.Context(), domain, pagePath)}
	}
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
	a.render(w, r, "edit_mode.html", map[string]any{"Path": pagePath})
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
		return r.URL.Path + "?edit"
	}
	refererURL, parseErr := url.Parse(strings.TrimSpace(r.Referer()))
	if parseErr == nil && refererURL.Path == r.URL.Path && strings.TrimSpace(refererURL.RawQuery) == "edit" {
		return refererURL.Path + "?edit"
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
	}
	_, _ = a.db.ExecContext(r.Context(), `INSERT INTO revisions(domain,page_path,html,created_at) VALUES(?,?,?,?)`, domain, pagePath, html, time.Now().Format(time.RFC3339))
	a.applyTemplatePropagation(r.Context(), domain, html)
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
	if !strings.HasPrefix(sourceURL, "http://") && !strings.HasPrefix(sourceURL, "https://") {
		http.Error(w, "source_url must start with http:// or https://", http.StatusBadRequest)
		return
	}

	remoteSourceURL, err := url.Parse(sourceURL)
	if err != nil {
		http.Error(w, "source_url is invalid", http.StatusBadRequest)
		return
	}

	response, err := http.Get(sourceURL)
	if err != nil {
		http.Error(w, "failed to download source page", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		http.Error(w, "source page returned non-success status", http.StatusBadGateway)
		return
	}

	htmlBytes, err := io.ReadAll(response.Body)
	if err != nil {
		http.Error(w, "failed to read source page", http.StatusBadGateway)
		return
	}

	domain := a.siteDomain(r.Context(), r)
	html := a.mirrorRemotePage(domain, sourceURL, remoteSourceURL, string(htmlBytes))
	_, _ = a.db.ExecContext(r.Context(), `INSERT OR REPLACE INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, domain, pagePath, pagePath, html)
	if !a.isDomainFrozen(r.Context(), domain) {
		_, _ = a.db.ExecContext(r.Context(), `INSERT OR REPLACE INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, domain, pagePath, pagePath, html)
	}
	_, _ = a.db.ExecContext(r.Context(), `INSERT INTO revisions(domain,page_path,html,created_at) VALUES(?,?,?,?)`, domain, pagePath, html, time.Now().Format(time.RFC3339))
	http.Redirect(w, r, pagePath+"?edit", http.StatusFound)
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
		return
	}
	_, _ = a.db.ExecContext(ctx, `UPDATE pages SET html=? WHERE domain=? AND path=?`, latestActiveHTML, domain, pagePath)
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
	if r.Method == http.MethodPost {
		fileName := safeFileName(r.FormValue("name"))
		if fileName != "" {
			_ = os.Remove(filepath.Join(a.domainFilesDir(r), fileName))
		}
		currentPath := r.URL.Query().Get("path")
		if currentPath == "" {
			currentPath = "/"
		}
		http.Redirect(w, r, currentPath+"?files", http.StatusFound)
		return
	}
	entries, err := os.ReadDir(a.domainFilesDir(r))
	if err != nil {
		a.render(w, r, "files.html", map[string]any{"Path": r.URL.Query().Get("path"), "Files": []ManagedFile{}})
		return
	}
	fileList := make([]ManagedFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		fileInfo, statErr := entry.Info()
		if statErr != nil {
			continue
		}
		fileList = append(fileList, ManagedFile{Name: entry.Name(), Size: fileInfo.Size()})
	}
	currentPath := r.URL.Query().Get("path")
	if currentPath == "" {
		currentPath = r.URL.Path
	}
	if currentPath == "" {
		currentPath = "/"
	}
	a.render(w, r, "files.html", map[string]any{"Path": currentPath, "Files": fileList})
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

func (a *App) wrapWithMenu(r *http.Request, pagePath, html string) string {
	domain := a.siteDomain(r.Context(), r)
	menuScript := buildContextMenuScript(a.isAdminRequest(r), a.isDomainFrozen(r.Context(), domain), pagePath, domain, translationsForRequest(r))
	if strings.Contains(strings.ToLower(html), "</body>") {
		bodyClosePattern := regexp.MustCompile(`(?i)</body>`)
		return bodyClosePattern.ReplaceAllString(html, menuScript+"</body>")
	}
	return html + menuScript
}

func buildContextMenuScript(isAdmin bool, isFrozen bool, pagePath, domain string, translations map[string]string) string {
	escapedPath := template.JSEscapeString(pagePath)
	escapedDomain := template.JSEscapeString(domain)
	confirmFreezePrompt := template.JSEscapeString(translationOrDefault(translations, "confirm_freeze_prompt", "Freeze domain now?"))
	confirmPublishPrompt := template.JSEscapeString(translationOrDefault(translations, "confirm_publish_prompt", "Publish website changes now?"))
	publishConfirmWithChangesLabel := template.JSEscapeString(translationOrDefault(translations, "publish_confirm_with_changes", "Publish the changes made to the site?"))
	publishConfirmWithoutChangesLabel := template.JSEscapeString(translationOrDefault(translations, "publish_confirm_without_changes", "No changes were made. Unfreeze the site?"))
	publishPreviewLoadingLabel := template.JSEscapeString(translationOrDefault(translations, "publish_preview_loading", "Checking changes to publish..."))
	publishPreviewSummaryLabel := template.JSEscapeString(translationOrDefault(translations, "publish_preview_summary", "Changes:"))
	confirmYesLabel := template.JSEscapeString(translationOrDefault(translations, "confirm_yes", "Yes"))
	confirmNoLabel := template.JSEscapeString(translationOrDefault(translations, "confirm_no", "No"))
	editLabel := template.JSEscapeString(translationOrDefault(translations, "menu_edit", "Edit"))
	revisionsLabel := template.JSEscapeString(translationOrDefault(translations, "menu_revisions", "Revisions"))
	filesLabel := template.JSEscapeString(translationOrDefault(translations, "menu_files", "Files"))
	treeLabel := template.JSEscapeString(translationOrDefault(translations, "menu_tree", "Site tree"))
	freezeLabel := template.JSEscapeString(translationOrDefault(translations, "menu_freeze", "Freeze"))
	publishLabel := template.JSEscapeString(translationOrDefault(translations, "menu_publish", "Publish"))
	settingsLabel := template.JSEscapeString(translationOrDefault(translations, "menu_domain_settings", "Domain settings"))
	logoutLabel := template.JSEscapeString(translationOrDefault(translations, "menu_logout", "Sign out"))
	loginLabel := template.JSEscapeString(translationOrDefault(translations, "menu_login", "Sign in"))
	treeModalTitle := template.JSEscapeString(translationOrDefault(translations, "tree_modal_title", "Site tree"))
	treeLoadingLabel := template.JSEscapeString(translationOrDefault(translations, "tree_loading", "Loading site tree..."))
	treeLoadErrorLabel := template.JSEscapeString(translationOrDefault(translations, "tree_load_error", "Failed to load site tree."))
	if isAdmin {
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
    freeze: { path: "?freeze", message: "` + confirmFreezePrompt + `" },
    publish: { path: "?publish", message: "` + confirmPublishPrompt + `" }
  };
  const confirmYesLabel = "` + confirmYesLabel + `";
  const confirmNoLabel = "` + confirmNoLabel + `";
  function openConfirmationDialog(confirmMessageText, onConfirm) {
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
      "<li class='SiteBrushContextMenu'><a href='?edit' class='SiteBrushContextMenuLink'><img src='/p/static/pencil.png' class='SiteBrushMenuIcon' alt=''>" + "` + editLabel + `" + "</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?revisions' class='SiteBrushContextMenuLink'><img src='/p/static/revisions.png' class='SiteBrushMenuIcon' alt=''>" + "` + revisionsLabel + `" + "</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?files' class='SiteBrushContextMenuLink'><img src='/p/static/upload.png' class='SiteBrushMenuIcon' alt=''>" + "` + filesLabel + `" + "</a></li>",
      "<li class='SiteBrushContextMenu'><button type='button' data-sitebrush-action='tree' class='SiteBrushContextMenuLink SiteBrushContextMenuButton'><img src='/p/static/tree.png' class='SiteBrushMenuIcon' alt=''>" + "` + treeLabel + `" + "</button></li>",
      "` + freezeActionEntry + `",
      "<li class='SiteBrushContextMenu'><a href='?settings' class='SiteBrushContextMenuLink'><img src='/p/static/revisions.png' class='SiteBrushMenuIcon' alt=''>" + "` + settingsLabel + `" + "</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?logout' class='SiteBrushContextMenuLink'><img src='/p/static/sign-out.png' class='SiteBrushMenuIcon' alt=''>" + "` + logoutLabel + `" + "</a></li>",
      "<li class='SiteBrushContextMenu ContextMenuCopyright'><a href='http://sitebrush.com' class='SiteBrushContextMenuLink'>sitebrush</a></li>",
      "</ul>"
    ];
    showSitebrushMenu(browserEvent, menuHtmlEntries, currentPagePath, isDomainFrozen);
	  }, {capture: false, passive: false});
  function openSiteTreeDialog() {
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
      "<li class='SiteBrushContextMenu'><a href='?login' class='SiteBrushContextMenuLink'><img src='/p/static/lock.png' class='SiteBrushMenuIcon' alt=''>" + "` + loginLabel + `" + "</a></li>",
      "<li class='SiteBrushContextMenu ContextMenuCopyright'><a href='http://sitebrush.com' class='SiteBrushContextMenuLink'>sitebrush</a></li>",
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
.SiteBrushMenuBox{position:fixed;background:#fff url(/p/static/bg.png) repeat-x top;border:1px solid #8ea4c1;z-index:2147483647;padding:2px;min-width:240px;box-shadow:0 2px 12px rgba(0,0,0,0.2);font-family:Arial,Helvetica,sans-serif}
.SiteBrushMenuBox.SiteBrushMenuBoxFrozen{background:#e9f5ff;border-color:#6da6d4}
.SiteBrushMenuList{list-style:none;margin:0;padding:0}
.SiteBrushContextMenu{margin:0;padding:0}
.SiteBrushContextMenuLink{display:flex;align-items:center;gap:8px;padding:8px 10px;color:#1f3f6f;text-decoration:none;font-family:Arial,Helvetica,sans-serif;font-size:14px;cursor:pointer}
.SiteBrushContextMenuLink:hover{background:#eef5ff}
.SiteBrushContextMenuButton{width:100%;border:0;background:transparent;text-align:left}
.SiteBrushDomainMenuItem .SiteBrushContextMenuLink{font-weight:700;border-bottom:1px solid #c8d5e7}
.ContextMenuCopyright .SiteBrushContextMenuLink{font-size:12px;color:#5b6f8b;border-top:1px solid #c8d5e7;margin-top:2px;padding-top:7px}.SiteBrushMenuIcon{width:16px;height:16px;flex:0 0 16px}
.SiteBrushConfirmOverlay{position:fixed;inset:0;background:rgba(0,0,0,.45);display:flex;align-items:center;justify-content:center;z-index:100000}
.SiteBrushConfirmModal{background:#fff;border:1px solid #8ea4c1;min-width:260px;max-width:340px;padding:16px;font-family:Arial,Helvetica,sans-serif}
.SiteBrushConfirmText{margin:0 0 14px 0;color:#1f3f6f;font-size:14px}
.SiteBrushPublishPreviewList{list-style:none;margin:0 0 12px 0;padding:0;max-height:180px;overflow:auto}
.SiteBrushPublishPreviewListItem{margin:0 0 4px 0}
.SiteBrushPublishPreviewLink{color:#1f3f6f;text-decoration:underline;font-size:13px}
.SiteBrushConfirmActions{display:flex;gap:8px;justify-content:flex-end}
.SiteBrushConfirmButton,.SiteBrushCancelButton{border:1px solid #8ea4c1;background:#f2f7ff;padding:6px 12px;cursor:pointer;font-size:13px}
.SiteBrushTreeOverlay{position:fixed;inset:0;background:rgba(0,0,0,.45);display:flex;align-items:center;justify-content:center;z-index:100000}
.SiteBrushTreeModal{background:#fff;border:1px solid #8ea4c1;min-width:320px;max-width:700px;max-height:80vh;overflow:auto;padding:16px;font-family:Arial,Helvetica,sans-serif}
.SiteBrushTreeTitle{margin:0 0 12px 0;color:#1f3f6f;font-size:18px}
.SiteBrushTreeContent{margin:0 0 12px 0;color:#1f3f6f;font-size:14px}
.SiteBrushTreeList{list-style:none;margin:0;padding-left:16px}
.SiteBrushTreeLink{color:#1f3f6f;text-decoration:none;font-size:14px;line-height:1.6}
.SiteBrushTreeCurrent{font-weight:700;text-decoration:underline}
@media (prefers-color-scheme: dark){
  .SiteBrushMenuBox{background:#172235;border-color:#2f405d}
  .SiteBrushContextMenuLink{color:#dbe8ff}
  .SiteBrushContextMenuLink:hover{background:#24344d}
  .SiteBrushDomainMenuItem .SiteBrushContextMenuLink{border-bottom-color:#2f405d}
  .ContextMenuCopyright .SiteBrushContextMenuLink{color:#a7bbd8;border-top-color:#2f405d}
  .SiteBrushTreeModal{background:#172235;border-color:#2f405d}
  .SiteBrushTreeTitle,.SiteBrushTreeContent,.SiteBrushTreeLink{color:#dbe8ff}
}
</style>
<script>
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
  const existingMenuBox = document.getElementById("SiteBrushMenuBox");
  if (existingMenuBox) {
    existingMenuBox.remove();
  }
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
  document.addEventListener("click", function closeMenuOnClick() {
    const openMenu = document.getElementById("SiteBrushMenuBox");
    if (openMenu) {
      openMenu.remove();
    }
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
	pattern := regexp.MustCompile(`(?s)<([a-zA-Z0-9]+)[^>]*class="[^"]*sitebrush-template-([a-zA-Z0-9_-]+)[^"]*"[^>]*>.*?</[a-zA-Z0-9]+>`)
	matches := pattern.FindAllStringSubmatch(sourceHTML, -1)
	for _, match := range matches {
		templateBlock := match[0]
		templateName := match[2]
		rows, err := a.db.QueryContext(ctx, `SELECT path,html FROM pages WHERE domain=? AND html LIKE ?`, domain, "%sitebrush-template-"+templateName+"%")
		if err != nil {
			continue
		}
		for rows.Next() {
			var pagePath, pageHTML string
			_ = rows.Scan(&pagePath, &pageHTML)
			updatedHTML := replaceTemplateByClass(pageHTML, templateName, templateBlock)
			_, _ = a.db.ExecContext(ctx, `UPDATE pages SET html=? WHERE domain=? AND path=?`, updatedHTML, domain, pagePath)
		}
		_ = rows.Close()
	}
}

func replaceTemplateByClass(pageHTML, templateName, newBlock string) string {
	templatePattern := regexp.MustCompile(`(?s)<([a-zA-Z0-9]+)[^>]*class="[^"]*sitebrush-template-` + regexp.QuoteMeta(templateName) + `[^"]*"[^>]*>.*?</[a-zA-Z0-9]+>`)
	return templatePattern.ReplaceAllString(pageHTML, newBlock)
}

func (a *App) render(w http.ResponseWriter, r *http.Request, templateName string, templateData any) {
	fileBytes, err := fs.ReadFile(embeddedWebFiles, "web/"+templateName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	parsedTemplate := template.Must(template.New(templateName).Parse(string(fileBytes)))
	envelope := map[string]any{"Domain": a.siteDomain(r.Context(), r), "T": translationsForRequest(r)}
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

func contentHashName(fileBytes []byte, extension string) (string, error) {
	if extension == "" {
		return "", errors.New("missing extension")
	}
	hashedBytes := sha256.Sum256(fileBytes)
	return hex.EncodeToString(hashedBytes[:]) + extension, nil
}

func (a *App) mirrorRemotePage(domain, sourceURL string, pageURL *url.URL, fallbackHTML string) string {
	tempDirPath, err := os.MkdirTemp("", "sitebrush-grab-*")
	if err != nil {
		return fallbackHTML
	}
	defer os.RemoveAll(tempDirPath)

	htmlFilePath := filepath.Join(tempDirPath, "page.html")
	command := exec.Command("wget", "--page-requisites", "--convert-links", "--adjust-extension", "--span-hosts", "--no-parent", "--execute", "robots=off", "-O", htmlFilePath, "-P", tempDirPath, sourceURL)
	if command.Run() != nil {
		return fallbackHTML
	}

	renderedHTMLBytes, err := os.ReadFile(htmlFilePath)
	if err != nil {
		return fallbackHTML
	}
	renderedHTML := string(renderedHTMLBytes)

	localFileToAssetPath := a.importMirroredFiles(domain, tempDirPath)
	return rewriteMirroredLinks(renderedHTML, localFileToAssetPath)
}

func (a *App) importMirroredFiles(domain, tempDirPath string) map[string]string {
	localFileToAssetPath := make(map[string]string)
	baseDir := filepath.Join("storage/files", domainStorageName(domain))
	_ = os.MkdirAll(baseDir, 0o755)
	_ = filepath.Walk(tempDirPath, func(currentPath string, fileInfo os.FileInfo, walkErr error) error {
		if walkErr != nil || fileInfo.IsDir() || strings.HasSuffix(currentPath, "page.html") {
			return nil
		}
		fileBytes, err := os.ReadFile(currentPath)
		if err != nil {
			return nil
		}
		fileExtension := strings.ToLower(path.Ext(currentPath))
		if fileExtension == "" {
			fileExtension = ".bin"
		}
		hashedFileName, err := contentHashName(fileBytes, fileExtension)
		if err != nil {
			return nil
		}
		assetPath := filepath.Join(baseDir, hashedFileName)
		if err = os.WriteFile(assetPath, fileBytes, 0o644); err != nil {
			return nil
		}
		relativePath, _ := filepath.Rel(tempDirPath, currentPath)
		localFileToAssetPath[filepath.ToSlash(relativePath)] = "/assets/" + domainStorageName(domain) + "/" + hashedFileName
		return nil
	})
	return localFileToAssetPath
}

func domainFromRequest(r *http.Request) string {
	host := strings.TrimSpace(r.Host)
	if strings.Contains(host, ":") {
		parts := strings.Split(host, ":")
		host = parts[0]
	}
	if host == "" {
		return "localhost"
	}
	return strings.ToLower(host)
}

func (a *App) siteDomain(ctx context.Context, r *http.Request) string {
	requestDomain := domainFromRequest(r)
	var primaryDomain string
	err := a.db.QueryRowContext(ctx, `SELECT primary_domain FROM domain_aliases WHERE alias_domain=?`, requestDomain).Scan(&primaryDomain)
	if err != nil || strings.TrimSpace(primaryDomain) == "" {
		return requestDomain
	}
	return primaryDomain
}

func (a *App) domainSettingsPage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	siteDomain := a.siteDomain(r.Context(), r)
	if r.Method == http.MethodPost {
		rawAliases := strings.Split(r.FormValue("aliases"), "\n")
		cleanAliases := make([]string, 0, 3)
		aliasSeen := make(map[string]struct{})
		for _, rawAlias := range rawAliases {
			normalizedAlias := strings.ToLower(strings.TrimSpace(rawAlias))
			if normalizedAlias == "" || normalizedAlias == siteDomain {
				continue
			}
			if _, exists := aliasSeen[normalizedAlias]; exists {
				continue
			}
			aliasSeen[normalizedAlias] = struct{}{}
			cleanAliases = append(cleanAliases, normalizedAlias)
			if len(cleanAliases) == 3 {
				break
			}
		}
		_, _ = a.db.ExecContext(r.Context(), `DELETE FROM domain_aliases WHERE primary_domain=?`, siteDomain)
		for _, aliasDomain := range cleanAliases {
			_, _ = a.db.ExecContext(r.Context(), `INSERT INTO domain_aliases(primary_domain,alias_domain) VALUES(?,?)`, siteDomain, aliasDomain)
		}
		returnPath := r.FormValue("return")
		if strings.TrimSpace(returnPath) == "" {
			returnPath = "/"
		}
		http.Redirect(w, r, returnPath+"?settings", http.StatusFound)
		return
	}
	aliasRows, err := a.db.QueryContext(r.Context(), `SELECT alias_domain FROM domain_aliases WHERE primary_domain=? ORDER BY alias_domain`, siteDomain)
	if err != nil {
		http.Error(w, "failed to load domain aliases", http.StatusInternalServerError)
		return
	}
	defer aliasRows.Close()
	domainAliases := make([]string, 0, 3)
	for aliasRows.Next() {
		var aliasDomain string
		_ = aliasRows.Scan(&aliasDomain)
		domainAliases = append(domainAliases, aliasDomain)
	}
	returnPath := r.URL.Query().Get("return")
	if strings.TrimSpace(returnPath) == "" {
		returnPath = "/"
	}
	a.render(w, r, "domain_settings.html", map[string]any{"Domain": siteDomain, "Aliases": strings.Join(domainAliases, "\n"), "ReturnPath": returnPath})
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
				renderedPublishedHTML := a.wrapPublishedPageWithGuestMenu(domain, pagePath, pageHTMLToPublish)
				if !a.shouldUpdatePublishedPageFile(domain, pagePath, renderedPublishedHTML) {
					skippedPagesCount++
					continue
				}
				_, _ = a.db.ExecContext(r.Context(), `INSERT OR REPLACE INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, domain, pagePath, pageTitle, pageHTMLToPublish)
				a.writePublishedStaticHTML(domain, pagePath, renderedPublishedHTML)
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
		renderedPublishedHTML := a.wrapPublishedPageWithGuestMenu(domain, pagePath, pageHTMLToPublish)
		if a.shouldUpdatePublishedPageFile(domain, pagePath, renderedPublishedHTML) {
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
	packsDirPath := filepath.Join("storage", "packs")
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
	if addFilesErr := addDirectoryToZip(zipWriter, filepath.Join("storage", "files", domainDirName), filepath.Join("files", domainDirName)); addFilesErr != nil {
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
	staticFilePath := filepath.Join(a.domainStaticDir(domain), staticRelativePathForPage(pagePath))
	if _, statErr := os.Stat(staticFilePath); statErr != nil {
		return false
	}
	a.logContentDelivery(w, "static-file")
	http.ServeFile(w, r, staticFilePath)
	return true
}

func (a *App) writePublishedStaticHTML(domain, pagePath, html string) {
	staticFilePath := filepath.Join(a.domainStaticDir(domain), staticRelativePathForPage(pagePath))
	_ = os.MkdirAll(filepath.Dir(staticFilePath), 0755)
	_ = os.WriteFile(staticFilePath, []byte(html), 0644)
}

func (a *App) wrapPublishedPageWithGuestMenu(domain, pagePath, html string) string {
	script := buildContextMenuScript(false, false, pagePath, domain, map[string]string{})
	if strings.Contains(strings.ToLower(html), "</body>") {
		bodyClosePattern := regexp.MustCompile(`(?i)</body>`)
		return bodyClosePattern.ReplaceAllString(html, script+"</body>")
	}
	return html + script
}

func staticRelativePathForPage(pagePath string) string {
	normalizedPath := strings.TrimPrefix(pagePath, "/")
	if normalizedPath == "" {
		return "index.html"
	}
	if strings.HasSuffix(normalizedPath, "/") {
		return normalizedPath + "index.html"
	}
	return normalizedPath + ".html"
}

func (a *App) domainStaticDir(domain string) string {
	return filepath.Join("storage/static", domainStorageName(domain))
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
	return filepath.Join("storage/files", domainStorageName(a.siteDomain(r.Context(), r)))
}

func rewriteMirroredLinks(sourceHTML string, localFileToAssetPath map[string]string) string {
	referencePattern := regexp.MustCompile(`(["'\(])(?!https?:|data:|javascript:|#)([^"'\)]+)(["'\)])`)
	return referencePattern.ReplaceAllStringFunc(sourceHTML, func(fragment string) string {
		parts := referencePattern.FindStringSubmatch(fragment)
		if len(parts) != 4 {
			return fragment
		}
		relativeReference := strings.TrimPrefix(parts[2], "./")
		if assetPath, exists := localFileToAssetPath[relativeReference]; exists {
			return parts[1] + assetPath + parts[3]
		}
		fileNameReference := path.Base(relativeReference)
		if assetPath, exists := localFileToAssetPath[fileNameReference]; exists {
			return parts[1] + assetPath + parts[3]
		}
		return fragment
	})
}
