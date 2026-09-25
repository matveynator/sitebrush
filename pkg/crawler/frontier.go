package crawler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrImportFrontierStorageLimit = errors.New("import frontier storage limit reached")

const importFrontierEntryOverheadBytes = 256

type importFrontierDatabase interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type ImportFrontierRun struct {
	ID                  string
	Domain              string
	PagePath            string
	SourceURL           string
	AutoDetectTemplates bool
}

type ImportFrontierPage struct {
	Key        string
	URL        string
	LocalPath  string
	LinkOffset int
}

type ImportFrontierStats struct {
	PendingPages int
	QueueBytes   int64
}

// ImportFrontierSchema creates the durable, domain-scoped crawl queue.
func ImportFrontierSchema() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS whole_site_imports(import_id TEXT PRIMARY KEY,domain TEXT NOT NULL,page_path TEXT NOT NULL,source_url TEXT NOT NULL,auto_templates INTEGER NOT NULL DEFAULT 0,state TEXT NOT NULL DEFAULT 'partial',created_at TEXT NOT NULL,updated_at TEXT NOT NULL);`,
		`CREATE INDEX IF NOT EXISTS idx_whole_site_imports_domain ON whole_site_imports(domain,state,updated_at);`,
		`CREATE TABLE IF NOT EXISTS whole_site_import_pages(import_id TEXT NOT NULL,domain TEXT NOT NULL,page_key TEXT NOT NULL,page_url TEXT NOT NULL,local_path TEXT NOT NULL,link_offset INTEGER NOT NULL DEFAULT 0,storage_bytes INTEGER NOT NULL DEFAULT 0,state TEXT NOT NULL DEFAULT 'pending',created_at TEXT NOT NULL,PRIMARY KEY(import_id,page_key));`,
		`CREATE INDEX IF NOT EXISTS idx_whole_site_import_pages_pending ON whole_site_import_pages(import_id,state,created_at);`,
	}
}

// RecoverImportFrontiers returns interrupted work to claimable states at startup.
func RecoverImportFrontiers(ctx context.Context, database importFrontierDatabase) error {
	if _, err := database.ExecContext(ctx, `UPDATE whole_site_imports SET state='partial',updated_at=? WHERE state='running'`, frontierNow()); err != nil {
		return fmt.Errorf("recover interrupted whole-site imports: %w", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE whole_site_import_pages SET state='pending' WHERE state='processing'`); err != nil {
		return fmt.Errorf("recover interrupted whole-site import pages: %w", err)
	}
	if err := CleanupDiscardedImportFrontiers(ctx, database); err != nil {
		return err
	}
	return nil
}

func CreateImportFrontier(ctx context.Context, database importFrontierDatabase, run ImportFrontierRun) error {
	if database == nil || strings.TrimSpace(run.ID) == "" || strings.TrimSpace(run.Domain) == "" || strings.TrimSpace(run.SourceURL) == "" {
		return errors.New("import frontier run identity is required")
	}
	_, err := database.ExecContext(ctx, `INSERT INTO whole_site_imports(import_id,domain,page_path,source_url,auto_templates,state,created_at,updated_at) VALUES(?,?,?,?,?,'running',?,?)`,
		run.ID, run.Domain, run.PagePath, run.SourceURL, boolToInt(run.AutoDetectTemplates), frontierNow(), frontierNow())
	if err != nil {
		return fmt.Errorf("create whole-site import: %w", err)
	}
	return nil
}

func FindImportFrontier(ctx context.Context, database importFrontierDatabase, importID, domain string) (ImportFrontierRun, error) {
	var run ImportFrontierRun
	var autoTemplates int
	err := database.QueryRowContext(ctx, `SELECT import_id,domain,page_path,source_url,auto_templates FROM whole_site_imports WHERE import_id=? AND domain=?`, importID, domain).
		Scan(&run.ID, &run.Domain, &run.PagePath, &run.SourceURL, &autoTemplates)
	if err != nil {
		return ImportFrontierRun{}, err
	}
	run.AutoDetectTemplates = autoTemplates != 0
	return run, nil
}

