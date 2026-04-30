package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"goup/pkg/desktop"
)

//go:embed web/*
var embeddedWebFiles embed.FS

const appVersion = "dev"

// App keeps only explicit dependencies to stay readable and easy to swap.
type App struct {
	db *sql.DB
}

type Page struct {
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
		startedAt := time.Now()
		writer := &statusCapturingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(writer, r)
		log.Printf("access method=%s path=%s query=%s status=%d remote=%s duration=%s", r.Method, r.URL.Path, r.URL.RawQuery, writer.statusCode, r.RemoteAddr, time.Since(startedAt).String())
	})
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
	router.HandleFunc("/revisions", application.revisionsPage)
	router.HandleFunc("/revision/restore", application.restoreRevision)
	router.HandleFunc("/revision/delete", application.deleteRevision)
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
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users(id INTEGER PRIMARY KEY AUTOINCREMENT,email TEXT UNIQUE,password TEXT,is_admin INTEGER);`,
		`CREATE TABLE IF NOT EXISTS sessions(token TEXT PRIMARY KEY,user_email TEXT,created_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS pages(path TEXT PRIMARY KEY,title TEXT,html TEXT,published INTEGER);`,
		`CREATE TABLE IF NOT EXISTS revisions(id INTEGER PRIMARY KEY AUTOINCREMENT,page_path TEXT,html TEXT,created_at TEXT);`,
	}
	for _, query := range queries {
		if _, err := a.db.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) route(w http.ResponseWriter, r *http.Request) {
	pagePath := r.URL.Path
	if r.URL.RawQuery == "edit" {
		http.Redirect(w, r, "/edit/mode?path="+pagePath, http.StatusFound)
		return
	}
	if r.URL.RawQuery == "files" {
		http.Redirect(w, r, "/files?path="+pagePath, http.StatusFound)
		return
	}
	if r.URL.RawQuery == "revisions" {
		http.Redirect(w, r, "/revisions?path="+pagePath, http.StatusFound)
		return
	}
	if r.URL.RawQuery == "login" {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	if r.URL.RawQuery == "logout" {
		http.Redirect(w, r, "/logout", http.StatusFound)
		return
	}
	if r.URL.RawQuery == "properties" || r.URL.RawQuery == "freeze" || r.URL.RawQuery == "unfreeze" {
		http.Redirect(w, r, "/edit/mode?path="+pagePath, http.StatusFound)
		return
	}
	pageRecord, err := a.findPage(r.Context(), pagePath)
	if err == nil && pageRecord.Published == 1 {
		_, _ = w.Write([]byte(a.wrapWithMenu(r, pageRecord.Path, pageRecord.HTML)))
		return
	}
	if !a.hasAdmin(r.Context()) {
		a.render(w, "setup.html", nil)
		return
	}
	if a.isAdminRequest(r) {
		a.render(w, "missing.html", map[string]string{"Path": pagePath})
		return
	}
	http.NotFound(w, r)
}

func (a *App) setupAdmin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.hasAdmin(r.Context()) {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	email := r.FormValue("email")
	password := r.FormValue("password")
	if email == "" || password == "" {
		http.Error(w, "email and password required", http.StatusBadRequest)
		return
	}
	_, err := a.db.ExecContext(r.Context(), `INSERT INTO users(email,password,is_admin) VALUES(?,?,1)`, email, password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.createSession(w, r, email)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		a.render(w, "login.html", nil)
		return
	}
	email := r.FormValue("email")
	password := r.FormValue("password")
	var matchedUsers int
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(1) FROM users WHERE email=? AND password=?`, email, password).Scan(&matchedUsers)
	if matchedUsers == 0 {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	a.createSession(w, r, email)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "sitebrush_session", Value: "", Path: "/", Expires: time.Unix(0, 0)})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (a *App) editPage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	pagePath := r.URL.Query().Get("path")
	if pagePath == "" {
		pagePath = "/"
	}
	record, _ := a.findPage(r.Context(), pagePath)
	if record.Path == "" {
		record = Page{Path: pagePath, Title: pagePath, HTML: "<h1>New page</h1>"}
	}
	a.render(w, "edit.html", record)
}

