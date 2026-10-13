package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSavePageResolvesAllTemplateCopiesAndRejectsConflictsBeforeWriting(t *testing.T) {
	for _, scenario := range []string{"first copy", "last copy", "same edits", "conflict"} {
		t.Run(scenario, func(t *testing.T) {
			application, rawDB := newTestApplication(t)
			if _, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "admin@example.com", "old"); err != nil {
				t.Fatal(err)
			}
			before := `<section class="SiteBrush-Template shared"><p>Old</p></section>`
			after := `<section class="SiteBrush-Template shared"><p>New</p></section>`
			different := `<section class="SiteBrush-Template shared"><p>Different</p></section>`
			storedHTML := `<html><head></head><body>` + before + before + `</body></html>`
			for _, pagePath := range []string{"/", "/about"} {
				if _, err := rawDB.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", pagePath, "Page", storedHTML); err != nil {
					t.Fatal(err)
				}
				if _, err := rawDB.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, "localhost", pagePath, "Page", storedHTML); err != nil {
					t.Fatal(err)
				}
				application.writePublishedStaticHTML("localhost", pagePath, storedHTML)
			}
			savedCopies := after + before
			switch scenario {
			case "last copy":
				savedCopies = before + after
			case "same edits":
				savedCopies = after + after
			case "conflict":
				savedCopies = after + different
			}
			form := url.Values{"path": {"/"}, "title": {"Page"}, "html": {`<html><head></head><body>` + savedCopies + `</body></html>`}}
			request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?save", strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
			response := httptest.NewRecorder()
			application.route(response, request)
			expectedStatus := http.StatusFound
			if scenario == "conflict" {
				expectedStatus = http.StatusConflict
			}
			if response.Code != expectedStatus {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			expectedHTML := `<html><head></head><body>` + after + after + `</body></html>`
			if scenario == "conflict" {
				expectedHTML = storedHTML
				if !strings.Contains(response.Body.String(), "shared") {
					t.Fatal("conflict response omits template identifier")
				}
			}
			for _, pagePath := range []string{"/", "/about"} {
				for _, tableName := range []string{"pages", "published_pages"} {
					var storedPageHTML string
					if err := rawDB.QueryRow(`SELECT html FROM `+tableName+` WHERE domain=? AND path=?`, "localhost", pagePath).Scan(&storedPageHTML); err != nil {
						t.Fatal(err)
					}
					if storedPageHTML != expectedHTML {
						t.Fatalf("%s %s = %s, want %s", tableName, pagePath, storedPageHTML, expectedHTML)
					}
				}
				staticHTML, err := os.ReadFile(filepath.Join(application.domainStaticDir("localhost"), staticRelativePathForPage(pagePath)))
				if err != nil {
					t.Fatal(err)
				}
				if string(staticHTML) != expectedHTML {
					t.Fatalf("published file %s = %s", pagePath, staticHTML)
				}
				var revisionCount int
				if err := rawDB.QueryRow(`SELECT COUNT(*) FROM revisions WHERE domain=? AND page_path=?`, "localhost", pagePath).Scan(&revisionCount); err != nil {
					t.Fatal(err)
				}
				expectedRevisions := 1
				if scenario == "conflict" {
					expectedRevisions = 0
				}
				if revisionCount != expectedRevisions {
					t.Fatalf("%s revisions = %d, want %d", pagePath, revisionCount, expectedRevisions)
				}
			}
		})
	}
}

func TestImportedTemplatesRequireContentAndDistinctPages(t *testing.T) {
	shared := `<section><img src="/logo.png"></section>`
	empty := `<meta name="generator" content="Site Kit by Google 1.188.0"/><div class="column"><div></div></div><table><tr><td>&nbsp;</td></tr></table>`
	importedPages := []wholeSiteImportedPage{
		{LocalPath: "/one", HTML: `<html data-page="one"><body data-page="one">` + empty + shared + shared + `<p>One</p></body></html>`},
		{LocalPath: "/two", HTML: `<html data-page="two"><body data-page="two">` + empty + shared + `<p>Two</p></body></html>`},
	}
	detectedPages, err := maybeDetectImportedPageTemplates(importedPages, true)
	if err != nil {
		t.Fatal(err)
	}
	for pageIndex, detectedPage := range detectedPages {
		expectedCount := 1
		if pageIndex == 0 {
			expectedCount = 2
		}
		if strings.Count(detectedPage.HTML, "SiteBrush-Template") != expectedCount {
			t.Fatalf("wrong imported templates: %s", detectedPage.HTML)
		}
		if !strings.Contains(detectedPage.HTML, empty) {
			t.Fatalf("empty markup was modified: %s", detectedPage.HTML)
		}
	}
}
