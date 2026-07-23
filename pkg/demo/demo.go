package demo

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const ResetDelay = 30 * time.Minute

type Settings struct {
	Domain        string
	SourceURL     string
	CopyWholeSite bool
	Enabled       bool
}

type Session struct {
	ID           int
	Domain       string
	SessionToken string
	UserEmail    string
	Status       string
	CreatedAt    string
	DeleteAfter  string
}

type Store struct {
	DB Database
}

type Database interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

func SchemaQueries() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS demo_site_sessions(id INTEGER PRIMARY KEY AUTOINCREMENT,domain TEXT,session_token TEXT,user_email TEXT,status TEXT,created_at TEXT,delete_after TEXT);`,
	}
}

func TableNames() []string {
	return []string{"demo_site_sessions"}
}

func AdminEmail(domain string) string {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		domain = "localhost"
	}
	return "demo@" + domain
}

func SnapshotPath(backupRootDir, domainStorageName string) string {
	return path.Join(backupRootDir, "demo-"+strings.TrimSpace(domainStorageName)+"-snapshot.zip")
}

func UniqueDomains(domains ...string) []string {
	seen := make(map[string]struct{}, len(domains))
	result := make([]string, 0, len(domains))
	for _, domain := range domains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain == "" {
			continue
		}
		if _, found := seen[domain]; found {
			continue
		}
		seen[domain] = struct{}{}
		result = append(result, domain)
	}
	return result
}

func NormalizeSourceURL(rawSourceURL string) (string, error) {
	trimmedSourceURL := strings.TrimSpace(rawSourceURL)
	if trimmedSourceURL == "" {
		return "", nil
	}
	if strings.HasPrefix(trimmedSourceURL, "//") {
		trimmedSourceURL = "https:" + trimmedSourceURL
	}
	if !strings.Contains(trimmedSourceURL, "://") {
		trimmedSourceURL = "https://" + trimmedSourceURL
	}
	sourceURL, err := url.Parse(trimmedSourceURL)
	if err != nil || sourceURL.Hostname() == "" || (sourceURL.Scheme != "http" && sourceURL.Scheme != "https") {
		return "", fmt.Errorf("source_url must be an http or https URL")
	}
	sourceURL.Fragment = ""
	return sourceURL.String(), nil
}

func CanStartFromRequest(r *http.Request) bool {
	if r == nil || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return false
	}
	if path.Ext(cleanURLPath(r.URL.Path)) != "" {
		return false
	}
	for _, queryFlag := range []string{"logout", "billing", "billing_backup_download", "backup_download", "captcha", "email_confirm", "grab_events", "grab_ws", "publish_events"} {
		if HasQueryFlag(r, queryFlag) {
			return false
		}
	}
	return true
}

func HasQueryFlag(r *http.Request, key string) bool {
	if r == nil {
		return false
	}
	values, found := r.URL.Query()[key]
	if !found {
		return false
	}
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		if strings.TrimSpace(value) != "" && value != "0" && !strings.EqualFold(value, "false") {
			return true
		}
	}
	return len(values) == 1 && values[0] == ""
}

func SessionsReadyForReset(sessions []Session, now time.Time) bool {
	for _, session := range sessions {
		resetAfter, ok := SessionResetAfter(session)
		if ok && !now.Before(resetAfter) {
			return true
		}
	}
	return false
}

func SessionResetAfter(session Session) (time.Time, bool) {
	if deleteAfter, err := time.Parse(time.RFC3339, strings.TrimSpace(session.DeleteAfter)); err == nil {
		return deleteAfter, true
	}
	createdAt, err := time.Parse(time.RFC3339, strings.TrimSpace(session.CreatedAt))
	if err != nil {
		return time.Time{}, false
	}
	return createdAt.Add(ResetDelay), true
}

func (store Store) Settings(ctx context.Context) Settings {
	settings := Settings{
		Domain:    settingText(ctx, store.DB, "demo_site_domain"),
		SourceURL: settingText(ctx, store.DB, "demo_site_source_url"),
	}
	settings.CopyWholeSite = settingBool(ctx, store.DB, "demo_site_copy_whole_site", false)
	settings.Enabled = settingBool(ctx, store.DB, "demo_site_enabled", strings.TrimSpace(settings.Domain) != "")
	return settings
}

func (store Store) SaveSettings(ctx context.Context, domain, sourceURL string, copyWholeSite bool, enabled bool) error {
	domain = strings.TrimSpace(domain)
	sourceURL = strings.TrimSpace(sourceURL)
	copyWholeSiteValue := "0"
	if copyWholeSite {
		copyWholeSiteValue = "1"
	}
	enabledValue := "0"
	if enabled {
		enabledValue = "1"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	transaction, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, setting := range []struct {
		name  string
		value string
	}{
		{name: "demo_site_enabled", value: enabledValue},
		{name: "demo_site_domain", value: domain},
		{name: "demo_site_source_url", value: sourceURL},
		{name: "demo_site_copy_whole_site", value: copyWholeSiteValue},
	} {
		if _, err = transaction.ExecContext(ctx, `INSERT INTO server_settings(name,value,updated_at) VALUES(?,?,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`,
			setting.name, setting.value, now); err != nil {
			_ = transaction.Rollback()
			return err
		}
	}
	return transaction.Commit()
}

func (store Store) CreateSession(ctx context.Context, domain, sessionToken, userEmail string, resetAfter time.Time) error {
	domain = strings.TrimSpace(domain)
	sessionToken = strings.TrimSpace(sessionToken)
	userEmail = strings.TrimSpace(userEmail)
	if domain == "" {
		return fmt.Errorf("demo domain is required")
	}
	if sessionToken == "" {
		return fmt.Errorf("demo session token is required")
	}
	nowTime := time.Now().UTC()
	if resetAfter.IsZero() {
		resetAfter = nowTime
	}
	now := nowTime.Format(time.RFC3339)
	_, err := store.DB.ExecContext(ctx, `INSERT INTO demo_site_sessions(domain,session_token,user_email,status,created_at,delete_after) VALUES(?,?,?,?,?,?)`,
		domain, sessionToken, userEmail, "active", now, resetAfter.UTC().Format(time.RFC3339))
	return err
}

func (store Store) ScheduleSessionReset(ctx context.Context, sessionToken string, resetAfter time.Time) error {
	sessionToken = strings.TrimSpace(sessionToken)
	if sessionToken == "" {
		return nil
	}
	_, err := store.DB.ExecContext(ctx, `UPDATE demo_site_sessions SET status='deleting',delete_after=? WHERE session_token=? AND status='active'`,
		resetAfter.UTC().Format(time.RFC3339), sessionToken)
	return err
}

func (store Store) Sessions(ctx context.Context) []Session {
	rows, err := store.DB.QueryContext(ctx, `SELECT id,domain,session_token,user_email,status,created_at,delete_after FROM demo_site_sessions ORDER BY id ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	sessions := make([]Session, 0, 8)
	for rows.Next() {
		var session Session
		if scanErr := rows.Scan(&session.ID, &session.Domain, &session.SessionToken, &session.UserEmail, &session.Status, &session.CreatedAt, &session.DeleteAfter); scanErr != nil {
			continue
		}
		sessions = append(sessions, session)
	}
	return sessions
}

func (store Store) RemoveSessionsForDomain(ctx context.Context, domain string) error {
	_, err := store.DB.ExecContext(ctx, `DELETE FROM demo_site_sessions WHERE domain=?`, strings.TrimSpace(domain))
	return err
}

func settingText(ctx context.Context, database Database, name string) string {
	var value string
	_ = database.QueryRowContext(ctx, `SELECT value FROM server_settings WHERE name=?`, strings.TrimSpace(name)).Scan(&value)
	return strings.TrimSpace(value)
}

func settingBool(ctx context.Context, database Database, name string, fallback bool) bool {
	rawValue := strings.ToLower(strings.TrimSpace(settingText(ctx, database, name)))
	if rawValue == "" {
		return fallback
	}
	return rawValue == "1" || rawValue == "true" || rawValue == "yes" || rawValue == "on"
}

func cleanURLPath(rawPath string) string {
	cleanedPath := path.Clean("/" + strings.TrimSpace(rawPath))
	if cleanedPath == "." {
		return "/"
	}
	return cleanedPath
}