func (a *App) editModePage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	pagePath := r.URL.Query().Get("path")
	if pagePath == "" {
		pagePath = "/"
	}
	a.render(w, "edit_mode.html", map[string]string{"Path": pagePath})
}

func (a *App) editRawPage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	pagePath := r.URL.Query().Get("path")
	if pagePath == "" {
		pagePath = "/"
	}
	record, _ := a.findPage(r.Context(), pagePath)
	if record.Path == "" {
		record = Page{Path: pagePath, Title: pagePath, HTML: ""}
	}
	a.render(w, "edit_raw.html", record)
}

func (a *App) savePage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) || r.Method != http.MethodPost {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	pagePath := r.FormValue("path")
	title := r.FormValue("title")
	html := r.FormValue("html")
	_, _ = a.db.ExecContext(r.Context(), `INSERT OR REPLACE INTO pages(path,title,html,published) VALUES(?,?,?,1)`, pagePath, title, html)
	_, _ = a.db.ExecContext(r.Context(), `INSERT INTO revisions(page_path,html,created_at) VALUES(?,?,?)`, pagePath, html, time.Now().Format(time.RFC3339))
	a.applyTemplatePropagation(r.Context(), html)
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

	html := a.mirrorRemotePage(sourceURL, remoteSourceURL, string(htmlBytes))
	_, _ = a.db.ExecContext(r.Context(), `INSERT OR REPLACE INTO pages(path,title,html,published) VALUES(?,?,?,1)`, pagePath, pagePath, html)
	_, _ = a.db.ExecContext(r.Context(), `INSERT INTO revisions(page_path,html,created_at) VALUES(?,?,?)`, pagePath, html, time.Now().Format(time.RFC3339))
	http.Redirect(w, r, "/edit/mode?path="+pagePath, http.StatusFound)
}

func (a *App) revisionsPage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	pagePath := r.URL.Query().Get("path")
	revisionRows, err := a.db.QueryContext(r.Context(), `SELECT id,page_path,html,created_at FROM revisions WHERE page_path=? ORDER BY id DESC`, pagePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer revisionRows.Close()
	var revisionList []Revision
	for revisionRows.Next() {
		var current Revision
		_ = revisionRows.Scan(&current.ID, &current.PagePath, &current.HTML, &current.CreatedAt)
		revisionList = append(revisionList, current)
	}
	a.render(w, "revisions.html", map[string]any{"Path": pagePath, "Revisions": revisionList})
}

func (a *App) restoreRevision(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) || r.Method != http.MethodPost {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	revisionID, _ := strconv.Atoi(r.FormValue("id"))
	var pagePath, html string
	err := a.db.QueryRowContext(r.Context(), `SELECT page_path,html FROM revisions WHERE id=?`, revisionID).Scan(&pagePath, &html)
	if err != nil {
		http.Error(w, "revision not found", http.StatusNotFound)
		return
	}
	_, _ = a.db.ExecContext(r.Context(), `UPDATE pages SET html=? WHERE path=?`, html, pagePath)
	_, _ = a.db.ExecContext(r.Context(), `INSERT INTO revisions(page_path,html,created_at) VALUES(?,?,?)`, pagePath, html, time.Now().Format(time.RFC3339))
	http.Redirect(w, r, pagePath, http.StatusFound)
}

func (a *App) deleteRevision(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) || r.Method != http.MethodPost {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	revisionID, _ := strconv.Atoi(r.FormValue("id"))
	pagePath := r.FormValue("path")
	_, _ = a.db.ExecContext(r.Context(), `DELETE FROM revisions WHERE id=?`, revisionID)
	http.Redirect(w, r, "/revisions?path="+pagePath, http.StatusFound)
}

func (a *App) filesPage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodPost {
		fileName := safeFileName(r.FormValue("name"))
		if fileName != "" {
			_ = os.Remove(filepath.Join("storage/files", fileName))
		}
		currentPath := r.URL.Query().Get("path")
		if currentPath == "" {
			currentPath = "/"
		}
		http.Redirect(w, r, "/files?path="+currentPath, http.StatusFound)
		return
	}
	entries, err := os.ReadDir("storage/files")
	if err != nil {
		a.render(w, "files.html", map[string]any{"Path": r.URL.Query().Get("path"), "Files": []ManagedFile{}})
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
		currentPath = "/"
	}
	a.render(w, "files.html", map[string]any{"Path": currentPath, "Files": fileList})
}

