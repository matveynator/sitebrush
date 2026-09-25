package main

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestAdminWebAttackTemplatePropagationStaysWithinSourceSite(t *testing.T) {
	for _, synchronizeClasses := range []bool{false, true} {
		name := "replace blocks"
		if synchronizeClasses {
			name = "synchronize classes"
		}
		t.Run(name, func(t *testing.T) {
			application, database := newTestApplication(t)
			const sourceDomain = "alpha.example"
			const otherDomain = "beta.example"
			const pagePath = "/index.html"
			previousHeader := `<header>Shared old header</header>`
			savedHeader := `<header class="SiteBrush-Template site-header">Shared replacement header</header>`
			otherSiteHTML := `<html><body><header class="SiteBrush-Template site-header">Beta site header</header></body></html>`
			if synchronizeClasses {
				otherSiteHTML = `<html><body><header>Beta site header</header></body></html>`
			}
			alphaSiteHTML := savedHeader
			if synchronizeClasses {
				alphaSiteHTML = previousHeader
			}
			if err := insertTemplateAttackPage(database, sourceDomain, pagePath, "Alpha", alphaSiteHTML); err != nil {
				t.Fatal(err)
			}
			if err := insertTemplateAttackPage(database, otherDomain, pagePath, "Beta", otherSiteHTML); err != nil {
				t.Fatal(err)
			}
			if synchronizeClasses {
				application.applyTemplateClassSynchronization(context.Background(), sourceDomain, previousHeader, savedHeader, "")
			} else {
				application.applyTemplatePropagation(context.Background(), sourceDomain, savedHeader, "")
			}

			var betaHTML string
			if err := database.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, otherDomain, pagePath).Scan(&betaHTML); err != nil {
				t.Fatal(err)
			}
			if betaHTML != otherSiteHTML {
				t.Fatalf("SECURITY: source site's template changed another site's editable page: %s", betaHTML)
			}
			var betaPublishedHTML string
			if err := database.QueryRow(`SELECT html FROM published_pages WHERE domain=? AND path=?`, otherDomain, pagePath).Scan(&betaPublishedHTML); err != nil {
				t.Fatal(err)
			}
			if betaPublishedHTML != otherSiteHTML {
				t.Fatalf("SECURITY: source site's template changed another site's published page: %s", betaPublishedHTML)
			}
			var betaRevisionCount int
			if err := database.QueryRow(`SELECT COUNT(1) FROM revisions WHERE domain=? AND page_path=?`, otherDomain, pagePath).Scan(&betaRevisionCount); err != nil {
				t.Fatal(err)
			}
			if betaRevisionCount != 0 {
				t.Fatalf("SECURITY: propagation wrote %d revisions to the unrelated site", betaRevisionCount)
			}
			var alphaHTML string
			if err := database.QueryRow(`SELECT html FROM pages WHERE domain=? AND path=?`, sourceDomain, pagePath).Scan(&alphaHTML); err != nil {
				t.Fatal(err)
			}
			if synchronizeClasses && (!strings.Contains(alphaHTML, `class="SiteBrush-Template site-header"`) || alphaHTML == previousHeader) {
				t.Fatalf("class synchronization did not update the source site's matching block: %s", alphaHTML)
			}
			if !synchronizeClasses && alphaHTML != savedHeader {
				t.Fatalf("source site's template block was not applied: %s", alphaHTML)
			}
		})
	}
}

func insertTemplateAttackPage(database *sql.DB, domain, path, title, html string) error {
	if _, err := database.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, domain, path, title, html); err != nil {
		return err
	}
	if _, err := database.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, domain, path, title, html); err != nil {
		return err
	}
	_, err := database.Exec(`INSERT INTO domain_storage_usage(domain,page_bytes,published_page_bytes,revision_bytes,file_bytes,published_static_bytes,limit_bytes,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		domain, len(html), len(html), 0, 0, len(html), 1<<30, time.Now().UTC().Format(time.RFC3339))
	return err
}
