package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed web/*
var embeddedWebFiles embed.FS

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

func main() {
	database, err := sql.Open("sqlite3", "file:sitebrush.db")
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
	router.HandleFunc("/save", application.savePage)
	router.HandleFunc("/revisions", application.revisionsPage)
	router.HandleFunc("/revision/restore", application.restoreRevision)
	router.HandleFunc("/revision/delete", application.deleteRevision)
	router.HandleFunc("/", application.route)

	log.Println("Sidebrush started on http://127.0.0.1:8080")
	log.Fatal(http.ListenAndServe(":8080", router))
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
		http.Redirect(w, r, "/edit?path="+pagePath, http.StatusFound)
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
		http.Redirect(w, r, "/edit?path="+pagePath, http.StatusFound)
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
	http.SetCookie(w, &http.Cookie{Name: "sidebrush_session", Value: "", Path: "/", Expires: time.Unix(0, 0)})
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
	http.SetCookie(w, &http.Cookie{Name: "sidebrush_session", Value: token, Path: "/", HttpOnly: true})
}

func (a *App) isAdminRequest(r *http.Request) bool {
	cookie, err := r.Cookie("sidebrush_session")
	if err != nil {
		return false
	}
	var sessionCount int
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(1) FROM sessions s JOIN users u ON u.email=s.user_email WHERE s.token=? AND u.is_admin=1`, cookie.Value).Scan(&sessionCount)
	return sessionCount > 0
}

func (a *App) wrapWithMenu(r *http.Request, pagePath, html string) string {
	return html + buildContextMenuScript(a.isAdminRequest(r), pagePath)
}

func buildContextMenuScript(isAdmin bool, pagePath string) string {
	escapedPath := template.JSEscapeString(pagePath)
	if isAdmin {
		return `<script>
(function initializeSitebrushContextMenuForAdmin() {
  const currentPagePath = "` + escapedPath + `";
  document.addEventListener("contextmenu", function onContextMenuOpen(browserEvent) {
    browserEvent.preventDefault();
    const menuHtml = [
      "<ul class='SiteBrushMenuList'>",
      "<li class='SiteBrushContextMenu'><a href='?edit' class='SiteBrushContextMenuLink'><img src='/p/static/pencil.png' class='SiteBrushMenuIcon' alt=''>Редактировать</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?revisions' class='SiteBrushContextMenuLink'><img src='/p/static/revisions.png' class='SiteBrushMenuIcon' alt=''>Ревизии</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?properties' class='SiteBrushContextMenuLink'><img src='/p/static/properties.gif' class='SiteBrushMenuIcon' alt=''>Свойства</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?freeze' class='SiteBrushContextMenuLink'><img src='/p/static/freeze.png' class='SiteBrushMenuIcon' alt=''>Заморозить</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?logout' class='SiteBrushContextMenuLink'><img src='/p/static/sign-out.png' class='SiteBrushMenuIcon' alt=''>Выйти</a></li>",
      "<li class='SiteBrushContextMenu ContextMenuCopyright'><a href='http://sitebrush.com' class='SiteBrushContextMenuLink'>sitebrush</a></li>",
      "</ul>"
    ];
    showSitebrushMenu(browserEvent, menuHtml, currentPagePath);
  });
})();
</script>` + contextMenuSharedScript()
	}
	return `<script>
(function initializeSitebrushContextMenuForGuest() {
  const currentPagePath = "` + escapedPath + `";
  document.addEventListener("contextmenu", function onContextMenuOpen(browserEvent) {
    browserEvent.preventDefault();
    const menuHtml = [
      "<ul class='SiteBrushMenuList'>",
      "<li class='SiteBrushContextMenu'><a href='?login' class='SiteBrushContextMenuLink'><img src='/p/static/lock.png' class='SiteBrushMenuIcon' alt=''>Войти</a></li>",
      "<li class='SiteBrushContextMenu ContextMenuCopyright'><a href='http://sitebrush.com' class='SiteBrushContextMenuLink'>sitebrush</a></li>",
      "</ul>"
    ];
    showSitebrushMenu(browserEvent, menuHtml, currentPagePath);
  });
})();
</script>` + contextMenuSharedScript()
}

func contextMenuSharedScript() string {
	return `<style>
.SiteBrushMenuBox{position:fixed;background:#fff url(/p/static/bg.png) repeat-x top;border:1px solid #8ea4c1;z-index:99999;padding:2px;min-width:240px;box-shadow:0 2px 12px rgba(0,0,0,0.2)}
.SiteBrushMenuList{list-style:none;margin:0;padding:0}
.SiteBrushContextMenu{padding:0;margin:0}
.SiteBrushContextMenuLink{display:flex;align-items:center;gap:8px;color:#1d3557;text-decoration:none;padding:6px 10px;font-family:Arial,sans-serif;font-size:13px;line-height:16px}
.SiteBrushContextMenuLink:hover{background:#dfe8f6}
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