func (a *App) findPage(ctx context.Context, pagePath string) (Page, error) {
	var current Page
	err := a.db.QueryRowContext(ctx, `SELECT path,title,html,published FROM pages WHERE path=?`, pagePath).Scan(&current.Path, &current.Title, &current.HTML, &current.Published)
	return current, err
}

func (a *App) hasAdmin(ctx context.Context) bool {
	var adminCount int
	_ = a.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM users WHERE is_admin=1`).Scan(&adminCount)
	return adminCount > 0
}

func (a *App) createSession(w http.ResponseWriter, r *http.Request, email string) {
	token := fmt.Sprintf("%x", sha256.Sum256([]byte(email+time.Now().String())))
	_, _ = a.db.ExecContext(r.Context(), `INSERT OR REPLACE INTO sessions(token,user_email,created_at) VALUES(?,?,?)`, token, email, time.Now().Format(time.RFC3339))
	http.SetCookie(w, &http.Cookie{Name: "sitebrush_session", Value: token, Path: "/", HttpOnly: true})
}

func (a *App) isAdminRequest(r *http.Request) bool {
	cookie, err := r.Cookie("sitebrush_session")
	if err != nil {
		return false
	}
	var sessionCount int
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(1) FROM sessions s JOIN users u ON u.email=s.user_email WHERE s.token=? AND u.is_admin=1`, cookie.Value).Scan(&sessionCount)
	return sessionCount > 0
}

func (a *App) wrapWithMenu(r *http.Request, pagePath, html string) string {
	menuScript := buildContextMenuScript(a.isAdminRequest(r), pagePath)
	if strings.Contains(strings.ToLower(html), "</body>") {
		bodyClosePattern := regexp.MustCompile(`(?i)</body>`)
		return bodyClosePattern.ReplaceAllString(html, menuScript+"</body>")
	}
	return html + menuScript
}

