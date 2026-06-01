package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"sitebrush/pkg/demo"
)

const DefaultStorageLimitBytes int64 = 10 * 1024 * 1024 * 1024
const DefaultDeletionBackupRetentionDays = 365
const currentBillingSchemaVersion = 4

type Store struct {
	DB *sql.DB
}

type Plan struct {
	ID            int
	Name          string
	QuotaLabel    string
	QuotaBytes    int64
	Price         string
	Currency      string
	BillingPeriod string
	IsDefault     bool
}

type Site struct {
	Domain            string
	IsDemo            bool
	Aliases           string
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
	schemaComplete, err := billingSchemaComplete(ctx, database)
	if err != nil {
		return fmt.Errorf("verify billing schema: %w", err)
	}
	if schemaVersion >= currentBillingSchemaVersion && schemaComplete {
		return nil
	}
	queries := []string{
		`CREATE TABLE IF NOT EXISTS server_managers(domain TEXT,email TEXT,role TEXT,scope_domain TEXT,created_at TEXT,PRIMARY KEY(domain,email,role,scope_domain));`,
		`CREATE TABLE IF NOT EXISTS server_settings(name TEXT PRIMARY KEY,value TEXT,updated_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS site_service_plans(id INTEGER PRIMARY KEY AUTOINCREMENT,name TEXT UNIQUE,quota_bytes INTEGER,price TEXT,currency TEXT,billing_period TEXT,is_default INTEGER DEFAULT 0,created_at TEXT,updated_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS site_service_assignments(domain TEXT PRIMARY KEY,plan_id INTEGER DEFAULT 0,service_status TEXT,notes TEXT,updated_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS site_registration_requests(id INTEGER PRIMARY KEY AUTOINCREMENT,domain TEXT,name TEXT,email TEXT,phone TEXT,plan_id INTEGER DEFAULT 0,status TEXT,owner_message TEXT,created_at TEXT,updated_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS site_deletion_backups(id INTEGER PRIMARY KEY AUTOINCREMENT,domain TEXT,archive_path TEXT,file_name TEXT,size_bytes INTEGER,token TEXT,token_created_at TEXT,created_at TEXT,expires_at TEXT,retention_days INTEGER,owner_contacts TEXT,metadata_json TEXT,language_code TEXT,downloaded_at TEXT,download_count INTEGER DEFAULT 0);`,
	}
	queries = append(queries, demo.SchemaQueries()...)
	for queryIndex, query := range queries {
		if _, err := database.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("billing schema statement %d: %w", queryIndex+1, err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = database.ExecContext(ctx, `INSERT OR IGNORE INTO server_settings(name,value,updated_at) VALUES('auto_registration_enabled','1',?)`, now)
	_, _ = database.ExecContext(ctx, `INSERT OR IGNORE INTO server_settings(name,value,updated_at) VALUES('deletion_backup_retention_days',?,?)`, strconv.Itoa(DefaultDeletionBackupRetentionDays), now)
	_, _ = database.ExecContext(ctx, `INSERT OR IGNORE INTO site_service_plans(name,quota_bytes,price,currency,billing_period,is_default,created_at,updated_at) VALUES('Free',?,'0','USD','monthly',1,?,?)`,
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

func billingSchemaComplete(ctx context.Context, database *sql.DB) (bool, error) {
	tableNames := []string{"server_managers", "server_settings", "site_service_plans", "site_service_assignments", "site_registration_requests", "site_deletion_backups"}
	tableNames = append(tableNames, demo.TableNames()...)
	for _, tableName := range tableNames {
		found, err := tableExists(ctx, database, tableName)
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

func (store Store) AssignSite(ctx context.Context, domain string, planID int, serviceStatus string) error {
	_, err := store.DB.ExecContext(ctx, `INSERT INTO site_service_assignments(domain,plan_id,service_status,notes,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(domain) DO UPDATE SET plan_id=excluded.plan_id,service_status=excluded.service_status,updated_at=excluded.updated_at`,
		strings.TrimSpace(domain), planID, strings.TrimSpace(serviceStatus), "", time.Now().UTC().Format(time.RFC3339))
	return err
}

func settingText(ctx context.Context, database *sql.DB, name string) string {
	var value string
	err := database.QueryRowContext(ctx, `SELECT value FROM server_settings WHERE name=?`, name).Scan(&value)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func (store Store) RemoveSiteAssignment(ctx context.Context, domain string) {
	_, _ = store.DB.ExecContext(ctx, `DELETE FROM site_service_assignments WHERE domain=?`, strings.TrimSpace(domain))
}

func (store Store) SavePlan(ctx context.Context, planID int, name string, quotaBytes int64, price, currency, billingPeriod string, isDefault bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("plan name is required")
	}
	if quotaBytes <= 0 {
		return fmt.Errorf("plan quota is required")
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
		_, err = store.DB.ExecContext(ctx, `UPDATE site_service_plans SET name=?,quota_bytes=?,price=?,currency=?,billing_period=?,is_default=?,updated_at=? WHERE id=?`,
			name, quotaBytes, strings.TrimSpace(price), currency, billingPeriod, defaultFlag, now, planID)
	} else {
		_, err = store.DB.ExecContext(ctx, `INSERT INTO site_service_plans(name,quota_bytes,price,currency,billing_period,is_default,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
			name, quotaBytes, strings.TrimSpace(price), currency, billingPeriod, defaultFlag, now, now)
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
	rows, err := store.DB.QueryContext(ctx, `SELECT id,name,quota_bytes,price,currency,billing_period,is_default FROM site_service_plans ORDER BY is_default DESC, quota_bytes ASC, name ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	plans := make([]Plan, 0, 4)
	for rows.Next() {
		var plan Plan
		var isDefault int
		if scanErr := rows.Scan(&plan.ID, &plan.Name, &plan.QuotaBytes, &plan.Price, &plan.Currency, &plan.BillingPeriod, &isDefault); scanErr != nil {
			continue
		}
		plan.QuotaLabel = FormatFileSize(plan.QuotaBytes)
		plan.IsDefault = isDefault == 1
		plans = append(plans, plan)
	}
	return plans
}

func (store Store) PlanByID(ctx context.Context, planID int) (Plan, bool) {
	var plan Plan
	var isDefault int
	err := store.DB.QueryRowContext(ctx, `SELECT id,name,quota_bytes,price,currency,billing_period,is_default FROM site_service_plans WHERE id=?`, planID).Scan(
		&plan.ID, &plan.Name, &plan.QuotaBytes, &plan.Price, &plan.Currency, &plan.BillingPeriod, &isDefault)
	if err != nil {
		return Plan{}, false
	}
	plan.QuotaLabel = FormatFileSize(plan.QuotaBytes)
	plan.IsDefault = isDefault == 1
	return plan, true
}

func (store Store) DefaultPlan(ctx context.Context) (Plan, bool) {
	var plan Plan
	var isDefault int
	err := store.DB.QueryRowContext(ctx, `SELECT id,name,quota_bytes,price,currency,billing_period,is_default FROM site_service_plans ORDER BY is_default DESC, quota_bytes ASC, name ASC LIMIT 1`).Scan(
		&plan.ID, &plan.Name, &plan.QuotaBytes, &plan.Price, &plan.Currency, &plan.BillingPeriod, &isDefault)
	if err != nil {
		return Plan{}, false
	}
	plan.QuotaLabel = FormatFileSize(plan.QuotaBytes)
	plan.IsDefault = isDefault == 1
	return plan, true
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

func applyPlanToSiteRequest(request *SiteRequest, plan Plan) {
	if request == nil || plan.ID == 0 {
		return
	}
	request.PlanName = plan.Name
	request.PlanQuotaLabel = plan.QuotaLabel
	request.PlanPrice = plan.Price
	request.PlanCurrency = plan.Currency
	request.PlanBillingPeriod = plan.BillingPeriod
	request.PlanDescriptionText = fmt.Sprintf("Storage: %s. Price: %s %s/%s. Includes Sitebrush editor, file storage, publishing, backups, domain settings, and automatic SSL when DNS is configured.", plan.QuotaLabel, plan.Price, plan.Currency, plan.BillingPeriod)
}

func BuildSites(usages []SiteUsage, plans []Plan, assignments map[string]ServiceAssignment, currentDomain string) []Site {
	return BuildSitesWithDemoDomain(usages, plans, assignments, currentDomain, "")
}

func BuildSitesWithDemoDomain(usages []SiteUsage, plans []Plan, assignments map[string]ServiceAssignment, currentDomain string, demoDomain string) []Site {
	planByID := make(map[int]Plan)
	for _, plan := range plans {
		planByID[plan.ID] = plan
	}
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
		sites = append(sites, Site{
			Domain:        usage.Domain,
			IsDemo:        isDemo,
			Aliases:       strings.Join(usage.Aliases, ", "),
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