func FindActiveImportFrontier(ctx context.Context, database importFrontierDatabase, domain, pagePath, sourceURL string) (ImportFrontierRun, error) {
	var run ImportFrontierRun
	var autoTemplates int
	err := database.QueryRowContext(ctx, `SELECT import_id,domain,page_path,source_url,auto_templates FROM whole_site_imports WHERE domain=? AND page_path=? AND source_url=? AND state='partial' ORDER BY updated_at DESC LIMIT 1`, domain, pagePath, sourceURL).
		Scan(&run.ID, &run.Domain, &run.PagePath, &run.SourceURL, &autoTemplates)
	if err != nil {
		return ImportFrontierRun{}, err
	}
	run.AutoDetectTemplates = autoTemplates != 0
	return run, nil
}

func ClaimImportFrontier(ctx context.Context, database importFrontierDatabase, importID, domain string) error {
	result, err := database.ExecContext(ctx, `UPDATE whole_site_imports SET state='running',updated_at=? WHERE import_id=? AND domain=? AND state='partial'`, frontierNow(), importID, domain)
	if err != nil {
		return fmt.Errorf("claim whole-site import: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check whole-site import claim: %w", err)
	}
	if rowsAffected != 1 {
		return errors.New("whole-site import is not available to resume")
	}
	return nil
}

func AddImportFrontierPage(ctx context.Context, database importFrontierDatabase, importID, domain string, page ImportFrontierPage, maximumQueueBytes int64) (bool, error) {
	if database == nil || strings.TrimSpace(importID) == "" || strings.TrimSpace(domain) == "" || strings.TrimSpace(page.Key) == "" || strings.TrimSpace(page.URL) == "" {
		return false, errors.New("import frontier page is incomplete")
	}
	var exists int
	err := database.QueryRowContext(ctx, `SELECT 1 FROM whole_site_import_pages WHERE import_id=? AND domain=? AND page_key=?`, importID, domain, page.Key).Scan(&exists)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("check import frontier duplicate: %w", err)
	}
	queueBytes := int64(len(importID)+len(domain)+len(page.Key)+len(page.URL)+len(page.LocalPath)) + importFrontierEntryOverheadBytes
	result, err := database.ExecContext(ctx, `UPDATE domain_storage_usage
SET import_queue_bytes=COALESCE(import_queue_bytes,0)+?,updated_at=?
WHERE domain=?
AND COALESCE(import_queue_bytes,0)+?<=?
AND page_bytes+published_page_bytes+revision_bytes+file_bytes+published_static_bytes+COALESCE(import_queue_bytes,0)+?<=limit_bytes`, queueBytes, frontierNow(), domain, queueBytes, maximumQueueBytes, queueBytes)
	if err != nil {
		return false, fmt.Errorf("reserve import frontier storage: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("check import frontier storage reservation: %w", err)
	}
	if rowsAffected != 1 {
		return false, ErrImportFrontierStorageLimit
	}
	_, err = database.ExecContext(ctx, `INSERT INTO whole_site_import_pages(import_id,domain,page_key,page_url,local_path,link_offset,storage_bytes,state,created_at) VALUES(?,?,?,?,?,?,?,'pending',?)`, importID, domain, page.Key, page.URL, page.LocalPath, page.LinkOffset, queueBytes, frontierNow())
	if err != nil {
		var existingPage int
		duplicateErr := database.QueryRowContext(ctx, `SELECT 1 FROM whole_site_import_pages WHERE import_id=? AND domain=? AND page_key=?`, importID, domain, page.Key).Scan(&existingPage)
		_, _ = database.ExecContext(ctx, `UPDATE domain_storage_usage SET import_queue_bytes=CASE WHEN COALESCE(import_queue_bytes,0)>? THEN import_queue_bytes-? ELSE 0 END WHERE domain=?`, queueBytes, queueBytes, domain)
		if duplicateErr == nil {
			return false, nil
		}
		return false, fmt.Errorf("save import frontier page: %w", err)
	}
	return true, nil
}