func buildContextMenuScript(isAdmin bool, pagePath string) string {
	escapedPath := template.JSEscapeString(pagePath)
	if isAdmin {
		return contextMenuStylesAndHelpers() + `<script>
(function initializeSitebrushContextMenuForAdmin() {
  if (window.__sitebrushContextMenuInitialized) {
    return;
  }
  window.__sitebrushContextMenuInitialized = true;
  const currentPagePath = "` + escapedPath + `";
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
      "<li class='SiteBrushContextMenu'><a href='?edit' class='SiteBrushContextMenuLink'><img src='/p/static/pencil.png' class='SiteBrushMenuIcon' alt=''>Редактировать</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?revisions' class='SiteBrushContextMenuLink'><img src='/p/static/revisions.png' class='SiteBrushMenuIcon' alt=''>Ревизии</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?files' class='SiteBrushContextMenuLink'><img src='/p/static/upload.png' class='SiteBrushMenuIcon' alt=''>Файлы</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?logout' class='SiteBrushContextMenuLink'><img src='/p/static/sign-out.png' class='SiteBrushMenuIcon' alt=''>Выйти</a></li>",
      "<li class='SiteBrushContextMenu ContextMenuCopyright'><a href='http://sitebrush.com' class='SiteBrushContextMenuLink'>sitebrush</a></li>",
      "</ul>"
    ];
    showSitebrushMenu(browserEvent, menuHtmlEntries, currentPagePath);
	  }, {capture: false, passive: false});
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
      "<li class='SiteBrushContextMenu'><a href='?login' class='SiteBrushContextMenuLink'><img src='/p/static/lock.png' class='SiteBrushMenuIcon' alt=''>Войти</a></li>",
      "<li class='SiteBrushContextMenu ContextMenuCopyright'><a href='http://sitebrush.com' class='SiteBrushContextMenuLink'>sitebrush</a></li>",
      "</ul>"
    ];
    showSitebrushMenu(browserEvent, menuHtmlEntries, currentPagePath);
	  }, {capture: false, passive: false});
})();
	</script>`
}

func contextMenuStylesAndHelpers() string {
	return `<style>
.SiteBrushMenuBox{position:fixed;background:#fff url(/p/static/bg.png) repeat-x top;border:1px solid #8ea4c1;z-index:99999;padding:2px;min-width:240px;box-shadow:0 2px 12px rgba(0,0,0,0.2)}
.SiteBrushMenuList{list-style:none;margin:0;padding:0}
.SiteBrushContextMenu{margin:0;padding:0}
.SiteBrushContextMenuLink{display:flex;align-items:center;gap:8px;padding:8px 10px;color:#1f3f6f;text-decoration:none;font-family:Arial,Helvetica,sans-serif;font-size:14px}
.SiteBrushContextMenuLink:hover{background:#eef5ff}
.ContextMenuCopyright .SiteBrushContextMenuLink{font-size:12px;color:#5b6f8b;border-top:1px solid #c8d5e7;margin-top:2px;padding-top:7px}.SiteBrushMenuIcon{width:16px;height:16px;flex:0 0 16px}
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
function showSitebrushMenu(browserEvent, menuHtmlEntries, currentPagePath) {
  const existingMenuBox = document.getElementById("SiteBrushMenuBox");
  if (existingMenuBox) {
    existingMenuBox.remove();
  }
  const menuBoxElement = document.createElement("div");
  menuBoxElement.id = "SiteBrushMenuBox";
  menuBoxElement.className = "SiteBrushMenuBox";
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
</script>`
}

func (a *App) applyTemplatePropagation(ctx context.Context, sourceHTML string) {
	pattern := regexp.MustCompile(`(?s)<([a-zA-Z0-9]+)[^>]*class="[^"]*sitebrush-template-([a-zA-Z0-9_-]+)[^"]*"[^>]*>.*?</[a-zA-Z0-9]+>`)
	matches := pattern.FindAllStringSubmatch(sourceHTML, -1)
	for _, match := range matches {
		templateBlock := match[0]
		templateName := match[2]
		rows, err := a.db.QueryContext(ctx, `SELECT path,html FROM pages WHERE html LIKE ?`, "%sitebrush-template-"+templateName+"%")
		if err != nil {
			continue
		}
		for rows.Next() {
			var pagePath, pageHTML string
			_ = rows.Scan(&pagePath, &pageHTML)
			updatedHTML := replaceTemplateByClass(pageHTML, templateName, templateBlock)
			_, _ = a.db.ExecContext(ctx, `UPDATE pages SET html=? WHERE path=?`, updatedHTML, pagePath)
		}
		_ = rows.Close()
	}
}

func replaceTemplateByClass(pageHTML, templateName, newBlock string) string {
	templatePattern := regexp.MustCompile(`(?s)<([a-zA-Z0-9]+)[^>]*class="[^"]*sitebrush-template-` + regexp.QuoteMeta(templateName) + `[^"]*"[^>]*>.*?</[a-zA-Z0-9]+>`)
	return templatePattern.ReplaceAllString(pageHTML, newBlock)
}

func (a *App) render(w http.ResponseWriter, templateName string, templateData any) {
	fileBytes, err := fs.ReadFile(embeddedWebFiles, "web/"+templateName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	parsedTemplate := template.Must(template.New(templateName).Parse(string(fileBytes)))
	_ = parsedTemplate.Execute(w, templateData)
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

func (a *App) mirrorRemotePage(sourceURL string, pageURL *url.URL, fallbackHTML string) string {
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

	localFileToAssetPath := a.importMirroredFiles(tempDirPath)
	return rewriteMirroredLinks(renderedHTML, localFileToAssetPath)
}

func (a *App) importMirroredFiles(tempDirPath string) map[string]string {
	localFileToAssetPath := make(map[string]string)
	_ = os.MkdirAll("storage/files", 0o755)
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
		assetPath := filepath.Join("storage/files", hashedFileName)
		if err = os.WriteFile(assetPath, fileBytes, 0o644); err != nil {
			return nil
		}
		relativePath, _ := filepath.Rel(tempDirPath, currentPath)
		localFileToAssetPath[filepath.ToSlash(relativePath)] = "/assets/" + hashedFileName
		return nil
	})
	return localFileToAssetPath
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
