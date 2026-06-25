package hostingandsupport

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"sitebrush/pkg/demo"
)

const DefaultStorageLimitBytes int64 = 10 * 1024 * 1024 * 1024
const DefaultDeletionBackupRetentionDays = 365
const currentBillingSchemaVersion = 15

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
	PlanName          string
	PlanQuotaLabel    string
	ServiceStatus     string
	BillingUsageLabel string
	BillingPriceLabel string
	BillingStatusText string
	BillingAmount     string
	BillingCurrency   string
	BillingBillable   bool
	AdminEmails       string
	CanDelete         bool
	DatabasePath      string
	DeletionSizeBytes int64
	DeletionSizeLabel string
	HasSiteRequest    bool
	SiteRequest       SiteRequest
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

const billingIncludedMegabytes int64 = 500
const billingStepMegabytes int64 = 50
const billingStepCents int64 = 10

type ServiceAssignment struct {
	PlanID        int
	ServiceStatus string
}

type PaymentProvider struct {
	Provider     string
	Enabled      bool
	DisplayName  string
	PaymentURL   string
	Instructions string
	UpdatedAt    string
}

type Invoice struct {
	ID              int
	Number          string
	CustomerEmail   string
	Domain          string
	PlanName        string
	Amount          string
	Currency        string
	Status          string
	Provider        string
	PaymentURL      string
	DueAt           string
	PaidAt          string
	Notes           string
	Recurring       bool
	RecurringPeriod string
	CreatedAt       string
	UpdatedAt       string
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
	Version             int                    `json:"version"`
	InstallationID      string                 `json:"installation_id"`
	OwnerEmail          string                 `json:"owner_email"`
	ServerIP            string                 `json:"server_ip"`
	ServerStatus        string                 `json:"server_status"`
	ServerDomain        string                 `json:"server_domain"`
	SitebrushVersion    string                 `json:"sitebrush_version"`
	OSName              string                 `json:"os_name"`
	OSVersion           string                 `json:"os_version"`
	CPUModel            string                 `json:"cpu_model"`
	CPUCores            int                    `json:"cpu_cores"`
	CPUUsagePercent     float64                `json:"cpu_usage_percent_1h"`
	LoadAverage         float64                `json:"load_average_1h"`
	RAMTotalBytes       int64                  `json:"ram_total_bytes"`
	ServerUptimeSeconds int64                  `json:"server_uptime_seconds"`
	StoragePath         string                 `json:"storage_path"`
	DiskFreeBytes       int64                  `json:"disk_free_bytes"`
	DiskTotalBytes      int64                  `json:"disk_total_bytes"`
	Plans               []HostingSnapshotPlan  `json:"plans"`
	Roles               []HostingSnapshotRole  `json:"roles"`
	Sites               []HostingSnapshotSite  `json:"sites"`
	Events              []HostingSnapshotEvent `json:"events"`
	CreatedAt           string                 `json:"created_at"`
}

type HostingSnapshotSite struct {
	Domain         string   `json:"domain"`
	OwnerEmail     string   `json:"owner_email"`
	UsedBytes      int64    `json:"used_bytes"`
	LimitBytes     int64    `json:"limit_bytes"`
	PlanName       string   `json:"plan_name"`
	PlanStatus     string   `json:"plan_status"`
	PlanPaidStatus string   `json:"plan_paid_status"`
	AdminEmails    []string `json:"admin_emails"`
}

type HostingSnapshotPlan struct {
	Name                 string `json:"name"`
	QuotaBytes           int64  `json:"quota_bytes"`
	SiteLimit            int    `json:"site_limit"`
	AnalyticsReportLimit int    `json:"analytics_report_limit"`
	Price                string `json:"price"`
	Currency             string `json:"currency"`
	BillingPeriod        string `json:"billing_period"`
	PaidStatus           string `json:"paid_status"`
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
	InstallationID       string
	OwnerEmail           string
	ServerIP             string
	ServerStatus         string
	ServerDomain         string
	SitebrushVersion     string
	OSName               string
	OSVersion            string
	CPUModel             string
	CPUCores             int
	CPUUsagePercent      float64
	LoadAverage          float64
	RAMTotalBytes        int64
	ServerUptimeSeconds  int64
	ServerUptimeLabel    string
	ServerUptimeClass    string
	RAMTotalLabel        string
	StoragePath          string
	DiskFreeBytes        int64
	DiskTotalBytes       int64
	DiskUsedBytes        int64
	DiskUsedPercent      int
	DiskStatusClass      string
	CPUStatusClass       string
	LoadStatusClass      string
	NetworkUptimePercent float64
	NetworkUptimeLabel   string
	LastResponseMS       int
	NetworkStatusClass   string
	DiskFreeLabel        string
	DiskTotalLabel       string
	DiskUsedLabel        string
	LastSeenAt           string
	Sites                []ClientHostingSite
	ClientEmails         []string
	SiteCount            int
	TotalUsedBytes       int64
	TotalUsedLabel       string
	Plans                []ClientHostingPlan
	Roles                []ClientHostingRole
	Events               []ClientHostingEvent
}

type ClientHostingSite struct {
	Domain            string
	OwnerEmail        string
	UsedBytes         int64
	LimitBytes        int64
	PlanName          string
	PlanStatus        string
	PlanPaidStatus    string
	UsedLabel         string
	LimitLabel        string
	BillingUsageLabel string
	BillingPriceLabel string
	BillingStatusText string
	BillingAmount     string
	BillingCurrency   string
	BillingBillable   bool
	OverLimit         bool
	AdminEmails       []string
	HTTPSAvailable    bool
	CertExpiresAt     string
	CertDaysLeft      int
	TLSStatusClass    string
}

type ClientHostingPlan struct {
	Name          string
	QuotaLabel    string
	SiteLimit     int
	Price         string
	Currency      string
	BillingPeriod string
	PaidStatus    string
	IsDefault     bool
}

type ClientHostingRole struct {
	Email  string
	Role   string
	Scope  string
	Domain string
}

type ClientHostingEvent struct {
	Kind      string
	Status    string
	Email     string
	Domain    string
	Message   string
	CreatedAt string
}

type RegistrySyncEvent struct {
	ID             int
	InstallationID string
	Status         string
	StatusLabel    string
	Error          string
	CreatedAt      string
	HasSummary     bool
	Summary        RegistrySyncSummary
}

type RegistrySyncSummary struct {
	Version             int                       `json:"version"`
	OwnerEmail          string                    `json:"owner_email"`
	ServerIP            string                    `json:"server_ip"`
	ServerStatus        string                    `json:"server_status"`
	ServerDomain        string                    `json:"server_domain"`
	SitebrushVersion    string                    `json:"sitebrush_version"`
	OSName              string                    `json:"os_name"`
	OSVersion           string                    `json:"os_version"`
	CPUModel            string                    `json:"cpu_model"`
	CPUCores            int                       `json:"cpu_cores"`
	CPUUsagePercent     float64                   `json:"cpu_usage_percent_1h"`
	LoadAverage         float64                   `json:"load_average_1h"`
	RAMTotalBytes       int64                     `json:"ram_total_bytes"`
	ServerUptimeSeconds int64                     `json:"server_uptime_seconds"`
	RAMTotalLabel       string                    `json:"-"`
	StoragePath         string                    `json:"storage_path"`
	DiskFreeBytes       int64                     `json:"disk_free_bytes"`
	DiskTotalBytes      int64                     `json:"disk_total_bytes"`
	DiskFreeLabel       string                    `json:"-"`
	DiskTotalLabel      string                    `json:"-"`
	DiskUsedBytes       int64                     `json:"disk_used_bytes"`
	DiskUsedLabel       string                    `json:"-"`
	SiteCount           int                       `json:"site_count"`
	PlanCount           int                       `json:"plan_count"`
	RoleCount           int                       `json:"role_count"`
	EventCount          int                       `json:"event_count"`
	Sites               []RegistrySyncSummarySite `json:"sites"`
	Plans               []RegistrySyncSummaryPlan `json:"plans"`
	Roles               []HostingSnapshotRole     `json:"roles"`
	Events              []HostingSnapshotEvent    `json:"events"`
	CreatedAt           string                    `json:"created_at"`
}

