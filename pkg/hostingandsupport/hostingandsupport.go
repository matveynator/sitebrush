package hostingandsupport

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"sitebrush/pkg/demo"
)

const DefaultStorageLimitBytes int64 = 10 * 1024 * 1024 * 1024
const DefaultDeletionBackupRetentionDays = 365
const currentBillingSchemaVersion = 8

type Store struct {
	DB *sql.DB
}

type Plan struct {
	ID                   int
	Name                 string
	QuotaLabel           string
	QuotaInput           string
	QuotaBytes           int64
	SiteLimit            int
	AnalyticsReportLimit int
	Price                string
	Currency             string
	BillingPeriod        string
	IsDefault            bool
}

type Site struct {
	Domain            string
	IsDemo            bool
	IsMainDomain      bool
	Aliases           string
	URL               string
	UsedBytes         int64
	UsedLabel         string
	LimitLabel        string
	FreeLabel         string
	UsedPercent       int
	QuotaInput        string
	PlanID            int
	ServiceStatus     string
	AdminEmails       string
	CanDelete         bool
	DatabasePath      string
	DeletionSizeBytes int64
	DeletionSizeLabel string
}

type SiteRequest struct {
	ID                  int
	Domain              string
	Name                string
	Email               string
	Phone               string
	PlanID              int
	PlanName            string
	PlanQuotaLabel      string
	PlanSiteLimit       int
	PlanAnalyticsLimit  int
	PlanPrice           string
	PlanCurrency        string
	PlanBillingPeriod   string
	Status              string
	OwnerMessage        string
	CreatedAt           string
	UpdatedAt           string
	CanApproveOrReject  bool
	PlanDescriptionText string
}

type SiteUsage struct {
	Domain       string
	Aliases      []string
	UsedBytes    int64
	LimitBytes   int64
	AdminEmails  []string
	DatabasePath string
}

type ServiceAssignment struct {
	PlanID        int
	ServiceStatus string
}

type ServiceMailInstallation struct {
	InstallationID string
	PublicKey      string
	FirstSeenAt    string
	LastSeenAt     string
	LastIP         string
	LastDomain     string
	Blocked        bool
	SentCount      int
	ErrorCount     int
}

type ServiceMailEvent struct {
	ID              int
	InstallationID  string
	SourceDomain    string
	SourceIP        string
	Recipient       string
	RecipientDomain string
	CodeKind        string
	Status          string
	Error           string
	CreatedAt       string
}

type ServiceMailBlock struct {
	ID        int
	Scope     string
	Value     string
	Reason    string
	CreatedAt string
}

type ServiceMailRecipient struct {
	InstallationID string
	RecipientHash  string
	RecipientMask  string
	Status         string
	PurposeScope   string
	CreatedAt      string
	VerifiedAt     string
}

type HostingSnapshot struct {
	Version          int                    `json:"version"`
	InstallationID   string                 `json:"installation_id"`
	OwnerEmail       string                 `json:"owner_email"`
	ServerIP         string                 `json:"server_ip"`
	ServerStatus     string                 `json:"server_status"`
	SitebrushVersion string                 `json:"sitebrush_version"`
	StoragePath      string                 `json:"storage_path"`
	DiskFreeBytes    int64                  `json:"disk_free_bytes"`
	DiskTotalBytes   int64                  `json:"disk_total_bytes"`
	Plans            []HostingSnapshotPlan  `json:"plans"`
	Roles            []HostingSnapshotRole  `json:"roles"`
	Sites            []HostingSnapshotSite  `json:"sites"`
	Events           []HostingSnapshotEvent `json:"events"`
	CreatedAt        string                 `json:"created_at"`
}

type HostingSnapshotSite struct {
	Domain      string   `json:"domain"`
	UsedBytes   int64    `json:"used_bytes"`
	LimitBytes  int64    `json:"limit_bytes"`
	PlanName    string   `json:"plan_name"`
	PlanStatus  string   `json:"plan_status"`
	AdminEmails []string `json:"admin_emails"`
}

type HostingSnapshotPlan struct {
	Name                 string `json:"name"`
	QuotaBytes           int64  `json:"quota_bytes"`
	SiteLimit            int    `json:"site_limit"`
	AnalyticsReportLimit int    `json:"analytics_report_limit"`
	Price                string `json:"price"`
	Currency             string `json:"currency"`
	BillingPeriod        string `json:"billing_period"`
	IsDefault            bool   `json:"is_default"`
}

type HostingSnapshotRole struct {
	Email  string `json:"email"`
	Role   string `json:"role"`
	Scope  string `json:"scope"`
	Domain string `json:"domain"`
}

