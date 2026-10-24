package sitefreeze

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func Schema() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS site_freeze_points(domain TEXT NOT NULL,id TEXT NOT NULL,created_at TEXT NOT NULL,status TEXT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(domain,id))`,
		`CREATE TABLE IF NOT EXISTS site_freeze_state(domain TEXT PRIMARY KEY,checkpoint_id TEXT NOT NULL,stage TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS site_hidden_assets(domain TEXT NOT NULL,tree TEXT NOT NULL,path TEXT NOT NULL,PRIMARY KEY(domain,tree,path))`,
	}
}
func Save(transaction *sql.Tx, domain string, point Checkpoint, status string) error {
	encoded, err := json.Marshal(point)
	if err != nil {
		return err
	}
	_, err = transaction.Exec(`INSERT INTO site_freeze_points(domain,id,created_at,status,payload) VALUES(?,?,?,?,?)`, domain, point.ID, point.CreatedAt, status, string(encoded))
	return err
}
func Activate(transaction *sql.Tx, domain, id, stage string) error {
	if _, err := transaction.Exec(`DELETE FROM site_freeze_state WHERE domain=?`, domain); err != nil {
		return err
	}
	_, err := transaction.Exec(`INSERT INTO site_freeze_state(domain,checkpoint_id,stage) VALUES(?,?,?)`, domain, id, stage)
	return err
}
func Finish(transaction *sql.Tx, domain, status string) error {
	if _, err := transaction.Exec(`UPDATE site_freeze_points SET status=? WHERE domain=? AND id=(SELECT checkpoint_id FROM site_freeze_state WHERE domain=?)`, status, domain, domain); err != nil {
		return err
	}
	_, err := transaction.Exec(`DELETE FROM site_freeze_state WHERE domain=?`, domain)
	return err
}

// Capture runs in the existing content SQL owner's turn. Properties from the
// control owner and file references arrive through the caller's exclusive lease.
func Capture(transaction *sql.Tx, domain string, ensure bool) (Checkpoint, error) {
	point := Checkpoint{CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	for _, table := range []string{"pages", "published_pages"} {
		query := `SELECT path,title,html FROM ` + table + ` WHERE domain=? ORDER BY path`
		if table == "pages" {
			query = `SELECT path,title,html,COALESCE(published,0),EXISTS(SELECT 1 FROM page_pending_deletions WHERE domain=pages.domain AND path=pages.path) FROM pages WHERE domain=? ORDER BY path`
		}
		rows, err := transaction.Query(query, domain)
		if err != nil {
			return point, err
		}
		type sourcePage struct {
			page Page
			html string
		}
		sources := []sourcePage{}
		for rows.Next() {
			var source sourcePage
			if table == "pages" {
				err = rows.Scan(&source.page.Path, &source.page.Title, &source.html, &source.page.Published, &source.page.PendingDelete)
			} else {
				err = rows.Scan(&source.page.Path, &source.page.Title, &source.html)
			}
			if err != nil {
				rows.Close()
				return point, err
			}
			sources = append(sources, source)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return point, err
		}
		for _, source := range sources {
			digest := sha256.Sum256([]byte(source.html))
			source.page.Hash = hex.EncodeToString(digest[:])
			err = transaction.QueryRow(`SELECT id FROM revisions WHERE domain=? AND page_path=? AND html=? ORDER BY id DESC LIMIT 1`, domain, source.page.Path, source.html).Scan(&source.page.Revision)
			if errors.Is(err, sql.ErrNoRows) && ensure {
				count := int64(len(source.html))
				reservation, reserveErr := transaction.Exec(`UPDATE domain_storage_usage SET revision_bytes=revision_bytes+? WHERE domain=? AND page_bytes+published_page_bytes+revision_bytes+file_bytes+published_static_bytes+COALESCE(import_queue_bytes,0)+COALESCE(analytics_bytes,0)+COALESCE(analytics_reserved_bytes,0)+?<=limit_bytes`, count, domain, count)
				if reserveErr != nil {
					return point, reserveErr
				}
				admitted, reserveErr := reservation.RowsAffected()
				if reserveErr != nil {
					return point, reserveErr
				}
				if admitted != 1 {
					return point, errors.New("not enough storage to retain a baseline revision")
				}
				result, insertErr := transaction.Exec(`INSERT INTO revisions(domain,page_path,html,created_at,is_active) VALUES(?,?,?,?,0)`, domain, source.page.Path, source.html, point.CreatedAt)
				if insertErr != nil {
					return point, insertErr
				}
				source.page.Revision, err = result.LastInsertId()
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return point, err
			}
			if table == "pages" {
				point.Pages = append(point.Pages, source.page)
			} else {
				source.page.Published = true
				point.Published = append(point.Published, source.page)
			}
		}
	}
	rows, err := transaction.Query(`SELECT id,page_path,COALESCE(is_active,0) FROM revisions WHERE domain=? ORDER BY id`, domain)
	if err != nil {
		return point, err
	}
	defer rows.Close()
	for rows.Next() {
		var revision Revision
		if err = rows.Scan(&revision.ID, &revision.Path, &revision.Active); err != nil {
			return point, err
		}
		point.Revisions = append(point.Revisions, revision)
	}
	return point, rows.Err()
}

// RestoreMaterializedViews deliberately bypasses page saving, template
// propagation and revision insertion. Only current views and selection flags move.
func RestoreMaterializedViews(transaction *sql.Tx, domain string, point, current Checkpoint) error {
	if _, err := transaction.Exec(`UPDATE revisions SET is_active=0 WHERE domain=?`, domain); err != nil {
		return err
	}
	for _, revision := range point.Revisions {
		active := 0
		if revision.Active {
			active = 1
		}
		if _, err := transaction.Exec(`UPDATE revisions SET is_active=? WHERE domain=? AND id=?`, active, domain, revision.ID); err != nil {
			return err
		}
	}
	for _, table := range []string{"pages", "published_pages", "page_pending_deletions"} {
		if _, err := transaction.Exec(`DELETE FROM `+table+` WHERE domain=?`, domain); err != nil {
			return err
		}
	}
	before := make(map[string]bool)
	for _, page := range point.Pages {
		before[page.Path] = true
		var body string
		if err := transaction.QueryRow(`SELECT html FROM revisions WHERE domain=? AND id=?`, domain, page.Revision).Scan(&body); err != nil {
			return fmt.Errorf("checkpoint page %s is unavailable: %w", page.Path, err)
		}
		publishedFlag := 0
		if page.Published {
			publishedFlag = 1
		}
		if _, err := transaction.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,?)`, domain, page.Path, page.Title, body, publishedFlag); err != nil {
			return err
		}
		if page.PendingDelete {
			if _, err := transaction.Exec(`INSERT INTO page_pending_deletions(domain,path) VALUES(?,?)`, domain, page.Path); err != nil {
				return err
			}
		}
	}
	for _, page := range point.Published {
		var body string
		if err := transaction.QueryRow(`SELECT html FROM revisions WHERE domain=? AND id=?`, domain, page.Revision).Scan(&body); err != nil {
			return err
		}
		if _, err := transaction.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, domain, page.Path, page.Title, body); err != nil {
			return err
		}
	}
	for _, page := range append(append([]Page(nil), current.Pages...), current.Published...) {
		if !before[page.Path] {
			if err := Hide(transaction, domain, "page", page.Path); err != nil {
				return err
			}
		}
	}
	published := make(map[string]bool)
	for _, page := range point.Published {
		published[page.Path] = true
	}
	for _, page := range current.Published {
		if !published[page.Path] {
			if err := Hide(transaction, domain, "page", page.Path); err != nil {
				return err
			}
		}
	}
	selected := make(map[int64]bool)
	for _, revision := range point.Revisions {
		selected[revision.ID] = true
	}
	for _, revision := range current.Revisions {
		if !selected[revision.ID] && revision.Path != "" && !published[revision.Path] {
			if err := Hide(transaction, domain, "page", revision.Path); err != nil {
				return err
			}
		}
	}

	for _, file := range point.Files {
		if _, err := transaction.Exec(`DELETE FROM site_hidden_assets WHERE domain=? AND tree=? AND path=?`, domain, file.Tree, file.Path); err != nil {
			return err
		}
	}
	for _, page := range point.Published {
		if _, err := transaction.Exec(`DELETE FROM site_hidden_assets WHERE domain=? AND tree='page' AND path=?`, domain, page.Path); err != nil {
			return err
		}
	}
	return nil
}
func Hide(transaction *sql.Tx, domain, tree, path string) error {
	var count int
	if err := transaction.QueryRow(`SELECT COUNT(*) FROM site_hidden_assets WHERE domain=? AND tree=? AND path=?`, domain, tree, path).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return nil
	}
	_, err := transaction.Exec(`INSERT INTO site_hidden_assets(domain,tree,path) VALUES(?,?,?)`, domain, tree, path)
	return err
}