func ClaimImportFrontierPages(ctx context.Context, database importFrontierDatabase, importID, domain string, maximum int) ([]ImportFrontierPage, error) {
	if maximum < 1 {
		return nil, nil
	}
	rows, err := database.QueryContext(ctx, `SELECT page_key,page_url,local_path,link_offset FROM whole_site_import_pages WHERE import_id=? AND domain=? AND state='pending' ORDER BY created_at,page_key LIMIT ?`, importID, domain, maximum)
	if err != nil {
		return nil, fmt.Errorf("read pending import pages: %w", err)
	}
	pages := make([]ImportFrontierPage, 0, maximum)
	for rows.Next() {
		var page ImportFrontierPage
		if err := rows.Scan(&page.Key, &page.URL, &page.LocalPath, &page.LinkOffset); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read pending import page: %w", err)
		}
		pages = append(pages, page)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("read pending import pages: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close pending import pages: %w", err)
	}
	claimedPages := make([]ImportFrontierPage, 0, len(pages))
	for _, page := range pages {
		result, err := database.ExecContext(ctx, `UPDATE whole_site_import_pages SET state='processing' WHERE import_id=? AND domain=? AND page_key=? AND state='pending'`, importID, domain, page.Key)
		if err != nil {
			return nil, fmt.Errorf("claim pending import page: %w", err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("check claimed import page: %w", err)
		}
		if rowsAffected == 1 {
			claimedPages = append(claimedPages, page)
		}
	}
	return claimedPages, nil
}

func ListImportFrontierPages(ctx context.Context, database importFrontierDatabase, importID, domain string) ([]ImportFrontierPage, error) {
	rows, err := database.QueryContext(ctx, `SELECT page_key,page_url,local_path,link_offset FROM whole_site_import_pages WHERE import_id=? AND domain=? ORDER BY created_at,page_key`, importID, domain)
	if err != nil {
		return nil, fmt.Errorf("list whole-site import pages: %w", err)
	}
	defer rows.Close()
	pages := make([]ImportFrontierPage, 0)
	for rows.Next() {
		var page ImportFrontierPage
		if err := rows.Scan(&page.Key, &page.URL, &page.LocalPath, &page.LinkOffset); err != nil {
			return nil, fmt.Errorf("read whole-site import page: %w", err)
		}
		pages = append(pages, page)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read whole-site import pages: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close whole-site import pages: %w", err)
	}
	return pages, nil
}

func SetImportFrontierPageState(ctx context.Context, database importFrontierDatabase, importID, domain, pageKey, state string) error {
	if state != "pending" && state != "processing" && state != "done" {
		return errors.New("invalid import frontier page state")
	}
	result, err := database.ExecContext(ctx, `UPDATE whole_site_import_pages SET state=? WHERE import_id=? AND domain=? AND page_key=?`, state, importID, domain, pageKey)
	if err != nil {
		return fmt.Errorf("update import frontier page state: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check import frontier page state: %w", err)
	}
	if rowsAffected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func UpdateImportFrontierPageCursor(ctx context.Context, database importFrontierDatabase, importID, domain, pageKey string, linkOffset int, state string) error {
	if linkOffset < 0 || (state != "pending" && state != "done") {
		return errors.New("invalid import frontier cursor")
	}
	result, err := database.ExecContext(ctx, `UPDATE whole_site_import_pages SET link_offset=?,state=? WHERE import_id=? AND domain=? AND page_key=?`, linkOffset, state, importID, domain, pageKey)
	if err != nil {
		return fmt.Errorf("update import frontier cursor: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check import frontier cursor: %w", err)
	}
	if rowsAffected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func ReleaseProcessingImportFrontierPages(ctx context.Context, database importFrontierDatabase, importID, domain string) error {
	_, err := database.ExecContext(ctx, `UPDATE whole_site_import_pages SET state='pending' WHERE import_id=? AND domain=? AND state='processing'`, importID, domain)
	if err != nil {
		return fmt.Errorf("release processing import pages: %w", err)
	}
	return nil
}

func ImportFrontierStatsForRun(ctx context.Context, database importFrontierDatabase, importID, domain string) (ImportFrontierStats, error) {
	var stats ImportFrontierStats
	err := database.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(storage_bytes),0) FROM whole_site_import_pages WHERE import_id=? AND domain=? AND state<>'done'`, importID, domain).
		Scan(&stats.PendingPages, &stats.QueueBytes)
	if err != nil {
		return ImportFrontierStats{}, fmt.Errorf("read import frontier stats: %w", err)
	}
	return stats, nil
}

func FinishImportFrontier(ctx context.Context, database importFrontierDatabase, importID, domain string) error {
	var queueBytes int64
	if err := database.QueryRowContext(ctx, `SELECT COALESCE(SUM(storage_bytes),0) FROM whole_site_import_pages WHERE import_id=? AND domain=?`, importID, domain).Scan(&queueBytes); err != nil {
		return fmt.Errorf("measure completed import frontier: %w", err)
	}
	if _, err := database.ExecContext(ctx, `DELETE FROM whole_site_import_pages WHERE import_id=? AND domain=?`, importID, domain); err != nil {
		return fmt.Errorf("delete completed import frontier pages: %w", err)
	}
	if _, err := database.ExecContext(ctx, `DELETE FROM whole_site_imports WHERE import_id=? AND domain=?`, importID, domain); err != nil {
		return fmt.Errorf("delete completed import frontier: %w", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE domain_storage_usage SET import_queue_bytes=CASE WHEN COALESCE(import_queue_bytes,0)>? THEN import_queue_bytes-? ELSE 0 END,updated_at=? WHERE domain=?`, queueBytes, queueBytes, frontierNow(), domain); err != nil {
		return fmt.Errorf("release import frontier storage: %w", err)
	}
	return nil
}