type HostingSnapshotEvent struct {
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Email     string `json:"email"`
	Domain    string `json:"domain"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
}

type ClientHosting struct {
	InstallationID   string
	OwnerEmail       string
	ServerIP         string
	ServerStatus     string
	SitebrushVersion string
	StoragePath      string
	DiskFreeBytes    int64
	DiskTotalBytes   int64
	DiskFreeLabel    string
	DiskTotalLabel   string
	LastSeenAt       string
	Sites            []ClientHostingSite
	ClientEmails     []string
	SiteCount        int
	TotalUsedBytes   int64
	TotalUsedLabel   string
}

type ClientHostingSite struct {
	Domain      string
	UsedBytes   int64
	LimitBytes  int64
	PlanName    string
	PlanStatus  string
	UsedLabel   string
	LimitLabel  string
	AdminEmails []string
}

type RegistrySyncEvent struct {
	ID             int
	InstallationID string
	Status         string
	Error          string
	CreatedAt      string
}

type SitebrushComKey struct {
	PublicKey      string
	Fingerprint    string
	PrivateKeyPath string
	CreatedAt      string
	UpdatedAt      string
}

type DeletionBackupMetadata struct {
	Version           int      `json:"version"`
	Domain            string   `json:"domain"`
	CreatedAt         string   `json:"created_at"`
	ExpiresAt         string   `json:"expires_at"`
	RetentionDays     int      `json:"retention_days"`
	OwnerContacts     []string `json:"owner_contacts"`
	DeletedBytes      int64    `json:"deleted_bytes"`
	DatabaseFile      string   `json:"database_file"`
	StaticDirectory   string   `json:"static_directory"`
	OriginalDatabase  string   `json:"original_database"`
	OriginalStaticDir string   `json:"original_static_dir"`
}

func Migrate(ctx context.Context, database *sql.DB) error {
	if _, err := database.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(component TEXT PRIMARY KEY,version INTEGER,updated_at TEXT);`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	schemaVersion, err := schemaMigrationVersion(ctx, database, "billing")
	if err != nil {
		return fmt.Errorf("read billing schema version: %w", err)
	}
	schemaComplete, err := hostingAndSupportSchemaComplete(ctx, database)
	if err != nil {
		return fmt.Errorf("verify billing schema: %w", err)
	}
	if schemaVersion >= currentBillingSchemaVersion && schemaComplete {
		return nil
	}
	queries := []string{
		`CREATE TABLE IF NOT EXISTS server_managers(domain TEXT,email TEXT,role TEXT,scope_domain TEXT,created_at TEXT,PRIMARY KEY(domain,email,role,scope_domain));`,
		`CREATE TABLE IF NOT EXISTS server_settings(name TEXT PRIMARY KEY,value TEXT,updated_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS site_service_plans(id INTEGER PRIMARY KEY AUTOINCREMENT,name TEXT UNIQUE,quota_bytes INTEGER,site_limit INTEGER DEFAULT 1,analytics_report_limit INTEGER DEFAULT 0,price TEXT,currency TEXT,billing_period TEXT,is_default INTEGER DEFAULT 0,created_at TEXT,updated_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS site_service_assignments(domain TEXT PRIMARY KEY,plan_id INTEGER DEFAULT 0,service_status TEXT,notes TEXT,updated_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS site_registration_requests(id INTEGER PRIMARY KEY AUTOINCREMENT,domain TEXT,name TEXT,email TEXT,phone TEXT,plan_id INTEGER DEFAULT 0,status TEXT,owner_message TEXT,created_at TEXT,updated_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS site_deletion_backups(id INTEGER PRIMARY KEY AUTOINCREMENT,domain TEXT,archive_path TEXT,file_name TEXT,size_bytes INTEGER,token TEXT,token_created_at TEXT,created_at TEXT,expires_at TEXT,retention_days INTEGER,owner_contacts TEXT,metadata_json TEXT,language_code TEXT,downloaded_at TEXT,download_count INTEGER DEFAULT 0);`,
		`CREATE TABLE IF NOT EXISTS service_mail_installations(installation_id TEXT PRIMARY KEY,public_key TEXT,first_seen_at TEXT,last_seen_at TEXT,last_ip TEXT,last_domain TEXT,blocked INTEGER DEFAULT 0);`,
		`CREATE TABLE IF NOT EXISTS service_mail_events(id INTEGER PRIMARY KEY AUTOINCREMENT,installation_id TEXT,source_domain TEXT,source_ip TEXT,recipient TEXT,recipient_domain TEXT,code_kind TEXT,status TEXT,error TEXT,created_at TEXT);`,
		`CREATE INDEX IF NOT EXISTS idx_service_mail_events_installation_created ON service_mail_events(installation_id,created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_service_mail_events_recipient_created ON service_mail_events(recipient,created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_service_mail_events_domain_created ON service_mail_events(recipient_domain,created_at);`,
		`CREATE TABLE IF NOT EXISTS service_mail_blocks(id INTEGER PRIMARY KEY AUTOINCREMENT,scope TEXT,value TEXT,reason TEXT,created_at TEXT,UNIQUE(scope,value));`,
		`CREATE TABLE IF NOT EXISTS service_mail_recipients(installation_id TEXT,recipient_hash TEXT,recipient_mask TEXT,status TEXT,purpose_scope TEXT,created_at TEXT,verified_at TEXT,PRIMARY KEY(installation_id,recipient_hash));`,
		`CREATE INDEX IF NOT EXISTS idx_service_mail_recipients_installation_status ON service_mail_recipients(installation_id,status);`,
		`CREATE TABLE IF NOT EXISTS client_hostings(installation_id TEXT PRIMARY KEY,owner_email TEXT,server_ip TEXT,server_status TEXT,sitebrush_version TEXT,storage_path TEXT,disk_free_bytes INTEGER DEFAULT 0,disk_total_bytes INTEGER DEFAULT 0,first_seen_at TEXT,last_seen_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS client_hosting_sites(installation_id TEXT,domain TEXT,used_bytes INTEGER DEFAULT 0,limit_bytes INTEGER DEFAULT 0,plan_name TEXT,plan_status TEXT,admin_emails TEXT,updated_at TEXT,PRIMARY KEY(installation_id,domain));`,
		`CREATE INDEX IF NOT EXISTS idx_client_hosting_sites_installation ON client_hosting_sites(installation_id);`,
		`CREATE TABLE IF NOT EXISTS registry_accounts(email TEXT PRIMARY KEY,first_seen_at TEXT,last_seen_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS registry_installation_roles(installation_id TEXT,email TEXT,role TEXT,scope TEXT,domain TEXT,updated_at TEXT,PRIMARY KEY(installation_id,email,role,scope,domain));`,
		`CREATE INDEX IF NOT EXISTS idx_registry_installation_roles_email ON registry_installation_roles(email);`,
		`CREATE TABLE IF NOT EXISTS registry_installation_plans(installation_id TEXT,name TEXT,quota_bytes INTEGER DEFAULT 0,site_limit INTEGER DEFAULT 0,analytics_report_limit INTEGER DEFAULT 0,price TEXT,currency TEXT,billing_period TEXT,is_default INTEGER DEFAULT 0,updated_at TEXT,PRIMARY KEY(installation_id,name));`,
		`CREATE TABLE IF NOT EXISTS registry_events(id INTEGER PRIMARY KEY AUTOINCREMENT,installation_id TEXT,event_key TEXT,kind TEXT,status TEXT,email TEXT,domain TEXT,message TEXT,created_at TEXT,UNIQUE(installation_id,event_key));`,
		`CREATE TABLE IF NOT EXISTS registry_sync_events(id INTEGER PRIMARY KEY AUTOINCREMENT,installation_id TEXT,status TEXT,error TEXT,created_at TEXT);`,
		`CREATE INDEX IF NOT EXISTS idx_registry_sync_events_installation_created ON registry_sync_events(installation_id,created_at);`,
		`CREATE TABLE IF NOT EXISTS sitebrush_com_keys(domain TEXT PRIMARY KEY,public_key TEXT,private_key_path TEXT,fingerprint TEXT,created_at TEXT,updated_at TEXT);`,
	}
	queries = append(queries, demo.SchemaQueries()...)
	for queryIndex, query := range queries {
		if _, err := database.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("billing schema statement %d: %w", queryIndex+1, err)
		}
	}
	for _, column := range requiredHostingAndSupportColumns() {
		found, err := hostingAndSupportColumnExists(ctx, database, column.tableName, column.columnName)
		if err != nil {
			return fmt.Errorf("check billing column %s.%s: %w", column.tableName, column.columnName, err)
		}
		if found {
			continue
		}
		if _, err := database.ExecContext(ctx, `ALTER TABLE `+column.tableName+` ADD COLUMN `+column.columnName+` `+column.definition); err != nil {
			return fmt.Errorf("add billing column %s.%s: %w", column.tableName, column.columnName, err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = database.ExecContext(ctx, `INSERT OR IGNORE INTO server_settings(name,value,updated_at) VALUES('auto_registration_enabled','1',?)`, now)
	_, _ = database.ExecContext(ctx, `INSERT OR IGNORE INTO server_settings(name,value,updated_at) VALUES('deletion_backup_retention_days',?,?)`, strconv.Itoa(DefaultDeletionBackupRetentionDays), now)
	_, _ = database.ExecContext(ctx, `INSERT OR IGNORE INTO server_settings(name,value,updated_at) VALUES('service_mail_relay_enabled','1',?)`, now)
	_, _ = database.ExecContext(ctx, `INSERT OR IGNORE INTO site_service_plans(name,quota_bytes,site_limit,analytics_report_limit,price,currency,billing_period,is_default,created_at,updated_at) VALUES('Free',?,1,0,'0','USD','monthly',1,?,?)`,
		DefaultStorageLimitBytes, now, now)
	if err := setSchemaMigrationVersion(ctx, database, "billing", currentBillingSchemaVersion); err != nil {
		return fmt.Errorf("write billing schema version: %w", err)
	}
	return nil
}

func schemaMigrationVersion(ctx context.Context, database *sql.DB, component string) (int, error) {
	var version int
	err := database.QueryRowContext(ctx, `SELECT version FROM schema_migrations WHERE component=?`, component).Scan(&version)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return version, err
}

func setSchemaMigrationVersion(ctx context.Context, database *sql.DB, component string, version int) error {
	_, err := database.ExecContext(ctx, `INSERT INTO schema_migrations(component,version,updated_at) VALUES(?,?,?) ON CONFLICT(component) DO UPDATE SET version=excluded.version,updated_at=excluded.updated_at`,
		component, version, time.Now().UTC().Format(time.RFC3339))
	return err
}

func hostingAndSupportSchemaComplete(ctx context.Context, database *sql.DB) (bool, error) {
	tableNames := []string{"server_managers", "server_settings", "site_service_plans", "site_service_assignments", "site_registration_requests", "site_deletion_backups", "service_mail_installations", "service_mail_events", "service_mail_blocks", "service_mail_recipients", "client_hostings", "client_hosting_sites", "registry_accounts", "registry_installation_roles", "registry_installation_plans", "registry_events", "registry_sync_events", "sitebrush_com_keys"}
	tableNames = append(tableNames, demo.TableNames()...)
	for _, tableName := range tableNames {
		found, err := tableExists(ctx, database, tableName)
		if err != nil || !found {
			return found, err
		}
	}
	for _, column := range requiredHostingAndSupportColumns() {
		found, err := hostingAndSupportColumnExists(ctx, database, column.tableName, column.columnName)
		if err != nil || !found {
			return found, err
		}
	}
	return true, nil
}

func tableExists(ctx context.Context, database *sql.DB, tableName string) (bool, error) {
	var foundName string
	err := database.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tableName).Scan(&foundName)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil && foundName == tableName, err
}

type hostingAndSupportColumn struct {
	tableName  string
	columnName string
	definition string
}

func requiredHostingAndSupportColumns() []hostingAndSupportColumn {
	return []hostingAndSupportColumn{
		{tableName: "site_service_plans", columnName: "site_limit", definition: "INTEGER DEFAULT 1"},
		{tableName: "site_service_plans", columnName: "analytics_report_limit", definition: "INTEGER DEFAULT 0"},
		{tableName: "client_hostings", columnName: "server_status", definition: "TEXT"},
		{tableName: "client_hostings", columnName: "sitebrush_version", definition: "TEXT"},
		{tableName: "client_hosting_sites", columnName: "plan_name", definition: "TEXT"},
		{tableName: "client_hosting_sites", columnName: "plan_status", definition: "TEXT"},
		{tableName: "sitebrush_com_keys", columnName: "private_key_path", definition: "TEXT"},
	}
}

func hostingAndSupportColumnExists(ctx context.Context, database *sql.DB, tableName, columnName string) (bool, error) {
	rows, err := database.QueryContext(ctx, `PRAGMA table_info(`+tableName+`)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var columnID int
		var name string
		var columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if scanErr := rows.Scan(&columnID, &name, &columnType, &notNull, &defaultValue, &primaryKey); scanErr != nil {
			return false, scanErr
		}
		if strings.EqualFold(name, columnName) {
			return true, nil
		}
	}
	return false, rows.Err()
}

func SettingBool(ctx context.Context, database *sql.DB, name string, fallback bool) bool {
	var value string
	err := database.QueryRowContext(ctx, `SELECT value FROM server_settings WHERE name=?`, name).Scan(&value)
	if err != nil {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func (store Store) OwnerExists(ctx context.Context) bool {
	var ownerCount int
	_ = store.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM server_managers WHERE role='owner'`).Scan(&ownerCount)
	return ownerCount > 0
}

func (store Store) PromoteOwnerIfMissing(ctx context.Context, domain, email string) {
	domain = strings.TrimSpace(domain)
	email = strings.TrimSpace(email)
	if domain == "" || email == "" {
		return
	}
	var ownerCount int
	_ = store.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM server_managers WHERE role='owner'`).Scan(&ownerCount)
	if ownerCount > 0 {
		return
	}
	_, _ = store.DB.ExecContext(ctx, `INSERT OR IGNORE INTO server_managers(domain,email,role,scope_domain,created_at) VALUES(?,?,?,?,?)`,
		domain, email, "owner", "*", time.Now().UTC().Format(time.RFC3339))
}

func (store Store) IsOwner(ctx context.Context, email string) bool {
	var managerCount int
	_ = store.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM server_managers WHERE email=? AND role='owner'`, strings.TrimSpace(email)).Scan(&managerCount)
	return managerCount > 0
}