type RegistrySyncSummarySite struct {
	Domain         string `json:"domain"`
	OwnerEmail     string `json:"owner_email"`
	UsedBytes      int64  `json:"used_bytes"`
	LimitBytes     int64  `json:"limit_bytes"`
	UsedLabel      string `json:"-"`
	LimitLabel     string `json:"-"`
	PlanName       string `json:"plan_name"`
	PlanStatus     string `json:"plan_status"`
	PlanPaidStatus string `json:"plan_paid_status"`
	OverLimit      bool   `json:"over_limit"`
	AdminEmails    string `json:"admin_emails"`
}

type RegistrySyncSummaryPlan struct {
	Name                 string `json:"name"`
	QuotaBytes           int64  `json:"quota_bytes"`
	QuotaLabel           string `json:"-"`
	SiteLimit            int    `json:"site_limit"`
	AnalyticsReportLimit int    `json:"analytics_report_limit"`
	Price                string `json:"price"`
	Currency             string `json:"currency"`
	BillingPeriod        string `json:"billing_period"`
	PaidStatus           string `json:"paid_status"`
	IsDefault            bool   `json:"is_default"`
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
		`CREATE TABLE IF NOT EXISTS payment_providers(provider TEXT PRIMARY KEY,enabled INTEGER DEFAULT 0,display_name TEXT,payment_url TEXT,instructions TEXT,updated_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS billing_invoices(id INTEGER PRIMARY KEY AUTOINCREMENT,invoice_number TEXT UNIQUE,customer_email TEXT,domain TEXT,plan_name TEXT,amount TEXT,currency TEXT,status TEXT,provider TEXT,payment_url TEXT,due_at TEXT,paid_at TEXT,notes TEXT,recurring_enabled INTEGER DEFAULT 0,recurring_period TEXT,created_at TEXT,updated_at TEXT);`,
		`CREATE INDEX IF NOT EXISTS idx_billing_invoices_customer_created ON billing_invoices(customer_email,created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_billing_invoices_domain_created ON billing_invoices(domain,created_at);`,
		`CREATE TABLE IF NOT EXISTS site_registration_requests(id INTEGER PRIMARY KEY AUTOINCREMENT,domain TEXT,name TEXT,email TEXT,phone TEXT,plan_id INTEGER DEFAULT 0,status TEXT,owner_message TEXT,created_at TEXT,updated_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS site_deletion_backups(id INTEGER PRIMARY KEY AUTOINCREMENT,domain TEXT,archive_path TEXT,file_name TEXT,size_bytes INTEGER,token TEXT,token_created_at TEXT,created_at TEXT,expires_at TEXT,retention_days INTEGER,owner_contacts TEXT,metadata_json TEXT,language_code TEXT,downloaded_at TEXT,download_count INTEGER DEFAULT 0);`,
		`CREATE TABLE IF NOT EXISTS service_mail_installations(installation_id TEXT PRIMARY KEY,public_key TEXT,first_seen_at TEXT,last_seen_at TEXT,last_ip TEXT,last_domain TEXT,blocked INTEGER DEFAULT 0);`,
		`CREATE TABLE IF NOT EXISTS service_mail_events(id INTEGER PRIMARY KEY AUTOINCREMENT,installation_id TEXT,source_domain TEXT,source_ip TEXT,recipient TEXT,recipient_domain TEXT,code_kind TEXT,status TEXT,error TEXT,created_at TEXT);`,
		`CREATE INDEX IF NOT EXISTS idx_service_mail_events_installation_created ON service_mail_events(installation_id,created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_service_mail_events_recipient_created ON service_mail_events(recipient,created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_service_mail_events_domain_created ON service_mail_events(recipient_domain,created_at);`,
		`CREATE TABLE IF NOT EXISTS support_events(id INTEGER PRIMARY KEY AUTOINCREMENT,kind TEXT,status TEXT,email TEXT,domain TEXT,message TEXT,created_at TEXT);`,
		`CREATE INDEX IF NOT EXISTS idx_support_events_created ON support_events(created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_support_events_email_created ON support_events(email,created_at);`,
		`CREATE TABLE IF NOT EXISTS service_mail_blocks(id INTEGER PRIMARY KEY AUTOINCREMENT,scope TEXT,value TEXT,reason TEXT,created_at TEXT,UNIQUE(scope,value));`,
		`CREATE TABLE IF NOT EXISTS service_mail_recipients(installation_id TEXT,recipient_hash TEXT,recipient_mask TEXT,status TEXT,purpose_scope TEXT,created_at TEXT,verified_at TEXT,PRIMARY KEY(installation_id,recipient_hash));`,
		`CREATE INDEX IF NOT EXISTS idx_service_mail_recipients_installation_status ON service_mail_recipients(installation_id,status);`,
		`CREATE TABLE IF NOT EXISTS client_hostings(installation_id TEXT PRIMARY KEY,owner_email TEXT,server_ip TEXT,server_status TEXT,server_domain TEXT,sitebrush_version TEXT,os_name TEXT,os_version TEXT,cpu_model TEXT,cpu_cores INTEGER DEFAULT 0,cpu_usage_percent_1h REAL DEFAULT 0,load_average_1h REAL DEFAULT 0,ram_total_bytes INTEGER DEFAULT 0,server_uptime_seconds INTEGER DEFAULT 0,storage_path TEXT,disk_free_bytes INTEGER DEFAULT 0,disk_total_bytes INTEGER DEFAULT 0,first_seen_at TEXT,last_seen_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS client_hosting_sites(installation_id TEXT,domain TEXT,owner_email TEXT,used_bytes INTEGER DEFAULT 0,limit_bytes INTEGER DEFAULT 0,plan_name TEXT,plan_status TEXT,plan_paid_status TEXT,admin_emails TEXT,updated_at TEXT,PRIMARY KEY(installation_id,domain));`,
		`CREATE INDEX IF NOT EXISTS idx_client_hosting_sites_installation ON client_hosting_sites(installation_id);`,
		`CREATE TABLE IF NOT EXISTS server_resource_checks(id INTEGER PRIMARY KEY AUTOINCREMENT,installation_id TEXT,cpu_usage_percent REAL DEFAULT 0,load_average REAL DEFAULT 0,ram_total_bytes INTEGER DEFAULT 0,disk_free_bytes INTEGER DEFAULT 0,disk_total_bytes INTEGER DEFAULT 0,checked_at TEXT);`,
		`CREATE INDEX IF NOT EXISTS idx_server_resource_checks_installation_checked ON server_resource_checks(installation_id,checked_at);`,
		`CREATE TABLE IF NOT EXISTS server_network_checks(id INTEGER PRIMARY KEY AUTOINCREMENT,installation_id TEXT,server_domain TEXT,server_ip TEXT,success INTEGER DEFAULT 0,response_ms INTEGER DEFAULT 0,error TEXT,checked_at TEXT);`,
		`CREATE INDEX IF NOT EXISTS idx_server_network_checks_installation_checked ON server_network_checks(installation_id,checked_at);`,
		`CREATE TABLE IF NOT EXISTS site_tls_checks(domain TEXT PRIMARY KEY,installation_id TEXT,https_available INTEGER DEFAULT 0,cert_expires_at TEXT,cert_days_left INTEGER DEFAULT 0,status TEXT,error TEXT,checked_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS registry_accounts(email TEXT PRIMARY KEY,first_seen_at TEXT,last_seen_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS registry_installation_roles(installation_id TEXT,email TEXT,role TEXT,scope TEXT,domain TEXT,updated_at TEXT,PRIMARY KEY(installation_id,email,role,scope,domain));`,
		`CREATE INDEX IF NOT EXISTS idx_registry_installation_roles_email ON registry_installation_roles(email);`,
		`CREATE TABLE IF NOT EXISTS registry_installation_plans(installation_id TEXT,name TEXT,quota_bytes INTEGER DEFAULT 0,site_limit INTEGER DEFAULT 0,analytics_report_limit INTEGER DEFAULT 0,price TEXT,currency TEXT,billing_period TEXT,paid_status TEXT,is_default INTEGER DEFAULT 0,updated_at TEXT,PRIMARY KEY(installation_id,name));`,
		`CREATE TABLE IF NOT EXISTS registry_events(id INTEGER PRIMARY KEY AUTOINCREMENT,installation_id TEXT,event_key TEXT,kind TEXT,status TEXT,email TEXT,domain TEXT,message TEXT,created_at TEXT,UNIQUE(installation_id,event_key));`,
		`CREATE TABLE IF NOT EXISTS registry_sync_events(id INTEGER PRIMARY KEY AUTOINCREMENT,installation_id TEXT,status TEXT,error TEXT,created_at TEXT,summary_json TEXT);`,
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
	_, _ = database.ExecContext(ctx, `INSERT OR IGNORE INTO payment_providers(provider,enabled,display_name,payment_url,instructions,updated_at) VALUES('sitebrush_com',1,'SiteBrush.com demo payments','/?hosting_and_support_demo_payment&invoice={invoice}','Предустановленная демо-оплата через SiteBrush.com.',?)`, now)
	_, _ = database.ExecContext(ctx, `UPDATE payment_providers SET enabled=1,display_name='SiteBrush.com demo payments',payment_url='/?hosting_and_support_demo_payment&invoice={invoice}',instructions='Предустановленная демо-оплата через SiteBrush.com.',updated_at=? WHERE provider='sitebrush_com'`, now)
	_, _ = database.ExecContext(ctx, `INSERT OR IGNORE INTO payment_providers(provider,enabled,display_name,payment_url,instructions,updated_at) VALUES('stripe',0,'Stripe','','Stripe Checkout or Payment Link URL template',?)`, now)
	_, _ = database.ExecContext(ctx, `INSERT OR IGNORE INTO payment_providers(provider,enabled,display_name,payment_url,instructions,updated_at) VALUES('paypal',0,'PayPal','','PayPal payment URL template',?)`, now)
	_, _ = database.ExecContext(ctx, `INSERT OR IGNORE INTO payment_providers(provider,enabled,display_name,payment_url,instructions,updated_at) VALUES('sbp',0,'СБП','','СБП: банк, телефон, получатель или QR/link template',?)`, now)
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
	tableNames := []string{"server_managers", "server_settings", "site_service_plans", "site_service_assignments", "payment_providers", "billing_invoices", "site_registration_requests", "site_deletion_backups", "service_mail_installations", "service_mail_events", "support_events", "service_mail_blocks", "service_mail_recipients", "client_hostings", "client_hosting_sites", "server_resource_checks", "server_network_checks", "site_tls_checks", "registry_accounts", "registry_installation_roles", "registry_installation_plans", "registry_events", "registry_sync_events", "sitebrush_com_keys"}
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
		{tableName: "payment_providers", columnName: "instructions", definition: "TEXT"},
		{tableName: "billing_invoices", columnName: "provider", definition: "TEXT"},
		{tableName: "billing_invoices", columnName: "payment_url", definition: "TEXT"},
		{tableName: "billing_invoices", columnName: "paid_at", definition: "TEXT"},
		{tableName: "billing_invoices", columnName: "recurring_enabled", definition: "INTEGER DEFAULT 0"},
		{tableName: "billing_invoices", columnName: "recurring_period", definition: "TEXT"},
		{tableName: "client_hostings", columnName: "server_status", definition: "TEXT"},
		{tableName: "client_hostings", columnName: "server_domain", definition: "TEXT"},
		{tableName: "client_hostings", columnName: "sitebrush_version", definition: "TEXT"},
		{tableName: "client_hostings", columnName: "os_name", definition: "TEXT"},
		{tableName: "client_hostings", columnName: "os_version", definition: "TEXT"},
		{tableName: "client_hostings", columnName: "cpu_model", definition: "TEXT"},
		{tableName: "client_hostings", columnName: "cpu_cores", definition: "INTEGER DEFAULT 0"},
		{tableName: "client_hostings", columnName: "cpu_usage_percent_1h", definition: "REAL DEFAULT 0"},
		{tableName: "client_hostings", columnName: "load_average_1h", definition: "REAL DEFAULT 0"},
		{tableName: "client_hostings", columnName: "ram_total_bytes", definition: "INTEGER DEFAULT 0"},
		{tableName: "client_hostings", columnName: "server_uptime_seconds", definition: "INTEGER DEFAULT 0"},
		{tableName: "client_hosting_sites", columnName: "owner_email", definition: "TEXT"},
		{tableName: "client_hosting_sites", columnName: "plan_name", definition: "TEXT"},
		{tableName: "client_hosting_sites", columnName: "plan_status", definition: "TEXT"},
		{tableName: "client_hosting_sites", columnName: "plan_paid_status", definition: "TEXT"},
		{tableName: "registry_installation_plans", columnName: "paid_status", definition: "TEXT"},
		{tableName: "registry_sync_events", columnName: "summary_json", definition: "TEXT"},
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

func (store Store) PaymentProviders(ctx context.Context) []PaymentProvider {
	rows, err := store.DB.QueryContext(ctx, `SELECT provider,COALESCE(enabled,0),COALESCE(display_name,''),COALESCE(payment_url,''),COALESCE(instructions,''),COALESCE(updated_at,'') FROM payment_providers ORDER BY CASE provider WHEN 'sitebrush_com' THEN 0 WHEN 'stripe' THEN 1 WHEN 'paypal' THEN 2 WHEN 'sbp' THEN 3 ELSE 4 END,provider ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	providers := make([]PaymentProvider, 0, 3)
	for rows.Next() {
		var provider PaymentProvider
		var enabled int
		if scanErr := rows.Scan(&provider.Provider, &enabled, &provider.DisplayName, &provider.PaymentURL, &provider.Instructions, &provider.UpdatedAt); scanErr != nil {
			continue
		}
		provider.Enabled = enabled != 0
		providers = append(providers, provider)
	}
	return providers
}

func (store Store) SavePaymentProvider(ctx context.Context, provider PaymentProvider) error {
	provider.Provider = normalizePaymentProvider(provider.Provider)
	if provider.Provider == "" {
		return fmt.Errorf("payment provider is required")
	}
	displayName := strings.TrimSpace(provider.DisplayName)
	if displayName == "" {
		displayName = defaultPaymentProviderName(provider.Provider)
	}
	enabledFlag := 0
	if provider.Enabled {
		enabledFlag = 1
	}
	_, err := store.DB.ExecContext(ctx, `INSERT INTO payment_providers(provider,enabled,display_name,payment_url,instructions,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(provider) DO UPDATE SET enabled=excluded.enabled,display_name=excluded.display_name,payment_url=excluded.payment_url,instructions=excluded.instructions,updated_at=excluded.updated_at`,
		provider.Provider, enabledFlag, displayName, strings.TrimSpace(provider.PaymentURL), strings.TrimSpace(provider.Instructions), time.Now().UTC().Format(time.RFC3339))
	return err
}

func normalizePaymentProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "sitebrush_com", "stripe", "paypal", "sbp":
		return strings.ToLower(strings.TrimSpace(provider))
	default:
		return ""
	}
}

func defaultPaymentProviderName(provider string) string {
	switch normalizePaymentProvider(provider) {
	case "stripe":
		return "Stripe"
	case "sitebrush_com":
		return "SiteBrush.com demo payments"
	case "paypal":
		return "PayPal"
	case "sbp":
		return "СБП"
	default:
		return strings.TrimSpace(provider)
	}
}

func (store Store) Invoices(ctx context.Context, limit int) []Invoice {
	if limit <= 0 || limit > 200 {
		limit = 80
	}
	rows, err := store.DB.QueryContext(ctx, `SELECT id,invoice_number,COALESCE(customer_email,''),COALESCE(domain,''),COALESCE(plan_name,''),COALESCE(amount,''),COALESCE(currency,''),COALESCE(status,''),COALESCE(provider,''),COALESCE(payment_url,''),COALESCE(due_at,''),COALESCE(paid_at,''),COALESCE(notes,''),COALESCE(recurring_enabled,0),COALESCE(recurring_period,''),COALESCE(created_at,''),COALESCE(updated_at,'') FROM billing_invoices ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	invoices := make([]Invoice, 0, limit)
	for rows.Next() {
		var invoice Invoice
		var recurringEnabled int
		if scanErr := rows.Scan(&invoice.ID, &invoice.Number, &invoice.CustomerEmail, &invoice.Domain, &invoice.PlanName, &invoice.Amount, &invoice.Currency, &invoice.Status, &invoice.Provider, &invoice.PaymentURL, &invoice.DueAt, &invoice.PaidAt, &invoice.Notes, &recurringEnabled, &invoice.RecurringPeriod, &invoice.CreatedAt, &invoice.UpdatedAt); scanErr != nil {
			continue
		}
		invoice.Recurring = recurringEnabled != 0
		invoices = append(invoices, invoice)
	}
	return invoices
}

func (store Store) CreateInvoice(ctx context.Context, invoice Invoice) (Invoice, error) {
	invoice.CustomerEmail = strings.ToLower(strings.TrimSpace(invoice.CustomerEmail))
	invoice.Domain = strings.ToLower(strings.TrimSpace(invoice.Domain))
	invoice.PlanName = strings.TrimSpace(invoice.PlanName)
	invoice.Amount = strings.TrimSpace(invoice.Amount)
	invoice.Currency = strings.ToUpper(strings.TrimSpace(invoice.Currency))
	invoice.Provider = normalizePaymentProvider(invoice.Provider)
	if invoice.Recurring {
		invoice.RecurringPeriod = normalizeInvoiceRecurringPeriod(invoice.RecurringPeriod)
	} else {
		invoice.RecurringPeriod = ""
	}
	if invoice.Provider == "" {
		invoice.Provider = "sitebrush_com"
	}
	if invoice.CustomerEmail == "" {
		return Invoice{}, fmt.Errorf("customer email is required")
	}
	if invoice.Domain == "" {
		return Invoice{}, fmt.Errorf("site domain is required")
	}
	if invoice.Amount == "" {
		return Invoice{}, fmt.Errorf("invoice amount is required")
	}
	if invoice.Currency == "" {
		invoice.Currency = "USD"
	}
	if !store.paymentProviderEnabled(ctx, invoice.Provider) {
		return Invoice{}, fmt.Errorf("payment provider is disabled")
	}
	now := time.Now().UTC()
	invoice.Number = nextInvoiceNumber(now)
	invoice.Status = "issued"
	invoice.CreatedAt = now.Format(time.RFC3339)
	invoice.UpdatedAt = invoice.CreatedAt
	invoice.PaymentURL = store.renderInvoicePaymentURL(ctx, invoice)
	recurringEnabled := 0
	if invoice.Recurring {
		recurringEnabled = 1
	}
	result, err := store.DB.ExecContext(ctx, `INSERT INTO billing_invoices(invoice_number,customer_email,domain,plan_name,amount,currency,status,provider,payment_url,due_at,paid_at,notes,recurring_enabled,recurring_period,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		invoice.Number, invoice.CustomerEmail, invoice.Domain, invoice.PlanName, invoice.Amount, invoice.Currency, invoice.Status, invoice.Provider, invoice.PaymentURL, strings.TrimSpace(invoice.DueAt), "", strings.TrimSpace(invoice.Notes), recurringEnabled, invoice.RecurringPeriod, invoice.CreatedAt, invoice.UpdatedAt)
	if err != nil {
		return Invoice{}, err
	}
	if invoiceID, idErr := result.LastInsertId(); idErr == nil {
		invoice.ID = int(invoiceID)
	}
	return invoice, nil
}

func (store Store) UpdateInvoiceStatus(ctx context.Context, invoiceID int, status string) (Invoice, error) {
	if invoiceID <= 0 {
		return Invoice{}, fmt.Errorf("invoice is required")
	}
	status = strings.TrimSpace(status)
	switch status {
	case "issued", "paid", "cancelled", "payment_error":
	default:
		return Invoice{}, fmt.Errorf("unsupported invoice status %q", status)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	paidAt := ""
	if status == "paid" {
		paidAt = now
	}
	_, err := store.DB.ExecContext(ctx, `UPDATE billing_invoices SET status=?,paid_at=?,updated_at=? WHERE id=?`, status, paidAt, now, invoiceID)
	if err != nil {
		return Invoice{}, err
	}
	return store.InvoiceByID(ctx, invoiceID)
}

func (store Store) InvoiceByID(ctx context.Context, invoiceID int) (Invoice, error) {
	var invoice Invoice
	var recurringEnabled int
	err := store.DB.QueryRowContext(ctx, `SELECT id,invoice_number,COALESCE(customer_email,''),COALESCE(domain,''),COALESCE(plan_name,''),COALESCE(amount,''),COALESCE(currency,''),COALESCE(status,''),COALESCE(provider,''),COALESCE(payment_url,''),COALESCE(due_at,''),COALESCE(paid_at,''),COALESCE(notes,''),COALESCE(recurring_enabled,0),COALESCE(recurring_period,''),COALESCE(created_at,''),COALESCE(updated_at,'') FROM billing_invoices WHERE id=?`, invoiceID).Scan(
		&invoice.ID, &invoice.Number, &invoice.CustomerEmail, &invoice.Domain, &invoice.PlanName, &invoice.Amount, &invoice.Currency, &invoice.Status, &invoice.Provider, &invoice.PaymentURL, &invoice.DueAt, &invoice.PaidAt, &invoice.Notes, &recurringEnabled, &invoice.RecurringPeriod, &invoice.CreatedAt, &invoice.UpdatedAt)
	invoice.Recurring = recurringEnabled != 0
	return invoice, err
}

func normalizeInvoiceRecurringPeriod(period string) string {
	switch strings.ToLower(strings.TrimSpace(period)) {
	case "weekly", "monthly", "quarterly", "yearly":
		return strings.ToLower(strings.TrimSpace(period))
	default:
		return "monthly"
	}
}

func (store Store) renderInvoicePaymentURL(ctx context.Context, invoice Invoice) string {
	var templateText string
	_ = store.DB.QueryRowContext(ctx, `SELECT payment_url FROM payment_providers WHERE provider=? AND enabled=1`, invoice.Provider).Scan(&templateText)
	templateText = strings.TrimSpace(templateText)
	if templateText == "" {
		return ""
	}
	replacements := map[string]string{
		"{invoice}":  invoice.Number,
		"{amount}":   invoice.Amount,
		"{currency}": invoice.Currency,
		"{email}":    invoice.CustomerEmail,
		"{domain}":   invoice.Domain,
	}
	for placeholder, replacement := range replacements {
		templateText = strings.ReplaceAll(templateText, placeholder, urlQueryEscape(replacement))
	}
	return templateText
}

func (store Store) paymentProviderEnabled(ctx context.Context, provider string) bool {
	var enabled int
	err := store.DB.QueryRowContext(ctx, `SELECT COALESCE(enabled,0) FROM payment_providers WHERE provider=?`, normalizePaymentProvider(provider)).Scan(&enabled)
	return err == nil && enabled != 0
}

func nextInvoiceNumber(now time.Time) string {
	return "SB-" + now.UTC().Format("20060102-150405") + "-" + strconv.FormatInt(now.UnixNano()%1000000, 10)
}

func urlQueryEscape(value string) string {
	return url.QueryEscape(value)
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
	syncSummary := registrySyncSummaryFromSnapshot(snapshot)
	transaction, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	_, err = transaction.ExecContext(ctx, `INSERT INTO client_hostings(installation_id,owner_email,server_ip,server_status,server_domain,sitebrush_version,os_name,os_version,cpu_model,cpu_cores,cpu_usage_percent_1h,load_average_1h,ram_total_bytes,server_uptime_seconds,storage_path,disk_free_bytes,disk_total_bytes,first_seen_at,last_seen_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(installation_id) DO UPDATE SET owner_email=excluded.owner_email,server_ip=excluded.server_ip,server_status=excluded.server_status,server_domain=excluded.server_domain,sitebrush_version=excluded.sitebrush_version,os_name=excluded.os_name,os_version=excluded.os_version,cpu_model=excluded.cpu_model,cpu_cores=excluded.cpu_cores,cpu_usage_percent_1h=excluded.cpu_usage_percent_1h,load_average_1h=excluded.load_average_1h,ram_total_bytes=excluded.ram_total_bytes,server_uptime_seconds=excluded.server_uptime_seconds,storage_path=excluded.storage_path,disk_free_bytes=excluded.disk_free_bytes,disk_total_bytes=excluded.disk_total_bytes,last_seen_at=excluded.last_seen_at`,
		installationID, strings.ToLower(strings.TrimSpace(snapshot.OwnerEmail)), strings.TrimSpace(snapshot.ServerIP), strings.TrimSpace(snapshot.ServerStatus), strings.ToLower(strings.TrimSpace(snapshot.ServerDomain)), strings.TrimSpace(snapshot.SitebrushVersion), strings.TrimSpace(snapshot.OSName), strings.TrimSpace(snapshot.OSVersion), strings.TrimSpace(snapshot.CPUModel), snapshot.CPUCores, snapshot.CPUUsagePercent, snapshot.LoadAverage, snapshot.RAMTotalBytes, snapshot.ServerUptimeSeconds, strings.TrimSpace(snapshot.StoragePath), snapshot.DiskFreeBytes, snapshot.DiskTotalBytes, now, now)
	if err == nil {
		_, err = transaction.ExecContext(ctx, `INSERT INTO server_resource_checks(installation_id,cpu_usage_percent,load_average,ram_total_bytes,disk_free_bytes,disk_total_bytes,checked_at) VALUES(?,?,?,?,?,?,?)`,
			installationID, snapshot.CPUUsagePercent, snapshot.LoadAverage, snapshot.RAMTotalBytes, snapshot.DiskFreeBytes, snapshot.DiskTotalBytes, now)
	}
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
		_, err = transaction.ExecContext(ctx, `INSERT INTO client_hosting_sites(installation_id,domain,owner_email,used_bytes,limit_bytes,plan_name,plan_status,plan_paid_status,admin_emails,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			installationID, domain, strings.ToLower(strings.TrimSpace(site.OwnerEmail)), site.UsedBytes, site.LimitBytes, strings.TrimSpace(site.PlanName), strings.TrimSpace(site.PlanStatus), strings.TrimSpace(site.PlanPaidStatus), strings.Join(adminEmails, ","), now)
		if err == nil {
			err = upsertRegistryAccountTx(ctx, transaction, site.OwnerEmail, now)
		}
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
		_, err = transaction.ExecContext(ctx, `INSERT INTO registry_installation_plans(installation_id,name,quota_bytes,site_limit,analytics_report_limit,price,currency,billing_period,paid_status,is_default,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(installation_id,name) DO UPDATE SET quota_bytes=excluded.quota_bytes,site_limit=excluded.site_limit,analytics_report_limit=excluded.analytics_report_limit,price=excluded.price,currency=excluded.currency,billing_period=excluded.billing_period,paid_status=excluded.paid_status,is_default=excluded.is_default,updated_at=excluded.updated_at`,
			installationID, planName, plan.QuotaBytes, plan.SiteLimit, plan.AnalyticsReportLimit, strings.TrimSpace(plan.Price), strings.TrimSpace(plan.Currency), strings.TrimSpace(plan.BillingPeriod), strings.TrimSpace(plan.PaidStatus), defaultFlag, now)
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
		_ = store.LogRegistrySyncEventWithSummary(ctx, installationID, "error", err.Error(), syncSummary)
		return err
	}
	if err = transaction.Commit(); err != nil {
		_ = store.LogRegistrySyncEventWithSummary(ctx, installationID, "error", err.Error(), syncSummary)
		return err
	}
	return store.LogRegistrySyncEventWithSummary(ctx, installationID, "stored", "", syncSummary)
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

func registrySyncSummaryFromSnapshot(snapshot HostingSnapshot) RegistrySyncSummary {
	summary := RegistrySyncSummary{
		Version:             snapshot.Version,
		OwnerEmail:          strings.ToLower(strings.TrimSpace(snapshot.OwnerEmail)),
		ServerIP:            strings.TrimSpace(snapshot.ServerIP),
		ServerStatus:        strings.TrimSpace(snapshot.ServerStatus),
		ServerDomain:        strings.ToLower(strings.TrimSpace(snapshot.ServerDomain)),
		SitebrushVersion:    strings.TrimSpace(snapshot.SitebrushVersion),
		OSName:              strings.TrimSpace(snapshot.OSName),
		OSVersion:           strings.TrimSpace(snapshot.OSVersion),
		CPUModel:            strings.TrimSpace(snapshot.CPUModel),
		CPUCores:            snapshot.CPUCores,
		CPUUsagePercent:     snapshot.CPUUsagePercent,
		LoadAverage:         snapshot.LoadAverage,
		RAMTotalBytes:       snapshot.RAMTotalBytes,
		ServerUptimeSeconds: snapshot.ServerUptimeSeconds,
		StoragePath:         strings.TrimSpace(snapshot.StoragePath),
		DiskFreeBytes:       snapshot.DiskFreeBytes,
		DiskTotalBytes:      snapshot.DiskTotalBytes,
		SiteCount:           len(snapshot.Sites),
		PlanCount:           len(snapshot.Plans),
		RoleCount:           len(snapshot.Roles),
		EventCount:          len(snapshot.Events),
		CreatedAt:           strings.TrimSpace(snapshot.CreatedAt),
	}
	if summary.DiskTotalBytes > summary.DiskFreeBytes {
		summary.DiskUsedBytes = summary.DiskTotalBytes - summary.DiskFreeBytes
	}
	for _, site := range snapshot.Sites {
		adminEmails := normalizedHostingEmails(site.AdminEmails)
		summary.Sites = append(summary.Sites, RegistrySyncSummarySite{
			Domain:         strings.ToLower(strings.TrimSpace(site.Domain)),
			OwnerEmail:     strings.ToLower(strings.TrimSpace(site.OwnerEmail)),
			UsedBytes:      site.UsedBytes,
			LimitBytes:     site.LimitBytes,
			PlanName:       strings.TrimSpace(site.PlanName),
			PlanStatus:     strings.TrimSpace(site.PlanStatus),
			PlanPaidStatus: strings.TrimSpace(site.PlanPaidStatus),
			OverLimit:      site.LimitBytes > 0 && site.UsedBytes > site.LimitBytes,
			AdminEmails:    strings.Join(adminEmails, ", "),
		})
	}
	for _, plan := range snapshot.Plans {
		summary.Plans = append(summary.Plans, RegistrySyncSummaryPlan{
			Name:                 strings.TrimSpace(plan.Name),
			QuotaBytes:           plan.QuotaBytes,
			SiteLimit:            plan.SiteLimit,
			AnalyticsReportLimit: plan.AnalyticsReportLimit,
			Price:                strings.TrimSpace(plan.Price),
			Currency:             strings.TrimSpace(plan.Currency),
			BillingPeriod:        strings.TrimSpace(plan.BillingPeriod),
			PaidStatus:           strings.TrimSpace(plan.PaidStatus),
			IsDefault:            plan.IsDefault,
		})
	}
	for _, role := range snapshot.Roles {
		email := strings.ToLower(strings.TrimSpace(role.Email))
		roleName := strings.TrimSpace(role.Role)
		if email == "" || roleName == "" {
			continue
		}
		summary.Roles = append(summary.Roles, HostingSnapshotRole{
			Email:  email,
			Role:   roleName,
			Scope:  strings.TrimSpace(role.Scope),
			Domain: strings.ToLower(strings.TrimSpace(role.Domain)),
		})
	}
	for _, event := range snapshot.Events {
		kind := strings.TrimSpace(event.Kind)
		if kind == "" {
			continue
		}
		summary.Events = append(summary.Events, HostingSnapshotEvent{
			Kind:      kind,
			Status:    strings.TrimSpace(event.Status),
			Email:     strings.ToLower(strings.TrimSpace(event.Email)),
			Domain:    strings.ToLower(strings.TrimSpace(event.Domain)),
			Message:   strings.TrimSpace(event.Message),
			CreatedAt: strings.TrimSpace(event.CreatedAt),
		})
	}
	applyRegistrySyncSummaryLabels(&summary)
	return summary
}

func applyRegistrySyncSummaryLabels(summary *RegistrySyncSummary) {
	if summary == nil {
		return
	}
	summary.RAMTotalLabel = FormatFileSize(summary.RAMTotalBytes)
	summary.DiskFreeLabel = FormatFileSize(summary.DiskFreeBytes)
	summary.DiskTotalLabel = FormatFileSize(summary.DiskTotalBytes)
	summary.DiskUsedLabel = FormatFileSize(summary.DiskUsedBytes)
	for siteIndex := range summary.Sites {
		summary.Sites[siteIndex].UsedLabel = FormatFileSize(summary.Sites[siteIndex].UsedBytes)
		summary.Sites[siteIndex].LimitLabel = FormatFileSize(summary.Sites[siteIndex].LimitBytes)
	}
	for planIndex := range summary.Plans {
		summary.Plans[planIndex].QuotaLabel = FormatFileSize(summary.Plans[planIndex].QuotaBytes)
	}
}

func (store Store) LogRegistrySyncEvent(ctx context.Context, installationID, status, errorMessage string) error {
	return store.LogRegistrySyncEventWithSummary(ctx, installationID, status, errorMessage, RegistrySyncSummary{})
}

func (store Store) LogRegistrySyncEventWithSummary(ctx context.Context, installationID, status, errorMessage string, summary RegistrySyncSummary) error {
	installationID = strings.TrimSpace(installationID)
	if installationID == "" {
		return nil
	}
	summaryJSON := ""
	if summary.Version != 0 || summary.OwnerEmail != "" || summary.ServerIP != "" || summary.ServerStatus != "" || summary.ServerDomain != "" || summary.SiteCount != 0 || summary.PlanCount != 0 || summary.RoleCount != 0 || summary.EventCount != 0 {
		summaryBytes, marshalErr := json.Marshal(summary)
		if marshalErr == nil {
			summaryJSON = string(summaryBytes)
		}
	}
	_, err := store.DB.ExecContext(ctx, `INSERT INTO registry_sync_events(installation_id,status,error,created_at,summary_json) VALUES(?,?,?,?,?)`,
		installationID, strings.TrimSpace(status), strings.TrimSpace(errorMessage), time.Now().UTC().Format(time.RFC3339), summaryJSON)
	return err
}

func (store Store) ClientHostings(ctx context.Context) []ClientHosting {
	rows, err := store.DB.QueryContext(ctx, `SELECT installation_id,COALESCE(owner_email,''),COALESCE(server_ip,''),COALESCE(server_status,''),COALESCE(server_domain,''),COALESCE(sitebrush_version,''),COALESCE(os_name,''),COALESCE(os_version,''),COALESCE(cpu_model,''),COALESCE(cpu_cores,0),COALESCE(cpu_usage_percent_1h,0),COALESCE(load_average_1h,0),COALESCE(ram_total_bytes,0),COALESCE(server_uptime_seconds,0),COALESCE(storage_path,''),COALESCE(disk_free_bytes,0),COALESCE(disk_total_bytes,0),COALESCE(last_seen_at,'') FROM client_hostings ORDER BY last_seen_at DESC,installation_id ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	hostings := make([]ClientHosting, 0, 8)
	for rows.Next() {
		var hosting ClientHosting
		if scanErr := rows.Scan(&hosting.InstallationID, &hosting.OwnerEmail, &hosting.ServerIP, &hosting.ServerStatus, &hosting.ServerDomain, &hosting.SitebrushVersion, &hosting.OSName, &hosting.OSVersion, &hosting.CPUModel, &hosting.CPUCores, &hosting.CPUUsagePercent, &hosting.LoadAverage, &hosting.RAMTotalBytes, &hosting.ServerUptimeSeconds, &hosting.StoragePath, &hosting.DiskFreeBytes, &hosting.DiskTotalBytes, &hosting.LastSeenAt); scanErr != nil {
			continue
		}
		hosting.DiskFreeLabel = FormatFileSize(hosting.DiskFreeBytes)
		hosting.DiskTotalLabel = FormatFileSize(hosting.DiskTotalBytes)
		if hosting.DiskTotalBytes > hosting.DiskFreeBytes {
			hosting.DiskUsedBytes = hosting.DiskTotalBytes - hosting.DiskFreeBytes
		}
		hosting.DiskUsedLabel = FormatFileSize(hosting.DiskUsedBytes)
		hosting.RAMTotalLabel = FormatFileSize(hosting.RAMTotalBytes)
		hosting.ServerUptimeLabel = formatDurationDays(hosting.ServerUptimeSeconds)
		hosting.ServerUptimeClass = serverUptimeStatusClass(hosting.ServerUptimeSeconds)
		hostings = append(hostings, hosting)
	}
	for hostingIndex := range hostings {
		hostings[hostingIndex].Sites = store.clientHostingSites(ctx, hostings[hostingIndex].InstallationID)
		tlsChecks := store.siteTLSChecks(ctx, hostings[hostingIndex].InstallationID)
		for siteIndex := range hostings[hostingIndex].Sites {
			check := tlsChecks[hostings[hostingIndex].Sites[siteIndex].Domain]
			hostings[hostingIndex].Sites[siteIndex].HTTPSAvailable = check.HTTPSAvailable
			hostings[hostingIndex].Sites[siteIndex].CertExpiresAt = check.CertExpiresAt
			hostings[hostingIndex].Sites[siteIndex].CertDaysLeft = check.CertDaysLeft
			hostings[hostingIndex].Sites[siteIndex].TLSStatusClass = check.StatusClass
		}
		hostings[hostingIndex].Plans = store.clientHostingPlans(ctx, hostings[hostingIndex].InstallationID)
		hostings[hostingIndex].Roles = store.clientHostingRoles(ctx, hostings[hostingIndex].InstallationID)
		hostings[hostingIndex].Events = store.clientHostingEvents(ctx, hostings[hostingIndex].InstallationID, 12)
		hostings[hostingIndex].SiteCount = len(hostings[hostingIndex].Sites)
		emailSet := make(map[string]struct{})
		if strings.TrimSpace(hostings[hostingIndex].OwnerEmail) != "" {
			emailSet[strings.TrimSpace(hostings[hostingIndex].OwnerEmail)] = struct{}{}
		}
		for _, site := range hostings[hostingIndex].Sites {
			hostings[hostingIndex].TotalUsedBytes += site.UsedBytes
			if strings.TrimSpace(site.OwnerEmail) != "" {
				emailSet[strings.TrimSpace(site.OwnerEmail)] = struct{}{}
			}
			for _, email := range site.AdminEmails {
				emailSet[email] = struct{}{}
			}
		}
		hostings[hostingIndex].ClientEmails = sortedStringsFromMap(emailSet)
		hostings[hostingIndex].TotalUsedLabel = FormatFileSize(hostings[hostingIndex].TotalUsedBytes)
		if hostings[hostingIndex].DiskTotalBytes > 0 {
			hostings[hostingIndex].DiskUsedPercent = int(math.Round(float64(hostings[hostingIndex].DiskUsedBytes) / float64(hostings[hostingIndex].DiskTotalBytes) * 100))
			if hostings[hostingIndex].DiskUsedPercent > 100 {
				hostings[hostingIndex].DiskUsedPercent = 100
			}
		}
		hostings[hostingIndex].DiskStatusClass = metricStatusClass(float64(hostings[hostingIndex].DiskUsedPercent), 80, 95)
		hostings[hostingIndex].CPUStatusClass = metricStatusClass(hostings[hostingIndex].CPUUsagePercent, 80, 95)
		hostings[hostingIndex].LoadStatusClass = loadAverageStatusClass(hostings[hostingIndex].LoadAverage, hostings[hostingIndex].CPUCores)
		uptimePercent, responseMS := store.serverNetworkSummary(ctx, hostings[hostingIndex].InstallationID)
		hostings[hostingIndex].NetworkUptimePercent = uptimePercent
		hostings[hostingIndex].NetworkUptimeLabel = fmt.Sprintf("%.2f%%", uptimePercent)
		hostings[hostingIndex].LastResponseMS = responseMS
		hostings[hostingIndex].NetworkStatusClass = metricStatusClass(100-uptimePercent, 1, 5)
	}
	return hostings
}

type SiteTLSCheck struct {
	Domain         string
	InstallationID string
	HTTPSAvailable bool
	CertExpiresAt  string
	CertDaysLeft   int
	StatusClass    string
	Error          string
}

func (store Store) LogServerNetworkCheck(ctx context.Context, installationID, serverDomain, serverIP string, success bool, responseMS int, errorMessage string) error {
	successFlag := 0
	if success {
		successFlag = 1
	}
	_, err := store.DB.ExecContext(ctx, `INSERT INTO server_network_checks(installation_id,server_domain,server_ip,success,response_ms,error,checked_at) VALUES(?,?,?,?,?,?,?)`,
		strings.TrimSpace(installationID), strings.ToLower(strings.TrimSpace(serverDomain)), strings.TrimSpace(serverIP), successFlag, responseMS, strings.TrimSpace(errorMessage), time.Now().UTC().Format(time.RFC3339))
	return err
}

func (store Store) SaveSiteTLSCheck(ctx context.Context, check SiteTLSCheck) error {
	availableFlag := 0
	if check.HTTPSAvailable {
		availableFlag = 1
	}
	_, err := store.DB.ExecContext(ctx, `INSERT INTO site_tls_checks(domain,installation_id,https_available,cert_expires_at,cert_days_left,status,error,checked_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(domain) DO UPDATE SET installation_id=excluded.installation_id,https_available=excluded.https_available,cert_expires_at=excluded.cert_expires_at,cert_days_left=excluded.cert_days_left,status=excluded.status,error=excluded.error,checked_at=excluded.checked_at`,
		strings.ToLower(strings.TrimSpace(check.Domain)), strings.TrimSpace(check.InstallationID), availableFlag, strings.TrimSpace(check.CertExpiresAt), check.CertDaysLeft, strings.TrimSpace(check.StatusClass), strings.TrimSpace(check.Error), time.Now().UTC().Format(time.RFC3339))
	return err
}

func (store Store) serverNetworkSummary(ctx context.Context, installationID string) (float64, int) {
	since := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	rows, err := store.DB.QueryContext(ctx, `SELECT COALESCE(success,0),COALESCE(response_ms,0) FROM server_network_checks WHERE installation_id=? AND checked_at>=? ORDER BY checked_at ASC`, strings.TrimSpace(installationID), since)
	if err != nil {
		return 100, 0
	}
	defer rows.Close()
	total := 0
	successful := 0
	lastResponseMS := 0
	for rows.Next() {
		var success int
		var responseMS int
		if scanErr := rows.Scan(&success, &responseMS); scanErr != nil {
			continue
		}
		total++
		if success != 0 {
			successful++
		}
		lastResponseMS = responseMS
	}
	if total == 0 {
		return 100, 0
	}
	return math.Round(float64(successful)/float64(total)*10000) / 100, lastResponseMS
}

func (store Store) siteTLSChecks(ctx context.Context, installationID string) map[string]SiteTLSCheck {
	rows, err := store.DB.QueryContext(ctx, `SELECT domain,COALESCE(https_available,0),COALESCE(cert_expires_at,''),COALESCE(cert_days_left,0),COALESCE(status,''),COALESCE(error,'') FROM site_tls_checks WHERE installation_id=?`, strings.TrimSpace(installationID))
	if err != nil {
		return nil
	}
	defer rows.Close()
	checks := make(map[string]SiteTLSCheck)
	for rows.Next() {
		var check SiteTLSCheck
		var available int
		if scanErr := rows.Scan(&check.Domain, &available, &check.CertExpiresAt, &check.CertDaysLeft, &check.StatusClass, &check.Error); scanErr != nil {
			continue
		}
		check.HTTPSAvailable = available != 0
		checks[check.Domain] = check
	}
	return checks
}

func metricStatusClass(value float64, warningThreshold float64, dangerThreshold float64) string {
	if value >= dangerThreshold {
		return "hosting-metric-danger"
	}
	if value >= warningThreshold {
		return "hosting-metric-warning"
	}
	return "hosting-metric-ok"
}

func MetricStatusClass(value float64, warningThreshold float64, dangerThreshold float64) string {
	return metricStatusClass(value, warningThreshold, dangerThreshold)
}

func loadAverageStatusClass(loadAverage float64, cpuCores int) string {
	if cpuCores <= 0 || loadAverage <= 0 {
		return "hosting-metric-ok"
	}
	if loadAverage >= float64(cpuCores)*2 {
		return "hosting-metric-danger"
	}
	if loadAverage >= float64(cpuCores) {
		return "hosting-metric-warning"
	}
	return "hosting-metric-ok"
}

func LoadAverageStatusClass(loadAverage float64, cpuCores int) string {
	return loadAverageStatusClass(loadAverage, cpuCores)
}

func serverUptimeStatusClass(seconds int64) string {
	if seconds <= 0 {
		return "hosting-metric-warning"
	}
	days := seconds / 86400
	if days > 5*365 {
		return "hosting-metric-danger"
	}
	if days < 1 || days > 3*365 {
		return "hosting-metric-warning"
	}
	return "hosting-metric-ok"
}

func ServerUptimeStatusClass(seconds int64) string {
	return serverUptimeStatusClass(seconds)
}

func formatDurationDays(seconds int64) string {
	if seconds <= 0 {
		return "нет данных"
	}
	days := seconds / 86400
	if days < 1 {
		hours := seconds / 3600
		return fmt.Sprintf("%d ч", hours)
	}
	years := days / 365
	if years > 0 {
		return fmt.Sprintf("%d г %d д", years, days%365)
	}
	return fmt.Sprintf("%d д", days)
}

func FormatDurationDays(seconds int64) string {
	return formatDurationDays(seconds)
}

func (store Store) clientHostingSites(ctx context.Context, installationID string) []ClientHostingSite {
	rows, err := store.DB.QueryContext(ctx, `SELECT domain,COALESCE(owner_email,''),COALESCE(used_bytes,0),COALESCE(limit_bytes,0),COALESCE(plan_name,''),COALESCE(plan_status,''),COALESCE(plan_paid_status,''),COALESCE(admin_emails,'') FROM client_hosting_sites WHERE installation_id=? ORDER BY used_bytes DESC,domain ASC`, strings.TrimSpace(installationID))
	if err != nil {
		return nil
	}
	defer rows.Close()
	sites := make([]ClientHostingSite, 0, 8)
	for rows.Next() {
		var site ClientHostingSite
		var adminEmails string
		if scanErr := rows.Scan(&site.Domain, &site.OwnerEmail, &site.UsedBytes, &site.LimitBytes, &site.PlanName, &site.PlanStatus, &site.PlanPaidStatus, &adminEmails); scanErr != nil {
			continue
		}
		site.AdminEmails = normalizedHostingEmails(strings.Split(adminEmails, ","))
		site.UsedLabel = FormatFileSize(site.UsedBytes)
		site.LimitLabel = FormatFileSize(site.LimitBytes)
		billingPrice := BillingPriceForUsedBytes(site.UsedBytes)
		site.BillingUsageLabel = BillingUsageLabel(site.UsedBytes)
		site.BillingPriceLabel = billingPrice.PriceLabel
		site.BillingStatusText = billingPrice.StatusText
		site.BillingAmount = billingPrice.Amount
		site.BillingCurrency = billingPrice.Currency
		site.BillingBillable = billingPrice.Billable
		site.OverLimit = site.LimitBytes > 0 && site.UsedBytes > site.LimitBytes
		sites = append(sites, site)
	}
	return sites
}

func (store Store) clientHostingPlans(ctx context.Context, installationID string) []ClientHostingPlan {
	rows, err := store.DB.QueryContext(ctx, `SELECT name,COALESCE(quota_bytes,0),COALESCE(site_limit,0),COALESCE(price,''),COALESCE(currency,''),COALESCE(billing_period,''),COALESCE(paid_status,''),COALESCE(is_default,0) FROM registry_installation_plans WHERE installation_id=? ORDER BY is_default DESC,name ASC`, strings.TrimSpace(installationID))
	if err != nil {
		return nil
	}
	defer rows.Close()
	plans := make([]ClientHostingPlan, 0, 4)
	for rows.Next() {
		var plan ClientHostingPlan
		var quotaBytes int64
		var defaultFlag int
		if scanErr := rows.Scan(&plan.Name, &quotaBytes, &plan.SiteLimit, &plan.Price, &plan.Currency, &plan.BillingPeriod, &plan.PaidStatus, &defaultFlag); scanErr != nil {
			continue
		}
		plan.QuotaLabel = FormatFileSize(quotaBytes)
		plan.IsDefault = defaultFlag != 0
		plans = append(plans, plan)
	}
	return plans
}

func (store Store) clientHostingRoles(ctx context.Context, installationID string) []ClientHostingRole {
	rows, err := store.DB.QueryContext(ctx, `SELECT COALESCE(email,''),COALESCE(role,''),COALESCE(scope,''),COALESCE(domain,'') FROM registry_installation_roles WHERE installation_id=? ORDER BY email ASC,scope ASC,domain ASC,role ASC`, strings.TrimSpace(installationID))
	if err != nil {
		return nil
	}
	defer rows.Close()
	roles := make([]ClientHostingRole, 0, 8)
	for rows.Next() {
		var role ClientHostingRole
		if scanErr := rows.Scan(&role.Email, &role.Role, &role.Scope, &role.Domain); scanErr != nil {
			continue
		}
		roles = append(roles, role)
	}
	return roles
}

func (store Store) clientHostingEvents(ctx context.Context, installationID string, limit int) []ClientHostingEvent {
	if limit <= 0 {
		limit = 12
	}
	rows, err := store.DB.QueryContext(ctx, `SELECT COALESCE(kind,''),COALESCE(status,''),COALESCE(email,''),COALESCE(domain,''),COALESCE(message,''),COALESCE(created_at,'') FROM registry_events WHERE installation_id=? ORDER BY id DESC LIMIT ?`, strings.TrimSpace(installationID), limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	events := make([]ClientHostingEvent, 0, limit)
	for rows.Next() {
		var event ClientHostingEvent
		if scanErr := rows.Scan(&event.Kind, &event.Status, &event.Email, &event.Domain, &event.Message, &event.CreatedAt); scanErr != nil {
			continue
		}
		events = append(events, event)
	}
	return events
}

func (store Store) RegistrySyncEvents(ctx context.Context, limit int) []RegistrySyncEvent {
	if limit <= 0 {
		limit = 50
	}
	rows, err := store.DB.QueryContext(ctx, `SELECT id,installation_id,status,error,created_at,COALESCE(summary_json,'') FROM registry_sync_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	events := make([]RegistrySyncEvent, 0, limit)
	for rows.Next() {
		var event RegistrySyncEvent
		var summaryJSON string
		if scanErr := rows.Scan(&event.ID, &event.InstallationID, &event.Status, &event.Error, &event.CreatedAt, &summaryJSON); scanErr != nil {
			continue
		}
		event.StatusLabel = registrySyncStatusLabel(event.Status, summaryJSON)
		if strings.TrimSpace(summaryJSON) != "" {
			if err := json.Unmarshal([]byte(summaryJSON), &event.Summary); err == nil {
				applyRegistrySyncSummaryLabels(&event.Summary)
				event.HasSummary = true
			}
		}
		events = append(events, event)
	}
	return events
}

func registrySyncStatusLabel(status string, summaryJSON string) string {
	switch strings.TrimSpace(status) {
	case "stored":
		if strings.TrimSpace(summaryJSON) == "" {
			return "принято · старый формат"
		}
		return "принято"
	case "error":
		return "ошибка"
	default:
		if strings.TrimSpace(status) == "" {
			return "неизвестно"
		}
		return strings.TrimSpace(status)
	}
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

func (store Store) LogSupportEvent(ctx context.Context, event HostingSnapshotEvent) error {
	kind := strings.TrimSpace(event.Kind)
	if kind == "" {
		return nil
	}
	createdAt := strings.TrimSpace(event.CreatedAt)
	if createdAt == "" {
		createdAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := store.DB.ExecContext(ctx, `INSERT INTO support_events(kind,status,email,domain,message,created_at) VALUES(?,?,?,?,?,?)`,
		kind,
		strings.TrimSpace(event.Status),
		strings.ToLower(strings.TrimSpace(event.Email)),
		strings.ToLower(strings.TrimSpace(event.Domain)),
		truncateSupportEventMessage(event.Message),
		createdAt)
	return err
}

func truncateSupportEventMessage(message string) string {
	message = strings.TrimSpace(message)
	messageRunes := []rune(message)
	if len(messageRunes) <= 512 {
		return message
	}
	return string(messageRunes[:512])
}

func (store Store) SupportEvents(ctx context.Context, limit int) []HostingSnapshotEvent {
	if limit <= 0 || limit > 200 {
		limit = 80
	}
	rows, err := store.DB.QueryContext(ctx, `SELECT kind,status,email,domain,message,created_at FROM support_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	events := make([]HostingSnapshotEvent, 0, limit)
	for rows.Next() {
		var event HostingSnapshotEvent
		if scanErr := rows.Scan(&event.Kind, &event.Status, &event.Email, &event.Domain, &event.Message, &event.CreatedAt); scanErr != nil {
			continue
		}
		events = append(events, event)
	}
	return events
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

func (store Store) DeleteServiceMailEvents(ctx context.Context, eventIDs []int) error {
	cleanEventIDs := make([]int, 0, len(eventIDs))
	for _, eventID := range eventIDs {
		if eventID > 0 {
			cleanEventIDs = append(cleanEventIDs, eventID)
		}
	}
	if len(cleanEventIDs) == 0 {
		return nil
	}
	placeholders := make([]string, len(cleanEventIDs))
	arguments := make([]any, len(cleanEventIDs))
	for eventIndex, eventID := range cleanEventIDs {
		placeholders[eventIndex] = "?"
		arguments[eventIndex] = eventID
	}
	_, err := store.DB.ExecContext(ctx, `DELETE FROM service_mail_events WHERE id IN (`+strings.Join(placeholders, ",")+`)`, arguments...)
	return err
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
		billingPrice := BillingPriceForUsedBytes(usage.UsedBytes)
		quotaInput := FormatQuotaInput(usage.LimitBytes)
		planName := ""
		planQuotaLabel := ""
		if assignment.PlanID > 0 {
			if plan, found := planByID[assignment.PlanID]; found {
				planName = plan.Name
				planQuotaLabel = plan.QuotaLabel
				if plan.QuotaBytes > 0 {
					quotaInput = FormatQuotaInput(plan.QuotaBytes)
				}
			}
		}
		isDemo := strings.TrimSpace(usage.Domain) != "" && strings.EqualFold(strings.TrimSpace(usage.Domain), strings.TrimSpace(demoDomain))
		isMainDomain := mainDomain != "" && strings.EqualFold(strings.TrimSpace(usage.Domain), mainDomain)
		sites = append(sites, Site{
			Domain:            usage.Domain,
			IsDemo:            isDemo,
			IsMainDomain:      isMainDomain,
			Aliases:           strings.Join(usage.Aliases, ", "),
			UsedBytes:         usage.UsedBytes,
			UsedLabel:         FormatFileSize(usage.UsedBytes),
			LimitLabel:        FormatFileSize(usage.LimitBytes),
			FreeLabel:         FormatFileSize(freeBytes),
			UsedPercent:       usedPercent,
			QuotaInput:        quotaInput,
			PlanID:            assignment.PlanID,
			PlanName:          planName,
			PlanQuotaLabel:    planQuotaLabel,
			ServiceStatus:     assignment.ServiceStatus,
			BillingUsageLabel: BillingUsageLabel(usage.UsedBytes),
			BillingPriceLabel: billingPrice.PriceLabel,
			BillingStatusText: billingPrice.StatusText,
			BillingAmount:     billingPrice.Amount,
			BillingCurrency:   billingPrice.Currency,
			BillingBillable:   billingPrice.Billable,
			AdminEmails:       strings.Join(usage.AdminEmails, ", "),
			CanDelete:         !isDemo && strings.TrimSpace(usage.Domain) != strings.TrimSpace(currentDomain),
			DatabasePath:      usage.DatabasePath,
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

type BillingPrice struct {
	UsedMegabytes     int64
	BillableMegabytes int64
	Amount            string
	Currency          string
	PriceLabel        string
	StatusText        string
	Billable          bool
}

func BillingPriceForUsedBytes(usedBytes int64) BillingPrice {
	usedMegabytes := bytesToRoundedMegabytes(usedBytes)
	billableMegabytes := roundMegabytesUpToBillingStep(usedMegabytes)
	if usedMegabytes == 0 {
		billableMegabytes = 0
	}
	cents := billableMegabytes / billingStepMegabytes * billingStepCents
	price := BillingPrice{
		UsedMegabytes:     usedMegabytes,
		BillableMegabytes: billableMegabytes,
		Amount:            formatEuroAmount(cents),
		Currency:          "EUR",
		PriceLabel:        "€" + formatEuroAmount(cents) + "/мес",
		Billable:          usedMegabytes > billingIncludedMegabytes,
	}
	if price.Billable {
		price.StatusText = "к выставлению"
	} else {
		price.StatusText = "бесплатно до 500 MB"
	}
	return price
}

func BillingUsageLabel(usedBytes int64) string {
	usedMegabytes := bytesToRoundedMegabytes(usedBytes)
	return strconv.FormatInt(usedMegabytes, 10) + " MB"
}

func bytesToRoundedMegabytes(bytes int64) int64 {
	if bytes <= 0 {
		return 0
	}
	const megabyte = int64(1000 * 1000)
	return (bytes + megabyte - 1) / megabyte
}

func roundMegabytesUpToBillingStep(megabytes int64) int64 {
	if megabytes <= 0 {
		return billingStepMegabytes
	}
	if megabytes%billingStepMegabytes == 0 {
		return megabytes
	}
	return ((megabytes / billingStepMegabytes) + 1) * billingStepMegabytes
}

func formatEuroAmount(cents int64) string {
	if cents <= 0 {
		return "0.00"
	}
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
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