func DiscardPartialImportFrontiers(ctx context.Context, database importFrontierDatabase, domain, pagePath string) error {
	rows, err := database.QueryContext(ctx, `SELECT import_id,state FROM whole_site_imports WHERE domain=? AND page_path=? AND state IN ('partial','discarding')`, domain, pagePath)
	if err != nil {
		return fmt.Errorf("find previous whole-site imports: %w", err)
	}
	type importRunState struct {
		id    string
		state string
	}
	importRuns := make([]importRunState, 0, 1)
	for rows.Next() {
		var importRun importRunState
		if err := rows.Scan(&importRun.id, &importRun.state); err != nil {
			_ = rows.Close()
			return fmt.Errorf("read previous whole-site import: %w", err)
		}
		importRuns = append(importRuns, importRun)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("read previous whole-site imports: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close previous whole-site imports: %w", err)
	}
	for _, importRun := range importRuns {
		if importRun.state == "partial" {
			result, updateErr := database.ExecContext(ctx, `UPDATE whole_site_imports SET state='discarding',updated_at=? WHERE import_id=? AND domain=? AND state='partial'`, frontierNow(), importRun.id, domain)
			if updateErr != nil {
				return fmt.Errorf("claim previous whole-site import cleanup: %w", updateErr)
			}
			rowsAffected, countErr := result.RowsAffected()
			if countErr != nil {
				return fmt.Errorf("check previous whole-site import cleanup: %w", countErr)
			}
			if rowsAffected == 0 {
				continue
			}
		}
		if err := FinishImportFrontier(ctx, database, importRun.id, domain); err != nil {
			return err
		}
	}
	return nil
}

func CleanupDiscardedImportFrontiers(ctx context.Context, database importFrontierDatabase) error {
	rows, err := database.QueryContext(ctx, `SELECT import_id,domain FROM whole_site_imports WHERE state='discarding'`)
	if err != nil {
		return fmt.Errorf("find interrupted import cleanup: %w", err)
	}
	type importIdentity struct {
		id     string
		domain string
	}
	importRuns := make([]importIdentity, 0, 1)
	for rows.Next() {
		var importRun importIdentity
		if err := rows.Scan(&importRun.id, &importRun.domain); err != nil {
			_ = rows.Close()
			return fmt.Errorf("read interrupted import cleanup: %w", err)
		}
		importRuns = append(importRuns, importRun)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("read interrupted import cleanup: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close interrupted import cleanup: %w", err)
	}
	for _, importRun := range importRuns {
		if err := FinishImportFrontier(ctx, database, importRun.id, importRun.domain); err != nil {
			return err
		}
	}
	return nil
}

func SetImportFrontierRunState(ctx context.Context, database importFrontierDatabase, importID, domain, state string) error {
	if state != "partial" && state != "done" {
		return errors.New("invalid import frontier state")
	}
	_, err := database.ExecContext(ctx, `UPDATE whole_site_imports SET state=?,updated_at=? WHERE import_id=? AND domain=?`, state, frontierNow(), importID, domain)
	if err != nil {
		return fmt.Errorf("update import frontier state: %w", err)
	}
	return nil
}

func frontierNow() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func boolToInt(enabled bool) int {
	if enabled {
		return 1
	}
	return 0
}