func (store Store) OwnerEmails(ctx context.Context) []string {
	rows, err := store.DB.QueryContext(ctx, `SELECT email FROM server_managers WHERE role='owner' ORDER BY created_at ASC,email ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	emails := make([]string, 0, 2)
	seen := make(map[string]struct{})
	for rows.Next() {
		var email string
		if scanErr := rows.Scan(&email); scanErr != nil {
			continue
		}
		email = strings.TrimSpace(email)
		if email == "" {
			continue
		}
		if _, found := seen[email]; found {
			continue
		}
		seen[email] = struct{}{}
		emails = append(emails, email)
	}
	return emails
}

func (store Store) OwnerDomain(ctx context.Context) (string, bool) {
	var domain string
	err := store.DB.QueryRowContext(ctx, `SELECT domain FROM server_managers WHERE role='owner' ORDER BY created_at ASC LIMIT 1`).Scan(&domain)
	domain = strings.TrimSpace(domain)
	return domain, err == nil && domain != ""
}

func (store Store) SetOwner(ctx context.Context, domain, email string) error {
	domain = strings.TrimSpace(domain)
	email = strings.TrimSpace(email)
	if domain == "" {
		return fmt.Errorf("owner domain is required")
	}
	if email == "" {
		return fmt.Errorf("owner email is required")
	}
	transaction, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	_, err = transaction.ExecContext(ctx, `DELETE FROM server_managers WHERE role='owner'`)
	if err == nil {
		_, err = transaction.ExecContext(ctx, `INSERT INTO server_managers(domain,email,role,scope_domain,created_at) VALUES(?,?,?,?,?)`,
			domain, email, "owner", "*", time.Now().UTC().Format(time.RFC3339))
	}
	if err != nil {
		_ = transaction.Rollback()
		return err
	}
	return transaction.Commit()
}

func (store Store) AutomaticRegistrationAllowed(ctx context.Context) bool {
	if !store.OwnerExists(ctx) {
		return true
	}
	return SettingBool(ctx, store.DB, "auto_registration_enabled", true)
}

func (store Store) SaveSettings(ctx context.Context, autoRegistrationEnabled bool) error {
	settingValue := "0"
	if autoRegistrationEnabled {
		settingValue = "1"
	}
	_, err := store.DB.ExecContext(ctx, `INSERT INTO server_settings(name,value,updated_at) VALUES(?,?,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`,
		"auto_registration_enabled", settingValue, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (store Store) ServiceMailRelayEnabled(ctx context.Context) bool {
	return SettingBool(ctx, store.DB, "service_mail_relay_enabled", true)
}

func (store Store) SaveServiceMailSettings(ctx context.Context, relayEnabled bool) error {
	settingValue := "0"
	if relayEnabled {
		settingValue = "1"
	}
	_, err := store.DB.ExecContext(ctx, `INSERT INTO server_settings(name,value,updated_at) VALUES(?,?,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`,
		"service_mail_relay_enabled", settingValue, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (store Store) AssignSite(ctx context.Context, domain string, planID int, serviceStatus string) error {
	_, err := store.DB.ExecContext(ctx, `INSERT INTO site_service_assignments(domain,plan_id,service_status,notes,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(domain) DO UPDATE SET plan_id=excluded.plan_id,service_status=excluded.service_status,updated_at=excluded.updated_at`,
		strings.TrimSpace(domain), planID, strings.TrimSpace(serviceStatus), "", time.Now().UTC().Format(time.RFC3339))
	return err
}

func SettingText(ctx context.Context, database *sql.DB, name string) string {
	var value string
	err := database.QueryRowContext(ctx, `SELECT value FROM server_settings WHERE name=?`, name).Scan(&value)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func settingText(ctx context.Context, database *sql.DB, name string) string {
	return SettingText(ctx, database, name)
}

func (store Store) RemoveSiteAssignment(ctx context.Context, domain string) {
	_, _ = store.DB.ExecContext(ctx, `DELETE FROM site_service_assignments WHERE domain=?`, strings.TrimSpace(domain))
}

func (store Store) SavePlan(ctx context.Context, planID int, name string, quotaBytes int64, siteLimit, analyticsReportLimit int, price, currency, billingPeriod string, isDefault bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("plan name is required")
	}
	if quotaBytes <= 0 {
		return fmt.Errorf("plan quota is required")
	}
	if siteLimit <= 0 {
		return fmt.Errorf("plan site limit is required")
	}
	if analyticsReportLimit < 0 {
		return fmt.Errorf("plan analytics report limit cannot be negative")
	}
	currency = strings.TrimSpace(currency)
	if currency == "" {
		currency = "USD"
	}
	billingPeriod = strings.TrimSpace(billingPeriod)
	if billingPeriod == "" {
		billingPeriod = "monthly"
	}
	defaultFlag := 0
	if isDefault {
		defaultFlag = 1
		_, _ = store.DB.ExecContext(ctx, `UPDATE site_service_plans SET is_default=0`)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var err error
	if planID > 0 {
		_, err = store.DB.ExecContext(ctx, `UPDATE site_service_plans SET name=?,quota_bytes=?,site_limit=?,analytics_report_limit=?,price=?,currency=?,billing_period=?,is_default=?,updated_at=? WHERE id=?`,
			name, quotaBytes, siteLimit, analyticsReportLimit, strings.TrimSpace(price), currency, billingPeriod, defaultFlag, now, planID)
	} else {
		_, err = store.DB.ExecContext(ctx, `INSERT INTO site_service_plans(name,quota_bytes,site_limit,analytics_report_limit,price,currency,billing_period,is_default,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			name, quotaBytes, siteLimit, analyticsReportLimit, strings.TrimSpace(price), currency, billingPeriod, defaultFlag, now, now)
	}
	return err
}

func (store Store) DeletePlan(ctx context.Context, planID int) error {
	if planID <= 0 {
		return fmt.Errorf("plan is required")
	}
	_, _ = store.DB.ExecContext(ctx, `UPDATE site_service_assignments SET plan_id=0 WHERE plan_id=?`, planID)
	_, err := store.DB.ExecContext(ctx, `DELETE FROM site_service_plans WHERE id=?`, planID)
	return err
}

func (store Store) Plans(ctx context.Context) []Plan {
	rows, err := store.DB.QueryContext(ctx, `SELECT id,name,quota_bytes,site_limit,analytics_report_limit,price,currency,billing_period,is_default FROM site_service_plans ORDER BY is_default DESC, quota_bytes ASC, name ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	plans := make([]Plan, 0, 4)
	for rows.Next() {
		var plan Plan
		var isDefault int
		if scanErr := rows.Scan(&plan.ID, &plan.Name, &plan.QuotaBytes, &plan.SiteLimit, &plan.AnalyticsReportLimit, &plan.Price, &plan.Currency, &plan.BillingPeriod, &isDefault); scanErr != nil {
			continue
		}
		preparePlanView(&plan, isDefault)
		plans = append(plans, plan)
	}
	return plans
}

func (store Store) PlanByID(ctx context.Context, planID int) (Plan, bool) {
	var plan Plan
	var isDefault int
	err := store.DB.QueryRowContext(ctx, `SELECT id,name,quota_bytes,site_limit,analytics_report_limit,price,currency,billing_period,is_default FROM site_service_plans WHERE id=?`, planID).Scan(
		&plan.ID, &plan.Name, &plan.QuotaBytes, &plan.SiteLimit, &plan.AnalyticsReportLimit, &plan.Price, &plan.Currency, &plan.BillingPeriod, &isDefault)
	if err != nil {
		return Plan{}, false
	}
	preparePlanView(&plan, isDefault)
	return plan, true
}

func (store Store) DefaultPlan(ctx context.Context) (Plan, bool) {
	var plan Plan
	var isDefault int
	err := store.DB.QueryRowContext(ctx, `SELECT id,name,quota_bytes,site_limit,analytics_report_limit,price,currency,billing_period,is_default FROM site_service_plans ORDER BY is_default DESC, quota_bytes ASC, name ASC LIMIT 1`).Scan(
		&plan.ID, &plan.Name, &plan.QuotaBytes, &plan.SiteLimit, &plan.AnalyticsReportLimit, &plan.Price, &plan.Currency, &plan.BillingPeriod, &isDefault)
	if err != nil {
		return Plan{}, false
	}
	preparePlanView(&plan, isDefault)
	return plan, true
}

func preparePlanView(plan *Plan, isDefault int) {
	plan.QuotaLabel = FormatFileSize(plan.QuotaBytes)
	plan.QuotaInput = FormatQuotaInput(plan.QuotaBytes)
	if plan.SiteLimit <= 0 {
		plan.SiteLimit = 1
	}
	if plan.AnalyticsReportLimit < 0 {
		plan.AnalyticsReportLimit = 0
	}
	plan.IsDefault = isDefault == 1
}

func (store Store) ServiceAssignments(ctx context.Context) map[string]ServiceAssignment {
	rows, err := store.DB.QueryContext(ctx, `SELECT domain,plan_id,service_status FROM site_service_assignments`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	assignments := make(map[string]ServiceAssignment)
	for rows.Next() {
		var domain string
		var assignment ServiceAssignment
		if scanErr := rows.Scan(&domain, &assignment.PlanID, &assignment.ServiceStatus); scanErr != nil {
			continue
		}
		domain = strings.TrimSpace(domain)
		if domain != "" {
			assignments[domain] = assignment
		}
	}
	return assignments
}

func (store Store) CreateSiteRequest(ctx context.Context, domain, name, email, phone string, planID int) error {
	domain = strings.TrimSpace(domain)
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)
	phone = strings.TrimSpace(phone)
	if domain == "" {
		return fmt.Errorf("site domain is required")
	}
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if email == "" {
		return fmt.Errorf("email is required")
	}
	if phone == "" {
		return fmt.Errorf("phone is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := store.DB.ExecContext(ctx, `INSERT INTO site_registration_requests(domain,name,email,phone,plan_id,status,owner_message,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		domain, name, email, phone, planID, "pending", "", now, now)
	return err
}

func (store Store) SiteRequests(ctx context.Context) []SiteRequest {
	plans := store.Plans(ctx)
	planByID := make(map[int]Plan, len(plans))
	for _, plan := range plans {
		planByID[plan.ID] = plan
	}
	rows, err := store.DB.QueryContext(ctx, `SELECT id,domain,name,email,phone,plan_id,status,owner_message,created_at,updated_at FROM site_registration_requests ORDER BY CASE status WHEN 'pending' THEN 0 WHEN 'approved' THEN 1 ELSE 2 END, created_at DESC, id DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	requests := make([]SiteRequest, 0, 8)
	for rows.Next() {
		var request SiteRequest
		if scanErr := rows.Scan(&request.ID, &request.Domain, &request.Name, &request.Email, &request.Phone, &request.PlanID, &request.Status, &request.OwnerMessage, &request.CreatedAt, &request.UpdatedAt); scanErr != nil {
			continue
		}
		applyPlanToSiteRequest(&request, planByID[request.PlanID])
		request.CanApproveOrReject = strings.TrimSpace(request.Status) == "pending"
		requests = append(requests, request)
	}
	return requests
}

func (store Store) SiteRequestByID(ctx context.Context, requestID int) (SiteRequest, bool) {
	var request SiteRequest
	err := store.DB.QueryRowContext(ctx, `SELECT id,domain,name,email,phone,plan_id,status,owner_message,created_at,updated_at FROM site_registration_requests WHERE id=?`, requestID).Scan(
		&request.ID, &request.Domain, &request.Name, &request.Email, &request.Phone, &request.PlanID, &request.Status, &request.OwnerMessage, &request.CreatedAt, &request.UpdatedAt)
	if err != nil {
		return SiteRequest{}, false
	}
	if plan, found := store.PlanByID(ctx, request.PlanID); found {
		applyPlanToSiteRequest(&request, plan)
	}
	request.CanApproveOrReject = strings.TrimSpace(request.Status) == "pending"
	return request, true
}

func (store Store) UpdateSiteRequestStatus(ctx context.Context, requestID int, status, ownerMessage string) error {
	status = strings.TrimSpace(status)
	if status == "" {
		return fmt.Errorf("request status is required")
	}
	_, err := store.DB.ExecContext(ctx, `UPDATE site_registration_requests SET status=?,owner_message=?,updated_at=? WHERE id=?`,
		status, strings.TrimSpace(ownerMessage), time.Now().UTC().Format(time.RFC3339), requestID)
	return err
}

func (store Store) UpsertServiceMailInstallation(ctx context.Context, installationID, publicKey, sourceIP, sourceDomain string) error {
	installationID = strings.TrimSpace(installationID)
	publicKey = strings.TrimSpace(publicKey)
	if installationID == "" {
		return fmt.Errorf("installation id is required")
	}
	if publicKey == "" {
		return fmt.Errorf("public key is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := store.DB.ExecContext(ctx, `INSERT INTO service_mail_installations(installation_id,public_key,first_seen_at,last_seen_at,last_ip,last_domain,blocked) VALUES(?,?,?,?,?,?,0) ON CONFLICT(installation_id) DO UPDATE SET last_seen_at=excluded.last_seen_at,last_ip=excluded.last_ip,last_domain=excluded.last_domain`,
		installationID, publicKey, now, now, strings.TrimSpace(sourceIP), strings.TrimSpace(sourceDomain))
	return err
}

func (store Store) ServiceMailInstallationPublicKey(ctx context.Context, installationID string) (string, bool) {
	var publicKey string
	err := store.DB.QueryRowContext(ctx, `SELECT public_key FROM service_mail_installations WHERE installation_id=?`, strings.TrimSpace(installationID)).Scan(&publicKey)
	publicKey = strings.TrimSpace(publicKey)
	return publicKey, err == nil && publicKey != ""
}

func (store Store) ServiceMailInstallationFirstSeenAt(ctx context.Context, installationID string) (time.Time, bool) {
	var firstSeenText string
	err := store.DB.QueryRowContext(ctx, `SELECT first_seen_at FROM service_mail_installations WHERE installation_id=?`, strings.TrimSpace(installationID)).Scan(&firstSeenText)
	if err != nil {
		return time.Time{}, false
	}
	firstSeenAt, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(firstSeenText))
	return firstSeenAt, parseErr == nil
}

func (store Store) ServiceMailInstallationBlocked(ctx context.Context, installationID string) bool {
	var blocked int
	_ = store.DB.QueryRowContext(ctx, `SELECT blocked FROM service_mail_installations WHERE installation_id=?`, strings.TrimSpace(installationID)).Scan(&blocked)
	return blocked != 0
}

func (store Store) SetServiceMailInstallationBlocked(ctx context.Context, installationID string, blocked bool) error {
	blockedValue := 0
	if blocked {
		blockedValue = 1
	}
	_, err := store.DB.ExecContext(ctx, `UPDATE service_mail_installations SET blocked=?,last_seen_at=? WHERE installation_id=?`,
		blockedValue, time.Now().UTC().Format(time.RFC3339), strings.TrimSpace(installationID))
	return err
}

func (store Store) SaveHostingSnapshot(ctx context.Context, snapshot HostingSnapshot) error {
	installationID := strings.TrimSpace(snapshot.InstallationID)
	if installationID == "" {
		return fmt.Errorf("installation id is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if strings.TrimSpace(snapshot.CreatedAt) != "" {
		now = strings.TrimSpace(snapshot.CreatedAt)
	}
	transaction, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	_, err = transaction.ExecContext(ctx, `INSERT INTO client_hostings(installation_id,owner_email,server_ip,server_status,sitebrush_version,storage_path,disk_free_bytes,disk_total_bytes,first_seen_at,last_seen_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(installation_id) DO UPDATE SET owner_email=excluded.owner_email,server_ip=excluded.server_ip,server_status=excluded.server_status,sitebrush_version=excluded.sitebrush_version,storage_path=excluded.storage_path,disk_free_bytes=excluded.disk_free_bytes,disk_total_bytes=excluded.disk_total_bytes,last_seen_at=excluded.last_seen_at`,
		installationID, strings.ToLower(strings.TrimSpace(snapshot.OwnerEmail)), strings.TrimSpace(snapshot.ServerIP), strings.TrimSpace(snapshot.ServerStatus), strings.TrimSpace(snapshot.SitebrushVersion), strings.TrimSpace(snapshot.StoragePath), snapshot.DiskFreeBytes, snapshot.DiskTotalBytes, now, now)
	if err == nil {
		_, err = transaction.ExecContext(ctx, `DELETE FROM client_hosting_sites WHERE installation_id=?`, installationID)
	}
	if err == nil {
		_, err = transaction.ExecContext(ctx, `DELETE FROM registry_installation_roles WHERE installation_id=?`, installationID)
	}
	if err == nil {
		_, err = transaction.ExecContext(ctx, `DELETE FROM registry_installation_plans WHERE installation_id=?`, installationID)
	}
	for _, site := range snapshot.Sites {
		if err != nil {
			break
		}
		domain := strings.ToLower(strings.TrimSpace(site.Domain))
		if domain == "" {
			continue
		}
		adminEmails := normalizedHostingEmails(site.AdminEmails)
		_, err = transaction.ExecContext(ctx, `INSERT INTO client_hosting_sites(installation_id,domain,used_bytes,limit_bytes,plan_name,plan_status,admin_emails,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
			installationID, domain, site.UsedBytes, site.LimitBytes, strings.TrimSpace(site.PlanName), strings.TrimSpace(site.PlanStatus), strings.Join(adminEmails, ","), now)
		for _, adminEmail := range adminEmails {
			if err != nil {
				break
			}
			err = upsertRegistryAccountTx(ctx, transaction, adminEmail, now)
		}
	}
	for _, plan := range snapshot.Plans {
		if err != nil {
			break
		}
		planName := strings.TrimSpace(plan.Name)
		if planName == "" {
			continue
		}
		defaultFlag := 0
		if plan.IsDefault {
			defaultFlag = 1
		}
		_, err = transaction.ExecContext(ctx, `INSERT INTO registry_installation_plans(installation_id,name,quota_bytes,site_limit,analytics_report_limit,price,currency,billing_period,is_default,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(installation_id,name) DO UPDATE SET quota_bytes=excluded.quota_bytes,site_limit=excluded.site_limit,analytics_report_limit=excluded.analytics_report_limit,price=excluded.price,currency=excluded.currency,billing_period=excluded.billing_period,is_default=excluded.is_default,updated_at=excluded.updated_at`,
			installationID, planName, plan.QuotaBytes, plan.SiteLimit, plan.AnalyticsReportLimit, strings.TrimSpace(plan.Price), strings.TrimSpace(plan.Currency), strings.TrimSpace(plan.BillingPeriod), defaultFlag, now)
	}
	for _, role := range snapshot.Roles {
		if err != nil {
			break
		}
		email := strings.ToLower(strings.TrimSpace(role.Email))
		roleName := strings.TrimSpace(role.Role)
		scope := strings.TrimSpace(role.Scope)
		domain := strings.ToLower(strings.TrimSpace(role.Domain))
		if email == "" || roleName == "" {
			continue
		}
		if scope == "" {
			scope = "site"
		}
		if err = upsertRegistryAccountTx(ctx, transaction, email, now); err != nil {
			break
		}
		_, err = transaction.ExecContext(ctx, `INSERT OR IGNORE INTO registry_installation_roles(installation_id,email,role,scope,domain,updated_at) VALUES(?,?,?,?,?,?)`,
			installationID, email, roleName, scope, domain, now)
	}
	for _, event := range snapshot.Events {
		if err != nil {
			break
		}
		eventCreatedAt := strings.TrimSpace(event.CreatedAt)
		if eventCreatedAt == "" {
			eventCreatedAt = now
		}
		eventKey := registryEventKey(event)
		if eventKey == "" {
			continue
		}
		_, err = transaction.ExecContext(ctx, `INSERT OR IGNORE INTO registry_events(installation_id,event_key,kind,status,email,domain,message,created_at) VALUES(?,?,?,?,?,?,?,?)`,
			installationID, eventKey, strings.TrimSpace(event.Kind), strings.TrimSpace(event.Status), strings.ToLower(strings.TrimSpace(event.Email)), strings.ToLower(strings.TrimSpace(event.Domain)), strings.TrimSpace(event.Message), eventCreatedAt)
	}
	if err != nil {
		_ = transaction.Rollback()
		_ = store.LogRegistrySyncEvent(ctx, installationID, "error", err.Error())
		return err
	}
	if err = transaction.Commit(); err != nil {
		_ = store.LogRegistrySyncEvent(ctx, installationID, "error", err.Error())
		return err
	}
	return store.LogRegistrySyncEvent(ctx, installationID, "stored", "")
}

func normalizedHostingEmails(rawEmails []string) []string {
	seen := make(map[string]struct{}, len(rawEmails))
	emails := make([]string, 0, len(rawEmails))
	for _, rawEmail := range rawEmails {
		email := strings.ToLower(strings.TrimSpace(rawEmail))
		if email == "" {
			continue
		}
		if _, found := seen[email]; found {
			continue
		}
		seen[email] = struct{}{}
		emails = append(emails, email)
	}
	sort.Strings(emails)
	return emails
}

func upsertRegistryAccountTx(ctx context.Context, transaction *sql.Tx, email string, now string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil
	}
	_, err := transaction.ExecContext(ctx, `INSERT INTO registry_accounts(email,first_seen_at,last_seen_at) VALUES(?,?,?) ON CONFLICT(email) DO UPDATE SET last_seen_at=excluded.last_seen_at`,
		email, now, now)
	return err
}

func registryEventKey(event HostingSnapshotEvent) string {
	parts := []string{
		strings.TrimSpace(event.Kind),
		strings.TrimSpace(event.Status),
		strings.ToLower(strings.TrimSpace(event.Email)),
		strings.ToLower(strings.TrimSpace(event.Domain)),
		strings.TrimSpace(event.Message),
		strings.TrimSpace(event.CreatedAt),
	}
	joined := strings.Join(parts, "\n")
	if strings.TrimSpace(joined) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(joined))
	return hex.EncodeToString(sum[:])
}

func (store Store) LogRegistrySyncEvent(ctx context.Context, installationID, status, errorMessage string) error {
	installationID = strings.TrimSpace(installationID)
	if installationID == "" {
		return nil
	}
	_, err := store.DB.ExecContext(ctx, `INSERT INTO registry_sync_events(installation_id,status,error,created_at) VALUES(?,?,?,?)`,
		installationID, strings.TrimSpace(status), strings.TrimSpace(errorMessage), time.Now().UTC().Format(time.RFC3339))
	return err
}

func (store Store) ClientHostings(ctx context.Context) []ClientHosting {
	rows, err := store.DB.QueryContext(ctx, `SELECT installation_id,owner_email,server_ip,server_status,sitebrush_version,storage_path,disk_free_bytes,disk_total_bytes,last_seen_at FROM client_hostings ORDER BY last_seen_at DESC,installation_id ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	hostings := make([]ClientHosting, 0, 8)
	for rows.Next() {
		var hosting ClientHosting
		if scanErr := rows.Scan(&hosting.InstallationID, &hosting.OwnerEmail, &hosting.ServerIP, &hosting.ServerStatus, &hosting.SitebrushVersion, &hosting.StoragePath, &hosting.DiskFreeBytes, &hosting.DiskTotalBytes, &hosting.LastSeenAt); scanErr != nil {
			continue
		}
		hosting.DiskFreeLabel = FormatFileSize(hosting.DiskFreeBytes)
		hosting.DiskTotalLabel = FormatFileSize(hosting.DiskTotalBytes)
		hostings = append(hostings, hosting)
	}
	for hostingIndex := range hostings {
		hostings[hostingIndex].Sites = store.clientHostingSites(ctx, hostings[hostingIndex].InstallationID)
		hostings[hostingIndex].SiteCount = len(hostings[hostingIndex].Sites)
		emailSet := make(map[string]struct{})
		if strings.TrimSpace(hostings[hostingIndex].OwnerEmail) != "" {
			emailSet[strings.TrimSpace(hostings[hostingIndex].OwnerEmail)] = struct{}{}
		}
		for _, site := range hostings[hostingIndex].Sites {
			hostings[hostingIndex].TotalUsedBytes += site.UsedBytes
			for _, email := range site.AdminEmails {
				emailSet[email] = struct{}{}
			}
		}
		hostings[hostingIndex].ClientEmails = sortedStringsFromMap(emailSet)
		hostings[hostingIndex].TotalUsedLabel = FormatFileSize(hostings[hostingIndex].TotalUsedBytes)
	}
	return hostings
}

func (store Store) clientHostingSites(ctx context.Context, installationID string) []ClientHostingSite {
	rows, err := store.DB.QueryContext(ctx, `SELECT domain,used_bytes,limit_bytes,plan_name,plan_status,admin_emails FROM client_hosting_sites WHERE installation_id=? ORDER BY used_bytes DESC,domain ASC`, strings.TrimSpace(installationID))
	if err != nil {
		return nil
	}
	defer rows.Close()
	sites := make([]ClientHostingSite, 0, 8)
	for rows.Next() {
		var site ClientHostingSite
		var adminEmails string
		if scanErr := rows.Scan(&site.Domain, &site.UsedBytes, &site.LimitBytes, &site.PlanName, &site.PlanStatus, &adminEmails); scanErr != nil {
			continue
		}
		site.AdminEmails = normalizedHostingEmails(strings.Split(adminEmails, ","))
		site.UsedLabel = FormatFileSize(site.UsedBytes)
		site.LimitLabel = FormatFileSize(site.LimitBytes)
		sites = append(sites, site)
	}
	return sites
}

func (store Store) RegistrySyncEvents(ctx context.Context, limit int) []RegistrySyncEvent {
	if limit <= 0 {
		limit = 50
	}
	rows, err := store.DB.QueryContext(ctx, `SELECT id,installation_id,status,error,created_at FROM registry_sync_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	events := make([]RegistrySyncEvent, 0, limit)
	for rows.Next() {
		var event RegistrySyncEvent
		if scanErr := rows.Scan(&event.ID, &event.InstallationID, &event.Status, &event.Error, &event.CreatedAt); scanErr != nil {
			continue
		}
		events = append(events, event)
	}
	return events
}

func (store Store) SitebrushComKey(ctx context.Context) SitebrushComKey {
	var key SitebrushComKey
	_ = store.DB.QueryRowContext(ctx, `SELECT public_key,fingerprint,private_key_path,created_at,updated_at FROM sitebrush_com_keys WHERE domain='sitebrush.com'`).Scan(
		&key.PublicKey, &key.Fingerprint, &key.PrivateKeyPath, &key.CreatedAt, &key.UpdatedAt)
	return key
}

func (store Store) SaveSitebrushComKey(ctx context.Context, publicKey, privateKeyPath string) (SitebrushComKey, error) {
	publicKey = strings.TrimSpace(publicKey)
	privateKeyPath = strings.TrimSpace(privateKeyPath)
	if publicKey == "" || privateKeyPath == "" {
		return SitebrushComKey{}, fmt.Errorf("public key and private key path are required")
	}
	fingerprint := FingerprintPublicKey(publicKey)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := store.DB.ExecContext(ctx, `INSERT INTO sitebrush_com_keys(domain,public_key,private_key_path,fingerprint,created_at,updated_at) VALUES('sitebrush.com',?,?,?,?,?) ON CONFLICT(domain) DO UPDATE SET public_key=excluded.public_key,private_key_path=excluded.private_key_path,fingerprint=excluded.fingerprint,updated_at=excluded.updated_at`,
		publicKey, privateKeyPath, fingerprint, now, now)
	if err != nil {
		return SitebrushComKey{}, err
	}
	_ = store.clearLegacySitebrushComPrivateKey(ctx)
	return store.SitebrushComKey(ctx), nil
}

func (store Store) clearLegacySitebrushComPrivateKey(ctx context.Context) error {
	found, err := hostingAndSupportColumnExists(ctx, store.DB, "sitebrush_com_keys", "private_key")
	if err != nil || !found {
		return err
	}
	_, err = store.DB.ExecContext(ctx, `UPDATE sitebrush_com_keys SET private_key='' WHERE domain='sitebrush.com'`)
	return err
}

func FingerprintPublicKey(publicKey string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(publicKey)))
	encoded := hex.EncodeToString(sum[:])
	groups := make([]string, 0, 8)
	for index := 0; index < len(encoded) && index < 32; index += 4 {
		groups = append(groups, encoded[index:index+4])
	}
	return strings.Join(groups, ":")
}

func sortedStringsFromMap(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func (store Store) CreateServiceMailBlock(ctx context.Context, scope, value, reason string) error {
	scope = strings.TrimSpace(scope)
	value = strings.TrimSpace(value)
	if scope == "" || value == "" {
		return fmt.Errorf("block scope and value are required")
	}
	_, err := store.DB.ExecContext(ctx, `INSERT OR REPLACE INTO service_mail_blocks(scope,value,reason,created_at) VALUES(?,?,?,?)`,
		scope, value, strings.TrimSpace(reason), time.Now().UTC().Format(time.RFC3339))
	return err
}

func (store Store) DeleteServiceMailBlock(ctx context.Context, blockID int) error {
	_, err := store.DB.ExecContext(ctx, `DELETE FROM service_mail_blocks WHERE id=?`, blockID)
	return err
}

func (store Store) ServiceMailBlocked(ctx context.Context, installationID, sourceIP, recipient, recipientDomain string) (string, bool) {
	candidates := []struct {
		scope string
		value string
	}{
		{scope: "installation", value: strings.TrimSpace(installationID)},
		{scope: "ip", value: strings.TrimSpace(sourceIP)},
		{scope: "subnet", value: ServiceMailIPv4Subnet(sourceIP)},
		{scope: "recipient", value: strings.ToLower(strings.TrimSpace(recipient))},
		{scope: "recipient_domain", value: strings.ToLower(strings.TrimSpace(recipientDomain))},
	}
	for _, candidate := range candidates {
		if candidate.value == "" {
			continue
		}
		var reason string
		err := store.DB.QueryRowContext(ctx, `SELECT reason FROM service_mail_blocks WHERE scope=? AND value=?`, candidate.scope, candidate.value).Scan(&reason)
		if err == nil {
			return candidate.scope + ":" + candidate.value + " " + strings.TrimSpace(reason), true
		}
	}
	return "", false
}

func ServiceMailIPv4Subnet(rawIP string) string {
	parsedIP := netParseIPv4(rawIP)
	if parsedIP == "" {
		return ""
	}
	parts := strings.Split(parsedIP, ".")
	if len(parts) != 4 {
		return ""
	}
	return strings.Join(parts[:3], ".") + ".0/24"
}

func netParseIPv4(rawIP string) string {
	parts := strings.Split(strings.TrimSpace(rawIP), ".")
	if len(parts) != 4 {
		return ""
	}
	for _, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 || number > 255 {
			return ""
		}
	}
	return strings.Join(parts, ".")
}

func (store Store) CountServiceMailEventsSince(ctx context.Context, columnName, value string, since time.Time) int {
	switch columnName {
	case "installation_id", "source_domain", "source_ip", "recipient", "recipient_domain":
	default:
		return 0
	}
	var count int
	_ = store.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM service_mail_events WHERE `+columnName+`=? AND created_at>=?`,
		strings.TrimSpace(value), since.UTC().Format(time.RFC3339)).Scan(&count)
	return count
}

func (store Store) CountServiceMailSubnetEventsSince(ctx context.Context, subnet string, since time.Time) int {
	subnet = strings.TrimSpace(subnet)
	if !strings.HasSuffix(subnet, ".0/24") {
		return 0
	}
	prefix := strings.TrimSuffix(subnet, "0/24")
	if prefix == "" {
		return 0
	}
	var count int
	_ = store.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM service_mail_events WHERE source_ip LIKE ? AND created_at>=?`,
		prefix+"%", since.UTC().Format(time.RFC3339)).Scan(&count)
	return count
}

func (store Store) UpsertServiceMailRecipient(ctx context.Context, installationID, recipient, status, purposeScope string) error {
	installationID = strings.TrimSpace(installationID)
	recipientHash := ServiceMailRecipientHash(recipient)
	if installationID == "" || recipientHash == "" {
		return fmt.Errorf("service mail recipient is invalid")
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = "pending"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	verifiedAt := ""
	if status == "verified" {
		verifiedAt = now
	}
	_, err := store.DB.ExecContext(ctx, `INSERT INTO service_mail_recipients(installation_id,recipient_hash,recipient_mask,status,purpose_scope,created_at,verified_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(installation_id,recipient_hash) DO UPDATE SET status=excluded.status,purpose_scope=excluded.purpose_scope,verified_at=CASE WHEN excluded.verified_at<>'' THEN excluded.verified_at ELSE service_mail_recipients.verified_at END`,
		installationID, recipientHash, MaskServiceMailRecipient(recipient), status, strings.TrimSpace(purposeScope), now, verifiedAt)
	return err
}

func (store Store) ServiceMailRecipientVerified(ctx context.Context, installationID, recipient string) bool {
	var status string
	err := store.DB.QueryRowContext(ctx, `SELECT status FROM service_mail_recipients WHERE installation_id=? AND recipient_hash=?`,
		strings.TrimSpace(installationID), ServiceMailRecipientHash(recipient)).Scan(&status)
	return err == nil && strings.TrimSpace(status) == "verified"
}

func (store Store) CountServiceMailVerifiedRecipients(ctx context.Context, installationID string) int {
	var count int
	_ = store.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM service_mail_recipients WHERE installation_id=? AND status='verified'`, strings.TrimSpace(installationID)).Scan(&count)
	return count
}

func (store Store) CountServiceMailRecipientsSince(ctx context.Context, installationID string, since time.Time) int {
	var count int
	_ = store.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM service_mail_recipients WHERE installation_id=? AND created_at>=?`,
		strings.TrimSpace(installationID), since.UTC().Format(time.RFC3339)).Scan(&count)
	return count
}

func ServiceMailRecipientHash(recipient string) string {
	normalizedRecipient := strings.ToLower(strings.TrimSpace(recipient))
	if normalizedRecipient == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("sitebrush service mail recipient\n" + normalizedRecipient))
	return hex.EncodeToString(sum[:])
}

func MaskServiceMailRecipient(recipient string) string {
	recipient = strings.ToLower(strings.TrimSpace(recipient))
	atIndex := strings.LastIndex(recipient, "@")
	if atIndex <= 0 || atIndex == len(recipient)-1 {
		return ""
	}
	localPart := recipient[:atIndex]
	domain := recipient[atIndex+1:]
	if len(localPart) <= 2 {
		return localPart[:1] + "***@" + domain
	}
	return localPart[:1] + "***" + localPart[len(localPart)-1:] + "@" + domain
}

func (store Store) LogServiceMailEvent(ctx context.Context, event ServiceMailEvent) error {
	_, err := store.DB.ExecContext(ctx, `INSERT INTO service_mail_events(installation_id,source_domain,source_ip,recipient,recipient_domain,code_kind,status,error,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		strings.TrimSpace(event.InstallationID),
		strings.TrimSpace(event.SourceDomain),
		strings.TrimSpace(event.SourceIP),
		strings.TrimSpace(event.Recipient),
		strings.TrimSpace(event.RecipientDomain),
		strings.TrimSpace(event.CodeKind),
		strings.TrimSpace(event.Status),
		strings.TrimSpace(event.Error),
		time.Now().UTC().Format(time.RFC3339))
	return err
}

func (store Store) ServiceMailInstallations(ctx context.Context) []ServiceMailInstallation {
	rows, err := store.DB.QueryContext(ctx, `SELECT installation_id,public_key,first_seen_at,last_seen_at,last_ip,last_domain,blocked FROM service_mail_installations ORDER BY last_seen_at DESC,installation_id ASC`)
	if err != nil {
		return nil
	}
	installations := make([]ServiceMailInstallation, 0, 8)
	for rows.Next() {
		var installation ServiceMailInstallation
		var blocked int
		if scanErr := rows.Scan(&installation.InstallationID, &installation.PublicKey, &installation.FirstSeenAt, &installation.LastSeenAt, &installation.LastIP, &installation.LastDomain, &blocked); scanErr != nil {
			continue
		}
		installation.Blocked = blocked != 0
		installations = append(installations, installation)
	}
	_ = rows.Close()
	for installationIndex := range installations {
		installations[installationIndex].SentCount = store.CountServiceMailEventsSince(ctx, "installation_id", installations[installationIndex].InstallationID, time.Time{})
		installations[installationIndex].ErrorCount = store.countServiceMailErrors(ctx, installations[installationIndex].InstallationID)
	}
	return installations
}

func (store Store) countServiceMailErrors(ctx context.Context, installationID string) int {
	var count int
	_ = store.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM service_mail_events WHERE installation_id=? AND status<>'sent'`, strings.TrimSpace(installationID)).Scan(&count)
	return count
}

func (store Store) ServiceMailEvents(ctx context.Context, limit int) []ServiceMailEvent {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := store.DB.QueryContext(ctx, `SELECT id,installation_id,source_domain,source_ip,recipient,recipient_domain,code_kind,status,error,created_at FROM service_mail_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	events := make([]ServiceMailEvent, 0, limit)
	for rows.Next() {
		var event ServiceMailEvent
		if scanErr := rows.Scan(&event.ID, &event.InstallationID, &event.SourceDomain, &event.SourceIP, &event.Recipient, &event.RecipientDomain, &event.CodeKind, &event.Status, &event.Error, &event.CreatedAt); scanErr != nil {
			continue
		}
		events = append(events, event)
	}
	return events
}

func (store Store) ServiceMailBlocks(ctx context.Context) []ServiceMailBlock {
	rows, err := store.DB.QueryContext(ctx, `SELECT id,scope,value,reason,created_at FROM service_mail_blocks ORDER BY created_at DESC,id DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	blocks := make([]ServiceMailBlock, 0, 8)
	for rows.Next() {
		var block ServiceMailBlock
		if scanErr := rows.Scan(&block.ID, &block.Scope, &block.Value, &block.Reason, &block.CreatedAt); scanErr != nil {
			continue
		}
		blocks = append(blocks, block)
	}
	return blocks
}

func applyPlanToSiteRequest(request *SiteRequest, plan Plan) {
	if request == nil || plan.ID == 0 {
		return
	}
	request.PlanName = plan.Name
	request.PlanQuotaLabel = plan.QuotaLabel
	request.PlanSiteLimit = plan.SiteLimit
	request.PlanAnalyticsLimit = plan.AnalyticsReportLimit
	request.PlanPrice = plan.Price
	request.PlanCurrency = plan.Currency
	request.PlanBillingPeriod = plan.BillingPeriod
	request.PlanDescriptionText = fmt.Sprintf("Storage: %s. Sites: %d. Analytics reports: %d. Price: %s %s/%s. Includes Sitebrush editor, file storage, publishing, backups, domain settings, and automatic SSL when DNS is configured.", plan.QuotaLabel, plan.SiteLimit, plan.AnalyticsReportLimit, plan.Price, plan.Currency, plan.BillingPeriod)
}

func BuildSites(usages []SiteUsage, plans []Plan, assignments map[string]ServiceAssignment, currentDomain string) []Site {
	return BuildSitesWithDemoDomain(usages, plans, assignments, currentDomain, "")
}

func BuildSitesWithDemoDomain(usages []SiteUsage, plans []Plan, assignments map[string]ServiceAssignment, currentDomain string, demoDomain string) []Site {
	return BuildSitesWithDemoAndMainDomain(usages, plans, assignments, currentDomain, demoDomain, "")
}

func BuildSitesWithDemoAndMainDomain(usages []SiteUsage, plans []Plan, assignments map[string]ServiceAssignment, currentDomain string, demoDomain string, mainDomain string) []Site {
	planByID := make(map[int]Plan)
	for _, plan := range plans {
		planByID[plan.ID] = plan
	}
	mainDomain = strings.TrimSpace(mainDomain)
	sites := make([]Site, 0, len(usages))
	for _, usage := range usages {
		freeBytes := usage.LimitBytes - usage.UsedBytes
		if freeBytes < 0 {
			freeBytes = 0
		}
		usedPercent := 0
		if usage.LimitBytes > 0 {
			usedPercent = int(math.Round(float64(usage.UsedBytes) / float64(usage.LimitBytes) * 100))
		}
		if usedPercent > 100 {
			usedPercent = 100
		}
		assignment := assignments[usage.Domain]
		if assignment.ServiceStatus == "" {
			assignment.ServiceStatus = "free"
		}
		quotaInput := FormatQuotaInput(usage.LimitBytes)
		if assignment.PlanID > 0 {
			if plan, found := planByID[assignment.PlanID]; found && plan.QuotaBytes > 0 {
				quotaInput = FormatQuotaInput(plan.QuotaBytes)
			}
		}
		isDemo := strings.TrimSpace(usage.Domain) != "" && strings.EqualFold(strings.TrimSpace(usage.Domain), strings.TrimSpace(demoDomain))
		isMainDomain := mainDomain != "" && strings.EqualFold(strings.TrimSpace(usage.Domain), mainDomain)
		sites = append(sites, Site{
			Domain:        usage.Domain,
			IsDemo:        isDemo,
			IsMainDomain:  isMainDomain,
			Aliases:       strings.Join(usage.Aliases, ", "),
			UsedBytes:     usage.UsedBytes,
			UsedLabel:     FormatFileSize(usage.UsedBytes),
			LimitLabel:    FormatFileSize(usage.LimitBytes),
			FreeLabel:     FormatFileSize(freeBytes),
			UsedPercent:   usedPercent,
			QuotaInput:    quotaInput,
			PlanID:        assignment.PlanID,
			ServiceStatus: assignment.ServiceStatus,
			AdminEmails:   strings.Join(usage.AdminEmails, ", "),
			CanDelete:     !isDemo && strings.TrimSpace(usage.Domain) != strings.TrimSpace(currentDomain),
			DatabasePath:  usage.DatabasePath,
		})
	}
	sort.Slice(sites, func(left, right int) bool {
		if sites[left].UsedBytes != sites[right].UsedBytes {
			return sites[left].UsedBytes > sites[right].UsedBytes
		}
		return sites[left].Domain < sites[right].Domain
	})
	return sites
}

func ParseQuotaLimitBytes(rawQuota string) (int64, bool, error) {
	quotaText := strings.ToLower(strings.TrimSpace(rawQuota))
	if quotaText == "" {
		return 0, false, nil
	}
	quotaText = strings.ReplaceAll(quotaText, " ", "")
	unitMultiplier := int64(0)
	numberText := ""
	switch {
	case strings.HasSuffix(quotaText, "mb"):
		unitMultiplier = 1024 * 1024
		numberText = strings.TrimSuffix(quotaText, "mb")
	case strings.HasSuffix(quotaText, "m"):
		unitMultiplier = 1024 * 1024
		numberText = strings.TrimSuffix(quotaText, "m")
	case strings.HasSuffix(quotaText, "gb"):
		unitMultiplier = 1024 * 1024 * 1024
		numberText = strings.TrimSuffix(quotaText, "gb")
	case strings.HasSuffix(quotaText, "g"):
		unitMultiplier = 1024 * 1024 * 1024
		numberText = strings.TrimSuffix(quotaText, "g")
	default:
		return 0, false, fmt.Errorf("quota must use mb or gb suffix, for example 50mb or 20gb")
	}
	numberText = strings.TrimSpace(numberText)
	if numberText == "" {
		return 0, false, fmt.Errorf("quota value is missing")
	}
	quotaNumber, err := strconv.ParseInt(numberText, 10, 64)
	if err != nil || quotaNumber <= 0 {
		return 0, false, fmt.Errorf("quota must be a positive integer with mb or gb suffix")
	}
	if quotaNumber > math.MaxInt64/unitMultiplier {
		return 0, false, fmt.Errorf("quota is too large")
	}
	return quotaNumber * unitMultiplier, true, nil
}

func ParseDeletionBackupRetentionDays(rawDays string) (int, error) {
	if strings.TrimSpace(rawDays) == "" {
		return DefaultDeletionBackupRetentionDays, nil
	}
	retentionDays, err := strconv.Atoi(strings.TrimSpace(rawDays))
	if err != nil || retentionDays < 1 || retentionDays > 3650 {
		return 0, fmt.Errorf("backup retention must be between 1 and 3650 days")
	}
	return retentionDays, nil
}

func RewriteDeletionBackupMetadataRetention(metadataText string, retentionDays int, expiresAt time.Time) string {
	var metadata DeletionBackupMetadata
	if json.Unmarshal([]byte(metadataText), &metadata) != nil {
		return metadataText
	}
	metadata.RetentionDays = retentionDays
	metadata.ExpiresAt = expiresAt.Format(time.RFC3339)
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return metadataText
	}
	return string(metadataJSON)
}

func FormatQuotaInput(sizeBytes int64) string {
	const gigabyte = int64(1024 * 1024 * 1024)
	const megabyte = int64(1024 * 1024)
	if sizeBytes >= gigabyte && sizeBytes%gigabyte == 0 {
		return fmt.Sprintf("%dgb", sizeBytes/gigabyte)
	}
	return fmt.Sprintf("%dmb", sizeBytes/megabyte)
}

func FormatFileSize(size int64) string {
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
