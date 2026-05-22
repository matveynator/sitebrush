package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"math"
	"mime"
	"net"
	"net/http"
	stdmail "net/mail"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/html"
	"golang.org/x/term"
	appcli "sitebrush/pkg/cli"
	_ "sitebrush/pkg/database/drivers"
	"sitebrush/pkg/desktop"
	"sitebrush/pkg/dirprotect"
	"sitebrush/pkg/diskusage"
	"sitebrush/pkg/geoip"
	"sitebrush/pkg/grabber"
	"sitebrush/pkg/mailout"
	"sitebrush/pkg/serviceinstall"
	"sitebrush/pkg/winservice"
)

//go:embed web/*
var embeddedWebFiles embed.FS
var CompileVersion = "dev"
var translationCatalog = loadTranslationCatalog()
var guestContextMenuTemplates = buildGuestContextMenuTemplates()
var guestNotFoundPageTemplates = buildGuestNotFoundPageTemplates()

const storageAppName = "sitebrush"
const defaultDBPath = "storage/db/sitebrush.db"
const grabResourceMaxDepth = 64
const wholeSiteImportMaxPages = 2048
const defaultDomainStorageLimitBytes int64 = 10 * 1024 * 1024 * 1024
const defaultAnalyticsMemoryLimitBytes int64 = 500 * 1024 * 1024
const defaultGuestStaticHTMLCacheLimitBytes int64 = 128 * 1024 * 1024
const guestStaticHTMLCacheQueueSize = 4096
const authIPFailureCacheQueueSize = 4096
const emailDeliveryQueueSize = 256
const emailConfirmationTTL = 24 * time.Hour
const pagePasswordSessionTTL = time.Hour

// App keeps only explicit dependencies to stay readable and easy to swap.
type App struct {
	db                    sqlExecutor
	storagePath           string
	nativeFileDialog      bool
	automaticSSLAvailable bool
	grabTracker           *grabProgressTracker
	publishTracker        *publishProgressTracker
	analyticsEvents       chan siteAnalyticsEvent
	analyticsMemoryLimit  int64
	guestStaticHTMLCache  chan guestStaticHTMLCacheRequest
	geoIP                 *geoip.Resolver
	domainLogEvents       chan domainLogEvent
	autoCertGuard         chan autoCertGuardRequest
	authIPFailureCache    chan authIPFailureCacheRequest
	emailDelivery         chan emailDeliveryJob
	sendEmail             emailSender
}

type emailSender func(context.Context, mailout.Message) error

type emailDeliveryJob struct {
	message mailout.Message
}

type autoCertGuardRequest struct {
	action   string
	domain   string
	response chan autoCertGuardResponse
}

type autoCertGuardResponse struct {
	allowed bool
	reason  string
}

type guestStaticHTMLCacheRequest struct {
	filePath     string
	pagePath     string
	domain       string
	languageCode string
	modTime      time.Time
	size         int64
	response     chan guestStaticHTMLCacheResponse
}

type guestStaticHTMLCacheResponse struct {
	body []byte
	err  error
}

type guestStaticHTMLCacheEntry struct {
	modTime time.Time
	size    int64
	body    []byte
}

type authIPFailureCacheRequest struct {
	action   string
	domain   string
	clientIP string
	state    authIPFailure
	response chan authIPFailureCacheResponse
}

type authIPFailureCacheResponse struct {
	state authIPFailure
	found bool
}

type sqlExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type siteDBMigrator func(*sql.DB) error

type siteDBRequest struct {
	domain   string
	response chan siteDBResponse
}

type siteDBResponse struct {
	db  *sql.DB
	err error
}

type siteDomainContextKey struct{}

func contextWithDomain(ctx context.Context, domain string) context.Context {
	normalizedDomain := normalizeDomainName(domain)
	if normalizedDomain == "" {
		normalizedDomain = "localhost"
	}
	return context.WithValue(ctx, siteDomainContextKey{}, normalizedDomain)
}

func domainFromContext(ctx context.Context) string {
	if ctx == nil {
		return "localhost"
	}
	domain, ok := ctx.Value(siteDomainContextKey{}).(string)
	if !ok {
		return "localhost"
	}
	normalizedDomain := normalizeDomainName(domain)
	if normalizedDomain == "" {
		return "localhost"
	}
	return normalizedDomain
}

// perSiteDBRouter resolves a separate sqlite file per domain and keeps the map
// ownership in a single goroutine so we do not coordinate it with mutexes.
type perSiteDBRouter struct {
	fallbackDomain string
	requests       chan siteDBRequest
	closeRequests  chan chan error
	noopDatabase   *sql.DB
}

func newPerSiteDBRouter(siteDatabaseRootDir string, fallbackDomain string, migrate siteDBMigrator) *perSiteDBRouter {
	router := &perSiteDBRouter{
		fallbackDomain: fallbackDomain,
		requests:       make(chan siteDBRequest),
		closeRequests:  make(chan chan error),
		noopDatabase:   mustOpenNoopSQLite(),
	}
	go router.run(siteDatabaseRootDir, migrate)
	return router
}

func (r *perSiteDBRouter) run(siteDatabaseRootDir string, migrate siteDBMigrator) {
	databasesByDomain := make(map[string]*sql.DB)
	primaryDomainsByAlias := make(map[string]string)
	for {
		select {
		case request := <-r.requests:
			domain := normalizeDomainName(request.domain)
			if domain == "" {
				domain = r.fallbackDomain
			}
			databaseDomain := domain
			if primaryDomain := primaryDomainsByAlias[domain]; primaryDomain != "" {
				database, err := r.databaseForDomain(siteDatabaseRootDir, databasesByDomain, primaryDomain, migrate)
				if err != nil {
					request.response <- siteDBResponse{err: err}
					continue
				}
				if activePrimaryDomainForAlias(database, domain) == primaryDomain {
					request.response <- siteDBResponse{db: database}
					continue
				}
				delete(primaryDomainsByAlias, domain)
			}
			if primaryDomain := findActivePrimaryDomainForAlias(siteDatabaseRootDir, databasesByDomain, domain); primaryDomain != "" {
				primaryDomainsByAlias[domain] = primaryDomain
				databaseDomain = primaryDomain
			}
			database, err := r.databaseForDomain(siteDatabaseRootDir, databasesByDomain, databaseDomain, migrate)
			if err != nil {
				request.response <- siteDBResponse{err: err}
				continue
			}
			request.response <- siteDBResponse{db: database}
		case closeResponse := <-r.closeRequests:
			var closeErr error
			for _, database := range databasesByDomain {
				if err := database.Close(); err != nil && closeErr == nil {
					closeErr = err
				}
			}
			_ = routerCloseNoop(r.noopDatabase)
			closeResponse <- closeErr
			return
		}
	}
}

func (r *perSiteDBRouter) databaseForDomain(siteDatabaseRootDir string, databasesByDomain map[string]*sql.DB, domain string, migrate siteDBMigrator) (*sql.DB, error) {
	databaseDomain := normalizeDomainName(domain)
	if databaseDomain == "" {
		databaseDomain = r.fallbackDomain
	}
	database := databasesByDomain[databaseDomain]
	if database != nil {
		return database, nil
	}
	databasePath := filepath.Join(siteDatabaseRootDir, domainStorageName(databaseDomain)+".db")
	if err := ensureParentDir(databasePath); err != nil {
		return nil, err
	}
	nextDatabase, err := sql.Open("sqlite", "file:"+databasePath)
	if err != nil {
		return nil, err
	}
	if migrateErr := migrate(nextDatabase); migrateErr != nil {
		_ = nextDatabase.Close()
		return nil, migrateErr
	}
	databasesByDomain[databaseDomain] = nextDatabase
	return nextDatabase, nil
}

func findActivePrimaryDomainForAlias(siteDatabaseRootDir string, databasesByDomain map[string]*sql.DB, aliasDomain string) string {
	aliasDomain = normalizeDomainName(aliasDomain)
	if aliasDomain == "" {
		return ""
	}
	for _, database := range databasesByDomain {
		if primaryDomain := activePrimaryDomainForAlias(database, aliasDomain); primaryDomain != "" {
			return primaryDomain
		}
	}
	databaseEntries, err := os.ReadDir(siteDatabaseRootDir)
	if err != nil {
		return ""
	}
	for _, databaseEntry := range databaseEntries {
		if databaseEntry.IsDir() || !strings.HasSuffix(databaseEntry.Name(), ".db") {
			continue
		}
		databasePath := filepath.Join(siteDatabaseRootDir, databaseEntry.Name())
		database, err := sql.Open("sqlite", "file:"+databasePath)
		if err != nil {
			continue
		}
		primaryDomain := activePrimaryDomainForAlias(database, aliasDomain)
		_ = database.Close()
		if primaryDomain != "" {
			return primaryDomain
		}
	}
	return ""
}

func activePrimaryDomainForAlias(database *sql.DB, aliasDomain string) string {
	if database == nil {
		return ""
	}
	var rawPrimaryDomain string
	err := database.QueryRowContext(context.Background(), `SELECT primary_domain FROM domain_aliases WHERE alias_domain=? AND is_verified=1 AND dns_a_ok=1 LIMIT 1`, aliasDomain).Scan(&rawPrimaryDomain)
	if err != nil {
		return ""
	}
	primaryDomain := normalizeDomainName(rawPrimaryDomain)
	if primaryDomain == "" || primaryDomain == aliasDomain {
		return ""
	}
	return primaryDomain
}

func (r *perSiteDBRouter) Close() error {
	closeResponse := make(chan error, 1)
	r.closeRequests <- closeResponse
	return <-closeResponse
}

func (r *perSiteDBRouter) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	database, err := r.databaseForContext(ctx)
	if err != nil {
		return nil, err
	}
	return database.ExecContext(ctx, query, args...)
}

func (r *perSiteDBRouter) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	database, err := r.databaseForContext(ctx)
	if err != nil {
		return nil, err
	}
	return database.QueryContext(ctx, query, args...)
}

func (r *perSiteDBRouter) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	database, err := r.databaseForContext(ctx)
	if err != nil {
		log.Printf("site db router query-row fallback: %v", err)
		return r.noopDatabase.QueryRowContext(ctx, `SELECT 1 WHERE 0`)
	}
	return database.QueryRowContext(ctx, query, args...)
}

func (r *perSiteDBRouter) databaseForContext(ctx context.Context) (*sql.DB, error) {
	domain := domainFromContext(ctx)
	if domain == "" {
		domain = r.fallbackDomain
	}
	response := make(chan siteDBResponse, 1)
	r.requests <- siteDBRequest{domain: domain, response: response}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case next := <-response:
		return next.db, next.err
	}
}

func mustOpenNoopSQLite() *sql.DB {
	noopDatabase, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err == nil {
		return noopDatabase
	}
	noopDatabase, err = sql.Open("sqlite", ":memory:")
	if err == nil {
		return noopDatabase
	}
	log.Fatalf("failed to open noop sqlite database: %v", err)
	return nil
}

func routerCloseNoop(noopDatabase *sql.DB) error {
	if noopDatabase == nil {
		return nil
	}
	return noopDatabase.Close()
}

type siteAnalyticsEvent struct {
	Domain         string
	Path           string
	Query          string
	Method         string
	StatusCode     int
	ContentSource  string
	OccurredAt     time.Time
	Duration       time.Duration
	ClientIP       string
	RemoteAddress  string
	UserAgent      string
	Referer        string
	AcceptLanguage string
	GeoCountryCode string
	GeoCity        string
	GeoLatitude    float64
	GeoLongitude   float64
	GeoSource      string
	VisitorID      string
	IsAdmin        bool
	IsAsset        bool
	IsController   bool
}

type analyticsPreparedReport struct {
	GeneratedAt        string              `json:"generated_at"`
	PeriodStart        string              `json:"period_start"`
	PeriodEnd          string              `json:"period_end"`
	TotalRequests      int                 `json:"total_requests"`
	PageViews          int                 `json:"page_views"`
	UniqueVisitors     int                 `json:"unique_visitors"`
	HumanRequests      int                 `json:"human_requests"`
	BotRequests        int                 `json:"bot_requests"`
	ReturningVisitors  int                 `json:"returning_visitors"`
	ReturnVisits       int                 `json:"return_visits"`
	Sessions           int                 `json:"sessions"`
	BounceRate         float64             `json:"bounce_rate"`
	AverageDurationMS  int64               `json:"average_duration_ms"`
	ErrorCount         int                 `json:"error_count"`
	AdminRequests      int                 `json:"admin_requests"`
	StaticRequests     int                 `json:"static_requests"`
	TopPages           []analyticsCountRow `json:"top_pages"`
	EntryPages         []analyticsCountRow `json:"entry_pages"`
	ExitPages          []analyticsCountRow `json:"exit_pages"`
	TrafficSources     []analyticsCountRow `json:"traffic_sources"`
	Referrers          []analyticsCountRow `json:"referrers"`
	ReturningSources   []analyticsCountRow `json:"returning_sources"`
	ReturningReferrers []analyticsCountRow `json:"returning_referrers"`
	Countries          []analyticsCountRow `json:"countries"`
	Cities             []analyticsCountRow `json:"cities"`
	EntryHours         []analyticsCountRow `json:"entry_hours"`
	MapPoints          []analyticsMapPoint `json:"map_points"`
	Devices            []analyticsCountRow `json:"devices"`
	VisitorTypes       []analyticsCountRow `json:"visitor_types"`
	BotCrawlers        []analyticsCountRow `json:"bot_crawlers"`
	BotReturnSources   []analyticsCountRow `json:"bot_return_sources"`
	BotReferrers       []analyticsCountRow `json:"bot_referrers"`
	Browsers           []analyticsCountRow `json:"browsers"`
	OperatingSystems   []analyticsCountRow `json:"operating_systems"`
	Languages          []analyticsCountRow `json:"languages"`
	StatusCodes        []analyticsCountRow `json:"status_codes"`
	HourlyActivity     []analyticsCountRow `json:"hourly_activity"`
	DailyActivity      []analyticsCountRow `json:"daily_activity"`
	SlowPages          []analyticsCountRow `json:"slow_pages"`
	TopAssets          []analyticsCountRow `json:"top_assets"`
	ErrorPaths         []analyticsCountRow `json:"error_paths"`
	ContentSources     []analyticsCountRow `json:"content_sources"`
	SystemEvents       []analyticsCountRow `json:"system_events,omitempty"`
}

type analyticsCountRow struct {
	Label  string `json:"label"`
	Count  int    `json:"count"`
	Value  string `json:"value,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type analyticsMapPoint struct {
	Label     string  `json:"label"`
	Country   string  `json:"country"`
	City      string  `json:"city,omitempty"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Count     int     `json:"count"`
	Percent   string  `json:"percent"`
	Left      string  `json:"left"`
	Top       string  `json:"top"`
	Detail    string  `json:"detail,omitempty"`
	Color     string  `json:"color"`
	Heat      float64 `json:"heat"`
	Radius    float64 `json:"radius"`
}

type analyticsPageData struct {
	GeneratedAt    string
	Period         string
	Cards          []analyticsMetricCard
	MapTitle       string
	MapHint        string
	MapPoints      []analyticsMapPoint
	MapJSON        template.JS
	MapAttribution template.HTML
	Sections       []analyticsReportSection
}

type analyticsMetricCard struct {
	Label string
	Value string
	Hint  string
}

type analyticsReportSection struct {
	Title       string
	Description string
	Rows        []analyticsReportRow
}

type analyticsReportRow struct {
	Label   string
	Value   string
	Percent string
	Detail  string
}

type grabProgressEvent struct {
	Token                  string `json:"token"`
	Stage                  string `json:"stage"`
	FoundTotal             int    `json:"found_total"`
	DownloadedTotal        int    `json:"downloaded_total"`
	CurrentURL             string `json:"current_url"`
	CurrentPercent         int    `json:"current_percent"`
	CurrentDownloadedBytes int64  `json:"current_downloaded_bytes"`
	CurrentSizeBytes       int64  `json:"current_size_bytes"`
	CompletedPercent       int    `json:"completed_percent"`
}

type publishProgressEvent struct {
	Token            string `json:"token"`
	Stage            string `json:"stage"`
	CurrentPath      string `json:"current_path"`
	Completed        int    `json:"completed"`
	Total            int    `json:"total"`
	CompletedPercent int    `json:"completed_percent"`
	Message          string `json:"message"`
}

type publishPageCandidate struct {
	Path  string
	Title string
	HTML  string
}

type grabResourcePreview struct {
	URL       string `json:"url"`
	Kind      string `json:"kind"`
	SizeBytes int64  `json:"size_bytes"`
}

type grabPreviewResponse struct {
	SourceURL             string                `json:"source_url"`
	PageCount             int                   `json:"page_count"`
	ResourceCount         int                   `json:"resource_count"`
	Resources             []grabResourcePreview `json:"resources"`
	PageDownloadBytes     int64                 `json:"page_download_bytes"`
	PageStorageBytes      int64                 `json:"page_storage_bytes"`
	CurrentUsedBytes      int64                 `json:"current_used_bytes"`
	LimitBytes            int64                 `json:"limit_bytes"`
	FreeBytes             int64                 `json:"free_bytes"`
	SelectedResourceBytes int64                 `json:"selected_resource_bytes"`
	EstimatedImportBytes  int64                 `json:"estimated_import_bytes"`
	ProjectedUsedBytes    int64                 `json:"projected_used_bytes"`
	FitsQuota             bool                  `json:"fits_quota"`
}

type grabSourceOptions struct {
	IP           string
	LanguageCode string
}

type wholeSiteImportedPage struct {
	SourceURL string
	LocalPath string
	HTML      string
}

type wholeSitePageJob struct {
	URL  *url.URL
	HTML string
}

type wholeSitePreviewResult struct {
	PageCount     int
	Resources     []grabResourcePreview
	ImportedPages []wholeSiteImportedPage
	Spider        *pageSpider
}

type nativePickedFile struct {
	Name    string `json:"name"`
	Mime    string `json:"mime"`
	Content string `json:"content"`
}

type nativePickedFilesResponse struct {
	Files []nativePickedFile `json:"files"`
}

type nativeSavedBackupResponse struct {
	Saved    bool   `json:"saved"`
	Canceled bool   `json:"canceled,omitempty"`
	Path     string `json:"path,omitempty"`
}

type Page struct {
	Domain           string
	Path             string
	Title            string
	HTML             string
	ContentKind      string
	Published        int
	NativeFileDialog bool
}

type Revision struct {
	ID        int
	PagePath  string
	HTML      string
	CreatedAt string
	IsActive  int
}

type ManagedFile struct {
	Name          string
	Size          int64
	SizeLabel     string
	AssetPath     string
	PagePath      string
	MimeType      string
	Extension     string
	CreatedAt     string
	CreatedUnix   int64
	IsImage       bool
	AccessMode    string
	Token         string
	ExpiresAt     string
	SingleUseLeft int
	DownloadCount int64
	TokenUseCount int64
}

type DomainAlias struct {
	Domain            string
	VerificationToken string
	TXTVerified       bool
	ARecordVerified   bool
	IsActive          bool
	IsSelected        bool
	LastCheckedAt     string
}

type DomainChrootLocation struct {
	URLPath       string
	DirectoryPath string
}

type directoryListingEntry struct {
	Name      string
	Href      string
	Kind      string
	Icon      string
	SizeLabel string
	TimeLabel string
	IsDir     bool
}

type DomainAutomaticSSLSetting struct {
	Domain            string
	Enabled           bool
	ManuallyDisabled  bool
	Available         bool
	LastCheckedAt     string
	LastFailedAt      string
	LastFailureReason string
	RetryAfter        string
}

type DomainAutomaticSSLStatus struct {
	OverallClass       string
	OverallTextKey     string
	DomainCheckClass   string
	DomainCheckTextKey string
	CertificateClass   string
	CertificateTextKey string
}

type ManagedFileAccess struct {
	AccessMode    string
	Token         string
	ExpiresAt     string
	SingleUseLeft int
	TokenUseCount int64
}

type EmailConfirmation struct {
	Token        string
	Domain       string
	Action       string
	Email        string
	Password     string
	CurrentEmail string
	ReturnPath   string
	LanguageCode string
	ExpiresAt    string
}

type EmailDNSSetupView struct {
	Title       string
	Intro       string
	Explanation string
	DNSIntro    string
	Records     []EmailDNSRecordView
	CopyLabel   string
	AfterText   string
	PlainText   string
}

type EmailDNSRecordView struct {
	Label string
	Value string
}

type PagePasswordRule = dirprotect.Rule

type backupPage struct {
	Path      string `json:"path"`
	Title     string `json:"title"`
	HTML      string `json:"html"`
	Published int    `json:"published"`
}

type backupFileMetadata struct {
	FileName      string `json:"file_name"`
	PagePath      string `json:"page_path"`
	Size          int64  `json:"size"`
	MimeType      string `json:"mime_type"`
	CreatedAt     string `json:"created_at"`
	DownloadCount int64  `json:"download_count"`
}

type backupFileAccessRule struct {
	FileName      string `json:"file_name"`
	AccessMode    string `json:"access_mode"`
	Token         string `json:"token"`
	ExpiresAt     string `json:"expires_at"`
	SingleUseLeft int    `json:"single_use_left"`
	TokenUseCount int64  `json:"token_use_count"`
}

type backupRedirect struct {
	OldPath string `json:"old_path"`
	NewPath string `json:"new_path"`
}

type domainBackup struct {
	Version         int                    `json:"version"`
	ExportedAt      string                 `json:"exported_at"`
	Domain          string                 `json:"domain"`
	Pages           []backupPage           `json:"pages"`
	FileMetadata    []backupFileMetadata   `json:"file_metadata"`
	FileAccessRules []backupFileAccessRule `json:"file_access_rules"`
	Redirects       []backupRedirect       `json:"redirects"`
}

type fileMetadata struct {
	PagePath      string
	Size          int64
	MimeType      string
	CreatedAt     string
	DownloadCount int64
}

type domainStorageUsage struct {
	PageBytes            int64
	PublishedPageBytes   int64
	RevisionBytes        int64
	FileBytes            int64
	PublishedStaticBytes int64
	LimitBytes           int64
}

type authIPFailure struct {
	FailureCount int
	BlockedUntil string
	HardLocked   int
}

type statusCapturingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (writer *statusCapturingResponseWriter) WriteHeader(statusCode int) {
	writer.statusCode = statusCode
	writer.ResponseWriter.WriteHeader(statusCode)
}

func (writer *statusCapturingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := writer.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijack is not supported")
	}
	return hijacker.Hijack()
}

func (writer *statusCapturingResponseWriter) Flush() {
	flusher, ok := writer.ResponseWriter.(http.Flusher)
	if ok {
		flusher.Flush()
	}
}

type domainLogEvent struct {
	Domain     string
	OccurredAt time.Time
	Message    string
}

const domainLogRetentionDays = 5

func (a *App) accessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const (
			colorGreen  = "\033[32m"
			colorBlue   = "\033[34m"
			colorYellow = "\033[33m"
			colorReset  = "\033[0m"
		)
		startedAt := time.Now()
		writer := &statusCapturingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(writer, r)
		contentSource := writer.Header().Get("X-Sitebrush-Source")
		logType := "REQUEST"
		logColor := colorYellow
		if contentSource == "static" {
			logType = "STATIC"
			logColor = colorGreen
		}
		if contentSource == "dynamic" {
			logType = "DYNAMIC"
			logColor = colorBlue
		}
		if contentSource == "" && isLikelyStaticAssetPath(r.URL.Path) {
			logType = "STATIC"
			logColor = colorGreen
		}
		if logType == "STATIC" && !hasSitebrushSessionCookie(r) {
			return
		}
		duration := time.Since(startedAt)
		requestDomain := domainFromRequest(r)
		if strings.TrimSpace(r.URL.RawQuery) == "" {
			log.Printf("%s%s%s method=%s path=%s status=%d remote=%s duration=%s", logColor, logType, colorReset, r.Method, r.URL.Path, writer.statusCode, r.RemoteAddr, duration.String())
			a.writeDomainLog(requestDomain, "%s method=%s path=%s status=%d remote=%s duration=%s", logType, r.Method, r.URL.Path, writer.statusCode, r.RemoteAddr, duration.String())
			return
		}
		log.Printf("%s%s%s method=%s path=%s query=%s status=%d remote=%s duration=%s", logColor, logType, colorReset, r.Method, r.URL.Path, r.URL.RawQuery, writer.statusCode, r.RemoteAddr, duration.String())
		a.writeDomainLog(requestDomain, "%s method=%s path=%s query=%s status=%d remote=%s duration=%s", logType, r.Method, r.URL.Path, r.URL.RawQuery, writer.statusCode, r.RemoteAddr, duration.String())
	})
}

func (a *App) startDomainLogWorker(ctx context.Context) {
	if a.domainLogEvents == nil {
		a.domainLogEvents = make(chan domainLogEvent, 1024)
	}
	go a.runDomainLogWorker(ctx, a.domainLogEvents)
}

func (a *App) writeDomainLog(domain string, format string, args ...any) {
	cleanDomain := normalizeDomainName(domain)
	if cleanDomain == "" {
		cleanDomain = "localhost"
	}
	message := fmt.Sprintf(format, args...)
	if a.domainLogEvents == nil {
		return
	}
	event := domainLogEvent{Domain: cleanDomain, OccurredAt: time.Now().UTC(), Message: message}
	select {
	case a.domainLogEvents <- event:
	default:
		log.Printf("domain log queue is full, skipped log for %s: %s", cleanDomain, message)
	}
}

func (a *App) logDomainEvent(domain string, format string, args ...any) {
	cleanDomain := normalizeDomainName(domain)
	if cleanDomain == "" {
		cleanDomain = "localhost"
	}
	message := fmt.Sprintf(format, args...)
	log.Printf("domain=%s %s", cleanDomain, message)
	a.writeDomainLog(cleanDomain, "%s", message)
}

func (a *App) runDomainLogWorker(ctx context.Context, events <-chan domainLogEvent) {
	lastCleanupByDomain := make(map[string]string)
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-events:
			if event.OccurredAt.IsZero() {
				event.OccurredAt = time.Now().UTC()
			}
			a.appendDomainLogEvent(event)
			cleanupDate := event.OccurredAt.Format("2006-01-02")
			if lastCleanupByDomain[event.Domain] == cleanupDate {
				continue
			}
			lastCleanupByDomain[event.Domain] = cleanupDate
			a.cleanupOldDomainLogs(event.Domain, event.OccurredAt)
		}
	}
}

func (a *App) appendDomainLogEvent(event domainLogEvent) {
	logDir := a.domainLogDir(event.Domain)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		log.Printf("failed to create domain log dir for %s: %v", event.Domain, err)
		return
	}
	logPath := filepath.Join(logDir, event.OccurredAt.Format("2006-01-02")+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("failed to open domain log for %s: %v", event.Domain, err)
		return
	}
	defer logFile.Close()
	if _, err := fmt.Fprintf(logFile, "%s %s\n", event.OccurredAt.Format(time.RFC3339), event.Message); err != nil {
		log.Printf("failed to write domain log for %s: %v", event.Domain, err)
	}
}

func (a *App) cleanupOldDomainLogs(domain string, now time.Time) {
	logDir := a.domainLogDir(domain)
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return
	}
	cutoffDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -domainLogRetentionDays)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		logDate, parseErr := time.Parse("2006-01-02", strings.TrimSuffix(entry.Name(), ".log"))
		if parseErr != nil || !logDate.Before(cutoffDate) {
			continue
		}
		if removeErr := os.Remove(filepath.Join(logDir, entry.Name())); removeErr != nil {
			log.Printf("failed to remove old domain log %s/%s: %v", domain, entry.Name(), removeErr)
		}
	}
}

func (a *App) logProblemEvent(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	log.Printf("PROBLEM %s", message)
	a.appendProblemLogEvent(time.Now().UTC(), message)
}

func (a *App) appendProblemLogEvent(occurredAt time.Time, message string) {
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	logDir := a.problemLogDir()
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		log.Printf("failed to create problem log dir: %v", err)
		return
	}
	logPath := filepath.Join(logDir, occurredAt.Format("2006-01-02")+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("failed to open problem log: %v", err)
		return
	}
	defer logFile.Close()
	if _, err := fmt.Fprintf(logFile, "%s %s\n", occurredAt.Format(time.RFC3339), message); err != nil {
		log.Printf("failed to write problem log: %v", err)
	}
	a.cleanupOldProblemLogs(occurredAt)
}

func (a *App) cleanupOldProblemLogs(now time.Time) {
	logDir := a.problemLogDir()
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return
	}
	cutoffDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -domainLogRetentionDays)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		logDate, parseErr := time.Parse("2006-01-02", strings.TrimSuffix(entry.Name(), ".log"))
		if parseErr != nil || !logDate.Before(cutoffDate) {
			continue
		}
		if removeErr := os.Remove(filepath.Join(logDir, entry.Name())); removeErr != nil {
			log.Printf("failed to remove old problem log %s: %v", entry.Name(), removeErr)
		}
	}
}

type problemLogWriter struct {
	application *App
}

func (writer problemLogWriter) Write(payload []byte) (int, error) {
	message := strings.TrimSpace(string(payload))
	if writer.application != nil && message != "" {
		writer.application.logProblemEvent("%s", message)
	}
	return len(payload), nil
}

func (a *App) analyticsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		writer := &statusCapturingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(writer, r)
		if !shouldRecordAnalyticsRequest(r) {
			return
		}
		contentSource := writer.Header().Get("X-Sitebrush-Source")
		if contentSource == "" && isLikelyStaticAssetPath(r.URL.Path) {
			contentSource = "static"
		}
		if contentSource == "" {
			contentSource = "request"
		}
		event := siteAnalyticsEvent{
			Domain:         a.analyticsEventDomain(r, contentSource),
			Path:           cleanPath(r.URL.Path),
			Query:          r.URL.RawQuery,
			Method:         r.Method,
			StatusCode:     writer.statusCode,
			ContentSource:  contentSource,
			OccurredAt:     startedAt.UTC(),
			Duration:       time.Since(startedAt),
			ClientIP:       clientIPAddress(r),
			RemoteAddress:  r.RemoteAddr,
			UserAgent:      r.UserAgent(),
			Referer:        r.Referer(),
			AcceptLanguage: r.Header.Get("Accept-Language"),
			GeoCountryCode: analyticsGeoCountryCodeFromRequest(r),
			GeoCity:        analyticsGeoCityFromRequest(r),
			GeoSource:      analyticsGeoSourceFromRequest(r),
			IsAdmin:        a.analyticsEventIsAdmin(r, contentSource),
			IsAsset:        isLikelyStaticAssetPath(r.URL.Path),
			IsController:   isSitebrushControllerQuery(r.URL.Query()),
		}
		event.VisitorID = analyticsVisitorID(event.ClientIP, event.UserAgent)
		a.enqueueAnalyticsEvent(event)
	})
}

func shouldRecordAnalyticsRequest(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	query := r.URL.Query()
	for _, skippedFlag := range []string{"analytics", "grab_events", "grab_ws", "publish_events", "captcha"} {
		if _, found := query[skippedFlag]; found {
			return false
		}
	}
	return true
}

func isSitebrushControllerQuery(query url.Values) bool {
	for _, controllerFlag := range []string{
		"save", "grab_preview", "grab_events", "grab_ws", "revision_restore", "revision_delete", "revision_toggle",
		"tree", "native_pick_files", "native_save_backup", "edit", "visual", "text", "editraw", "settings", "properties",
		"backup_download", "backup_import", "profile", "freeze", "publish", "publish_events", "publish_preview", "files",
		"revisions", "login", "register", "email_confirm", "grab", "recover", "captcha", "analytics",
	} {
		if _, found := query[controllerFlag]; found {
			return true
		}
	}
	if strings.TrimSpace(query.Get("delete")) != "" {
		return true
	}
	return false
}

func analyticsVisitorID(clientIP, userAgent string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(clientIP) + "\n" + strings.TrimSpace(userAgent)))
	return hex.EncodeToString(sum[:12])
}

func (a *App) enqueueAnalyticsEvent(event siteAnalyticsEvent) {
	if a.analyticsEvents == nil {
		return
	}
	select {
	case a.analyticsEvents <- event:
	default:
	}
}

func (a *App) analyticsEventDomain(r *http.Request, contentSource string) string {
	if r == nil {
		return "localhost"
	}
	if contentSource == "static" && !hasSitebrushSessionCookie(r) {
		return domainFromContext(r.Context())
	}
	return a.siteDomain(r.Context(), r)
}

func (a *App) analyticsEventIsAdmin(r *http.Request, contentSource string) bool {
	if contentSource == "static" && !hasSitebrushSessionCookie(r) {
		return false
	}
	return a.isAdminRequest(r)
}

func (a *App) startAnalyticsWorkers(ctx context.Context) {
	if a.analyticsEvents == nil {
		return
	}
	go a.runAnalyticsEventWriter(ctx)
}

func (a *App) runAnalyticsEventWriter(ctx context.Context) {
	memoryLimit := a.analyticsMemoryLimit
	if memoryLimit <= 0 {
		memoryLimit = defaultAnalyticsMemoryLimitBytes
	}
	state := newAnalyticsAggregateState(memoryLimit)
	flushInterval := time.Minute
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			a.flushAnalyticsAggregateState(ctx, state)
			return
		case event := <-a.analyticsEvents:
			eventContext := contextWithDomain(ctx, event.Domain)
			event = a.enrichAnalyticsEventGeo(eventContext, event)
			state.record(event)
		case <-ticker.C:
			startedAt := time.Now()
			a.flushAnalyticsAggregateState(ctx, state)
			if time.Since(startedAt) > flushInterval && flushInterval < 30*time.Minute {
				flushInterval *= 2
				if flushInterval > 30*time.Minute {
					flushInterval = 30 * time.Minute
				}
				ticker.Reset(flushInterval)
			}
		}
	}
}

func (a *App) flushAnalyticsAggregateState(ctx context.Context, state *analyticsAggregateState) {
	if state == nil {
		return
	}
	reports := state.reports(time.Now().UTC())
	state.resetAfterFlush()
	for domain, report := range reports {
		if strings.TrimSpace(domain) == "" {
			continue
		}
		reportContext := contextWithDomain(ctx, domain)
		if saveErr := a.saveAnalyticsReport(reportContext, domain, report); saveErr != nil {
			continue
		}
	}
}

type analyticsAggregateState struct {
	memoryLimitBytes int64
	usedBytes        int64
	domains          map[string]*siteAnalyticsAggregate
	systemEvents     map[string][]analyticsCountRow
	disabled         bool
	overloadDomain   string
}

type siteAnalyticsAggregate struct {
	periodStart           time.Time
	periodEnd             time.Time
	totalRequests         int
	pageViews             int
	humanRequests         int
	botRequests           int
	adminRequests         int
	staticRequests        int
	errorCount            int
	totalDurationMS       int64
	visitorSet            map[string]struct{}
	topPages              map[string]int
	trafficSources        map[string]int
	referrers             map[string]int
	countries             map[string]int
	cities                map[string]int
	mapBuckets            map[string]analyticsMapBucket
	devices               map[string]int
	visitorTypes          map[string]int
	botCrawlers           map[string]int
	botReferrers          map[string]int
	browsers              map[string]int
	operatingSystems      map[string]int
	languages             map[string]int
	statusCodes           map[string]int
	hourlyActivity        map[string]int
	dailyActivity         map[string]int
	assets                map[string]int
	errorPaths            map[string]int
	contentSources        map[string]int
	slowPageTotalDuration map[string]int64
	slowPageCounts        map[string]int
	sessionSummary        analyticsSessionSummary
	visitorSessions       map[string]*analyticsVisitorSessionState
}

type analyticsVisitorSessionState struct {
	firstEvent              siteAnalyticsEvent
	lastEvent               siteAnalyticsEvent
	pageCount               int
	sessionIndex            int
	returningVisitorCounted bool
}

func newAnalyticsAggregateState(memoryLimitBytes int64) *analyticsAggregateState {
	return &analyticsAggregateState{
		memoryLimitBytes: memoryLimitBytes,
		domains:          make(map[string]*siteAnalyticsAggregate),
		systemEvents:     make(map[string][]analyticsCountRow),
	}
}

func (state *analyticsAggregateState) record(event siteAnalyticsEvent) {
	if state == nil {
		return
	}
	domain := strings.TrimSpace(event.Domain)
	if domain == "" {
		domain = "localhost"
	}
	if state.disabled {
		return
	}
	aggregate := state.aggregateForDomain(domain)
	aggregate.record(event)
	state.usedBytes += estimateAnalyticsEventAggregateBytes(event)
	if state.memoryLimitBytes > 0 && state.usedBytes > state.memoryLimitBytes {
		state.recordSystemEvent(domain, "analytics overload started", time.Now().UTC())
		state.domains = make(map[string]*siteAnalyticsAggregate)
		state.usedBytes = 0
		state.disabled = true
		state.overloadDomain = domain
	}
}

func estimateAnalyticsEventAggregateBytes(event siteAnalyticsEvent) int64 {
	estimatedBytes := 512
	estimatedBytes += len(event.Path)
	estimatedBytes += len(event.Query)
	estimatedBytes += len(event.UserAgent)
	estimatedBytes += len(event.Referer)
	estimatedBytes += len(event.AcceptLanguage)
	estimatedBytes += len(event.VisitorID)
	estimatedBytes += len(event.GeoCountryCode)
	estimatedBytes += len(event.GeoCity)
	return int64(estimatedBytes)
}

func (state *analyticsAggregateState) reports(generatedAt time.Time) map[string]analyticsPreparedReport {
	if state.disabled {
		domain := state.overloadDomain
		if domain == "" {
			domain = "localhost"
		}
		state.recordSystemEvent(domain, "analytics overload ended", generatedAt)
	}
	reports := make(map[string]analyticsPreparedReport, len(state.domains)+len(state.systemEvents))
	for domain, aggregate := range state.domains {
		report := aggregate.report(generatedAt)
		if systemEvents := state.systemEvents[domain]; len(systemEvents) > 0 {
			report.SystemEvents = append(report.SystemEvents, systemEvents...)
		}
		reports[domain] = report
	}
	for domain, systemEvents := range state.systemEvents {
		if _, found := reports[domain]; found {
			continue
		}
		reports[domain] = analyticsPreparedReport{
			GeneratedAt:  generatedAt.Format(time.RFC3339),
			PeriodStart:  generatedAt.Format(time.RFC3339),
			PeriodEnd:    generatedAt.Format(time.RFC3339),
			SystemEvents: append([]analyticsCountRow(nil), systemEvents...),
		}
	}
	return reports
}

func (state *analyticsAggregateState) resetAfterFlush() {
	if state == nil {
		return
	}
	state.domains = make(map[string]*siteAnalyticsAggregate)
	state.systemEvents = make(map[string][]analyticsCountRow)
	state.usedBytes = 0
	state.disabled = false
	state.overloadDomain = ""
}

func (state *analyticsAggregateState) aggregateForDomain(domain string) *siteAnalyticsAggregate {
	aggregate := state.domains[domain]
	if aggregate == nil {
		aggregate = newSiteAnalyticsAggregate()
		state.domains[domain] = aggregate
		state.usedBytes += int64(len(domain) + 256)
	}
	return aggregate
}

func (state *analyticsAggregateState) recordSystemEvent(domain, label string, occurredAt time.Time) {
	if domain == "" {
		domain = "localhost"
	}
	state.systemEvents[domain] = append(state.systemEvents[domain], analyticsCountRow{
		Label:  label,
		Count:  1,
		Value:  occurredAt.Format(time.RFC3339),
		Detail: "analytics",
	})
}

func newSiteAnalyticsAggregate() *siteAnalyticsAggregate {
	return &siteAnalyticsAggregate{
		visitorSet:            make(map[string]struct{}),
		topPages:              make(map[string]int),
		trafficSources:        make(map[string]int),
		referrers:             make(map[string]int),
		countries:             make(map[string]int),
		cities:                make(map[string]int),
		mapBuckets:            make(map[string]analyticsMapBucket),
		devices:               make(map[string]int),
		visitorTypes:          make(map[string]int),
		botCrawlers:           make(map[string]int),
		botReferrers:          make(map[string]int),
		browsers:              make(map[string]int),
		operatingSystems:      make(map[string]int),
		languages:             make(map[string]int),
		statusCodes:           make(map[string]int),
		hourlyActivity:        make(map[string]int),
		dailyActivity:         make(map[string]int),
		assets:                make(map[string]int),
		errorPaths:            make(map[string]int),
		contentSources:        make(map[string]int),
		slowPageTotalDuration: make(map[string]int64),
		slowPageCounts:        make(map[string]int),
		sessionSummary: analyticsSessionSummary{
			EntryPages:         make(map[string]int),
			ExitPages:          make(map[string]int),
			EntryHours:         make(map[string]int),
			ReturningSources:   make(map[string]int),
			ReturningReferrers: make(map[string]int),
			BotReturnSources:   make(map[string]int),
		},
		visitorSessions: make(map[string]*analyticsVisitorSessionState),
	}
}

func (aggregate *siteAnalyticsAggregate) record(event siteAnalyticsEvent) {
	if aggregate.periodStart.IsZero() || event.OccurredAt.Before(aggregate.periodStart) {
		aggregate.periodStart = event.OccurredAt
	}
	if event.OccurredAt.After(aggregate.periodEnd) {
		aggregate.periodEnd = event.OccurredAt
	}
	aggregate.totalRequests++
	aggregate.totalDurationMS += event.Duration.Milliseconds()
	if _, found := aggregate.visitorSet[event.VisitorID]; !found && strings.TrimSpace(event.VisitorID) != "" {
		aggregate.visitorSet[event.VisitorID] = struct{}{}
	}
	if analyticsIsBot(event.UserAgent) {
		aggregate.botRequests++
	} else {
		aggregate.humanRequests++
	}
	incrementAnalyticsCounter(aggregate.contentSources, analyticsContentSourceLabel(event.ContentSource))
	incrementAnalyticsCounter(aggregate.statusCodes, strconv.Itoa(event.StatusCode))
	incrementAnalyticsCounter(aggregate.hourlyActivity, event.OccurredAt.Format("15:00"))
	incrementAnalyticsCounter(aggregate.dailyActivity, event.OccurredAt.Format("2006-01-02"))
	if event.IsAdmin {
		aggregate.adminRequests++
	}
	if event.ContentSource == "static" || event.IsAsset {
		aggregate.staticRequests++
		incrementAnalyticsCounter(aggregate.assets, event.Path)
	}
	if event.StatusCode >= 400 {
		aggregate.errorCount++
		incrementAnalyticsCounter(aggregate.errorPaths, event.Path+" "+strconv.Itoa(event.StatusCode))
	}
	if event.IsController || event.IsAsset || event.Method != http.MethodGet {
		return
	}
	aggregate.pageViews++
	incrementAnalyticsCounter(aggregate.topPages, event.Path)
	incrementAnalyticsCounter(aggregate.trafficSources, classifyAnalyticsTrafficSource(event.Referer))
	if host := analyticsRefererHost(event.Referer); host != "" {
		incrementAnalyticsCounter(aggregate.referrers, host)
	}
	location := analyticsLocationForEvent(event)
	incrementAnalyticsCounter(aggregate.countries, location.Country)
	incrementAnalyticsCounter(aggregate.cities, location.CityLabel)
	if location.hasCoordinates() {
		addAnalyticsMapBucket(aggregate.mapBuckets, location)
	}
	incrementAnalyticsCounter(aggregate.devices, analyticsDeviceClass(event.UserAgent))
	if analyticsIsBot(event.UserAgent) {
		incrementAnalyticsCounter(aggregate.visitorTypes, "bot")
		incrementAnalyticsCounter(aggregate.botCrawlers, analyticsBotCrawlerName(event.UserAgent))
		if host := analyticsRefererHost(event.Referer); host != "" {
			incrementAnalyticsCounter(aggregate.botReferrers, host)
		}
	} else {
		incrementAnalyticsCounter(aggregate.visitorTypes, "human")
	}
	incrementAnalyticsCounter(aggregate.browsers, analyticsBrowserName(event.UserAgent))
	incrementAnalyticsCounter(aggregate.operatingSystems, analyticsOSName(event.UserAgent))
	incrementAnalyticsCounter(aggregate.languages, analyticsLanguageLabel(event.AcceptLanguage))
	aggregate.slowPageTotalDuration[event.Path] += event.Duration.Milliseconds()
	aggregate.slowPageCounts[event.Path]++
	aggregate.recordSessionEvent(event)
}

func (aggregate *siteAnalyticsAggregate) recordSessionEvent(event siteAnalyticsEvent) {
	visitorID := event.VisitorID
	if strings.TrimSpace(visitorID) == "" {
		visitorID = analyticsVisitorID(event.ClientIP, event.UserAgent)
	}
	state := aggregate.visitorSessions[visitorID]
	if state == nil {
		state = &analyticsVisitorSessionState{firstEvent: event, lastEvent: event, pageCount: 1}
		aggregate.visitorSessions[visitorID] = state
		return
	}
	if event.OccurredAt.Sub(state.lastEvent.OccurredAt) > 30*time.Minute {
		aggregate.flushVisitorSession(state)
		state.firstEvent = event
		state.lastEvent = event
		state.pageCount = 1
		return
	}
	state.lastEvent = event
	state.pageCount++
}

func (aggregate *siteAnalyticsAggregate) report(generatedAt time.Time) analyticsPreparedReport {
	sessionSummary := cloneAnalyticsSessionSummary(aggregate.sessionSummary)
	for _, visitorSession := range aggregate.visitorSessions {
		aggregate.addVisitorSessionToSummary(&sessionSummary, visitorSession)
		if visitorSession.sessionIndex > 0 && !visitorSession.returningVisitorCounted {
			sessionSummary.ReturningVisitors++
		}
	}
	report := analyticsPreparedReport{
		GeneratedAt:       generatedAt.Format(time.RFC3339),
		PeriodStart:       generatedAt.Format(time.RFC3339),
		PeriodEnd:         generatedAt.Format(time.RFC3339),
		TotalRequests:     aggregate.totalRequests,
		PageViews:         aggregate.pageViews,
		UniqueVisitors:    len(aggregate.visitorSet),
		HumanRequests:     aggregate.humanRequests,
		BotRequests:       aggregate.botRequests,
		Sessions:          sessionSummary.SessionCount,
		ReturningVisitors: sessionSummary.ReturningVisitors,
		ReturnVisits:      sessionSummary.ReturnVisits,
		ErrorCount:        aggregate.errorCount,
		AdminRequests:     aggregate.adminRequests,
		StaticRequests:    aggregate.staticRequests,
	}
	if !aggregate.periodStart.IsZero() {
		report.PeriodStart = aggregate.periodStart.Format(time.RFC3339)
	}
	if !aggregate.periodEnd.IsZero() {
		report.PeriodEnd = aggregate.periodEnd.Format(time.RFC3339)
	}
	if aggregate.totalRequests > 0 {
		report.AverageDurationMS = aggregate.totalDurationMS / int64(aggregate.totalRequests)
	}
	if sessionSummary.SessionCount > 0 {
		report.BounceRate = float64(sessionSummary.BouncedSessions) / float64(sessionSummary.SessionCount) * 100
	}
	report.TopPages = sortedAnalyticsRows(aggregate.topPages, 12, report.PageViews)
	report.EntryPages = sortedAnalyticsRows(sessionSummary.EntryPages, 10, sessionSummary.SessionCount)
	report.ExitPages = sortedAnalyticsRows(sessionSummary.ExitPages, 10, sessionSummary.SessionCount)
	report.TrafficSources = sortedAnalyticsRows(aggregate.trafficSources, 10, report.PageViews)
	report.Referrers = sortedAnalyticsRows(aggregate.referrers, 10, report.PageViews)
	report.ReturningSources = sortedAnalyticsRows(sessionSummary.ReturningSources, 10, report.ReturnVisits)
	report.ReturningReferrers = sortedAnalyticsRows(sessionSummary.ReturningReferrers, 10, report.ReturnVisits)
	report.Countries = sortedAnalyticsRows(aggregate.countries, 10, report.PageViews)
	report.Cities = sortedAnalyticsRows(aggregate.cities, 10, report.PageViews)
	report.EntryHours = sortedAnalyticsRows(sessionSummary.EntryHours, 24, sessionSummary.SessionCount)
	report.MapPoints = analyticsMapPoints(aggregate.mapBuckets, report.PageViews)
	report.Devices = sortedAnalyticsRows(aggregate.devices, 10, report.PageViews)
	report.VisitorTypes = sortedAnalyticsRows(aggregate.visitorTypes, 10, report.PageViews)
	report.BotCrawlers = sortedAnalyticsRows(aggregate.botCrawlers, 10, report.PageViews)
	report.BotReturnSources = sortedAnalyticsRows(sessionSummary.BotReturnSources, 10, report.ReturnVisits)
	report.BotReferrers = sortedAnalyticsRows(aggregate.botReferrers, 10, report.PageViews)
	report.Browsers = sortedAnalyticsRows(aggregate.browsers, 10, report.PageViews)
	report.OperatingSystems = sortedAnalyticsRows(aggregate.operatingSystems, 10, report.PageViews)
	report.Languages = sortedAnalyticsRows(aggregate.languages, 10, report.PageViews)
	report.StatusCodes = sortedAnalyticsRows(aggregate.statusCodes, 10, report.TotalRequests)
	report.HourlyActivity = sortedAnalyticsRows(aggregate.hourlyActivity, 24, report.TotalRequests)
	report.DailyActivity = sortedAnalyticsRows(aggregate.dailyActivity, 14, report.TotalRequests)
	report.TopAssets = sortedAnalyticsRows(aggregate.assets, 10, report.StaticRequests)
	report.ErrorPaths = sortedAnalyticsRows(aggregate.errorPaths, 10, report.ErrorCount)
	report.ContentSources = sortedAnalyticsRows(aggregate.contentSources, 10, report.TotalRequests)
	report.SlowPages = analyticsSlowPageRows(aggregate.slowPageTotalDuration, aggregate.slowPageCounts, 10)
	return report
}

func cloneAnalyticsSessionSummary(source analyticsSessionSummary) analyticsSessionSummary {
	return analyticsSessionSummary{
		EntryPages:         cloneAnalyticsCounter(source.EntryPages),
		ExitPages:          cloneAnalyticsCounter(source.ExitPages),
		EntryHours:         cloneAnalyticsCounter(source.EntryHours),
		ReturningSources:   cloneAnalyticsCounter(source.ReturningSources),
		ReturningReferrers: cloneAnalyticsCounter(source.ReturningReferrers),
		BotReturnSources:   cloneAnalyticsCounter(source.BotReturnSources),
		SessionCount:       source.SessionCount,
		BouncedSessions:    source.BouncedSessions,
		ReturningVisitors:  source.ReturningVisitors,
		ReturnVisits:       source.ReturnVisits,
	}
}

func cloneAnalyticsCounter(source map[string]int) map[string]int {
	cloned := make(map[string]int, len(source))
	for key, count := range source {
		cloned[key] = count
	}
	return cloned
}

func (aggregate *siteAnalyticsAggregate) flushVisitorSession(visitorSession *analyticsVisitorSessionState) {
	aggregate.addVisitorSessionToSummary(&aggregate.sessionSummary, visitorSession)
	if visitorSession.sessionIndex > 0 && !visitorSession.returningVisitorCounted {
		aggregate.sessionSummary.ReturningVisitors++
		visitorSession.returningVisitorCounted = true
	}
	visitorSession.sessionIndex++
}

func (aggregate *siteAnalyticsAggregate) addVisitorSessionToSummary(summary *analyticsSessionSummary, visitorSession *analyticsVisitorSessionState) {
	if visitorSession == nil || visitorSession.pageCount == 0 {
		return
	}
	summary.SessionCount++
	firstEvent := visitorSession.firstEvent
	lastEvent := visitorSession.lastEvent
	summary.EntryPages[firstEvent.Path]++
	summary.ExitPages[lastEvent.Path]++
	summary.EntryHours[firstEvent.OccurredAt.Format("15:00")]++
	if visitorSession.pageCount == 1 {
		summary.BouncedSessions++
	}
	if visitorSession.sessionIndex > 0 {
		summary.ReturnVisits++
		source := classifyAnalyticsTrafficSource(firstEvent.Referer)
		if analyticsIsBot(firstEvent.UserAgent) {
			summary.BotReturnSources[source]++
		} else {
			summary.ReturningSources[source]++
			if host := analyticsRefererHost(firstEvent.Referer); host != "" {
				summary.ReturningReferrers[host]++
			}
		}
	}
}

func (aggregate *siteAnalyticsAggregate) estimatedBytes() int64 {
	total := int64(1024)
	total += estimateAnalyticsStringSetBytes(aggregate.visitorSet)
	total += estimateAnalyticsCounterBytes(aggregate.topPages)
	total += estimateAnalyticsCounterBytes(aggregate.trafficSources)
	total += estimateAnalyticsCounterBytes(aggregate.referrers)
	total += estimateAnalyticsCounterBytes(aggregate.countries)
	total += estimateAnalyticsCounterBytes(aggregate.cities)
	total += int64(len(aggregate.mapBuckets) * 192)
	total += estimateAnalyticsCounterBytes(aggregate.devices)
	total += estimateAnalyticsCounterBytes(aggregate.visitorTypes)
	total += estimateAnalyticsCounterBytes(aggregate.botCrawlers)
	total += estimateAnalyticsCounterBytes(aggregate.botReferrers)
	total += estimateAnalyticsCounterBytes(aggregate.browsers)
	total += estimateAnalyticsCounterBytes(aggregate.operatingSystems)
	total += estimateAnalyticsCounterBytes(aggregate.languages)
	total += estimateAnalyticsCounterBytes(aggregate.statusCodes)
	total += estimateAnalyticsCounterBytes(aggregate.hourlyActivity)
	total += estimateAnalyticsCounterBytes(aggregate.dailyActivity)
	total += estimateAnalyticsCounterBytes(aggregate.assets)
	total += estimateAnalyticsCounterBytes(aggregate.errorPaths)
	total += estimateAnalyticsCounterBytes(aggregate.contentSources)
	total += estimateAnalyticsDurationCounterBytes(aggregate.slowPageTotalDuration)
	total += estimateAnalyticsCounterBytes(aggregate.slowPageCounts)
	total += int64(len(aggregate.visitorSessions) * 512)
	total += estimateAnalyticsCounterBytes(aggregate.sessionSummary.EntryPages)
	total += estimateAnalyticsCounterBytes(aggregate.sessionSummary.ExitPages)
	total += estimateAnalyticsCounterBytes(aggregate.sessionSummary.EntryHours)
	total += estimateAnalyticsCounterBytes(aggregate.sessionSummary.ReturningSources)
	total += estimateAnalyticsCounterBytes(aggregate.sessionSummary.ReturningReferrers)
	total += estimateAnalyticsCounterBytes(aggregate.sessionSummary.BotReturnSources)
	return total
}

func incrementAnalyticsCounter(values map[string]int, key string) {
	cleanKey := strings.TrimSpace(key)
	if cleanKey == "" {
		return
	}
	values[cleanKey]++
}

func estimateAnalyticsCounterBytes(values map[string]int) int64 {
	var total int64
	for key := range values {
		total += int64(len(key) + 96)
	}
	return total
}

func estimateAnalyticsDurationCounterBytes(values map[string]int64) int64 {
	var total int64
	for key := range values {
		total += int64(len(key) + 96)
	}
	return total
}

func estimateAnalyticsStringSetBytes(values map[string]struct{}) int64 {
	var total int64
	for key := range values {
		total += int64(len(key) + 96)
	}
	return total
}

func (a *App) runAnalyticsReportBuilder(ctx context.Context) {
	a.rebuildAnalyticsReports(ctx)
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.rebuildAnalyticsReports(ctx)
		}
	}
}

func (a *App) insertAnalyticsEvent(ctx context.Context, event siteAnalyticsEvent) error {
	if strings.TrimSpace(event.Domain) == "" {
		event.Domain = "localhost"
	}
	if strings.TrimSpace(event.Path) == "" {
		event.Path = "/"
	}
	_, err := a.db.ExecContext(ctx, `INSERT INTO analytics_events(domain,path,query,method,status_code,content_source,occurred_at,duration_ms,client_ip,remote_addr,user_agent,referer,accept_language,geo_country_code,geo_city,geo_latitude,geo_longitude,geo_source,visitor_id,is_admin,is_asset,is_controller) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		event.Domain,
		event.Path,
		event.Query,
		event.Method,
		event.StatusCode,
		event.ContentSource,
		event.OccurredAt.Format(time.RFC3339Nano),
		event.Duration.Milliseconds(),
		event.ClientIP,
		event.RemoteAddress,
		event.UserAgent,
		event.Referer,
		event.AcceptLanguage,
		event.GeoCountryCode,
		event.GeoCity,
		event.GeoLatitude,
		event.GeoLongitude,
		event.GeoSource,
		event.VisitorID,
		boolToInt(event.IsAdmin),
		boolToInt(event.IsAsset),
		boolToInt(event.IsController),
	)
	return err
}

func (a *App) enrichAnalyticsEventGeo(ctx context.Context, event siteAnalyticsEvent) siteAnalyticsEvent {
	if a.geoIP == nil || strings.TrimSpace(event.ClientIP) == "" {
		return event
	}
	location, found := a.geoIP.Lookup(ctx, event.ClientIP)
	if !found {
		return event
	}
	event.GeoCountryCode = location.CountryCode
	event.GeoCity = location.City
	event.GeoLatitude = location.Latitude
	event.GeoLongitude = location.Longitude
	event.GeoSource = location.Source
	return event
}

func (a *App) rebuildAnalyticsReports(ctx context.Context) {
	for _, domain := range a.analyticsDomains(ctx) {
		report, err := a.buildAnalyticsReport(ctx, domain)
		if err != nil {
			log.Printf("analytics report build failed domain=%s: %v", domain, err)
			continue
		}
		if err := a.saveAnalyticsReport(ctx, domain, report); err != nil {
			log.Printf("analytics report save failed domain=%s: %v", domain, err)
		}
	}
}

func (a *App) analyticsDomains(ctx context.Context) []string {
	rows, err := a.db.QueryContext(ctx, `SELECT domain FROM analytics_events GROUP BY domain UNION SELECT domain FROM users GROUP BY domain UNION SELECT domain FROM pages GROUP BY domain UNION SELECT domain FROM published_pages GROUP BY domain ORDER BY domain`)
	if err != nil {
		return []string{}
	}
	defer rows.Close()
	domains := make([]string, 0, 8)
	for rows.Next() {
		var domain string
		if scanErr := rows.Scan(&domain); scanErr != nil {
			continue
		}
		domain = strings.TrimSpace(domain)
		if domain != "" {
			domains = append(domains, domain)
		}
	}
	return domains
}

func (a *App) buildAnalyticsReport(ctx context.Context, domain string) (analyticsPreparedReport, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT path,query,method,status_code,content_source,occurred_at,duration_ms,client_ip,user_agent,referer,accept_language,geo_country_code,geo_city,geo_latitude,geo_longitude,geo_source,visitor_id,is_admin,is_asset,is_controller FROM analytics_events WHERE domain=? ORDER BY occurred_at ASC`, domain)
	if err != nil {
		return analyticsPreparedReport{}, err
	}
	defer rows.Close()
	events := make([]siteAnalyticsEvent, 0, 512)
	for rows.Next() {
		var event siteAnalyticsEvent
		var occurredAt string
		var durationMS int64
		var isAdmin, isAsset, isController int
		if scanErr := rows.Scan(&event.Path, &event.Query, &event.Method, &event.StatusCode, &event.ContentSource, &occurredAt, &durationMS, &event.ClientIP, &event.UserAgent, &event.Referer, &event.AcceptLanguage, &event.GeoCountryCode, &event.GeoCity, &event.GeoLatitude, &event.GeoLongitude, &event.GeoSource, &event.VisitorID, &isAdmin, &isAsset, &isController); scanErr != nil {
			continue
		}
		event.Domain = domain
		event.OccurredAt = parseAnalyticsTime(occurredAt)
		event.Duration = time.Duration(durationMS) * time.Millisecond
		event.IsAdmin = isAdmin == 1
		event.IsAsset = isAsset == 1
		event.IsController = isController == 1
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return analyticsPreparedReport{}, err
	}
	return buildAnalyticsReportFromEvents(events, time.Now().UTC()), nil
}

func buildAnalyticsReportFromEvents(events []siteAnalyticsEvent, generatedAt time.Time) analyticsPreparedReport {
	report := analyticsPreparedReport{GeneratedAt: generatedAt.Format(time.RFC3339), PeriodStart: generatedAt.Format(time.RFC3339), PeriodEnd: generatedAt.Format(time.RFC3339)}
	if len(events) == 0 {
		return report
	}
	report.PeriodStart = events[0].OccurredAt.Format(time.RFC3339)
	report.PeriodEnd = events[len(events)-1].OccurredAt.Format(time.RFC3339)
	report.TotalRequests = len(events)
	visitorSet := make(map[string]struct{})
	topPages := make(map[string]int)
	trafficSources := make(map[string]int)
	referrers := make(map[string]int)
	countries := make(map[string]int)
	cities := make(map[string]int)
	mapBuckets := make(map[string]analyticsMapBucket)
	devices := make(map[string]int)
	visitorTypes := make(map[string]int)
	botCrawlers := make(map[string]int)
	botReferrers := make(map[string]int)
	browsers := make(map[string]int)
	operatingSystems := make(map[string]int)
	languages := make(map[string]int)
	statusCodes := make(map[string]int)
	hourlyActivity := make(map[string]int)
	dailyActivity := make(map[string]int)
	assets := make(map[string]int)
	errorPaths := make(map[string]int)
	contentSources := make(map[string]int)
	slowPageTotalDuration := make(map[string]int64)
	slowPageCounts := make(map[string]int)
	sessionEvents := make(map[string][]siteAnalyticsEvent)
	var totalDuration int64
	for _, event := range events {
		visitorSet[event.VisitorID] = struct{}{}
		totalDuration += event.Duration.Milliseconds()
		if analyticsIsBot(event.UserAgent) {
			report.BotRequests++
		} else {
			report.HumanRequests++
		}
		contentSources[analyticsContentSourceLabel(event.ContentSource)]++
		statusCodes[strconv.Itoa(event.StatusCode)]++
		hourlyActivity[event.OccurredAt.Format("15:00")]++
		dailyActivity[event.OccurredAt.Format("2006-01-02")]++
		if event.IsAdmin {
			report.AdminRequests++
		}
		if event.ContentSource == "static" || event.IsAsset {
			report.StaticRequests++
			assets[event.Path]++
		}
		if event.StatusCode >= 400 {
			report.ErrorCount++
			errorPaths[event.Path+" "+strconv.Itoa(event.StatusCode)]++
		}
		if event.IsController || event.IsAsset || event.Method != http.MethodGet {
			continue
		}
		report.PageViews++
		topPages[event.Path]++
		trafficSources[classifyAnalyticsTrafficSource(event.Referer)]++
		if host := analyticsRefererHost(event.Referer); host != "" {
			referrers[host]++
		}
		location := analyticsLocationForEvent(event)
		countries[location.Country]++
		cities[location.CityLabel]++
		if location.hasCoordinates() {
			addAnalyticsMapBucket(mapBuckets, location)
		}
		devices[analyticsDeviceClass(event.UserAgent)]++
		if analyticsIsBot(event.UserAgent) {
			visitorTypes["bot"]++
			botCrawlers[analyticsBotCrawlerName(event.UserAgent)]++
			if host := analyticsRefererHost(event.Referer); host != "" {
				botReferrers[host]++
			}
		} else {
			visitorTypes["human"]++
		}
		browsers[analyticsBrowserName(event.UserAgent)]++
		operatingSystems[analyticsOSName(event.UserAgent)]++
		languages[analyticsLanguageLabel(event.AcceptLanguage)]++
		slowPageTotalDuration[event.Path] += event.Duration.Milliseconds()
		slowPageCounts[event.Path]++
		sessionEvents[event.VisitorID] = append(sessionEvents[event.VisitorID], event)
	}
	report.UniqueVisitors = len(visitorSet)
	if len(events) > 0 {
		report.AverageDurationMS = totalDuration / int64(len(events))
	}
	sessionSummary := analyticsSessionPageStats(sessionEvents)
	report.Sessions = sessionSummary.SessionCount
	report.ReturningVisitors = sessionSummary.ReturningVisitors
	report.ReturnVisits = sessionSummary.ReturnVisits
	if sessionSummary.SessionCount > 0 {
		report.BounceRate = float64(sessionSummary.BouncedSessions) / float64(sessionSummary.SessionCount) * 100
	}
	report.TopPages = sortedAnalyticsRows(topPages, 12, report.PageViews)
	report.EntryPages = sortedAnalyticsRows(sessionSummary.EntryPages, 10, sessionSummary.SessionCount)
	report.ExitPages = sortedAnalyticsRows(sessionSummary.ExitPages, 10, sessionSummary.SessionCount)
	report.TrafficSources = sortedAnalyticsRows(trafficSources, 10, report.PageViews)
	report.Referrers = sortedAnalyticsRows(referrers, 10, report.PageViews)
	report.ReturningSources = sortedAnalyticsRows(sessionSummary.ReturningSources, 10, report.ReturnVisits)
	report.ReturningReferrers = sortedAnalyticsRows(sessionSummary.ReturningReferrers, 10, report.ReturnVisits)
	report.Countries = sortedAnalyticsRows(countries, 10, report.PageViews)
	report.Cities = sortedAnalyticsRows(cities, 10, report.PageViews)
	report.EntryHours = sortedAnalyticsRows(sessionSummary.EntryHours, 24, sessionSummary.SessionCount)
	report.MapPoints = analyticsMapPoints(mapBuckets, report.PageViews)
	report.Devices = sortedAnalyticsRows(devices, 10, report.PageViews)
	report.VisitorTypes = sortedAnalyticsRows(visitorTypes, 10, report.PageViews)
	report.BotCrawlers = sortedAnalyticsRows(botCrawlers, 10, report.PageViews)
	report.BotReturnSources = sortedAnalyticsRows(sessionSummary.BotReturnSources, 10, report.ReturnVisits)
	report.BotReferrers = sortedAnalyticsRows(botReferrers, 10, report.PageViews)
	report.Browsers = sortedAnalyticsRows(browsers, 10, report.PageViews)
	report.OperatingSystems = sortedAnalyticsRows(operatingSystems, 10, report.PageViews)
	report.Languages = sortedAnalyticsRows(languages, 10, report.PageViews)
	report.StatusCodes = sortedAnalyticsRows(statusCodes, 10, report.TotalRequests)
	report.HourlyActivity = sortedAnalyticsRows(hourlyActivity, 24, report.TotalRequests)
	report.DailyActivity = sortedAnalyticsRows(dailyActivity, 14, report.TotalRequests)
	report.TopAssets = sortedAnalyticsRows(assets, 10, report.StaticRequests)
	report.ErrorPaths = sortedAnalyticsRows(errorPaths, 10, report.ErrorCount)
	report.ContentSources = sortedAnalyticsRows(contentSources, 10, report.TotalRequests)
	report.SlowPages = analyticsSlowPageRows(slowPageTotalDuration, slowPageCounts, 10)
	return report
}

type analyticsSessionSummary struct {
	EntryPages         map[string]int
	ExitPages          map[string]int
	EntryHours         map[string]int
	ReturningSources   map[string]int
	ReturningReferrers map[string]int
	BotReturnSources   map[string]int
	SessionCount       int
	BouncedSessions    int
	ReturningVisitors  int
	ReturnVisits       int
}

func analyticsSessionPageStats(eventsByVisitor map[string][]siteAnalyticsEvent) analyticsSessionSummary {
	summary := analyticsSessionSummary{
		EntryPages:         make(map[string]int),
		ExitPages:          make(map[string]int),
		EntryHours:         make(map[string]int),
		ReturningSources:   make(map[string]int),
		ReturningReferrers: make(map[string]int),
		BotReturnSources:   make(map[string]int),
	}
	for _, visitorEvents := range eventsByVisitor {
		sort.Slice(visitorEvents, func(i, j int) bool { return visitorEvents[i].OccurredAt.Before(visitorEvents[j].OccurredAt) })
		sessionEvents := make([]siteAnalyticsEvent, 0, 8)
		var previousEvent time.Time
		sessionIndex := 0
		visitorReturned := false
		flushSession := func() {
			if len(sessionEvents) == 0 {
				return
			}
			summary.SessionCount++
			firstEvent := sessionEvents[0]
			lastEvent := sessionEvents[len(sessionEvents)-1]
			summary.EntryPages[firstEvent.Path]++
			summary.ExitPages[lastEvent.Path]++
			summary.EntryHours[firstEvent.OccurredAt.Format("15:00")]++
			if len(sessionEvents) == 1 {
				summary.BouncedSessions++
			}
			if sessionIndex > 0 {
				summary.ReturnVisits++
				visitorReturned = true
				source := classifyAnalyticsTrafficSource(firstEvent.Referer)
				if analyticsIsBot(firstEvent.UserAgent) {
					summary.BotReturnSources[source]++
				} else {
					summary.ReturningSources[source]++
					if host := analyticsRefererHost(firstEvent.Referer); host != "" {
						summary.ReturningReferrers[host]++
					}
				}
			}
			sessionIndex++
			sessionEvents = sessionEvents[:0]
		}
		for _, event := range visitorEvents {
			if !previousEvent.IsZero() && event.OccurredAt.Sub(previousEvent) > 30*time.Minute {
				flushSession()
			}
			sessionEvents = append(sessionEvents, event)
			previousEvent = event.OccurredAt
		}
		flushSession()
		if visitorReturned {
			summary.ReturningVisitors++
		}
	}
	return summary
}

func sortedAnalyticsRows(values map[string]int, limit int, total int) []analyticsCountRow {
	rows := make([]analyticsCountRow, 0, len(values))
	for label, count := range values {
		if strings.TrimSpace(label) == "" || count == 0 {
			continue
		}
		rows = append(rows, analyticsCountRow{Label: label, Count: count, Value: analyticsPercent(count, total)})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count == rows[j].Count {
			return rows[i].Label < rows[j].Label
		}
		return rows[i].Count > rows[j].Count
	})
	if limit > 0 && len(rows) > limit {
		return rows[:limit]
	}
	return rows
}

func analyticsSlowPageRows(totalDuration map[string]int64, counts map[string]int, limit int) []analyticsCountRow {
	rows := make([]analyticsCountRow, 0, len(totalDuration))
	for pagePath, duration := range totalDuration {
		count := counts[pagePath]
		if count == 0 {
			continue
		}
		rows = append(rows, analyticsCountRow{Label: pagePath, Count: int(duration / int64(count)), Value: formatDurationMS(duration / int64(count)), Detail: strconv.Itoa(count)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Count > rows[j].Count })
	if limit > 0 && len(rows) > limit {
		return rows[:limit]
	}
	return rows
}

func (a *App) saveAnalyticsReport(ctx context.Context, domain string, report analyticsPreparedReport) error {
	reportBytes, err := json.Marshal(report)
	if err != nil {
		return err
	}
	_, err = a.db.ExecContext(ctx, `INSERT INTO analytics_reports(domain,generated_at,period_start,period_end,event_count,report_json) VALUES(?,?,?,?,?,?) ON CONFLICT(domain) DO UPDATE SET generated_at=excluded.generated_at,period_start=excluded.period_start,period_end=excluded.period_end,event_count=excluded.event_count,report_json=excluded.report_json`,
		domain, report.GeneratedAt, report.PeriodStart, report.PeriodEnd, report.TotalRequests, string(reportBytes))
	return err
}

func (a *App) loadAnalyticsReport(ctx context.Context, domain string) (analyticsPreparedReport, bool) {
	var reportJSON string
	err := a.db.QueryRowContext(ctx, `SELECT report_json FROM analytics_reports WHERE domain=?`, domain).Scan(&reportJSON)
	if err != nil || strings.TrimSpace(reportJSON) == "" {
		return analyticsPreparedReport{}, false
	}
	var report analyticsPreparedReport
	if json.Unmarshal([]byte(reportJSON), &report) != nil {
		return analyticsPreparedReport{}, false
	}
	return report, true
}

func parseAnalyticsTime(rawTime string) time.Time {
	parsedTime, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(rawTime))
	if err == nil {
		return parsedTime
	}
	parsedTime, err = time.Parse(time.RFC3339, strings.TrimSpace(rawTime))
	if err == nil {
		return parsedTime
	}
	return time.Time{}
}

func analyticsContentSourceLabel(contentSource string) string {
	switch strings.TrimSpace(contentSource) {
	case "dynamic":
		return "dynamic"
	case "static":
		return "static"
	default:
		return "other"
	}
}

func classifyAnalyticsTrafficSource(rawReferer string) string {
	host := analyticsRefererHost(rawReferer)
	if host == "" {
		return "direct"
	}
	searchHosts := []string{"google.", "bing.", "yahoo.", "duckduckgo.", "yandex.", "baidu."}
	for _, searchHost := range searchHosts {
		if strings.Contains(host, searchHost) {
			return "organic search"
		}
	}
	socialHosts := []string{"facebook.", "instagram.", "t.co", "twitter.", "x.com", "linkedin.", "vk.", "tiktok.", "reddit."}
	for _, socialHost := range socialHosts {
		if strings.Contains(host, socialHost) {
			return "social"
		}
	}
	return "referral"
}

func analyticsRefererHost(rawReferer string) string {
	parsedURL, err := url.Parse(strings.TrimSpace(rawReferer))
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(parsedURL.Hostname(), "www."))
}

func analyticsDeviceClass(userAgent string) string {
	loweredAgent := strings.ToLower(userAgent)
	switch {
	case analyticsIsBot(userAgent):
		return "bot"
	case strings.Contains(loweredAgent, "ipad") || strings.Contains(loweredAgent, "tablet"):
		return "tablet"
	case strings.Contains(loweredAgent, "mobile") || strings.Contains(loweredAgent, "android") || strings.Contains(loweredAgent, "iphone"):
		return "mobile"
	default:
		return "desktop"
	}
}

func analyticsIsBot(userAgent string) bool {
	loweredAgent := strings.ToLower(userAgent)
	botMarkers := []string{
		"bot", "spider", "crawler", "crawl", "slurp", "bingpreview", "facebookexternalhit",
		"headless", "python-requests", "curl/", "wget/", "httpclient", "monitoring",
	}
	for _, marker := range botMarkers {
		if strings.Contains(loweredAgent, marker) {
			return true
		}
	}
	return false
}

func analyticsBotCrawlerName(userAgent string) string {
	loweredAgent := strings.ToLower(userAgent)
	switch {
	case strings.Contains(loweredAgent, "gptbot"):
		return "GPTBot"
	case strings.Contains(loweredAgent, "chatgpt-user"):
		return "ChatGPT"
	case strings.Contains(loweredAgent, "openai"):
		return "OpenAI"
	case strings.Contains(loweredAgent, "googlebot"):
		return "Googlebot"
	case strings.Contains(loweredAgent, "bingbot") || strings.Contains(loweredAgent, "bingpreview"):
		return "Bingbot"
	case strings.Contains(loweredAgent, "yandex"):
		return "YandexBot"
	case strings.Contains(loweredAgent, "baiduspider"):
		return "Baiduspider"
	case strings.Contains(loweredAgent, "duckduckbot"):
		return "DuckDuckBot"
	case strings.Contains(loweredAgent, "ahrefsbot"):
		return "AhrefsBot"
	case strings.Contains(loweredAgent, "semrushbot"):
		return "SemrushBot"
	case strings.Contains(loweredAgent, "facebookexternalhit"):
		return "Facebook crawler"
	case strings.Contains(loweredAgent, "twitterbot"):
		return "Twitterbot"
	case strings.Contains(loweredAgent, "telegrambot"):
		return "TelegramBot"
	case strings.Contains(loweredAgent, "slackbot"):
		return "Slackbot"
	case analyticsIsBot(userAgent):
		return "Other crawler"
	default:
		return "Human"
	}
}

func analyticsBrowserName(userAgent string) string {
	loweredAgent := strings.ToLower(userAgent)
	switch {
	case strings.Contains(loweredAgent, "edg/"):
		return "Edge"
	case strings.Contains(loweredAgent, "opr/") || strings.Contains(loweredAgent, "opera"):
		return "Opera"
	case strings.Contains(loweredAgent, "firefox/"):
		return "Firefox"
	case strings.Contains(loweredAgent, "chrome/") || strings.Contains(loweredAgent, "crios/"):
		return "Chrome"
	case strings.Contains(loweredAgent, "safari/"):
		return "Safari"
	case analyticsIsBot(userAgent):
		return analyticsBotCrawlerName(userAgent)
	default:
		return "Other"
	}
}

func analyticsOSName(userAgent string) string {
	loweredAgent := strings.ToLower(userAgent)
	switch {
	case strings.Contains(loweredAgent, "windows"):
		return "Windows"
	case strings.Contains(loweredAgent, "mac os") || strings.Contains(loweredAgent, "macintosh"):
		return "macOS"
	case strings.Contains(loweredAgent, "iphone") || strings.Contains(loweredAgent, "ipad") || strings.Contains(loweredAgent, "ios"):
		return "iOS"
	case strings.Contains(loweredAgent, "android"):
		return "Android"
	case strings.Contains(loweredAgent, "linux"):
		return "Linux"
	default:
		return "Other"
	}
}

func analyticsLanguageLabel(acceptLanguageHeader string) string {
	languageCode := preferredLanguageCode(acceptLanguageHeader)
	if languageCode == "" {
		return "unknown"
	}
	return languageCode
}

type analyticsLocation struct {
	Country   string
	CityLabel string
	City      string
	Latitude  float64
	Longitude float64
	Detail    string
}

func (location analyticsLocation) hasCoordinates() bool {
	return location.Latitude >= -90 && location.Latitude <= 90 && location.Longitude >= -180 && location.Longitude <= 180 && (location.Latitude != 0 || location.Longitude != 0)
}

type analyticsMapBucket struct {
	Label     string
	Country   string
	City      string
	Latitude  float64
	Longitude float64
	Detail    string
	Count     int
}

type analyticsCountryLocation struct {
	Name      string
	Latitude  float64
	Longitude float64
}

var analyticsCountryLocations = map[string]analyticsCountryLocation{
	"AR": {Name: "Argentina", Latitude: -34.60, Longitude: -58.38},
	"AU": {Name: "Australia", Latitude: -35.28, Longitude: 149.13},
	"BR": {Name: "Brazil", Latitude: -15.78, Longitude: -47.93},
	"CA": {Name: "Canada", Latitude: 45.42, Longitude: -75.69},
	"CN": {Name: "China", Latitude: 39.90, Longitude: 116.40},
	"DE": {Name: "Germany", Latitude: 52.52, Longitude: 13.41},
	"ES": {Name: "Spain", Latitude: 40.42, Longitude: -3.70},
	"FI": {Name: "Finland", Latitude: 60.17, Longitude: 24.94},
	"FR": {Name: "France", Latitude: 48.86, Longitude: 2.35},
	"GB": {Name: "United Kingdom", Latitude: 51.51, Longitude: -0.13},
	"IL": {Name: "Israel", Latitude: 31.78, Longitude: 35.22},
	"IN": {Name: "India", Latitude: 28.61, Longitude: 77.21},
	"IR": {Name: "Iran", Latitude: 35.69, Longitude: 51.39},
	"IT": {Name: "Italy", Latitude: 41.90, Longitude: 12.50},
	"JP": {Name: "Japan", Latitude: 35.68, Longitude: 139.76},
	"KZ": {Name: "Kazakhstan", Latitude: 51.16, Longitude: 71.43},
	"MN": {Name: "Mongolia", Latitude: 47.92, Longitude: 106.92},
	"MX": {Name: "Mexico", Latitude: 19.43, Longitude: -99.13},
	"NL": {Name: "Netherlands", Latitude: 52.37, Longitude: 4.90},
	"PT": {Name: "Portugal", Latitude: 38.72, Longitude: -9.14},
	"RU": {Name: "Russia", Latitude: 55.75, Longitude: 37.62},
	"SE": {Name: "Sweden", Latitude: 59.33, Longitude: 18.07},
	"TR": {Name: "Turkey", Latitude: 39.93, Longitude: 32.86},
	"UA": {Name: "Ukraine", Latitude: 50.45, Longitude: 30.52},
	"US": {Name: "United States", Latitude: 38.90, Longitude: -77.04},
}

var analyticsLanguageDefaultCountries = map[string]string{
	"de": "DE", "en": "US", "es": "ES", "fa": "IR", "fi": "FI", "fr": "FR", "he": "IL",
	"it": "IT", "ja": "JP", "kk": "KZ", "mn": "MN", "pt": "PT", "ru": "RU", "sv": "SE",
	"tr": "TR", "zh": "CN",
}

func analyticsLocationForEvent(event siteAnalyticsEvent) analyticsLocation {
	countryCode := strings.ToUpper(strings.TrimSpace(event.GeoCountryCode))
	sourceDetail := strings.TrimSpace(event.GeoSource)
	if sourceDetail == "" && countryCode != "" {
		sourceDetail = "from proxy geo header"
	}
	if countryCode == "" {
		countryCode = analyticsCountryCodeFromAcceptLanguage(event.AcceptLanguage)
		sourceDetail = "estimated from language"
	}
	country, found := analyticsCountryLocations[countryCode]
	if !found {
		if countryCode != "" {
			country = analyticsCountryLocation{Name: countryCode}
			found = true
		}
	}
	if !found {
		return analyticsLocation{Country: "Unknown", CityLabel: "Unknown city", Detail: "unknown"}
	}
	cityLabel := "Unknown city, " + country.Name
	cityName := strings.TrimSpace(event.GeoCity)
	if cityName != "" {
		cityLabel = cityName + ", " + country.Name
	}
	latitude := country.Latitude
	longitude := country.Longitude
	if event.GeoLatitude >= -90 && event.GeoLatitude <= 90 && event.GeoLongitude >= -180 && event.GeoLongitude <= 180 && (event.GeoLatitude != 0 || event.GeoLongitude != 0) {
		latitude = event.GeoLatitude
		longitude = event.GeoLongitude
	}
	return analyticsLocation{
		Country:   country.Name,
		CityLabel: cityLabel,
		City:      cityName,
		Latitude:  latitude,
		Longitude: longitude,
		Detail:    sourceDetail,
	}
}

func analyticsCountryCodeFromAcceptLanguage(acceptLanguageHeader string) string {
	bestLanguage := ""
	bestCountry := ""
	bestWeight := -1.0
	for _, languageEntry := range strings.Split(strings.ToLower(strings.TrimSpace(acceptLanguageHeader)), ",") {
		parts := strings.Split(languageEntry, ";")
		languageTag := strings.TrimSpace(parts[0])
		if languageTag == "" {
			continue
		}
		weight := 1.0
		for _, parameterEntry := range parts[1:] {
			parameter := strings.TrimSpace(parameterEntry)
			if !strings.HasPrefix(parameter, "q=") {
				continue
			}
			parsedWeight, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(parameter, "q=")), 64)
			if err == nil && parsedWeight >= 0 && parsedWeight <= 1 {
				weight = parsedWeight
			}
			break
		}
		tagParts := strings.FieldsFunc(languageTag, func(currentRune rune) bool {
			return currentRune == '-' || currentRune == '_'
		})
		if len(tagParts) == 0 || weight <= bestWeight {
			continue
		}
		bestLanguage = strings.TrimSpace(tagParts[0])
		bestCountry = ""
		for _, tagPart := range tagParts[1:] {
			if len(tagPart) == 2 {
				bestCountry = strings.ToUpper(tagPart)
				break
			}
		}
		bestWeight = weight
	}
	if bestCountry != "" {
		return bestCountry
	}
	if defaultCountry := analyticsLanguageDefaultCountries[bestLanguage]; defaultCountry != "" {
		return defaultCountry
	}
	return ""
}

func addAnalyticsMapBucket(buckets map[string]analyticsMapBucket, location analyticsLocation) {
	label := location.Country
	if location.City != "" {
		label = location.City + ", " + location.Country
	}
	key := fmt.Sprintf("%.4f:%.4f:%s", location.Latitude, location.Longitude, label)
	bucket := buckets[key]
	if bucket.Count == 0 {
		bucket = analyticsMapBucket{
			Label:     label,
			Country:   location.Country,
			City:      location.City,
			Latitude:  location.Latitude,
			Longitude: location.Longitude,
			Detail:    location.Detail,
		}
	}
	bucket.Count++
	buckets[key] = bucket
}

func analyticsMapPoints(buckets map[string]analyticsMapBucket, total int) []analyticsMapPoint {
	rows := make([]analyticsMapBucket, 0, len(buckets))
	for _, bucket := range buckets {
		rows = append(rows, bucket)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count == rows[j].Count {
			return rows[i].Label < rows[j].Label
		}
		return rows[i].Count > rows[j].Count
	})
	if len(rows) > 120 {
		rows = rows[:120]
	}
	minCount := 0
	maxCount := 0
	for _, row := range rows {
		if minCount == 0 || row.Count < minCount {
			minCount = row.Count
		}
		if row.Count > maxCount {
			maxCount = row.Count
		}
	}
	points := make([]analyticsMapPoint, 0, len(rows))
	for _, row := range rows {
		heat := analyticsHeatValue(row.Count, minCount, maxCount)
		left := (row.Longitude + 180) / 360 * 100
		top := (90 - row.Latitude) / 180 * 100
		points = append(points, analyticsMapPoint{
			Label:     row.Label,
			Country:   row.Country,
			City:      row.City,
			Latitude:  row.Latitude,
			Longitude: row.Longitude,
			Count:     row.Count,
			Percent:   analyticsPercent(row.Count, total),
			Left:      fmt.Sprintf("%.2f%%", left),
			Top:       fmt.Sprintf("%.2f%%", top),
			Detail:    row.Detail,
			Color:     analyticsHeatColor(heat),
			Heat:      heat,
			Radius:    5 + heat*7,
		})
	}
	return points
}

func analyticsHeatValue(count, minCount, maxCount int) float64 {
	if maxCount <= minCount {
		return 0
	}
	return float64(count-minCount) / float64(maxCount-minCount)
}

func analyticsHeatColor(heat float64) string {
	normalizedHeat := math.Max(0, math.Min(1, heat))
	if normalizedHeat <= 0.5 {
		return interpolateHexColor(0x2ecc71, 0xf1c40f, normalizedHeat*2)
	}
	return interpolateHexColor(0xf1c40f, 0xe74c3c, (normalizedHeat-0.5)*2)
}

func interpolateHexColor(startColor, endColor int, ratio float64) string {
	startRed := (startColor >> 16) & 0xff
	startGreen := (startColor >> 8) & 0xff
	startBlue := startColor & 0xff
	endRed := (endColor >> 16) & 0xff
	endGreen := (endColor >> 8) & 0xff
	endBlue := endColor & 0xff
	red := startRed + int(math.Round(float64(endRed-startRed)*ratio))
	green := startGreen + int(math.Round(float64(endGreen-startGreen)*ratio))
	blue := startBlue + int(math.Round(float64(endBlue-startBlue)*ratio))
	return fmt.Sprintf("#%02x%02x%02x", red, green, blue)
}

func analyticsPercent(count, total int) string {
	if total <= 0 {
		return "0%"
	}
	return fmt.Sprintf("%.1f%%", float64(count)/float64(total)*100)
}

func formatDurationMS(milliseconds int64) string {
	if milliseconds < 1000 {
		return fmt.Sprintf("%d ms", milliseconds)
	}
	return fmt.Sprintf("%.2f s", float64(milliseconds)/1000)
}

func (a *App) analyticsPage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		if !a.hasAdmin(r.Context(), a.siteDomain(r.Context(), r)) {
			http.Redirect(w, r, r.URL.Path+"?register", http.StatusFound)
			return
		}
		http.Redirect(w, r, loginURLForRequest(r), http.StatusFound)
		return
	}
	domain := a.siteDomain(r.Context(), r)
	report, found := a.loadAnalyticsReport(r.Context(), domain)
	if !found {
		var err error
		report, err = a.buildAnalyticsReport(r.Context(), domain)
		if err == nil {
			_ = a.saveAnalyticsReport(r.Context(), domain, report)
			found = true
		}
	}
	if !found {
		report = analyticsPreparedReport{GeneratedAt: time.Now().UTC().Format(time.RFC3339)}
	}
	a.render(w, r, "analytics.html", map[string]any{
		"ReturnPath": requestedReturnPath(r),
		"Report":     analyticsReportView(report, translationsForRequest(r)),
	})
}

func analyticsReportView(report analyticsPreparedReport, translations map[string]string) analyticsPageData {
	view := analyticsPageData{
		GeneratedAt:    formatAnalyticsTime(report.GeneratedAt),
		Period:         formatAnalyticsTime(report.PeriodStart) + " - " + formatAnalyticsTime(report.PeriodEnd),
		MapTitle:       translationOrDefault(translations, "analytics_section_world_map", "World map"),
		MapHint:        translationOrDefault(translations, "analytics_section_world_map_hint", "Visitor geography from the local GeoIP database cache, with request metadata fallback while the database downloads."),
		MapPoints:      report.MapPoints,
		MapJSON:        analyticsMapPointsJSON(report.MapPoints),
		MapAttribution: template.HTML(geoip.AttributionHTML),
	}
	if strings.TrimSpace(report.PeriodStart) == "" || report.TotalRequests == 0 {
		view.Period = translationOrDefault(translations, "analytics_no_data", "No analytics data yet.")
	}
	view.Cards = []analyticsMetricCard{
		{Label: translationOrDefault(translations, "analytics_total_requests", "Total requests"), Value: strconv.Itoa(report.TotalRequests), Hint: translationOrDefault(translations, "analytics_total_requests_hint", "All logged dynamic, static, and controller requests.")},
		{Label: translationOrDefault(translations, "analytics_page_views", "Page views"), Value: strconv.Itoa(report.PageViews), Hint: translationOrDefault(translations, "analytics_page_views_hint", "GET page requests excluding assets and Sitebrush controllers.")},
		{Label: translationOrDefault(translations, "analytics_unique_visitors", "Unique visitors"), Value: strconv.Itoa(report.UniqueVisitors), Hint: translationOrDefault(translations, "analytics_unique_visitors_hint", "Estimated from IP and browser signature.")},
		{Label: translationOrDefault(translations, "analytics_human_requests", "People"), Value: strconv.Itoa(report.HumanRequests), Hint: translationOrDefault(translations, "analytics_human_requests_hint", "Requests that do not look like known bots or crawlers.")},
		{Label: translationOrDefault(translations, "analytics_bot_requests", "Bots"), Value: strconv.Itoa(report.BotRequests), Hint: translationOrDefault(translations, "analytics_bot_requests_hint", "Requests from crawlers, bots, monitors, and automated clients.")},
		{Label: translationOrDefault(translations, "analytics_returning_visitors", "Returning visitors"), Value: strconv.Itoa(report.ReturningVisitors), Hint: translationOrDefault(translations, "analytics_returning_visitors_hint", "Visitors with more than one visit in the report period.")},
		{Label: translationOrDefault(translations, "analytics_return_visits", "Repeat visits"), Value: strconv.Itoa(report.ReturnVisits), Hint: translationOrDefault(translations, "analytics_return_visits_hint", "Visits after the first visit from the same visitor signature.")},
		{Label: translationOrDefault(translations, "analytics_sessions", "Sessions"), Value: strconv.Itoa(report.Sessions), Hint: translationOrDefault(translations, "analytics_sessions_hint", "Visits split after 30 minutes of inactivity.")},
		{Label: translationOrDefault(translations, "analytics_bounce_rate", "Bounce rate"), Value: fmt.Sprintf("%.1f%%", report.BounceRate), Hint: translationOrDefault(translations, "analytics_bounce_rate_hint", "Sessions with one page view.")},
		{Label: translationOrDefault(translations, "analytics_avg_duration", "Average response time"), Value: formatDurationMS(report.AverageDurationMS), Hint: translationOrDefault(translations, "analytics_avg_duration_hint", "Average server response time across logged requests.")},
		{Label: translationOrDefault(translations, "analytics_errors", "Errors"), Value: strconv.Itoa(report.ErrorCount), Hint: translationOrDefault(translations, "analytics_errors_hint", "Requests with HTTP status 400 or higher.")},
		{Label: translationOrDefault(translations, "analytics_admin_traffic", "Admin traffic"), Value: strconv.Itoa(report.AdminRequests), Hint: translationOrDefault(translations, "analytics_admin_traffic_hint", "Requests made while logged in as an administrator.")},
	}
	view.Sections = []analyticsReportSection{
		analyticsSectionView("analytics_section_top_pages", "analytics_section_top_pages_hint", report.TopPages, report.PageViews, "path", translations),
		analyticsSectionView("analytics_section_entry_pages", "analytics_section_entry_pages_hint", report.EntryPages, report.Sessions, "path", translations),
		analyticsSectionView("analytics_section_exit_pages", "analytics_section_exit_pages_hint", report.ExitPages, report.Sessions, "path", translations),
		analyticsSectionView("analytics_section_traffic_sources", "analytics_section_traffic_sources_hint", report.TrafficSources, report.PageViews, "traffic", translations),
		analyticsSectionView("analytics_section_referrers", "analytics_section_referrers_hint", report.Referrers, report.PageViews, "plain", translations),
		analyticsSectionView("analytics_section_returning_sources", "analytics_section_returning_sources_hint", report.ReturningSources, report.ReturnVisits, "traffic", translations),
		analyticsSectionView("analytics_section_returning_referrers", "analytics_section_returning_referrers_hint", report.ReturningReferrers, report.ReturnVisits, "plain", translations),
		analyticsSectionView("analytics_section_countries", "analytics_section_countries_hint", report.Countries, report.PageViews, "plain", translations),
		analyticsSectionView("analytics_section_cities", "analytics_section_cities_hint", report.Cities, report.PageViews, "plain", translations),
		analyticsSectionView("analytics_section_entry_hours", "analytics_section_entry_hours_hint", report.EntryHours, report.Sessions, "plain", translations),
		analyticsSectionView("analytics_section_visitor_types", "analytics_section_visitor_types_hint", report.VisitorTypes, report.PageViews, "visitor", translations),
		analyticsSectionView("analytics_section_bot_crawlers", "analytics_section_bot_crawlers_hint", report.BotCrawlers, report.PageViews, "plain", translations),
		analyticsSectionView("analytics_section_bot_return_sources", "analytics_section_bot_return_sources_hint", report.BotReturnSources, report.ReturnVisits, "traffic", translations),
		analyticsSectionView("analytics_section_bot_referrers", "analytics_section_bot_referrers_hint", report.BotReferrers, report.PageViews, "plain", translations),
		analyticsSectionView("analytics_section_devices", "analytics_section_devices_hint", report.Devices, report.PageViews, "device", translations),
		analyticsSectionView("analytics_section_browsers", "analytics_section_browsers_hint", report.Browsers, report.PageViews, "plain", translations),
		analyticsSectionView("analytics_section_os", "analytics_section_os_hint", report.OperatingSystems, report.PageViews, "plain", translations),
		analyticsSectionView("analytics_section_languages", "analytics_section_languages_hint", report.Languages, report.PageViews, "language", translations),
		analyticsSectionView("analytics_section_status_codes", "analytics_section_status_codes_hint", report.StatusCodes, report.TotalRequests, "plain", translations),
		analyticsSectionView("analytics_section_hourly", "analytics_section_hourly_hint", report.HourlyActivity, report.TotalRequests, "plain", translations),
		analyticsSectionView("analytics_section_daily", "analytics_section_daily_hint", report.DailyActivity, report.TotalRequests, "plain", translations),
		analyticsSectionView("analytics_section_slow_pages", "analytics_section_slow_pages_hint", report.SlowPages, 0, "duration", translations),
		analyticsSectionView("analytics_section_assets", "analytics_section_assets_hint", report.TopAssets, report.StaticRequests, "path", translations),
		analyticsSectionView("analytics_section_errors", "analytics_section_errors_hint", report.ErrorPaths, report.ErrorCount, "path", translations),
		analyticsSectionView("analytics_section_content_sources", "analytics_section_content_sources_hint", report.ContentSources, report.TotalRequests, "content", translations),
		analyticsSectionView("analytics_section_system_events", "analytics_section_system_events_hint", report.SystemEvents, 0, "plain", translations),
	}
	return view
}

func analyticsSectionView(titleKey, descriptionKey string, sourceRows []analyticsCountRow, total int, labelKind string, translations map[string]string) analyticsReportSection {
	titleFallback := titleKey
	descriptionFallback := ""
	if titleKey == "analytics_section_system_events" {
		titleFallback = "Analytics system events"
	}
	if descriptionKey == "analytics_section_system_events_hint" {
		descriptionFallback = "Overload and recovery markers from the bounded in-memory analytics worker."
	}
	section := analyticsReportSection{
		Title:       translationOrDefault(translations, titleKey, titleFallback),
		Description: translationOrDefault(translations, descriptionKey, descriptionFallback),
		Rows:        make([]analyticsReportRow, 0, len(sourceRows)),
	}
	for _, sourceRow := range sourceRows {
		value := strconv.Itoa(sourceRow.Count)
		detail := sourceRow.Detail
		if labelKind == "duration" {
			value = sourceRow.Value
			if detail != "" {
				detail = detail + " " + translationOrDefault(translations, "analytics_views_suffix", "views")
			}
		}
		section.Rows = append(section.Rows, analyticsReportRow{
			Label:   localizeAnalyticsReportLabel(labelKind, sourceRow.Label, translations),
			Value:   value,
			Percent: analyticsPercent(sourceRow.Count, total),
			Detail:  detail,
		})
	}
	return section
}

func analyticsMapPointsJSON(points []analyticsMapPoint) template.JS {
	payload, err := json.Marshal(points)
	if err != nil {
		return template.JS("[]")
	}
	return template.JS(payload)
}

func localizeAnalyticsReportLabel(labelKind, label string, translations map[string]string) string {
	keyPrefix := ""
	switch labelKind {
	case "traffic":
		keyPrefix = "analytics_source_"
	case "device":
		keyPrefix = "analytics_device_"
	case "content":
		keyPrefix = "analytics_content_"
	case "language":
		keyPrefix = "analytics_language_"
	case "visitor":
		keyPrefix = "analytics_visitor_"
	}
	if keyPrefix == "" {
		return label
	}
	key := keyPrefix + strings.NewReplacer(" ", "_", "-", "_").Replace(strings.ToLower(label))
	return translationOrDefault(translations, key, label)
}

func formatAnalyticsTime(rawTime string) string {
	parsedTime := parseAnalyticsTime(rawTime)
	if parsedTime.IsZero() {
		return ""
	}
	return parsedTime.Local().Format("2006-01-02 15:04:05")
}

func (a *App) authAbuseMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

func isLikelyStaticAssetPath(requestPath string) bool {
	if strings.HasPrefix(requestPath, "/p/static/") || strings.HasPrefix(requestPath, "/p/") {
		return true
	}
	fileExtension := strings.ToLower(path.Ext(requestPath))
	switch fileExtension {
	case ".css", ".js", ".mjs", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp", ".woff", ".woff2", ".ttf", ".eot", ".map":
		return true
	default:
		return false
	}
}

func clientIPAddress(r *http.Request) string {
	remoteIP := remoteIPAddress(r)
	if trustedForwardingProxyIP(remoteIP) {
		forwardedHeader := strings.TrimSpace(r.Header.Get("Forwarded"))
		if forwardedHeader != "" {
			for _, forwardedPart := range strings.Split(forwardedHeader, ";") {
				normalizedPart := strings.TrimSpace(forwardedPart)
				if !strings.HasPrefix(strings.ToLower(normalizedPart), "for=") {
					continue
				}
				forwardedValue := strings.Trim(strings.TrimSpace(strings.TrimPrefix(normalizedPart, "for=")), "\"")
				if parsedIP := net.ParseIP(strings.Trim(strings.TrimSpace(forwardedValue), "[]")); parsedIP != nil {
					return normalizedClientIPAddress(parsedIP)
				}
			}
		}
		xForwardedFor := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
		if xForwardedFor != "" {
			for _, ipCandidate := range strings.Split(xForwardedFor, ",") {
				if parsedIP := net.ParseIP(strings.TrimSpace(ipCandidate)); parsedIP != nil {
					return normalizedClientIPAddress(parsedIP)
				}
			}
		}
	}
	if remoteIP != nil {
		return normalizedClientIPAddress(remoteIP)
	}
	return ""
}

func remoteIPAddress(r *http.Request) net.IP {
	hostPart, _, splitErr := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if splitErr == nil {
		if parsedIP := net.ParseIP(strings.TrimSpace(hostPart)); parsedIP != nil {
			return parsedIP
		}
	}
	if parsedIP := net.ParseIP(strings.TrimSpace(r.RemoteAddr)); parsedIP != nil {
		return parsedIP
	}
	return nil
}

func trustedForwardingProxyIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate()
}

func normalizedClientIPAddress(ip net.IP) string {
	if ip == nil {
		return ""
	}
	if ip.IsLoopback() {
		return "localhost"
	}
	return ip.String()
}

func clientIPStorageAliases(clientIP string) []string {
	normalizedClientIP := strings.TrimSpace(clientIP)
	if normalizedClientIP == "localhost" || normalizedClientIP == "127.0.0.1" || normalizedClientIP == "::1" {
		return []string{"localhost", "127.0.0.1", "::1"}
	}
	return []string{normalizedClientIP}
}

func analyticsGeoCountryCodeFromRequest(r *http.Request) string {
	headerNames := []string{"CF-IPCountry", "CloudFront-Viewer-Country", "X-Vercel-IP-Country", "X-Appengine-Country", "X-Geo-Country"}
	for _, headerName := range headerNames {
		countryCode := strings.ToUpper(strings.TrimSpace(r.Header.Get(headerName)))
		if len(countryCode) == 2 && countryCode != "XX" {
			return countryCode
		}
	}
	return ""
}

func analyticsGeoCityFromRequest(r *http.Request) string {
	headerNames := []string{"CF-IPCity", "X-Vercel-IP-City", "X-Appengine-City", "X-Geo-City"}
	for _, headerName := range headerNames {
		city := strings.TrimSpace(r.Header.Get(headerName))
		if city != "" {
			if decodedCity, err := url.QueryUnescape(city); err == nil && strings.TrimSpace(decodedCity) != "" {
				city = decodedCity
			}
			return city
		}
	}
	return ""
}

func analyticsGeoSourceFromRequest(r *http.Request) string {
	if analyticsGeoCountryCodeFromRequest(r) != "" || analyticsGeoCityFromRequest(r) != "" {
		return "proxy geo header"
	}
	return ""
}

func loginBlockDurationForFailureCount(failureCount int) (time.Duration, bool) {
	switch failureCount {
	case 4:
		return time.Minute, false
	case 5:
		return 5 * time.Minute, false
	case 6:
		return 15 * time.Minute, false
	case 7:
		return 30 * time.Minute, false
	case 8:
		return time.Hour, false
	case 9:
		return 3 * time.Hour, false
	default:
		if failureCount >= 10 {
			return 0, true
		}
		return 0, false
	}
}

func flagWasProvided(flagName string) bool {
	provided := false
	flag.Visit(func(currentFlag *flag.Flag) {
		if currentFlag.Name == flagName {
			provided = true
		}
	})
	return provided
}

func cleanStoragePath(storagePath string) string {
	trimmedStoragePath := strings.TrimSpace(storagePath)
	if trimmedStoragePath == "" {
		return defaultAppStoragePath()
	}
	return filepath.Clean(trimmedStoragePath)
}

func cleanDBPath(dbPath string) string {
	trimmedDBPath := strings.TrimSpace(dbPath)
	if trimmedDBPath == "" {
		return defaultDBPath
	}
	return filepath.Clean(trimmedDBPath)
}

func ensureParentDir(filePath string) error {
	parentDir := filepath.Dir(filePath)
	if parentDir == "." || parentDir == "" {
		return nil
	}
	return os.MkdirAll(parentDir, 0o755)
}

type listenPorts struct {
	Raw        string
	HTTPPort   int
	TLSEnabled bool
}

type serverRunConfig struct {
	ParsedPorts listenPorts
	StoragePath string
	DBPath      string
	DesktopMode bool
}

func main() {
	port := flag.String("port", "80,443", "listen port or standard pair 80,443")
	dbType := flag.String("db-type", "sqlite", "database driver (supported: sqlite)")
	storagePath := flag.String("storage-path", defaultAppStoragePath(), "path to the Sitebrush app data directory")
	dbPath := flag.String("db-path", defaultDBPath, "path to sqlite database file")
	listSitesMode := flag.Bool("list-sites", false, "open interactive server site quota console")
	quotaSite := flag.String("quota-site", "", "site domain to update storage quota for")
	quotaValue := flag.String("quota", "", "set -quota-site storage quota, for example 50mb or 20gb")
	versionShort := flag.Bool("v", false, "print version and exit")
	versionLong := flag.Bool("version", false, "print version and exit")
	var desktopModeFlag *bool
	if appcli.DesktopModeFlagSupported() {
		desktopModeFlag = flag.Bool("desktop", desktop.DefaultEnabled(), "enable desktop mode when desktop build tags are used")
	}
	var installModeFlag *bool
	var uninstallModeFlag *bool
	if appcli.InstallFlagSupported() {
		installModeFlag = flag.Bool("install", false, "install Sitebrush as a system service")
		uninstallModeFlag = flag.Bool("uninstall", false, "uninstall Sitebrush system service and remove startup registration")
	}
	flag.Parse()
	if *versionShort || *versionLong {
		fmt.Println(CompileVersion)
		return
	}
	desktopMode := false
	if desktopModeFlag != nil {
		desktopMode = *desktopModeFlag
	}
	installMode := false
	if installModeFlag != nil {
		installMode = *installModeFlag
	}
	uninstallMode := false
	if uninstallModeFlag != nil {
		uninstallMode = *uninstallModeFlag
	}
	if installMode && uninstallMode {
		log.Fatal("choose only one service action: -install or -uninstall")
	}

	if *dbType != "sqlite" {
		log.Fatalf("unsupported -db-type %q, supported: sqlite", *dbType)
	}

	effectiveStoragePath := cleanStoragePath(*storagePath)
	effectiveDBPath := cleanDBPath(*dbPath)
	if !flagWasProvided("db-path") {
		effectiveDBPath = filepath.Join(effectiveStoragePath, defaultDBPath)
	}
	quotaCommandMode := *listSitesMode || strings.TrimSpace(*quotaSite) != "" || flagWasProvided("quota")
	if quotaCommandMode {
		if err := runSiteQuotaCommand(context.Background(), os.Stdout, os.Stdin, effectiveStoragePath, effectiveDBPath, *listSitesMode, *quotaSite, *quotaValue); err != nil {
			log.Fatal(err)
		}
		return
	}
	parsedPorts, err := parseListenPorts(*port)
	if err != nil {
		log.Fatal(err)
	}
	if installMode {
		if err := runServiceInstaller(parsedPorts.Raw, *dbType, effectiveStoragePath, effectiveDBPath); err != nil {
			handleServiceControlError(err)
		}
		return
	}
	if uninstallMode {
		if err := runServiceUninstaller(parsedPorts.Raw, *dbType, effectiveStoragePath, effectiveDBPath); err != nil {
			handleServiceControlError(err)
		}
		return
	}
	serverConfig := serverRunConfig{ParsedPorts: parsedPorts, StoragePath: effectiveStoragePath, DBPath: effectiveDBPath, DesktopMode: desktopMode}
	if handled, err := winservice.RunIfNeeded("sitebrush", func(ctx context.Context) error {
		return runSitebrushServer(ctx, serverConfig)
	}); handled {
		if err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := runSitebrushServer(context.Background(), serverConfig); err != nil {
		log.Fatal(err)
	}
}

func runSitebrushServer(ctx context.Context, config serverRunConfig) error {
	parsedPorts := config.ParsedPorts
	effectiveDBPath := config.DBPath
	effectiveStoragePath := config.StoragePath
	if err := ensureParentDir(effectiveDBPath); err != nil {
		return err
	}

	var siteDatabaseRouter *perSiteDBRouter
	application := &App{storagePath: effectiveStoragePath, nativeFileDialog: desktop.NativeFileDialogSupported(), grabTracker: newGrabProgressTracker(), publishTracker: newPublishProgressTracker(), analyticsEvents: make(chan siteAnalyticsEvent, 65536), domainLogEvents: make(chan domainLogEvent, 1024)}
	application.guestStaticHTMLCache = startGuestStaticHTMLCacheWorker(ctx, defaultGuestStaticHTMLCacheLimitBytes)
	application.authIPFailureCache = startAuthIPFailureCacheWorker(ctx)
	application.emailDelivery = startEmailDeliveryWorker(ctx, application.defaultEmailSender())
	application.geoIP = geoip.NewResolver(filepath.Join(application.storageRootDir(), "geoip"))
	siteDatabaseRootDir := siteDatabaseRootPath(effectiveDBPath)
	siteDatabaseRouter = newPerSiteDBRouter(siteDatabaseRootDir, "localhost", func(rawDatabase *sql.DB) error {
		bootstrapApplication := &App{db: rawDatabase, storagePath: effectiveStoragePath}
		return bootstrapApplication.migrate(contextWithDomain(ctx, "localhost"))
	})
	defer func() {
		if closeErr := siteDatabaseRouter.Close(); closeErr != nil {
			log.Printf("site database router close failed: %v", closeErr)
		}
	}()
	application.db = siteDatabaseRouter
	application.ensureAutoCertGuard()
	application.startDomainLogWorker(context.Background())
	application.startAnalyticsWorkers(context.Background())

	router := http.NewServeMux()
	staticFiles, err := fs.Sub(embeddedWebFiles, "web/static")
	if err != nil {
		return err
	}
	router.Handle("/p/static/", http.StripPrefix("/p/static/", http.FileServer(http.FS(staticFiles))))
	router.HandleFunc("/p/", application.servePublicAsset)
	router.HandleFunc("/", application.route)

	listener, listenPort, err := listenOnAvailablePort(parsedPorts.HTTPPort)
	if err != nil {
		return err
	}
	defer listener.Close()

	address := "localhost:" + strconv.Itoa(listenPort)
	log.Printf("Sitebrush started on http://%s", address)

	domainContextMiddleware := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestDomain := domainFromRequest(r)
		nextRequest := r.WithContext(contextWithDomain(r.Context(), requestDomain))
		router.ServeHTTP(w, nextRequest)
	})
	appHandler := application.analyticsMiddleware(application.accessLogMiddleware(application.authAbuseMiddleware(domainContextMiddleware)))
	httpHandler := appHandler
	if parsedPorts.TLSEnabled && listenPort != 80 {
		application.logProblemEvent("AUTOCERT disabled: Let’s Encrypt HTTP-01 checks need public port 80; current HTTP port is %d", listenPort)
	}
	certificateCacheDir := filepath.Join(application.storageRootDir(), "letsencrypt")
	if mkdirErr := os.MkdirAll(certificateCacheDir, 0o755); mkdirErr != nil {
		application.logProblemEvent("AUTOCERT disabled: failed to create certificate cache %s: %v", certificateCacheDir, mkdirErr)
	} else if parsedPorts.TLSEnabled && listenPort == 80 {
		certificateManager := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			Cache:      autocert.DirCache(certificateCacheDir),
			HostPolicy: application.autoCertHostPolicy,
		}
		tlsListener, tlsListenErr := listenTLSForAutoCert()
		if tlsListenErr != nil {
			application.logProblemEvent("AUTOCERT disabled: cannot listen on port 443: %v", tlsListenErr)
		} else {
			application.automaticSSLAvailable = true
			httpHandler = certificateManager.HTTPHandler(appHandler)
			application.logProblemEvent("AUTOCERT enabled: HTTP challenge on port 80, HTTPS TLS listener on port 443, certificate cache=%s", certificateCacheDir)
			go application.serveTLSWithAutoCert(tlsListener, application.autoCertTLSConfig(certificateManager), appHandler)
			application.startAutomaticSSLRefreshWorker(ctx)
		}
	}

	server := &http.Server{Handler: httpHandler}
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.Serve(listener)
	}()

	if config.DesktopMode {
		if err := desktop.RunWebviewWindow(address, CompileVersion); err != nil {
			return err
		}
		return nil
	}

	select {
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func runServiceInstaller(port, dbType, storagePath, dbPath string) error {
	binaryPath, err := os.Executable()
	if err != nil || strings.TrimSpace(binaryPath) == "" {
		binaryPath = os.Args[0]
	}
	_, err = serviceinstall.Install(context.Background(), serviceinstall.Options{
		Port:        port,
		StoragePath: storagePath,
		DBType:      dbType,
		DBPath:      dbPath,
		BinaryPath:  binaryPath,
		WorkingDir:  storagePath,
		Input:       os.Stdin,
		Output:      os.Stdout,
	})
	return err
}

func runServiceUninstaller(port, dbType, storagePath, dbPath string) error {
	binaryPath, err := os.Executable()
	if err != nil || strings.TrimSpace(binaryPath) == "" {
		binaryPath = os.Args[0]
	}
	_, err = serviceinstall.Uninstall(context.Background(), serviceinstall.Options{
		Port:        port,
		StoragePath: storagePath,
		DBType:      dbType,
		DBPath:      dbPath,
		BinaryPath:  binaryPath,
		WorkingDir:  storagePath,
		Input:       os.Stdin,
		Output:      os.Stdout,
	})
	return err
}

func handleServiceControlError(err error) {
	if errors.Is(err, serviceinstall.ErrCancelled) {
		message := strings.TrimSpace(strings.TrimSuffix(err.Error(), ": "+serviceinstall.ErrCancelled.Error()))
		if message == "" {
			message = err.Error()
		}
		fmt.Fprintln(os.Stdout, message)
		return
	}
	fmt.Fprintf(os.Stderr, "Sitebrush service action failed: %v\n", err)
	os.Exit(1)
}

func parseListenPorts(rawPorts string) (listenPorts, error) {
	cleaned := strings.TrimSpace(rawPorts)
	if cleaned == "" {
		cleaned = "80,443"
	}
	parts := strings.Split(cleaned, ",")
	seenPorts := make(map[int]bool, len(parts))
	parsed := make([]int, 0, len(parts))
	for _, part := range parts {
		nextPort, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || nextPort <= 0 || nextPort > 65535 {
			return listenPorts{}, fmt.Errorf("invalid -port value %q", rawPorts)
		}
		if seenPorts[nextPort] {
			continue
		}
		seenPorts[nextPort] = true
		parsed = append(parsed, nextPort)
	}
	if len(parsed) == 1 {
		if parsed[0] == 80 {
			return listenPorts{Raw: "80,443", HTTPPort: 80, TLSEnabled: true}, nil
		}
		if parsed[0] == 443 {
			return listenPorts{}, fmt.Errorf("-port 443 is incomplete; use -port 80,443 or choose another HTTP port")
		}
		return listenPorts{Raw: strconv.Itoa(parsed[0]), HTTPPort: parsed[0]}, nil
	}
	if len(parsed) == 2 && seenPorts[80] && seenPorts[443] {
		return listenPorts{Raw: "80,443", HTTPPort: 80, TLSEnabled: true}, nil
	}
	return listenPorts{}, fmt.Errorf("-port must be 80,443 or one custom HTTP port")
}

func listenOnAvailablePort(requestedPort int) (net.Listener, int, error) {
	listener, err := net.Listen("tcp", ":"+strconv.Itoa(requestedPort))
	if err == nil {
		return listener, requestedPort, nil
	}
	for fallbackPort := 9898; fallbackPort < 65536; fallbackPort++ {
		listener, err = net.Listen("tcp", ":"+strconv.Itoa(fallbackPort))
		if err == nil {
			log.Printf("port %d is unavailable, using %d", requestedPort, fallbackPort)
			return listener, fallbackPort, nil
		}
	}
	return nil, 0, fmt.Errorf("no available HTTP port after %d: %w", requestedPort, err)
}

func listenTLSForAutoCert() (net.Listener, error) {
	return net.Listen("tcp", ":443")
}

func (a *App) serveTLSWithAutoCert(tlsListener net.Listener, tlsConfig *tls.Config, handler http.Handler) {
	tlsServer := &http.Server{
		Handler:   handler,
		TLSConfig: tlsConfig,
		ErrorLog:  log.New(problemLogWriter{application: a}, "", 0),
	}
	tlsAddress := tlsListener.Addr().String()
	a.logProblemEvent("HTTPS server enabled on %s with TLS", tlsAddress)
	if serveErr := tlsServer.ServeTLS(tlsListener, "", ""); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		a.logProblemEvent("HTTPS server stopped: %v", serveErr)
	}
}

func (a *App) autoCertTLSConfig(certificateManager *autocert.Manager) *tls.Config {
	tlsConfig := certificateManager.TLSConfig()
	originalGetCertificate := tlsConfig.GetCertificate
	tlsConfig.GetCertificate = func(clientHello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		certificateDomain := normalizeDomainName(clientHello.ServerName)
		if certificateDomain == "" {
			certificateDomain = "localhost"
		}
		remoteAddress := tlsClientRemoteAddress(clientHello)
		now := time.Now()
		cachedCertificate, cachedExpiresAt, cachedCertificateOK := a.autoCertCachedCertificate(certificateDomain, now, 0)
		if cachedCertificateOK && !cachedExpiresAt.Before(now.Add(automaticSSLCertificateRenewBefore)) {
			a.logDomainEvent(certificateDomain, "AUTOCERT certificate loaded from cache server_name=%s remote=%s expires_at=%s", clientHello.ServerName, remoteAddress, cachedExpiresAt.Format(time.RFC3339))
			return cachedCertificate, nil
		}
		if err := a.autoCertRetryDelayError(contextWithDomain(context.Background(), certificateDomain), certificateDomain, now); err != nil {
			if cachedCertificateOK {
				a.logDomainEvent(certificateDomain, "AUTOCERT renewal delayed; serving cached certificate server_name=%s remote=%s expires_at=%s error=%v", clientHello.ServerName, remoteAddress, cachedExpiresAt.Format(time.RFC3339), err)
				return cachedCertificate, nil
			}
			a.logDomainEvent(certificateDomain, "AUTOCERT certificate request delayed server_name=%s remote=%s error=%v", clientHello.ServerName, remoteAddress, err)
			return nil, err
		}
		if err := a.beginAutoCertIssuance(context.Background(), certificateDomain); err != nil {
			if cachedCertificateOK {
				a.logDomainEvent(certificateDomain, "AUTOCERT renewal skipped; serving cached certificate server_name=%s remote=%s expires_at=%s error=%v", clientHello.ServerName, remoteAddress, cachedExpiresAt.Format(time.RFC3339), err)
				return cachedCertificate, nil
			}
			a.logDomainEvent(certificateDomain, "AUTOCERT certificate request skipped server_name=%s remote=%s error=%v", clientHello.ServerName, remoteAddress, err)
			return nil, err
		}
		defer a.finishAutoCertIssuance(certificateDomain)
		a.logDomainEvent(certificateDomain, "AUTOCERT certificate request started server_name=%s remote=%s", clientHello.ServerName, remoteAddress)
		certificate, err := originalGetCertificate(clientHello)
		if err != nil {
			retryDelay := automaticSSLRetryDelayForCertificateError(err)
			a.recordDomainAutomaticSSLFailure(contextWithDomain(context.Background(), certificateDomain), certificateDomain, fmt.Sprintf("certificate request failed: %v", err), retryDelay)
			if cachedCertificateOK {
				a.logDomainEvent(certificateDomain, "AUTOCERT certificate request failed; serving cached certificate server_name=%s remote=%s expires_at=%s error=%v", clientHello.ServerName, remoteAddress, cachedExpiresAt.Format(time.RFC3339), err)
				return cachedCertificate, nil
			}
			a.logDomainEvent(certificateDomain, "AUTOCERT certificate request failed server_name=%s remote=%s error=%v", clientHello.ServerName, remoteAddress, err)
			return nil, err
		}
		a.clearDomainAutomaticSSLFailure(contextWithDomain(context.Background(), certificateDomain), certificateDomain)
		a.logDomainEvent(certificateDomain, "AUTOCERT certificate ready server_name=%s remote=%s", clientHello.ServerName, remoteAddress)
		return certificate, nil
	}
	return tlsConfig
}

func tlsClientRemoteAddress(clientHello *tls.ClientHelloInfo) string {
	if clientHello == nil || clientHello.Conn == nil || clientHello.Conn.RemoteAddr() == nil {
		return ""
	}
	return clientHello.Conn.RemoteAddr().String()
}

func (a *App) ensureAutoCertGuard() {
	if a.autoCertGuard != nil {
		return
	}
	autoCertGuard := make(chan autoCertGuardRequest, 128)
	a.autoCertGuard = autoCertGuard
	go runAutoCertGuard(autoCertGuard)
}

func runAutoCertGuard(requests <-chan autoCertGuardRequest) {
	inFlightDomains := make(map[string]bool)
	for request := range requests {
		switch request.action {
		case "begin":
			if inFlightDomains[request.domain] {
				request.response <- autoCertGuardResponse{allowed: false, reason: "automatic SSL request already in progress for this domain"}
				continue
			}
			inFlightDomains[request.domain] = true
			request.response <- autoCertGuardResponse{allowed: true}
		case "finish":
			delete(inFlightDomains, request.domain)
		}
	}
}

func (a *App) beginAutoCertIssuance(ctx context.Context, domain string) error {
	certificateDomain := normalizeDomainName(domain)
	if certificateDomain == "" {
		return fmt.Errorf("invalid certificate domain %q", domain)
	}
	a.ensureAutoCertGuard()
	response := make(chan autoCertGuardResponse, 1)
	request := autoCertGuardRequest{action: "begin", domain: certificateDomain, response: response}
	select {
	case a.autoCertGuard <- request:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case guardResponse := <-response:
		if guardResponse.allowed {
			return nil
		}
		return fmt.Errorf("%s", guardResponse.reason)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *App) finishAutoCertIssuance(domain string) {
	certificateDomain := normalizeDomainName(domain)
	if certificateDomain == "" || a.autoCertGuard == nil {
		return
	}
	a.autoCertGuard <- autoCertGuardRequest{action: "finish", domain: certificateDomain}
}

func (a *App) migrate(ctx context.Context) error {
	const legacyDomain = "localhost"
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users(id INTEGER PRIMARY KEY AUTOINCREMENT,domain TEXT,email TEXT,password TEXT,is_admin INTEGER,UNIQUE(domain,email));`,
		`CREATE TABLE IF NOT EXISTS sessions(token TEXT PRIMARY KEY,user_email TEXT,created_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS pages(domain TEXT,path TEXT,title TEXT,html TEXT,published INTEGER,PRIMARY KEY(domain,path));`,
		`CREATE TABLE IF NOT EXISTS published_pages(domain TEXT,path TEXT,title TEXT,html TEXT,PRIMARY KEY(domain,path));`,
		`CREATE TABLE IF NOT EXISTS page_redirects(domain TEXT,old_path TEXT,new_path TEXT,created_at TEXT,PRIMARY KEY(domain,old_path));`,
		`CREATE TABLE IF NOT EXISTS domain_storage_usage(domain TEXT PRIMARY KEY,page_bytes INTEGER DEFAULT 0,published_page_bytes INTEGER DEFAULT 0,revision_bytes INTEGER DEFAULT 0,file_bytes INTEGER DEFAULT 0,published_static_bytes INTEGER DEFAULT 0,limit_bytes INTEGER DEFAULT 10737418240,updated_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS auth_ip_failures(domain TEXT,client_ip TEXT,failure_count INTEGER DEFAULT 0,blocked_until TEXT,hard_locked INTEGER DEFAULT 0,last_failed_at TEXT,last_attempt_at TEXT,PRIMARY KEY(domain,client_ip));`,
		`CREATE TABLE IF NOT EXISTS revisions(id INTEGER PRIMARY KEY AUTOINCREMENT,domain TEXT,page_path TEXT,html TEXT,created_at TEXT,is_active INTEGER DEFAULT 1);`,
		`CREATE TABLE IF NOT EXISTS domain_aliases(primary_domain TEXT,alias_domain TEXT UNIQUE,verification_token TEXT,is_verified INTEGER DEFAULT 0,dns_a_ok INTEGER DEFAULT 0,is_selected INTEGER DEFAULT 0,last_checked_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS domain_states(domain TEXT PRIMARY KEY,is_frozen INTEGER DEFAULT 0);`,
		`CREATE TABLE IF NOT EXISTS domain_ssl_settings(domain TEXT PRIMARY KEY,auto_ssl_enabled INTEGER DEFAULT 0,manually_disabled INTEGER DEFAULT 0,last_checked_at TEXT,last_failed_at TEXT,last_failure_reason TEXT,retry_after TEXT);`,
		`CREATE TABLE IF NOT EXISTS page_password_rules(domain TEXT,path TEXT,password_hash TEXT,created_at TEXT,updated_at TEXT,PRIMARY KEY(domain,path));`,
		`CREATE TABLE IF NOT EXISTS page_password_sessions(token TEXT PRIMARY KEY,domain TEXT,path TEXT,created_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS domain_chroot_locations(domain TEXT,url_path TEXT,directory_path TEXT,updated_at TEXT,PRIMARY KEY(domain,url_path));`,
		`CREATE TABLE IF NOT EXISTS domain_backup_tokens(domain TEXT PRIMARY KEY,token TEXT,updated_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS file_access_rules(domain TEXT,file_name TEXT,access_mode TEXT,token TEXT,expires_at TEXT,single_use_left INTEGER DEFAULT 0,token_use_count INTEGER DEFAULT 0,PRIMARY KEY(domain,file_name));`,
		`CREATE TABLE IF NOT EXISTS file_metadata(domain TEXT,file_name TEXT,page_path TEXT,size INTEGER,mime_type TEXT,created_at TEXT,updated_at TEXT,source TEXT,download_count INTEGER DEFAULT 0,PRIMARY KEY(domain,file_name));`,
		`CREATE TABLE IF NOT EXISTS email_confirmations(token TEXT PRIMARY KEY,domain TEXT,action TEXT,email TEXT,password TEXT,current_email TEXT,return_path TEXT,language_code TEXT,created_at TEXT,expires_at TEXT);`,
		`CREATE TABLE IF NOT EXISTS analytics_events(id INTEGER PRIMARY KEY AUTOINCREMENT,domain TEXT,path TEXT,query TEXT,method TEXT,status_code INTEGER,content_source TEXT,occurred_at TEXT,duration_ms INTEGER,client_ip TEXT,remote_addr TEXT,user_agent TEXT,referer TEXT,accept_language TEXT,geo_country_code TEXT,geo_city TEXT,geo_latitude REAL DEFAULT 0,geo_longitude REAL DEFAULT 0,geo_source TEXT,visitor_id TEXT,is_admin INTEGER DEFAULT 0,is_asset INTEGER DEFAULT 0,is_controller INTEGER DEFAULT 0);`,
		`CREATE INDEX IF NOT EXISTS idx_analytics_events_domain_time ON analytics_events(domain,occurred_at);`,
		`CREATE INDEX IF NOT EXISTS idx_analytics_events_visitor ON analytics_events(domain,visitor_id,occurred_at);`,
		`CREATE TABLE IF NOT EXISTS analytics_reports(domain TEXT PRIMARY KEY,generated_at TEXT,period_start TEXT,period_end TEXT,event_count INTEGER,report_json TEXT);`,
	}
	for _, query := range queries {
		if _, err := a.db.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE users ADD COLUMN domain TEXT`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE pages ADD COLUMN domain TEXT`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE revisions ADD COLUMN domain TEXT`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE revisions ADD COLUMN is_active INTEGER DEFAULT 1`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE domain_aliases ADD COLUMN verification_token TEXT`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE domain_aliases ADD COLUMN is_verified INTEGER DEFAULT 0`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE domain_aliases ADD COLUMN dns_a_ok INTEGER DEFAULT 0`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE domain_aliases ADD COLUMN is_selected INTEGER DEFAULT 0`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE domain_aliases ADD COLUMN last_checked_at TEXT`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE domain_ssl_settings ADD COLUMN auto_ssl_enabled INTEGER DEFAULT 0`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE domain_ssl_settings ADD COLUMN manually_disabled INTEGER DEFAULT 0`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE domain_ssl_settings ADD COLUMN last_checked_at TEXT`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE domain_ssl_settings ADD COLUMN last_failed_at TEXT`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE domain_ssl_settings ADD COLUMN last_failure_reason TEXT`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE domain_ssl_settings ADD COLUMN retry_after TEXT`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE page_password_rules ADD COLUMN created_at TEXT`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE page_password_rules ADD COLUMN updated_at TEXT`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE file_access_rules ADD COLUMN token_use_count INTEGER DEFAULT 0`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE file_metadata ADD COLUMN download_count INTEGER DEFAULT 0`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE analytics_events ADD COLUMN geo_country_code TEXT`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE analytics_events ADD COLUMN geo_city TEXT`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE analytics_events ADD COLUMN geo_latitude REAL DEFAULT 0`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE analytics_events ADD COLUMN geo_longitude REAL DEFAULT 0`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE analytics_events ADD COLUMN geo_source TEXT`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE domain_storage_usage ADD COLUMN page_bytes INTEGER DEFAULT 0`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE domain_storage_usage ADD COLUMN published_page_bytes INTEGER DEFAULT 0`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE domain_storage_usage ADD COLUMN revision_bytes INTEGER DEFAULT 0`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE domain_storage_usage ADD COLUMN file_bytes INTEGER DEFAULT 0`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE domain_storage_usage ADD COLUMN published_static_bytes INTEGER DEFAULT 0`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE domain_storage_usage ADD COLUMN limit_bytes INTEGER DEFAULT 10737418240`)
	_, _ = a.db.ExecContext(ctx, `ALTER TABLE domain_storage_usage ADD COLUMN updated_at TEXT`)
	_, _ = a.db.ExecContext(ctx, `UPDATE users SET domain=? WHERE domain IS NULL OR TRIM(domain)=''`, legacyDomain)
	_, _ = a.db.ExecContext(ctx, `UPDATE pages SET domain=? WHERE domain IS NULL OR TRIM(domain)=''`, legacyDomain)
	_, _ = a.db.ExecContext(ctx, `UPDATE revisions SET domain=? WHERE domain IS NULL OR TRIM(domain)=''`, legacyDomain)
	a.migrateLoopbackDomainsToLocalhost(ctx)
	_, _ = a.db.ExecContext(ctx, `UPDATE revisions SET is_active=1 WHERE is_active IS NULL`)
	_, _ = a.db.ExecContext(ctx, `UPDATE domain_storage_usage SET limit_bytes=? WHERE limit_bytes IS NULL OR limit_bytes<=0`, defaultDomainStorageLimitBytes)
	a.assignMissingDomainAliasTokens(ctx)
	_, _ = a.db.ExecContext(ctx, `
		INSERT INTO published_pages(domain,path,title,html)
		SELECT p.domain,p.path,p.title,p.html
		FROM pages AS p
		WHERE p.published=1
		AND NOT EXISTS (
			SELECT 1 FROM published_pages AS pp WHERE pp.domain=p.domain AND pp.path=p.path
		)
	`)
	a.rebuildAllDomainStorageUsage(ctx)
	a.rebuildPagePasswordPrefixFiles(ctx)
	return nil
}

func (a *App) migrateLoopbackDomainsToLocalhost(ctx context.Context) {
	legacyLoopbackDomains := []string{"127.0.0.1", "::1", "[::1]"}
	placeholders := "(" + strings.TrimSuffix(strings.Repeat("?,", len(legacyLoopbackDomains)), ",") + ")"
	sqlArguments := make([]any, 0, len(legacyLoopbackDomains))
	for _, legacyLoopbackDomain := range legacyLoopbackDomains {
		sqlArguments = append(sqlArguments, legacyLoopbackDomain)
	}
	usersMergeQuery := `INSERT OR IGNORE INTO users(domain,email,password,is_admin)
SELECT 'localhost',email,password,is_admin FROM users WHERE domain IN ` + placeholders
	_, _ = a.db.ExecContext(ctx, usersMergeQuery, sqlArguments...)
	pagesMergeQuery := `INSERT OR IGNORE INTO pages(domain,path,title,html,published)
SELECT 'localhost',path,title,html,published FROM pages WHERE domain IN ` + placeholders
	_, _ = a.db.ExecContext(ctx, pagesMergeQuery, sqlArguments...)
	publishedPagesMergeQuery := `INSERT OR IGNORE INTO published_pages(domain,path,title,html)
SELECT 'localhost',path,title,html FROM published_pages WHERE domain IN ` + placeholders
	_, _ = a.db.ExecContext(ctx, publishedPagesMergeQuery, sqlArguments...)
	fileAccessRulesMergeQuery := `INSERT OR IGNORE INTO file_access_rules(domain,file_name,access_mode,token,expires_at,single_use_left,token_use_count)
SELECT 'localhost',file_name,access_mode,token,expires_at,single_use_left,token_use_count FROM file_access_rules WHERE domain IN ` + placeholders
	_, _ = a.db.ExecContext(ctx, fileAccessRulesMergeQuery, sqlArguments...)
	fileMetadataMergeQuery := `INSERT OR IGNORE INTO file_metadata(domain,file_name,page_path,size,mime_type,created_at,updated_at,source,download_count)
SELECT 'localhost',file_name,page_path,size,mime_type,created_at,updated_at,source,download_count FROM file_metadata WHERE domain IN ` + placeholders
	_, _ = a.db.ExecContext(ctx, fileMetadataMergeQuery, sqlArguments...)
	pageRedirectsMergeQuery := `INSERT OR IGNORE INTO page_redirects(domain,old_path,new_path,created_at)
SELECT 'localhost',old_path,new_path,created_at FROM page_redirects WHERE domain IN ` + placeholders
	_, _ = a.db.ExecContext(ctx, pageRedirectsMergeQuery, sqlArguments...)
	storageUsageMergeQuery := `INSERT OR IGNORE INTO domain_storage_usage(domain,page_bytes,published_page_bytes,revision_bytes,file_bytes,published_static_bytes,limit_bytes,updated_at)
SELECT 'localhost',page_bytes,published_page_bytes,revision_bytes,file_bytes,published_static_bytes,limit_bytes,updated_at FROM domain_storage_usage WHERE domain IN ` + placeholders
	_, _ = a.db.ExecContext(ctx, storageUsageMergeQuery, sqlArguments...)
	domainStatesMergeQuery := `INSERT OR IGNORE INTO domain_states(domain,is_frozen)
SELECT 'localhost',is_frozen FROM domain_states WHERE domain IN ` + placeholders
	_, _ = a.db.ExecContext(ctx, domainStatesMergeQuery, sqlArguments...)
	domainSSLSettingsMergeQuery := `INSERT OR IGNORE INTO domain_ssl_settings(domain,auto_ssl_enabled,manually_disabled,last_checked_at,last_failed_at,last_failure_reason,retry_after)
SELECT 'localhost',auto_ssl_enabled,manually_disabled,last_checked_at,last_failed_at,last_failure_reason,retry_after FROM domain_ssl_settings WHERE domain IN ` + placeholders
	_, _ = a.db.ExecContext(ctx, domainSSLSettingsMergeQuery, sqlArguments...)
	pagePasswordRulesMergeQuery := `INSERT OR IGNORE INTO page_password_rules(domain,path,password_hash,created_at,updated_at)
SELECT 'localhost',path,password_hash,created_at,updated_at FROM page_password_rules WHERE domain IN ` + placeholders
	_, _ = a.db.ExecContext(ctx, pagePasswordRulesMergeQuery, sqlArguments...)
	pagePasswordSessionsMergeQuery := `INSERT OR IGNORE INTO page_password_sessions(token,domain,path,created_at)
SELECT token,'localhost',path,created_at FROM page_password_sessions WHERE domain IN ` + placeholders
	_, _ = a.db.ExecContext(ctx, pagePasswordSessionsMergeQuery, sqlArguments...)

	_, _ = a.db.ExecContext(ctx, `UPDATE revisions SET domain='localhost' WHERE domain IN `+placeholders, sqlArguments...)
	_, _ = a.db.ExecContext(ctx, `UPDATE domain_aliases SET primary_domain='localhost' WHERE primary_domain IN `+placeholders, sqlArguments...)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM domain_aliases WHERE alias_domain IN `+placeholders, sqlArguments...)

	_, _ = a.db.ExecContext(ctx, `DELETE FROM users WHERE domain IN `+placeholders, sqlArguments...)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM pages WHERE domain IN `+placeholders, sqlArguments...)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM published_pages WHERE domain IN `+placeholders, sqlArguments...)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM page_redirects WHERE domain IN `+placeholders, sqlArguments...)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM file_access_rules WHERE domain IN `+placeholders, sqlArguments...)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM file_metadata WHERE domain IN `+placeholders, sqlArguments...)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM domain_storage_usage WHERE domain IN `+placeholders, sqlArguments...)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM domain_states WHERE domain IN `+placeholders, sqlArguments...)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM domain_ssl_settings WHERE domain IN `+placeholders, sqlArguments...)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM page_password_rules WHERE domain IN `+placeholders, sqlArguments...)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM page_password_sessions WHERE domain IN `+placeholders, sqlArguments...)
}

func (a *App) autoCertHostPolicy(ctx context.Context, host string) error {
	certificateDomain := normalizeDomainName(host)
	if certificateDomain == "" {
		err := fmt.Errorf("invalid certificate host %q", host)
		a.logDomainEvent("localhost", "AUTOCERT host policy rejected host=%s error=%v", host, err)
		return err
	}
	domainContext := contextWithDomain(ctx, certificateDomain)
	if !a.automaticSSLAvailable {
		err := fmt.Errorf("automatic SSL is unavailable because Sitebrush is not listening on ports 80 and 443")
		a.logDomainEvent(certificateDomain, "AUTOCERT host policy rejected host=%s error=%v", host, err)
		return err
	}
	setting := a.domainAutomaticSSLSetting(domainContext, certificateDomain)
	if setting.ManuallyDisabled {
		err := fmt.Errorf("automatic SSL is manually disabled for %q", certificateDomain)
		a.logDomainEvent(certificateDomain, "AUTOCERT host policy rejected host=%s error=%v", host, err)
		return err
	}
	now := time.Now()
	if a.autoCertCachedCertificateReusable(certificateDomain, now) {
		a.logDomainEvent(certificateDomain, "AUTOCERT host policy accepted host=%s from existing certificate cache with more than %s remaining", host, automaticSSLCertificateRenewBefore)
		return nil
	}
	if err := autoCertDomainEligibilityError(certificateDomain); err != nil {
		a.recordDomainAutomaticSSLFailure(domainContext, certificateDomain, err.Error(), automaticSSLRetryDelayEligibility)
		a.logDomainEvent(certificateDomain, "AUTOCERT host policy rejected host=%s error=%v", host, err)
		return err
	}
	if err := a.autoCertRetryDelayError(domainContext, certificateDomain, now); err != nil {
		a.logDomainEvent(certificateDomain, "AUTOCERT host policy rejected host=%s error=%v", host, err)
		return err
	}
	if !a.domainIsAutomaticSSLCandidate(domainContext, certificateDomain) {
		err := fmt.Errorf("automatic SSL domain %q is not managed by Sitebrush", certificateDomain)
		a.recordDomainAutomaticSSLFailure(domainContext, certificateDomain, err.Error(), automaticSSLRetryDelayEligibility)
		a.logDomainEvent(certificateDomain, "AUTOCERT host policy rejected host=%s error=%v", host, err)
		return err
	}
	serverIPs, _, err := detectServerIPCandidates(ctx)
	if err != nil || len(serverIPs) == 0 {
		if err == nil {
			err = errors.New("no public server IP addresses detected")
		}
		a.recordDomainAutomaticSSLFailure(domainContext, certificateDomain, err.Error(), automaticSSLRetryDelayDNS)
		a.logDomainEvent(certificateDomain, "AUTOCERT host policy rejected host=%s error=%v", host, err)
		return err
	}
	setting, err = a.precheckDomainAutomaticSSL(domainContext, certificateDomain, serverIPs)
	if err != nil {
		a.logDomainEvent(certificateDomain, "AUTOCERT host policy rejected host=%s error=%v", host, err)
		return err
	}
	if !setting.Enabled || setting.ManuallyDisabled {
		err := fmt.Errorf("automatic SSL DNS check failed for %q", certificateDomain)
		a.recordDomainAutomaticSSLFailure(domainContext, certificateDomain, err.Error(), automaticSSLRetryDelayDNS)
		a.logDomainEvent(certificateDomain, "AUTOCERT host policy rejected host=%s error=%v", host, err)
		return err
	}
	a.clearDomainAutomaticSSLFailure(domainContext, certificateDomain)
	a.logDomainEvent(certificateDomain, "AUTOCERT host policy accepted host=%s", host)
	return nil
}

func (a *App) autoCertCachedCertificateValid(domain string, now time.Time) bool {
	return a.autoCertCachedCertificateValidForDuration(domain, now, 0)
}

func (a *App) autoCertCachedCertificateReusable(domain string, now time.Time) bool {
	return a.autoCertCachedCertificateValidForDuration(domain, now, automaticSSLCertificateRenewBefore)
}

func (a *App) autoCertCachedCertificateValidForDuration(domain string, now time.Time, minimumRemaining time.Duration) bool {
	certificateDomain := normalizeDomainName(domain)
	if certificateDomain == "" {
		return false
	}
	certificateCacheDir := filepath.Join(a.storageRootDir(), "letsencrypt")
	cacheNames := []string{certificateDomain, certificateDomain + "+rsa"}
	for _, cacheName := range cacheNames {
		cacheBytes, err := os.ReadFile(filepath.Join(certificateCacheDir, cacheName))
		if err != nil {
			continue
		}
		if _, ok := cachedCertificatePEMExpiresForDomain(cacheBytes, certificateDomain, now, minimumRemaining); ok {
			return true
		}
	}
	return false
}

func (a *App) autoCertCachedCertificate(domain string, now time.Time, minimumRemaining time.Duration) (*tls.Certificate, time.Time, bool) {
	certificateDomain := normalizeDomainName(domain)
	if certificateDomain == "" {
		return nil, time.Time{}, false
	}
	certificateCacheDir := filepath.Join(a.storageRootDir(), "letsencrypt")
	cacheNames := []string{certificateDomain, certificateDomain + "+rsa"}
	for _, cacheName := range cacheNames {
		cacheBytes, err := os.ReadFile(filepath.Join(certificateCacheDir, cacheName))
		if err != nil {
			continue
		}
		expiresAt, ok := cachedCertificatePEMExpiresForDomain(cacheBytes, certificateDomain, now, minimumRemaining)
		if !ok {
			continue
		}
		certificate, err := tls.X509KeyPair(cacheBytes, cacheBytes)
		if err != nil {
			continue
		}
		return &certificate, expiresAt, true
	}
	return nil, time.Time{}, false
}

func cachedCertificatePEMValidForDomain(certificateBytes []byte, domain string, now time.Time) bool {
	_, ok := cachedCertificatePEMExpiresForDomain(certificateBytes, domain, now, 0)
	return ok
}

func cachedCertificatePEMExpiresForDomain(certificateBytes []byte, domain string, now time.Time, minimumRemaining time.Duration) (time.Time, bool) {
	certificateDomain := normalizeDomainName(domain)
	if certificateDomain == "" {
		return time.Time{}, false
	}
	remainingBytes := certificateBytes
	for {
		var pemBlock *pem.Block
		pemBlock, remainingBytes = pem.Decode(remainingBytes)
		if pemBlock == nil {
			return time.Time{}, false
		}
		if pemBlock.Type != "CERTIFICATE" {
			continue
		}
		certificate, err := x509.ParseCertificate(pemBlock.Bytes)
		if err != nil {
			continue
		}
		if now.Before(certificate.NotBefore) || !now.Before(certificate.NotAfter) {
			continue
		}
		if minimumRemaining > 0 && certificate.NotAfter.Before(now.Add(minimumRemaining)) {
			continue
		}
		if certificate.VerifyHostname(certificateDomain) == nil {
			return certificate.NotAfter, true
		}
	}
}

func (a *App) assignMissingDomainAliasTokens(ctx context.Context) {
	aliasRows, err := a.db.QueryContext(ctx, `SELECT alias_domain FROM domain_aliases WHERE verification_token IS NULL OR TRIM(verification_token)=''`)
	if err != nil {
		return
	}
	defer aliasRows.Close()
	for aliasRows.Next() {
		var aliasDomain string
		if scanErr := aliasRows.Scan(&aliasDomain); scanErr != nil {
			continue
		}
		_, _ = a.db.ExecContext(ctx, `UPDATE domain_aliases SET verification_token=? WHERE alias_domain=?`, randomAccessToken(), aliasDomain)
	}
}

func (a *App) route(w http.ResponseWriter, r *http.Request) {
	pagePath := cleanPath(r.URL.Path)
	if a.isDomainPrefixedPublicAssetPath(r) {
		a.servePublicAsset(w, r)
		return
	}
	requestDomain := domainFromContext(r.Context())
	if strings.TrimSpace(requestDomain) == "" {
		requestDomain = domainFromRequest(r)
	}
	if redirectTarget := a.canonicalTrailingSlashStaticRedirectTarget(r, requestDomain, pagePath); redirectTarget != "" {
		http.Redirect(w, r, redirectTarget, http.StatusMovedPermanently)
		return
	}
	if hasQueryFlag(r, "logout") {
		a.logout(w, r)
		return
	}
	if hasQueryFlag(r, "save") {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		a.savePage(w, r)
		return
	}
	if hasQueryFlag(r, "grab_preview") {
		a.grabPreview(w, r)
		return
	}
	if hasQueryFlag(r, "grab_events") {
		a.grabProgressEvents(w, r)
		return
	}
	if hasQueryFlag(r, "grab_ws") {
		a.grabProgressWS(w, r)
		return
	}
	if hasQueryFlag(r, "revision_restore") {
		a.restoreRevision(w, r)
		return
	}
	if hasQueryFlag(r, "revision_delete") {
		a.deleteRevision(w, r)
		return
	}
	if strings.TrimSpace(r.URL.Query().Get("delete")) != "" {
		a.deleteRevisionByQuery(w, r)
		return
	}
	if hasQueryFlag(r, "revision_toggle") {
		a.toggleRevision(w, r)
		return
	}
	if hasQueryFlag(r, "tree") {
		a.siteTreeJSON(w, r)
		return
	}
	if hasQueryFlag(r, "native_pick_files") {
		a.nativePickedFilesJSON(w, r)
		return
	}
	if hasQueryFlag(r, "native_save_backup") {
		a.nativeSaveBackupJSON(w, r)
		return
	}
	if hasQueryFlag(r, "edit") {
		a.editModePage(w, r)
		return
	}
	if hasQueryFlag(r, "visual") {
		a.editPage(w, r)
		return
	}
	if hasQueryFlag(r, "text") {
		a.editRawPage(w, r)
		return
	}
	if hasQueryFlag(r, "editraw") {
		a.editRawPage(w, r)
		return
	}
	if hasQueryFlag(r, "settings") || hasQueryFlag(r, "properties") {
		a.domainSettingsPage(w, r)
		return
	}
	if hasQueryFlag(r, "analytics") {
		a.analyticsPage(w, r)
		return
	}
	if hasQueryFlag(r, "backup_download") {
		a.downloadBackup(w, r)
		return
	}
	if hasQueryFlag(r, "backup_import") {
		a.importBackup(w, r)
		return
	}
	if hasQueryFlag(r, "profile") {
		a.profilePage(w, r)
		return
	}
	if hasQueryFlag(r, "freeze") {
		a.freezeDomain(w, r)
		return
	}
	if hasQueryFlag(r, "publish") {
		a.publishDomain(w, r)
		return
	}
	if hasQueryFlag(r, "publish_events") {
		a.publishProgressEvents(w, r)
		return
	}
	if hasQueryFlag(r, "publish_preview") {
		a.publishPreviewJSON(w, r)
		return
	}
	if hasQueryFlag(r, "files") {
		a.filesPage(w, r)
		return
	}
	if hasQueryFlag(r, "revisions") {
		a.revisionsPage(w, r)
		return
	}
	if hasQueryFlag(r, "page_password") {
		a.pagePasswordAction(w, r)
		return
	}
	if strings.TrimSpace(r.URL.Query().Get("email_confirm")) != "" {
		a.confirmEmailToken(w, r)
		return
	}
	if hasQueryFlag(r, "login") {
		a.login(w, r)
		return
	}
	if hasQueryFlag(r, "register") {
		a.registerPage(w, r)
		return
	}
	if hasQueryFlag(r, "grab") {
		if r.Method == http.MethodPost {
			a.grabPage(w, r)
			return
		}
		a.render(w, r, "missing.html", map[string]any{"Path": pagePath})
		return
	}
	if hasQueryFlag(r, "recover") {
		a.recoverPage(w, r)
		return
	}
	if hasQueryFlag(r, "captcha") {
		a.captchaImage(w, r)
		return
	}
	pagePasswordUnlockedForGuest := false
	if isGuestStaticRequest(r) {
		if rule, found := a.pagePasswordRuleFromPrefixFile(requestDomain, pagePath); found {
			failureDomainPrefix := pagePasswordFailureDomainPrefix(rule.Domain)
			clientIP := clientIPAddress(r)
			if blocked, hardLocked, blockedUntil := a.cachedAuthIPIsBlockedForDomainPrefix(failureDomainPrefix, clientIP); blocked {
				a.renderBlockedPagePasswordPrompt(w, r, requestDomain, pagePath, hardLocked, blockedUntil)
				return
			}
			if a.pagePasswordSessionValid(r, rule) {
				if a.servePublishedStaticFileFromDisk(w, r, requestDomain, pagePath, true) {
					return
				}
				pagePasswordUnlockedForGuest = true
			} else {
				a.renderPagePasswordPrompt(w, r, requestDomain, pagePath, "", http.StatusUnauthorized, time.Time{})
				return
			}
		}
		if a.servePublishedStaticFileFromDisk(w, r, requestDomain, pagePath, true) {
			return
		}
	}
	domain := a.siteDomain(r.Context(), r)
	if redirectTarget := a.canonicalTrailingSlashRedirectTarget(r, domain, pagePath); redirectTarget != "" {
		http.Redirect(w, r, redirectTarget, http.StatusMovedPermanently)
		return
	}
	if hasQueryFlag(r, "page_password_unlock") {
		a.pagePasswordUnlock(w, r, domain, pagePath)
		return
	}
	pagePasswordHandled, pagePasswordUnlocked := a.requirePagePassword(w, r, domain, pagePath)
	if pagePasswordHandled {
		return
	}
	pagePasswordUnlockedForGuest = pagePasswordUnlockedForGuest || pagePasswordUnlocked
	if isGuestStaticRequest(r) && a.servePublishedStaticFileFromDisk(w, r, domain, pagePath, true) {
		return
	}
	if a.serveDomainChrootLocation(w, r, domain, pagePath) {
		return
	}
	redirectTargetPath := a.resolvedPageRedirectPath(r.Context(), domain, pagePath)
	if redirectTargetPath != "" && redirectTargetPath != pagePath {
		http.Redirect(w, r, redirectTargetPath, http.StatusMovedPermanently)
		return
	}
	if isGuestStaticRequest(r) && !pagePasswordUnlockedForGuest {
		a.serveGuestNotFoundPage(w, r, domain, pagePath)
		return
	}
	isAdmin := a.isAdminRequest(r)
	pageRecord, err := a.findPage(r.Context(), domain, pagePath)
	if err == nil && isAdmin {
		a.serveManagedPageContent(w, r, pageRecord.Path, pageRecord.HTML, "db-draft")
		return
	}
	if !isAdmin && a.servePublishedStaticFile(w, r, domain, pagePath) {
		return
	}
	publishedPage, publishedErr := a.findPublishedPage(r.Context(), domain, pagePath)
	if publishedErr == nil {
		a.serveManagedPageContent(w, r, publishedPage.Path, publishedPage.HTML, "db-published-fallback")
		return
	}
	a.renderMissingPage(w, r, pagePath, isAdmin)
}

func (a *App) setupAdmin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	domain := a.siteDomain(r.Context(), r)
	translations := translationsForRequest(r)
	if a.hasAdmin(r.Context(), domain) {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	password := strings.TrimSpace(r.FormValue("password"))
	if email == "" || password == "" {
		w.WriteHeader(http.StatusBadRequest)
		a.renderSetupPage(w, r, domain, email, translationOrDefault(translations, "email_confirmation_status_required", "Email and password are required."))
		return
	}
	returnPath := r.FormValue("return_path")
	if returnPath == "" {
		returnPath = requestedReturnPath(r)
	}
	if err := a.createAndSendEmailConfirmation(r, "register", domain, "", email, password, returnPath); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		a.renderSetupPage(w, r, domain, email, err.Error())
		return
	}
	a.renderSetupPage(w, r, domain, email, translationOrDefault(translations, "email_confirmation_status_sent", "A confirmation link has been sent to the email address."))
}

func (a *App) registerPage(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		a.setupAdmin(w, r)
		return
	}
	domain := a.siteDomain(r.Context(), r)
	if a.hasAdmin(r.Context(), domain) {
		http.Redirect(w, r, loginURLForRequest(r), http.StatusFound)
		return
	}
	a.renderSetupPage(w, r, domain, "", "")
}

func (a *App) renderSetupPage(w http.ResponseWriter, r *http.Request, domain, email, status string) {
	a.render(w, r, "setup.html", map[string]any{"Domain": domain, "Email": strings.TrimSpace(email), "Status": strings.TrimSpace(status), "ReturnPath": requestedReturnPath(r)})
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	domain := a.siteDomain(r.Context(), r)
	if !a.hasAdmin(r.Context(), domain) {
		http.Redirect(w, r, r.URL.Path+"?register", http.StatusFound)
		return
	}
	clientIP := clientIPAddress(r)
	blocked, hardLocked, blockedUntil := a.authIPIsBlocked(r.Context(), domain, clientIP)
	if r.Method == http.MethodGet {
		returnPath := loginReturnPathOrDefault(r)
		if hardLocked {
			w.WriteHeader(http.StatusForbidden)
			a.renderLoginPage(w, r, returnPath, "", translationOrDefault(translationsForRequest(r), "login_status_hard_locked", "Too many failed attempts from this IP. Account recovery is now required."), "danger", blockedUntil, true)
			return
		}
		if blocked {
			retryAfter := int(time.Until(blockedUntil).Seconds())
			if retryAfter < 1 {
				retryAfter = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			w.WriteHeader(http.StatusTooManyRequests)
			a.renderLoginPage(w, r, returnPath, "", translationOrDefault(translationsForRequest(r), "login_status_rate_limited", "Too many failed attempts from this IP. Please try again later."), "warning", blockedUntil, false)
			return
		}
		a.renderLoginPage(w, r, returnPath, "", "", "", time.Time{}, false)
		return
	}
	email := r.FormValue("email")
	password := r.FormValue("password")
	returnPath := strings.TrimSpace(r.FormValue("return_path"))
	if returnPath == "" {
		returnPath = loginReturnPathOrDefault(r)
	}
	if hardLocked {
		w.WriteHeader(http.StatusForbidden)
		a.renderLoginPage(w, r, returnPath, email, translationOrDefault(translationsForRequest(r), "login_status_hard_locked", "Too many failed attempts from this IP. Account recovery is now required."), "danger", blockedUntil, true)
		return
	}
	if blocked {
		retryAfter := int(time.Until(blockedUntil).Seconds())
		if retryAfter < 1 {
			retryAfter = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		w.WriteHeader(http.StatusTooManyRequests)
		a.renderLoginPage(w, r, returnPath, email, translationOrDefault(translationsForRequest(r), "login_status_rate_limited", "Too many failed attempts from this IP. Please try again later."), "warning", blockedUntil, false)
		return
	}
	var matchedUsers int
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(1) FROM users WHERE domain=? AND email=? AND password=?`, domain, email, password).Scan(&matchedUsers)
	if matchedUsers == 0 {
		failureCount, blockedUntil, hardLocked := a.registerFailedLoginAttempt(r.Context(), domain, clientIP)
		translations := translationsForRequest(r)
		if hardLocked {
			w.WriteHeader(http.StatusForbidden)
			a.renderLoginPage(w, r, returnPath, email, translationOrDefault(translations, "login_status_hard_locked", "Too many failed attempts from this IP. Account recovery is now required."), "danger", blockedUntil, true)
			return
		}
		if !blockedUntil.IsZero() {
			retryAfter := int(time.Until(blockedUntil).Seconds())
			if retryAfter < 1 {
				retryAfter = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			w.WriteHeader(http.StatusTooManyRequests)
			a.renderLoginPage(w, r, returnPath, email, translationOrDefault(translations, "login_status_rate_limited", "Too many failed attempts from this IP. Please try again later."), "warning", blockedUntil, false)
			return
		}
		_ = failureCount
		w.WriteHeader(http.StatusUnauthorized)
		a.renderLoginPage(w, r, returnPath, email, translationOrDefault(translations, "login_status_invalid_credentials", "Incorrect email or password."), "danger", time.Time{}, false)
		return
	}
	a.clearFailedLoginAttempts(r.Context(), domain, clientIP)
	a.createSession(w, r, email)
	http.Redirect(w, r, returnPath, http.StatusFound)
}

func (a *App) renderLoginPage(w http.ResponseWriter, r *http.Request, returnPath, email, status, statusClass string, blockedUntil time.Time, hardLocked bool) {
	translations := translationsForRequest(r)
	a.render(w, r, "login.html", map[string]any{
		"ReturnPath":           returnPath,
		"Domain":               a.siteDomain(r.Context(), r),
		"Email":                strings.TrimSpace(email),
		"Status":               strings.TrimSpace(status),
		"StatusClass":          strings.TrimSpace(statusClass),
		"ShowForm":             strings.TrimSpace(status) == "" || (!hardLocked && blockedUntil.IsZero()),
		"BlockedUntilUnix":     blockedUntil.Unix(),
		"BlockedUntilISO":      blockedUntil.UTC().Format(time.RFC3339),
		"IsHardLocked":         hardLocked,
		"CountdownLabel":       translationOrDefault(translations, "login_retry_in", "You can try again in:"),
		"RetryAtLabel":         translationOrDefault(translations, "login_retry_at", "You can try again at:"),
		"TryAgainNowText":      translationOrDefault(translations, "login_try_again_now", "You can try again now."),
		"RecoveryRequiredText": translationOrDefault(translations, "login_recovery_required", "Use account recovery to continue."),
	})
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "sitebrush_session", Value: "", Path: "/", Expires: time.Unix(0, 0)})
	http.Redirect(w, r, requestedReturnTarget(r), http.StatusFound)
}

func (a *App) editPage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		if !a.hasAdmin(r.Context(), a.siteDomain(r.Context(), r)) {
			http.Redirect(w, r, r.URL.Path+"?register", http.StatusFound)
			return
		}
		http.Redirect(w, r, loginURLForRequest(r), http.StatusFound)
		return
	}
	pagePath := cleanPath(r.URL.Query().Get("path"))
	if pagePath == "/" && strings.TrimSpace(r.URL.Query().Get("path")) == "" {
		pagePath = cleanPath(r.URL.Path)
	}
	if pagePath == "" {
		pagePath = r.URL.Path
	}
	if pagePath == "" {
		pagePath = "/"
	}
	domain := a.siteDomain(r.Context(), r)
	record, _ := a.findPage(r.Context(), domain, pagePath)
	if record.Path != "" && pageContentKind(record.Path, record.HTML) != "html" {
		http.Redirect(w, r, pagePath+"?text", http.StatusFound)
		return
	}
	if record.Path == "" && pageContentKind(pagePath, "") != "html" {
		http.Redirect(w, r, pagePath+"?text", http.StatusFound)
		return
	}
	if record.Path == "" {
		record = Page{Path: pagePath, Title: pagePath, HTML: a.defaultHTMLForNewPage(r.Context(), domain, pagePath)}
	}
	record.NativeFileDialog = a.nativeFileDialog
	a.render(w, r, "edit.html", record)
}

func (a *App) editModePage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		if !a.hasAdmin(r.Context(), a.siteDomain(r.Context(), r)) {
			http.Redirect(w, r, r.URL.Path+"?register", http.StatusFound)
			return
		}
		http.Redirect(w, r, loginURLForRequest(r), http.StatusFound)
		return
	}
	pagePath := cleanPath(r.URL.Query().Get("path"))
	if pagePath == "/" && strings.TrimSpace(r.URL.Query().Get("path")) == "" {
		pagePath = cleanPath(r.URL.Path)
	}
	if pagePath == "" {
		pagePath = r.URL.Path
	}
	if pagePath == "" {
		pagePath = "/"
	}
	domain := a.siteDomain(r.Context(), r)
	record, _ := a.findPage(r.Context(), domain, pagePath)
	contentKind := pageContentKind(pagePath, "")
	if record.Path != "" {
		contentKind = pageContentKind(record.Path, record.HTML)
	}
	a.render(w, r, "edit_mode.html", map[string]any{"Path": pagePath, "ContentKind": contentKindLabel(contentKind), "IsHTML": contentKind == "html"})
}

func (a *App) editRawPage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		if !a.hasAdmin(r.Context(), a.siteDomain(r.Context(), r)) {
			http.Redirect(w, r, r.URL.Path+"?register", http.StatusFound)
			return
		}
		http.Redirect(w, r, loginURLForRequest(r), http.StatusFound)
		return
	}
	pagePath := cleanPath(r.URL.Query().Get("path"))
	if pagePath == "/" && strings.TrimSpace(r.URL.Query().Get("path")) == "" {
		pagePath = cleanPath(r.URL.Path)
	}
	if pagePath == "" {
		pagePath = r.URL.Path
	}
	if pagePath == "" {
		pagePath = "/"
	}
	domain := a.siteDomain(r.Context(), r)
	record, _ := a.findPage(r.Context(), domain, pagePath)
	if record.Path == "" {
		record = Page{Path: pagePath, Title: pagePath, HTML: ""}
	}
	record.ContentKind = contentKindLabel(pageContentKind(record.Path, record.HTML))
	a.render(w, r, "edit_raw.html", record)
}

func (a *App) defaultHTMLForNewPage(ctx context.Context, domain, pagePath string) string {
	ancestorPath := parentPagePath(pagePath)
	for ancestorPath != "" {
		ancestorPage, err := a.findPage(ctx, domain, ancestorPath)
		if err == nil && ancestorPage.Path != "" && strings.TrimSpace(ancestorPage.HTML) != "" {
			return ancestorPage.HTML
		}
		ancestorPath = parentPagePath(ancestorPath)
	}
	return "<h1>New page</h1>"
}

func parentPagePath(pagePath string) string {
	if pagePath == "" || pagePath == "/" {
		return ""
	}
	if strings.HasSuffix(pagePath, "/") {
		trimmedPath := strings.TrimSuffix(pagePath, "/")
		if trimmedPath == "" {
			return "/"
		}
		return trimmedPath
	}
	lastSlashIndex := strings.LastIndex(pagePath, "/")
	if lastSlashIndex <= 0 {
		return "/"
	}
	return pagePath[:lastSlashIndex]
}

func cleanPath(rawPath string) string {
	trimmedPath := strings.TrimSpace(rawPath)
	if trimmedPath == "" {
		return "/"
	}
	if !strings.HasPrefix(trimmedPath, "/") {
		trimmedPath = "/" + trimmedPath
	}
	normalizedPath := path.Clean(trimmedPath)
	if normalizedPath == "." || normalizedPath == "" {
		return "/"
	}
	return normalizedPath
}

func requestCanUseCanonicalTrailingSlash(r *http.Request, pagePath string) bool {
	if r == nil || r.URL == nil {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if strings.TrimSpace(r.URL.RawQuery) != "" {
		return false
	}
	rawPath := strings.TrimSpace(r.URL.Path)
	if rawPath == "" || rawPath == "/" || strings.HasSuffix(rawPath, "/") {
		return false
	}
	if pagePath == "/" || strings.HasPrefix(pagePath, "/p/") {
		return false
	}
	if path.Ext(path.Base(pagePath)) != "" {
		return false
	}
	return true
}

func canonicalTrailingSlashURL(r *http.Request, pagePath string) string {
	redirectURL := url.URL{}
	redirectURL.Path = pagePath + "/"
	redirectURL.RawQuery = r.URL.RawQuery
	return redirectURL.String()
}

func (a *App) canonicalTrailingSlashStaticRedirectTarget(r *http.Request, domain, pagePath string) string {
	if !requestCanUseCanonicalTrailingSlash(r, pagePath) {
		return ""
	}
	if _, found := a.pagePasswordRuleFromPrefixFile(domain, pagePath); found {
		return ""
	}
	if !a.publishedStaticPageExists(domain, pagePath) {
		return ""
	}
	return canonicalTrailingSlashURL(r, pagePath)
}

func (a *App) canonicalTrailingSlashRedirectTarget(r *http.Request, domain, pagePath string) string {
	if !requestCanUseCanonicalTrailingSlash(r, pagePath) {
		return ""
	}
	if redirectTargetPath := a.resolvedPageRedirectPath(r.Context(), domain, pagePath); redirectTargetPath != "" && redirectTargetPath != pagePath {
		return ""
	}
	if _, found := a.pagePasswordRuleFromPrefixFile(domain, pagePath); found {
		return ""
	}
	if !a.canonicalTrailingSlashResourceExists(r.Context(), domain, pagePath) {
		return ""
	}
	return canonicalTrailingSlashURL(r, pagePath)
}

func (a *App) canonicalTrailingSlashResourceExists(ctx context.Context, domain, pagePath string) bool {
	if a.publishedStaticPageExists(domain, pagePath) {
		return true
	}
	var storedPageCount int
	_ = a.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM pages WHERE domain=? AND path=?`, domain, pagePath).Scan(&storedPageCount)
	if storedPageCount > 0 {
		return true
	}
	_ = a.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM published_pages WHERE domain=? AND path=?`, domain, pagePath).Scan(&storedPageCount)
	if storedPageCount > 0 {
		return true
	}
	return a.chrootResourceExists(ctx, domain, pagePath)
}

func loginReturnPathOrDefault(r *http.Request) string {
	returnPath := strings.TrimSpace(r.URL.Query().Get("return_path"))
	if returnPath != "" {
		return returnPath
	}
	return requestedReturnTarget(r)
}

func hasQueryFlag(r *http.Request, flagName string) bool {
	if strings.TrimSpace(r.URL.RawQuery) == flagName {
		return true
	}
	_, hasFlag := r.URL.Query()[flagName]
	return hasFlag
}

func requestedReturnTarget(r *http.Request) string {
	returnPath := r.URL.Path
	if strings.TrimSpace(returnPath) == "" {
		returnPath = "/"
	}
	queryValues := r.URL.Query()
	queryValues.Del("login")
	queryValues.Del("logout")
	queryValues.Del("return_path")
	encodedQuery := queryValues.Encode()
	if encodedQuery == "" {
		return returnPath
	}
	return returnPath + "?" + encodedQuery
}

func loginURLForRequest(r *http.Request) string {
	loginURL := &url.URL{Path: r.URL.Path}
	queryValues := url.Values{}
	queryValues.Set("login", "")
	queryValues.Set("return_path", requestedReturnTarget(r))
	loginURL.RawQuery = queryValues.Encode()
	return loginURL.String()
}

func (a *App) saveEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		a.savePage(w, r)
		return
	}
	a.route(w, r)
}

func (a *App) savePage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) || r.Method != http.MethodPost {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	pagePath := cleanPath(r.FormValue("path"))
	previousPath := pagePath
	if strings.TrimSpace(r.FormValue("previous_path")) != "" {
		previousPath = cleanPath(r.FormValue("previous_path"))
	}
	domain := a.siteDomain(r.Context(), r)
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = pagePath
	}
	html := r.FormValue("html")
	newHTMLBytes := int64(len([]byte(html)))
	var previousStoredHTML string
	_ = a.db.QueryRowContext(r.Context(), `SELECT html FROM pages WHERE domain=? AND path=?`, domain, pagePath).Scan(&previousStoredHTML)
	pageDelta := newHTMLBytes - int64(len([]byte(previousStoredHTML)))
	publishedPageDelta := int64(0)
	publishedStaticDelta := int64(0)
	if !a.isDomainFrozen(r.Context(), domain) {
		var previousPublishedHTML string
		_ = a.db.QueryRowContext(r.Context(), `SELECT html FROM published_pages WHERE domain=? AND path=?`, domain, pagePath).Scan(&previousPublishedHTML)
		publishedPageDelta = newHTMLBytes - int64(len([]byte(previousPublishedHTML)))
		publishedStaticPath := filepath.Join(a.domainStaticDir(domain), staticRelativePathForPage(pagePath))
		publishedStaticDelta = newHTMLBytes - fileSizeBytes(publishedStaticPath)
	}
	if storageErr := a.applyDomainStorageDelta(r.Context(), domain, pageDelta, publishedPageDelta, newHTMLBytes, 0, publishedStaticDelta); storageErr != nil {
		http.Error(w, storageErr.Error(), http.StatusInsufficientStorage)
		return
	}
	a.clearPageRedirectSource(r.Context(), domain, pagePath)
	_, _ = a.db.ExecContext(r.Context(), `INSERT OR REPLACE INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, domain, pagePath, title, html)
	if !a.isDomainFrozen(r.Context(), domain) {
		_, _ = a.db.ExecContext(r.Context(), `INSERT OR REPLACE INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, domain, pagePath, title, html)
		a.writePublishedStaticHTML(domain, pagePath, html)
	}
	_, _ = a.db.ExecContext(r.Context(), `INSERT INTO revisions(domain,page_path,html,created_at) VALUES(?,?,?,?)`, domain, pagePath, html, time.Now().Format(time.RFC3339))
	if previousPath != pagePath {
		a.registerPageRedirect(r.Context(), domain, previousPath, pagePath)
	}
	if pageContentKind(pagePath, html) == "html" {
		a.applyTemplateClassSynchronization(r.Context(), domain, previousStoredHTML, html)
		a.applyTemplatePropagation(r.Context(), domain, html)
	}
	http.Redirect(w, r, pagePath, http.StatusFound)
}

func (a *App) grabPage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) || r.Method != http.MethodPost {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	pagePath := grabRequestTargetPath(r)

	sourceURL := r.FormValue("source_url")
	if sourceURL == "" {
		http.Error(w, "source_url is required", http.StatusBadRequest)
		return
	}
	sourceOptions, err := parseGrabSourceOptions(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	remoteSourceURL, err := parseGrabSourceURLForServerIP(sourceURL, sourceOptions.IP)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sourceURL = remoteSourceURL.String()

	htmlBytes, resolvedSourceURL, err := downloadGrabSourceHTMLWithResolvedURL(sourceURL, sourceOptions)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	remoteSourceURL = resolvedSourceURL
	sourceURL = remoteSourceURL.String()

	domain := a.siteDomain(r.Context(), r)
	progressToken := strings.TrimSpace(r.FormValue("progress_token"))
	if grabCopyWholeSite(r) {
		selectedResourceURLs := selectedGrabResourceURLs(r)
		redirectPath, importErr := a.importWholeRemoteSite(r.Context(), domain, pagePath, remoteSourceURL, string(htmlBytes), progressToken, selectedResourceURLs, sourceOptions)
		if importErr != nil {
			statusCode := http.StatusBadGateway
			if strings.Contains(importErr.Error(), "storage limit reached:") {
				statusCode = http.StatusInsufficientStorage
			}
			http.Error(w, importErr.Error(), statusCode)
			return
		}
		if wantsJSONResponse(r) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"redirect": redirectPath})
			return
		}
		http.Redirect(w, r, redirectPath, http.StatusFound)
		return
	}
	selectedResourceURLs := selectedGrabResourceURLs(r)
	spider, html := prepareSinglePageImport(domain, pagePath, sourceURL, remoteSourceURL, string(htmlBytes), a.grabTracker, progressToken, selectedResourceURLs, sourceOptions)
	importedPages := []wholeSiteImportedPage{{SourceURL: sourceURL, LocalPath: pagePath, HTML: html}}
	pageDelta, publishedPageDelta, revisionDelta, publishedStaticDelta := a.estimateImportedPagesStorageDelta(r.Context(), domain, importedPages)
	fileDelta := a.estimateImportedFileDelta(domain, spider)
	if storageErr := a.applyDomainStorageDelta(r.Context(), domain, pageDelta, publishedPageDelta, revisionDelta, fileDelta, publishedStaticDelta); storageErr != nil {
		http.Error(w, storageErr.Error(), http.StatusInsufficientStorage)
		return
	}
	if persistErr := a.persistSpiderAssets(spider, pagePath); persistErr != nil {
		_ = a.applyDomainStorageDelta(r.Context(), domain, -pageDelta, -publishedPageDelta, -revisionDelta, -fileDelta, -publishedStaticDelta)
		http.Error(w, persistErr.Error(), http.StatusBadGateway)
		return
	}
	a.clearPageRedirectSource(r.Context(), domain, pagePath)
	_, _ = a.db.ExecContext(r.Context(), `INSERT OR REPLACE INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, domain, pagePath, pagePath, html)
	if !a.isDomainFrozen(r.Context(), domain) {
		_, _ = a.db.ExecContext(r.Context(), `INSERT OR REPLACE INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, domain, pagePath, pagePath, html)
	}
	_, _ = a.db.ExecContext(r.Context(), `INSERT INTO revisions(domain,page_path,html,created_at) VALUES(?,?,?,?)`, domain, pagePath, html, time.Now().Format(time.RFC3339))
	if wantsJSONResponse(r) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"redirect": pagePath})
		return
	}
	http.Redirect(w, r, pagePath, http.StatusFound)
}

func (a *App) grabPreview(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) || r.Method != http.MethodPost {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	sourceURL := r.FormValue("source_url")
	if sourceURL == "" {
		http.Error(w, "source_url is required", http.StatusBadRequest)
		return
	}
	sourceOptions, err := parseGrabSourceOptions(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	remoteSourceURL, err := parseGrabSourceURLForServerIP(sourceURL, sourceOptions.IP)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sourceURL = remoteSourceURL.String()
	htmlBytes, resolvedSourceURL, err := downloadGrabSourceHTMLWithResolvedURL(sourceURL, sourceOptions)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	remoteSourceURL = resolvedSourceURL
	sourceURL = remoteSourceURL.String()
	progressToken := strings.TrimSpace(r.FormValue("progress_token"))
	domain := a.siteDomain(r.Context(), r)
	pagePath := grabRequestTargetPath(r)
	var resources []grabResourcePreview
	pageCount := 1
	var importedPages []wholeSiteImportedPage
	var pageDownloadBytes int64
	var previewSpider *pageSpider
	if grabCopyWholeSite(r) {
		wholeSitePreview := previewWholeRemoteSiteResources(remoteSourceURL, string(htmlBytes), pagePath, a.grabTracker, progressToken, sourceOptions)
		resources = wholeSitePreview.Resources
		pageCount = wholeSitePreview.PageCount
		importedPages = wholeSitePreview.ImportedPages
		previewSpider = wholeSitePreview.Spider
	} else {
		previewSpider, importedHTML := prepareSinglePageImport(domain, pagePath, sourceURL, remoteSourceURL, string(htmlBytes), a.grabTracker, progressToken, nil, sourceOptions)
		resources = previewResourcesFromSpider(previewSpider, map[string]struct{}{sourceURL: {}})
		importedPages = []wholeSiteImportedPage{{SourceURL: sourceURL, LocalPath: pagePath, HTML: importedHTML}}
		if a.grabTracker != nil && progressToken != "" {
			a.grabTracker.publish(grabProgressEvent{Token: progressToken, Stage: "done", FoundTotal: previewSpider.foundTotal, DownloadedTotal: previewSpider.downloadedTotal, CompletedPercent: 100})
		}
	}
	for _, importedPage := range importedPages {
		pageDownloadBytes += int64(len([]byte(importedPage.HTML)))
	}
	selectedResourceBytes := sumGrabPreviewResourceBytes(resources)
	quotaEstimate := a.estimateImportQuota(r.Context(), domain, importedPages, previewSpider)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(grabPreviewResponse{
		SourceURL:             remoteSourceURL.String(),
		PageCount:             pageCount,
		ResourceCount:         len(resources),
		Resources:             resources,
		PageDownloadBytes:     pageDownloadBytes,
		PageStorageBytes:      quotaEstimate.PageStorageBytes,
		CurrentUsedBytes:      quotaEstimate.CurrentUsedBytes,
		LimitBytes:            quotaEstimate.LimitBytes,
		FreeBytes:             quotaEstimate.FreeBytes,
		SelectedResourceBytes: selectedResourceBytes,
		EstimatedImportBytes:  quotaEstimate.EstimatedImportBytes,
		ProjectedUsedBytes:    quotaEstimate.ProjectedUsedBytes,
		FitsQuota:             quotaEstimate.FitsQuota,
	})
}

func parseGrabSourceURL(sourceURL string) (*url.URL, error) {
	return parseGrabSourceURLForServerIP(sourceURL, "")
}

func grabRequestTargetPath(r *http.Request) string {
	rawPath := strings.TrimSpace(r.FormValue("path"))
	if rawPath == "" {
		rawPath = r.URL.Path
	}
	return cleanPath(rawPath)
}

func parseGrabSourceURLForServerIP(sourceURL, sourceIP string) (*url.URL, error) {
	trimmedSourceURL := strings.TrimSpace(sourceURL)
	if trimmedSourceURL == "" {
		return nil, errors.New("source_url is required")
	}
	sourceIPPort := grabSourceIPPort(sourceIP)
	if strings.HasPrefix(trimmedSourceURL, "//") {
		trimmedSourceURL = "https:" + trimmedSourceURL
	}
	if !strings.Contains(trimmedSourceURL, "://") {
		trimmedSourceURL = defaultGrabSchemeForServerIP(trimmedSourceURL, sourceIP) + "://" + trimmedSourceURL
	}
	remoteSourceURL, err := url.Parse(trimmedSourceURL)
	if err != nil || remoteSourceURL.Hostname() == "" || (remoteSourceURL.Scheme != "http" && remoteSourceURL.Scheme != "https") {
		return nil, errors.New("source_url is invalid")
	}
	if sourceIPPort != "" {
		remoteSourceURL.Host = net.JoinHostPort(remoteSourceURL.Hostname(), sourceIPPort)
	}
	return remoteSourceURL, nil
}

func defaultGrabScheme(sourceURL string) string {
	return defaultGrabSchemeForServerIP(sourceURL, "")
}

func defaultGrabSchemeForServerIP(sourceURL, sourceIP string) string {
	sourceIPPort := grabSourceIPPort(sourceIP)
	if sourceIPPort == "443" {
		return "https"
	}
	if sourceIPPort != "" {
		return "http"
	}
	if strings.TrimSpace(sourceIP) != "" {
		return "https"
	}
	hostCandidate := sourceURL
	if slashIndex := strings.Index(hostCandidate, "/"); slashIndex >= 0 {
		hostCandidate = hostCandidate[:slashIndex]
	}
	if colonIndex := strings.LastIndex(hostCandidate, ":"); colonIndex >= 0 {
		hostCandidate = hostCandidate[:colonIndex]
	}
	if hostCandidate == "localhost" || net.ParseIP(hostCandidate) != nil {
		return "http"
	}
	return "https"
}

func parseOptionalGrabSourceIP(rawSourceIP string) (string, error) {
	trimmedSourceIP := strings.TrimSpace(rawSourceIP)
	if trimmedSourceIP == "" {
		return "", nil
	}
	parsedIP, portPart := splitGrabSourceIP(trimmedSourceIP)
	if parsedIP == nil {
		return "", errors.New("source_ip is invalid")
	}
	if portPart == "" {
		return parsedIP.String(), nil
	}
	parsedPort, err := strconv.Atoi(strings.TrimSpace(portPart))
	if err != nil || parsedPort <= 0 || parsedPort > 65535 {
		return "", errors.New("source_ip port is invalid")
	}
	return net.JoinHostPort(parsedIP.String(), strconv.Itoa(parsedPort)), nil
}

func parseGrabSourceOptions(r *http.Request) (grabSourceOptions, error) {
	sourceIP, err := parseOptionalGrabSourceIP(r.FormValue("source_ip"))
	if err != nil {
		return grabSourceOptions{}, err
	}
	languageCode, err := parseOptionalGrabSourceLanguage(r.FormValue("source_language"))
	if err != nil {
		return grabSourceOptions{}, err
	}
	return grabSourceOptions{IP: sourceIP, LanguageCode: languageCode}, nil
}

func parseOptionalGrabSourceLanguage(rawLanguage string) (string, error) {
	languageCode := strings.ToLower(strings.TrimSpace(rawLanguage))
	if languageCode == "" || languageCode == "auto" {
		return "", nil
	}
	for _, supportedLanguageCode := range supportedGrabSourceLanguageCodes() {
		if languageCode == supportedLanguageCode {
			return languageCode, nil
		}
	}
	return "", errors.New("source_language is invalid")
}

func supportedGrabSourceLanguageCodes() []string {
	return []string{"en", "fr", "ru", "ja", "it", "sv", "fi", "mn", "zh", "he", "fa", "de", "tr", "kk", "es", "pt"}
}

func grabSourceAcceptLanguage(languageCode string) string {
	languageCode = strings.ToLower(strings.TrimSpace(languageCode))
	if languageCode == "" {
		return ""
	}
	regionTags := map[string]string{
		"en": "en-US", "fr": "fr-FR", "ru": "ru-RU", "ja": "ja-JP",
		"it": "it-IT", "sv": "sv-SE", "fi": "fi-FI", "mn": "mn-MN",
		"zh": "zh-CN", "he": "he-IL", "fa": "fa-IR", "de": "de-DE",
		"tr": "tr-TR", "kk": "kk-KZ", "es": "es-ES", "pt": "pt-PT",
	}
	regionTag, found := regionTags[languageCode]
	if !found {
		return languageCode
	}
	fallbackCode := "en"
	if languageCode == "en" {
		fallbackCode = "ru"
	}
	return languageCode + "," + regionTag + ";q=0.9," + fallbackCode + ";q=0.5,*;q=0.1"
}

func wantsJSONResponse(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/json")
}

func downloadGrabSourceHTML(sourceURL, sourceIP string) ([]byte, error) {
	htmlBytes, _, err := downloadGrabSourceHTMLWithResolvedURL(sourceURL, grabSourceOptions{IP: sourceIP})
	return htmlBytes, err
}

func downloadGrabSourceHTMLWithResolvedURL(sourceURL string, sourceOptions grabSourceOptions) ([]byte, *url.URL, error) {
	remoteSourceURL, err := url.Parse(sourceURL)
	if err != nil {
		return nil, nil, errors.New("source_url is invalid")
	}
	client := newGrabHTTPClientForServerIP(remoteSourceURL.Hostname(), sourceOptions.IP)
	response, err := doGrabGET(client, sourceURL, sourceOptions)
	if err == nil && isSuccessfulGrabResponse(response) {
		defer response.Body.Close()
		if response.Request != nil && response.Request.URL != nil {
			remoteSourceURL = response.Request.URL
		}
		htmlBytes, readErr := io.ReadAll(response.Body)
		if readErr != nil {
			return nil, nil, errors.New("failed to read source page")
		}
		return htmlBytes, remoteSourceURL, nil
	}
	if response != nil {
		response.Body.Close()
	}
	if shouldFallbackGrabSourceToHTTP(remoteSourceURL) {
		fallbackURL := cloneURL(remoteSourceURL)
		fallbackURL.Scheme = "http"
		fallbackURL.Host = fallbackURL.Hostname()
		response, err = doGrabGET(client, fallbackURL.String(), sourceOptions)
		if err == nil && isSuccessfulGrabResponse(response) {
			defer response.Body.Close()
			if response.Request != nil && response.Request.URL != nil {
				remoteSourceURL = response.Request.URL
			} else {
				remoteSourceURL = fallbackURL
			}
			htmlBytes, readErr := io.ReadAll(response.Body)
			if readErr != nil {
				return nil, nil, errors.New("failed to read source page")
			}
			return htmlBytes, remoteSourceURL, nil
		}
		if response != nil {
			response.Body.Close()
		}
	}
	if err != nil {
		return nil, nil, errors.New("failed to download source page")
	}
	return nil, nil, errors.New("source page returned non-success status")
}

func isSuccessfulGrabResponse(response *http.Response) bool {
	return response != nil && response.StatusCode >= 200 && response.StatusCode <= 299
}

func doGrabGET(client *http.Client, rawURL string, sourceOptions grabSourceOptions) (*http.Response, error) {
	request, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	applyGrabRequestHeaders(request, sourceOptions)
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	return client.Do(request)
}

func applyGrabRequestHeaders(request *http.Request, sourceOptions grabSourceOptions) {
	acceptLanguage := grabSourceAcceptLanguage(sourceOptions.LanguageCode)
	if acceptLanguage != "" {
		request.Header.Set("Accept-Language", acceptLanguage)
	}
}

func shouldFallbackGrabSourceToHTTP(sourceURL *url.URL) bool {
	return sourceURL != nil && sourceURL.Scheme == "https"
}

func grabSourceIPPort(sourceIP string) string {
	trimmedSourceIP := strings.TrimSpace(sourceIP)
	if trimmedSourceIP == "" {
		return ""
	}
	if _, port, splitErr := net.SplitHostPort(trimmedSourceIP); splitErr == nil {
		return port
	}
	return ""
}

func splitGrabSourceIP(sourceIP string) (net.IP, string) {
	trimmedSourceIP := strings.TrimSpace(sourceIP)
	ipPart := trimmedSourceIP
	portPart := ""
	if host, port, splitErr := net.SplitHostPort(trimmedSourceIP); splitErr == nil {
		ipPart = host
		portPart = port
	} else if strings.Count(trimmedSourceIP, ":") == 1 {
		host, port, foundPort := strings.Cut(trimmedSourceIP, ":")
		if foundPort {
			ipPart = host
			portPart = port
		}
	}
	return net.ParseIP(strings.Trim(ipPart, "[]")), strings.TrimSpace(portPart)
}

func selectedGrabResourceURLs(r *http.Request) map[string]struct{} {
	if strings.TrimSpace(r.FormValue("import_selection_confirmed")) == "" {
		return nil
	}
	selectedResourceURLs := make(map[string]struct{})
	for _, resourceURL := range r.Form["import_resource_url"] {
		trimmedResourceURL := strings.TrimSpace(resourceURL)
		if trimmedResourceURL == "" {
			continue
		}
		selectedResourceURLs[trimmedResourceURL] = struct{}{}
	}
	return selectedResourceURLs
}

func grabCopyWholeSite(r *http.Request) bool {
	rawValue := strings.ToLower(strings.TrimSpace(r.FormValue("copy_whole_site")))
	return rawValue == "1" || rawValue == "on" || rawValue == "true" || rawValue == "yes"
}

func sumGrabPreviewResourceBytes(resources []grabResourcePreview) int64 {
	var totalBytes int64
	for _, resource := range resources {
		if resource.SizeBytes <= 0 {
			continue
		}
		totalBytes += resource.SizeBytes
	}
	return totalBytes
}

type importQuotaEstimate struct {
	PageStorageBytes     int64
	ResourceBytes        int64
	EstimatedImportBytes int64
	CurrentUsedBytes     int64
	LimitBytes           int64
	FreeBytes            int64
	ProjectedUsedBytes   int64
	FitsQuota            bool
}

func (a *App) estimateImportQuota(ctx context.Context, domain string, importedPages []wholeSiteImportedPage, spider *pageSpider) importQuotaEstimate {
	pageDelta, publishedPageDelta, revisionDelta, publishedStaticDelta := a.estimateImportedPagesStorageDelta(ctx, domain, importedPages)
	resourceBytes := a.estimateImportedFileDelta(domain, spider)
	usage := a.domainStorageUsage(ctx, domain)
	currentUsedBytes := usage.totalBytes()
	estimatedImportBytes := pageDelta + publishedPageDelta + revisionDelta + publishedStaticDelta + resourceBytes
	projectedUsedBytes := currentUsedBytes + estimatedImportBytes
	freeBytes := usage.LimitBytes - currentUsedBytes
	return importQuotaEstimate{
		PageStorageBytes:     pageDelta + publishedPageDelta + revisionDelta + publishedStaticDelta,
		ResourceBytes:        resourceBytes,
		EstimatedImportBytes: estimatedImportBytes,
		CurrentUsedBytes:     currentUsedBytes,
		LimitBytes:           usage.LimitBytes,
		FreeBytes:            freeBytes,
		ProjectedUsedBytes:   projectedUsedBytes,
		FitsQuota:            projectedUsedBytes <= usage.LimitBytes,
	}
}

func previewGrabResources(pageURL *url.URL, htmlSource string, tracker *grabProgressTracker, progressToken string, sourceOptions grabSourceOptions) []grabResourcePreview {
	spider := newPageSpider("", pageURL, grabResourceMaxDepth, tracker, progressToken, sourceOptions)
	rootResource := &mirroredResource{url: pageURL.String(), content: []byte(htmlSource)}
	spider.resources[pageURL.String()] = rootResource
	spider.rewriteNestedResources(rootResource, 0, "text/html")
	resources := previewResourcesFromSpider(spider, map[string]struct{}{pageURL.String(): {}})
	if tracker != nil && strings.TrimSpace(progressToken) != "" {
		tracker.publish(grabProgressEvent{Token: progressToken, Stage: "done", FoundTotal: spider.foundTotal, DownloadedTotal: spider.downloadedTotal, CompletedPercent: 100})
	}
	return resources
}

func prepareSinglePageImport(domain, pagePath, sourceURL string, pageURL *url.URL, fallbackHTML string, tracker *grabProgressTracker, progressToken string, selectedResourceURLs map[string]struct{}, sourceOptions grabSourceOptions) (*pageSpider, string) {
	spider := newPageSpider(domain, pageURL, grabResourceMaxDepth, tracker, progressToken, sourceOptions)
	spider.selectedResourceURLs = selectedResourceURLs
	rootResource := &mirroredResource{url: sourceURL, content: []byte(fallbackHTML)}
	spider.resources[sourceURL] = rootResource
	spider.rewriteNestedResources(rootResource, 0, "text/html")
	spider.fetchSelectedResources()
	rootResource.content = []byte(spider.rewriteStaticURLTextReferences(string(rootResource.content), pageURL, 0))
	return spider, string(rootResource.content)
}

func previewResourcesFromSpider(spider *pageSpider, excludedURLs map[string]struct{}) []grabResourcePreview {
	resources := make([]grabResourcePreview, 0, len(spider.resources))
	for resourceURL, resource := range spider.resources {
		if _, excluded := excludedURLs[resourceURL]; excluded {
			continue
		}
		if resource == nil || resource.content == nil {
			continue
		}
		sizeBytes := int64(-1)
		if resource.content != nil {
			sizeBytes = int64(len(resource.content))
		}
		resourceKind := previewResourceKind("", "", resourceURL)
		if resource != nil && resourceKind == "file" {
			if contentTypeKind := resourceKindFromContentType(resource.contentType); contentTypeKind != "" {
				resourceKind = contentTypeKind
			}
		}
		resources = append(resources, grabResourcePreview{URL: resourceURL, Kind: resourceKind, SizeBytes: sizeBytes})
	}
	sort.Slice(resources, func(leftIndex, rightIndex int) bool {
		if resources[leftIndex].Kind == resources[rightIndex].Kind {
			return resources[leftIndex].URL < resources[rightIndex].URL
		}
		return resources[leftIndex].Kind < resources[rightIndex].Kind
	})
	return resources
}

func previewWholeRemoteSiteResources(startURL *url.URL, startHTML, publicAssetBasePath string, tracker *grabProgressTracker, progressToken string, sourceOptions grabSourceOptions) wholeSitePreviewResult {
	spider, pageURLs, importedPages := crawlWholeRemoteSite(startURL, startHTML, publicAssetBasePath, tracker, progressToken, sourceOptions)
	resources := previewResourcesFromSpider(spider, pageURLs)
	if tracker != nil && strings.TrimSpace(progressToken) != "" {
		tracker.publish(grabProgressEvent{Token: progressToken, Stage: "done", FoundTotal: spider.foundTotal, DownloadedTotal: spider.downloadedTotal, CompletedPercent: 100})
	}
	return wholeSitePreviewResult{PageCount: len(pageURLs), Resources: resources, ImportedPages: importedPages, Spider: spider}
}

func (a *App) prepareWholeRemoteSiteImport(domain, basePath string, startURL *url.URL, startHTML, progressToken string, selectedResourceURLs map[string]struct{}, sourceOptions grabSourceOptions) (*pageSpider, []wholeSiteImportedPage, error) {
	basePath = cleanPath(basePath)
	if startURL == nil || startURL.Hostname() == "" {
		return nil, nil, errors.New("source_url is invalid")
	}
	spider := newPageSpider(domain, startURL, grabResourceMaxDepth, a.grabTracker, progressToken, sourceOptions)
	spider.selectedResourceURLs = selectedResourceURLs
	spider.publicAssetBasePath = basePath
	spider.documentURLRewriter = func(normalizedURL string) (string, bool) {
		parsedURL, parseErr := url.Parse(normalizedURL)
		if parseErr != nil || !sameWholeSiteHost(startURL, parsedURL) || !isWholeSitePageURL(parsedURL) {
			return "", false
		}
		return wholeSiteLocalLink(basePath, startURL, parsedURL), true
	}

	pageClient := newGrabHTTPClientForServerIP(startURL.Hostname(), sourceOptions.IP)
	knownPagePathsByKey := map[string]string{wholeSitePageKey(startURL): basePath}
	pageQueue := []wholeSitePageJob{{URL: cloneURL(startURL), HTML: startHTML}}
	importedPages := make([]wholeSiteImportedPage, 0, 32)
	importedLocalPaths := make(map[string]struct{})
	spider.foundTotal++
	spider.publishProgress("found", startURL.String(), 0)

	for len(pageQueue) > 0 && len(importedPages) < wholeSiteImportMaxPages {
		currentJob := pageQueue[0]
		pageQueue = pageQueue[1:]
		if currentJob.URL == nil {
			continue
		}
		pageKey := wholeSitePageKey(currentJob.URL)
		if pageKey == "" {
			continue
		}
		pageHTML := currentJob.HTML
		if strings.TrimSpace(pageHTML) == "" {
			downloadedHTML, downloaded, downloadErr := downloadWholeSitePageHTML(pageClient, currentJob.URL, sourceOptions)
			if downloadErr != nil || !downloaded {
				spider.publishResourceProgress("error", currentJob.URL.String(), 0, 0, -1)
				continue
			}
			pageHTML = downloadedHTML
		}

		for _, linkedPageURL := range extractWholeSitePageLinks(pageHTML, currentJob.URL, startURL) {
			linkedPageKey := wholeSitePageKey(linkedPageURL)
			if linkedPageKey == "" {
				continue
			}
			if _, alreadyKnown := knownPagePathsByKey[linkedPageKey]; alreadyKnown {
				continue
			}
			if len(knownPagePathsByKey) >= wholeSiteImportMaxPages {
				break
			}
			knownPagePathsByKey[linkedPageKey] = wholeSiteLocalPath(basePath, startURL, linkedPageURL)
			pageQueue = append(pageQueue, wholeSitePageJob{URL: cloneURL(linkedPageURL)})
			spider.foundTotal++
			spider.publishProgress("found", linkedPageURL.String(), 0)
		}

		rootResource := &mirroredResource{url: currentJob.URL.String(), content: []byte(pageHTML)}
		spider.resources[currentJob.URL.String()] = rootResource
		spider.rewriteNestedResources(rootResource, 0, "text/html")
		localPath := knownPagePathsByKey[pageKey]
		if _, alreadyImported := importedLocalPaths[localPath]; alreadyImported {
			continue
		}
		importedLocalPaths[localPath] = struct{}{}
		importedPages = append(importedPages, wholeSiteImportedPage{SourceURL: currentJob.URL.String(), LocalPath: localPath, HTML: string(rootResource.content)})
		spider.downloadedTotal++
		spider.publishResourceProgress("downloaded", currentJob.URL.String(), 100, int64(len(pageHTML)), int64(len(pageHTML)))
	}
	if len(importedPages) == 0 {
		return nil, nil, errors.New("no pages were imported")
	}
	spider.rewriteImportedPagesStaticURLTextReferences(importedPages)
	return spider, importedPages, nil
}

func (a *App) importWholeRemoteSite(ctx context.Context, domain, basePath string, startURL *url.URL, startHTML, progressToken string, selectedResourceURLs map[string]struct{}, sourceOptions grabSourceOptions) (string, error) {
	spider, importedPages, prepareErr := a.prepareWholeRemoteSiteImport(domain, basePath, startURL, startHTML, progressToken, selectedResourceURLs, sourceOptions)
	if prepareErr != nil {
		return "", prepareErr
	}
	pageDelta, publishedPageDelta, revisionDelta, publishedStaticDelta := a.estimateImportedPagesStorageDelta(ctx, domain, importedPages)
	fileDelta := a.estimateImportedFileDelta(domain, spider)
	if storageErr := a.applyDomainStorageDelta(ctx, domain, pageDelta, publishedPageDelta, revisionDelta, fileDelta, publishedStaticDelta); storageErr != nil {
		return "", storageErr
	}
	if persistErr := a.persistSpiderAssets(spider, basePath); persistErr != nil {
		_ = a.applyDomainStorageDelta(ctx, domain, -pageDelta, -publishedPageDelta, -revisionDelta, -fileDelta, -publishedStaticDelta)
		return "", persistErr
	}
	if storeErr := a.storeWholeSiteImportedPages(ctx, domain, importedPages); storeErr != nil {
		_ = a.applyDomainStorageDelta(ctx, domain, -pageDelta, -publishedPageDelta, -revisionDelta, -fileDelta, -publishedStaticDelta)
		return "", storeErr
	}
	a.rebuildDomainStorageUsage(ctx, domain)
	if a.grabTracker != nil {
		a.grabTracker.publish(grabProgressEvent{Token: progressToken, Stage: "done", FoundTotal: spider.foundTotal, DownloadedTotal: spider.downloadedTotal, CompletedPercent: 100})
	}
	return basePath, nil
}

func crawlWholeRemoteSite(startURL *url.URL, startHTML, publicAssetBasePath string, tracker *grabProgressTracker, progressToken string, sourceOptions grabSourceOptions) (*pageSpider, map[string]struct{}, []wholeSiteImportedPage) {
	spider := newPageSpider("", startURL, grabResourceMaxDepth, tracker, progressToken, sourceOptions)
	spider.publicAssetBasePath = publicAssetBasePath
	pageClient := newGrabHTTPClientForServerIP(startURL.Hostname(), sourceOptions.IP)
	knownPagePathsByKey := map[string]string{wholeSitePageKey(startURL): cleanPath(publicAssetBasePath)}
	pageURLs := map[string]struct{}{startURL.String(): {}}
	pageQueue := []wholeSitePageJob{{URL: cloneURL(startURL), HTML: startHTML}}
	importedPages := make([]wholeSiteImportedPage, 0, 32)
	spider.foundTotal++
	spider.publishProgress("found", startURL.String(), 0)
	for len(pageQueue) > 0 && len(pageURLs) < wholeSiteImportMaxPages {
		currentJob := pageQueue[0]
		pageQueue = pageQueue[1:]
		if currentJob.URL == nil {
			continue
		}
		pageKey := wholeSitePageKey(currentJob.URL)
		if pageKey == "" {
			continue
		}
		pageHTML := currentJob.HTML
		if strings.TrimSpace(pageHTML) == "" {
			downloadedHTML, downloaded, downloadErr := downloadWholeSitePageHTML(pageClient, currentJob.URL, sourceOptions)
			if downloadErr != nil || !downloaded {
				spider.publishResourceProgress("error", currentJob.URL.String(), 0, 0, -1)
				continue
			}
			pageHTML = downloadedHTML
		}
		for _, linkedPageURL := range extractWholeSitePageLinks(pageHTML, currentJob.URL, startURL) {
			linkedPageKey := wholeSitePageKey(linkedPageURL)
			if linkedPageKey == "" {
				continue
			}
			if _, alreadyKnown := knownPagePathsByKey[linkedPageKey]; alreadyKnown {
				continue
			}
			if len(knownPagePathsByKey) >= wholeSiteImportMaxPages {
				break
			}
			knownPagePathsByKey[linkedPageKey] = wholeSiteLocalPath(cleanPath(publicAssetBasePath), startURL, linkedPageURL)
			pageQueue = append(pageQueue, wholeSitePageJob{URL: cloneURL(linkedPageURL)})
			pageURLs[linkedPageURL.String()] = struct{}{}
			spider.foundTotal++
			spider.publishProgress("found", linkedPageURL.String(), 0)
		}
		rootResource := &mirroredResource{url: currentJob.URL.String(), content: []byte(pageHTML)}
		spider.resources[currentJob.URL.String()] = rootResource
		spider.rewriteNestedResources(rootResource, 0, "text/html")
		importedPages = append(importedPages, wholeSiteImportedPage{SourceURL: currentJob.URL.String(), LocalPath: knownPagePathsByKey[pageKey], HTML: string(rootResource.content)})
		spider.downloadedTotal++
		spider.publishResourceProgress("downloaded", currentJob.URL.String(), 100, int64(len(pageHTML)), int64(len(pageHTML)))
	}
	spider.rewriteImportedPagesStaticURLTextReferences(importedPages)
	return spider, pageURLs, importedPages
}

func (a *App) storeWholeSiteImportedPages(ctx context.Context, domain string, importedPages []wholeSiteImportedPage) error {
	frozenDomain := a.isDomainFrozen(ctx, domain)
	for _, importedPage := range importedPages {
		pagePath := cleanPath(importedPage.LocalPath)
		pageHTML := importedPage.HTML
		a.clearPageRedirectSource(ctx, domain, pagePath)
		_, _ = a.db.ExecContext(ctx, `INSERT OR REPLACE INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, domain, pagePath, pagePath, pageHTML)
		if !frozenDomain {
			_, _ = a.db.ExecContext(ctx, `INSERT OR REPLACE INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, domain, pagePath, pagePath, pageHTML)
			a.writePublishedStaticHTML(domain, pagePath, pageHTML)
		}
		_, _ = a.db.ExecContext(ctx, `INSERT INTO revisions(domain,page_path,html,created_at) VALUES(?,?,?,?)`, domain, pagePath, pageHTML, time.Now().Format(time.RFC3339))
	}
	return nil
}

func (a *App) estimateImportedPagesStorageDelta(ctx context.Context, domain string, importedPages []wholeSiteImportedPage) (int64, int64, int64, int64) {
	frozenDomain := a.isDomainFrozen(ctx, domain)
	var pageDelta int64
	var publishedPageDelta int64
	var revisionDelta int64
	var publishedStaticDelta int64
	for _, importedPage := range importedPages {
		pagePath := cleanPath(importedPage.LocalPath)
		pageHTML := importedPage.HTML
		newHTMLBytes := int64(len([]byte(pageHTML)))
		var previousStoredHTML string
		_ = a.db.QueryRowContext(ctx, `SELECT html FROM pages WHERE domain=? AND path=?`, domain, pagePath).Scan(&previousStoredHTML)
		pageDelta += newHTMLBytes - int64(len([]byte(previousStoredHTML)))
		revisionDelta += newHTMLBytes
		if frozenDomain {
			continue
		}
		var previousPublishedHTML string
		_ = a.db.QueryRowContext(ctx, `SELECT html FROM published_pages WHERE domain=? AND path=?`, domain, pagePath).Scan(&previousPublishedHTML)
		publishedPageDelta += newHTMLBytes - int64(len([]byte(previousPublishedHTML)))
		publishedStaticDelta += newHTMLBytes - fileSizeBytes(filepath.Join(a.domainStaticDir(domain), staticRelativePathForPage(pagePath)))
	}
	return pageDelta, publishedPageDelta, revisionDelta, publishedStaticDelta
}

func (a *App) estimateImportedFileDelta(domain string, spider *pageSpider) int64 {
	if a == nil || spider == nil {
		return 0
	}
	baseDir := a.domainFilesDirForDomain(domain)
	var fileDelta int64
	for _, resource := range spider.resources {
		if resource == nil || !resource.persist || strings.TrimSpace(resource.assetPath) == "" || resource.content == nil {
			continue
		}
		assetReference := publicAssetReferenceFromPath(resource.assetPath)
		if assetReference == "" {
			continue
		}
		targetFilePath := filepath.Join(baseDir, filepath.FromSlash(assetReference))
		existingSize := fileSizeBytes(targetFilePath)
		newSize := int64(len(resource.content))
		if existingSize <= 0 {
			fileDelta += newSize
			continue
		}
		fileDelta += newSize - existingSize
	}
	return fileDelta
}

func downloadWholeSitePageHTML(client *http.Client, pageURL *url.URL, sourceOptions grabSourceOptions) (string, bool, error) {
	response, err := doGrabGET(client, pageURL.String(), sourceOptions)
	if err != nil {
		return "", false, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return "", false, fmt.Errorf("page download failed: %s", response.Status)
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if contentType != "" && contentType != "text/html" && contentType != "application/xhtml+xml" {
		return "", false, nil
	}
	pageBytes, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		return "", false, readErr
	}
	return string(pageBytes), true, nil
}

func extractWholeSitePageLinks(htmlSource string, baseURL, siteURL *url.URL) []*url.URL {
	pageURLs := make([]*url.URL, 0, 16)
	tokenizer := html.NewTokenizer(strings.NewReader(htmlSource))
	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			break
		}
		if tokenType != html.StartTagToken && tokenType != html.SelfClosingTagToken {
			continue
		}
		token := tokenizer.Token()
		tagName := strings.ToLower(strings.TrimSpace(token.Data))
		for _, attribute := range token.Attr {
			attributeName := strings.ToLower(strings.TrimSpace(attribute.Key))
			if !isWholeSiteDocumentAttribute(tagName, attributeName) {
				continue
			}
			normalizedURL, blocked := (&pageSpider{}).normalizeURL(attribute.Val, baseURL)
			if blocked || normalizedURL == "" {
				continue
			}
			linkedPageURL, parseErr := url.Parse(normalizedURL)
			if parseErr != nil || !sameWholeSiteHost(siteURL, linkedPageURL) || !isWholeSitePageURL(linkedPageURL) {
				continue
			}
			pageURLs = append(pageURLs, linkedPageURL)
		}
	}
	return pageURLs
}

func isWholeSiteDocumentAttribute(tagName, attributeName string) bool {
	switch strings.ToLower(strings.TrimSpace(tagName)) {
	case "a", "area":
		return attributeName == "href" || attributeName == "xlink:href"
	case "form":
		return attributeName == "action"
	case "iframe", "embed":
		return attributeName == "src"
	case "object":
		return attributeName == "data"
	default:
		return false
	}
}

func sameWholeSiteHost(leftURL, rightURL *url.URL) bool {
	if leftURL == nil || rightURL == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(leftURL.Host), strings.TrimSpace(rightURL.Host))
}

func isWholeSitePageURL(pageURL *url.URL) bool {
	if pageURL == nil {
		return false
	}
	extension := strings.ToLower(path.Ext(pageURL.Path))
	if extension == "" {
		return true
	}
	switch extension {
	case ".htm", ".html", ".xhtml", ".php", ".asp", ".aspx", ".jsp", ".cgi":
		return true
	default:
		return false
	}
}

func wholeSitePageKey(pageURL *url.URL) string {
	if pageURL == nil || pageURL.Host == "" {
		return ""
	}
	return strings.ToLower(pageURL.Scheme) + "://" + strings.ToLower(pageURL.Host) + canonicalWholeSitePagePath(pageURL.Path)
}

func canonicalWholeSitePagePath(rawPath string) string {
	cleanedPath := cleanPath(rawPath)
	for _, indexName := range []string{"/index.html", "/index.htm", "/index.xhtml"} {
		if strings.HasSuffix(strings.ToLower(cleanedPath), indexName) {
			cleanedPath = cleanedPath[:len(cleanedPath)-len(indexName)]
			if cleanedPath == "" {
				return "/"
			}
			return cleanPath(cleanedPath)
		}
	}
	return cleanedPath
}

func wholeSiteLocalPath(basePath string, startURL, pageURL *url.URL) string {
	basePath = cleanPath(basePath)
	if wholeSitePageKey(startURL) == wholeSitePageKey(pageURL) {
		return basePath
	}
	sourcePath := canonicalWholeSitePagePath(pageURL.Path)
	if sourcePath == "/" {
		return basePath
	}
	if basePath == "/" {
		return sourcePath
	}
	return cleanPath(strings.TrimRight(basePath, "/") + sourcePath)
}

func wholeSiteLocalLink(basePath string, startURL, pageURL *url.URL) string {
	localPath := wholeSiteLocalPath(basePath, startURL, pageURL)
	if pageURL != nil && strings.TrimSpace(pageURL.RawQuery) != "" {
		return localPath + "?" + pageURL.RawQuery
	}
	return localPath
}

func cloneURL(sourceURL *url.URL) *url.URL {
	if sourceURL == nil {
		return nil
	}
	clonedURL := *sourceURL
	return &clonedURL
}

func previewResourceKind(tagName, attributeName, rawRef string) string {
	tag := strings.ToLower(strings.TrimSpace(tagName))
	attribute := strings.ToLower(strings.TrimSpace(attributeName))
	switch tag {
	case "script":
		return "script"
	case "link":
		return "style"
	case "img", "source":
		return "image"
	case "video", "audio":
		return tag
	case "iframe", "embed", "object":
		return "embedded"
	}
	if attribute == "poster" {
		return "image"
	}
	if resourceKind := grabber.ResourceKindFromURL(rawRef); resourceKind != "" {
		return resourceKind
	}
	return "file"
}

type importedHTMLCleanupRule struct {
	name    string
	matches func(*html.Node) bool
}

var legacyImportedSiteBrushCleanupRules = []importedHTMLCleanupRule{
	{name: "sitebrush-comment", matches: isLegacySiteBrushCommentNode},
	{name: "sitebrush-menu-container", matches: isLegacySiteBrushMenuContainerNode},
	{name: "sitebrush-context-script", matches: isLegacySiteBrushContextScriptNode},
	{name: "sitebrush-context-style", matches: isLegacySiteBrushContextStyleNode},
}

var importedLanguageRedirectPathPattern = regexp.MustCompile(`(?i)['"]/?(en|fr|ru|ja|it|sv|fi|mn|zh|he|fa|de|tr|kk|es|pt)/(index\.(html?|xhtml|php|asp|aspx|jsp|cgi))?['"]`)

func cleanupLegacyImportedSiteBrushHTML(source string) string {
	loweredSource := strings.ToLower(source)
	if !strings.Contains(loweredSource, "sitebrush") && !strings.Contains(loweredSource, "jqcontextmenu") && !strings.Contains(loweredSource, "contextmenu") {
		return source
	}
	documentNode, parseErr := html.Parse(strings.NewReader(source))
	if parseErr != nil {
		return source
	}
	removeImportedHTMLNodes(documentNode)
	var rendered bytes.Buffer
	if renderErr := html.Render(&rendered, documentNode); renderErr != nil {
		return source
	}
	return rendered.String()
}

func neutralizeImportedHostLanguageRedirects(source string) string {
	loweredSource := strings.ToLower(source)
	if !strings.Contains(loweredSource, "location") || (!strings.Contains(loweredSource, "hostname") && !strings.Contains(loweredSource, "host")) {
		return source
	}
	documentNode, parseErr := html.Parse(strings.NewReader(source))
	if parseErr != nil {
		return source
	}
	removeImportedHostLanguageRedirectScripts(documentNode)
	var rendered bytes.Buffer
	if renderErr := html.Render(&rendered, documentNode); renderErr != nil {
		return source
	}
	return rendered.String()
}

func removeImportedHostLanguageRedirectScripts(parentNode *html.Node) {
	if parentNode == nil {
		return
	}
	for childNode := parentNode.FirstChild; childNode != nil; {
		nextNode := childNode.NextSibling
		if isImportedHostLanguageRedirectScriptNode(childNode) {
			parentNode.RemoveChild(childNode)
		} else {
			removeImportedHostLanguageRedirectScripts(childNode)
		}
		childNode = nextNode
	}
}

func isImportedHostLanguageRedirectScriptNode(node *html.Node) bool {
	if node == nil || node.Type != html.ElementNode || node.Data != "script" || strings.TrimSpace(htmlAttribute(node, "src")) != "" {
		return false
	}
	scriptSource := strings.ToLower(htmlNodeText(node))
	if scriptSource == "" {
		return false
	}
	hasNavigationSink := strings.Contains(scriptSource, "location.href") || strings.Contains(scriptSource, "location.replace") || strings.Contains(scriptSource, "location.assign")
	if !hasNavigationSink {
		return false
	}
	if !strings.Contains(scriptSource, "hostname") && !strings.Contains(scriptSource, "host") {
		return false
	}
	if !strings.Contains(scriptSource, "pathname") && !strings.Contains(scriptSource, "path") {
		return false
	}
	return importedLanguageRedirectPathPattern.MatchString(scriptSource)
}

func removeImportedHTMLNodes(parentNode *html.Node) {
	if parentNode == nil {
		return
	}
	for childNode := parentNode.FirstChild; childNode != nil; {
		nextNode := childNode.NextSibling
		if shouldRemoveImportedHTMLNode(childNode) {
			parentNode.RemoveChild(childNode)
		} else {
			removeImportedHTMLNodes(childNode)
		}
		childNode = nextNode
	}
}

func shouldRemoveImportedHTMLNode(node *html.Node) bool {
	for _, cleanupRule := range legacyImportedSiteBrushCleanupRules {
		if cleanupRule.matches(node) {
			return true
		}
	}
	return false
}

func isLegacySiteBrushCommentNode(node *html.Node) bool {
	return node != nil && node.Type == html.CommentNode && strings.Contains(strings.ToLower(node.Data), "powered by sitebrush")
}

func isLegacySiteBrushMenuContainerNode(node *html.Node) bool {
	if node == nil || node.Type != html.ElementNode {
		return false
	}
	nodeID := strings.ToLower(strings.TrimSpace(htmlAttribute(node, "id")))
	if nodeID == "sitebrushmenu" || nodeID == "jqcontextmenu" {
		return true
	}
	nodeClass := htmlClassSet(node)
	return nodeClass["sitebrushcontextmenu"] || nodeClass["contextmenucopyright"] || (nodeClass["contextmenu"] && nodeClass["sitebrushmenu"])
}

func isLegacySiteBrushContextScriptNode(node *html.Node) bool {
	if node == nil || node.Type != html.ElementNode || node.Data != "script" {
		return false
	}
	scriptSource := strings.ToLower(htmlNodeText(node))
	if scriptSource == "" {
		return false
	}
	for _, marker := range []string{"$.fn.contextmenu", "jqcontextmenu", "$('#sitebrush').contextmenu(", `$("#sitebrush").contextmenu(`, "div.contextmenu", "sitebrushmenu"} {
		if strings.Contains(scriptSource, marker) {
			return true
		}
	}
	return false
}

func isLegacySiteBrushContextStyleNode(node *html.Node) bool {
	if node == nil || node.Type != html.ElementNode || node.Data != "style" {
		return false
	}
	styleSource := strings.ToLower(htmlNodeText(node))
	if styleSource == "" {
		return false
	}
	for _, marker := range []string{".sitebrushcontextmenu", ".contextmenucopyright", ".sitebrushmenu", "div.contextmenu"} {
		if strings.Contains(styleSource, marker) {
			return true
		}
	}
	return false
}

func htmlAttribute(node *html.Node, attributeKey string) string {
	if node == nil {
		return ""
	}
	for _, attribute := range node.Attr {
		if strings.EqualFold(strings.TrimSpace(attribute.Key), attributeKey) {
			return attribute.Val
		}
	}
	return ""
}

func htmlClassSet(node *html.Node) map[string]bool {
	classSet := make(map[string]bool)
	for _, className := range strings.Fields(strings.ToLower(htmlAttribute(node, "class"))) {
		classSet[className] = true
	}
	return classSet
}

func htmlNodeText(node *html.Node) string {
	if node == nil {
		return ""
	}
	var textBuilder strings.Builder
	for childNode := node.FirstChild; childNode != nil; childNode = childNode.NextSibling {
		if childNode.Type == html.TextNode || childNode.Type == html.CommentNode {
			textBuilder.WriteString(childNode.Data)
		}
	}
	return textBuilder.String()
}

func (a *App) grabProgressWS(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	progressToken := strings.TrimSpace(r.URL.Query().Get("token"))
	if progressToken == "" {
		http.Error(w, "token is required", http.StatusBadRequest)
		return
	}
	connection, err := upgradeToWebSocket(w, r)
	if err != nil {
		return
	}
	defer connection.Close()
	events := a.grabTracker.subscribe(progressToken)
	defer a.grabTracker.unsubscribe(progressToken, events)
	readyEventJSON, marshalReadyErr := json.Marshal(grabProgressEvent{Token: progressToken, Stage: "ready"})
	if marshalReadyErr != nil {
		return
	}
	if err := connection.WriteText(readyEventJSON); err != nil {
		return
	}
	for {
		event, isOpen := <-events
		if !isOpen {
			return
		}
		eventJSON, marshalErr := json.Marshal(event)
		if marshalErr != nil {
			return
		}
		if err := connection.WriteText(eventJSON); err != nil {
			return
		}
		if event.Stage == "done" || (event.Stage == "error" && strings.TrimSpace(event.CurrentURL) == "") {
			return
		}
	}
}

func (a *App) grabProgressEvents(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	progressToken := strings.TrimSpace(r.URL.Query().Get("token"))
	if progressToken == "" {
		http.Error(w, "token is required", http.StatusBadRequest)
		return
	}
	flusher, canFlush := w.(http.Flusher)
	if !canFlush {
		http.Error(w, "streaming is not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	events := a.grabTracker.subscribe(progressToken)
	defer a.grabTracker.unsubscribe(progressToken, events)
	if !writeGrabProgressEvent(w, flusher, grabProgressEvent{Token: progressToken, Stage: "ready"}) {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case event, isOpen := <-events:
			if !isOpen {
				return
			}
			if !writeGrabProgressEvent(w, flusher, event) {
				return
			}
			if event.Stage == "done" || (event.Stage == "error" && strings.TrimSpace(event.CurrentURL) == "") {
				return
			}
		}
	}
}

func writeGrabProgressEvent(w io.Writer, flusher http.Flusher, event grabProgressEvent) bool {
	eventJSON, marshalErr := json.Marshal(event)
	if marshalErr != nil {
		return false
	}
	if _, writeErr := fmt.Fprintf(w, "data: %s\n\n", eventJSON); writeErr != nil {
		return false
	}
	flusher.Flush()
	return true
}

func (a *App) publishProgressEvents(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	progressToken := strings.TrimSpace(r.URL.Query().Get("token"))
	if progressToken == "" {
		http.Error(w, "token is required", http.StatusBadRequest)
		return
	}
	flusher, canFlush := w.(http.Flusher)
	if !canFlush {
		http.Error(w, "streaming is not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	tracker := a.activePublishTracker()
	events := tracker.subscribe(progressToken)
	defer tracker.unsubscribe(progressToken, events)
	if !writePublishProgressEvent(w, flusher, publishProgressEvent{Token: progressToken, Stage: "ready"}) {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case event, isOpen := <-events:
			if !isOpen {
				return
			}
			if !writePublishProgressEvent(w, flusher, event) {
				return
			}
			if event.Stage == "done" || event.Stage == "error" {
				return
			}
		}
	}
}

func writePublishProgressEvent(w io.Writer, flusher http.Flusher, event publishProgressEvent) bool {
	eventJSON, marshalErr := json.Marshal(event)
	if marshalErr != nil {
		return false
	}
	if _, writeErr := fmt.Fprintf(w, "data: %s\n\n", eventJSON); writeErr != nil {
		return false
	}
	flusher.Flush()
	return true
}

func (a *App) revisionsPage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		if !a.hasAdmin(r.Context(), a.siteDomain(r.Context(), r)) {
			http.Redirect(w, r, r.URL.Path+"?register", http.StatusFound)
			return
		}
		http.Redirect(w, r, loginURLForRequest(r), http.StatusFound)
		return
	}
	pagePath := cleanPath(r.URL.Query().Get("path"))
	if pagePath == "/" && strings.TrimSpace(r.URL.Query().Get("path")) == "" {
		pagePath = cleanPath(r.URL.Path)
	}
	domain := a.siteDomain(r.Context(), r)
	revisionRows, err := a.db.QueryContext(r.Context(), `SELECT id,page_path,html,created_at,is_active FROM revisions WHERE domain=? AND page_path=? ORDER BY id DESC`, domain, pagePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer revisionRows.Close()
	var revisionList []Revision
	for revisionRows.Next() {
		var current Revision
		_ = revisionRows.Scan(&current.ID, &current.PagePath, &current.HTML, &current.CreatedAt, &current.IsActive)
		revisionList = append(revisionList, current)
	}
	returnPath := r.URL.Query().Get("return")
	if strings.TrimSpace(returnPath) == "" {
		returnPath = pagePath
	}
	a.render(w, r, "revisions.html", map[string]any{"Path": pagePath, "ReturnPath": returnPath, "Revisions": revisionList})
}

func (a *App) applyLatestActiveRevision(ctx context.Context, domain string, pagePath string) {
	var latestActiveHTML string
	err := a.db.QueryRowContext(ctx, `SELECT html FROM revisions WHERE domain=? AND page_path=? AND is_active=1 ORDER BY id DESC LIMIT 1`, domain, pagePath).Scan(&latestActiveHTML)
	if err != nil {
		a.removeManagedPage(ctx, domain, pagePath)
		return
	}
	pageTitle := pagePath
	_ = a.db.QueryRowContext(ctx, `SELECT title FROM pages WHERE domain=? AND path=?`, domain, pagePath).Scan(&pageTitle)
	newHTMLBytes := int64(len([]byte(latestActiveHTML)))
	var previousStoredHTML string
	_ = a.db.QueryRowContext(ctx, `SELECT html FROM pages WHERE domain=? AND path=?`, domain, pagePath).Scan(&previousStoredHTML)
	pageDelta := newHTMLBytes - int64(len([]byte(previousStoredHTML)))
	publishedPageDelta := int64(0)
	publishedStaticDelta := int64(0)
	if !a.isDomainFrozen(ctx, domain) {
		var previousPublishedHTML string
		_ = a.db.QueryRowContext(ctx, `SELECT html FROM published_pages WHERE domain=? AND path=?`, domain, pagePath).Scan(&previousPublishedHTML)
		publishedPageDelta = newHTMLBytes - int64(len([]byte(previousPublishedHTML)))
		publishedStaticDelta = newHTMLBytes - fileSizeBytes(filepath.Join(a.domainStaticDir(domain), staticRelativePathForPage(pagePath)))
	}
	if storageErr := a.applyDomainStorageDelta(ctx, domain, pageDelta, publishedPageDelta, 0, 0, publishedStaticDelta); storageErr != nil {
		log.Printf("restore blocked by storage limit domain=%s path=%s error=%v", domain, pagePath, storageErr)
		return
	}
	a.clearPageRedirectSource(ctx, domain, pagePath)
	_, _ = a.db.ExecContext(ctx, `INSERT OR REPLACE INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, domain, pagePath, pageTitle, latestActiveHTML)
	if !a.isDomainFrozen(ctx, domain) {
		_, _ = a.db.ExecContext(ctx, `INSERT OR REPLACE INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, domain, pagePath, pageTitle, latestActiveHTML)
		a.writePublishedStaticHTML(domain, pagePath, latestActiveHTML)
	}
}

func (a *App) removeManagedPage(ctx context.Context, domain string, pagePath string) {
	var storedHTML string
	_ = a.db.QueryRowContext(ctx, `SELECT html FROM pages WHERE domain=? AND path=?`, domain, pagePath).Scan(&storedHTML)
	var publishedHTML string
	_ = a.db.QueryRowContext(ctx, `SELECT html FROM published_pages WHERE domain=? AND path=?`, domain, pagePath).Scan(&publishedHTML)
	publishedStaticBytes := fileSizeBytes(filepath.Join(a.domainStaticDir(domain), staticRelativePathForPage(pagePath)))
	_ = a.applyDomainStorageDelta(ctx, domain, -int64(len([]byte(storedHTML))), -int64(len([]byte(publishedHTML))), 0, 0, -publishedStaticBytes)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM pages WHERE domain=? AND path=?`, domain, pagePath)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM published_pages WHERE domain=? AND path=?`, domain, pagePath)
	a.removePublishedStaticFile(domain, pagePath)
}

func (a *App) restoreRevision(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) || r.Method != http.MethodPost {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	revisionID, _ := strconv.Atoi(r.FormValue("id"))
	var pagePath, html string
	domain := a.siteDomain(r.Context(), r)
	err := a.db.QueryRowContext(r.Context(), `SELECT page_path,html FROM revisions WHERE id=? AND domain=?`, revisionID, domain).Scan(&pagePath, &html)
	if err != nil {
		http.Error(w, "revision not found", http.StatusNotFound)
		return
	}
	if storageErr := a.applyDomainStorageDelta(r.Context(), domain, 0, 0, int64(len([]byte(html))), 0, 0); storageErr != nil {
		http.Error(w, storageErr.Error(), http.StatusInsufficientStorage)
		return
	}
	_, _ = a.db.ExecContext(r.Context(), `UPDATE pages SET html=? WHERE domain=? AND path=?`, html, domain, pagePath)
	_, _ = a.db.ExecContext(r.Context(), `INSERT INTO revisions(domain,page_path,html,created_at) VALUES(?,?,?,?)`, domain, pagePath, html, time.Now().Format(time.RFC3339))
	a.applyLatestActiveRevision(r.Context(), domain, pagePath)
	http.Redirect(w, r, pagePath, http.StatusFound)
}

func (a *App) deleteRevision(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) || r.Method != http.MethodPost {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	revisionID, _ := strconv.Atoi(r.FormValue("id"))
	pagePath := r.FormValue("path")
	domain := a.siteDomain(r.Context(), r)
	_, _ = a.db.ExecContext(r.Context(), `UPDATE revisions SET is_active=0 WHERE id=? AND domain=?`, revisionID, domain)
	a.applyLatestActiveRevision(r.Context(), domain, pagePath)
	http.Redirect(w, r, pagePath+"?revisions", http.StatusFound)
}

func (a *App) deleteRevisionByQuery(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) || r.Method != http.MethodPost {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	revisionID, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("delete")))
	if revisionID <= 0 {
		http.Error(w, "revision not found", http.StatusNotFound)
		return
	}
	domain := a.siteDomain(r.Context(), r)
	var pagePath string
	err := a.db.QueryRowContext(r.Context(), `SELECT page_path FROM revisions WHERE id=? AND domain=?`, revisionID, domain).Scan(&pagePath)
	if err != nil {
		http.Error(w, "revision not found", http.StatusNotFound)
		return
	}
	_, _ = a.db.ExecContext(r.Context(), `UPDATE revisions SET is_active=0 WHERE id=? AND domain=?`, revisionID, domain)
	a.applyLatestActiveRevision(r.Context(), domain, pagePath)
	http.Redirect(w, r, pagePath, http.StatusFound)
}

func (a *App) toggleRevision(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) || r.Method != http.MethodPost {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	revisionID, _ := strconv.Atoi(r.FormValue("id"))
	pagePath := r.FormValue("path")
	domain := a.siteDomain(r.Context(), r)
	enableRevision := r.FormValue("enable") == "1"
	nextActiveState := 0
	if enableRevision {
		nextActiveState = 1
	}
	_, _ = a.db.ExecContext(r.Context(), `UPDATE revisions SET is_active=? WHERE id=? AND domain=?`, nextActiveState, revisionID, domain)
	a.applyLatestActiveRevision(r.Context(), domain, pagePath)
	http.Redirect(w, r, pagePath+"?revisions", http.StatusFound)
}

func (a *App) profilePage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		if !a.hasAdmin(r.Context(), a.siteDomain(r.Context(), r)) {
			http.Redirect(w, r, r.URL.Path+"?register", http.StatusFound)
			return
		}
		http.Redirect(w, r, loginURLForRequest(r), http.StatusFound)
		return
	}
	currentEmail, found := a.currentAdminEmail(r)
	if !found {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	translations := translationsForRequest(r)
	status := ""
	if r.Method == http.MethodPost {
		nextEmail := strings.TrimSpace(r.FormValue("email"))
		nextPassword := strings.TrimSpace(r.FormValue("password"))
		confirmPassword := strings.TrimSpace(r.FormValue("password_confirm"))
		switch {
		case nextEmail == "":
			status = translationOrDefault(translations, "profile_status_email_required", "Email is required.")
		case nextPassword != "" && nextPassword != confirmPassword:
			status = translationOrDefault(translations, "profile_status_password_mismatch", "Password confirmation does not match.")
		case nextEmail == currentEmail && nextPassword == "":
			status = translationOrDefault(translations, "profile_status_updated", "Account updated.")
		default:
			domain := a.siteDomain(r.Context(), r)
			if nextEmail != currentEmail {
				var existingCount int
				_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(1) FROM users WHERE domain=? AND email=?`, domain, nextEmail).Scan(&existingCount)
				if existingCount > 0 {
					status = translationOrDefault(translations, "email_confirmation_status_email_exists", "This email address is already used.")
					break
				}
			}
			if err := a.createAndSendEmailConfirmation(r, "profile", domain, currentEmail, nextEmail, nextPassword, requestedReturnPath(r)); err != nil {
				status = err.Error()
				break
			}
			status = translationOrDefault(translations, "email_confirmation_status_sent", "A confirmation link has been sent to the email address.")
		}
	}
	a.render(w, r, "profile.html", map[string]any{
		"Email":                currentEmail,
		"Status":               status,
		"Title":                translationOrDefault(translations, "profile_title", "Account"),
		"EmailLabel":           translationOrDefault(translations, "profile_email", "Email"),
		"PasswordLabel":        translationOrDefault(translations, "profile_new_password", "New password"),
		"PasswordConfirmLabel": translationOrDefault(translations, "profile_confirm_password", "Confirm password"),
		"SaveLabel":            translationOrDefault(translations, "profile_save", "Save"),
	})
}

func (a *App) recoverPage(w http.ResponseWriter, r *http.Request) {
	translations := translationsForRequest(r)
	domain := a.siteDomain(r.Context(), r)
	languageCode := preferredLanguageCode(r.Header.Get("Accept-Language"))
	fromAddress := a.emailFromAddress(domain)
	dnsSetup, dnsSetupRequired := a.emailDNSSetupView(r.Context(), domain, fromAddress, languageCode)
	var dnsSetupView any
	if dnsSetupRequired {
		dnsSetupView = &dnsSetup
	}
	if r.Method == http.MethodGet {
		a.render(w, r, "recover.html", map[string]any{"DNSSetup": dnsSetupView, "ShowForm": !dnsSetupRequired, "ReturnPath": requestedReturnPath(r)})
		return
	}
	if dnsSetupRequired {
		a.render(w, r, "recover.html", map[string]any{"DNSSetup": dnsSetupView, "ShowForm": false, "ReturnPath": requestedReturnPath(r)})
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	captchaValue := strings.TrimSpace(r.FormValue("captcha"))
	captchaCookie, err := r.Cookie("sitebrush_captcha")
	if err != nil || captchaCookie.Value == "" || captchaCookie.Value != captchaValue {
		a.render(w, r, "recover.html", map[string]any{"Status": translationOrDefault(translations, "recover_status_captcha_invalid", "Captcha is invalid"), "ShowForm": true, "ReturnPath": requestedReturnPath(r)})
		return
	}
	var userCount int
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(1) FROM users WHERE domain=? AND email=? AND is_admin=1`, domain, email).Scan(&userCount)
	if userCount == 0 {
		http.Redirect(w, r, requestedReturnPath(r), http.StatusFound)
		return
	}
	recoveryCode := fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
	if mailError := a.enqueueEmail(r.Context(), mailout.Message{
		From:    fromAddress,
		To:      email,
		Subject: emailSubjectForLanguage(languageCode, "recover", domain),
		Body:    emailBodyForLanguage(languageCode, "recover", domain, recoveryCode),
	}); mailError != nil {
		a.render(w, r, "recover.html", map[string]any{"Status": translationOrDefault(translations, "recover_status_smtp_failed_prefix", "SMTP send failed: ") + mailError.Error(), "ShowForm": true, "ReturnPath": requestedReturnPath(r)})
		return
	}
	a.clearFailedLoginAttempts(r.Context(), domain, clientIPAddress(r))
	http.Redirect(w, r, requestedReturnPath(r), http.StatusFound)
}

func (a *App) createAndSendEmailConfirmation(r *http.Request, action, domain, currentEmail, email, password, returnPath string) error {
	translations := translationsForRequest(r)
	email = strings.TrimSpace(email)
	if _, err := stdmail.ParseAddress(email); err != nil {
		return fmt.Errorf("%s", translationOrDefault(translations, "email_confirmation_status_invalid_email", "Email address is invalid."))
	}
	if strings.TrimSpace(returnPath) == "" {
		returnPath = requestedReturnPath(r)
	}
	languageCode := preferredLanguageCode(r.Header.Get("Accept-Language"))
	token := randomAccessToken()
	now := time.Now().UTC()
	expiresAt := now.Add(emailConfirmationTTL)
	_, _ = a.db.ExecContext(r.Context(), `DELETE FROM email_confirmations WHERE expires_at<>'' AND expires_at<?`, now.Format(time.RFC3339))
	_, err := a.db.ExecContext(r.Context(), `INSERT INTO email_confirmations(token,domain,action,email,password,current_email,return_path,language_code,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		token, domain, action, email, password, strings.TrimSpace(currentEmail), returnPath, languageCode, now.Format(time.RFC3339), expiresAt.Format(time.RFC3339))
	if err != nil {
		return err
	}
	confirmationURL := emailConfirmationURL(r, token)
	fromAddress := a.emailFromAddress(domain)
	if dnsSetup, dnsSetupRequired := a.emailDNSSetupView(r.Context(), domain, fromAddress, languageCode); dnsSetupRequired {
		_, _ = a.db.ExecContext(r.Context(), `DELETE FROM email_confirmations WHERE token=?`, token)
		return errors.New(dnsSetup.PlainText)
	}
	message := mailout.Message{
		From:    fromAddress,
		To:      email,
		Subject: emailSubjectForLanguage(languageCode, action, domain),
		Body:    emailBodyForLanguage(languageCode, action, domain, confirmationURL),
	}
	if err := a.enqueueEmail(r.Context(), message); err != nil {
		_, _ = a.db.ExecContext(r.Context(), `DELETE FROM email_confirmations WHERE token=?`, token)
		return err
	}
	return nil
}

func (a *App) confirmEmailToken(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("email_confirm"))
	translations := translationsForRequest(r)
	if token == "" {
		a.renderEmailConfirmationStatus(w, r, http.StatusBadRequest, translationOrDefault(translations, "email_confirmation_status_invalid", "Confirmation link is invalid."))
		return
	}
	confirmation, found := a.emailConfirmationByToken(r.Context(), token)
	if !found {
		a.renderEmailConfirmationStatus(w, r, http.StatusNotFound, translationOrDefault(translations, "email_confirmation_status_invalid", "Confirmation link is invalid."))
		return
	}
	confirmationTranslations := translationsForLanguageCode(confirmation.LanguageCode)
	if confirmationExpired(confirmation.ExpiresAt, time.Now().UTC()) {
		_, _ = a.db.ExecContext(r.Context(), `DELETE FROM email_confirmations WHERE token=?`, token)
		a.renderEmailConfirmationStatus(w, r, http.StatusGone, translationOrDefault(confirmationTranslations, "email_confirmation_status_expired", "Confirmation link has expired."))
		return
	}
	switch strings.TrimSpace(confirmation.Action) {
	case "register":
		if a.hasAdmin(r.Context(), confirmation.Domain) {
			_, _ = a.db.ExecContext(r.Context(), `DELETE FROM email_confirmations WHERE token=?`, token)
			a.renderEmailConfirmationStatus(w, r, http.StatusConflict, translationOrDefault(confirmationTranslations, "email_confirmation_status_admin_exists", "Administrator already exists."))
			return
		}
		if _, err := a.db.ExecContext(r.Context(), `INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, confirmation.Domain, confirmation.Email, confirmation.Password); err != nil {
			a.renderEmailConfirmationStatus(w, r, http.StatusBadRequest, err.Error())
			return
		}
		_, _ = a.db.ExecContext(r.Context(), `DELETE FROM email_confirmations WHERE token=?`, token)
		a.createSession(w, r, confirmation.Email)
		http.Redirect(w, r, safeConfirmationReturnPath(confirmation.ReturnPath), http.StatusFound)
	case "profile":
		if err := a.applyProfileEmailConfirmation(r.Context(), confirmation); err != nil {
			a.renderEmailConfirmationStatus(w, r, http.StatusBadRequest, err.Error())
			return
		}
		_, _ = a.db.ExecContext(r.Context(), `DELETE FROM email_confirmations WHERE token=?`, token)
		a.createSession(w, r, confirmation.Email)
		http.Redirect(w, r, safeConfirmationReturnPath(confirmation.ReturnPath), http.StatusFound)
	default:
		a.renderEmailConfirmationStatus(w, r, http.StatusBadRequest, translationOrDefault(confirmationTranslations, "email_confirmation_status_invalid", "Confirmation link is invalid."))
	}
}

func (a *App) emailConfirmationByToken(ctx context.Context, token string) (EmailConfirmation, bool) {
	var confirmation EmailConfirmation
	err := a.db.QueryRowContext(ctx, `SELECT token,domain,action,email,password,current_email,return_path,language_code,expires_at FROM email_confirmations WHERE token=?`, token).Scan(
		&confirmation.Token, &confirmation.Domain, &confirmation.Action, &confirmation.Email, &confirmation.Password, &confirmation.CurrentEmail, &confirmation.ReturnPath, &confirmation.LanguageCode, &confirmation.ExpiresAt)
	if err != nil {
		return EmailConfirmation{}, false
	}
	return confirmation, true
}

func (a *App) applyProfileEmailConfirmation(ctx context.Context, confirmation EmailConfirmation) error {
	if strings.TrimSpace(confirmation.Password) == "" {
		result, err := a.db.ExecContext(ctx, `UPDATE users SET email=? WHERE domain=? AND email=?`, confirmation.Email, confirmation.Domain, confirmation.CurrentEmail)
		if err != nil {
			return err
		}
		if rowsAffected, err := result.RowsAffected(); err == nil && rowsAffected == 0 {
			return errors.New("account not found")
		}
		return nil
	}
	result, err := a.db.ExecContext(ctx, `UPDATE users SET email=?,password=? WHERE domain=? AND email=?`, confirmation.Email, confirmation.Password, confirmation.Domain, confirmation.CurrentEmail)
	if err != nil {
		return err
	}
	if rowsAffected, err := result.RowsAffected(); err == nil && rowsAffected == 0 {
		return errors.New("account not found")
	}
	return nil
}

func confirmationExpired(rawExpiresAt string, now time.Time) bool {
	expiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(rawExpiresAt))
	if err != nil {
		return true
	}
	return !now.Before(expiresAt)
}

func safeConfirmationReturnPath(returnPath string) string {
	trimmedReturnPath := strings.TrimSpace(returnPath)
	if trimmedReturnPath == "" || !strings.HasPrefix(trimmedReturnPath, "/") || strings.HasPrefix(trimmedReturnPath, "//") {
		return "/"
	}
	return trimmedReturnPath
}

func (a *App) renderEmailConfirmationStatus(w http.ResponseWriter, r *http.Request, statusCode int, status string) {
	w.WriteHeader(statusCode)
	a.render(w, r, "recover.html", map[string]any{"Status": status, "ShowForm": false, "ReturnPath": requestedReturnPath(r)})
}

func emailConfirmationURL(r *http.Request, token string) string {
	scheme := "http"
	if forwardedProto := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); forwardedProto == "http" || forwardedProto == "https" {
		scheme = forwardedProto
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0])
	if host == "" {
		host = r.Host
	}
	confirmationURL := url.URL{Scheme: scheme, Host: host, Path: cleanPath(r.URL.Path)}
	queryValues := confirmationURL.Query()
	queryValues.Set("email_confirm", token)
	confirmationURL.RawQuery = queryValues.Encode()
	return confirmationURL.String()
}

func (a *App) enqueueEmail(ctx context.Context, message mailout.Message) error {
	if a.emailDelivery == nil {
		a.emailDelivery = startEmailDeliveryWorker(context.Background(), a.defaultEmailSender())
	}
	select {
	case a.emailDelivery <- emailDeliveryJob{message: message}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return errors.New("email delivery queue is full")
	}
}

func startEmailDeliveryWorker(ctx context.Context, sender emailSender) chan emailDeliveryJob {
	jobs := make(chan emailDeliveryJob, emailDeliveryQueueSize)
	go runEmailDeliveryWorker(ctx, jobs, sender)
	return jobs
}

func runEmailDeliveryWorker(ctx context.Context, jobs <-chan emailDeliveryJob, sender emailSender) {
	if sender == nil {
		sender = mailout.DirectSender{}.Send
	}
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-jobs:
			sendCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			if err := sender(sendCtx, job.message); err != nil {
				log.Printf("email delivery failed to=%s subject=%q error=%v", job.message.To, job.message.Subject, err)
			}
			cancel()
		}
	}
}

func (a *App) defaultEmailSender() emailSender {
	if a.sendEmail != nil {
		return a.sendEmail
	}
	hostName := strings.TrimSpace(os.Getenv("SITEBRUSH_SMTP_HOSTNAME"))
	if hostName == "" {
		if systemHostName, err := os.Hostname(); err == nil {
			hostName = systemHostName
		}
	}
	return mailout.DirectSender{Hostname: hostName}.Send
}

func (a *App) emailFromAddress(domain string) string {
	configuredFrom := strings.TrimSpace(os.Getenv("SITEBRUSH_SMTP_FROM"))
	if configuredFrom != "" {
		return configuredFrom
	}
	normalizedDomain := normalizeDomainName(domain)
	if normalizedDomain == "" {
		normalizedDomain = canonicalLocalDomain(domain)
	}
	if normalizedDomain == "" {
		normalizedDomain = "localhost"
	}
	return "SiteBrush <sitebrush@" + normalizedDomain + ">"
}

func (a *App) emailDNSSetupView(ctx context.Context, siteDomain, fromAddress, languageCode string) (EmailDNSSetupView, bool) {
	fromDomain := emailAddressDomain(fromAddress)
	setupDomain := emailSetupDomain(siteDomain, fromDomain)
	setupFromAddress := "sitebrush@" + setupDomain
	if fromDomain != "" && fromDomain != "localhost" && net.ParseIP(fromDomain) == nil {
		parsedFromAddress, err := stdmail.ParseAddress(strings.TrimSpace(fromAddress))
		if err == nil {
			setupFromAddress = parsedFromAddress.Address
		}
	}
	serverIPs, externalIP, err := detectServerIPCandidates(ctx)
	if err != nil {
		return emailDNSSetupViewForLanguage(languageCode, setupDomain, setupFromAddress, "SERVER_IP"), true
	}
	selectedIP := selectedEmailDNSIP(externalIP, serverIPs)
	if selectedIP == nil {
		return emailDNSSetupViewForLanguage(languageCode, setupDomain, setupFromAddress, "SERVER_IP"), true
	}
	selectedIPText := selectedIP.String()
	if fromDomain == "" || fromDomain == "localhost" || net.ParseIP(fromDomain) != nil {
		return emailDNSSetupViewForLanguage(languageCode, setupDomain, setupFromAddress, selectedIPText), true
	}
	ipRecords, ipErr := lookupIPRecords(fromDomain)
	domainPointsToServer := ipErr == nil && ipRecordsAllowAnyServerIP(ipRecords, serverIPs)
	txtRecords, txtErr := lookupTXTRecords(fromDomain)
	spfAllowsServer := txtErr == nil && spfRecordsAllowAnyServerIP(txtRecords, serverIPs)
	if domainPointsToServer && spfAllowsServer {
		return EmailDNSSetupView{}, false
	}
	return emailDNSSetupViewForLanguage(languageCode, setupDomain, setupFromAddress, selectedIPText), true
}

func emailAddressDomain(rawAddress string) string {
	address, err := stdmail.ParseAddress(strings.TrimSpace(rawAddress))
	if err != nil {
		return ""
	}
	atIndex := strings.LastIndex(address.Address, "@")
	if atIndex < 0 || atIndex == len(address.Address)-1 {
		return ""
	}
	return strings.ToLower(strings.Trim(address.Address[atIndex+1:], ". "))
}

func emailSetupDomain(siteDomain, fromDomain string) string {
	for _, candidateDomain := range []string{fromDomain, normalizeDomainName(siteDomain), canonicalLocalDomain(siteDomain)} {
		candidateDomain = strings.ToLower(strings.Trim(candidateDomain, ". "))
		if candidateDomain == "" || candidateDomain == "localhost" || net.ParseIP(candidateDomain) != nil {
			continue
		}
		return candidateDomain
	}
	return "domain.com"
}

func selectedEmailDNSIP(externalIP string, serverIPs []net.IP) net.IP {
	if parsedExternalIP := net.ParseIP(strings.TrimSpace(externalIP)); parsedExternalIP != nil {
		return parsedExternalIP
	}
	for _, serverIP := range serverIPs {
		if serverIP != nil {
			return serverIP
		}
	}
	return nil
}

func suggestedSPFRecord(externalIP string, serverIPs []net.IP) string {
	selectedIP := selectedEmailDNSIP(externalIP, serverIPs)
	if selectedIP == nil {
		return ""
	}
	if selectedIP.To4() != nil {
		return "v=spf1 a mx ip4:" + selectedIP.String() + " ~all"
	}
	return "v=spf1 a mx ip6:" + selectedIP.String() + " ~all"
}

func ipRecordsAllowAnyServerIP(ipRecords []net.IP, serverIPs []net.IP) bool {
	for _, ipRecord := range ipRecords {
		for _, serverIP := range serverIPs {
			if ipRecord != nil && serverIP != nil && ipRecord.Equal(serverIP) {
				return true
			}
		}
	}
	return false
}

func spfRecordsAllowAnyServerIP(txtRecords []string, serverIPs []net.IP) bool {
	spfRecords := make([]string, 0, 1)
	for _, txtRecord := range txtRecords {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(txtRecord)), "v=spf1") {
			continue
		}
		spfRecords = append(spfRecords, txtRecord)
	}
	if len(spfRecords) != 1 {
		return false
	}
	for _, serverIP := range serverIPs {
		if serverIP != nil && spfRecordAllowsIP(spfRecords[0], serverIP) {
			return true
		}
	}
	return false
}

func spfRecordAllowsIP(spfRecord string, serverIP net.IP) bool {
	for _, mechanism := range strings.Fields(strings.ToLower(strings.TrimSpace(spfRecord))) {
		mechanism = strings.TrimLeft(mechanism, "+")
		if strings.HasPrefix(mechanism, "ip4:") && serverIP.To4() != nil {
			if spfIPMechanismMatches(strings.TrimPrefix(mechanism, "ip4:"), serverIP) {
				return true
			}
		}
		if strings.HasPrefix(mechanism, "ip6:") && serverIP.To4() == nil {
			if spfIPMechanismMatches(strings.TrimPrefix(mechanism, "ip6:"), serverIP) {
				return true
			}
		}
	}
	return false
}

func spfIPMechanismMatches(rawMechanism string, serverIP net.IP) bool {
	ipPart := rawMechanism
	prefixLength := -1
	if slashIndex := strings.Index(rawMechanism, "/"); slashIndex >= 0 {
		ipPart = rawMechanism[:slashIndex]
		parsedPrefix, err := strconv.Atoi(rawMechanism[slashIndex+1:])
		if err == nil {
			prefixLength = parsedPrefix
		}
	}
	parsedIP := net.ParseIP(strings.TrimSpace(ipPart))
	if parsedIP == nil {
		return false
	}
	if prefixLength >= 0 {
		bitLength := 128
		if parsedIP.To4() != nil {
			bitLength = 32
			parsedIP = parsedIP.To4()
			serverIP = serverIP.To4()
		}
		if serverIP == nil || prefixLength < 0 || prefixLength > bitLength {
			return false
		}
		ipNet := net.IPNet{IP: parsedIP, Mask: net.CIDRMask(prefixLength, bitLength)}
		return ipNet.Contains(serverIP)
	}
	return parsedIP.Equal(serverIP)
}

func emailSubjectForLanguage(languageCode, action, domain string) string {
	domain = strings.TrimSpace(domain)
	switch strings.ToLower(strings.TrimSpace(languageCode)) {
	case "ru":
		if action == "recover" {
			return "Код восстановления SiteBrush"
		}
		return "Подтвердите email для " + domain
	case "fr":
		if action == "recover" {
			return "Code de récupération SiteBrush"
		}
		return "Confirmez votre e-mail pour " + domain
	case "ja":
		if action == "recover" {
			return "SiteBrush 復旧コード"
		}
		return domain + " のメール確認"
	case "it":
		if action == "recover" {
			return "Codice di recupero SiteBrush"
		}
		return "Conferma l'email per " + domain
	case "sv":
		if action == "recover" {
			return "SiteBrush återställningskod"
		}
		return "Bekräfta e-post för " + domain
	case "fi":
		if action == "recover" {
			return "SiteBrush-palautuskoodi"
		}
		return "Vahvista sähköposti kohteelle " + domain
	case "mn":
		if action == "recover" {
			return "SiteBrush сэргээх код"
		}
		return domain + " домэйны имэйлийг баталгаажуулна уу"
	case "zh":
		if action == "recover" {
			return "SiteBrush 恢复代码"
		}
		return "确认 " + domain + " 的邮箱"
	case "he":
		if action == "recover" {
			return "קוד שחזור של SiteBrush"
		}
		return "אימות אימייל עבור " + domain
	case "fa":
		if action == "recover" {
			return "کد بازیابی SiteBrush"
		}
		return "تأیید ایمیل برای " + domain
	case "de":
		if action == "recover" {
			return "SiteBrush-Wiederherstellungscode"
		}
		return "E-Mail für " + domain + " bestätigen"
	case "tr":
		if action == "recover" {
			return "SiteBrush kurtarma kodu"
		}
		return domain + " için e-postayı doğrulayın"
	case "kk":
		if action == "recover" {
			return "SiteBrush қалпына келтіру коды"
		}
		return domain + " үшін email растаңыз"
	case "es":
		if action == "recover" {
			return "Código de recuperación de SiteBrush"
		}
		return "Confirma el correo para " + domain
	case "pt":
		if action == "recover" {
			return "Código de recuperação do SiteBrush"
		}
		return "Confirme o e-mail de " + domain
	default:
		if action == "recover" {
			return "SiteBrush recovery code"
		}
		return "Confirm email for " + domain
	}
}

func emailDNSSetupViewForLanguage(languageCode, domain, fromAddress, serverIP string) EmailDNSSetupView {
	if strings.TrimSpace(domain) == "" {
		domain = "example.com"
	}
	if strings.TrimSpace(fromAddress) == "" {
		fromAddress = "sitebrush@" + domain
	}
	if strings.TrimSpace(serverIP) == "" {
		serverIP = "1.2.3.4"
	}
	addressRecordType := "A"
	spfIPMechanism := "ip4:" + serverIP
	if parsedIP := net.ParseIP(serverIP); parsedIP != nil && parsedIP.To4() == nil {
		addressRecordType = "AAAA"
		spfIPMechanism = "ip6:" + serverIP
	}
	dnsDomain := strings.Trim(domain, ".") + "."
	addressRecord := fmt.Sprintf("%s IN %s %s", dnsDomain, addressRecordType, serverIP)
	spfRecord := fmt.Sprintf("%s IN TXT \"v=spf1 a mx %s ~all\"", dnsDomain, spfIPMechanism)
	view := EmailDNSSetupView{
		Records: []EmailDNSRecordView{
			{Label: "A/AAAA", Value: addressRecord},
			{Label: "SPF TXT", Value: spfRecord},
		},
		CopyLabel: "Copy",
	}
	switch strings.ToLower(strings.TrimSpace(languageCode)) {
	case "ru":
		view.Title = "Сначала настройте DNS для отправки писем"
		view.Intro = fmt.Sprintf("Восстановление пароля через email будет работать только после того, как домен %s будет настроен для этого сайта.", domain)
		view.Explanation = fmt.Sprintf("SiteBrush будет отправлять письма с адреса %s. Почтовые серверы принимают такие письма только когда DNS домена подтверждает, что этот сервер имеет право отправлять почту от имени домена. Поэтому нужны A/AAAA запись для домена и SPF TXT запись.", fromAddress)
		view.DNSIntro = "Откройте DNS-настройки домена у регистратора или хостинг-провайдера и добавьте или обновите эти записи:"
		view.CopyLabel = "Копировать"
		view.AfterText = "После обновления DNS вернитесь на эту страницу. Когда записи начнут проверяться, появится форма ввода email для восстановления пароля."
	case "fr":
		view.Title = "Configurez d'abord le DNS pour envoyer les e-mails"
		view.Intro = fmt.Sprintf("La récupération du mot de passe par e-mail fonctionnera seulement après la configuration du domaine %s pour ce site.", domain)
		view.Explanation = fmt.Sprintf("SiteBrush enverra les messages depuis %s. Les serveurs de messagerie acceptent cela seulement si le DNS du domaine confirme que ce serveur peut envoyer du courrier pour ce domaine. Il faut donc un enregistrement A/AAAA et un enregistrement TXT SPF.", fromAddress)
		view.DNSIntro = "Ouvrez les paramètres DNS chez votre registraire ou hébergeur et ajoutez ou mettez à jour ces enregistrements :"
		view.CopyLabel = "Copier"
		view.AfterText = "Après la mise à jour DNS, revenez sur cette page. Quand les enregistrements seront valides, le formulaire de récupération apparaîtra."
	case "ja":
		view.Title = "まずメール送信用の DNS を設定してください"
		view.Intro = fmt.Sprintf("メールによるパスワード復旧は、ドメイン %s がこのサイト用に設定された後にだけ動作します。", domain)
		view.Explanation = fmt.Sprintf("SiteBrush は %s からメールを送信します。受信側メールサーバーは、このサーバーがそのドメインのメールを送信できることを DNS が示している場合だけ受け入れます。そのため A/AAAA レコードと SPF TXT レコードが必要です。", fromAddress)
		view.DNSIntro = "レジストラまたはホスティング事業者の DNS 設定を開き、次のレコードを追加または更新してください:"
		view.CopyLabel = "コピー"
		view.AfterText = "DNS 更新後、このページに戻ってください。レコードが確認できると、復旧用メールアドレスの入力フォームが表示されます。"
	case "it":
		view.Title = "Prima configura il DNS per inviare email"
		view.Intro = fmt.Sprintf("Il recupero password via email funzionerà solo dopo aver configurato il dominio %s per questo sito.", domain)
		view.Explanation = fmt.Sprintf("SiteBrush invierà email da %s. I server di posta accettano questi messaggi solo se il DNS del dominio conferma che questo server può inviare posta per il dominio. Servono quindi un record A/AAAA e un record TXT SPF.", fromAddress)
		view.DNSIntro = "Apri le impostazioni DNS dal registrar o hosting provider e aggiungi o aggiorna questi record:"
		view.CopyLabel = "Copia"
		view.AfterText = "Dopo l'aggiornamento DNS torna su questa pagina. Quando i record saranno validi, apparirà il modulo per inserire l'email di recupero."
	case "sv":
		view.Title = "Ställ först in DNS för e-post"
		view.Intro = fmt.Sprintf("Lösenordsåterställning via e-post fungerar först när domänen %s är inställd för den här webbplatsen.", domain)
		view.Explanation = fmt.Sprintf("SiteBrush skickar e-post från %s. Mottagande e-postservrar accepterar det bara när domänens DNS visar att den här servern får skicka e-post för domänen. Därför behövs en A/AAAA-post och en SPF TXT-post.", fromAddress)
		view.DNSIntro = "Öppna DNS-inställningarna hos din registrar eller värdleverantör och lägg till eller uppdatera dessa poster:"
		view.CopyLabel = "Kopiera"
		view.AfterText = "När DNS har uppdaterats, kom tillbaka till sidan. När posterna är giltiga visas formuläret för återställningsadressen."
	case "fi":
		view.Title = "Määritä ensin DNS sähköpostin lähetystä varten"
		view.Intro = fmt.Sprintf("Salasanan palautus sähköpostilla toimii vasta, kun verkkotunnus %s on määritetty tälle sivustolle.", domain)
		view.Explanation = fmt.Sprintf("SiteBrush lähettää sähköpostia osoitteesta %s. Vastaanottavat sähköpostipalvelimet hyväksyvät sen vain, jos verkkotunnuksen DNS vahvistaa, että tämä palvelin saa lähettää postia verkkotunnuksen puolesta. Siksi tarvitaan A/AAAA-tietue ja SPF TXT -tietue.", fromAddress)
		view.DNSIntro = "Avaa DNS-asetukset rekisteröijän tai palveluntarjoajan hallinnassa ja lisää tai päivitä nämä tietueet:"
		view.CopyLabel = "Kopioi"
		view.AfterText = "Kun DNS on päivittynyt, palaa tälle sivulle. Kun tietueet ovat voimassa, palautussähköpostin lomake tulee näkyviin."
	case "mn":
		view.Title = "Эхлээд имэйл илгээх DNS тохируулна уу"
		view.Intro = fmt.Sprintf("Имэйлээр нууц үг сэргээх нь %s домэйныг энэ сайтад тохируулсны дараа л ажиллана.", domain)
		view.Explanation = fmt.Sprintf("SiteBrush %s хаягаас имэйл илгээнэ. Хүлээн авагч шуудангийн серверүүд энэ сервер тухайн домэйны өмнөөс имэйл илгээх эрхтэйг DNS баталсан үед л захидлыг авна. Тиймээс A/AAAA бичлэг болон SPF TXT бичлэг хэрэгтэй.", fromAddress)
		view.DNSIntro = "Домэйн бүртгэгч эсвэл хостингийн DNS тохиргоог нээгээд эдгээр бичлэгийг нэмэх эсвэл шинэчилнэ үү:"
		view.CopyLabel = "Хуулах"
		view.AfterText = "DNS шинэчлэгдсэний дараа энэ хуудсанд буцаж ирнэ үү. Бичлэгүүд зөв шалгагдвал сэргээх email оруулах маягт гарч ирнэ."
	case "zh":
		view.Title = "请先配置用于发信的 DNS"
		view.Intro = fmt.Sprintf("只有当域名 %s 已为此网站正确配置后，才能通过邮箱恢复密码。", domain)
		view.Explanation = fmt.Sprintf("SiteBrush 会从 %s 发送邮件。收件方邮件服务器只有在 DNS 证明此服务器有权代表该域名发信时才会接收邮件。因此需要 A/AAAA 记录和 SPF TXT 记录。", fromAddress)
		view.DNSIntro = "在域名注册商或主机服务商的 DNS 设置中添加或更新这些记录："
		view.CopyLabel = "复制"
		view.AfterText = "DNS 更新后回到此页面。记录验证通过后，将显示输入恢复邮箱的表单。"
	case "he":
		view.Title = "קודם יש להגדיר DNS לשליחת אימייל"
		view.Intro = fmt.Sprintf("שחזור סיסמה בדוא״ל יעבוד רק אחרי שהדומיין %s יוגדר עבור האתר הזה.", domain)
		view.Explanation = fmt.Sprintf("SiteBrush ישלח דוא״ל מהכתובת %s. שרתי דוא״ל מקבלים הודעות כאלה רק כאשר ה-DNS של הדומיין מאשר שהשרת הזה רשאי לשלוח דוא״ל בשם הדומיין. לכן נדרשות רשומת A/AAAA ורשומת TXT של SPF.", fromAddress)
		view.DNSIntro = "פתח את הגדרות ה-DNS אצל הרשם או ספק האחסון והוסף או עדכן את הרשומות האלה:"
		view.CopyLabel = "העתק"
		view.AfterText = "לאחר עדכון ה-DNS חזור לעמוד הזה. כשהרשומות יהיו תקינות, יופיע טופס הזנת כתובת לשחזור."
	case "fa":
		view.Title = "ابتدا DNS را برای ارسال ایمیل تنظیم کنید"
		view.Intro = fmt.Sprintf("بازیابی رمز عبور با ایمیل فقط پس از تنظیم دامنه %s برای این سایت کار می‌کند.", domain)
		view.Explanation = fmt.Sprintf("SiteBrush ایمیل‌ها را از %s ارسال می‌کند. سرورهای ایمیل فقط وقتی این پیام‌ها را می‌پذیرند که DNS دامنه تأیید کند این سرور اجازه ارسال ایمیل از طرف دامنه را دارد. بنابراین به رکورد A/AAAA و رکورد TXT مربوط به SPF نیاز است.", fromAddress)
		view.DNSIntro = "تنظیمات DNS را در رجیسترار یا میزبان خود باز کنید و این رکوردها را اضافه یا به‌روزرسانی کنید:"
		view.CopyLabel = "کپی"
		view.AfterText = "پس از به‌روزرسانی DNS به این صفحه برگردید. وقتی رکوردها معتبر شوند، فرم ورود ایمیل بازیابی نمایش داده می‌شود."
	case "de":
		view.Title = "Richten Sie zuerst DNS für den E-Mail-Versand ein"
		view.Intro = fmt.Sprintf("Passwort-Wiederherstellung per E-Mail funktioniert erst, wenn die Domain %s für diese Website eingerichtet ist.", domain)
		view.Explanation = fmt.Sprintf("SiteBrush sendet E-Mails von %s. Empfangende Mailserver akzeptieren diese Nachrichten nur, wenn das DNS der Domain bestätigt, dass dieser Server für die Domain senden darf. Dafür werden ein A/AAAA-Eintrag und ein SPF-TXT-Eintrag benötigt.", fromAddress)
		view.DNSIntro = "Öffnen Sie die DNS-Einstellungen beim Registrar oder Hosting-Anbieter und fügen Sie diese Einträge hinzu oder aktualisieren Sie sie:"
		view.CopyLabel = "Kopieren"
		view.AfterText = "Kehren Sie nach der DNS-Aktualisierung zu dieser Seite zurück. Sobald die Einträge gültig sind, erscheint das Formular für die Wiederherstellungs-E-Mail."
	case "tr":
		view.Title = "Önce e-posta göndermek için DNS ayarlayın"
		view.Intro = fmt.Sprintf("E-posta ile parola kurtarma, %s alan adı bu site için ayarlandıktan sonra çalışır.", domain)
		view.Explanation = fmt.Sprintf("SiteBrush e-postaları %s adresinden gönderir. Alıcı posta sunucuları bu iletileri yalnızca alan adının DNS'i bu sunucunun alan adı adına posta gönderebileceğini doğrularsa kabul eder. Bu yüzden A/AAAA kaydı ve SPF TXT kaydı gerekir.", fromAddress)
		view.DNSIntro = "Alan adı kayıt kuruluşunuzdaki veya barındırma sağlayıcınızdaki DNS ayarlarını açıp bu kayıtları ekleyin ya da güncelleyin:"
		view.CopyLabel = "Kopyala"
		view.AfterText = "DNS güncellendikten sonra bu sayfaya dönün. Kayıtlar geçerli olduğunda kurtarma e-postası formu görünecektir."
	case "kk":
		view.Title = "Алдымен email жіберу үшін DNS баптаңыз"
		view.Intro = fmt.Sprintf("Email арқылы құпиясөзді қалпына келтіру %s домені осы сайтқа бапталғаннан кейін ғана жұмыс істейді.", domain)
		view.Explanation = fmt.Sprintf("SiteBrush хаттарды %s мекенжайынан жібереді. Қабылдаушы пошта серверлері DNS осы сервердің домен атынан хат жіберуге құқығы бар екенін растаса ғана хаттарды қабылдайды. Сондықтан A/AAAA жазбасы және SPF TXT жазбасы қажет.", fromAddress)
		view.DNSIntro = "Домен тіркеушісіндегі немесе хостинг провайдеріндегі DNS баптауларын ашып, мына жазбаларды қосыңыз немесе жаңартыңыз:"
		view.CopyLabel = "Көшіру"
		view.AfterText = "DNS жаңартылғаннан кейін осы бетке қайта кіріңіз. Жазбалар дұрыс тексерілсе, қалпына келтіру email енгізу формасы шығады."
	case "es":
		view.Title = "Primero configura DNS para enviar correo"
		view.Intro = fmt.Sprintf("La recuperación de contraseña por email funcionará solo cuando el dominio %s esté configurado para este sitio.", domain)
		view.Explanation = fmt.Sprintf("SiteBrush enviará correos desde %s. Los servidores de correo solo aceptan esos mensajes si el DNS del dominio confirma que este servidor puede enviar correo por el dominio. Por eso se necesita un registro A/AAAA y un registro TXT SPF.", fromAddress)
		view.DNSIntro = "Abre los ajustes DNS en tu registrador o proveedor de hosting y añade o actualiza estos registros:"
		view.CopyLabel = "Copiar"
		view.AfterText = "Después de actualizar DNS, vuelve a esta página. Cuando los registros sean válidos, aparecerá el formulario para introducir el email de recuperación."
	case "pt":
		view.Title = "Primeiro configure o DNS para enviar e-mail"
		view.Intro = fmt.Sprintf("A recuperação de senha por e-mail só funcionará depois que o domínio %s estiver configurado para este site.", domain)
		view.Explanation = fmt.Sprintf("O SiteBrush enviará e-mails de %s. Servidores de e-mail só aceitam essas mensagens quando o DNS do domínio confirma que este servidor pode enviar e-mail pelo domínio. Por isso são necessários um registro A/AAAA e um registro TXT SPF.", fromAddress)
		view.DNSIntro = "Abra as configurações DNS no registrador ou provedor de hospedagem e adicione ou atualize estes registros:"
		view.CopyLabel = "Copiar"
		view.AfterText = "Depois que o DNS atualizar, volte a esta página. Quando os registros forem válidos, o formulário para inserir o e-mail de recuperação aparecerá."
	default:
		view.Title = "Configure DNS before sending email"
		view.Intro = fmt.Sprintf("Password recovery by email will work only after the domain %s is configured for this site.", domain)
		view.Explanation = fmt.Sprintf("SiteBrush will send email from %s. Receiving mail servers accept those messages only when the domain DNS confirms that this server may send mail for the domain. That is why the domain needs an A/AAAA record and an SPF TXT record.", fromAddress)
		view.DNSIntro = "Open DNS settings at your domain registrar or hosting provider and add or update these records:"
		view.AfterText = "After DNS updates, return to this page. When the records validate, the recovery email form will appear."
	}
	view.PlainText = strings.Join([]string{view.Title, view.Intro, view.Explanation, view.DNSIntro, addressRecord, spfRecord, view.AfterText}, " ")
	return view
}

func emailBodyForLanguage(languageCode, action, domain, secret string) string {
	if action == "recover" {
		return recoveryEmailBodyForLanguage(languageCode, domain, secret)
	}
	return confirmationEmailBodyForLanguage(languageCode, domain, secret)
}

func confirmationEmailBodyForLanguage(languageCode, domain, confirmationURL string) string {
	switch strings.ToLower(strings.TrimSpace(languageCode)) {
	case "ru":
		return fmt.Sprintf("Для подтверждения email в SiteBrush для %s откройте ссылку:\n\n%s\n\nЕсли вы не запрашивали это действие, просто проигнорируйте письмо.", domain, confirmationURL)
	case "fr":
		return fmt.Sprintf("Pour confirmer votre e-mail dans SiteBrush pour %s, ouvrez ce lien :\n\n%s\n\nSi vous n'avez pas demandé cette action, ignorez ce message.", domain, confirmationURL)
	case "ja":
		return fmt.Sprintf("%s の SiteBrush メールを確認するには、次のリンクを開いてください。\n\n%s\n\nこの操作に心当たりがない場合は、このメールを無視してください。", domain, confirmationURL)
	case "it":
		return fmt.Sprintf("Per confermare l'email in SiteBrush per %s, apri questo link:\n\n%s\n\nSe non hai richiesto questa operazione, ignora questo messaggio.", domain, confirmationURL)
	case "sv":
		return fmt.Sprintf("Bekräfta e-postadressen i SiteBrush för %s genom att öppna länken:\n\n%s\n\nOm du inte begärde detta kan du ignorera meddelandet.", domain, confirmationURL)
	case "fi":
		return fmt.Sprintf("Vahvista SiteBrush-sähköposti kohteelle %s avaamalla linkki:\n\n%s\n\nJos et pyytänyt tätä, voit ohittaa viestin.", domain, confirmationURL)
	case "mn":
		return fmt.Sprintf("%s домэйны SiteBrush имэйлийг баталгаажуулахын тулд энэ холбоосыг нээнэ үү:\n\n%s\n\nХэрэв та энэ үйлдлийг хүсээгүй бол энэ захидлыг үл тооно уу.", domain, confirmationURL)
	case "zh":
		return fmt.Sprintf("要确认 %s 的 SiteBrush 邮箱，请打开此链接：\n\n%s\n\n如果这不是你发起的操作，请忽略这封邮件。", domain, confirmationURL)
	case "he":
		return fmt.Sprintf("כדי לאמת את האימייל ב-SiteBrush עבור %s, פתח את הקישור:\n\n%s\n\nאם לא ביקשת פעולה זו, אפשר להתעלם מההודעה.", domain, confirmationURL)
	case "fa":
		return fmt.Sprintf("برای تأیید ایمیل در SiteBrush برای %s این پیوند را باز کنید:\n\n%s\n\nاگر شما این کار را درخواست نکرده‌اید، این پیام را نادیده بگیرید.", domain, confirmationURL)
	case "de":
		return fmt.Sprintf("Öffnen Sie diesen Link, um die E-Mail in SiteBrush für %s zu bestätigen:\n\n%s\n\nWenn Sie diese Aktion nicht angefordert haben, ignorieren Sie diese Nachricht.", domain, confirmationURL)
	case "tr":
		return fmt.Sprintf("%s için SiteBrush e-postasını doğrulamak üzere bağlantıyı açın:\n\n%s\n\nBu işlemi siz istemediyseniz bu iletiyi yok sayın.", domain, confirmationURL)
	case "kk":
		return fmt.Sprintf("%s үшін SiteBrush email мекенжайын растау үшін мына сілтемені ашыңыз:\n\n%s\n\nЕгер бұл әрекетті сұрамаған болсаңыз, хатты елемеңіз.", domain, confirmationURL)
	case "es":
		return fmt.Sprintf("Para confirmar el correo en SiteBrush para %s, abre este enlace:\n\n%s\n\nSi no solicitaste esta acción, ignora este mensaje.", domain, confirmationURL)
	case "pt":
		return fmt.Sprintf("Para confirmar o e-mail no SiteBrush para %s, abra este link:\n\n%s\n\nSe você não solicitou esta ação, ignore esta mensagem.", domain, confirmationURL)
	default:
		return fmt.Sprintf("To confirm the email address in SiteBrush for %s, open this link:\n\n%s\n\nIf you did not request this action, ignore this message.", domain, confirmationURL)
	}
}

func recoveryEmailBodyForLanguage(languageCode, domain, code string) string {
	switch strings.ToLower(strings.TrimSpace(languageCode)) {
	case "ru":
		return fmt.Sprintf("Код восстановления SiteBrush для %s: %s\n\nЕсли вы не запрашивали восстановление, проигнорируйте письмо.", domain, code)
	case "fr":
		return fmt.Sprintf("Code de récupération SiteBrush pour %s : %s\n\nSi vous n'avez pas demandé de récupération, ignorez ce message.", domain, code)
	case "ja":
		return fmt.Sprintf("%s の SiteBrush 復旧コード: %s\n\n復旧を要求していない場合は、このメールを無視してください。", domain, code)
	case "it":
		return fmt.Sprintf("Codice di recupero SiteBrush per %s: %s\n\nSe non hai richiesto il recupero, ignora questo messaggio.", domain, code)
	case "sv":
		return fmt.Sprintf("SiteBrush återställningskod för %s: %s\n\nOm du inte begärde återställning kan du ignorera meddelandet.", domain, code)
	case "fi":
		return fmt.Sprintf("SiteBrush-palautuskoodi kohteelle %s: %s\n\nJos et pyytänyt palautusta, voit ohittaa viestin.", domain, code)
	case "mn":
		return fmt.Sprintf("%s домэйны SiteBrush сэргээх код: %s\n\nХэрэв та сэргээх хүсэлт гаргаагүй бол энэ захидлыг үл тооно уу.", domain, code)
	case "zh":
		return fmt.Sprintf("%s 的 SiteBrush 恢复代码：%s\n\n如果你没有请求恢复，请忽略这封邮件。", domain, code)
	case "he":
		return fmt.Sprintf("קוד שחזור של SiteBrush עבור %s: %s\n\nאם לא ביקשת שחזור, אפשר להתעלם מההודעה.", domain, code)
	case "fa":
		return fmt.Sprintf("کد بازیابی SiteBrush برای %s: %s\n\nاگر بازیابی را درخواست نکرده‌اید، این پیام را نادیده بگیرید.", domain, code)
	case "de":
		return fmt.Sprintf("SiteBrush-Wiederherstellungscode für %s: %s\n\nWenn Sie keine Wiederherstellung angefordert haben, ignorieren Sie diese Nachricht.", domain, code)
	case "tr":
		return fmt.Sprintf("%s için SiteBrush kurtarma kodu: %s\n\nKurtarma istemediyseniz bu iletiyi yok sayın.", domain, code)
	case "kk":
		return fmt.Sprintf("%s үшін SiteBrush қалпына келтіру коды: %s\n\nҚалпына келтіруді сұрамаған болсаңыз, хатты елемеңіз.", domain, code)
	case "es":
		return fmt.Sprintf("Código de recuperación de SiteBrush para %s: %s\n\nSi no solicitaste la recuperación, ignora este mensaje.", domain, code)
	case "pt":
		return fmt.Sprintf("Código de recuperação do SiteBrush para %s: %s\n\nSe você não solicitou recuperação, ignore esta mensagem.", domain, code)
	default:
		return fmt.Sprintf("SiteBrush recovery code for %s: %s\n\nIf you did not request recovery, ignore this message.", domain, code)
	}
}

func (a *App) captchaImage(w http.ResponseWriter, r *http.Request) {
	captchaCode := fmt.Sprintf("%04d", time.Now().UnixNano()%10000)
	http.SetCookie(w, &http.Cookie{Name: "sitebrush_captcha", Value: captchaCode, Path: "/", HttpOnly: true, MaxAge: 300})
	w.Header().Set("Content-Type", "image/svg+xml")
	_, _ = w.Write([]byte("<svg xmlns='http://www.w3.org/2000/svg' width='140' height='40'><rect width='100%' height='100%' fill='#f4f4f4'/><text x='15' y='28' font-size='24' font-family='monospace' fill='#333'>" + captchaCode + "</text></svg>"))
}

func (a *App) filesPage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		if !a.hasAdmin(r.Context(), a.siteDomain(r.Context(), r)) {
			http.Redirect(w, r, r.URL.Path+"?register", http.StatusFound)
			return
		}
		http.Redirect(w, r, loginURLForRequest(r), http.StatusFound)
		return
	}
	currentPath := currentFilesPath(r)
	if r.Method == http.MethodPost {
		fileName := safeRelativeAssetPath(r.FormValue("name"))
		action := r.FormValue("action")
		if action == "upload" {
			a.uploadFiles(w, r, currentPath)
			return
		}
		if action == "save_access" && fileName != "" {
			a.saveFileAccessRule(r.Context(), r, fileName)
		} else if fileName != "" {
			fileSize := fileSizeBytes(filepath.Join(a.domainFilesDir(r), fileName))
			_ = a.applyDomainStorageDelta(r.Context(), a.siteDomain(r.Context(), r), 0, 0, 0, -fileSize, 0)
			_ = os.Remove(filepath.Join(a.domainFilesDir(r), fileName))
			_, _ = a.db.ExecContext(r.Context(), `DELETE FROM file_access_rules WHERE domain=? AND file_name=?`, domainStorageName(a.siteDomain(r.Context(), r)), fileName)
			_, _ = a.db.ExecContext(r.Context(), `DELETE FROM file_metadata WHERE domain=? AND file_name=?`, domainStorageName(a.siteDomain(r.Context(), r)), fileName)
		}
		http.Redirect(w, r, currentPath+"?files", http.StatusFound)
		return
	}
	fileList, listErr := a.listManagedFiles(r.Context(), r, currentPath)
	if listErr != nil {
		a.render(w, r, "files.html", map[string]any{"Path": currentPath, "Files": []ManagedFile{}, "NativeFileDialog": a.nativeFileDialog})
		return
	}
	accessRulesByName := a.fileAccessRulesByName(r.Context(), r)
	for index := range fileList {
		accessRule := accessRulesByName[fileList[index].Name]
		if strings.TrimSpace(accessRule.AccessMode) == "" {
			accessRule.AccessMode = "public"
		}
		fileList[index].AssetPath = "/p/" + fileList[index].Name
		fileList[index].AccessMode = accessRule.AccessMode
		fileList[index].Token = accessRule.Token
		fileList[index].ExpiresAt = accessRule.ExpiresAt
		fileList[index].SingleUseLeft = accessRule.SingleUseLeft
		fileList[index].TokenUseCount = accessRule.TokenUseCount
	}
	a.render(w, r, "files.html", map[string]any{"Path": currentPath, "Files": fileList, "NativeFileDialog": a.nativeFileDialog})
}

func (a *App) nativePickedFilesJSON(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	selectedPaths, err := desktop.PickFiles()
	if err != nil {
		if errors.Is(err, desktop.ErrNativeDialogCanceled) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(nativePickedFilesResponse{Files: []nativePickedFile{}})
			return
		}
		http.Error(w, err.Error(), http.StatusNotImplemented)
		return
	}
	pickedFiles := make([]nativePickedFile, 0, len(selectedPaths))
	for _, selectedPath := range selectedPaths {
		fileInfo, statErr := os.Stat(selectedPath)
		if statErr != nil || fileInfo.IsDir() {
			continue
		}
		fileBytes, readErr := os.ReadFile(selectedPath)
		if readErr != nil {
			continue
		}
		mimeType := mime.TypeByExtension(path.Ext(selectedPath))
		if mimeType == "" {
			mimeType = http.DetectContentType(fileBytes)
		}
		pickedFiles = append(pickedFiles, nativePickedFile{
			Name:    filepath.Base(selectedPath),
			Mime:    mimeType,
			Content: base64.StdEncoding.EncodeToString(fileBytes),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(nativePickedFilesResponse{Files: pickedFiles})
}

func (a *App) servePublicAsset(w http.ResponseWriter, r *http.Request) {
	requestDomain := domainFromContext(r.Context())
	fileName := publicAssetFileNameFromPath(r.URL.Path, requestDomain)
	if fileName == "" {
		http.NotFound(w, r)
		return
	}
	domain := domainStorageName(requestDomain)
	if !a.publicAssetExists(domain, fileName) {
		primaryDomain := a.siteDomain(r.Context(), r)
		if primaryDomain != requestDomain {
			domain = domainStorageName(primaryDomain)
		}
	}
	a.serveAssetFile(w, r, domain, fileName)
}

func (a *App) listManagedFiles(ctx context.Context, r *http.Request, currentPath string) ([]ManagedFile, error) {
	fileList := make([]ManagedFile, 0, 32)
	rootPath := a.domainFilesDir(r)
	domain := domainStorageName(a.siteDomain(ctx, r))
	metadata := a.fileMetadataByName(ctx, domain)
	currentPath = cleanPath(currentPath)
	walkErr := filepath.WalkDir(rootPath, func(filePath string, currentEntry fs.DirEntry, entryErr error) error {
		if entryErr != nil || currentEntry.IsDir() {
			return nil
		}
		relativePath, relErr := filepath.Rel(rootPath, filePath)
		if relErr != nil {
			return nil
		}
		normalizedPath := filepath.ToSlash(relativePath)
		if safeRelativeAssetPath(normalizedPath) == "" {
			return nil
		}
		fileInfo, statErr := currentEntry.Info()
		if statErr != nil {
			return nil
		}
		meta := metadata[normalizedPath]
		if meta.PagePath == "" && currentPath != "/" {
			return nil
		}
		if meta.PagePath != "" && !filePathBelongsToBranch(meta.PagePath, currentPath) {
			return nil
		}
		size := fileInfo.Size()
		if meta.Size > 0 {
			size = meta.Size
		}
		createdUnix := fileInfo.ModTime().Unix()
		createdAt := fileInfo.ModTime().Format("2006-01-02 15:04")
		if meta.CreatedAt != "" {
			if parsedTime, parseErr := time.Parse(time.RFC3339, meta.CreatedAt); parseErr == nil {
				createdUnix = parsedTime.Unix()
				createdAt = parsedTime.Local().Format("2006-01-02 15:04")
			}
		}
		mimeType := meta.MimeType
		if mimeType == "" {
			mimeType = mime.TypeByExtension(path.Ext(normalizedPath))
		}
		fileList = append(fileList, ManagedFile{
			Name:          normalizedPath,
			Size:          size,
			SizeLabel:     formatFileSize(size),
			PagePath:      meta.PagePath,
			MimeType:      mimeType,
			Extension:     fileExtensionLabel(normalizedPath),
			CreatedAt:     createdAt,
			CreatedUnix:   createdUnix,
			IsImage:       isImageFile(normalizedPath, mimeType),
			DownloadCount: meta.DownloadCount,
		})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Slice(fileList, func(leftIndex, rightIndex int) bool {
		return fileList[leftIndex].CreatedUnix > fileList[rightIndex].CreatedUnix
	})
	return fileList, nil
}

func (a *App) uploadFiles(w http.ResponseWriter, r *http.Request, currentPath string) {
	if err := r.ParseMultipartForm(128 << 20); err != nil {
		http.Error(w, "failed to parse uploaded files", http.StatusBadRequest)
		return
	}
	if r.MultipartForm == nil || len(r.MultipartForm.File["upload_files"]) == 0 {
		http.Error(w, "no files selected", http.StatusBadRequest)
		return
	}

	domain := domainStorageName(a.siteDomain(r.Context(), r))
	siteDomain := a.siteDomain(r.Context(), r)
	baseDir := a.domainFilesDir(r)
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		http.Error(w, "failed to create files directory", http.StatusInternalServerError)
		return
	}

	uploadedNames := make([]string, 0, len(r.MultipartForm.File["upload_files"]))
	for _, fileHeader := range r.MultipartForm.File["upload_files"] {
		fileName := safeFileName(fileHeader.Filename)
		if fileName == "" {
			continue
		}
		reservedFileBytes := fileHeader.Size
		if reservedFileBytes > 0 {
			if storageErr := a.applyDomainStorageDelta(r.Context(), siteDomain, 0, 0, 0, reservedFileBytes, 0); storageErr != nil {
				http.Error(w, storageErr.Error(), http.StatusInsufficientStorage)
				return
			}
		}
		sourceFile, openErr := fileHeader.Open()
		if openErr != nil {
			if reservedFileBytes > 0 {
				_ = a.applyDomainStorageDelta(r.Context(), siteDomain, 0, 0, 0, -reservedFileBytes, 0)
			}
			continue
		}

		storedName := uniqueUploadedFileName(baseDir, fileName)
		targetPath := filepath.Join(baseDir, storedName)
		targetFile, createErr := os.Create(targetPath)
		if createErr != nil {
			_ = sourceFile.Close()
			if reservedFileBytes > 0 {
				_ = a.applyDomainStorageDelta(r.Context(), siteDomain, 0, 0, 0, -reservedFileBytes, 0)
			}
			continue
		}
		writtenBytes, copyErr := io.Copy(targetFile, sourceFile)
		closeErr := targetFile.Close()
		_ = sourceFile.Close()
		if copyErr != nil || closeErr != nil {
			_ = os.Remove(targetPath)
			if reservedFileBytes > 0 {
				_ = a.applyDomainStorageDelta(r.Context(), siteDomain, 0, 0, 0, -reservedFileBytes, 0)
			}
			continue
		}
		if reservedFileBytes <= 0 {
			if storageErr := a.applyDomainStorageDelta(r.Context(), siteDomain, 0, 0, 0, writtenBytes, 0); storageErr != nil {
				_ = os.Remove(targetPath)
				http.Error(w, storageErr.Error(), http.StatusInsufficientStorage)
				return
			}
		}
		if reservedFileBytes > 0 && writtenBytes != reservedFileBytes {
			if storageErr := a.applyDomainStorageDelta(r.Context(), siteDomain, 0, 0, 0, writtenBytes-reservedFileBytes, 0); storageErr != nil {
				_ = os.Remove(targetPath)
				_ = a.applyDomainStorageDelta(r.Context(), siteDomain, 0, 0, 0, -reservedFileBytes, 0)
				http.Error(w, storageErr.Error(), http.StatusInsufficientStorage)
				return
			}
		}

		mimeType := strings.TrimSpace(fileHeader.Header.Get("Content-Type"))
		if mimeType == "" {
			mimeType = mime.TypeByExtension(path.Ext(storedName))
		}
		a.upsertFileMetadata(r.Context(), domain, storedName, currentPath, writtenBytes, mimeType, "upload")
		uploadedNames = append(uploadedNames, storedName)
	}

	if len(uploadedNames) > 0 {
		a.rebuildDomainStorageUsage(r.Context(), siteDomain)
	}
	if wantsJSONResponse(r) {
		uploadedPaths := make([]string, 0, len(uploadedNames))
		for _, uploadedName := range uploadedNames {
			uploadedPaths = append(uploadedPaths, "/p/"+uploadedName)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"uploaded": len(uploadedNames), "files": uploadedNames, "paths": uploadedPaths, "redirect": currentPath + "?files"})
		return
	}
	http.Redirect(w, r, currentPath+"?files", http.StatusFound)
}

func uniqueUploadedFileName(baseDir, requestedName string) string {
	candidate := requestedName
	extension := path.Ext(requestedName)
	stem := strings.TrimSuffix(requestedName, extension)
	for index := 1; ; index++ {
		if _, err := os.Stat(filepath.Join(baseDir, candidate)); os.IsNotExist(err) {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d%s", stem, index, extension)
	}
}

func currentFilesPath(r *http.Request) string {
	currentPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if currentPath == "" {
		currentPath = r.URL.Path
	}
	return cleanPath(currentPath)
}

func (a *App) fileMetadataByName(ctx context.Context, domain string) map[string]fileMetadata {
	metadataByName := make(map[string]fileMetadata)
	if a == nil || a.db == nil {
		return metadataByName
	}
	rows, err := a.db.QueryContext(ctx, `SELECT file_name,page_path,size,mime_type,created_at,download_count FROM file_metadata WHERE domain=?`, domain)
	if err != nil {
		return metadataByName
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var meta fileMetadata
		if scanErr := rows.Scan(&name, &meta.PagePath, &meta.Size, &meta.MimeType, &meta.CreatedAt, &meta.DownloadCount); scanErr != nil {
			continue
		}
		if safeRelativeAssetPath(name) == "" {
			continue
		}
		meta.PagePath = cleanPath(meta.PagePath)
		metadataByName[name] = meta
	}
	return metadataByName
}

func (a *App) upsertFileMetadata(ctx context.Context, domain, fileName, pagePath string, size int64, mimeType, source string) {
	if a == nil || a.db == nil {
		return
	}
	if safeRelativeAssetPath(fileName) == "" {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = a.db.ExecContext(ctx, `INSERT INTO file_metadata(domain,file_name,page_path,size,mime_type,created_at,updated_at,source,download_count)
VALUES(?,?,?,?,?,?,?,?,0)
ON CONFLICT(domain,file_name) DO UPDATE SET page_path=excluded.page_path,size=excluded.size,mime_type=excluded.mime_type,updated_at=excluded.updated_at,source=excluded.source`,
		domain, fileName, cleanPath(pagePath), size, mimeType, now, now, source)
}

func filePathBelongsToBranch(filePagePath, currentPath string) bool {
	filePagePath = cleanPath(filePagePath)
	currentPath = cleanPath(currentPath)
	if currentPath == "/" {
		return true
	}
	return filePagePath == currentPath || strings.HasPrefix(filePagePath, currentPath+"/")
}

func isImageFile(fileName, mimeType string) bool {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(mimeType)), "image/") {
		return true
	}
	switch strings.ToLower(path.Ext(fileName)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".ico", ".bmp":
		return true
	default:
		return false
	}
}

func formatFileSize(size int64) string {
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

type siteQuotaDatabaseCandidate struct {
	path           string
	fallbackDomain string
}

type siteQuotaRow struct {
	Domain       string
	Aliases      []string
	UsedBytes    int64
	LimitBytes   int64
	FilesPath    string
	DatabasePath string
}

func siteDatabaseRootPath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "sites")
}

func parseSiteQuotaLimitBytes(rawQuota string) (int64, bool, error) {
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
		return 0, false, fmt.Errorf("quota must use mb or gb suffix, for example -quota 50mb or -quota 20gb")
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

func runSiteQuotaCommand(ctx context.Context, output io.Writer, input io.Reader, storagePath, dbPath string, listSites bool, quotaSite string, quota string) error {
	limitBytes, quotaRequested, err := parseSiteQuotaLimitBytes(quota)
	if err != nil {
		return err
	}
	if quotaRequested && strings.TrimSpace(quotaSite) == "" {
		return fmt.Errorf("-quota-site is required when changing quota")
	}
	if strings.TrimSpace(quotaSite) != "" && !quotaRequested {
		return fmt.Errorf("quota value is required: use -quota 50mb or -quota 20gb")
	}

	if quotaRequested {
		updatedRow, err := updateSiteQuotaLimit(ctx, storagePath, dbPath, quotaSite, limitBytes)
		if err != nil {
			return err
		}
		fmt.Fprintf(output, "Updated quota for %s: %s used / %s limit\n", updatedRow.Domain, formatFileSize(updatedRow.UsedBytes), formatFileSize(updatedRow.LimitBytes))
	}
	if listSites {
		rows, err := listSiteQuotaRows(ctx, storagePath, dbPath)
		if err != nil {
			return err
		}
		if err := runSiteQuotaInteractiveConsole(ctx, output, input, storagePath, dbPath, rows); err != nil {
			return err
		}
	}
	return nil
}

func listSiteQuotaRows(ctx context.Context, storagePath, dbPath string) ([]siteQuotaRow, error) {
	candidates, err := siteQuotaDatabaseCandidates(dbPath)
	if err != nil {
		return nil, err
	}
	rows := make([]siteQuotaRow, 0, len(candidates))
	for _, candidate := range candidates {
		candidateRows, err := siteQuotaRowsFromDatabase(ctx, storagePath, candidate)
		if err != nil {
			return nil, err
		}
		rows = append(rows, candidateRows...)
	}
	rows = mergeSiteQuotaRows(rows, dbPath)
	sort.Slice(rows, func(left, right int) bool {
		if rows[left].Domain == rows[right].Domain {
			return rows[left].DatabasePath < rows[right].DatabasePath
		}
		return rows[left].Domain < rows[right].Domain
	})
	return rows, nil
}

func siteQuotaDatabaseCandidates(dbPath string) ([]siteQuotaDatabaseCandidate, error) {
	candidates := make([]siteQuotaDatabaseCandidate, 0, 8)
	seenPaths := make(map[string]struct{})
	addCandidate := func(candidatePath string, fallbackDomain string) {
		cleanPath := filepath.Clean(candidatePath)
		if _, err := os.Stat(cleanPath); err != nil {
			return
		}
		absolutePath, err := filepath.Abs(cleanPath)
		if err != nil {
			absolutePath = cleanPath
		}
		if _, seen := seenPaths[absolutePath]; seen {
			return
		}
		seenPaths[absolutePath] = struct{}{}
		candidates = append(candidates, siteQuotaDatabaseCandidate{path: cleanPath, fallbackDomain: fallbackDomain})
	}

	addCandidate(dbPath, "")
	siteDatabaseDir := siteDatabaseRootPath(dbPath)
	databaseFiles, err := os.ReadDir(siteDatabaseDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return candidates, nil
		}
		return nil, err
	}
	for _, databaseFile := range databaseFiles {
		if databaseFile.IsDir() || strings.ToLower(filepath.Ext(databaseFile.Name())) != ".db" {
			continue
		}
		fallbackDomain := strings.TrimSuffix(databaseFile.Name(), filepath.Ext(databaseFile.Name()))
		addCandidate(filepath.Join(siteDatabaseDir, databaseFile.Name()), fallbackDomain)
	}
	return candidates, nil
}

func siteQuotaRowsFromDatabase(ctx context.Context, storagePath string, candidate siteQuotaDatabaseCandidate) ([]siteQuotaRow, error) {
	rawDatabase, err := sql.Open("sqlite", "file:"+candidate.path)
	if err != nil {
		return nil, err
	}
	defer rawDatabase.Close()

	application := &App{db: rawDatabase, storagePath: storagePath}
	migrateDomain := candidate.fallbackDomain
	if strings.TrimSpace(migrateDomain) == "" {
		migrateDomain = "localhost"
	}
	if err := application.migrate(contextWithDomain(ctx, migrateDomain)); err != nil {
		return nil, err
	}
	domains := siteDomainsInDatabase(ctx, rawDatabase)

	rows := make([]siteQuotaRow, 0, len(domains))
	for _, domain := range domains {
		usage := application.domainStorageUsage(contextWithDomain(ctx, domain), domain)
		rows = append(rows, siteQuotaRow{
			Domain:       domain,
			Aliases:      siteAliasesInDatabase(ctx, rawDatabase, domain),
			UsedBytes:    usage.totalBytes(),
			LimitBytes:   usage.LimitBytes,
			FilesPath:    application.domainFilesDirForDomain(domain),
			DatabasePath: candidate.path,
		})
	}
	return rows, nil
}

func mergeSiteQuotaRows(rows []siteQuotaRow, dbPath string) []siteQuotaRow {
	primaryDomainByAlias := make(map[string]string)
	for _, row := range rows {
		for _, aliasDomain := range row.Aliases {
			aliasDomain = normalizeQuotaDomainName(aliasDomain)
			if aliasDomain != "" && aliasDomain != row.Domain {
				primaryDomainByAlias[aliasDomain] = row.Domain
			}
		}
	}

	rowsByDomain := make(map[string]siteQuotaRow)
	for _, row := range rows {
		domain := normalizeQuotaDomainName(row.Domain)
		if domain == "" {
			continue
		}
		primaryDomain := domain
		if aliasPrimaryDomain := primaryDomainByAlias[domain]; aliasPrimaryDomain != "" {
			primaryDomain = aliasPrimaryDomain
		}

		currentRow, rowExists := rowsByDomain[primaryDomain]
		nextAliases := mergeSiteQuotaAliases(primaryDomain, currentRow.Aliases, row.Aliases)
		if domain != primaryDomain {
			nextAliases = mergeSiteQuotaAliases(primaryDomain, nextAliases, []string{domain})
		}
		if !rowExists || siteQuotaRowPreferred(row, currentRow, primaryDomain, dbPath) {
			row.Domain = primaryDomain
			row.Aliases = nextAliases
			rowsByDomain[primaryDomain] = row
			continue
		}
		currentRow.Aliases = nextAliases
		rowsByDomain[primaryDomain] = currentRow
	}

	mergedRows := make([]siteQuotaRow, 0, len(rowsByDomain))
	for _, row := range rowsByDomain {
		sort.Strings(row.Aliases)
		mergedRows = append(mergedRows, row)
	}
	return mergedRows
}

func mergeSiteQuotaAliases(primaryDomain string, aliasGroups ...[]string) []string {
	aliasSet := make(map[string]struct{})
	for _, aliasGroup := range aliasGroups {
		for _, aliasDomain := range aliasGroup {
			aliasDomain = normalizeQuotaDomainName(aliasDomain)
			if aliasDomain == "" || aliasDomain == primaryDomain {
				continue
			}
			aliasSet[aliasDomain] = struct{}{}
		}
	}
	aliases := make([]string, 0, len(aliasSet))
	for aliasDomain := range aliasSet {
		aliases = append(aliases, aliasDomain)
	}
	sort.Strings(aliases)
	return aliases
}

func siteQuotaRowPreferred(nextRow, currentRow siteQuotaRow, primaryDomain, dbPath string) bool {
	nextPriority := siteQuotaRowPriority(nextRow, primaryDomain, dbPath)
	currentPriority := siteQuotaRowPriority(currentRow, primaryDomain, dbPath)
	if nextPriority != currentPriority {
		return nextPriority > currentPriority
	}
	if nextRow.UsedBytes != currentRow.UsedBytes {
		return nextRow.UsedBytes > currentRow.UsedBytes
	}
	if nextRow.DatabasePath != currentRow.DatabasePath {
		return nextRow.DatabasePath < currentRow.DatabasePath
	}
	return false
}

func siteQuotaRowPriority(row siteQuotaRow, primaryDomain, dbPath string) int {
	priority := 0
	if row.Domain == primaryDomain {
		priority += 4
	}
	expectedSiteDatabasePath := filepath.Join(siteDatabaseRootPath(dbPath), domainStorageName(primaryDomain)+".db")
	if sameSiteQuotaPath(row.DatabasePath, expectedSiteDatabasePath) {
		priority += 8
	}
	if sameSiteQuotaPath(row.DatabasePath, dbPath) {
		priority--
	}
	return priority
}

func sameSiteQuotaPath(leftPath, rightPath string) bool {
	leftCleanPath := filepath.Clean(leftPath)
	rightCleanPath := filepath.Clean(rightPath)
	leftAbsolutePath, leftErr := filepath.Abs(leftCleanPath)
	rightAbsolutePath, rightErr := filepath.Abs(rightCleanPath)
	if leftErr == nil && rightErr == nil {
		return leftAbsolutePath == rightAbsolutePath
	}
	return leftCleanPath == rightCleanPath
}

func siteDomainsInDatabase(ctx context.Context, database *sql.DB) []string {
	domainSet := make(map[string]struct{})
	rows, err := database.QueryContext(ctx, `SELECT DISTINCT domain FROM users WHERE is_admin=1 AND TRIM(COALESCE(email,''))<>'' AND TRIM(COALESCE(password,''))<>''`)
	if err == nil {
		for rows.Next() {
			var domain string
			if scanErr := rows.Scan(&domain); scanErr != nil {
				continue
			}
			domain = normalizeQuotaDomainName(domain)
			if domain != "" {
				domainSet[domain] = struct{}{}
			}
		}
		_ = rows.Close()
	}
	domains := make([]string, 0, len(domainSet))
	for domain := range domainSet {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	return domains
}

func siteAliasesInDatabase(ctx context.Context, database *sql.DB, domain string) []string {
	rows, err := database.QueryContext(ctx, `SELECT alias_domain FROM domain_aliases WHERE primary_domain=? ORDER BY alias_domain`, domain)
	if err != nil {
		return nil
	}
	defer rows.Close()
	aliases := make([]string, 0, 4)
	for rows.Next() {
		var aliasDomain string
		if scanErr := rows.Scan(&aliasDomain); scanErr == nil && strings.TrimSpace(aliasDomain) != "" {
			aliases = append(aliases, aliasDomain)
		}
	}
	return aliases
}

func updateSiteQuotaLimit(ctx context.Context, storagePath, dbPath, rawDomain string, limitBytes int64) (siteQuotaRow, error) {
	domain := normalizeQuotaDomainName(rawDomain)
	if domain == "" {
		return siteQuotaRow{}, fmt.Errorf("invalid site domain %q", rawDomain)
	}
	candidate, err := siteQuotaDatabaseCandidateForDomain(ctx, storagePath, dbPath, domain)
	if err != nil {
		return siteQuotaRow{}, err
	}

	rawDatabase, err := sql.Open("sqlite", "file:"+candidate.path)
	if err != nil {
		return siteQuotaRow{}, err
	}
	defer rawDatabase.Close()

	application := &App{db: rawDatabase, storagePath: storagePath}
	commandContext := contextWithDomain(ctx, domain)
	if err := application.migrate(commandContext); err != nil {
		return siteQuotaRow{}, err
	}
	application.ensureDomainStorageUsageRow(commandContext, domain)
	_, err = rawDatabase.ExecContext(commandContext, `UPDATE domain_storage_usage SET limit_bytes=?, updated_at=? WHERE domain=?`, limitBytes, time.Now().UTC().Format(time.RFC3339), domain)
	if err != nil {
		return siteQuotaRow{}, err
	}
	usage := application.domainStorageUsage(commandContext, domain)
	return siteQuotaRow{
		Domain:       domain,
		Aliases:      siteAliasesInDatabase(commandContext, rawDatabase, domain),
		UsedBytes:    usage.totalBytes(),
		LimitBytes:   usage.LimitBytes,
		FilesPath:    application.domainFilesDirForDomain(domain),
		DatabasePath: candidate.path,
	}, nil
}

func siteQuotaDatabaseCandidateForDomain(ctx context.Context, storagePath, dbPath, domain string) (siteQuotaDatabaseCandidate, error) {
	siteDatabasePath := filepath.Join(siteDatabaseRootPath(dbPath), domainStorageName(domain)+".db")
	if _, err := os.Stat(siteDatabasePath); err == nil {
		return siteQuotaDatabaseCandidate{path: siteDatabasePath, fallbackDomain: domain}, nil
	}
	candidates, err := siteQuotaDatabaseCandidates(dbPath)
	if err != nil {
		return siteQuotaDatabaseCandidate{}, err
	}
	for _, candidate := range candidates {
		rows, err := siteQuotaRowsFromDatabase(ctx, storagePath, candidate)
		if err != nil {
			return siteQuotaDatabaseCandidate{}, err
		}
		for _, row := range rows {
			if row.Domain == domain {
				return candidate, nil
			}
		}
	}
	return siteQuotaDatabaseCandidate{}, fmt.Errorf("site %q was not found under %s", domain, siteDatabaseRootPath(dbPath))
}

func normalizeQuotaDomainName(rawDomain string) string {
	cleanDomain := strings.ToLower(strings.TrimSpace(rawDomain))
	if cleanDomain == "localhost" {
		return cleanDomain
	}
	return normalizeDomainName(cleanDomain)
}

func runSiteQuotaInteractiveConsole(ctx context.Context, output io.Writer, input io.Reader, storagePath, dbPath string, rows []siteQuotaRow) error {
	reader := bufio.NewReader(input)
	selectedIndex := 0
	inputFile, inputIsFile := input.(*os.File)
	rawTerminal := inputIsFile && term.IsTerminal(int(inputFile.Fd()))
	layout := siteQuotaTerminalLayoutForWriter(output)
	var rawTerminalState *term.State
	if rawTerminal {
		terminalState, err := term.MakeRaw(int(inputFile.Fd()))
		if err != nil {
			return err
		}
		layout.newline = "\r\n"
		rawTerminalState = terminalState
		defer func() {
			_ = term.Restore(int(inputFile.Fd()), rawTerminalState)
		}()
	}
	for {
		printSiteQuotaRows(output, storagePath, dbPath, rows, selectedIndex, layout)
		if len(rows) == 0 {
			return nil
		}
		command, err := readSiteQuotaMenuCommand(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		switch command.Action {
		case "quit":
			return nil
		case "up":
			if selectedIndex > 0 {
				selectedIndex--
			}
			continue
		case "down":
			if selectedIndex < len(rows)-1 {
				selectedIndex++
			}
			continue
		case "number":
			if command.Index < 1 || command.Index > len(rows) {
				fmt.Fprintf(output, "%sInvalid selection.%s%s", terminalRed(), terminalReset(), layout.newline)
				continue
			}
			selectedIndex = command.Index - 1
		case "enter":
		default:
			fmt.Fprintf(output, "%sInvalid selection.%s%s", terminalRed(), terminalReset(), layout.newline)
			continue
		}
		selectedRow := rows[selectedIndex]
		if rawTerminal {
			_ = term.Restore(int(inputFile.Fd()), rawTerminalState)
		}
		fmt.Fprintf(output, "%s%s", layout.newline, siteQuotaQuotaPromptLine(layout, selectedRow))
		quotaAnswer, quitQuotaPrompt, err := readSiteQuotaQuotaAnswer(reader)
		if rawTerminal {
			terminalState, rawErr := term.MakeRaw(int(inputFile.Fd()))
			if rawErr != nil {
				return rawErr
			}
			rawTerminalState = terminalState
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if quitQuotaPrompt {
			return nil
		}
		limitBytes, quotaRequested, err := parseSiteQuotaLimitBytes(quotaAnswer)
		if err != nil || !quotaRequested {
			if err == nil {
				err = fmt.Errorf("quota value is required")
			}
			fmt.Fprintf(output, "%s%v%s%s", terminalRed(), err, terminalReset(), layout.newline)
			continue
		}
		updatedRow, err := updateSiteQuotaLimit(ctx, storagePath, dbPath, selectedRow.Domain, limitBytes)
		if err != nil {
			return err
		}
		fmt.Fprintf(output, "%sUpdated %s: %s used / %s limit%s%s", terminalGreen(), updatedRow.Domain, formatFileSize(updatedRow.UsedBytes), formatFileSize(updatedRow.LimitBytes), terminalReset(), layout.newline)
		rows, err = listSiteQuotaRows(ctx, storagePath, dbPath)
		if err != nil {
			return err
		}
		if selectedIndex >= len(rows) {
			selectedIndex = len(rows) - 1
		}
	}
}

type siteQuotaMenuCommand struct {
	Action string
	Index  int
}

func readSiteQuotaMenuCommand(reader *bufio.Reader) (siteQuotaMenuCommand, error) {
	for {
		nextByte, err := reader.ReadByte()
		if err != nil {
			return siteQuotaMenuCommand{}, err
		}
		switch {
		case nextByte == '\r' || nextByte == '\n':
			return siteQuotaMenuCommand{Action: "enter"}, nil
		case nextByte == 'q' || nextByte == 'Q':
			drainSiteQuotaInputLine(reader)
			return siteQuotaMenuCommand{Action: "quit"}, nil
		case nextByte >= '0' && nextByte <= '9':
			digitBytes := []byte{nextByte}
			for {
				peekBytes, err := reader.Peek(1)
				if err != nil || len(peekBytes) == 0 || peekBytes[0] < '0' || peekBytes[0] > '9' {
					break
				}
				nextDigit, _ := reader.ReadByte()
				digitBytes = append(digitBytes, nextDigit)
			}
			drainSiteQuotaInputLine(reader)
			selectedNumber, err := strconv.Atoi(string(digitBytes))
			if err != nil {
				return siteQuotaMenuCommand{Action: "invalid"}, nil
			}
			return siteQuotaMenuCommand{Action: "number", Index: selectedNumber}, nil
		case nextByte == 0x1b:
			secondByte, err := reader.ReadByte()
			if err != nil {
				return siteQuotaMenuCommand{}, err
			}
			thirdByte, err := reader.ReadByte()
			if err != nil {
				return siteQuotaMenuCommand{}, err
			}
			drainSiteQuotaInputLine(reader)
			if secondByte == '[' && thirdByte == 'A' {
				return siteQuotaMenuCommand{Action: "up"}, nil
			}
			if secondByte == '[' && thirdByte == 'B' {
				return siteQuotaMenuCommand{Action: "down"}, nil
			}
			return siteQuotaMenuCommand{Action: "invalid"}, nil
		}
	}
}

func readSiteQuotaQuotaAnswer(reader *bufio.Reader) (string, bool, error) {
	var quotaText strings.Builder
	for {
		nextByte, err := reader.ReadByte()
		if err != nil {
			return "", false, err
		}
		switch nextByte {
		case '\r', '\n':
			return strings.TrimSpace(quotaText.String()), false, nil
		case 'q', 'Q':
			if quotaText.Len() == 0 {
				drainSiteQuotaInputLine(reader)
				return "", true, nil
			}
			quotaText.WriteByte(nextByte)
		default:
			quotaText.WriteByte(nextByte)
		}
	}
}

func drainSiteQuotaInputLine(reader *bufio.Reader) {
	if reader.Buffered() == 0 {
		return
	}
	peekBytes, err := reader.Peek(1)
	if err != nil || len(peekBytes) == 0 {
		return
	}
	if peekBytes[0] != '\n' && peekBytes[0] != '\r' {
		return
	}
	firstLineByte, _ := reader.ReadByte()
	if firstLineByte != '\r' || reader.Buffered() == 0 {
		return
	}
	peekBytes, err = reader.Peek(1)
	if err == nil && len(peekBytes) > 0 && peekBytes[0] == '\n' {
		_, _ = reader.ReadByte()
	}
}

type siteQuotaTerminalLayout struct {
	width       int
	height      int
	colors      bool
	clearScreen bool
	newline     string
}

func siteQuotaTerminalLayoutForWriter(output io.Writer) siteQuotaTerminalLayout {
	layout := siteQuotaTerminalLayout{width: 120, height: 24, newline: "\n"}
	outputFile, ok := output.(*os.File)
	if !ok || !term.IsTerminal(int(outputFile.Fd())) {
		return layout
	}
	layout.colors = true
	layout.clearScreen = true
	width, height, err := term.GetSize(int(outputFile.Fd()))
	if err == nil && width > 0 {
		layout.width = width
	}
	if err == nil && height > 0 {
		layout.height = height
	}
	return layout
}

func printSiteQuotaRows(output io.Writer, storagePath, dbPath string, rows []siteQuotaRow, selectedIndex int, layout siteQuotaTerminalLayout) {
	if layout.newline == "" {
		layout.newline = "\n"
	}
	if len(rows) == 0 {
		fmt.Fprintf(output, "No sites found in %s (database root: %s)\n", storagePath, siteDatabaseRootPath(dbPath))
		return
	}
	if layout.clearScreen {
		fmt.Fprint(output, "\033[2J\033[H")
	}
	innerWidth := siteQuotaInnerWidth(layout.width)
	visibleRows := layout.height - 4
	if visibleRows < 0 {
		visibleRows = 0
	}
	if visibleRows > len(rows) {
		visibleRows = len(rows)
	}
	if visibleRows == 0 {
		siteQuotaPrintLine(output, layout, siteQuotaBoxBorderLine(innerWidth))
		siteQuotaPrintLine(output, layout, siteQuotaBoxContentLineColored(layout, innerWidth, terminalBold(), siteQuotaHeaderLine(len(rows), selectedIndex, innerWidth)))
		siteQuotaPrintLine(output, layout, siteQuotaBoxContentLine(innerWidth, ""))
		siteQuotaPrintLine(output, layout, siteQuotaBoxContentLineColored(layout, innerWidth, terminalCyan(), truncateDisplayText("window too small", innerWidth)))
		siteQuotaPrintLine(output, layout, siteQuotaBoxBorderLine(innerWidth))
		return
	}
	startIndex := 0
	if len(rows) > visibleRows {
		startIndex = selectedIndex - visibleRows/2
		if startIndex < 0 {
			startIndex = 0
		}
		if startIndex+visibleRows > len(rows) {
			startIndex = len(rows) - visibleRows
		}
	}
	endIndex := startIndex + visibleRows
	siteQuotaPrintLine(output, layout, siteQuotaBoxBorderLine(innerWidth))
	siteQuotaPrintLine(output, layout, siteQuotaBoxContentLineColored(layout, innerWidth, terminalBold(), siteQuotaHeaderLine(len(rows), selectedIndex, innerWidth)))
	for rowIndex := startIndex; rowIndex < endIndex; rowIndex++ {
		rowLine, rowColor := siteQuotaRowLine(rows[rowIndex], rowIndex+1, rowIndex == selectedIndex, innerWidth)
		siteQuotaPrintLine(output, layout, siteQuotaBoxContentLineColored(layout, innerWidth, rowColor, rowLine))
	}
	if endIndex < len(rows) {
		hiddenRowsText := fmt.Sprintf("... %d more", len(rows)-endIndex)
		siteQuotaPrintLine(output, layout, siteQuotaBoxContentLineColored(layout, innerWidth, terminalCyan(), truncateDisplayText(hiddenRowsText, innerWidth)))
	}
	siteQuotaPrintLine(output, layout, siteQuotaBoxContentLineColored(layout, innerWidth, terminalCyan(), siteQuotaMenuHelpLine(innerWidth)))
	siteQuotaPrintLine(output, layout, siteQuotaBoxBorderLine(innerWidth))
}

func siteQuotaHeaderLine(totalRows, selectedIndex, width int) string {
	headerText := fmt.Sprintf("sites %d/%d  q quit  up/down move  enter edit", selectedIndex+1, totalRows)
	if width < 48 {
		headerText = fmt.Sprintf("%d/%d q quit up/down enter", selectedIndex+1, totalRows)
	}
	return truncateDisplayText(headerText, width)
}

func siteQuotaMenuHelpLine(width int) string {
	return truncateDisplayText("q quit  up/down move  enter edit", width)
}

func siteQuotaQuotaPromptLine(layout siteQuotaTerminalLayout, row siteQuotaRow) string {
	prompt := fmt.Sprintf("quota for %s [%s]: ", row.Domain, quotaRecommendation(row))
	return siteQuotaApplyColor(layout, terminalCyan(), truncateDisplayText(prompt, layout.width))
}

func siteQuotaRowLine(row siteQuotaRow, index int, selected bool, width int) (string, string) {
	usageText := compactQuotaUsageText(row)
	stateText, stateColor := siteQuotaQuotaState(row)
	lineText := fmt.Sprintf("[%d] %s | %s | %s | aliases:%d", index, row.Domain, usageText, stateText, len(row.Aliases))
	lineText = truncateDisplayText(lineText, width)
	if selected {
		return lineText, terminalCyan() + terminalBold()
	}
	return lineText, stateColor
}

func compactQuotaUsageText(row siteQuotaRow) string {
	usedText := formatCompactFileSize(row.UsedBytes)
	limitText := formatCompactFileSize(row.LimitBytes)
	if row.LimitBytes <= 0 {
		return fmt.Sprintf("used %s  quota:none", usedText)
	}
	freeBytes := row.LimitBytes - row.UsedBytes
	if freeBytes <= 0 {
		return fmt.Sprintf("used %s/%s  quota:full", usedText, limitText)
	}
	return fmt.Sprintf("used %s/%s  free:%s", usedText, limitText, compactPercentText(freeBytes, row.LimitBytes))
}

func siteQuotaQuotaState(row siteQuotaRow) (string, string) {
	if row.LimitBytes <= 0 {
		return "quota:none", terminalYellow()
	}
	freeBytes := row.LimitBytes - row.UsedBytes
	if freeBytes <= 0 {
		return "quota:full", terminalRed()
	}
	freePercent := float64(freeBytes) * 100 / float64(row.LimitBytes)
	if freePercent < 25 {
		return "quota:low", terminalYellow()
	}
	return "quota:ok", terminalGreen()
}

func siteQuotaApplyColor(layout siteQuotaTerminalLayout, color string, text string) string {
	if !layout.colors || color == "" {
		return text
	}
	return color + text + terminalReset()
}

func siteQuotaInnerWidth(width int) int {
	if width <= 4 {
		return 1
	}
	return width - 4
}

func siteQuotaBoxBorderLine(innerWidth int) string {
	return "+" + strings.Repeat("-", innerWidth) + "+"
}

func siteQuotaBoxContentLine(innerWidth int, text string) string {
	return "|" + padAndTruncateDisplayText(text, innerWidth) + "|"
}

func siteQuotaBoxContentLineColored(layout siteQuotaTerminalLayout, innerWidth int, color string, text string) string {
	paddedText := padAndTruncateDisplayText(text, innerWidth)
	if layout.colors && color != "" {
		paddedText = color + paddedText + terminalReset()
	}
	return "|" + paddedText + "|"
}

func siteQuotaPrintLine(output io.Writer, layout siteQuotaTerminalLayout, text string) {
	fmt.Fprint(output, text, layout.newline)
}

func padAndTruncateDisplayText(text string, width int) string {
	trimmedText := truncateDisplayText(text, width)
	if width <= 0 {
		return ""
	}
	if len(trimmedText) >= width {
		return trimmedText
	}
	return trimmedText + strings.Repeat(" ", width-len(trimmedText))
}

func compactPercentText(freeBytes, limitBytes int64) string {
	if limitBytes <= 0 {
		return "0%"
	}
	percent := int((float64(freeBytes) * 100) / float64(limitBytes))
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return fmt.Sprintf("%d%%", percent)
}

func compactDisplayText(text string, width int) string {
	trimmedText := strings.TrimSpace(text)
	if width <= 0 || len(trimmedText) <= width {
		return trimmedText
	}
	if width <= 3 {
		return trimmedText[:width]
	}
	return trimmedText[:width-3] + "..."
}

func compactPathText(text string, width int) string {
	trimmedText := strings.TrimSpace(text)
	if width <= 0 || len(trimmedText) <= width {
		return trimmedText
	}
	if width <= 3 {
		return trimmedText[len(trimmedText)-width:]
	}
	return "..." + trimmedText[len(trimmedText)-(width-3):]
}

func truncateDisplayText(text string, width int) string {
	trimmedText := strings.TrimSpace(text)
	if width <= 0 || len(trimmedText) <= width {
		return trimmedText
	}
	if width <= 3 {
		return trimmedText[:width]
	}
	return trimmedText[:width-3] + "..."
}

func formatCompactFileSize(size int64) string {
	const kilobyte = int64(1024)
	const megabyte = int64(1024 * kilobyte)
	const gigabyte = int64(1024 * megabyte)
	switch {
	case size >= gigabyte:
		if size%gigabyte == 0 {
			return fmt.Sprintf("%dG", size/gigabyte)
		}
		return fmt.Sprintf("%.1fG", float64(size)/float64(gigabyte))
	case size >= megabyte:
		if size%megabyte == 0 {
			return fmt.Sprintf("%dM", size/megabyte)
		}
		return fmt.Sprintf("%.1fM", float64(size)/float64(megabyte))
	case size >= kilobyte:
		if size%kilobyte == 0 {
			return fmt.Sprintf("%dK", size/kilobyte)
		}
		return fmt.Sprintf("%.1fK", float64(size)/float64(kilobyte))
	default:
		return fmt.Sprintf("%dB", size)
	}
}

func quotaRecommendation(row siteQuotaRow) string {
	recommendedBytes := recommendedQuotaBytes(row.UsedBytes)
	if row.UsedBytes >= row.LimitBytes {
		return fmt.Sprintf("quota is exhausted; set at least %s", formatQuotaForInput(recommendedBytes))
	}
	return fmt.Sprintf("suggested %s", formatQuotaForInput(recommendedBytes))
}

func recommendedQuotaBytes(usedBytes int64) int64 {
	const gigabyte = int64(1024 * 1024 * 1024)
	const megabyte = int64(1024 * 1024)
	minimumBytes := usedBytes + usedBytes/5
	if minimumBytes < 512*megabyte {
		minimumBytes = 512 * megabyte
	}
	if minimumBytes%gigabyte == 0 {
		return minimumBytes
	}
	if minimumBytes >= gigabyte {
		return ((minimumBytes / gigabyte) + 1) * gigabyte
	}
	return ((minimumBytes / megabyte) + 1) * megabyte
}

func formatQuotaForInput(sizeBytes int64) string {
	const gigabyte = int64(1024 * 1024 * 1024)
	const megabyte = int64(1024 * 1024)
	if sizeBytes >= gigabyte && sizeBytes%gigabyte == 0 {
		return fmt.Sprintf("%dgb", sizeBytes/gigabyte)
	}
	return fmt.Sprintf("%dmb", sizeBytes/megabyte)
}

func terminalBold() string {
	return "\033[1m"
}

func terminalGreen() string {
	return "\033[32m"
}

func terminalCyan() string {
	return "\033[36m"
}

func terminalRed() string {
	return "\033[31m"
}

func terminalYellow() string {
	return "\033[33m"
}

func terminalReset() string {
	return "\033[0m"
}

func (usage domainStorageUsage) totalBytes() int64 {
	return usage.PageBytes + usage.PublishedPageBytes + usage.RevisionBytes + usage.FileBytes + usage.PublishedStaticBytes
}

func (a *App) domainStorageUsage(ctx context.Context, domain string) domainStorageUsage {
	a.rebuildDomainStorageUsage(ctx, domain)
	a.ensureDomainStorageUsageRow(ctx, domain)
	usage := domainStorageUsage{LimitBytes: defaultDomainStorageLimitBytes}
	_ = a.db.QueryRowContext(ctx, `SELECT page_bytes,published_page_bytes,revision_bytes,file_bytes,published_static_bytes,limit_bytes FROM domain_storage_usage WHERE domain=?`, domain).Scan(&usage.PageBytes, &usage.PublishedPageBytes, &usage.RevisionBytes, &usage.FileBytes, &usage.PublishedStaticBytes, &usage.LimitBytes)
	if usage.LimitBytes <= 0 {
		usage.LimitBytes = defaultDomainStorageLimitBytes
	}
	return usage
}

func (a *App) ensureDomainStorageUsageRow(ctx context.Context, domain string) {
	if a == nil || a.db == nil || strings.TrimSpace(domain) == "" {
		return
	}
	_, _ = a.db.ExecContext(ctx, `INSERT OR IGNORE INTO domain_storage_usage(domain,limit_bytes,updated_at) VALUES(?,?,?)`, domain, defaultDomainStorageLimitBytes, time.Now().UTC().Format(time.RFC3339))
}

func (a *App) applyDomainStorageDelta(ctx context.Context, domain string, pageDelta, publishedPageDelta, revisionDelta, fileDelta, publishedStaticDelta int64) error {
	a.ensureDomainStorageUsageRow(ctx, domain)
	totalDelta := pageDelta + publishedPageDelta + revisionDelta + fileDelta + publishedStaticDelta
	if totalDelta > 0 {
		a.rebuildDomainStorageUsage(ctx, domain)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	updateUsage := func() (int64, error) {
		result, err := a.db.ExecContext(ctx, `UPDATE domain_storage_usage
SET page_bytes=page_bytes+?,
    published_page_bytes=published_page_bytes+?,
    revision_bytes=revision_bytes+?,
    file_bytes=file_bytes+?,
    published_static_bytes=published_static_bytes+?,
    updated_at=?
WHERE domain=?
  AND page_bytes+?>=0
  AND published_page_bytes+?>=0
  AND revision_bytes+?>=0
  AND file_bytes+?>=0
  AND published_static_bytes+?>=0
  AND page_bytes+published_page_bytes+revision_bytes+file_bytes+published_static_bytes+?<=limit_bytes`,
			pageDelta, publishedPageDelta, revisionDelta, fileDelta, publishedStaticDelta, now, domain,
			pageDelta, publishedPageDelta, revisionDelta, fileDelta, publishedStaticDelta, totalDelta)
		if err != nil {
			return 0, err
		}
		return result.RowsAffected()
	}
	updatedRowsCount, err := updateUsage()
	if err != nil {
		return err
	}
	if updatedRowsCount == 0 {
		a.rebuildDomainStorageUsage(ctx, domain)
		updatedRowsCount, err = updateUsage()
		if err != nil {
			return err
		}
	}
	if updatedRowsCount == 0 {
		usage := a.domainStorageUsage(ctx, domain)
		return fmt.Errorf("storage limit reached: %s / %s used", formatFileSize(usage.totalBytes()), formatFileSize(usage.LimitBytes))
	}
	return nil
}

func (a *App) rebuildAllDomainStorageUsage(ctx context.Context) {
	domainSet := make(map[string]struct{})
	for _, query := range []string{
		`SELECT DISTINCT domain FROM users`,
		`SELECT DISTINCT domain FROM pages`,
		`SELECT DISTINCT domain FROM published_pages`,
		`SELECT DISTINCT domain FROM revisions`,
		`SELECT DISTINCT domain FROM domain_states`,
		`SELECT DISTINCT primary_domain FROM domain_aliases`,
		`SELECT DISTINCT domain FROM domain_chroot_locations`,
		`SELECT DISTINCT domain FROM domain_storage_usage`,
	} {
		rows, err := a.db.QueryContext(ctx, query)
		if err != nil {
			continue
		}
		for rows.Next() {
			var domain string
			if scanErr := rows.Scan(&domain); scanErr == nil && strings.TrimSpace(domain) != "" {
				domainSet[domain] = struct{}{}
			}
		}
		_ = rows.Close()
	}
	for domain := range domainSet {
		a.rebuildDomainStorageUsage(ctx, domain)
	}
}

func (a *App) rebuildDomainStorageUsage(ctx context.Context, domain string) {
	if strings.TrimSpace(domain) == "" {
		return
	}
	pageBytes := a.sumHTMLColumnBytes(ctx, `SELECT html FROM pages WHERE domain=?`, domain)
	publishedPageBytes := a.sumHTMLColumnBytes(ctx, `SELECT html FROM published_pages WHERE domain=?`, domain)
	revisionBytes := a.sumHTMLColumnBytes(ctx, `SELECT html FROM revisions WHERE domain=?`, domain)
	fileBytes := diskusage.DirectorySize(a.domainFilesDirForDomain(domain)) + diskusage.DirectorySize(a.domainChrootRootDir(domain))
	publishedStaticBytes := diskusage.DirectorySize(a.domainStaticDir(domain))
	a.ensureDomainStorageUsageRow(ctx, domain)
	_, _ = a.db.ExecContext(ctx, `UPDATE domain_storage_usage SET page_bytes=?, published_page_bytes=?, revision_bytes=?, file_bytes=?, published_static_bytes=?, updated_at=?, limit_bytes=COALESCE(NULLIF(limit_bytes,0),?) WHERE domain=?`,
		pageBytes, publishedPageBytes, revisionBytes, fileBytes, publishedStaticBytes, time.Now().UTC().Format(time.RFC3339), defaultDomainStorageLimitBytes, domain)
}

func (a *App) sumHTMLColumnBytes(ctx context.Context, query string, domain string) int64 {
	rows, err := a.db.QueryContext(ctx, query, domain)
	if err != nil {
		return 0
	}
	defer rows.Close()
	var totalBytes int64
	for rows.Next() {
		var htmlText string
		if scanErr := rows.Scan(&htmlText); scanErr != nil {
			continue
		}
		totalBytes += int64(len([]byte(htmlText)))
	}
	return totalBytes
}

func fileSizeBytes(filePath string) int64 {
	return diskusage.FileSize(filePath)
}

func fileExtensionLabel(fileName string) string {
	extension := strings.TrimPrefix(strings.ToUpper(path.Ext(fileName)), ".")
	if extension == "" {
		return "FILE"
	}
	if len(extension) > 6 {
		return extension[:6]
	}
	return extension
}

func randomAccessToken() string {
	var tokenBytes [18]byte
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		return fmt.Sprintf("%x", sha256.Sum256([]byte(time.Now().String())))[:24]
	}
	return base64.RawURLEncoding.EncodeToString(tokenBytes[:])
}

func (a *App) fileAccessRulesByName(ctx context.Context, r *http.Request) map[string]ManagedFileAccess {
	accessRulesByName := make(map[string]ManagedFileAccess)
	rows, err := a.db.QueryContext(ctx, `SELECT file_name,access_mode,token,expires_at,single_use_left,token_use_count FROM file_access_rules WHERE domain=?`, domainStorageName(a.siteDomain(ctx, r)))
	if err != nil {
		return accessRulesByName
	}
	defer rows.Close()
	for rows.Next() {
		var fileName string
		var accessRule ManagedFileAccess
		if scanErr := rows.Scan(&fileName, &accessRule.AccessMode, &accessRule.Token, &accessRule.ExpiresAt, &accessRule.SingleUseLeft, &accessRule.TokenUseCount); scanErr != nil {
			continue
		}
		if safeRelativeAssetPath(fileName) == "" {
			continue
		}
		if strings.TrimSpace(accessRule.AccessMode) == "" {
			accessRule.AccessMode = "public"
		}
		accessRulesByName[fileName] = accessRule
	}
	return accessRulesByName
}

func (a *App) fileAccessRule(ctx context.Context, r *http.Request, fileName string) ManagedFileAccess {
	rule := ManagedFileAccess{AccessMode: "public"}
	_ = a.db.QueryRowContext(ctx, `SELECT access_mode,token,expires_at,single_use_left,token_use_count FROM file_access_rules WHERE domain=? AND file_name=?`, domainStorageName(a.siteDomain(ctx, r)), fileName).Scan(&rule.AccessMode, &rule.Token, &rule.ExpiresAt, &rule.SingleUseLeft, &rule.TokenUseCount)
	if strings.TrimSpace(rule.AccessMode) == "" {
		rule.AccessMode = "public"
	}
	return rule
}

func (a *App) saveFileAccessRule(ctx context.Context, r *http.Request, fileName string) {
	accessMode := r.FormValue("access_mode")
	if accessMode != "token" && accessMode != "timer" {
		accessMode = "public"
	}
	token := strings.TrimSpace(r.FormValue("access_token"))
	if accessMode == "token" && token == "" {
		token = randomAccessToken()
	}
	accessDaysValue := strings.TrimSpace(r.FormValue("access_days"))
	days, _ := strconv.Atoi(accessDaysValue)
	if days < 0 {
		days = 0
	}
	singleUseLeft, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("single_use_left")))
	if singleUseLeft < 0 {
		singleUseLeft = 0
	}

	expiresAt := ""
	if accessMode == "public" {
		token = ""
		singleUseLeft = 0
	} else if accessMode == "timer" {
		token = ""
		singleUseLeft = 0
		if accessDaysValue == "" {
			_ = a.db.QueryRowContext(ctx, `SELECT expires_at FROM file_access_rules WHERE domain=? AND file_name=?`, domainStorageName(a.siteDomain(ctx, r)), fileName).Scan(&expiresAt)
		} else if days > 0 {
			expiresAt = time.Now().Add(time.Duration(days) * 24 * time.Hour).UTC().Format(time.RFC3339)
		}
	} else if accessMode == "token" && accessDaysValue == "" {
		_ = a.db.QueryRowContext(ctx, `SELECT expires_at FROM file_access_rules WHERE domain=? AND file_name=?`, domainStorageName(a.siteDomain(ctx, r)), fileName).Scan(&expiresAt)
	} else if accessMode == "token" && days > 0 {
		expiresAt = time.Now().Add(time.Duration(days) * 24 * time.Hour).UTC().Format(time.RFC3339)
	}
	_, _ = a.db.ExecContext(ctx, `INSERT INTO file_access_rules(domain,file_name,access_mode,token,expires_at,single_use_left,token_use_count)
VALUES(?,?,?,?,?,?,0)
ON CONFLICT(domain,file_name) DO UPDATE SET access_mode=excluded.access_mode,token=excluded.token,expires_at=excluded.expires_at,single_use_left=excluded.single_use_left,token_use_count=CASE WHEN file_access_rules.token=excluded.token THEN file_access_rules.token_use_count ELSE 0 END`,
		domainStorageName(a.siteDomain(ctx, r)), fileName, accessMode, token, expiresAt, singleUseLeft)
}

func (a *App) serveAssetFile(w http.ResponseWriter, r *http.Request, domain, fileName string) {
	rule := ManagedFileAccess{AccessMode: "public"}
	_ = a.db.QueryRowContext(r.Context(), `SELECT access_mode,token,expires_at,single_use_left,token_use_count FROM file_access_rules WHERE domain=? AND file_name=?`, domain, fileName).Scan(&rule.AccessMode, &rule.Token, &rule.ExpiresAt, &rule.SingleUseLeft, &rule.TokenUseCount)
	if strings.TrimSpace(rule.AccessMode) == "timer" {
		if strings.TrimSpace(rule.ExpiresAt) != "" {
			expiresAt, parseErr := time.Parse(time.RFC3339, rule.ExpiresAt)
			if parseErr == nil && time.Now().UTC().After(expiresAt) {
				http.Error(w, "timer expired", http.StatusForbidden)
				return
			}
		}
	}
	if strings.TrimSpace(rule.AccessMode) == "token" {
		requestedToken := strings.TrimSpace(r.URL.Query().Get("token"))
		if requestedToken == "" || requestedToken != rule.Token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if strings.TrimSpace(rule.ExpiresAt) != "" {
			expiresAt, parseErr := time.Parse(time.RFC3339, rule.ExpiresAt)
			if parseErr == nil && time.Now().UTC().After(expiresAt) {
				http.Error(w, "token expired", http.StatusForbidden)
				return
			}
		}
		if rule.SingleUseLeft > 0 {
			_, _ = a.db.ExecContext(r.Context(), `UPDATE file_access_rules SET single_use_left=single_use_left-1 WHERE domain=? AND file_name=? AND single_use_left>0`, domain, fileName)
		}
		_, _ = a.db.ExecContext(r.Context(), `UPDATE file_access_rules SET token_use_count=token_use_count+1 WHERE domain=? AND file_name=?`, domain, fileName)
		_, _ = a.db.ExecContext(r.Context(), `UPDATE file_metadata SET download_count=download_count+1 WHERE domain=? AND file_name=?`, domain, fileName)
	}
	filePath := filepath.Join(a.filesRootDir(), domain, filepath.FromSlash(fileName))
	if _, err := os.Stat(filePath); err != nil && !strings.HasPrefix(fileName, "p/") {
		legacyPath := filepath.Join(a.filesRootDir(), domain, "p", filepath.FromSlash(fileName))
		if _, legacyErr := os.Stat(legacyPath); legacyErr == nil {
			filePath = legacyPath
		}
	}
	http.ServeFile(w, r, filePath)
}

func (a *App) publicAssetExists(domain, fileName string) bool {
	filePath := filepath.Join(a.filesRootDir(), domain, filepath.FromSlash(fileName))
	if fileInfo, err := os.Stat(filePath); err == nil && !fileInfo.IsDir() {
		return true
	}
	if strings.HasPrefix(fileName, "p/") {
		return false
	}
	legacyPath := filepath.Join(a.filesRootDir(), domain, "p", filepath.FromSlash(fileName))
	fileInfo, err := os.Stat(legacyPath)
	return err == nil && !fileInfo.IsDir()
}

func publicAssetFileNameFromPath(requestPath, domain string) string {
	cleanedPath := path.Clean("/" + strings.TrimSpace(requestPath))
	if strings.HasPrefix(cleanedPath, "/p/") {
		return safeRelativeAssetPath(strings.TrimPrefix(cleanedPath, "/p/"))
	}
	domainPrefix := "/" + domainStorageName(domain) + "/p/"
	if strings.HasPrefix(cleanedPath, domainPrefix) {
		return safeRelativeAssetPath(strings.TrimPrefix(cleanedPath, domainPrefix))
	}
	if prefixIndex := strings.Index(cleanedPath, "/p/"); prefixIndex > 0 {
		return safeRelativeAssetPath(cleanedPath[prefixIndex+len("/p/"):])
	}
	return ""
}

func (a *App) isDomainPrefixedPublicAssetPath(r *http.Request) bool {
	return publicAssetFileNameFromPath(r.URL.Path, domainFromContext(r.Context())) != "" && !strings.HasPrefix(path.Clean(r.URL.Path), "/p/")
}

func (a *App) findPage(ctx context.Context, domain, pagePath string) (Page, error) {
	var current Page
	err := a.db.QueryRowContext(ctx, `SELECT domain,path,title,html,published FROM pages WHERE domain=? AND path=?`, domain, pagePath).Scan(&current.Domain, &current.Path, &current.Title, &current.HTML, &current.Published)
	return current, err
}

func (a *App) resolvedPageRedirectPath(ctx context.Context, domain, pagePath string) string {
	currentPath := cleanPath(pagePath)
	visitedPathSet := map[string]struct{}{currentPath: {}}
	for redirectStep := 0; redirectStep < 16; redirectStep++ {
		var nextPath string
		err := a.db.QueryRowContext(ctx, `SELECT new_path FROM page_redirects WHERE domain=? AND old_path=?`, domain, currentPath).Scan(&nextPath)
		if err != nil {
			if len(visitedPathSet) == 1 {
				return ""
			}
			return currentPath
		}
		nextPath = cleanPath(nextPath)
		if nextPath == currentPath {
			return ""
		}
		if _, alreadyVisited := visitedPathSet[nextPath]; alreadyVisited {
			return ""
		}
		visitedPathSet[nextPath] = struct{}{}
		currentPath = nextPath
	}
	return currentPath
}

func (a *App) authIPFailureState(ctx context.Context, domain, clientIP string) authIPFailure {
	state := authIPFailure{}
	if strings.TrimSpace(domain) == "" || strings.TrimSpace(clientIP) == "" {
		return state
	}
	now := time.Now().UTC()
	for _, clientIPAlias := range clientIPStorageAliases(clientIP) {
		if cachedState, found := a.cachedAuthIPFailureState(domain, clientIPAlias); found {
			state = strongerAuthIPFailureState(state, cachedState, now)
		}
	}
	if state.FailureCount > 0 || strings.TrimSpace(state.BlockedUntil) != "" || state.HardLocked == 1 {
		return state
	}
	for _, clientIPAlias := range clientIPStorageAliases(clientIP) {
		var candidate authIPFailure
		_ = a.db.QueryRowContext(ctx, `SELECT failure_count,blocked_until,hard_locked FROM auth_ip_failures WHERE domain=? AND client_ip=?`, domain, clientIPAlias).Scan(&candidate.FailureCount, &candidate.BlockedUntil, &candidate.HardLocked)
		if candidate.FailureCount > 0 || strings.TrimSpace(candidate.BlockedUntil) != "" || candidate.HardLocked == 1 {
			a.storeAuthIPFailureState(domain, clientIPAlias, candidate)
			state = strongerAuthIPFailureState(state, candidate, now)
		}
	}
	return state
}

func (a *App) authIPIsBlocked(ctx context.Context, domain, clientIP string) (bool, bool, time.Time) {
	state := a.authIPFailureState(ctx, domain, clientIP)
	return authIPBlockFromFailureState(state, time.Now().UTC())
}

func (a *App) authIPIsBlockedForDomainPrefix(ctx context.Context, domainPrefix, clientIP string) (bool, bool, time.Time) {
	state := a.authIPFailureStateForDomainPrefix(ctx, domainPrefix, clientIP)
	return authIPBlockFromFailureState(state, time.Now().UTC())
}

func (a *App) cachedAuthIPIsBlocked(domain, clientIP string) (bool, bool, time.Time) {
	state, found := a.cachedAuthIPFailureState(domain, clientIP)
	if !found {
		return false, false, time.Time{}
	}
	return authIPBlockFromFailureState(state, time.Now().UTC())
}

func (a *App) cachedAuthIPIsBlockedForDomainPrefix(domainPrefix, clientIP string) (bool, bool, time.Time) {
	state, found := a.cachedAuthIPFailureStateForDomainPrefix(domainPrefix, clientIP)
	if !found {
		return false, false, time.Time{}
	}
	return authIPBlockFromFailureState(state, time.Now().UTC())
}

func authIPBlockFromFailureState(state authIPFailure, now time.Time) (bool, bool, time.Time) {
	if state.HardLocked == 1 {
		return true, true, time.Time{}
	}
	if strings.TrimSpace(state.BlockedUntil) == "" {
		return false, false, time.Time{}
	}
	blockedUntil, parseErr := time.Parse(time.RFC3339, state.BlockedUntil)
	if parseErr != nil || !now.UTC().Before(blockedUntil) {
		return false, false, time.Time{}
	}
	return true, false, blockedUntil
}

func strongerAuthIPFailureState(current authIPFailure, candidate authIPFailure, now time.Time) authIPFailure {
	if candidate.HardLocked == 1 {
		return candidate
	}
	if current.HardLocked == 1 {
		return current
	}
	currentBlocked, _, currentBlockedUntil := authIPBlockFromFailureState(current, now)
	candidateBlocked, _, candidateBlockedUntil := authIPBlockFromFailureState(candidate, now)
	if candidateBlocked && (!currentBlocked || candidateBlockedUntil.After(currentBlockedUntil)) {
		return candidate
	}
	if currentBlocked {
		return current
	}
	if candidate.FailureCount > current.FailureCount {
		return candidate
	}
	return current
}

func (a *App) authIPFailureStateForDomainPrefix(ctx context.Context, domainPrefix, clientIP string) authIPFailure {
	state := authIPFailure{}
	if strings.TrimSpace(domainPrefix) == "" || strings.TrimSpace(clientIP) == "" {
		return state
	}
	if cachedState, found := a.cachedAuthIPFailureStateForDomainPrefix(domainPrefix, clientIP); found {
		return cachedState
	}
	now := time.Now().UTC()
	for _, clientIPAlias := range clientIPStorageAliases(clientIP) {
		rows, err := a.db.QueryContext(ctx, `SELECT domain,failure_count,blocked_until,hard_locked FROM auth_ip_failures WHERE client_ip=?`, clientIPAlias)
		if err != nil {
			continue
		}
		for rows.Next() {
			var failureDomain string
			var candidate authIPFailure
			if scanErr := rows.Scan(&failureDomain, &candidate.FailureCount, &candidate.BlockedUntil, &candidate.HardLocked); scanErr != nil {
				continue
			}
			if failureDomain != strings.TrimSuffix(domainPrefix, "|") && !strings.HasPrefix(failureDomain, domainPrefix) {
				continue
			}
			state = strongerAuthIPFailureState(state, candidate, now)
			a.storeAuthIPFailureState(failureDomain, clientIPAlias, candidate)
		}
		_ = rows.Close()
	}
	return state
}

func (a *App) cachedAuthIPFailureState(domain, clientIP string) (authIPFailure, bool) {
	cache := a.authIPFailureCache
	if cache == nil || strings.TrimSpace(domain) == "" || strings.TrimSpace(clientIP) == "" {
		return authIPFailure{}, false
	}
	response := make(chan authIPFailureCacheResponse, 1)
	request := authIPFailureCacheRequest{action: "get", domain: domain, clientIP: clientIP, response: response}
	select {
	case cache <- request:
	default:
		return authIPFailure{}, false
	}
	select {
	case result := <-response:
		return result.state, result.found
	case <-time.After(50 * time.Millisecond):
		return authIPFailure{}, false
	}
}

func (a *App) cachedAuthIPFailureStateForDomainPrefix(domainPrefix, clientIP string) (authIPFailure, bool) {
	cache := a.authIPFailureCache
	if cache == nil || strings.TrimSpace(domainPrefix) == "" || strings.TrimSpace(clientIP) == "" {
		return authIPFailure{}, false
	}
	response := make(chan authIPFailureCacheResponse, 1)
	request := authIPFailureCacheRequest{action: "max-prefix", domain: domainPrefix, clientIP: clientIP, response: response}
	select {
	case cache <- request:
	default:
		return authIPFailure{}, false
	}
	select {
	case result := <-response:
		return result.state, result.found
	case <-time.After(50 * time.Millisecond):
		return authIPFailure{}, false
	}
}

func (a *App) storeAuthIPFailureState(domain, clientIP string, state authIPFailure) {
	cache := a.authIPFailureCache
	if cache == nil || strings.TrimSpace(domain) == "" || strings.TrimSpace(clientIP) == "" {
		return
	}
	response := make(chan authIPFailureCacheResponse, 1)
	request := authIPFailureCacheRequest{action: "set", domain: domain, clientIP: clientIP, state: state, response: response}
	select {
	case cache <- request:
	default:
		return
	}
	select {
	case <-response:
	case <-time.After(50 * time.Millisecond):
	}
}

func (a *App) deleteAuthIPFailureState(domain, clientIP string) {
	cache := a.authIPFailureCache
	if cache == nil || strings.TrimSpace(domain) == "" || strings.TrimSpace(clientIP) == "" {
		return
	}
	for _, clientIPAlias := range clientIPStorageAliases(clientIP) {
		response := make(chan authIPFailureCacheResponse, 1)
		request := authIPFailureCacheRequest{action: "delete", domain: domain, clientIP: clientIPAlias, response: response}
		select {
		case cache <- request:
		default:
			return
		}
		select {
		case <-response:
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (a *App) registerFailedLoginAttempt(ctx context.Context, domain, clientIP string) (int, time.Time, bool) {
	if strings.TrimSpace(domain) == "" || strings.TrimSpace(clientIP) == "" {
		return 0, time.Time{}, false
	}
	state := a.authIPFailureState(ctx, domain, clientIP)
	nextFailureCount := state.FailureCount + 1
	now := time.Now().UTC()
	blockDuration, hardLocked := loginBlockDurationForFailureCount(nextFailureCount)
	blockedUntilText := ""
	blockedUntil := time.Time{}
	if blockDuration > 0 {
		blockedUntil = now.Add(blockDuration)
		blockedUntilText = blockedUntil.Format(time.RFC3339)
	}
	_, _ = a.db.ExecContext(ctx, `INSERT INTO auth_ip_failures(domain,client_ip,failure_count,blocked_until,hard_locked,last_failed_at,last_attempt_at)
VALUES(?,?,?,?,?,?,?)
ON CONFLICT(domain,client_ip) DO UPDATE SET
	failure_count=excluded.failure_count,
	blocked_until=excluded.blocked_until,
	hard_locked=excluded.hard_locked,
	last_failed_at=excluded.last_failed_at,
	last_attempt_at=excluded.last_attempt_at`,
		domain, clientIP, nextFailureCount, blockedUntilText, boolToInt(hardLocked), now.Format(time.RFC3339), now.Format(time.RFC3339))
	a.storeAuthIPFailureState(domain, clientIP, authIPFailure{FailureCount: nextFailureCount, BlockedUntil: blockedUntilText, HardLocked: boolToInt(hardLocked)})
	return nextFailureCount, blockedUntil, hardLocked
}

func (a *App) clearFailedLoginAttempts(ctx context.Context, domain, clientIP string) {
	if strings.TrimSpace(domain) == "" || strings.TrimSpace(clientIP) == "" {
		return
	}
	a.deleteAuthIPFailureState(domain, clientIP)
	for _, clientIPAlias := range clientIPStorageAliases(clientIP) {
		_, _ = a.db.ExecContext(ctx, `DELETE FROM auth_ip_failures WHERE domain=? AND client_ip=?`, domain, clientIPAlias)
	}
}

func (a *App) clearPageRedirectSource(ctx context.Context, domain, pagePath string) {
	_, _ = a.db.ExecContext(ctx, `DELETE FROM page_redirects WHERE domain=? AND old_path=?`, domain, cleanPath(pagePath))
}

func (a *App) registerPageRedirect(ctx context.Context, domain, oldPath, newPath string) {
	oldPath = cleanPath(oldPath)
	newPath = cleanPath(newPath)
	if oldPath == newPath {
		return
	}
	redirectCreatedAt := time.Now().Format(time.RFC3339)
	_, _ = a.db.ExecContext(ctx, `UPDATE page_redirects SET new_path=?, created_at=? WHERE domain=? AND new_path=?`, newPath, redirectCreatedAt, domain, oldPath)
	_, _ = a.db.ExecContext(ctx, `INSERT INTO page_redirects(domain,old_path,new_path,created_at) VALUES(?,?,?,?) ON CONFLICT(domain,old_path) DO UPDATE SET new_path=excluded.new_path, created_at=excluded.created_at`, domain, oldPath, newPath, redirectCreatedAt)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM page_redirects WHERE domain=? AND old_path=new_path`, domain)
}

func (a *App) hasAdmin(ctx context.Context, domain string) bool {
	var adminCount int
	_ = a.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM users WHERE domain=? AND is_admin=1`, domain).Scan(&adminCount)
	return adminCount > 0
}

func (a *App) isDomainFrozen(ctx context.Context, domain string) bool {
	var isFrozen int
	_ = a.db.QueryRowContext(ctx, `SELECT is_frozen FROM domain_states WHERE domain=?`, domain).Scan(&isFrozen)
	return isFrozen == 1
}

func requestedReturnPath(r *http.Request) string {
	if r.URL.Path == "" {
		return "/"
	}
	return r.URL.Path
}

func requestScheme(r *http.Request) string {
	forwardedProto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if forwardedProto != "" {
		if commaIndex := strings.Index(forwardedProto, ","); commaIndex >= 0 {
			forwardedProto = forwardedProto[:commaIndex]
		}
		forwardedProto = strings.ToLower(strings.TrimSpace(forwardedProto))
		if forwardedProto == "http" || forwardedProto == "https" {
			return forwardedProto
		}
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func absoluteURLForPath(r *http.Request, relativePath string) string {
	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = "localhost"
	}
	return requestScheme(r) + "://" + host + relativePath
}

func (a *App) createSession(w http.ResponseWriter, r *http.Request, email string) {
	token := fmt.Sprintf("%x", sha256.Sum256([]byte(email+time.Now().String())))
	_, _ = a.db.ExecContext(r.Context(), `INSERT OR REPLACE INTO sessions(token,user_email,created_at) VALUES(?,?,?)`, token, a.siteDomain(r.Context(), r)+"|"+email, time.Now().Format(time.RFC3339))
	http.SetCookie(w, &http.Cookie{Name: "sitebrush_session", Value: token, Path: "/", HttpOnly: true})
}

func (a *App) isAdminRequest(r *http.Request) bool {
	cookie, err := r.Cookie("sitebrush_session")
	if err != nil {
		return false
	}
	var sessionCount int
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(1) FROM sessions s JOIN users u ON (u.domain||'|'||u.email)=s.user_email WHERE s.token=? AND u.domain=? AND u.is_admin=1`, cookie.Value, a.siteDomain(r.Context(), r)).Scan(&sessionCount)
	return sessionCount > 0
}

func (a *App) currentAdminEmail(r *http.Request) (string, bool) {
	cookie, err := r.Cookie("sitebrush_session")
	if err != nil {
		return "", false
	}
	var email string
	err = a.db.QueryRowContext(r.Context(), `SELECT u.email FROM sessions s JOIN users u ON (u.domain||'|'||u.email)=s.user_email WHERE s.token=? AND u.domain=? AND u.is_admin=1 LIMIT 1`, cookie.Value, a.siteDomain(r.Context(), r)).Scan(&email)
	if err != nil || strings.TrimSpace(email) == "" {
		return "", false
	}
	return email, true
}

func (a *App) pagePasswordAction(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	domain := a.siteDomain(r.Context(), r)
	pagePath := cleanPath(r.URL.Path)
	action := strings.TrimSpace(r.URL.Query().Get("page_password"))
	switch action {
	case "protect":
		password := strings.TrimSpace(r.FormValue("password"))
		if password == "" {
			http.Error(w, "password is required", http.StatusBadRequest)
			return
		}
		a.setPagePasswordRule(r.Context(), domain, pagePath, password)
	case "remove":
		a.removePagePasswordRule(r.Context(), domain, pagePath)
	default:
		http.Error(w, "unknown password action", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, pagePath, http.StatusFound)
}

func (a *App) pagePasswordUnlock(w http.ResponseWriter, r *http.Request, domain, pagePath string) {
	rule, found := a.pagePasswordRuleFromPrefixFile(domain, pagePath)
	if !found {
		rule, found = a.pagePasswordRuleForPath(r.Context(), domain, pagePath)
	}
	if !found {
		http.Redirect(w, r, pagePath, http.StatusFound)
		return
	}
	if r.Method != http.MethodPost {
		a.renderPagePasswordPrompt(w, r, domain, pagePath, "", http.StatusUnauthorized, time.Time{})
		return
	}
	clientIP := clientIPAddress(r)
	failureDomain := pagePasswordFailureDomain(rule.Domain, rule.Path)
	if blocked, hardLocked, blockedUntil := a.authIPIsBlockedForDomainPrefix(r.Context(), pagePasswordFailureDomainPrefix(rule.Domain), clientIP); blocked {
		a.renderBlockedPagePasswordPrompt(w, r, domain, pagePath, hardLocked, blockedUntil)
		return
	}
	if !pagePasswordMatches(rule.PasswordHash, r.FormValue("password")) {
		translations := translationsForRequest(r)
		failureCount, blockedUntil, hardLocked := a.registerFailedLoginAttempt(r.Context(), failureDomain, clientIP)
		if hardLocked {
			status := translationOrDefault(translations, "protected_page_status_hard_locked", "Too many incorrect password entries from this IP. Access is blocked.")
			a.renderPagePasswordPrompt(w, r, domain, pagePath, status, http.StatusForbidden, time.Time{})
			return
		}
		if failureCount >= 4 {
			retryAfterSeconds := int(time.Until(blockedUntil).Seconds())
			if retryAfterSeconds < 1 {
				retryAfterSeconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
			status := translationOrDefault(translations, "protected_page_status_rate_limited", "Too many incorrect password entries from this IP. The next password entry is available after the delay below.")
			a.renderPagePasswordPrompt(w, r, domain, pagePath, status, http.StatusTooManyRequests, blockedUntil)
			return
		}
		status := translationOrDefault(translations, "protected_page_invalid", "Incorrect password.")
		a.renderPagePasswordPrompt(w, r, domain, pagePath, status, http.StatusUnauthorized, time.Time{})
		return
	}
	a.clearFailedLoginAttempts(r.Context(), failureDomain, clientIP)
	issuedAt := time.Now().UTC()
	cookie := &http.Cookie{Name: pagePasswordCookieName(rule.Domain, rule.Path), Value: pagePasswordSessionTokenForRequest(rule, r, issuedAt), Path: "/", Expires: issuedAt.Add(pagePasswordSessionTTL), MaxAge: int(pagePasswordSessionTTL.Seconds()), HttpOnly: true, SameSite: http.SameSiteLaxMode}
	if requestScheme(r) == "https" {
		cookie.Secure = true
	}
	http.SetCookie(w, cookie)
	http.Redirect(w, r, pagePath, http.StatusFound)
}

func (a *App) requirePagePassword(w http.ResponseWriter, r *http.Request, domain, pagePath string) (bool, bool) {
	if a.isAdminRequest(r) {
		return false, false
	}
	rule, found := a.pagePasswordRuleForPath(r.Context(), domain, pagePath)
	if !found {
		return false, false
	}
	if blocked, hardLocked, blockedUntil := a.authIPIsBlockedForDomainPrefix(r.Context(), pagePasswordFailureDomainPrefix(rule.Domain), clientIPAddress(r)); blocked {
		a.renderBlockedPagePasswordPrompt(w, r, domain, pagePath, hardLocked, blockedUntil)
		return true, false
	}
	if a.pagePasswordSessionValid(r, rule) {
		return false, true
	}
	a.renderPagePasswordPrompt(w, r, domain, pagePath, "", http.StatusUnauthorized, time.Time{})
	return true, false
}

func (a *App) pagePasswordSessionValid(r *http.Request, rule PagePasswordRule) bool {
	cookie, err := r.Cookie(pagePasswordCookieName(rule.Domain, rule.Path))
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return false
	}
	return pagePasswordSessionTokenValid(rule, cookie.Value, r, time.Now().UTC())
}

func (a *App) setPagePasswordRule(ctx context.Context, domain, pagePath, password string) {
	normalizedPath := cleanPath(pagePath)
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = a.db.ExecContext(ctx, `INSERT INTO page_password_rules(domain,path,password_hash,created_at,updated_at)
VALUES(?,?,?,?,?)
ON CONFLICT(domain,path) DO UPDATE SET password_hash=excluded.password_hash,updated_at=excluded.updated_at`,
		domain, normalizedPath, pagePasswordHash(password), now, now)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM page_password_sessions WHERE domain=? AND path=?`, domain, normalizedPath)
	a.writePagePasswordPrefixFile(ctx, domain)
}

func (a *App) removePagePasswordRule(ctx context.Context, domain, pagePath string) {
	normalizedPath := cleanPath(pagePath)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM page_password_rules WHERE domain=? AND path=?`, domain, normalizedPath)
	_, _ = a.db.ExecContext(ctx, `DELETE FROM page_password_sessions WHERE domain=? AND path=?`, domain, normalizedPath)
	a.writePagePasswordPrefixFile(ctx, domain)
}

func (a *App) pagePasswordRuleExists(ctx context.Context, domain, pagePath string) bool {
	var ruleCount int
	_ = a.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM page_password_rules WHERE domain=? AND path=?`, domain, cleanPath(pagePath)).Scan(&ruleCount)
	return ruleCount > 0
}

func (a *App) pagePasswordRuleForPath(ctx context.Context, domain, pagePath string) (PagePasswordRule, bool) {
	requestPath := cleanPath(pagePath)
	rows, err := a.db.QueryContext(ctx, `SELECT path,password_hash FROM page_password_rules WHERE domain=? ORDER BY path DESC`, domain)
	if err != nil {
		return PagePasswordRule{}, false
	}
	defer rows.Close()
	var bestRule PagePasswordRule
	for rows.Next() {
		var candidateRule PagePasswordRule
		candidateRule.Domain = domain
		if scanErr := rows.Scan(&candidateRule.Path, &candidateRule.PasswordHash); scanErr != nil {
			continue
		}
		candidateRule.Path = cleanPath(candidateRule.Path)
		if !pagePathHasProtectedPrefix(requestPath, candidateRule.Path) {
			continue
		}
		if len(candidateRule.Path) > len(bestRule.Path) {
			bestRule = candidateRule
		}
	}
	return bestRule, bestRule.Path != ""
}

func pagePathHasProtectedPrefix(pagePath, protectedPrefix string) bool {
	return dirprotect.HasProtectedPrefix(pagePath, protectedPrefix)
}

func pagePasswordHash(password string) string {
	return dirprotect.Hash(password)
}

func pagePasswordMatches(storedHash, password string) bool {
	return dirprotect.Matches(storedHash, password)
}

func pagePasswordCookieName(domain, pagePath string) string {
	return dirprotect.CookieName(normalizeDomainName(domain), pagePath)
}

func pagePasswordSessionTokenForRequest(rule PagePasswordRule, r *http.Request, issuedAt time.Time) string {
	rule.Domain = normalizeDomainName(rule.Domain)
	return dirprotect.BoundSessionToken(rule, clientIPAddress(r), r.UserAgent(), issuedAt)
}

func pagePasswordSessionTokenValid(rule PagePasswordRule, token string, r *http.Request, now time.Time) bool {
	rule.Domain = normalizeDomainName(rule.Domain)
	return dirprotect.BoundSessionTokenValid(rule, token, clientIPAddress(r), r.UserAgent(), now, pagePasswordSessionTTL)
}

func pagePasswordFailureDomain(domain, pagePath string) string {
	return strings.TrimSuffix(pagePasswordFailureDomainPrefix(domain), "|")
}

func pagePasswordFailureDomainPrefix(domain string) string {
	normalizedDomain := normalizeDomainName(domain)
	if normalizedDomain == "" {
		normalizedDomain = "localhost"
	}
	return normalizedDomain + "|page-password|"
}

func (a *App) pagePasswordRuleFromPrefixFile(domain, pagePath string) (PagePasswordRule, bool) {
	prefixBytes, err := os.ReadFile(a.pagePasswordPrefixFilePath(domain))
	if err != nil {
		return PagePasswordRule{}, false
	}
	normalizedDomain := canonicalLocalDomain(domain)
	if normalizedDomain == "" {
		normalizedDomain = "localhost"
	}
	return dirprotect.FindBestRuleInPrefixData(normalizedDomain, pagePath, prefixBytes)
}

func parsePagePasswordPrefixLine(domain, rawLine string) (PagePasswordRule, bool) {
	normalizedDomain := canonicalLocalDomain(domain)
	if normalizedDomain == "" {
		normalizedDomain = "localhost"
	}
	return dirprotect.ParsePrefixLine(normalizedDomain, rawLine)
}

func (a *App) rebuildPagePasswordPrefixFiles(ctx context.Context) {
	rows, err := a.db.QueryContext(ctx, `SELECT DISTINCT domain FROM page_password_rules`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var domain string
		if scanErr := rows.Scan(&domain); scanErr != nil {
			continue
		}
		a.writePagePasswordPrefixFile(ctx, domain)
	}
}

func (a *App) writePagePasswordPrefixFile(ctx context.Context, domain string) {
	normalizedDomain := canonicalLocalDomain(domain)
	if normalizedDomain == "" {
		return
	}
	rows, err := a.db.QueryContext(ctx, `SELECT path,password_hash FROM page_password_rules WHERE domain=? ORDER BY path ASC`, normalizedDomain)
	if err != nil {
		return
	}
	defer rows.Close()
	rules := make([]PagePasswordRule, 0)
	for rows.Next() {
		var rule PagePasswordRule
		if scanErr := rows.Scan(&rule.Path, &rule.PasswordHash); scanErr != nil {
			continue
		}
		rule.Path = cleanPath(rule.Path)
		rule.PasswordHash = strings.TrimSpace(rule.PasswordHash)
		if rule.Path != "" && rule.PasswordHash != "" {
			rules = append(rules, rule)
		}
	}
	prefixFilePath := a.pagePasswordPrefixFilePath(normalizedDomain)
	if len(rules) == 0 {
		_ = os.Remove(prefixFilePath)
		return
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(prefixFilePath), 0o700); mkdirErr != nil {
		return
	}
	_ = os.WriteFile(prefixFilePath, dirprotect.PrefixFileBody(rules), 0o600)
}

func (a *App) pagePasswordPrefixFilePath(domain string) string {
	normalizedDomain := canonicalLocalDomain(domain)
	if normalizedDomain == "" {
		normalizedDomain = "localhost"
	}
	return filepath.Join(a.storageRootDir(), "page-password-prefixes", domainStorageName(normalizedDomain)+".txt")
}

func (a *App) renderBlockedPagePasswordPrompt(w http.ResponseWriter, r *http.Request, domain, pagePath string, hardLocked bool, blockedUntil time.Time) {
	translations := translationsForRequest(r)
	if hardLocked {
		status := translationOrDefault(translations, "protected_page_status_hard_locked", "Too many incorrect password entries from this IP. Access is blocked.")
		a.renderPagePasswordPrompt(w, r, domain, pagePath, status, http.StatusForbidden, time.Time{})
		return
	}
	retryAfterSeconds := int(time.Until(blockedUntil).Seconds())
	if retryAfterSeconds < 1 {
		retryAfterSeconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
	status := translationOrDefault(translations, "protected_page_status_rate_limited", "Too many incorrect password entries from this IP. The next password entry is available after the delay below.")
	a.renderPagePasswordPrompt(w, r, domain, pagePath, status, http.StatusTooManyRequests, blockedUntil)
}

func (a *App) renderPagePasswordPrompt(w http.ResponseWriter, r *http.Request, domain, pagePath, status string, statusCode int, blockedUntil time.Time) {
	translations := translationsForRequest(r)
	languageCode := preferredLanguageCode(r.Header.Get("Accept-Language"))
	title := template.HTMLEscapeString(translationOrDefault(translations, "protected_page_title", "Password required"))
	header := template.HTMLEscapeString(translationOrDefault(translations, "protected_page_header", "This page is password protected."))
	placeholder := template.HTMLEscapeString(translationOrDefault(translations, "protected_page_password_placeholder", "Password..."))
	submitLabel := template.HTMLEscapeString(translationOrDefault(translations, "protected_page_submit", "Open"))
	countdownLabel := template.HTMLEscapeString(translationOrDefault(translations, "login_retry_in", "You can try again in:"))
	retryAtLabel := template.HTMLEscapeString(translationOrDefault(translations, "login_retry_at", "You can try again at:"))
	tryAgainNowText := template.HTMLEscapeString(translationOrDefault(translations, "login_try_again_now", "You can try again now."))
	statusHTML := ""
	if strings.TrimSpace(status) != "" {
		statusHTML = "<p class=\"SiteBrushProtectedStatus\">" + template.HTMLEscapeString(status) + "</p>"
	}
	formControlsHTML := `<input class="SiteBrushProtectedInput" type="password" name="password" placeholder="` + placeholder + `" autocomplete="current-password" required autofocus>
      <button class="SiteBrushProtectedButton" type="submit">` + submitLabel + `</button>`
	countdownHTML := ""
	if statusCode == http.StatusForbidden {
		formControlsHTML = ""
	}
	if !blockedUntil.IsZero() && blockedUntil.After(time.Now()) {
		secondsLeft := int(time.Until(blockedUntil).Seconds())
		if secondsLeft < 1 {
			secondsLeft = 1
		}
		blockedUntilUnix := strconv.FormatInt(blockedUntil.Unix(), 10)
		blockedUntilISO := template.HTMLEscapeString(blockedUntil.UTC().Format(time.RFC3339))
		countdownHTML = `<div id="SiteBrushProtectedCountdown" class="SiteBrushProtectedCountdown" data-blocked-until-unix="` + blockedUntilUnix + `" data-blocked-until-iso="` + blockedUntilISO + `" data-ready-text="` + tryAgainNowText + `">
        <div class="SiteBrushProtectedCountdownLabel">` + countdownLabel + `</div>
        <div class="SiteBrushProtectedCountdownClock" data-countdown-text>` + formatRetryCountdownSeconds(secondsLeft) + `</div>
        <div class="SiteBrushProtectedCountdownAt">` + retryAtLabel + ` <span data-countdown-at>` + blockedUntilISO + `</span></div>
      </div>`
		formControlsHTML = ""
	}
	actionURL := template.HTMLEscapeString(cleanPath(pagePath) + "?page_password_unlock")
	menuScript := buildGuestContextMenuScriptForLanguage(pagePath, domain, languageCode)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(statusCode)
	_, _ = w.Write([]byte(`<!doctype html>
<html lang="` + template.HTMLEscapeString(languageCode) + `">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>` + title + `</title>
  <style>
    :root{color-scheme:light dark;--protected-bg:#f4f8fc;--protected-text:#1f3f6f;--protected-border:#c8d5e7;--protected-accent:#1a65d8;--protected-danger:#b42318}
    @media (prefers-color-scheme:dark){:root{--protected-bg:#0f1724;--protected-text:#dbe8ff;--protected-border:#2f405d;--protected-accent:#9ec2ff;--protected-danger:#ffb4a8}}
    html,body{min-height:100%}
    body{margin:0;background:var(--protected-bg);color:var(--protected-text);font-family:Arial,Helvetica,sans-serif}
    .SiteBrushProtectedShell{min-height:100vh;display:flex;align-items:center;justify-content:center;padding:24px 16px;box-sizing:border-box}
    .SiteBrushProtectedPanel{width:min(420px,100%);border:1px solid var(--protected-border);padding:20px;background:rgba(255,255,255,.55)}
    @media (prefers-color-scheme:dark){.SiteBrushProtectedPanel{background:rgba(23,34,53,.6)}}
    .SiteBrushProtectedIcon{width:32px;height:32px;margin-bottom:12px}
    h1{margin:0 0 10px 0;font-size:24px;line-height:1.2}
    p{margin:0 0 14px 0;font-size:14px;line-height:1.4}
    .SiteBrushProtectedStatus{color:var(--protected-danger);font-weight:700}
    .SiteBrushProtectedCountdown{border:1px solid var(--protected-border);padding:12px;margin-top:12px}
    .SiteBrushProtectedCountdownLabel,.SiteBrushProtectedCountdownAt{font-size:13px;line-height:1.35;opacity:.78}
    .SiteBrushProtectedCountdownClock{font-size:32px;line-height:1.1;font-weight:700;margin:8px 0}
    .SiteBrushProtectedInput{width:100%;box-sizing:border-box;border:1px solid var(--protected-border);padding:11px 12px;font:inherit;margin-bottom:12px;background:transparent;color:var(--protected-text)}
    .SiteBrushProtectedButton{width:100%;border:0;background:var(--protected-accent);color:#fff;padding:11px 12px;font:inherit;font-weight:700;cursor:pointer}
  </style>
</head>
<body>
  <main class="SiteBrushProtectedShell">
    <form class="SiteBrushProtectedPanel" method="post" action="` + actionURL + `">
      <img class="SiteBrushProtectedIcon" src="/p/static/lock.png" alt="">
      <h1>` + title + `</h1>
      <p>` + header + `</p>
      ` + statusHTML + `
      ` + countdownHTML + `
      ` + formControlsHTML + `
    </form>
  </main>
  ` + menuScript + `
  <script>
(function startProtectedPageBlockCountdown() {
  var countdownRoot = document.getElementById('SiteBrushProtectedCountdown');
  if (!countdownRoot) {
    return;
  }

  var blockedUntilUnix = Number(countdownRoot.getAttribute('data-blocked-until-unix'));
  if (!Number.isFinite(blockedUntilUnix) || blockedUntilUnix <= 0) {
    return;
  }

  var countdownTextElement = countdownRoot.querySelector('[data-countdown-text]');
  var countdownAtElement = countdownRoot.querySelector('[data-countdown-at]');
  var readyText = countdownRoot.getAttribute('data-ready-text') || '';
  var blockedUntilDate = new Date(blockedUntilUnix * 1000);

  if (countdownAtElement) {
    countdownAtElement.textContent = blockedUntilDate.toLocaleString(document.documentElement.lang || undefined);
  }

  function formatCountdown(totalSeconds) {
    var hours = Math.floor(totalSeconds / 3600);
    var minutes = Math.floor((totalSeconds % 3600) / 60);
    var seconds = totalSeconds % 60;
    return String(hours).padStart(2, '0') + ':' + String(minutes).padStart(2, '0') + ':' + String(seconds).padStart(2, '0');
  }

  function renderCountdown() {
    var secondsLeft = Math.ceil((blockedUntilDate.getTime() - Date.now()) / 1000);
    if (secondsLeft <= 0) {
      if (countdownTextElement) {
        countdownTextElement.textContent = readyText;
      }
      window.setTimeout(function reloadProtectedPage() {
        window.location.reload();
      }, 1000);
      return;
    }
    if (countdownTextElement) {
      countdownTextElement.textContent = formatCountdown(secondsLeft);
    }
    window.setTimeout(renderCountdown, 1000);
  }

  renderCountdown();
})();
</script>
</body>
</html>`))
}

func formatRetryCountdownSeconds(totalSeconds int) string {
	if totalSeconds < 0 {
		totalSeconds = 0
	}
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
}

func (a *App) latestActiveRevisionID(ctx context.Context, domain string, pagePath string) int {
	var revisionID int
	_ = a.db.QueryRowContext(ctx, `SELECT id FROM revisions WHERE domain=? AND page_path=? AND is_active=1 ORDER BY id DESC LIMIT 1`, domain, pagePath).Scan(&revisionID)
	return revisionID
}

func (a *App) revisionCount(ctx context.Context, domain string, pagePath string) int {
	var revisionCount int
	_ = a.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM revisions WHERE domain=? AND page_path=?`, domain, pagePath).Scan(&revisionCount)
	return revisionCount
}

func (a *App) serveManagedPageContent(w http.ResponseWriter, r *http.Request, pagePath, content, sourceType string) {
	a.logContentDelivery(w, sourceType)
	if pageContentKind(pagePath, content) == "html" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(a.injectContextMenu(r, pagePath, content)))
		return
	}
	w.Header().Set("Content-Type", contentTypeForManagedPage(pagePath, content))
	_, _ = w.Write([]byte(content))
}

func (a *App) renderMissingPage(w http.ResponseWriter, r *http.Request, pagePath string, isAdmin bool) {
	w.WriteHeader(http.StatusNotFound)
	a.render(w, r, "missing.html", map[string]any{"Path": pagePath, "EditLink": pagePath + "?visual", "IsAdmin": isAdmin})
}

func (a *App) serveGuestNotFoundPage(w http.ResponseWriter, r *http.Request, domain, pagePath string) {
	languageCode := preferredLanguageCode(r.Header.Get("Accept-Language"))
	body := buildGuestNotFoundPageForLanguage(pagePath, domain, languageCode)
	a.logContentDelivery(w, "static-file")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(body))
}

func (a *App) injectContextMenu(r *http.Request, pagePath, html string) string {
	domain := a.siteDomain(r.Context(), r)
	revisionID := 0
	revisionCount := 0
	storageUsageLabel := ""
	if a.isAdminRequest(r) {
		revisionID = a.latestActiveRevisionID(r.Context(), domain, pagePath)
		revisionCount = a.revisionCount(r.Context(), domain, pagePath)
		storageUsage := a.domainStorageUsage(r.Context(), domain)
		storageUsageLabel = formatFileSize(storageUsage.totalBytes()) + " / " + formatFileSize(storageUsage.LimitBytes)
	}
	pagePasswordProtected := a.isAdminRequest(r) && a.pagePasswordRuleExists(r.Context(), domain, pagePath)
	menuScript := buildContextMenuScript(a.isAdminRequest(r), a.isDomainFrozen(r.Context(), domain), pagePasswordProtected, pagePath, domain, revisionID, revisionCount, storageUsageLabel, translationsForRequest(r))
	if strings.Contains(strings.ToLower(html), "</body>") {
		bodyClosePattern := regexp.MustCompile(`(?i)</body>`)
		return bodyClosePattern.ReplaceAllString(html, menuScript+"</body>")
	}
	return html + menuScript
}

func pageContentKind(pagePath, content string) string {
	extension := strings.ToLower(path.Ext(strings.TrimSpace(pagePath)))
	switch extension {
	case ".htm", ".html":
		return "html"
	case ".txt", ".text", ".md", ".markdown", ".css", ".js", ".mjs", ".json", ".xml", ".csv", ".tsv", ".yml", ".yaml", ".toml", ".ini", ".svg":
		return "text"
	}
	if extension != "" {
		return "file"
	}
	trimmedContent := strings.TrimSpace(strings.ToLower(content))
	if trimmedContent == "" {
		return "html"
	}
	if strings.HasPrefix(trimmedContent, "<!doctype html") || strings.HasPrefix(trimmedContent, "<html") || strings.Contains(trimmedContent, "</body>") {
		return "html"
	}
	if strings.Contains(trimmedContent, "<script") || strings.Contains(trimmedContent, "<style") || looksLikeHTMLFragment(trimmedContent) {
		return "html"
	}
	return "text"
}

func looksLikeHTMLFragment(trimmedContent string) bool {
	for _, tagName := range []string{"<div", "<section", "<article", "<main", "<header", "<footer", "<nav", "<p", "<h1", "<h2", "<h3", "<ul", "<ol", "<table", "<form", "<img", "<a "} {
		if strings.Contains(trimmedContent, tagName) {
			return true
		}
	}
	return false
}

func contentKindLabel(kind string) string {
	switch kind {
	case "html":
		return "HTML page"
	case "text":
		return "Text document"
	default:
		return "File"
	}
}

func contentTypeForManagedPage(pagePath, content string) string {
	extension := strings.ToLower(path.Ext(strings.TrimSpace(pagePath)))
	if extension != "" {
		if contentType := mime.TypeByExtension(extension); contentType != "" {
			if strings.HasPrefix(contentType, "text/") && !strings.Contains(strings.ToLower(contentType), "charset=") {
				return contentType + "; charset=utf-8"
			}
			return contentType
		}
	}
	if strings.TrimSpace(content) == "" {
		return "text/plain; charset=utf-8"
	}
	detectedContentType := http.DetectContentType([]byte(content))
	if strings.HasPrefix(detectedContentType, "text/") && !strings.Contains(strings.ToLower(detectedContentType), "charset=") {
		return detectedContentType + "; charset=utf-8"
	}
	return detectedContentType
}

func buildContextMenuScript(isAdmin bool, isFrozen bool, pagePasswordProtected bool, pagePath, domain string, revisionID int, revisionCount int, storageUsageLabel string, translations map[string]string) string {
	if !isAdmin {
		return buildGuestContextMenuScript(pagePath, domain, translations)
	}
	escapedPath := template.JSEscapeString(pagePath)
	escapedDomain := template.JSEscapeString(domain)
	confirmFreezePrompt := template.JSEscapeString(translationOrDefault(translations, "confirm_freeze_prompt", "Freeze domain now?"))
	confirmPublishPrompt := template.JSEscapeString(translationOrDefault(translations, "confirm_publish_prompt", "Publish website changes now?"))
	confirmDeletePrompt := template.JSEscapeString(translationOrDefault(translations, "confirm_delete_revision_prompt", "Delete this revision?"))
	publishConfirmWithChangesLabel := template.JSEscapeString(translationOrDefault(translations, "publish_confirm_with_changes", "Publish the changes made to the site?"))
	publishConfirmWithoutChangesLabel := template.JSEscapeString(translationOrDefault(translations, "publish_confirm_without_changes", "No changes were made. Unfreeze the site?"))
	publishPreviewLoadingLabel := template.JSEscapeString(translationOrDefault(translations, "publish_preview_loading", "Checking changes to publish..."))
	publishPreviewSummaryLabel := template.JSEscapeString(translationOrDefault(translations, "publish_preview_summary", "Changes:"))
	publishProgressPreparingLabel := template.JSEscapeString(translationOrDefault(translations, "publish_progress_preparing", "Preparing publication..."))
	publishProgressPagesLabel := template.JSEscapeString(translationOrDefault(translations, "publish_progress_pages", "Publishing pages..."))
	publishProgressPackLabel := template.JSEscapeString(translationOrDefault(translations, "publish_progress_pack", "Rebuilding backup package..."))
	publishProgressUnfreezeLabel := template.JSEscapeString(translationOrDefault(translations, "publish_progress_unfreeze", "Opening the site to visitors..."))
	publishProgressDoneLabel := template.JSEscapeString(translationOrDefault(translations, "publish_progress_done", "Done. Refreshing page..."))
	publishProgressFailedLabel := template.JSEscapeString(translationOrDefault(translations, "publish_progress_failed", "Publication failed."))
	publishProgressRemainingLabel := template.JSEscapeString(translationOrDefault(translations, "publish_progress_remaining", "Remaining:"))
	confirmYesLabel := template.JSEscapeString(translationOrDefault(translations, "confirm_yes", "Yes"))
	confirmNoLabel := template.JSEscapeString(translationOrDefault(translations, "confirm_no", "No"))
	editLabel := template.JSEscapeString(translationOrDefault(translations, "menu_edit", "Edit"))
	textEditLabel := template.JSEscapeString(translationOrDefault(translations, "menu_text_edit", "Edit as text"))
	deleteLabel := template.JSEscapeString(translationOrDefault(translations, "menu_delete", "Delete"))
	protectPasswordLabel := template.JSEscapeString(translationOrDefault(translations, "menu_protect_password", "Protect with password"))
	removePasswordProtectionLabel := template.JSEscapeString(translationOrDefault(translations, "menu_remove_password_protection", "Remove password protection"))
	revisionsLabel := template.JSEscapeString(fmt.Sprintf("%s: %d", translationOrDefault(translations, "menu_revisions", "Revisions"), revisionCount))
	filesLabel := template.JSEscapeString(translationOrDefault(translations, "menu_files", "Files"))
	treeLabel := template.JSEscapeString(translationOrDefault(translations, "menu_tree", "Site tree"))
	freezeLabel := template.JSEscapeString(translationOrDefault(translations, "menu_freeze", "Freeze"))
	publishLabel := template.JSEscapeString(translationOrDefault(translations, "menu_publish", "Publish"))
	settingsLabel := template.JSEscapeString(translationOrDefault(translations, "menu_domain_settings", "Settings"))
	analyticsLabel := template.JSEscapeString(translationOrDefault(translations, "menu_analytics", "Analytics"))
	profileLabel := template.JSEscapeString(translationOrDefault(translations, "menu_profile", "Account"))
	logoutLabel := template.JSEscapeString(translationOrDefault(translations, "menu_logout", "Sign out"))
	loginLabel := template.JSEscapeString(translationOrDefault(translations, "menu_login", "Sign in"))
	treeModalTitle := template.JSEscapeString(translationOrDefault(translations, "tree_modal_title", "Site tree"))
	treeLoadingLabel := template.JSEscapeString(translationOrDefault(translations, "tree_loading", "Loading site tree..."))
	treeLoadErrorLabel := template.JSEscapeString(translationOrDefault(translations, "tree_load_error", "Failed to load site tree."))
	treeCloseLabel := template.JSEscapeString(translationOrDefault(translations, "tree_close", "Close"))
	protectPasswordPrompt := template.JSEscapeString(translationOrDefault(translations, "protect_password_prompt", "Password for this page and nested pages:"))
	protectPasswordEmptyLabel := template.JSEscapeString(translationOrDefault(translations, "protect_password_empty", "Password is required."))
	removePasswordProtectionPrompt := template.JSEscapeString(translationOrDefault(translations, "confirm_remove_password_protection", "Remove password protection for this page and nested pages?"))
	compiledVersionLabel := template.JSEscapeString("v." + CompileVersion)
	sitebrushHomeURL := "https://sitebrush.com"
	serverBinaryDownloadURL := latestServerBinaryDownloadURL(runtime.GOOS, runtime.GOARCH)
	storageUsageHTML := ""
	if strings.TrimSpace(storageUsageLabel) != "" {
		storageUsageHTML = "<span class='SiteBrushMenuStorageUsage'>" + template.HTMLEscapeString(storageUsageLabel) + "</span>"
	}
	copyrightMenuEntry := fmt.Sprintf("<li class='SiteBrushContextMenu ContextMenuCopyright'><div class='SiteBrushContextMenuFooter'><a href='%s' class='SiteBrushContextMenuFooterLink' target='_blank' rel='noopener noreferrer'>sitebrush</a><a href='%s' class='SiteBrushContextMenuVersion' download>%s</a>%s</div></li>", sitebrushHomeURL, serverBinaryDownloadURL, compiledVersionLabel, storageUsageHTML)
	if isAdmin {
		deleteActionEntry := ""
		if revisionID > 0 {
			deleteActionEntry = "<li class='SiteBrushContextMenu'><button type='button' data-sitebrush-action='delete' class='SiteBrushContextMenuLink SiteBrushContextMenuButton'><img src='/p/static/cross.png' class='SiteBrushMenuIcon' alt=''>" + deleteLabel + "</button></li>"
		}
		freezeActionEntry := "<li class='SiteBrushContextMenu'><button type='button' data-sitebrush-action='freeze' class='SiteBrushContextMenuLink SiteBrushContextMenuButton'><img src='/p/static/freeze.png' class='SiteBrushMenuIcon' alt=''>" + freezeLabel + "</button></li>"
		if isFrozen {
			freezeActionEntry = "<li class='SiteBrushContextMenu'><button type='button' data-sitebrush-action='publish' class='SiteBrushContextMenuLink SiteBrushContextMenuButton'><img src='/p/static/publish.png' class='SiteBrushMenuIcon' alt=''>" + publishLabel + "</button></li>"
		}
		passwordActionEntry := "<li class='SiteBrushContextMenu'><button type='button' data-sitebrush-action='protect_password' class='SiteBrushContextMenuLink SiteBrushContextMenuButton'><img src='/p/static/lock.png' class='SiteBrushMenuIcon' alt=''>" + protectPasswordLabel + "</button></li>"
		if pagePasswordProtected {
			passwordActionEntry = "<li class='SiteBrushContextMenu'><button type='button' data-sitebrush-action='remove_password_protection' class='SiteBrushContextMenuLink SiteBrushContextMenuButton'><img src='/p/static/unlock.png' class='SiteBrushMenuIcon' alt=''>" + removePasswordProtectionLabel + "</button></li>"
		}
		return contextMenuStylesAndHelpers() + `<script>
(function initializeSitebrushContextMenuForAdmin() {
  if (window.__sitebrushContextMenuInitialized) {
    return;
  }
  window.__sitebrushContextMenuInitialized = true;
  const currentPagePath = "` + escapedPath + `";
  const currentDomainName = "` + escapedDomain + `";
  const isDomainFrozen = ` + strconv.FormatBool(isFrozen) + `;
  const actionConfigByName = {
    delete: { path: "?delete=` + strconv.Itoa(revisionID) + `", message: "` + confirmDeletePrompt + `" },
    freeze: { path: "?freeze", message: "` + confirmFreezePrompt + `" },
    publish: { path: "?publish", message: "` + confirmPublishPrompt + `" },
    remove_password_protection: { path: "?page_password=remove", message: "` + removePasswordProtectionPrompt + `" }
  };
  const confirmYesLabel = "` + confirmYesLabel + `";
  const confirmNoLabel = "` + confirmNoLabel + `";
  function openConfirmationDialog(confirmMessageText, onConfirm) {
    closeSitebrushMenu();
    const overlayElement = document.createElement("div");
    overlayElement.className = "SiteBrushConfirmOverlay";
    const modalElement = document.createElement("div");
    modalElement.className = "SiteBrushConfirmModal";
    const textElement = document.createElement("p");
    textElement.className = "SiteBrushConfirmText";
    textElement.textContent = confirmMessageText;
    const actionRowElement = document.createElement("div");
    actionRowElement.className = "SiteBrushConfirmActions";
    const confirmButtonElement = document.createElement("button");
    confirmButtonElement.type = "button";
    confirmButtonElement.className = "SiteBrushConfirmButton";
    confirmButtonElement.textContent = confirmYesLabel;
    const cancelButtonElement = document.createElement("button");
    cancelButtonElement.type = "button";
    cancelButtonElement.className = "SiteBrushCancelButton";
    cancelButtonElement.textContent = confirmNoLabel;
    actionRowElement.appendChild(confirmButtonElement);
    actionRowElement.appendChild(cancelButtonElement);
    modalElement.appendChild(textElement);
    modalElement.appendChild(actionRowElement);
    overlayElement.appendChild(modalElement);
    document.body.appendChild(overlayElement);
    function closeDialog() { overlayElement.remove(); }
    cancelButtonElement.addEventListener("click", closeDialog);
    confirmButtonElement.addEventListener("click", function onConfirmClick() { closeDialog(); onConfirm(); });
  }
  function openPublishConfirmationDialog(confirmMessageText, changedPagePaths, onConfirm) {
    closeSitebrushMenu();
    const overlayElement = document.createElement("div");
    overlayElement.className = "SiteBrushConfirmOverlay";
    const modalElement = document.createElement("div");
    modalElement.className = "SiteBrushConfirmModal";
    const textElement = document.createElement("p");
    textElement.className = "SiteBrushConfirmText";
    textElement.textContent = confirmMessageText;
    modalElement.appendChild(textElement);
    if (Array.isArray(changedPagePaths) && changedPagePaths.length > 0) {
      const listElement = document.createElement("ul");
      listElement.className = "SiteBrushPublishPreviewList";
      for (const changedPagePath of changedPagePaths) {
        const itemElement = document.createElement("li");
        itemElement.className = "SiteBrushPublishPreviewListItem";
        const linkElement = document.createElement("a");
        linkElement.className = "SiteBrushPublishPreviewLink";
        linkElement.href = changedPagePath;
        linkElement.textContent = changedPagePath;
        itemElement.appendChild(linkElement);
        listElement.appendChild(itemElement);
      }
      modalElement.appendChild(listElement);
    }
    const actionRowElement = document.createElement("div");
    actionRowElement.className = "SiteBrushConfirmActions";
    const confirmButtonElement = document.createElement("button");
    confirmButtonElement.type = "button";
    confirmButtonElement.className = "SiteBrushConfirmButton";
    confirmButtonElement.textContent = confirmYesLabel;
    const cancelButtonElement = document.createElement("button");
    cancelButtonElement.type = "button";
    cancelButtonElement.className = "SiteBrushCancelButton";
    cancelButtonElement.textContent = confirmNoLabel;
    actionRowElement.appendChild(confirmButtonElement);
    actionRowElement.appendChild(cancelButtonElement);
    modalElement.appendChild(actionRowElement);
    overlayElement.appendChild(modalElement);
    document.body.appendChild(overlayElement);
    function closeDialog() { overlayElement.remove(); }
    cancelButtonElement.addEventListener("click", closeDialog);
    confirmButtonElement.addEventListener("click", function onConfirmClick() { closeDialog(); onConfirm(); });
  }
  function randomSitebrushToken() {
    return Date.now().toString(36) + "-" + Math.random().toString(36).slice(2, 12);
  }
  function labelForPublishStage(stageName) {
    if (stageName === "preparing") { return "` + publishProgressPreparingLabel + `"; }
    if (stageName === "pages") { return "` + publishProgressPagesLabel + `"; }
    if (stageName === "pack") { return "` + publishProgressPackLabel + `"; }
    if (stageName === "unfreeze") { return "` + publishProgressUnfreezeLabel + `"; }
    if (stageName === "done") { return "` + publishProgressDoneLabel + `"; }
    if (stageName === "error") { return "` + publishProgressFailedLabel + `"; }
    return "` + publishProgressPreparingLabel + `";
  }
  function openPublishProgressDialog(progressToken, readyCallback) {
    closeSitebrushMenu();
    const overlayElement = document.createElement("div");
    overlayElement.className = "SiteBrushConfirmOverlay";
    const modalElement = document.createElement("div");
    modalElement.className = "SiteBrushConfirmModal SiteBrushPublishProgressModal";
    const textElement = document.createElement("p");
    textElement.className = "SiteBrushConfirmText";
    textElement.textContent = "` + publishProgressPreparingLabel + `";
    const pathElement = document.createElement("div");
    pathElement.className = "SiteBrushPublishProgressPath";
    const progressElement = document.createElement("div");
    progressElement.className = "SiteBrushPublishProgress";
    const progressBarElement = document.createElement("div");
    progressBarElement.className = "SiteBrushPublishProgressBar";
    progressBarElement.textContent = "0%";
    progressElement.appendChild(progressBarElement);
    modalElement.appendChild(textElement);
    modalElement.appendChild(pathElement);
    modalElement.appendChild(progressElement);
    overlayElement.appendChild(modalElement);
    document.body.appendChild(overlayElement);
    let readyCallbackWasCalled = false;
    function setProgressPercent(percentNumber) {
      const boundedPercent = Math.max(0, Math.min(100, Math.round(Number(percentNumber) || 0)));
      progressBarElement.style.width = boundedPercent + "%";
      progressBarElement.textContent = boundedPercent + "%";
    }
    const progressStream = new EventSource(currentPagePath + "?publish_events&token=" + encodeURIComponent(progressToken));
    progressStream.onmessage = function onPublishProgressMessage(messageEvent) {
      let progressState = null;
      try {
        progressState = JSON.parse(messageEvent.data);
      } catch (parseError) {
        return;
      }
      if (progressState.stage === "ready") {
        if (!readyCallbackWasCalled) {
          readyCallbackWasCalled = true;
          readyCallback();
        }
        return;
      }
      setProgressPercent(progressState.completed_percent);
      const totalCount = Number(progressState.total) || 0;
      const completedCount = Number(progressState.completed) || 0;
      const remainingCount = Math.max(totalCount - completedCount, 0);
      const remainingPercent = Math.max(0, 100 - (Number(progressState.completed_percent) || 0));
      textElement.textContent = labelForPublishStage(progressState.stage) + " " + completedCount + " / " + totalCount + ". " + "` + publishProgressRemainingLabel + `" + " " + remainingCount + " (" + remainingPercent + "%).";
      pathElement.textContent = progressState.current_path || "";
      if (progressState.stage === "done") {
        textElement.textContent = "` + publishProgressDoneLabel + `";
        setProgressPercent(100);
        progressStream.close();
      }
      if (progressState.stage === "error") {
        textElement.textContent = "` + publishProgressFailedLabel + `";
        progressStream.close();
      }
    };
    progressStream.onerror = function onPublishProgressError() {
      textElement.textContent = "` + publishProgressPreparingLabel + `";
    };
    window.setTimeout(function submitPublishIfStreamReadyWasMissed() {
      if (readyCallbackWasCalled) {
        return;
      }
      readyCallbackWasCalled = true;
      readyCallback();
    }, 750);
  }
  document.addEventListener("click", function onActionClick(browserEvent) {
    const actionButtonElement = closestSitebrushEventElement(browserEvent, "[data-sitebrush-action]");
    if (!actionButtonElement) {
      return;
    }
    browserEvent.preventDefault();
    const actionName = actionButtonElement.getAttribute("data-sitebrush-action");
    if (actionName === "tree") {
      openSiteTreeDialog();
      return;
    }
    if (actionName === "protect_password") {
      const passwordText = window.prompt("` + protectPasswordPrompt + `");
      if (passwordText === null) {
        return;
      }
      if (passwordText.trim() === "") {
        window.alert("` + protectPasswordEmptyLabel + `");
        return;
      }
      submitPasswordActionForm("?page_password=protect", passwordText);
      return;
    }
    const selectedActionConfig = actionConfigByName[actionName];
    if (!selectedActionConfig) {
      return;
    }
    if (actionName === "publish") {
      fetch(currentPagePath + "?publish_preview", { headers: { "Accept": "application/json" } })
        .then(function parsePublishPreview(previewResponse) {
          if (!previewResponse.ok) { throw new Error("publish preview failed"); }
          return previewResponse.json();
        })
	        .then(function confirmPublishWithPreview(previewPayload) {
		          let summaryText = "` + publishPreviewSummaryLabel + `" + " " + previewPayload.changed + " / " + previewPayload.total;
		          let confirmQuestionText = "` + publishConfirmWithChangesLabel + `";
		          if (previewPayload.changed === 0) {
		            summaryText = "";
		            confirmQuestionText = "` + publishConfirmWithoutChangesLabel + `";
		          }
		          const dialogMessage = summaryText === "" ? confirmQuestionText : confirmQuestionText + "\n\n" + summaryText;
		          openPublishConfirmationDialog(dialogMessage, previewPayload.paths || [], submitConfirmedAction);
		        })
        .catch(function fallbackPublishConfirmation() {
          openConfirmationDialog(selectedActionConfig.message + "\n\n" + "` + publishPreviewLoadingLabel + `", submitConfirmedAction);
        });
      return;
    }
    openConfirmationDialog(selectedActionConfig.message, submitConfirmedAction);
    function submitConfirmedAction() {
      if (actionName === "publish") {
        const publishToken = randomSitebrushToken();
        openPublishProgressDialog(publishToken, function submitPublishAfterProgressReady() {
          submitActionForm(publishToken);
        });
        return;
      }
      submitActionForm("");
    }
    function submitActionForm(publishToken) {
      const actionFormElement = document.createElement("form");
      actionFormElement.setAttribute("data-sitebrush-owned", "true");
      actionFormElement.method = "POST";
      actionFormElement.action = selectedActionConfig.path;
      if (publishToken !== "") {
        const tokenInputElement = document.createElement("input");
        tokenInputElement.type = "hidden";
        tokenInputElement.name = "publish_token";
        tokenInputElement.value = publishToken;
        actionFormElement.appendChild(tokenInputElement);
      }
      document.body.appendChild(actionFormElement);
      actionFormElement.submit();
    }
    function submitPasswordActionForm(actionPath, passwordText) {
      const actionFormElement = document.createElement("form");
      actionFormElement.setAttribute("data-sitebrush-owned", "true");
      actionFormElement.method = "POST";
      actionFormElement.action = actionPath;
      const passwordInputElement = document.createElement("input");
      passwordInputElement.type = "hidden";
      passwordInputElement.name = "password";
      passwordInputElement.value = passwordText;
      actionFormElement.appendChild(passwordInputElement);
      document.body.appendChild(actionFormElement);
      actionFormElement.submit();
    }
  }, {capture: true});
  function buildSitebrushAdminMenuEntries() {
    return [
      "<ul class='SiteBrushMenuList'>",
      "<li class='SiteBrushContextMenu SiteBrushDomainMenuItem'><a href='/' class='SiteBrushContextMenuLink'>" + currentDomainName + "</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?visual' class='SiteBrushContextMenuLink'><img src='/p/static/pencil.png' class='SiteBrushMenuIcon' alt=''>" + "` + editLabel + `" + "</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?text' class='SiteBrushContextMenuLink'><img src='/p/static/pencil-text.png' class='SiteBrushMenuIcon' alt=''>" + "` + textEditLabel + `" + "</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?revisions' class='SiteBrushContextMenuLink'><img src='/p/static/revisions.png' class='SiteBrushMenuIcon' alt=''>" + "` + revisionsLabel + `" + "</a></li>",
      "` + deleteActionEntry + `",
      "` + passwordActionEntry + `",
      "<li class='SiteBrushContextMenu'><a href='?files' class='SiteBrushContextMenuLink'><img src='/p/static/upload.png' class='SiteBrushMenuIcon' alt=''>" + "` + filesLabel + `" + "</a></li>",
      "<li class='SiteBrushContextMenu'><button type='button' data-sitebrush-action='tree' class='SiteBrushContextMenuLink SiteBrushContextMenuButton'><img src='/p/static/tree.png' class='SiteBrushMenuIcon' alt=''>" + "` + treeLabel + `" + "</button></li>",
      "` + freezeActionEntry + `",
      "<li class='SiteBrushContextMenu'><a href='?analytics' class='SiteBrushContextMenuLink'><img src='/p/static/analytics.svg' class='SiteBrushMenuIcon' alt=''>" + "` + analyticsLabel + `" + "</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?settings' class='SiteBrushContextMenuLink'><img src='/p/static/settings.png' class='SiteBrushMenuIcon' alt=''>" + "` + settingsLabel + `" + "</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?profile' class='SiteBrushContextMenuLink'><img src='/p/static/profile.png' class='SiteBrushMenuIcon' alt=''>" + "` + profileLabel + `" + "</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?logout' class='SiteBrushContextMenuLink'><img src='/p/static/sign-out.png' class='SiteBrushMenuIcon' alt=''>" + "` + logoutLabel + `" + "</a></li>",
      "` + copyrightMenuEntry + `",
      "</ul>"
    ];
  }
  function onContextMenuOpen(browserEvent) {
    if (browserEvent.__sitebrushContextMenuHandled || browserEvent.ctrlKey) {
      return;
    }
    if (sitebrushShouldIgnoreContextMenuEvent(browserEvent)) {
      return;
    }
    const clickedInsideSitebrushMenu = closestSitebrushEventElement(browserEvent, "#SiteBrushMenuBox");
    if (clickedInsideSitebrushMenu) {
      return;
    }
    browserEvent.__sitebrushContextMenuHandled = true;
    browserEvent.preventDefault();
    browserEvent.stopPropagation();
    showSitebrushMenu(browserEvent, buildSitebrushAdminMenuEntries(), currentPagePath, isDomainFrozen);
  }
  window.addEventListener("contextmenu", onContextMenuOpen, {capture: true, passive: false});
  document.addEventListener("contextmenu", onContextMenuOpen, {capture: true, passive: false});
  installSitebrushLongPressMenu(function openAdminMenuFromLongPress(menuPoint) {
    showSitebrushMenu(menuPoint, buildSitebrushAdminMenuEntries(), currentPagePath, isDomainFrozen);
  });
  function openSiteTreeDialog() {
    closeSitebrushMenu();
    const overlayElement = document.createElement("div");
    overlayElement.className = "SiteBrushTreeOverlay";
    const modalElement = document.createElement("div");
    modalElement.className = "SiteBrushTreeModal";
    const cornerIconElement = document.createElement("img");
    cornerIconElement.className = "SiteBrushTreeCornerIcon";
    cornerIconElement.src = "/p/static/tree.png";
    cornerIconElement.alt = "";
    const titleElement = document.createElement("h3");
    titleElement.className = "SiteBrushTreeTitle";
    titleElement.textContent = "` + treeModalTitle + `";
    const contentElement = document.createElement("div");
    contentElement.className = "SiteBrushTreeContent";
    contentElement.textContent = "` + treeLoadingLabel + `";
    const closeButtonElement = document.createElement("button");
    closeButtonElement.type = "button";
    closeButtonElement.className = "SiteBrushTreeCloseButton";
    closeButtonElement.textContent = "` + treeCloseLabel + `";
    modalElement.appendChild(cornerIconElement);
    modalElement.appendChild(titleElement);
    modalElement.appendChild(contentElement);
    modalElement.appendChild(closeButtonElement);
    overlayElement.appendChild(modalElement);
    document.body.appendChild(overlayElement);
    function closeTreeDialog() { overlayElement.remove(); }
    closeButtonElement.addEventListener("click", closeTreeDialog);
    overlayElement.addEventListener("click", function closeTreeDialogOnOverlay(browserEvent) {
      if (browserEvent.target === overlayElement) {
        closeTreeDialog();
      }
    });
    fetch(currentPagePath + "?tree", { headers: { "Accept": "application/json" } })
      .then(function parseTreeResponse(treeResponse) {
        if (!treeResponse.ok) { throw new Error("tree request failed"); }
        return treeResponse.json();
      })
      .then(function renderTree(treeData) { renderSiteTree(contentElement, treeData); })
      .catch(function showTreeError() { contentElement.textContent = "` + treeLoadErrorLabel + `"; });
  }
})();
	</script>`
	}
	return guestContextMenuStylesAndHelpers() + `<script>
(function initializeSitebrushContextMenuForGuests() {
  if (window.__sitebrushContextMenuInitialized) {
    return;
  }
  window.__sitebrushContextMenuInitialized = true;
  const currentPagePath = "` + escapedPath + `";
  const currentDomainName = "` + escapedDomain + `";
  function buildSitebrushGuestMenuEntries() {
    return [
      "<ul class='SiteBrushMenuList'>",
      "<li class='SiteBrushContextMenu SiteBrushDomainMenuItem'><a href='/' class='SiteBrushContextMenuLink'>" + currentDomainName + "</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?login' class='SiteBrushContextMenuLink'><img src='/p/static/login.png' class='SiteBrushMenuIcon' alt=''>" + "` + loginLabel + `" + "</a></li>",
      "` + copyrightMenuEntry + `",
      "</ul>"
    ];
  }
  function onContextMenuOpen(browserEvent) {
    if (browserEvent.__sitebrushContextMenuHandled || browserEvent.ctrlKey) {
      return;
    }
    if (sitebrushShouldIgnoreContextMenuEvent(browserEvent)) {
      return;
    }
    const clickedInsideSitebrushMenu = closestSitebrushEventElement(browserEvent, "#SiteBrushMenuBox");
    if (clickedInsideSitebrushMenu) {
      return;
    }
    browserEvent.__sitebrushContextMenuHandled = true;
    browserEvent.preventDefault();
    browserEvent.stopPropagation();
    showSitebrushMenu(browserEvent, buildSitebrushGuestMenuEntries(), currentPagePath, false);
  }
  window.addEventListener("contextmenu", onContextMenuOpen, {capture: true, passive: false});
  document.addEventListener("contextmenu", onContextMenuOpen, {capture: true, passive: false});
  installSitebrushLongPressMenu(function openGuestMenuFromLongPress(menuPoint) {
    showSitebrushMenu(menuPoint, buildSitebrushGuestMenuEntries(), currentPagePath, false);
  });
	})();
		</script>`
}

func buildGuestContextMenuScript(pagePath, domain string, translations map[string]string) string {
	menuTemplate := buildGuestContextMenuScriptTemplate(translations)
	return strings.NewReplacer(
		guestContextMenuPagePathPlaceholder, template.JSEscapeString(pagePath),
		guestContextMenuDomainPlaceholder, template.JSEscapeString(domain),
	).Replace(menuTemplate)
}

func buildGuestContextMenuScriptForLanguage(pagePath, domain, languageCode string) string {
	menuTemplate := guestContextMenuTemplates[languageCode]
	if strings.TrimSpace(menuTemplate) == "" {
		menuTemplate = guestContextMenuTemplates["ru"]
	}
	if strings.TrimSpace(menuTemplate) == "" {
		menuTemplate = buildGuestContextMenuScriptTemplate(translationsForLanguageCode("ru"))
	}
	return strings.NewReplacer(
		guestContextMenuPagePathPlaceholder, template.JSEscapeString(pagePath),
		guestContextMenuDomainPlaceholder, template.JSEscapeString(domain),
	).Replace(menuTemplate)
}

func buildGuestNotFoundPageTemplates() map[string]string {
	templates := make(map[string]string, len(translationCatalog))
	for languageCode := range translationCatalog {
		templates[languageCode] = buildGuestNotFoundPageTemplate(languageCode, translationsForLanguageCode(languageCode))
	}
	if strings.TrimSpace(templates["ru"]) == "" {
		templates["ru"] = buildGuestNotFoundPageTemplate("ru", translationsForLanguageCode("ru"))
	}
	return templates
}

func buildGuestNotFoundPageForLanguage(pagePath, domain, languageCode string) string {
	pageTemplate := guestNotFoundPageTemplates[languageCode]
	if strings.TrimSpace(pageTemplate) == "" {
		pageTemplate = guestNotFoundPageTemplates["ru"]
	}
	if strings.TrimSpace(pageTemplate) == "" {
		pageTemplate = buildGuestNotFoundPageTemplate("ru", translationsForLanguageCode("ru"))
	}
	normalizedPath := cleanPath(pagePath)
	if strings.TrimSpace(normalizedPath) == "" {
		normalizedPath = "/"
	}
	return strings.NewReplacer(
		guestNotFoundPagePathPlaceholder, template.HTMLEscapeString(normalizedPath),
		guestNotFoundEditLinkPlaceholder, template.HTMLEscapeString(normalizedPath+"?edit"),
		guestNotFoundMenuPlaceholder, buildGuestContextMenuScriptForLanguage(normalizedPath, domain, languageCode),
	).Replace(pageTemplate)
}

func buildGuestNotFoundPageTemplate(languageCode string, translations map[string]string) string {
	pageTitle := template.HTMLEscapeString(translationOrDefault(translations, "missing_title", "Page not found"))
	notFoundPrefix := template.HTMLEscapeString(translationOrDefault(translations, "missing_page_not_found_prefix", "Page"))
	notFoundSuffix := template.HTMLEscapeString(translationOrDefault(translations, "missing_page_not_found_suffix", "not found. Please"))
	createPageLabel := template.HTMLEscapeString(translationOrDefault(translations, "missing_create_page", "create this page"))
	escapedLanguageCode := template.HTMLEscapeString(languageCode)

	return `<!doctype html>
<html lang="` + escapedLanguageCode + `">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>` + pageTitle + `</title>
  <style>
    :root{color-scheme:light dark;--guest-bg:#f4f8fc;--guest-text:#1f3f6f;--guest-link:#1a65d8}
    @media (prefers-color-scheme:dark){:root{--guest-bg:#0f1724;--guest-text:#dbe8ff;--guest-link:#9ec2ff}}
    html,body{min-height:100%}
    body{margin:0;background:var(--guest-bg);color:var(--guest-text);font-family:Arial,Helvetica,sans-serif}
    .SiteBrushGuestNotFoundShell{min-height:100vh;display:flex;align-items:center;justify-content:center;padding:24px 16px;box-sizing:border-box}
    .SiteBrushGuestNotFoundContent{width:min(720px,100%);text-align:left}
    h1{margin:0 0 12px 0;font-size:clamp(28px,6vw,52px);line-height:1.05;overflow-wrap:anywhere}
    .SiteBrushGuestNotFoundText{margin:0;font-size:15px;line-height:1.45;overflow-wrap:anywhere}
    .SiteBrushGuestNotFoundText strong{font-weight:700}
    .SiteBrushGuestNotFoundText a{color:var(--guest-link);font-weight:700;text-decoration:none}
    .SiteBrushGuestNotFoundText a:hover{text-decoration:underline}
    @media (max-width:640px){.SiteBrushGuestNotFoundShell{padding:18px 12px}.SiteBrushGuestNotFoundText{font-size:13px;line-height:1.36}}
  </style>
</head>
<body>
  <main class="SiteBrushGuestNotFoundShell">
    <section class="SiteBrushGuestNotFoundContent">
      <h1>404: ` + guestNotFoundPagePathPlaceholder + `</h1>
      <p class="SiteBrushGuestNotFoundText">` + notFoundPrefix + ` <strong>` + guestNotFoundPagePathPlaceholder + `</strong> ` + notFoundSuffix + ` <a href="` + guestNotFoundEditLinkPlaceholder + `">` + createPageLabel + `</a>.</p>
    </section>
  </main>
  ` + guestNotFoundMenuPlaceholder + `
</body>
</html>`
}

func buildGuestContextMenuScriptTemplate(translations map[string]string) string {
	loginLabel := template.JSEscapeString(translationOrDefault(translations, "menu_login", "Sign in"))
	compiledVersionLabel := template.JSEscapeString("v." + CompileVersion)
	sitebrushHomeURL := "https://sitebrush.com"
	serverBinaryDownloadURL := latestServerBinaryDownloadURL(runtime.GOOS, runtime.GOARCH)
	copyrightMenuEntry := fmt.Sprintf("<li class='SiteBrushContextMenu ContextMenuCopyright'><div class='SiteBrushContextMenuFooter'><a href='%s' class='SiteBrushContextMenuFooterLink' target='_blank' rel='noopener noreferrer'>sitebrush</a><a href='%s' class='SiteBrushContextMenuVersion' download>%s</a></div></li>", sitebrushHomeURL, serverBinaryDownloadURL, compiledVersionLabel)
	return guestContextMenuStylesAndHelpers() + `<script>
(function initializeSitebrushContextMenuForGuests() {
  if (window.__sitebrushContextMenuInitialized) {
    return;
  }
  window.__sitebrushContextMenuInitialized = true;
  const currentPagePath = "` + guestContextMenuPagePathPlaceholder + `";
  const currentDomainName = "` + guestContextMenuDomainPlaceholder + `";
  function buildSitebrushGuestMenuEntries() {
    return [
      "<ul class='SiteBrushMenuList'>",
      "<li class='SiteBrushContextMenu SiteBrushDomainMenuItem'><a href='/' class='SiteBrushContextMenuLink'>" + currentDomainName + "</a></li>",
      "<li class='SiteBrushContextMenu'><a href='?login' class='SiteBrushContextMenuLink'><img src='/p/static/login.png' class='SiteBrushMenuIcon' alt=''>" + "` + loginLabel + `" + "</a></li>",
      "` + copyrightMenuEntry + `",
      "</ul>"
    ];
  }
  function onContextMenuOpen(browserEvent) {
    if (browserEvent.__sitebrushContextMenuHandled || browserEvent.ctrlKey) {
      return;
    }
    if (sitebrushShouldIgnoreContextMenuEvent(browserEvent)) {
      return;
    }
    const clickedInsideSitebrushMenu = closestSitebrushEventElement(browserEvent, "#SiteBrushMenuBox");
    if (clickedInsideSitebrushMenu) {
      return;
    }
    browserEvent.__sitebrushContextMenuHandled = true;
    browserEvent.preventDefault();
    browserEvent.stopPropagation();
    showSitebrushMenu(browserEvent, buildSitebrushGuestMenuEntries(), currentPagePath, false);
  }
  window.addEventListener("contextmenu", onContextMenuOpen, {capture: true, passive: false});
  document.addEventListener("contextmenu", onContextMenuOpen, {capture: true, passive: false});
  installSitebrushLongPressMenu(function openGuestMenuFromLongPress(menuPoint) {
    showSitebrushMenu(menuPoint, buildSitebrushGuestMenuEntries(), currentPagePath, false);
  });
})();
	</script>`
}

func latestServerBinaryDownloadURL(goos, goarch string) string {
	fileName := "sitebrush_" + strings.TrimSpace(goos) + "_" + strings.TrimSpace(goarch)
	if goos == "windows" {
		fileName += ".exe"
	}
	return "https://files.zabiyaka.net/sitebrush/latest/server-app/" + fileName
}

func guestContextMenuStylesAndHelpers() string {
	return `<style>
.SiteBrushMenuBox,.SiteBrushMenuBox *{all:initial;box-sizing:border-box}
.SiteBrushMenuBox{position:fixed;background:#fff url(/p/static/bg.png) repeat-x top;border:1px solid #8ea4c1;z-index:2147483646;padding:2px;min-width:min(240px,calc(100vw - 16px));max-width:calc(100vw - 16px);max-height:calc(100vh - 16px);overflow:auto;box-shadow:0 2px 12px rgba(0,0,0,0.2);font-family:Arial,Helvetica,sans-serif;touch-action:manipulation}
.SiteBrushMenuList{list-style:none;margin:0;padding:0}
.SiteBrushContextMenu{margin:0;padding:0}
.SiteBrushContextMenuLink{display:flex;align-items:center;gap:8px;padding:8px 10px;color:#1f3f6f;text-decoration:none;font-family:Arial,Helvetica,sans-serif;font-size:14px;line-height:1.25;cursor:pointer;min-width:0;white-space:normal;word-break:break-word}
.SiteBrushContextMenuLink:link,.SiteBrushContextMenuLink:visited,.SiteBrushContextMenuLink:active{color:#1f3f6f;text-decoration:none}
.SiteBrushContextMenuLink:hover{color:#1f3f6f;background:#eef5ff;text-decoration:none}
.SiteBrushDomainMenuItem .SiteBrushContextMenuLink{font-weight:700;border-bottom:1px solid #c8d5e7}
.SiteBrushContextMenuFooter{display:flex;align-items:center;justify-content:space-between;gap:12px;border-top:1px solid #c8d5e7;margin-top:2px;padding:7px 10px 8px 10px;font-family:Arial,Helvetica,sans-serif;font-size:12px;color:#5b6f8b}.SiteBrushMenuIcon{width:18px;height:18px;flex:0 0 18px}
.SiteBrushContextMenuFooterLink,.SiteBrushContextMenuVersion{color:#5b6f8b;text-decoration:none;font-family:Arial,Helvetica,sans-serif;font-size:12px;cursor:pointer}
.SiteBrushContextMenuFooterLink:link,.SiteBrushContextMenuFooterLink:visited,.SiteBrushContextMenuFooterLink:active,.SiteBrushContextMenuFooterLink:hover,.SiteBrushContextMenuVersion:link,.SiteBrushContextMenuVersion:visited,.SiteBrushContextMenuVersion:active,.SiteBrushContextMenuVersion:hover{color:#5b6f8b;text-decoration:none}
.SiteBrushContextMenuVersion{font-weight:700}
@media (pointer: coarse), (max-width: 820px){
  .SiteBrushMenuBox{right:auto;min-width:min(220px,calc(100vw - 12px));max-width:calc(100vw - 12px);max-height:calc(100vh - 12px);border-radius:7px;padding:1px;-webkit-overflow-scrolling:touch}
  .SiteBrushContextMenuLink{min-height:34px;padding:7px 9px;font-size:13px;gap:7px}
  .SiteBrushMenuIcon{width:16px;height:16px;flex-basis:16px}
  .SiteBrushContextMenuFooter{font-size:11px;flex-wrap:wrap;padding:6px 8px 7px 8px;gap:8px}
  .SiteBrushContextMenuFooterLink,.SiteBrushContextMenuVersion{font-size:11px}
}
@media (prefers-color-scheme: dark){
  .SiteBrushMenuBox{background:#172235;border-color:#2f405d}
  .SiteBrushContextMenuLink{color:#dbe8ff}
  .SiteBrushContextMenuLink:link,.SiteBrushContextMenuLink:visited,.SiteBrushContextMenuLink:active{color:#dbe8ff}
  .SiteBrushContextMenuLink:hover{color:#dbe8ff;background:#24344d}
  .SiteBrushDomainMenuItem .SiteBrushContextMenuLink{border-bottom-color:#2f405d}
  .SiteBrushContextMenuFooter{color:#a7bbd8;border-top-color:#2f405d}
  .SiteBrushContextMenuFooterLink,.SiteBrushContextMenuVersion{color:#a7bbd8}
  .SiteBrushContextMenuFooterLink:link,.SiteBrushContextMenuFooterLink:visited,.SiteBrushContextMenuFooterLink:active,.SiteBrushContextMenuFooterLink:hover,.SiteBrushContextMenuVersion:link,.SiteBrushContextMenuVersion:visited,.SiteBrushContextMenuVersion:active,.SiteBrushContextMenuVersion:hover{color:#a7bbd8}
}
</style>
<script>
const sitebrushContextMenuShadowCSS = document.currentScript && document.currentScript.previousElementSibling ? document.currentScript.previousElementSibling.textContent : "";
function closestSitebrushEventElement(browserEvent, selector) {
  const directTarget = browserEvent.target;
  if (directTarget && directTarget.closest) {
    const directMatch = directTarget.closest(selector);
    if (directMatch) {
      return directMatch;
    }
  }
  if (!browserEvent.composedPath) {
    return null;
  }
  for (const pathNode of browserEvent.composedPath()) {
    if (!pathNode || pathNode.nodeType !== Node.ELEMENT_NODE) {
      continue;
    }
    if (pathNode.matches && pathNode.matches(selector)) {
      return pathNode;
    }
    if (pathNode.closest) {
      const pathMatch = pathNode.closest(selector);
      if (pathMatch) {
        return pathMatch;
      }
    }
  }
  return null;
}
function closeSitebrushMenu() {
  const existingMenuBox = document.getElementById("SiteBrushMenuBox");
  if (existingMenuBox) {
    existingMenuBox.remove();
  }
}
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
function sitebrushMenuViewportNumber(candidateNumber, fallbackNumber) {
  const parsedNumber = Number(candidateNumber);
  if (Number.isFinite(parsedNumber) && parsedNumber > 0) {
    return parsedNumber;
  }
  return fallbackNumber;
}
function sitebrushMenuPointFromEvent(browserEvent) {
  const viewportWidth = sitebrushMenuViewportNumber(window.innerWidth, document.documentElement.clientWidth || 320);
  const viewportHeight = sitebrushMenuViewportNumber(window.innerHeight, document.documentElement.clientHeight || 480);
  return {
    clientX: Math.max(0, Math.min(Number(browserEvent.clientX) || 0, viewportWidth)),
    clientY: Math.max(0, Math.min(Number(browserEvent.clientY) || 0, viewportHeight))
  };
}
function positionSitebrushMenuBox(menuBoxElement, menuPoint) {
  const viewportWidth = sitebrushMenuViewportNumber(window.innerWidth, document.documentElement.clientWidth || 320);
  const viewportHeight = sitebrushMenuViewportNumber(window.innerHeight, document.documentElement.clientHeight || 480);
  const viewportGap = 8;
  const menuRect = menuBoxElement.getBoundingClientRect();
  const menuWidth = Math.min(menuRect.width || 0, viewportWidth - viewportGap * 2);
  const menuHeight = Math.min(menuRect.height || 0, viewportHeight - viewportGap * 2);
  const boundedLeft = Math.max(viewportGap, Math.min(menuPoint.clientX, viewportWidth - menuWidth - viewportGap));
  const boundedTop = Math.max(viewportGap, Math.min(menuPoint.clientY, viewportHeight - menuHeight - viewportGap));
  menuBoxElement.style.left = boundedLeft + "px";
  menuBoxElement.style.top = boundedTop + "px";
}
function sitebrushShouldIgnoreContextMenuEvent(browserEvent) {
  if (!window.__sitebrushLongPressMenuUntil) {
    return false;
  }
  if (Date.now() > window.__sitebrushLongPressMenuUntil) {
    window.__sitebrushLongPressMenuUntil = 0;
    return false;
  }
  if (document.getElementById("SiteBrushMenuBox")) {
    browserEvent.preventDefault();
    browserEvent.stopPropagation();
    return true;
  }
  return false;
}
function installSitebrushLongPressMenu(openMenuAtPoint) {
  const longPressDelayMilliseconds = 650;
  const moveTolerancePixels = 12;
  let longPressTimer = 0;
  let longPressStartPoint = null;
  let longPressPointerID = null;
  function clearLongPressTimer() {
    if (longPressTimer) {
      window.clearTimeout(longPressTimer);
      longPressTimer = 0;
    }
  }
  function cancelLongPress() {
    clearLongPressTimer();
    longPressStartPoint = null;
    longPressPointerID = null;
  }
  function isTouchLikePointerEvent(browserEvent) {
    return browserEvent.pointerType === "touch" || browserEvent.pointerType === "pen";
  }
  function startLongPress(browserEvent) {
    if (closestSitebrushEventElement(browserEvent, "#SiteBrushMenuBox")) {
      return;
    }
    if (browserEvent.pointerType && !isTouchLikePointerEvent(browserEvent)) {
      return;
    }
    if (browserEvent.button && browserEvent.button !== 0) {
      return;
    }
    cancelLongPress();
    longPressStartPoint = sitebrushMenuPointFromEvent(browserEvent);
    longPressPointerID = browserEvent.pointerId || null;
    longPressTimer = window.setTimeout(function openMenuFromLongPress() {
      const menuPoint = longPressStartPoint;
      cancelLongPress();
      if (!menuPoint) {
        return;
      }
      window.__sitebrushLongPressMenuUntil = Date.now() + 1000;
      openMenuAtPoint(menuPoint);
    }, longPressDelayMilliseconds);
  }
  function moveLongPress(browserEvent) {
    if (!longPressStartPoint) {
      return;
    }
    if (longPressPointerID !== null && browserEvent.pointerId && browserEvent.pointerId !== longPressPointerID) {
      return;
    }
    const currentPoint = sitebrushMenuPointFromEvent(browserEvent);
    const movedX = Math.abs(currentPoint.clientX - longPressStartPoint.clientX);
    const movedY = Math.abs(currentPoint.clientY - longPressStartPoint.clientY);
    if (movedX > moveTolerancePixels || movedY > moveTolerancePixels) {
      cancelLongPress();
    }
  }
  function blockSyntheticClickAfterLongPress(browserEvent) {
    if (!window.__sitebrushLongPressMenuUntil || Date.now() > window.__sitebrushLongPressMenuUntil) {
      return;
    }
    if (closestSitebrushEventElement(browserEvent, "#SiteBrushMenuBox")) {
      return;
    }
    browserEvent.preventDefault();
    browserEvent.stopPropagation();
  }
  if (window.PointerEvent) {
    document.addEventListener("pointerdown", startLongPress, {capture: true, passive: true});
    document.addEventListener("pointermove", moveLongPress, {capture: true, passive: true});
    document.addEventListener("pointerup", cancelLongPress, {capture: true, passive: true});
    document.addEventListener("pointercancel", cancelLongPress, {capture: true, passive: true});
  } else {
    document.addEventListener("touchstart", function onTouchStart(browserEvent) {
      if (browserEvent.touches.length !== 1) {
        cancelLongPress();
        return;
      }
      startLongPress(browserEvent.touches[0]);
    }, {capture: true, passive: true});
    document.addEventListener("touchmove", function onTouchMove(browserEvent) {
      if (browserEvent.touches.length !== 1) {
        cancelLongPress();
        return;
      }
      moveLongPress(browserEvent.touches[0]);
    }, {capture: true, passive: true});
    document.addEventListener("touchend", cancelLongPress, {capture: true, passive: true});
    document.addEventListener("touchcancel", cancelLongPress, {capture: true, passive: true});
  }
  document.addEventListener("click", blockSyntheticClickAfterLongPress, {capture: true, passive: false});
}
function showSitebrushMenu(browserEvent, menuHtmlEntries, currentPagePath, frozenMenuEnabled) {
  closeSitebrushMenu();
  const menuPoint = sitebrushMenuPointFromEvent(browserEvent);
  const menuHostElement = document.createElement("div");
  menuHostElement.id = "SiteBrushMenuBox";
  menuHostElement.setAttribute("data-sitebrush-owned", "true");
  menuHostElement.style.setProperty("all", "initial", "important");
  menuHostElement.style.setProperty("position", "fixed", "important");
  menuHostElement.style.setProperty("left", "0", "important");
  menuHostElement.style.setProperty("top", "0", "important");
  menuHostElement.style.setProperty("z-index", "2147483646", "important");
  const menuRoot = menuHostElement.attachShadow ? menuHostElement.attachShadow({mode: "open"}) : menuHostElement;
  const menuStyleElement = document.createElement("style");
  menuStyleElement.setAttribute("data-sitebrush-owned", "true");
  menuStyleElement.textContent = sitebrushContextMenuShadowCSS;
  const menuBoxElement = document.createElement("div");
  menuBoxElement.className = "SiteBrushMenuBox";
  menuBoxElement.setAttribute("data-sitebrush-owned", "true");
  if (frozenMenuEnabled) {
    menuBoxElement.classList.add("SiteBrushMenuBoxFrozen");
  }
  menuBoxElement.innerHTML = menuHtmlEntries.join("");
  normalizeSitebrushMenuLinks(menuBoxElement, currentPagePath);
  menuBoxElement.style.left = menuPoint.clientX + "px";
  menuBoxElement.style.top = menuPoint.clientY + "px";
  menuRoot.appendChild(menuStyleElement);
  menuRoot.appendChild(menuBoxElement);
  document.body.appendChild(menuHostElement);
  positionSitebrushMenuBox(menuBoxElement, menuPoint);
  menuBoxElement.addEventListener("click", function onMenuClick(browserEvent) {
    const menuLinkElement = browserEvent.target && browserEvent.target.closest && browserEvent.target.closest("a[href]");
    if (!menuLinkElement) {
      return;
    }
    const targetHref = menuLinkElement.getAttribute("href");
    if (!targetHref) {
      return;
    }
    browserEvent.preventDefault();
    browserEvent.stopPropagation();
    window.location.href = targetHref;
  });
  document.addEventListener("click", function closeMenuOnClick(browserEvent) {
    const openMenu = document.getElementById("SiteBrushMenuBox");
    if (!openMenu) {
      return;
    }
    if (closestSitebrushEventElement(browserEvent, "#SiteBrushMenuBox")) {
      return;
    }
    openMenu.remove();
  }, { once: true });
}
</script>`
}

func contextMenuStylesAndHelpers() string {
	return `<style>
.SiteBrushMenuBox,.SiteBrushMenuBox *{all:initial;box-sizing:border-box}
.SiteBrushMenuBox{position:fixed;background:#fff url(/p/static/bg.png) repeat-x top;border:1px solid #8ea4c1;z-index:2147483646;padding:2px;min-width:min(240px,calc(100vw - 16px));max-width:calc(100vw - 16px);max-height:calc(100vh - 16px);overflow:auto;box-shadow:0 2px 12px rgba(0,0,0,0.2);font-family:Arial,Helvetica,sans-serif;touch-action:manipulation}
.SiteBrushMenuBox.SiteBrushMenuBoxFrozen{background:#e9f5ff;border-color:#6da6d4}
.SiteBrushMenuList{list-style:none;margin:0;padding:0}
.SiteBrushContextMenu{margin:0;padding:0}
.SiteBrushContextMenuLink{display:flex;align-items:center;gap:8px;padding:8px 10px;color:#1f3f6f;text-decoration:none;font-family:Arial,Helvetica,sans-serif;font-size:14px;line-height:1.25;cursor:pointer;min-width:0;white-space:normal;word-break:break-word}
.SiteBrushContextMenuLink:link,.SiteBrushContextMenuLink:visited,.SiteBrushContextMenuLink:active{color:#1f3f6f;text-decoration:none}
.SiteBrushContextMenuLink:hover{color:#1f3f6f;background:#eef5ff;text-decoration:none}
.SiteBrushContextMenuButton{width:100%;border:0;background:transparent;text-align:left}
.SiteBrushDomainMenuItem .SiteBrushContextMenuLink{font-weight:700;border-bottom:1px solid #c8d5e7}
.SiteBrushContextMenuFooter{display:flex;align-items:center;justify-content:space-between;gap:12px;border-top:1px solid #c8d5e7;margin-top:2px;padding:7px 10px 8px 10px;font-family:Arial,Helvetica,sans-serif;font-size:12px;color:#5b6f8b}.SiteBrushMenuIcon{width:18px;height:18px;flex:0 0 18px}
.SiteBrushContextMenuFooterLink,.SiteBrushContextMenuVersion{color:#5b6f8b;text-decoration:none;font-family:Arial,Helvetica,sans-serif;font-size:12px;cursor:pointer}
.SiteBrushContextMenuFooterLink:link,.SiteBrushContextMenuFooterLink:visited,.SiteBrushContextMenuFooterLink:active,.SiteBrushContextMenuFooterLink:hover,.SiteBrushContextMenuVersion:link,.SiteBrushContextMenuVersion:visited,.SiteBrushContextMenuVersion:active,.SiteBrushContextMenuVersion:hover{color:#5b6f8b;text-decoration:none}
.SiteBrushContextMenuVersion{font-weight:700}
.SiteBrushMenuStorageUsage{margin-left:auto;font-variant-numeric:tabular-nums;white-space:nowrap}
.SiteBrushConfirmOverlay{position:fixed;inset:0;background:rgba(0,0,0,.45);display:flex;align-items:center;justify-content:center;z-index:2147483647}
.SiteBrushConfirmModal{background:#fff;border:1px solid #8ea4c1;min-width:260px;max-width:340px;padding:16px;font-family:Arial,Helvetica,sans-serif}
.SiteBrushConfirmText{margin:0 0 14px 0;color:#1f3f6f;font-size:14px}
.SiteBrushPublishPreviewList{list-style:none;margin:0 0 12px 0;padding:0;max-height:180px;overflow:auto}
.SiteBrushPublishPreviewListItem{margin:0 0 4px 0}
.SiteBrushPublishPreviewLink{color:#1f3f6f;text-decoration:underline;font-size:13px}
.SiteBrushPublishPreviewLink:link,.SiteBrushPublishPreviewLink:visited,.SiteBrushPublishPreviewLink:active,.SiteBrushPublishPreviewLink:hover{color:#1f3f6f;text-decoration:underline}
.SiteBrushPublishProgressModal{min-width:320px;max-width:460px}
.SiteBrushPublishProgressPath{min-height:18px;margin:0 0 8px 0;color:#5b6f8b;font-size:12px;word-break:break-word}
.SiteBrushPublishProgress{height:20px;background:#e8f0fb;border:1px solid #8ea4c1;overflow:hidden}
.SiteBrushPublishProgressBar{height:100%;width:0%;background:#3f7ecb;color:#fff;text-align:center;font-size:12px;line-height:20px;font-family:Arial,Helvetica,sans-serif;transition:width .18s ease}
.SiteBrushConfirmActions{display:flex;gap:8px;justify-content:flex-end}
.SiteBrushConfirmButton,.SiteBrushCancelButton{border:1px solid #8ea4c1;background:#f2f7ff;padding:6px 12px;cursor:pointer;font-size:13px}
.SiteBrushTreeOverlay{position:fixed;inset:0;background:rgba(0,0,0,.45);display:flex;align-items:center;justify-content:center;z-index:2147483647}
.SiteBrushTreeModal{position:relative;background:#fff;border:1px solid #8ea4c1;min-width:320px;max-width:700px;max-height:80vh;overflow:auto;padding:16px 16px 84px 16px;font-family:Arial,Helvetica,sans-serif}
.SiteBrushTreeCloseButton{display:block;margin:12px 0 0 16px;border:1px solid #8ea4c1;background:#f2f7ff;color:#1f3f6f;padding:6px 14px;cursor:pointer;font-size:13px}
.SiteBrushTreeCornerIcon{position:absolute;top:14px;right:14px;width:60px;height:60px;object-fit:contain;opacity:.95}
.SiteBrushTreeTitle{margin:0 72px 12px 0;color:#1f3f6f;font-size:18px}
.SiteBrushTreeContent{margin:0;color:#1f3f6f;font-size:14px}
.SiteBrushTreeList{list-style:none;margin:0;padding-left:16px}
.SiteBrushTreeLink{color:#1f3f6f;text-decoration:none;font-size:14px;line-height:1.6}
.SiteBrushTreeLink:link,.SiteBrushTreeLink:visited,.SiteBrushTreeLink:active,.SiteBrushTreeLink:hover{color:#1f3f6f;text-decoration:none}
.SiteBrushTreeCurrent{font-weight:700;text-decoration:underline}
@media (pointer: coarse), (max-width: 820px){
  .SiteBrushMenuBox{right:auto;min-width:min(220px,calc(100vw - 12px));max-width:calc(100vw - 12px);max-height:calc(100vh - 12px);border-radius:7px;padding:1px;-webkit-overflow-scrolling:touch}
  .SiteBrushContextMenuLink{min-height:34px;padding:7px 9px;font-size:13px;gap:7px}
  .SiteBrushMenuIcon{width:16px;height:16px;flex-basis:16px}
  .SiteBrushContextMenuFooter{font-size:11px;flex-wrap:wrap;padding:6px 8px 7px 8px;gap:8px}
  .SiteBrushContextMenuFooterLink,.SiteBrushContextMenuVersion{font-size:11px}
  .SiteBrushMenuStorageUsage{margin-left:0}
}
@media (prefers-color-scheme: dark){
  .SiteBrushMenuBox{background:#172235;border-color:#2f405d}
  .SiteBrushMenuBox.SiteBrushMenuBoxFrozen{background:#13263d;border-color:#4a6f99}
  .SiteBrushContextMenuLink{color:#dbe8ff}
  .SiteBrushContextMenuLink:link,.SiteBrushContextMenuLink:visited,.SiteBrushContextMenuLink:active{color:#dbe8ff}
  .SiteBrushContextMenuLink:hover{color:#dbe8ff;background:#24344d}
  .SiteBrushDomainMenuItem .SiteBrushContextMenuLink{border-bottom-color:#2f405d}
  .SiteBrushContextMenuFooter{color:#a7bbd8;border-top-color:#2f405d}
  .SiteBrushContextMenuFooterLink,.SiteBrushContextMenuVersion{color:#a7bbd8}
  .SiteBrushContextMenuFooterLink:link,.SiteBrushContextMenuFooterLink:visited,.SiteBrushContextMenuFooterLink:active,.SiteBrushContextMenuFooterLink:hover,.SiteBrushContextMenuVersion:link,.SiteBrushContextMenuVersion:visited,.SiteBrushContextMenuVersion:active,.SiteBrushContextMenuVersion:hover{color:#a7bbd8}
  .SiteBrushConfirmModal{background:#172235;border-color:#2f405d}
  .SiteBrushConfirmText,.SiteBrushPublishPreviewLink{color:#dbe8ff}
  .SiteBrushPublishPreviewLink:link,.SiteBrushPublishPreviewLink:visited,.SiteBrushPublishPreviewLink:active,.SiteBrushPublishPreviewLink:hover{color:#dbe8ff}
  .SiteBrushPublishProgressPath{color:#a7bbd8}
  .SiteBrushPublishProgress{background:#22324a;border-color:#405674}
  .SiteBrushPublishProgressBar{background:#4f8bd8;color:#fff}
  .SiteBrushConfirmButton,.SiteBrushCancelButton{background:#22324a;color:#dbe8ff;border-color:#405674}
  .SiteBrushTreeModal{background:#172235;border-color:#2f405d}
  .SiteBrushTreeCloseButton{background:#22324a;color:#dbe8ff;border-color:#405674}
  .SiteBrushTreeTitle,.SiteBrushTreeContent,.SiteBrushTreeLink{color:#dbe8ff}
  .SiteBrushTreeLink:link,.SiteBrushTreeLink:visited,.SiteBrushTreeLink:active,.SiteBrushTreeLink:hover{color:#dbe8ff}
}
</style>
<script>
const sitebrushContextMenuShadowCSS = document.currentScript && document.currentScript.previousElementSibling ? document.currentScript.previousElementSibling.textContent : "";
function closestSitebrushEventElement(browserEvent, selector) {
  const directTarget = browserEvent.target;
  if (directTarget && directTarget.closest) {
    const directMatch = directTarget.closest(selector);
    if (directMatch) {
      return directMatch;
    }
  }
  if (!browserEvent.composedPath) {
    return null;
  }
  for (const pathNode of browserEvent.composedPath()) {
    if (!pathNode || pathNode.nodeType !== Node.ELEMENT_NODE) {
      continue;
    }
    if (pathNode.matches && pathNode.matches(selector)) {
      return pathNode;
    }
    if (pathNode.closest) {
      const pathMatch = pathNode.closest(selector);
      if (pathMatch) {
        return pathMatch;
      }
    }
  }
  return null;
}
function closeSitebrushMenu() {
  const existingMenuBox = document.getElementById("SiteBrushMenuBox");
  if (existingMenuBox) {
    existingMenuBox.remove();
  }
}
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
function sitebrushMenuViewportNumber(candidateNumber, fallbackNumber) {
  const parsedNumber = Number(candidateNumber);
  if (Number.isFinite(parsedNumber) && parsedNumber > 0) {
    return parsedNumber;
  }
  return fallbackNumber;
}
function sitebrushMenuPointFromEvent(browserEvent) {
  const viewportWidth = sitebrushMenuViewportNumber(window.innerWidth, document.documentElement.clientWidth || 320);
  const viewportHeight = sitebrushMenuViewportNumber(window.innerHeight, document.documentElement.clientHeight || 480);
  return {
    clientX: Math.max(0, Math.min(Number(browserEvent.clientX) || 0, viewportWidth)),
    clientY: Math.max(0, Math.min(Number(browserEvent.clientY) || 0, viewportHeight))
  };
}
function positionSitebrushMenuBox(menuBoxElement, menuPoint) {
  const viewportWidth = sitebrushMenuViewportNumber(window.innerWidth, document.documentElement.clientWidth || 320);
  const viewportHeight = sitebrushMenuViewportNumber(window.innerHeight, document.documentElement.clientHeight || 480);
  const viewportGap = 8;
  const menuRect = menuBoxElement.getBoundingClientRect();
  const menuWidth = Math.min(menuRect.width || 0, viewportWidth - viewportGap * 2);
  const menuHeight = Math.min(menuRect.height || 0, viewportHeight - viewportGap * 2);
  const boundedLeft = Math.max(viewportGap, Math.min(menuPoint.clientX, viewportWidth - menuWidth - viewportGap));
  const boundedTop = Math.max(viewportGap, Math.min(menuPoint.clientY, viewportHeight - menuHeight - viewportGap));
  menuBoxElement.style.left = boundedLeft + "px";
  menuBoxElement.style.top = boundedTop + "px";
}
function sitebrushShouldIgnoreContextMenuEvent(browserEvent) {
  if (!window.__sitebrushLongPressMenuUntil) {
    return false;
  }
  if (Date.now() > window.__sitebrushLongPressMenuUntil) {
    window.__sitebrushLongPressMenuUntil = 0;
    return false;
  }
  if (document.getElementById("SiteBrushMenuBox")) {
    browserEvent.preventDefault();
    browserEvent.stopPropagation();
    return true;
  }
  return false;
}
function installSitebrushLongPressMenu(openMenuAtPoint) {
  const longPressDelayMilliseconds = 650;
  const moveTolerancePixels = 12;
  let longPressTimer = 0;
  let longPressStartPoint = null;
  let longPressPointerID = null;
  function clearLongPressTimer() {
    if (longPressTimer) {
      window.clearTimeout(longPressTimer);
      longPressTimer = 0;
    }
  }
  function cancelLongPress() {
    clearLongPressTimer();
    longPressStartPoint = null;
    longPressPointerID = null;
  }
  function isTouchLikePointerEvent(browserEvent) {
    return browserEvent.pointerType === "touch" || browserEvent.pointerType === "pen";
  }
  function startLongPress(browserEvent) {
    if (closestSitebrushEventElement(browserEvent, "#SiteBrushMenuBox")) {
      return;
    }
    if (browserEvent.pointerType && !isTouchLikePointerEvent(browserEvent)) {
      return;
    }
    if (browserEvent.button && browserEvent.button !== 0) {
      return;
    }
    cancelLongPress();
    longPressStartPoint = sitebrushMenuPointFromEvent(browserEvent);
    longPressPointerID = browserEvent.pointerId || null;
    longPressTimer = window.setTimeout(function openMenuFromLongPress() {
      const menuPoint = longPressStartPoint;
      cancelLongPress();
      if (!menuPoint) {
        return;
      }
      window.__sitebrushLongPressMenuUntil = Date.now() + 1000;
      openMenuAtPoint(menuPoint);
    }, longPressDelayMilliseconds);
  }
  function moveLongPress(browserEvent) {
    if (!longPressStartPoint) {
      return;
    }
    if (longPressPointerID !== null && browserEvent.pointerId && browserEvent.pointerId !== longPressPointerID) {
      return;
    }
    const currentPoint = sitebrushMenuPointFromEvent(browserEvent);
    const movedX = Math.abs(currentPoint.clientX - longPressStartPoint.clientX);
    const movedY = Math.abs(currentPoint.clientY - longPressStartPoint.clientY);
    if (movedX > moveTolerancePixels || movedY > moveTolerancePixels) {
      cancelLongPress();
    }
  }
  function blockSyntheticClickAfterLongPress(browserEvent) {
    if (!window.__sitebrushLongPressMenuUntil || Date.now() > window.__sitebrushLongPressMenuUntil) {
      return;
    }
    if (closestSitebrushEventElement(browserEvent, "#SiteBrushMenuBox")) {
      return;
    }
    browserEvent.preventDefault();
    browserEvent.stopPropagation();
  }
  if (window.PointerEvent) {
    document.addEventListener("pointerdown", startLongPress, {capture: true, passive: true});
    document.addEventListener("pointermove", moveLongPress, {capture: true, passive: true});
    document.addEventListener("pointerup", cancelLongPress, {capture: true, passive: true});
    document.addEventListener("pointercancel", cancelLongPress, {capture: true, passive: true});
  } else {
    document.addEventListener("touchstart", function onTouchStart(browserEvent) {
      if (browserEvent.touches.length !== 1) {
        cancelLongPress();
        return;
      }
      startLongPress(browserEvent.touches[0]);
    }, {capture: true, passive: true});
    document.addEventListener("touchmove", function onTouchMove(browserEvent) {
      if (browserEvent.touches.length !== 1) {
        cancelLongPress();
        return;
      }
      moveLongPress(browserEvent.touches[0]);
    }, {capture: true, passive: true});
    document.addEventListener("touchend", cancelLongPress, {capture: true, passive: true});
    document.addEventListener("touchcancel", cancelLongPress, {capture: true, passive: true});
  }
  document.addEventListener("click", blockSyntheticClickAfterLongPress, {capture: true, passive: false});
}
function showSitebrushMenu(browserEvent, menuHtmlEntries, currentPagePath, frozenMenuEnabled) {
  closeSitebrushMenu();
  const menuPoint = sitebrushMenuPointFromEvent(browserEvent);
  const menuHostElement = document.createElement("div");
  menuHostElement.id = "SiteBrushMenuBox";
  menuHostElement.setAttribute("data-sitebrush-owned", "true");
  menuHostElement.style.setProperty("all", "initial", "important");
  menuHostElement.style.setProperty("position", "fixed", "important");
  menuHostElement.style.setProperty("left", "0", "important");
  menuHostElement.style.setProperty("top", "0", "important");
  menuHostElement.style.setProperty("z-index", "2147483646", "important");
  const menuRoot = menuHostElement.attachShadow ? menuHostElement.attachShadow({mode: "open"}) : menuHostElement;
  const menuStyleElement = document.createElement("style");
  menuStyleElement.setAttribute("data-sitebrush-owned", "true");
  menuStyleElement.textContent = sitebrushContextMenuShadowCSS;
  const menuBoxElement = document.createElement("div");
  menuBoxElement.className = "SiteBrushMenuBox";
  menuBoxElement.setAttribute("data-sitebrush-owned", "true");
  if (frozenMenuEnabled) {
    menuBoxElement.classList.add("SiteBrushMenuBoxFrozen");
  }
  menuBoxElement.innerHTML = menuHtmlEntries.join("");
  normalizeSitebrushMenuLinks(menuBoxElement, currentPagePath);
  menuBoxElement.style.left = menuPoint.clientX + "px";
  menuBoxElement.style.top = menuPoint.clientY + "px";
  menuRoot.appendChild(menuStyleElement);
  menuRoot.appendChild(menuBoxElement);
  document.body.appendChild(menuHostElement);
  positionSitebrushMenuBox(menuBoxElement, menuPoint);
  menuBoxElement.addEventListener("click", function onMenuClick(browserEvent) {
    const menuLinkElement = browserEvent.target && browserEvent.target.closest && browserEvent.target.closest("a[href]");
    if (!menuLinkElement) {
      return;
    }
    const targetHref = menuLinkElement.getAttribute("href");
    if (!targetHref) {
      return;
    }
    browserEvent.preventDefault();
    browserEvent.stopPropagation();
    window.location.href = targetHref;
  });
  document.addEventListener("click", function closeMenuOnClick(browserEvent) {
    const openMenu = document.getElementById("SiteBrushMenuBox");
    if (!openMenu) {
      return;
    }
    if (closestSitebrushEventElement(browserEvent, "#SiteBrushMenuBox")) {
      return;
    }
    openMenu.remove();
  }, { once: true });
}
function buildSiteTreeNodeName(fullPath) {
  if (fullPath === "/") { return "/"; }
  if (fullPath.endsWith("/")) {
    const trimmedPath = fullPath.slice(0, -1);
    const slashIndex = trimmedPath.lastIndexOf("/");
    return trimmedPath.slice(slashIndex + 1) + "/";
  }
  const slashIndex = fullPath.lastIndexOf("/");
  return fullPath.slice(slashIndex + 1);
}
function parentTreePath(fullPath) {
  if (fullPath === "/") { return ""; }
  if (fullPath.endsWith("/")) { return fullPath.slice(0, -1); }
  const slashIndex = fullPath.lastIndexOf("/");
  if (slashIndex <= 0) { return "/"; }
  return fullPath.slice(0, slashIndex);
}
function buildSiteTreeStateFromPaths(pagePathList, currentPagePath) {
  const rootNode = { name: "/", fullPath: "/", childrenByName: {}, childList: [] };
  const nodeByPath = { "/": rootNode };
  const ensureNode = function ensureTreeNode(pathValue) {
    if (nodeByPath[pathValue]) { return nodeByPath[pathValue]; }
    const createdNode = { name: buildSiteTreeNodeName(pathValue), fullPath: pathValue, childrenByName: {}, childList: [] };
    nodeByPath[pathValue] = createdNode;
    return createdNode;
  };
  const connectChildToParent = function connectTreeChild(parentNode, childNode) {
    if (parentNode.childrenByName[childNode.fullPath]) { return; }
    parentNode.childrenByName[childNode.fullPath] = childNode;
    parentNode.childList.push(childNode);
  };
  for (const fullPathEntry of pagePathList) {
    const exactPath = fullPathEntry || "/";
    let childNode = ensureNode(exactPath);
    let parentPath = parentTreePath(exactPath);
    while (parentPath) {
      const parentNode = ensureNode(parentPath);
      connectChildToParent(parentNode, childNode);
      childNode = parentNode;
      parentPath = parentTreePath(parentNode.fullPath);
    }
  }
  const sortBranch = function sortBranchNodes(branchNode) {
    branchNode.childList.sort(function compareBranchNames(leftBranch, rightBranch) { return leftBranch.name.localeCompare(rightBranch.name); });
    for (const childBranch of branchNode.childList) { sortBranch(childBranch); }
  };
  sortBranch(rootNode);
  rootNode.currentPath = currentPagePath;
  return rootNode;
}
function renderSiteTree(hostElement, treeData) {
  hostElement.textContent = "";
  const treeState = buildSiteTreeStateFromPaths(treeData.paths || [], treeData.current_path || "/");
  const branchRootList = document.createElement("ul");
  branchRootList.className = "SiteBrushTreeList";
  hostElement.appendChild(branchRootList);
  const renderBranchNode = function renderBranchNodeRecursive(branchNode, parentListElement) {
    const branchListItemElement = document.createElement("li");
    const branchLinkElement = document.createElement("a");
    branchLinkElement.className = "SiteBrushTreeLink";
    branchLinkElement.href = branchNode.fullPath;
    branchLinkElement.textContent = branchNode.fullPath;
    if (branchNode.fullPath === treeState.currentPath) { branchLinkElement.classList.add("SiteBrushTreeCurrent"); }
    branchListItemElement.appendChild(branchLinkElement);
    parentListElement.appendChild(branchListItemElement);
    if (branchNode.childList.length === 0) { return; }
    const childListElement = document.createElement("ul");
    childListElement.className = "SiteBrushTreeList";
    branchListItemElement.appendChild(childListElement);
    for (const childNode of branchNode.childList) { renderBranchNodeRecursive(childNode, childListElement); }
  };
  renderBranchNode(treeState, branchRootList);
}
</script>`
}

func translationOrDefault(translations map[string]string, key, fallback string) string {
	translatedValue := strings.TrimSpace(translations[key])
	if translatedValue == "" {
		return fallback
	}
	return translatedValue
}

const guestContextMenuPagePathPlaceholder = "__SITEBRUSH_PAGE_PATH__"
const guestContextMenuDomainPlaceholder = "__SITEBRUSH_DOMAIN__"
const guestNotFoundPagePathPlaceholder = "__SITEBRUSH_NOT_FOUND_PATH__"
const guestNotFoundEditLinkPlaceholder = "__SITEBRUSH_NOT_FOUND_EDIT_LINK__"
const guestNotFoundMenuPlaceholder = "__SITEBRUSH_NOT_FOUND_MENU__"

func (a *App) applyTemplatePropagation(ctx context.Context, domain, sourceHTML string) {
	templateBlockByID := extractTemplateBlocks(sourceHTML)
	if len(templateBlockByID) == 0 {
		return
	}

	pageRows, err := a.db.QueryContext(ctx, `SELECT path,title,html FROM pages WHERE domain=?`, domain)
	if err != nil {
		return
	}
	type storedPage struct {
		path  string
		title string
		html  string
	}
	pageList := make([]storedPage, 0)
	for pageRows.Next() {
		var currentPage storedPage
		if scanErr := pageRows.Scan(&currentPage.path, &currentPage.title, &currentPage.html); scanErr != nil {
			continue
		}
		pageList = append(pageList, currentPage)
	}
	_ = pageRows.Close()

	frozenDomain := a.isDomainFrozen(ctx, domain)
	for _, currentPage := range pageList {
		updatedHTML, changed := replaceTemplateBlocks(currentPage.html, templateBlockByID)
		if !changed || updatedHTML == currentPage.html {
			continue
		}
		updatedHTMLBytes := int64(len([]byte(updatedHTML)))
		pageDelta := updatedHTMLBytes - int64(len([]byte(currentPage.html)))
		publishedPageDelta := int64(0)
		publishedStaticDelta := int64(0)
		if !frozenDomain {
			var previousPublishedHTML string
			_ = a.db.QueryRowContext(ctx, `SELECT html FROM published_pages WHERE domain=? AND path=?`, domain, currentPage.path).Scan(&previousPublishedHTML)
			publishedPageDelta = updatedHTMLBytes - int64(len([]byte(previousPublishedHTML)))
			publishedStaticDelta = updatedHTMLBytes - fileSizeBytes(filepath.Join(a.domainStaticDir(domain), staticRelativePathForPage(currentPage.path)))
		}
		if storageErr := a.applyDomainStorageDelta(ctx, domain, pageDelta, publishedPageDelta, updatedHTMLBytes, 0, publishedStaticDelta); storageErr != nil {
			log.Printf("template propagation blocked by storage limit domain=%s path=%s error=%v", domain, currentPage.path, storageErr)
			continue
		}

		_, _ = a.db.ExecContext(ctx, `UPDATE pages SET html=? WHERE domain=? AND path=?`, updatedHTML, domain, currentPage.path)
		if !frozenDomain {
			_, _ = a.db.ExecContext(ctx, `INSERT OR REPLACE INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, domain, currentPage.path, currentPage.title, updatedHTML)
			a.writePublishedStaticHTML(domain, currentPage.path, updatedHTML)
		}
		_, _ = a.db.ExecContext(ctx, `INSERT INTO revisions(domain,page_path,html,created_at) VALUES(?,?,?,?)`, domain, currentPage.path, updatedHTML, time.Now().Format(time.RFC3339))
	}
}

type templateMatch struct {
	start int
	end   int
	id    string
	block string
}

type templateClassActionSet struct {
	addKeys    map[string]struct{}
	removeKeys map[string]struct{}
}

type templateClassElement struct {
	startTagStart int
	startTagEnd   int
	matchKey      string
	hasTemplate   bool
}

type templateClassOpenElement struct {
	tagName       string
	startTagStart int
	startTagEnd   int
	classKey      string
	hasTemplate   bool
}

type templateClassEdit struct {
	startTagStart int
	startTagEnd   int
	addTemplate   bool
}

type templateOpenElement struct {
	tagName    string
	start      int
	templateID string
}

var htmlClassAttributePattern = regexp.MustCompile(`(?is)\sclass\s*=\s*("([^"]*)"|'([^']*)'|([^\s"'=<>]+))`)

func (a *App) applyTemplateClassSynchronization(ctx context.Context, domain, previousHTML, savedHTML string) {
	actionSet := templateClassActionSetFromHTML(previousHTML, savedHTML)
	if len(actionSet.addKeys) == 0 && len(actionSet.removeKeys) == 0 {
		return
	}

	pageRows, err := a.db.QueryContext(ctx, `SELECT path,title,html FROM pages WHERE domain=?`, domain)
	if err != nil {
		return
	}
	type storedPage struct {
		path  string
		title string
		html  string
	}
	pageList := make([]storedPage, 0)
	for pageRows.Next() {
		var currentPage storedPage
		if scanErr := pageRows.Scan(&currentPage.path, &currentPage.title, &currentPage.html); scanErr != nil {
			continue
		}
		pageList = append(pageList, currentPage)
	}
	_ = pageRows.Close()

	frozenDomain := a.isDomainFrozen(ctx, domain)
	for _, currentPage := range pageList {
		if pageContentKind(currentPage.path, currentPage.html) != "html" {
			continue
		}
		updatedHTML, changed := synchronizeTemplateClassesInHTML(currentPage.html, actionSet)
		if !changed || updatedHTML == currentPage.html {
			continue
		}

		updatedHTMLBytes := int64(len([]byte(updatedHTML)))
		pageDelta := updatedHTMLBytes - int64(len([]byte(currentPage.html)))
		publishedPageDelta := int64(0)
		publishedStaticDelta := int64(0)
		if !frozenDomain {
			var previousPublishedHTML string
			_ = a.db.QueryRowContext(ctx, `SELECT html FROM published_pages WHERE domain=? AND path=?`, domain, currentPage.path).Scan(&previousPublishedHTML)
			publishedPageDelta = updatedHTMLBytes - int64(len([]byte(previousPublishedHTML)))
			publishedStaticDelta = updatedHTMLBytes - fileSizeBytes(filepath.Join(a.domainStaticDir(domain), staticRelativePathForPage(currentPage.path)))
		}
		if storageErr := a.applyDomainStorageDelta(ctx, domain, pageDelta, publishedPageDelta, updatedHTMLBytes, 0, publishedStaticDelta); storageErr != nil {
			log.Printf("template class synchronization blocked by storage limit domain=%s path=%s error=%v", domain, currentPage.path, storageErr)
			continue
		}

		_, _ = a.db.ExecContext(ctx, `UPDATE pages SET html=? WHERE domain=? AND path=?`, updatedHTML, domain, currentPage.path)
		if !frozenDomain {
			_, _ = a.db.ExecContext(ctx, `INSERT OR REPLACE INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, domain, currentPage.path, currentPage.title, updatedHTML)
			a.writePublishedStaticHTML(domain, currentPage.path, updatedHTML)
		}
		_, _ = a.db.ExecContext(ctx, `INSERT INTO revisions(domain,page_path,html,created_at) VALUES(?,?,?,?)`, domain, currentPage.path, updatedHTML, time.Now().Format(time.RFC3339))
	}
}

func templateClassActionSetFromHTML(previousHTML, savedHTML string) templateClassActionSet {
	previousTemplateKeys, previousPlainKeys := templateClassKeySets(previousHTML)
	savedTemplateKeys, savedPlainKeys := templateClassKeySets(savedHTML)

	actionSet := templateClassActionSet{
		addKeys:    make(map[string]struct{}),
		removeKeys: make(map[string]struct{}),
	}
	for innerKey := range savedTemplateKeys {
		_, wasPlain := previousPlainKeys[innerKey]
		_, wasTemplate := previousTemplateKeys[innerKey]
		if wasPlain || !wasTemplate {
			actionSet.addKeys[innerKey] = struct{}{}
		}
	}
	for innerKey := range previousTemplateKeys {
		if _, isPlain := savedPlainKeys[innerKey]; isPlain {
			actionSet.removeKeys[innerKey] = struct{}{}
			delete(actionSet.addKeys, innerKey)
		}
	}
	return actionSet
}

func templateClassKeySets(sourceHTML string) (map[string]struct{}, map[string]struct{}) {
	templateKeys := make(map[string]struct{})
	plainKeys := make(map[string]struct{})
	for _, element := range scanTemplateClassElements(sourceHTML) {
		if element.hasTemplate {
			templateKeys[element.matchKey] = struct{}{}
			continue
		}
		plainKeys[element.matchKey] = struct{}{}
	}
	return templateKeys, plainKeys
}

func synchronizeTemplateClassesInHTML(sourceHTML string, actionSet templateClassActionSet) (string, bool) {
	if len(actionSet.addKeys) == 0 && len(actionSet.removeKeys) == 0 {
		return sourceHTML, false
	}
	editList := make([]templateClassEdit, 0)
	for _, element := range scanTemplateClassElements(sourceHTML) {
		if _, removeTemplate := actionSet.removeKeys[element.matchKey]; removeTemplate && element.hasTemplate {
			editList = append(editList, templateClassEdit{startTagStart: element.startTagStart, startTagEnd: element.startTagEnd, addTemplate: false})
			continue
		}
		if _, addTemplate := actionSet.addKeys[element.matchKey]; addTemplate {
			editList = append(editList, templateClassEdit{startTagStart: element.startTagStart, startTagEnd: element.startTagEnd, addTemplate: true})
		}
	}
	if len(editList) == 0 {
		return sourceHTML, false
	}
	sort.Slice(editList, func(leftIndex, rightIndex int) bool {
		return editList[leftIndex].startTagStart < editList[rightIndex].startTagStart
	})

	var updatedHTML strings.Builder
	updatedHTML.Grow(len(sourceHTML) + len(editList)*len("SiteBrush-Template "))
	previousEnd := 0
	changed := false
	for _, edit := range editList {
		if edit.startTagStart < previousEnd || edit.startTagEnd > len(sourceHTML) {
			continue
		}
		startTag := sourceHTML[edit.startTagStart:edit.startTagEnd]
		updatedStartTag := rewriteTemplateClassStartTag(startTag, edit.addTemplate)
		updatedHTML.WriteString(sourceHTML[previousEnd:edit.startTagStart])
		updatedHTML.WriteString(updatedStartTag)
		previousEnd = edit.startTagEnd
		if updatedStartTag != startTag {
			changed = true
		}
	}
	if !changed {
		return sourceHTML, false
	}
	updatedHTML.WriteString(sourceHTML[previousEnd:])
	return updatedHTML.String(), true
}

func scanTemplateClassElements(sourceHTML string) []templateClassElement {
	tokenizer := html.NewTokenizer(strings.NewReader(sourceHTML))
	openElementStack := make([]templateClassOpenElement, 0)
	elementList := make([]templateClassElement, 0)
	offset := 0

	for {
		tokenType := tokenizer.Next()
		rawToken := tokenizer.Raw()
		tokenStart := offset
		tokenEnd := tokenStart + len(rawToken)
		offset = tokenEnd

		switch tokenType {
		case html.ErrorToken:
			return elementList
		case html.StartTagToken:
			token := tokenizer.Token()
			tagName := strings.ToLower(token.Data)
			classKey := normalizedTemplateClassKey(token)
			if isHTMLVoidElement(tagName) {
				elementList = append(elementList, templateClassElement{
					startTagStart: tokenStart,
					startTagEnd:   tokenEnd,
					matchKey:      templateClassElementKey(tagName, classKey, ""),
					hasTemplate:   tokenHasSiteBrushTemplateClass(token),
				})
				continue
			}
			openElementStack = append(openElementStack, templateClassOpenElement{
				tagName:       tagName,
				startTagStart: tokenStart,
				startTagEnd:   tokenEnd,
				classKey:      classKey,
				hasTemplate:   tokenHasSiteBrushTemplateClass(token),
			})
		case html.SelfClosingTagToken:
			token := tokenizer.Token()
			tagName := strings.ToLower(token.Data)
			elementList = append(elementList, templateClassElement{
				startTagStart: tokenStart,
				startTagEnd:   tokenEnd,
				matchKey:      templateClassElementKey(tagName, normalizedTemplateClassKey(token), ""),
				hasTemplate:   tokenHasSiteBrushTemplateClass(token),
			})
		case html.EndTagToken:
			token := tokenizer.Token()
			for len(openElementStack) > 0 {
				lastIndex := len(openElementStack) - 1
				openElement := openElementStack[lastIndex]
				openElementStack = openElementStack[:lastIndex]
				if !strings.EqualFold(openElement.tagName, token.Data) {
					continue
				}
				elementList = append(elementList, templateClassElement{
					startTagStart: openElement.startTagStart,
					startTagEnd:   openElement.startTagEnd,
					matchKey:      templateClassElementKey(openElement.tagName, openElement.classKey, normalizedTemplateInnerHTML(sourceHTML[openElement.startTagEnd:tokenStart])),
					hasTemplate:   openElement.hasTemplate,
				})
				break
			}
		}
	}
}

func templateClassElementKey(tagName, classKey, innerKey string) string {
	return strings.ToLower(tagName) + "\x00" + classKey + "\x00" + innerKey
}

func tokenHasSiteBrushTemplateClass(token html.Token) bool {
	for _, attribute := range token.Attr {
		if strings.EqualFold(attribute.Key, "class") && classListHasSiteBrushTemplate(attribute.Val) {
			return true
		}
	}
	return false
}

func normalizedTemplateClassKey(token html.Token) string {
	classNameSet := make(map[string]struct{})
	for _, attribute := range token.Attr {
		if !strings.EqualFold(attribute.Key, "class") {
			continue
		}
		for _, className := range strings.Fields(attribute.Val) {
			if strings.EqualFold(className, "SiteBrush-Template") {
				continue
			}
			classNameSet[className] = struct{}{}
		}
	}
	classNameList := make([]string, 0, len(classNameSet))
	for className := range classNameSet {
		classNameList = append(classNameList, className)
	}
	sort.Strings(classNameList)
	return strings.Join(classNameList, " ")
}

func normalizedTemplateInnerHTML(innerHTML string) string {
	return strings.Map(func(innerRune rune) rune {
		if unicode.IsSpace(innerRune) {
			return -1
		}
		return innerRune
	}, innerHTML)
}

func isHTMLVoidElement(tagName string) bool {
	switch strings.ToLower(tagName) {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return true
	default:
		return false
	}
}

func rewriteTemplateClassStartTag(startTag string, addTemplate bool) string {
	classMatch := htmlClassAttributePattern.FindStringSubmatchIndex(startTag)
	if classMatch == nil {
		if !addTemplate {
			return startTag
		}
		return insertTemplateClassAttribute(startTag)
	}

	classValueStart := -1
	classValueEnd := -1
	for _, pair := range [][2]int{{4, 5}, {6, 7}, {8, 9}} {
		if classMatch[pair[0]] >= 0 && classMatch[pair[1]] >= 0 {
			classValueStart = classMatch[pair[0]]
			classValueEnd = classMatch[pair[1]]
			break
		}
	}
	if classValueStart < 0 || classValueEnd < 0 {
		return startTag
	}

	classValue := startTag[classValueStart:classValueEnd]
	updatedClassValue := removeSiteBrushTemplateClass(classValue)
	if addTemplate {
		updatedClassValue = prependSiteBrushTemplateClass(updatedClassValue)
	}
	if strings.TrimSpace(updatedClassValue) == "" {
		return startTag[:classMatch[0]] + startTag[classMatch[1]:]
	}
	return startTag[:classMatch[0]] + ` class="` + updatedClassValue + `"` + startTag[classMatch[1]:]
}

func insertTemplateClassAttribute(startTag string) string {
	insertIndex := strings.LastIndex(startTag, ">")
	if insertIndex < 0 {
		return startTag
	}
	for insertIndex > 0 && unicode.IsSpace(rune(startTag[insertIndex-1])) {
		insertIndex--
	}
	if insertIndex > 0 && startTag[insertIndex-1] == '/' {
		insertIndex--
	}
	return startTag[:insertIndex] + ` class="SiteBrush-Template"` + startTag[insertIndex:]
}

func prependSiteBrushTemplateClass(classValue string) string {
	classNameList := make([]string, 0, len(strings.Fields(classValue))+1)
	classNameList = append(classNameList, "SiteBrush-Template")
	for _, className := range strings.Fields(classValue) {
		if strings.EqualFold(className, "SiteBrush-Template") {
			continue
		}
		classNameList = append(classNameList, className)
	}
	return strings.Join(classNameList, " ")
}

func removeSiteBrushTemplateClass(classValue string) string {
	classNameList := make([]string, 0, len(strings.Fields(classValue)))
	for _, className := range strings.Fields(classValue) {
		if strings.EqualFold(className, "SiteBrush-Template") {
			continue
		}
		classNameList = append(classNameList, className)
	}
	return strings.Join(classNameList, " ")
}

func classListHasSiteBrushTemplate(classValue string) bool {
	for _, className := range strings.Fields(classValue) {
		if strings.EqualFold(className, "SiteBrush-Template") {
			return true
		}
	}
	return false
}

func extractTemplateBlocks(sourceHTML string) map[string]string {
	matchList := scanTemplateMatches(sourceHTML)
	if len(matchList) == 0 {
		return nil
	}

	templateBlockByID := make(map[string]string, len(matchList))
	for _, currentMatch := range matchList {
		templateBlockByID[currentMatch.id] = currentMatch.block
	}
	return templateBlockByID
}

func replaceTemplateBlocks(pageHTML string, templateBlockByID map[string]string) (string, bool) {
	matchList := scanTemplateMatches(pageHTML)
	if len(matchList) == 0 {
		return pageHTML, false
	}
	matchList = sortedNonOverlappingTemplateMatches(matchList)
	if len(matchList) == 0 {
		return pageHTML, false
	}

	var updatedHTML strings.Builder
	updatedHTML.Grow(len(pageHTML))
	changed := false
	previousEnd := 0

	for _, currentMatch := range matchList {
		replacementBlock, found := templateBlockByID[currentMatch.id]
		if !found {
			continue
		}
		updatedHTML.WriteString(pageHTML[previousEnd:currentMatch.start])
		updatedHTML.WriteString(replacementBlock)
		previousEnd = currentMatch.end
		changed = true
	}

	if !changed {
		return pageHTML, false
	}
	updatedHTML.WriteString(pageHTML[previousEnd:])
	return updatedHTML.String(), true
}

func sortedNonOverlappingTemplateMatches(matchList []templateMatch) []templateMatch {
	if len(matchList) < 2 {
		return matchList
	}

	sortedMatchList := append([]templateMatch(nil), matchList...)
	sort.Slice(sortedMatchList, func(leftIndex, rightIndex int) bool {
		leftMatch := sortedMatchList[leftIndex]
		rightMatch := sortedMatchList[rightIndex]
		if leftMatch.start != rightMatch.start {
			return leftMatch.start < rightMatch.start
		}
		return leftMatch.end > rightMatch.end
	})

	filteredMatchList := sortedMatchList[:0]
	previousEnd := -1
	for _, currentMatch := range sortedMatchList {
		if currentMatch.start < previousEnd {
			continue
		}
		filteredMatchList = append(filteredMatchList, currentMatch)
		previousEnd = currentMatch.end
	}
	return filteredMatchList
}

func scanTemplateMatches(pageHTML string) []templateMatch {
	tokenizer := html.NewTokenizer(strings.NewReader(pageHTML))
	openElementStack := make([]templateOpenElement, 0)
	matchList := make([]templateMatch, 0)
	offset := 0

	for {
		tokenType := tokenizer.Next()
		rawToken := tokenizer.Raw()
		tokenStart := offset
		tokenEnd := tokenStart + len(rawToken)
		offset = tokenEnd

		switch tokenType {
		case html.ErrorToken:
			return matchList
		case html.StartTagToken:
			token := tokenizer.Token()
			openElementStack = append(openElementStack, templateOpenElement{
				tagName:    token.Data,
				start:      tokenStart,
				templateID: templateIdentifierFromAttributes(token.Attr),
			})
		case html.SelfClosingTagToken:
			token := tokenizer.Token()
			templateID := templateIdentifierFromAttributes(token.Attr)
			if templateID == "" {
				continue
			}
			matchList = append(matchList, templateMatch{
				start: tokenStart,
				end:   tokenEnd,
				id:    templateID,
				block: pageHTML[tokenStart:tokenEnd],
			})
		case html.EndTagToken:
			token := tokenizer.Token()
			for len(openElementStack) > 0 {
				lastIndex := len(openElementStack) - 1
				openElement := openElementStack[lastIndex]
				openElementStack = openElementStack[:lastIndex]
				if !strings.EqualFold(openElement.tagName, token.Data) {
					continue
				}
				if openElement.templateID == "" {
					break
				}
				matchList = append(matchList, templateMatch{
					start: openElement.start,
					end:   tokenEnd,
					id:    openElement.templateID,
					block: pageHTML[openElement.start:tokenEnd],
				})
				break
			}
		}
	}
}

func templateIdentifierFromAttributes(attributeList []html.Attribute) string {
	for _, attribute := range attributeList {
		if !strings.EqualFold(attribute.Key, "class") {
			continue
		}
		return templateIdentifierFromClassList(attribute.Val)
	}
	return ""
}

func templateIdentifierFromClassList(classValue string) string {
	classNameList := strings.Fields(classValue)
	for classIndex, className := range classNameList {
		if strings.EqualFold(className, "SiteBrush-Template") {
			if classIndex+1 < len(classNameList) {
				return classNameList[classIndex+1]
			}
			return ""
		}
		lowerClassName := strings.ToLower(className)
		if strings.HasPrefix(lowerClassName, "sitebrush-template-") {
			return className[len("sitebrush-template-"):]
		}
	}
	return ""
}

func (a *App) render(w http.ResponseWriter, r *http.Request, templateName string, templateData any) {
	fileBytes, err := fs.ReadFile(embeddedWebFiles, "web/"+templateName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	parsedTemplate := template.Must(template.New(templateName).Parse(string(fileBytes)))
	envelope := map[string]any{"Domain": a.siteDomain(r.Context(), r), "T": translationsForRequest(r), "CompileVersion": CompileVersion}
	mergeTemplateData(envelope, templateData)
	_ = parsedTemplate.Execute(w, envelope)
}

func mergeTemplateData(envelope map[string]any, templateData any) {
	if templateData == nil {
		return
	}
	if values, ok := templateData.(map[string]any); ok {
		for key, value := range values {
			envelope[key] = value
		}
		return
	}

	reflectedValue := reflect.ValueOf(templateData)
	if reflectedValue.Kind() == reflect.Pointer {
		if reflectedValue.IsNil() {
			return
		}
		reflectedValue = reflectedValue.Elem()
	}
	if reflectedValue.Kind() == reflect.Struct {
		reflectedType := reflectedValue.Type()
		for fieldIndex := 0; fieldIndex < reflectedValue.NumField(); fieldIndex++ {
			structField := reflectedType.Field(fieldIndex)
			if structField.PkgPath != "" {
				continue
			}
			envelope[structField.Name] = reflectedValue.Field(fieldIndex).Interface()
		}
		return
	}

	envelope["Data"] = templateData
}

func loadTranslationCatalog() map[string]map[string]string {
	translationBytes, err := fs.ReadFile(embeddedWebFiles, "web/translations.json")
	if err != nil {
		return map[string]map[string]string{}
	}
	var catalog map[string]map[string]string
	if json.Unmarshal(translationBytes, &catalog) != nil {
		return map[string]map[string]string{}
	}
	return catalog
}

func buildGuestContextMenuTemplates() map[string]string {
	templates := make(map[string]string, len(translationCatalog))
	for languageCode := range translationCatalog {
		templates[languageCode] = buildGuestContextMenuScriptTemplate(translationsForLanguageCode(languageCode))
	}
	if strings.TrimSpace(templates["ru"]) == "" {
		templates["ru"] = buildGuestContextMenuScriptTemplate(map[string]string{"menu_login": "Sign in"})
	}
	return templates
}

func translationsForRequest(r *http.Request) map[string]string {
	languageCode := preferredLanguageCode(r.Header.Get("Accept-Language"))
	return translationsForLanguageCode(languageCode)
}

func translationsForLanguageCode(languageCode string) map[string]string {
	selectedTranslations, found := translationCatalog[languageCode]
	englishTranslations := translationCatalog["en"]
	if !found {
		return englishTranslations
	}
	mergedTranslations := make(map[string]string, len(englishTranslations)+len(selectedTranslations))
	for translationKey, translationValue := range englishTranslations {
		mergedTranslations[translationKey] = translationValue
	}
	for translationKey, translationValue := range selectedTranslations {
		mergedTranslations[translationKey] = translationValue
	}
	return mergedTranslations
}

func preferredLanguageCode(acceptLanguageHeader string) string {
	normalizedHeader := strings.ToLower(strings.TrimSpace(acceptLanguageHeader))
	if normalizedHeader == "" {
		return "ru"
	}
	bestLanguageCode := "ru"
	bestWeight := -1.0
	supportedLanguageCodes := map[string]struct{}{
		"en": {}, "fr": {}, "ru": {}, "ja": {}, "it": {}, "sv": {}, "fi": {}, "mn": {},
		"zh": {}, "he": {}, "fa": {}, "de": {}, "tr": {}, "kk": {}, "es": {}, "pt": {},
	}
	for _, languageEntry := range strings.Split(normalizedHeader, ",") {
		parts := strings.Split(languageEntry, ";")
		baseCode := strings.TrimSpace(strings.Split(strings.TrimSpace(parts[0]), "-")[0])
		if _, supported := supportedLanguageCodes[baseCode]; !supported {
			continue
		}
		weight := 1.0
		for _, parameterEntry := range parts[1:] {
			parameter := strings.TrimSpace(parameterEntry)
			if !strings.HasPrefix(parameter, "q=") {
				continue
			}
			parsedWeight, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(parameter, "q=")), 64)
			if err != nil || parsedWeight < 0 || parsedWeight > 1 {
				continue
			}
			weight = parsedWeight
			break
		}
		if weight > bestWeight {
			bestWeight = weight
			bestLanguageCode = baseCode
		}
	}
	return bestLanguageCode
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

func safeRelativeAssetPath(rawPath string) string {
	cleanedPath := path.Clean("/" + strings.TrimSpace(rawPath))
	cleanedPath = strings.TrimPrefix(cleanedPath, "/")
	if cleanedPath == "" || cleanedPath == "." || strings.Contains(cleanedPath, "..") {
		return ""
	}
	return cleanedPath
}

func contentHashName(fileBytes []byte, extension string) (string, error) {
	if extension == "" {
		return "", errors.New("missing extension")
	}
	hashedBytes := sha256.Sum256(fileBytes)
	return hex.EncodeToString(hashedBytes[:]) + extension, nil
}

func resourceExtension(rawRef string) string {
	return grabber.ResourceExtension(rawRef)
}

func normalizedResourceContentType(contentTypeHeader string) string {
	return strings.ToLower(strings.TrimSpace(strings.Split(contentTypeHeader, ";")[0]))
}

func resourceKindFromContentType(contentType string) string {
	switch {
	case contentType == "text/css":
		return "style"
	case strings.Contains(contentType, "javascript"), strings.Contains(contentType, "ecmascript"):
		return "script"
	case strings.HasPrefix(contentType, "image/"):
		return "image"
	case strings.HasPrefix(contentType, "font/"), strings.Contains(contentType, "font"):
		return "font"
	case strings.HasPrefix(contentType, "video/"):
		return "video"
	case strings.HasPrefix(contentType, "audio/"):
		return "audio"
	case strings.HasPrefix(contentType, "text/"), strings.HasPrefix(contentType, "application/"):
		return "file"
	default:
		return ""
	}
}

func resourceExtensionFromContentType(contentType string) string {
	switch contentType {
	case "text/css":
		return ".css"
	case "application/javascript", "text/javascript", "application/x-javascript":
		return ".js"
	case "application/ecmascript", "text/ecmascript":
		return ".mjs"
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/svg+xml":
		return ".svg"
	case "image/webp":
		return ".webp"
	case "image/x-icon", "image/vnd.microsoft.icon":
		return ".ico"
	case "font/woff":
		return ".woff"
	case "font/woff2":
		return ".woff2"
	case "font/ttf", "application/x-font-ttf":
		return ".ttf"
	case "font/otf", "application/x-font-opentype":
		return ".otf"
	case "application/vnd.ms-fontobject":
		return ".eot"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "video/quicktime":
		return ".mov"
	case "audio/mpeg":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "audio/wav", "audio/wave", "audio/x-wav":
		return ".wav"
	}
	extensions, err := mime.ExtensionsByType(contentType)
	if err != nil || len(extensions) == 0 {
		return ""
	}
	sort.Strings(extensions)
	return strings.ToLower(strings.TrimSpace(extensions[0]))
}

func (a *App) mirrorRemotePage(domain, pagePath, sourceURL string, pageURL *url.URL, fallbackHTML, progressToken string, selectedResourceURLs map[string]struct{}, sourceIP string) string {
	spider, html := prepareSinglePageImport(domain, pagePath, sourceURL, pageURL, fallbackHTML, a.grabTracker, progressToken, selectedResourceURLs, grabSourceOptions{IP: sourceIP})
	_ = a.persistSpiderAssets(spider, pagePath)
	a.grabTracker.publish(grabProgressEvent{Token: progressToken, Stage: "done", FoundTotal: spider.foundTotal, DownloadedTotal: spider.downloadedTotal, CompletedPercent: 100})
	return html
}

func (a *App) persistSpiderAssets(spider *pageSpider, pagePath string) error {
	baseDir := a.domainFilesDirForDomain(spider.domain)
	_ = os.MkdirAll(baseDir, 0o755)
	ownerPath := cleanPath(pagePath)
	wroteFile := false
	for _, resource := range spider.resources {
		if !resource.persist || strings.TrimSpace(resource.assetPath) == "" {
			continue
		}
		assetReference := publicAssetReferenceFromPath(resource.assetPath)
		if assetReference == "" {
			continue
		}
		targetFilePath := filepath.Join(baseDir, filepath.FromSlash(assetReference))
		_ = os.MkdirAll(filepath.Dir(targetFilePath), 0o755)
		_ = os.WriteFile(targetFilePath, resource.content, 0o644)
		wroteFile = true
		resourceContentType := resource.contentType
		if strings.TrimSpace(resourceContentType) == "" {
			resourceContentType = mime.TypeByExtension(path.Ext(assetReference))
		}
		a.upsertFileMetadata(context.Background(), domainStorageName(spider.domain), assetReference, ownerPath, int64(len(resource.content)), resourceContentType, "import")
	}
	if wroteFile && a != nil && a.db != nil {
		a.rebuildDomainStorageUsage(context.Background(), spider.domain)
	}
	return nil
}

func publicAssetReferenceFromPath(assetPath string) string {
	cleanedPath := path.Clean("/" + strings.TrimSpace(assetPath))
	if strings.HasPrefix(cleanedPath, "/p/") {
		return safeRelativeAssetPath(strings.TrimPrefix(cleanedPath, "/p/"))
	}
	if prefixIndex := strings.Index(cleanedPath, "/p/"); prefixIndex > 0 {
		return safeRelativeAssetPath(cleanedPath[prefixIndex+len("/p/"):])
	}
	return safeRelativeAssetPath(strings.TrimPrefix(cleanedPath, "/"))
}

type mirroredResource struct {
	url         string
	content     []byte
	assetPath   string
	contentType string
	persist     bool
}

type pageSpider struct {
	domain               string
	pageURL              *url.URL
	maxDepth             int
	client               *http.Client
	sourceOptions        grabSourceOptions
	resources            map[string]*mirroredResource
	inFlight             map[string]bool
	selectedResourceURLs map[string]struct{}
	publicAssetBasePath  string
	documentURLRewriter  func(string) (string, bool)
	tracker              *grabProgressTracker
	progressToken        string
	foundTotal           int
	downloadedTotal      int
}

type grabReferenceContext = grabber.ReferenceContext

const (
	grabReferenceDocument   = grabber.ReferenceDocument
	grabReferenceJavaScript = grabber.ReferenceJavaScript
)

var (
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Timeout: 20 * time.Second, Transport: newGrabHTTPTransport()}
	}
)

func newGrabHTTPTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	transport.TLSClientConfig.InsecureSkipVerify = true
	return transport
}

func newGrabHTTPClientForServerIP(sourceHost, sourceIP string) *http.Client {
	trimmedSourceIP := strings.TrimSpace(sourceIP)
	if trimmedSourceIP == "" {
		return newGrabHTTPClient()
	}
	parsedIP, sourcePort := splitGrabSourceIP(trimmedSourceIP)
	if parsedIP == nil {
		return newGrabHTTPClient()
	}
	sourceHost = strings.TrimSpace(strings.ToLower(sourceHost))
	transport := newGrabHTTPTransport()
	transport.Proxy = nil
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, splitErr := net.SplitHostPort(address)
		if splitErr == nil && strings.EqualFold(host, sourceHost) {
			if sourcePort != "" {
				port = sourcePort
			}
			address = net.JoinHostPort(parsedIP.String(), port)
		}
		return dialer.DialContext(ctx, network, address)
	}
	return &http.Client{Timeout: 20 * time.Second, Transport: transport}
}

func newPageSpider(domain string, pageURL *url.URL, maxDepth int, tracker *grabProgressTracker, progressToken string, sourceOptions grabSourceOptions) *pageSpider {
	return &pageSpider{
		domain:        domain,
		pageURL:       pageURL,
		maxDepth:      maxDepth,
		client:        newGrabHTTPClientForServerIP(pageURL.Hostname(), sourceOptions.IP),
		sourceOptions: sourceOptions,
		resources:     make(map[string]*mirroredResource),
		inFlight:      make(map[string]bool),
		tracker:       tracker,
		progressToken: progressToken,
	}
}

func (spider *pageSpider) fetchResource(rawURL string, baseURL *url.URL, depth int, persist bool) (*mirroredResource, error) {
	if depth > spider.maxDepth {
		return nil, errors.New("max depth reached")
	}
	normalizedURL, blocked := spider.normalizeURL(rawURL, baseURL)
	if blocked || normalizedURL == "" {
		return nil, errors.New("unsupported resource url")
	}
	if spider.shouldSkipMirrorResource(normalizedURL) {
		return nil, errors.New("unsupported resource url")
	}
	persist = persist && spider.shouldPersistResource(normalizedURL)
	if existing, found := spider.resources[normalizedURL]; found {
		if persist {
			existing.persist = true
		}
		return existing, nil
	}
	spider.foundTotal++
	spider.publishProgress("found", normalizedURL, 0)
	if spider.inFlight[normalizedURL] {
		return nil, errors.New("resource is already in flight")
	}
	spider.inFlight[normalizedURL] = true
	defer delete(spider.inFlight, normalizedURL)
	request, _ := http.NewRequest(http.MethodGet, normalizedURL, nil)
	applyGrabRequestHeaders(request, spider.sourceOptions)
	response, err := spider.client.Do(request)
	if err != nil {
		spider.resources[normalizedURL] = &mirroredResource{url: normalizedURL, persist: persist}
		spider.publishResourceProgress("error", normalizedURL, 0, 0, -1)
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		spider.resources[normalizedURL] = &mirroredResource{url: normalizedURL, persist: persist}
		spider.publishResourceProgress("error", normalizedURL, 0, 0, response.ContentLength)
		return nil, fmt.Errorf("resource download failed: %s", response.Status)
	}
	resourceContentType := normalizedResourceContentType(response.Header.Get("Content-Type"))
	if !spider.isAllowedResourceContentType(normalizedURL, resourceContentType) {
		spider.resources[normalizedURL] = &mirroredResource{url: normalizedURL, persist: persist}
		spider.publishResourceProgress("error", normalizedURL, 0, 0, response.ContentLength)
		return nil, fmt.Errorf("resource content-type rejected: %s", response.Header.Get("Content-Type"))
	}
	body, err := spider.readResourceBody(response.Body, normalizedURL, response.ContentLength)
	if err != nil {
		spider.resources[normalizedURL] = &mirroredResource{url: normalizedURL, persist: persist}
		spider.publishResourceProgress("error", normalizedURL, 0, 0, response.ContentLength)
		return nil, err
	}
	resource := &mirroredResource{url: normalizedURL, content: body, contentType: resourceContentType, persist: persist}
	if persist {
		assetPath := spider.assetPathFor(body, normalizedURL, resourceContentType)
		if assetPath != "" {
			resource.assetPath = assetPath
		}
	}
	spider.resources[normalizedURL] = resource
	spider.downloadedTotal++
	spider.publishResourceProgress("downloaded", normalizedURL, 100, int64(len(body)), response.ContentLength)
	spider.rewriteNestedResources(resource, depth+1, resourceContentType)
	return resource, nil
}

func (spider *pageSpider) publishProgress(stage, currentURL string, currentPercent int) {
	spider.publishResourceProgress(stage, currentURL, currentPercent, 0, -1)
}

func (spider *pageSpider) publishResourceProgress(stage, currentURL string, currentPercent int, downloadedBytes, sizeBytes int64) {
	if spider.tracker == nil || strings.TrimSpace(spider.progressToken) == "" {
		return
	}
	completedPercent := 0
	if spider.foundTotal > 0 {
		completedPercent = spider.downloadedTotal * 100 / spider.foundTotal
	}
	spider.tracker.publish(grabProgressEvent{
		Token: spider.progressToken, Stage: stage, FoundTotal: spider.foundTotal, DownloadedTotal: spider.downloadedTotal,
		CurrentURL: currentURL, CurrentPercent: currentPercent, CurrentDownloadedBytes: downloadedBytes, CurrentSizeBytes: sizeBytes, CompletedPercent: completedPercent,
	})
}

func (spider *pageSpider) readResourceBody(reader io.Reader, resourceURL string, sizeBytes int64) ([]byte, error) {
	var bodyBuffer bytes.Buffer
	buffer := make([]byte, 32*1024)
	downloadedBytes := int64(0)
	lastPercent := -1
	spider.publishResourceProgress("downloading", resourceURL, 0, 0, sizeBytes)
	for {
		readCount, readErr := reader.Read(buffer)
		if readCount > 0 {
			downloadedBytes += int64(readCount)
			if _, writeErr := bodyBuffer.Write(buffer[:readCount]); writeErr != nil {
				return nil, writeErr
			}
			currentPercent := resourcePercent(downloadedBytes, sizeBytes)
			if currentPercent != lastPercent {
				spider.publishResourceProgress("downloading", resourceURL, currentPercent, downloadedBytes, sizeBytes)
				lastPercent = currentPercent
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	return bodyBuffer.Bytes(), nil
}

func resourcePercent(downloadedBytes, sizeBytes int64) int {
	if sizeBytes <= 0 {
		return 0
	}
	percent := int(downloadedBytes * 100 / sizeBytes)
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

func (spider *pageSpider) fetchSelectedResources() {
	if spider.selectedResourceURLs == nil || len(spider.selectedResourceURLs) == 0 {
		return
	}
	selectedURLs := make([]string, 0, len(spider.selectedResourceURLs))
	for selectedURL := range spider.selectedResourceURLs {
		selectedURLs = append(selectedURLs, selectedURL)
	}
	sort.Strings(selectedURLs)
	for _, selectedURL := range selectedURLs {
		if existing, found := spider.resources[selectedURL]; found && existing.persist {
			continue
		}
		_, _ = spider.fetchResource(selectedURL, spider.pageURL, 0, true)
	}
}

func (spider *pageSpider) rewriteNestedResources(resource *mirroredResource, depth int, contentType string) {
	isHTML := strings.Contains(contentType, "text/html")
	isCSS := strings.Contains(contentType, "text/css")
	isJS := strings.Contains(contentType, "javascript") || strings.Contains(contentType, "ecmascript") || strings.HasSuffix(resourceExtension(resource.url), ".js") || strings.HasSuffix(resourceExtension(resource.url), ".mjs")
	if !(isHTML || isCSS || isJS) {
		return
	}
	source := string(resource.content)
	if isHTML {
		source = cleanupLegacyImportedSiteBrushHTML(source)
		source = neutralizeImportedHostLanguageRedirects(source)
	}
	rewritten := source
	parser := spider.grabberParser()
	if isHTML || isCSS {
		rewritten = parser.RewriteTextReferences(source, resource.url, depth)
	} else if isJS {
		rewritten = parser.RewriteJavaScriptReferences(source, resource.url, depth)
	}
	resource.content = []byte(rewritten)
}

func (spider *pageSpider) grabberParser() grabber.Parser {
	return grabber.Parser{
		NormalizeURL:                         spider.normalizeURLForContext,
		RewriteResourceReference:             spider.rewriteResourceReferenceForContext,
		RewriteDocumentResourceReference:     spider.rewriteDocumentResourceReference,
		DocumentURLRewriter:                  spider.documentURLRewriter,
		ShouldBlankEmbeddedDocumentReference: spider.shouldBlankEmbeddedDocumentReference,
		ShouldRewriteImageAltResource:        spider.shouldRewriteImageAltResourceReference,
	}
}

func (spider *pageSpider) rewriteImportedPagesStaticURLTextReferences(importedPages []wholeSiteImportedPage) {
	for pageIndex := range importedPages {
		baseURL, _ := url.Parse(importedPages[pageIndex].SourceURL)
		importedPages[pageIndex].HTML = spider.rewriteStaticURLTextReferences(importedPages[pageIndex].HTML, baseURL, 0)
	}
}

func (spider *pageSpider) rewriteStaticURLTextReferences(source string, baseURL *url.URL, depth int) string {
	return spider.grabberParser().RewriteStaticURLTextReferences(source, baseURL, depth)
}

func (spider *pageSpider) shouldRewriteImageAltResourceReference(rawRef string, baseURL *url.URL) bool {
	normalizedURL, blocked := spider.normalizeURL(rawRef, baseURL)
	if blocked || normalizedURL == "" {
		return false
	}
	return hasAllowedGrabResourceExtension(normalizedURL)
}

func (spider *pageSpider) rewriteResourceReference(rawRef string, baseURL *url.URL, depth int) string {
	normalizedURL, blocked := spider.normalizeURL(rawRef, baseURL)
	if blocked || normalizedURL == "" {
		return rawRef
	}
	if !spider.shouldPersistResource(normalizedURL) {
		return normalizedURL
	}
	dependency, err := spider.fetchResource(rawRef, baseURL, depth, true)
	if err != nil || dependency == nil || dependency.assetPath == "" {
		return normalizedURL
	}
	return normalizeMirroredAssetReference(dependency.assetPath)
}

func (spider *pageSpider) rewriteDocumentResourceReference(rawRef string, baseURL *url.URL, depth int) string {
	normalizedURL, blocked := spider.normalizeURL(rawRef, baseURL)
	if blocked || normalizedURL == "" {
		return rawRef
	}
	if !spider.shouldPersistResource(normalizedURL) {
		return rawRef
	}
	dependency, err := spider.fetchResource(rawRef, baseURL, depth, true)
	if err != nil || dependency == nil || dependency.assetPath == "" {
		return rawRef
	}
	return normalizeMirroredAssetReference(dependency.assetPath)
}

func (spider *pageSpider) shouldBlankEmbeddedDocumentReference(tagName, normalizedURL string) bool {
	switch tagName {
	case "iframe", "embed", "object":
	default:
		return false
	}
	if normalizedURL == "" || spider.shouldSkipMirrorResource(normalizedURL) {
		return false
	}
	parsedURL, err := url.Parse(normalizedURL)
	if err != nil {
		return false
	}
	if spider.pageURL == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(parsedURL.Hostname()), strings.TrimSpace(spider.pageURL.Hostname())) {
		return false
	}
	extension := strings.ToLower(path.Ext(parsedURL.Path))
	if extension == "" {
		return true
	}
	switch extension {
	case ".htm", ".html", ".xhtml", ".php", ".asp", ".aspx", ".jsp", ".cgi":
		return true
	default:
		return false
	}
}

func (spider *pageSpider) normalizeURL(rawRef string, baseURL *url.URL) (string, bool) {
	return spider.normalizeURLForContext(rawRef, baseURL, grabReferenceDocument)
}

func (spider *pageSpider) normalizeURLForContext(rawRef string, baseURL *url.URL, referenceContext grabReferenceContext) (string, bool) {
	return grabber.NormalizeURL(rawRef, baseURL, referenceContext)
}

func (spider *pageSpider) rewriteResourceReferenceForContext(rawRef string, baseURL *url.URL, depth int, referenceContext grabReferenceContext) string {
	normalizedURL, blocked := spider.normalizeURLForContext(rawRef, baseURL, referenceContext)
	if blocked || normalizedURL == "" {
		return rawRef
	}
	if !spider.shouldPersistResource(normalizedURL) {
		return normalizedURL
	}
	dependency, err := spider.fetchResource(normalizedURL, baseURL, depth, true)
	if err != nil || dependency == nil || dependency.assetPath == "" {
		return normalizedURL
	}
	return normalizeMirroredAssetReference(dependency.assetPath)
}

func normalizeMirroredAssetReference(assetPath string) string {
	trimmedPath := strings.TrimSpace(assetPath)
	if trimmedPath == "" {
		return ""
	}
	if strings.HasPrefix(trimmedPath, "http://") || strings.HasPrefix(trimmedPath, "https://") || strings.HasPrefix(trimmedPath, "data:") || strings.HasPrefix(trimmedPath, "blob:") {
		return trimmedPath
	}
	if strings.HasPrefix(trimmedPath, "//") {
		return "/" + strings.TrimLeft(trimmedPath, "/")
	}
	return trimmedPath
}

func shouldResolveJavaScriptReferenceAgainstOriginRoot(rawRef string, baseURL *url.URL) bool {
	normalizedURL, blocked := grabber.NormalizeURL(rawRef, baseURL, grabber.ReferenceJavaScript)
	documentURL, documentBlocked := grabber.NormalizeURL(rawRef, baseURL, grabber.ReferenceDocument)
	return !blocked && !documentBlocked && normalizedURL != documentURL
}

func originRootURL(baseURL *url.URL) *url.URL {
	return grabber.OriginRootURL(baseURL)
}

func firstPathSegment(rawPath string) string {
	return grabber.FirstPathSegment(rawPath)
}

func isSuspiciousGrabReference(rawRef string) bool {
	return grabber.IsSuspiciousReference(rawRef)
}

func (spider *pageSpider) shouldSkipMirrorResource(resourceURL string) bool {
	parsedURL, err := url.Parse(resourceURL)
	if err != nil {
		return true
	}
	hostName := strings.ToLower(strings.TrimSpace(parsedURL.Hostname()))
	pathName := strings.ToLower(strings.TrimSpace(parsedURL.Path))
	if hostName == "" {
		return false
	}
	for _, blockedHostFragment := range []string{"youtube.com", "youtu.be", "youtube-nocookie.com", "googlevideo.com", "doubleclick.net", "googletagmanager.com", "google-analytics.com", "adservice.google.com", "connect.facebook.net", "facebook.com", "clarity.ms", "hotjar.com", "segment.com", "mixpanel.com", "matomo", "outbrain.com", "taboola.com"} {
		if strings.Contains(hostName, blockedHostFragment) {
			return true
		}
	}
	for _, blockedPathFragment := range []string{"/analytics", "/gtag", "/gtm", "/ads", "/adservice", "/pixel", "/tracking"} {
		if strings.Contains(pathName, blockedPathFragment) {
			return true
		}
	}
	return false
}

func (spider *pageSpider) isAllowedResourceContentType(resourceURL, contentType string) bool {
	if contentType == "" {
		return hasAllowedGrabResourceExtension(resourceURL)
	}
	switch contentType {
	case "text/html", "application/xhtml+xml":
		return false
	}
	resourceKind := resourceKindFromURL(resourceURL)
	if resourceKind == "" {
		resourceKind = resourceKindFromContentType(contentType)
	}
	switch resourceKind {
	case "style":
		return contentType == "text/css"
	case "script":
		return strings.Contains(contentType, "javascript") || strings.Contains(contentType, "ecmascript")
	case "font":
		return strings.HasPrefix(contentType, "font/") || strings.Contains(contentType, "font") || strings.Contains(contentType, "octet-stream")
	case "image":
		return strings.HasPrefix(contentType, "image/")
	case "video":
		return strings.HasPrefix(contentType, "video/")
	case "audio":
		return strings.HasPrefix(contentType, "audio/")
	case "file":
		return true
	default:
		return false
	}
}

func resourceKindFromURL(resourceURL string) string {
	return grabber.ResourceKindFromURL(resourceURL)
}

func hasAllowedGrabResourceExtension(resourceURL string) bool {
	return grabber.HasAllowedResourceExtension(resourceURL)
}

func (spider *pageSpider) shouldPersistResource(normalizedURL string) bool {
	if spider.selectedResourceURLs == nil {
		return true
	}
	if len(spider.selectedResourceURLs) == 0 {
		return false
	}
	_, selected := spider.selectedResourceURLs[normalizedURL]
	return selected
}

func (spider *pageSpider) assetPathFor(fileBytes []byte, sourceURL, contentType string) string {
	extension := resourceExtension(sourceURL)
	if extension == "" {
		extension = resourceExtensionFromContentType(contentType)
	}
	if extension == "" {
		extension = ".bin"
	}
	hashedFileName, err := contentHashName(fileBytes, extension)
	if err != nil {
		return ""
	}
	basePath := cleanPath(spider.publicAssetBasePath)
	if basePath == "/" {
		return "/p/" + hashedFileName
	}
	return strings.TrimRight(basePath, "/") + "/p/" + hashedFileName
}

type grabTrackerRequest struct {
	action string
	token  string
	stream chan grabProgressEvent
	event  grabProgressEvent
}

type grabProgressTracker struct {
	requests chan grabTrackerRequest
}

type publishTrackerRequest struct {
	action string
	token  string
	stream chan publishProgressEvent
	event  publishProgressEvent
}

type publishProgressTracker struct {
	requests chan publishTrackerRequest
}

type webSocketTextWriter struct {
	connection net.Conn
}

func newGrabProgressTracker() *grabProgressTracker {
	tracker := &grabProgressTracker{requests: make(chan grabTrackerRequest)}
	go tracker.loop()
	return tracker
}

func (tracker *grabProgressTracker) subscribe(token string) chan grabProgressEvent {
	stream := make(chan grabProgressEvent, 32)
	tracker.requests <- grabTrackerRequest{action: "subscribe", token: token, stream: stream}
	return stream
}
func (tracker *grabProgressTracker) unsubscribe(token string, stream chan grabProgressEvent) {
	tracker.requests <- grabTrackerRequest{action: "unsubscribe", token: token, stream: stream}
}
func (tracker *grabProgressTracker) publish(event grabProgressEvent) {
	tracker.requests <- grabTrackerRequest{action: "publish", token: event.Token, event: event}
}
func (tracker *grabProgressTracker) loop() {
	subscribersByToken := make(map[string]map[chan grabProgressEvent]struct{})
	for request := range tracker.requests {
		switch request.action {
		case "subscribe":
			if _, exists := subscribersByToken[request.token]; !exists {
				subscribersByToken[request.token] = make(map[chan grabProgressEvent]struct{})
			}
			subscribersByToken[request.token][request.stream] = struct{}{}
		case "unsubscribe":
			group := subscribersByToken[request.token]
			delete(group, request.stream)
			close(request.stream)
		case "publish":
			for stream := range subscribersByToken[request.token] {
				select {
				case stream <- request.event:
				default:
				}
			}
		}
	}
}

func newPublishProgressTracker() *publishProgressTracker {
	tracker := &publishProgressTracker{requests: make(chan publishTrackerRequest)}
	go tracker.loop()
	return tracker
}

var fallbackPublishProgressTracker = newPublishProgressTracker()

func (a *App) activePublishTracker() *publishProgressTracker {
	if a != nil && a.publishTracker != nil {
		return a.publishTracker
	}
	return fallbackPublishProgressTracker
}

func (tracker *publishProgressTracker) subscribe(token string) chan publishProgressEvent {
	stream := make(chan publishProgressEvent, 32)
	tracker.requests <- publishTrackerRequest{action: "subscribe", token: token, stream: stream}
	return stream
}
func (tracker *publishProgressTracker) unsubscribe(token string, stream chan publishProgressEvent) {
	tracker.requests <- publishTrackerRequest{action: "unsubscribe", token: token, stream: stream}
}
func (tracker *publishProgressTracker) publish(event publishProgressEvent) {
	if strings.TrimSpace(event.Token) == "" {
		return
	}
	tracker.requests <- publishTrackerRequest{action: "publish", token: event.Token, event: event}
}
func (tracker *publishProgressTracker) loop() {
	subscribersByToken := make(map[string]map[chan publishProgressEvent]struct{})
	for request := range tracker.requests {
		switch request.action {
		case "subscribe":
			if _, exists := subscribersByToken[request.token]; !exists {
				subscribersByToken[request.token] = make(map[chan publishProgressEvent]struct{})
			}
			subscribersByToken[request.token][request.stream] = struct{}{}
		case "unsubscribe":
			group := subscribersByToken[request.token]
			delete(group, request.stream)
			close(request.stream)
		case "publish":
			for stream := range subscribersByToken[request.token] {
				select {
				case stream <- request.event:
				default:
				}
			}
		}
	}
}

func upgradeToWebSocket(w http.ResponseWriter, r *http.Request) (*webSocketTextWriter, error) {
	if !strings.EqualFold(r.Header.Get("Connection"), "Upgrade") && !strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
		return nil, errors.New("missing websocket upgrade")
	}
	webSocketKey := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if webSocketKey == "" {
		return nil, errors.New("missing websocket key")
	}
	acceptSeed := webSocketKey + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	acceptHash := sha1.Sum([]byte(acceptSeed))
	acceptValue := base64.StdEncoding.EncodeToString(acceptHash[:])
	hijacker, isHijacker := w.(http.Hijacker)
	if !isHijacker {
		return nil, errors.New("hijack is not supported")
	}
	connection, bufferedReadWriter, err := hijacker.Hijack()
	if err != nil {
		return nil, err
	}
	if _, err := bufferedReadWriter.WriteString("HTTP/1.1 101 Switching Protocols\r\n"); err != nil {
		connection.Close()
		return nil, err
	}
	_, _ = bufferedReadWriter.WriteString("Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + acceptValue + "\r\n\r\n")
	_ = bufferedReadWriter.Flush()
	return &webSocketTextWriter{connection: connection}, nil
}

func (writer *webSocketTextWriter) Close() error { return writer.connection.Close() }
func (writer *webSocketTextWriter) WriteText(payload []byte) error {
	header := []byte{0x81}
	payloadLength := len(payload)
	if payloadLength < 126 {
		header = append(header, byte(payloadLength))
	} else if payloadLength <= 65535 {
		header = append(header, 126, 0, 0)
		binary.BigEndian.PutUint16(header[len(header)-2:], uint16(payloadLength))
	} else {
		header = append(header, 127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[len(header)-8:], uint64(payloadLength))
	}
	if _, err := writer.connection.Write(header); err != nil {
		return err
	}
	_, err := writer.connection.Write(payload)
	return err
}

func domainFromRequest(r *http.Request) string {
	host := strings.TrimSpace(r.Host)
	if parsedHost, _, splitErr := net.SplitHostPort(host); splitErr == nil {
		host = parsedHost
	} else if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.Trim(host, "[]")
	}
	host = canonicalLocalDomain(host)
	if host == "" {
		return "localhost"
	}
	return host
}

func canonicalLocalDomain(rawDomain string) string {
	cleanDomain := strings.ToLower(strings.TrimSpace(rawDomain))
	cleanDomain = strings.Trim(cleanDomain, "[]")
	if cleanDomain == "" {
		return ""
	}
	if cleanDomain == "localhost" {
		return "localhost"
	}
	parsedIP := net.ParseIP(cleanDomain)
	if parsedIP != nil && parsedIP.IsLoopback() {
		return "localhost"
	}
	return cleanDomain
}

func (a *App) siteDomain(ctx context.Context, r *http.Request) string {
	requestDomain := domainFromRequest(r)
	var primaryDomain string
	err := a.db.QueryRowContext(ctx, `SELECT primary_domain FROM domain_aliases WHERE alias_domain=? AND is_verified=1 AND dns_a_ok=1`, requestDomain).Scan(&primaryDomain)
	if err != nil || strings.TrimSpace(primaryDomain) == "" {
		return requestDomain
	}
	return primaryDomain
}

var lookupTXTRecords = lookupAuthoritativeTXTRecords
var lookupIPRecords = lookupAuthoritativeIPRecords
var lookupServerExternalIP = detectServerExternalIP
var lookupServerInterfaceIPs = detectServerInterfaceIPs
var exchangeDNSMessage = exchangeDNSMessageWithServer

const automaticSSLRefreshInterval = time.Hour
const automaticSSLCertificateRenewBefore = 10 * 24 * time.Hour
const automaticSSLRetryDelayEligibility = 24 * time.Hour
const automaticSSLRetryDelayDNS = 6 * time.Hour
const automaticSSLRetryDelayChallenge = time.Hour
const automaticSSLRetryDelayACME = 12 * time.Hour

const authoritativeDNSMaxDepth = 16
const authoritativeDNSTimeout = 3 * time.Second

var authoritativeDNSRootServers = []string{
	"198.41.0.4",
	"199.9.14.201",
	"192.33.4.12",
	"199.7.91.13",
	"192.203.230.10",
	"192.5.5.241",
	"192.112.36.4",
	"198.97.190.53",
	"192.36.148.17",
	"192.58.128.30",
	"193.0.14.129",
	"199.7.83.42",
	"202.12.27.33",
}

type authoritativeDNSResponse struct {
	nameServerIP string
	message      dnsmessage.Message
	err          error
}

// Authoritative DNS checks bypass recursive resolvers so domain settings and
// ACME admission see fresh records directly from the delegated name servers.
func lookupAuthoritativeTXTRecords(domain string) ([]string, error) {
	message, err := lookupAuthoritativeDNSMessage(context.Background(), domain, dnsmessage.TypeTXT, 0)
	if err != nil {
		return nil, err
	}
	txtRecords := make([]string, 0, len(message.Answers))
	for _, answer := range message.Answers {
		txtResource, ok := answer.Body.(*dnsmessage.TXTResource)
		if !ok {
			continue
		}
		txtRecords = append(txtRecords, strings.Join(txtResource.TXT, ""))
	}
	if len(txtRecords) == 0 {
		return nil, fmt.Errorf("authoritative TXT record for %s not found", domain)
	}
	return txtRecords, nil
}

func lookupAuthoritativeIPRecords(domain string) ([]net.IP, error) {
	ipRecords := make([]net.IP, 0, 4)
	lookupErrs := make([]error, 0, 2)
	aMessage, aErr := lookupAuthoritativeDNSMessage(context.Background(), domain, dnsmessage.TypeA, 0)
	if aErr != nil {
		lookupErrs = append(lookupErrs, aErr)
	} else {
		ipRecords = append(ipRecords, ipRecordsFromDNSMessage(aMessage)...)
	}
	aaaaMessage, aaaaErr := lookupAuthoritativeDNSMessage(context.Background(), domain, dnsmessage.TypeAAAA, 0)
	if aaaaErr != nil {
		lookupErrs = append(lookupErrs, aaaaErr)
	} else {
		ipRecords = append(ipRecords, ipRecordsFromDNSMessage(aaaaMessage)...)
	}
	if len(ipRecords) == 0 {
		return nil, errors.Join(lookupErrs...)
	}
	return dedupeIPRecords(ipRecords), nil
}

func lookupAuthoritativeDNSMessage(ctx context.Context, domain string, recordType dnsmessage.Type, depth int) (dnsmessage.Message, error) {
	domainName, err := dnsMessageName(domain)
	if err != nil {
		return dnsmessage.Message{}, err
	}
	return lookupAuthoritativeDNSMessageForName(ctx, domainName, recordType, authoritativeDNSRootServers, depth)
}

func lookupAuthoritativeDNSMessageForName(ctx context.Context, domainName dnsmessage.Name, recordType dnsmessage.Type, nameServerIPs []string, depth int) (dnsmessage.Message, error) {
	if depth > authoritativeDNSMaxDepth {
		return dnsmessage.Message{}, fmt.Errorf("authoritative DNS lookup exceeded recursion depth for %s", domainName.String())
	}
	currentNameServerIPs := append([]string(nil), nameServerIPs...)
	for referralDepth := 0; referralDepth < authoritativeDNSMaxDepth; referralDepth++ {
		nameServerIP, message, err := queryAuthoritativeNameServers(ctx, currentNameServerIPs, domainName, recordType)
		if err != nil {
			return dnsmessage.Message{}, err
		}
		if message.Header.RCode == dnsmessage.RCodeNameError {
			return dnsmessage.Message{}, fmt.Errorf("authoritative DNS name %s does not exist", domainName.String())
		}
		if messageHasAnswer(message, domainName, recordType) {
			return message, nil
		}
		if cnameName, ok := cnameAnswerTarget(message, domainName); ok {
			return lookupAuthoritativeDNSMessageForName(ctx, cnameName, recordType, authoritativeDNSRootServers, depth+1)
		}
		nextNameServerIPs, err := referralNameServerIPs(ctx, message, depth+1)
		if err != nil {
			return dnsmessage.Message{}, err
		}
		if len(nextNameServerIPs) == 0 {
			if messageHasSOA(message) {
				return dnsmessage.Message{}, fmt.Errorf("authoritative DNS record %s %s not found", domainName.String(), recordType.String())
			}
			return dnsmessage.Message{}, fmt.Errorf("authoritative DNS server %s returned no usable referral for %s", nameServerIP, domainName.String())
		}
		currentNameServerIPs = nextNameServerIPs
	}
	return dnsmessage.Message{}, fmt.Errorf("authoritative DNS lookup exceeded referral depth for %s", domainName.String())
}

func queryAuthoritativeNameServers(ctx context.Context, nameServerIPs []string, domainName dnsmessage.Name, recordType dnsmessage.Type) (string, dnsmessage.Message, error) {
	if len(nameServerIPs) == 0 {
		return "", dnsmessage.Message{}, fmt.Errorf("authoritative DNS has no name servers for %s", domainName.String())
	}
	queryContext, cancel := context.WithCancel(ctx)
	defer cancel()
	responses := make(chan authoritativeDNSResponse, len(nameServerIPs))
	for _, nameServerIP := range nameServerIPs {
		go func(currentNameServerIP string) {
			message, err := exchangeDNSMessage(queryContext, currentNameServerIP, domainName, recordType)
			select {
			case responses <- authoritativeDNSResponse{nameServerIP: currentNameServerIP, message: message, err: err}:
			case <-queryContext.Done():
			}
		}(nameServerIP)
	}
	var lastErr error
	var fallbackResponse authoritativeDNSResponse
	fallbackAvailable := false
	for responseIndex := 0; responseIndex < len(nameServerIPs); responseIndex++ {
		response := <-responses
		if response.err != nil {
			lastErr = response.err
			continue
		}
		if response.message.Header.RCode == dnsmessage.RCodeNameError {
			cancel()
			return response.nameServerIP, response.message, nil
		}
		if response.message.Header.RCode == dnsmessage.RCodeSuccess {
			if messageHasAnswer(response.message, domainName, recordType) {
				cancel()
				return response.nameServerIP, response.message, nil
			}
			if _, ok := cnameAnswerTarget(response.message, domainName); ok {
				cancel()
				return response.nameServerIP, response.message, nil
			}
			if len(nameServerNamesFromAuthorities(response.message)) > 0 {
				cancel()
				return response.nameServerIP, response.message, nil
			}
			if !fallbackAvailable {
				fallbackResponse = response
				fallbackAvailable = true
			}
			continue
		}
		lastErr = fmt.Errorf("authoritative DNS server %s returned rcode %v for %s", response.nameServerIP, response.message.Header.RCode, domainName.String())
	}
	if fallbackAvailable {
		return fallbackResponse.nameServerIP, fallbackResponse.message, nil
	}
	if lastErr != nil {
		return "", dnsmessage.Message{}, lastErr
	}
	return "", dnsmessage.Message{}, fmt.Errorf("authoritative DNS lookup failed for %s", domainName.String())
}

func exchangeDNSMessageWithServer(ctx context.Context, nameServerIP string, domainName dnsmessage.Name, recordType dnsmessage.Type) (dnsmessage.Message, error) {
	queryID, err := randomDNSQueryID()
	if err != nil {
		return dnsmessage.Message{}, err
	}
	queryMessage := dnsmessage.Message{
		Header: dnsmessage.Header{
			ID:               queryID,
			RecursionDesired: false,
		},
		Questions: []dnsmessage.Question{{
			Name:  domainName,
			Type:  recordType,
			Class: dnsmessage.ClassINET,
		}},
	}
	queryBytes, err := queryMessage.Pack()
	if err != nil {
		return dnsmessage.Message{}, err
	}
	message, err := exchangeDNSPacket(ctx, "udp", nameServerIP, queryBytes, queryID)
	if err == nil && !message.Header.Truncated {
		return message, nil
	}
	return exchangeDNSPacket(ctx, "tcp", nameServerIP, queryBytes, queryID)
}

func exchangeDNSPacket(ctx context.Context, network string, nameServerIP string, queryBytes []byte, queryID uint16) (dnsmessage.Message, error) {
	queryContext, cancel := context.WithTimeout(ctx, authoritativeDNSTimeout)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(queryContext, network, net.JoinHostPort(nameServerIP, "53"))
	if err != nil {
		return dnsmessage.Message{}, err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(authoritativeDNSTimeout))
	if network == "tcp" {
		lengthPrefix := []byte{byte(len(queryBytes) >> 8), byte(len(queryBytes))}
		if _, err := connection.Write(append(lengthPrefix, queryBytes...)); err != nil {
			return dnsmessage.Message{}, err
		}
		responseLengthPrefix := make([]byte, 2)
		if _, err := io.ReadFull(connection, responseLengthPrefix); err != nil {
			return dnsmessage.Message{}, err
		}
		responseLength := int(responseLengthPrefix[0])<<8 | int(responseLengthPrefix[1])
		responseBytes := make([]byte, responseLength)
		if _, err := io.ReadFull(connection, responseBytes); err != nil {
			return dnsmessage.Message{}, err
		}
		return unpackDNSResponse(responseBytes, queryID)
	}
	if _, err := connection.Write(queryBytes); err != nil {
		return dnsmessage.Message{}, err
	}
	responseBytes := make([]byte, 4096)
	responseLength, err := connection.Read(responseBytes)
	if err != nil {
		return dnsmessage.Message{}, err
	}
	return unpackDNSResponse(responseBytes[:responseLength], queryID)
}

func unpackDNSResponse(responseBytes []byte, queryID uint16) (dnsmessage.Message, error) {
	var message dnsmessage.Message
	if err := message.Unpack(responseBytes); err != nil {
		return dnsmessage.Message{}, err
	}
	if message.Header.ID != queryID {
		return dnsmessage.Message{}, fmt.Errorf("authoritative DNS response id mismatch")
	}
	return message, nil
}

func randomDNSQueryID() (uint16, error) {
	var queryIDBytes [2]byte
	if _, err := rand.Read(queryIDBytes[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(queryIDBytes[:]), nil
}

func dnsMessageName(domain string) (dnsmessage.Name, error) {
	cleanDomain := normalizeDomainName(domain)
	if cleanDomain == "" {
		return dnsmessage.Name{}, fmt.Errorf("invalid domain %q", domain)
	}
	return dnsmessage.NewName(cleanDomain + ".")
}

func messageHasAnswer(message dnsmessage.Message, domainName dnsmessage.Name, recordType dnsmessage.Type) bool {
	domainNameString := domainName.String()
	for _, answer := range message.Answers {
		if answer.Header.Type != recordType || !strings.EqualFold(answer.Header.Name.String(), domainNameString) {
			continue
		}
		switch recordType {
		case dnsmessage.TypeA:
			_, ok := answer.Body.(*dnsmessage.AResource)
			return ok
		case dnsmessage.TypeAAAA:
			_, ok := answer.Body.(*dnsmessage.AAAAResource)
			return ok
		case dnsmessage.TypeTXT:
			_, ok := answer.Body.(*dnsmessage.TXTResource)
			return ok
		default:
			return true
		}
	}
	return false
}

func cnameAnswerTarget(message dnsmessage.Message, domainName dnsmessage.Name) (dnsmessage.Name, bool) {
	domainNameString := domainName.String()
	for _, answer := range message.Answers {
		if answer.Header.Type != dnsmessage.TypeCNAME || !strings.EqualFold(answer.Header.Name.String(), domainNameString) {
			continue
		}
		cnameResource, ok := answer.Body.(*dnsmessage.CNAMEResource)
		if ok {
			return cnameResource.CNAME, true
		}
	}
	return dnsmessage.Name{}, false
}

func messageHasSOA(message dnsmessage.Message) bool {
	for _, authority := range message.Authorities {
		if authority.Header.Type == dnsmessage.TypeSOA {
			return true
		}
	}
	return false
}

func referralNameServerIPs(ctx context.Context, message dnsmessage.Message, depth int) ([]string, error) {
	nameServerNames := nameServerNamesFromAuthorities(message)
	if len(nameServerNames) == 0 {
		return nil, nil
	}
	nameServerIPs := nameServerIPsFromAdditionals(message, nameServerNames)
	if len(nameServerIPs) > 0 {
		return nameServerIPs, nil
	}
	for _, nameServerName := range nameServerNames {
		ipRecords, err := lookupAuthoritativeIPRecordsForName(ctx, nameServerName, depth)
		if err == nil && len(ipRecords) > 0 {
			nameServerIPs = append(nameServerIPs, ipStringsFromRecords(ipRecords)...)
		}
	}
	return dedupeIPStrings(nameServerIPs), nil
}

func nameServerNamesFromAuthorities(message dnsmessage.Message) []dnsmessage.Name {
	nameServerNames := make([]dnsmessage.Name, 0, len(message.Authorities))
	seenNames := make(map[string]struct{})
	for _, authority := range message.Authorities {
		if authority.Header.Type != dnsmessage.TypeNS {
			continue
		}
		nsResource, ok := authority.Body.(*dnsmessage.NSResource)
		if !ok {
			continue
		}
		nameKey := strings.ToLower(nsResource.NS.String())
		if _, seen := seenNames[nameKey]; seen {
			continue
		}
		seenNames[nameKey] = struct{}{}
		nameServerNames = append(nameServerNames, nsResource.NS)
	}
	return nameServerNames
}

func nameServerIPsFromAdditionals(message dnsmessage.Message, nameServerNames []dnsmessage.Name) []string {
	nameServerNameSet := make(map[string]struct{}, len(nameServerNames))
	for _, nameServerName := range nameServerNames {
		nameServerNameSet[strings.ToLower(nameServerName.String())] = struct{}{}
	}
	nameServerIPs := make([]string, 0)
	for _, additional := range message.Additionals {
		if _, wanted := nameServerNameSet[strings.ToLower(additional.Header.Name.String())]; !wanted {
			continue
		}
		switch body := additional.Body.(type) {
		case *dnsmessage.AResource:
			nameServerIPs = append(nameServerIPs, net.IP(body.A[:]).String())
		case *dnsmessage.AAAAResource:
			nameServerIPs = append(nameServerIPs, net.IP(body.AAAA[:]).String())
		}
	}
	return dedupeIPStrings(nameServerIPs)
}

func lookupAuthoritativeIPRecordsForName(ctx context.Context, domainName dnsmessage.Name, depth int) ([]net.IP, error) {
	ipRecords := make([]net.IP, 0, 4)
	lookupErrs := make([]error, 0, 2)
	aMessage, aErr := lookupAuthoritativeDNSMessageForName(ctx, domainName, dnsmessage.TypeA, authoritativeDNSRootServers, depth)
	if aErr != nil {
		lookupErrs = append(lookupErrs, aErr)
	} else {
		ipRecords = append(ipRecords, ipRecordsFromDNSMessage(aMessage)...)
	}
	aaaaMessage, aaaaErr := lookupAuthoritativeDNSMessageForName(ctx, domainName, dnsmessage.TypeAAAA, authoritativeDNSRootServers, depth)
	if aaaaErr != nil {
		lookupErrs = append(lookupErrs, aaaaErr)
	} else {
		ipRecords = append(ipRecords, ipRecordsFromDNSMessage(aaaaMessage)...)
	}
	if len(ipRecords) == 0 {
		return nil, errors.Join(lookupErrs...)
	}
	return dedupeIPRecords(ipRecords), nil
}

func ipRecordsFromDNSMessage(message dnsmessage.Message) []net.IP {
	ipRecords := make([]net.IP, 0, len(message.Answers))
	for _, answer := range message.Answers {
		switch body := answer.Body.(type) {
		case *dnsmessage.AResource:
			ipRecords = append(ipRecords, net.IP(body.A[:]))
		case *dnsmessage.AAAAResource:
			ipRecords = append(ipRecords, net.IP(body.AAAA[:]))
		}
	}
	return ipRecords
}

func ipStringsFromRecords(ipRecords []net.IP) []string {
	ipStrings := make([]string, 0, len(ipRecords))
	for _, ipRecord := range ipRecords {
		if ipRecord != nil {
			ipStrings = append(ipStrings, ipRecord.String())
		}
	}
	return ipStrings
}

func dedupeIPRecords(ipRecords []net.IP) []net.IP {
	seenIPs := make(map[string]struct{}, len(ipRecords))
	dedupedIPRecords := make([]net.IP, 0, len(ipRecords))
	for _, ipRecord := range ipRecords {
		if ipRecord == nil {
			continue
		}
		ipKey := ipRecord.String()
		if _, seen := seenIPs[ipKey]; seen {
			continue
		}
		seenIPs[ipKey] = struct{}{}
		dedupedIPRecords = append(dedupedIPRecords, ipRecord)
	}
	return dedupedIPRecords
}

func dedupeIPStrings(ipStrings []string) []string {
	seenIPs := make(map[string]struct{}, len(ipStrings))
	dedupedIPStrings := make([]string, 0, len(ipStrings))
	for _, ipString := range ipStrings {
		parsedIP := net.ParseIP(strings.TrimSpace(ipString))
		if parsedIP == nil {
			continue
		}
		ipKey := parsedIP.String()
		if _, seen := seenIPs[ipKey]; seen {
			continue
		}
		seenIPs[ipKey] = struct{}{}
		dedupedIPStrings = append(dedupedIPStrings, ipKey)
	}
	return dedupedIPStrings
}

func detectServerExternalIP(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("external IP service returned %s", response.Status)
	}
	bodyBytes, err := io.ReadAll(io.LimitReader(response.Body, 128))
	if err != nil {
		return "", err
	}
	externalIP := strings.TrimSpace(string(bodyBytes))
	if net.ParseIP(externalIP) == nil {
		return "", fmt.Errorf("external IP service returned invalid address %q", externalIP)
	}
	return externalIP, nil
}

func detectServerInterfaceIPs() ([]net.IP, error) {
	addressList, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	ipList := make([]net.IP, 0, len(addressList))
	for _, address := range addressList {
		var candidateIP net.IP
		switch typedAddress := address.(type) {
		case *net.IPNet:
			candidateIP = typedAddress.IP
		case *net.IPAddr:
			candidateIP = typedAddress.IP
		}
		if candidateIP == nil || candidateIP.IsLoopback() || !candidateIP.IsGlobalUnicast() {
			continue
		}
		ipList = append(ipList, candidateIP)
	}
	return ipList, nil
}

func detectServerIPCandidates(ctx context.Context) ([]net.IP, string, error) {
	externalIP, externalErr := lookupServerExternalIP(ctx)
	ipList := serverIPCandidatesWithExternalIP(externalIP)
	if len(ipList) > 0 {
		return ipList, ipList[0].String(), nil
	}
	if externalErr != nil {
		return nil, "", externalErr
	}
	return nil, "", errors.New("no public server IP addresses detected")
}

func serverIPCandidatesWithExternalIP(externalIP string) []net.IP {
	seenIPs := make(map[string]struct{})
	publicInterfaceIPs := make([]net.IP, 0)
	privateInterfaceIPs := make([]net.IP, 0)
	if interfaceIPs, err := lookupServerInterfaceIPs(); err == nil {
		for _, interfaceIP := range interfaceIPs {
			if interfaceIP == nil {
				continue
			}
			if interfaceIP.IsPrivate() {
				privateInterfaceIPs = append(privateInterfaceIPs, interfaceIP)
				continue
			}
			publicInterfaceIPs = append(publicInterfaceIPs, interfaceIP)
		}
	}
	sortIPListForDisplay(publicInterfaceIPs)
	sortIPListForDisplay(privateInterfaceIPs)
	ipList := make([]net.IP, 0, len(publicInterfaceIPs)+len(privateInterfaceIPs)+1)
	addIP := func(candidateIP net.IP) {
		if candidateIP == nil {
			return
		}
		ipKey := candidateIP.String()
		if _, seen := seenIPs[ipKey]; seen {
			return
		}
		seenIPs[ipKey] = struct{}{}
		ipList = append(ipList, candidateIP)
	}
	for _, interfaceIP := range publicInterfaceIPs {
		addIP(interfaceIP)
	}
	addIP(net.ParseIP(strings.TrimSpace(externalIP)))
	for _, interfaceIP := range privateInterfaceIPs {
		addIP(interfaceIP)
	}
	return ipList
}

func sortIPListForDisplay(ipList []net.IP) {
	sort.SliceStable(ipList, func(leftIndex, rightIndex int) bool {
		leftIsIPv4 := ipList[leftIndex].To4() != nil
		rightIsIPv4 := ipList[rightIndex].To4() != nil
		if leftIsIPv4 != rightIsIPv4 {
			return leftIsIPv4
		}
		return ipList[leftIndex].String() < ipList[rightIndex].String()
	})
}

func ipListForLog(ipList []net.IP) string {
	ipStrings := make([]string, 0, len(ipList))
	for _, currentIP := range ipList {
		if currentIP != nil {
			ipStrings = append(ipStrings, currentIP.String())
		}
	}
	sort.Strings(ipStrings)
	return strings.Join(ipStrings, ",")
}

func normalizeDomainName(rawDomain string) string {
	cleanDomain := strings.ToLower(strings.TrimSpace(rawDomain))
	if cleanDomain == "" {
		return ""
	}
	if strings.Contains(cleanDomain, "://") {
		parsedURL, err := url.Parse(cleanDomain)
		if err == nil {
			cleanDomain = parsedURL.Host
		}
	}
	cleanDomain = strings.TrimPrefix(cleanDomain, "//")
	if strings.Contains(cleanDomain, "/") {
		cleanDomain = strings.Split(cleanDomain, "/")[0]
	}
	if strings.Contains(cleanDomain, "@") {
		cleanDomain = cleanDomain[strings.LastIndex(cleanDomain, "@")+1:]
	}
	if host, _, splitErr := net.SplitHostPort(cleanDomain); splitErr == nil {
		cleanDomain = host
	}
	cleanDomain = strings.Trim(cleanDomain, ". ")
	if cleanDomain == "" || strings.ContainsAny(cleanDomain, " \t\r\n") {
		return ""
	}
	if net.ParseIP(cleanDomain) != nil {
		return ""
	}
	domainParts := strings.Split(cleanDomain, ".")
	if len(domainParts) < 2 {
		return ""
	}
	for _, domainPart := range domainParts {
		if domainPart == "" || len(domainPart) > 63 {
			return ""
		}
		for _, domainRune := range domainPart {
			if (domainRune >= 'a' && domainRune <= 'z') || (domainRune >= '0' && domainRune <= '9') || domainRune == '-' {
				continue
			}
			return ""
		}
		if strings.HasPrefix(domainPart, "-") || strings.HasSuffix(domainPart, "-") {
			return ""
		}
	}
	return cleanDomain
}

func (a *App) handleDomainSettingsPost(ctx context.Context, r *http.Request, siteDomain string, externalIP string) {
	action := strings.TrimSpace(r.FormValue("action"))
	switch action {
	case "update_auto_ssl":
		certificateDomain := normalizeDomainName(r.FormValue("ssl_domain"))
		if certificateDomain == "" || !a.domainBelongsToSite(ctx, siteDomain, certificateDomain) {
			return
		}
		enabled := r.FormValue("auto_ssl_enabled") == "1"
		a.setDomainAutomaticSSLManual(ctx, certificateDomain, enabled)
		if enabled && a.automaticSSLAvailable && strings.TrimSpace(externalIP) != "" {
			a.refreshDomainAutomaticSSL(ctx, certificateDomain, serverIPCandidatesWithExternalIP(externalIP))
		}
	case "add_alias":
		aliasDomain := normalizeDomainName(r.FormValue("alias_domain"))
		if aliasDomain == "" || aliasDomain == siteDomain {
			return
		}
		if a.domainAliasCount(ctx, siteDomain) >= 10 {
			return
		}
		token := randomAccessToken()
		_, _ = a.db.ExecContext(ctx, `INSERT INTO domain_aliases(primary_domain,alias_domain,verification_token,is_verified,dns_a_ok,is_selected,last_checked_at)
VALUES(?,?,?,?,?,?,?)
ON CONFLICT(alias_domain) DO UPDATE SET
	primary_domain=excluded.primary_domain,
	verification_token=CASE WHEN domain_aliases.primary_domain=excluded.primary_domain AND TRIM(COALESCE(domain_aliases.verification_token,''))<>'' THEN domain_aliases.verification_token ELSE excluded.verification_token END,
	is_verified=0,
	dns_a_ok=0,
	is_selected=0,
	last_checked_at=''`,
			siteDomain, aliasDomain, token, 0, 0, 0, "")
		a.refreshDomainAliasVerification(ctx, siteDomain, aliasDomain, externalIP)
	case "delete_alias":
		aliasDomain := normalizeDomainName(r.FormValue("alias_domain"))
		_, _ = a.db.ExecContext(ctx, `DELETE FROM domain_aliases WHERE primary_domain=? AND alias_domain=?`, siteDomain, aliasDomain)
	case "select_alias":
		aliasDomain := normalizeDomainName(r.FormValue("alias_domain"))
		a.refreshDomainAliasVerification(ctx, siteDomain, aliasDomain, externalIP)
		if a.domainAliasIsActive(ctx, siteDomain, aliasDomain) {
			_, _ = a.db.ExecContext(ctx, `UPDATE domain_aliases SET is_selected=0 WHERE primary_domain=?`, siteDomain)
			_, _ = a.db.ExecContext(ctx, `UPDATE domain_aliases SET is_selected=1 WHERE primary_domain=? AND alias_domain=?`, siteDomain, aliasDomain)
		}
	case "select_primary":
		_, _ = a.db.ExecContext(ctx, `UPDATE domain_aliases SET is_selected=0 WHERE primary_domain=?`, siteDomain)
	case "check_alias":
		aliasDomain := normalizeDomainName(r.FormValue("alias_domain"))
		a.refreshDomainAliasVerification(ctx, siteDomain, aliasDomain, externalIP)
	case "check_all":
		a.refreshDomainAliases(ctx, siteDomain, externalIP)
	case "rotate_backup_token":
		a.rotateBackupTokenForDomain(ctx, siteDomain)
	case "save_chroot_location":
		urlPath := normalizeChrootLocationURLPath(r.FormValue("location_url_path"))
		if urlPath == "" {
			return
		}
		directoryPath, err := a.ensureChrootLocationDirectory(siteDomain, urlPath)
		if err != nil {
			return
		}
		_, _ = a.db.ExecContext(ctx, `INSERT INTO domain_chroot_locations(domain,url_path,directory_path,updated_at)
VALUES(?,?,?,?)
ON CONFLICT(domain,url_path) DO UPDATE SET directory_path=excluded.directory_path,updated_at=excluded.updated_at`,
			siteDomain, urlPath, directoryPath, time.Now().UTC().Format(time.RFC3339))
	case "delete_chroot_location":
		urlPath := normalizeChrootLocationURLPath(r.FormValue("location_url_path"))
		if urlPath == "" {
			return
		}
		_, _ = a.db.ExecContext(ctx, `DELETE FROM domain_chroot_locations WHERE domain=? AND url_path=?`, siteDomain, urlPath)
	}
}

func normalizeChrootLocationURLPath(rawPath string) string {
	cleanedPath := cleanPath(rawPath)
	if cleanedPath == "/" {
		return ""
	}
	return strings.TrimRight(cleanedPath, "/")
}

func isPathWithinRoot(rootPath string, candidatePath string) bool {
	cleanRoot := filepath.Clean(rootPath)
	cleanCandidate := filepath.Clean(candidatePath)
	if cleanRoot == cleanCandidate {
		return true
	}
	return strings.HasPrefix(cleanCandidate, cleanRoot+string(os.PathSeparator))
}

func (a *App) domainChrootRootDir(domain string) string {
	return filepath.Join(a.storageRootDir(), "chroot", domainStorageName(domain))
}

func (a *App) domainChrootRealRootDir(domain string) (string, error) {
	rootPath := a.domainChrootRootDir(domain)
	if err := os.MkdirAll(rootPath, 0o755); err != nil {
		return "", err
	}
	rootAbsPath, err := filepath.Abs(rootPath)
	if err != nil {
		return "", err
	}
	rootRealPath, err := filepath.EvalSymlinks(rootAbsPath)
	if err != nil {
		return rootAbsPath, nil
	}
	return rootRealPath, nil
}

func (a *App) chrootLocationDirectoryPath(domain, urlPath string) string {
	normalizedURLPath := normalizeChrootLocationURLPath(urlPath)
	relativePath := strings.TrimPrefix(normalizedURLPath, "/")
	return filepath.Join(a.domainChrootRootDir(domain), filepath.FromSlash(relativePath))
}

func (a *App) ensureChrootLocationDirectory(domain, urlPath string) (string, error) {
	directoryPath := a.chrootLocationDirectoryPath(domain, urlPath)
	if err := os.MkdirAll(directoryPath, 0o755); err != nil {
		return "", err
	}
	return a.normalizeAndValidateChrootLocationPath(domain, directoryPath)
}

func (a *App) normalizeAndValidateChrootLocationPath(domain, rawDirectoryPath string) (string, error) {
	rootRealPath, err := a.domainChrootRealRootDir(domain)
	if err != nil {
		return "", err
	}
	cleanInputPath := strings.TrimSpace(rawDirectoryPath)
	if cleanInputPath == "" {
		return "", errors.New("directory path is required")
	}
	candidatePath := cleanInputPath
	if !filepath.IsAbs(candidatePath) {
		candidatePath = filepath.Join(rootRealPath, candidatePath)
	}
	candidateAbsPath, err := filepath.Abs(candidatePath)
	if err != nil {
		return "", err
	}
	candidateRealPath, err := filepath.EvalSymlinks(candidateAbsPath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(candidateRealPath)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("directory path must point to a folder")
	}
	if !isPathWithinRoot(rootRealPath, candidateRealPath) {
		return "", errors.New("directory path is outside allowed chroot")
	}
	return candidateRealPath, nil
}

func (a *App) listDomainChrootLocations(ctx context.Context, domain string) ([]DomainChrootLocation, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT url_path,directory_path FROM domain_chroot_locations WHERE domain=? ORDER BY url_path`, domain)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	locationList := make([]DomainChrootLocation, 0)
	for rows.Next() {
		var currentLocation DomainChrootLocation
		if scanErr := rows.Scan(&currentLocation.URLPath, &currentLocation.DirectoryPath); scanErr != nil {
			return nil, scanErr
		}
		currentLocation.URLPath = normalizeChrootLocationURLPath(currentLocation.URLPath)
		if currentLocation.URLPath == "" {
			continue
		}
		expectedDirectoryPath, err := a.ensureChrootLocationDirectory(domain, currentLocation.URLPath)
		if err == nil {
			currentLocation.DirectoryPath = expectedDirectoryPath
		}
		locationList = append(locationList, currentLocation)
	}
	return locationList, rows.Err()
}

func (a *App) matchDomainChrootLocation(ctx context.Context, domain, requestPath string) (DomainChrootLocation, string, bool) {
	rows, err := a.db.QueryContext(ctx, `SELECT url_path,directory_path FROM domain_chroot_locations WHERE domain=?`, domain)
	if err != nil {
		return DomainChrootLocation{}, "", false
	}
	defer rows.Close()
	locationList := make([]DomainChrootLocation, 0)
	for rows.Next() {
		var location DomainChrootLocation
		if scanErr := rows.Scan(&location.URLPath, &location.DirectoryPath); scanErr != nil {
			continue
		}
		location.URLPath = normalizeChrootLocationURLPath(location.URLPath)
		if location.URLPath == "" {
			continue
		}
		locationList = append(locationList, location)
	}
	sort.SliceStable(locationList, func(leftIndex, rightIndex int) bool {
		return len(locationList[leftIndex].URLPath) > len(locationList[rightIndex].URLPath)
	})
	cleanRequestPath := cleanPath(requestPath)
	for _, location := range locationList {
		if cleanRequestPath != location.URLPath && !strings.HasPrefix(cleanRequestPath, location.URLPath+"/") {
			continue
		}
		expectedDirectoryPath, err := a.ensureChrootLocationDirectory(domain, location.URLPath)
		if err != nil {
			continue
		}
		location.DirectoryPath = expectedDirectoryPath
		relativeRequestPath := strings.TrimPrefix(cleanRequestPath, location.URLPath)
		if relativeRequestPath == "" {
			relativeRequestPath = "/"
		}
		return location, relativeRequestPath, true
	}
	return DomainChrootLocation{}, "", false
}

func (a *App) resolveExistingChrootPath(domain string, candidatePath string) (string, os.FileInfo, error) {
	candidateAbsPath, err := filepath.Abs(candidatePath)
	if err != nil {
		return "", nil, err
	}
	candidateRealPath, err := filepath.EvalSymlinks(candidateAbsPath)
	if err != nil {
		return "", nil, err
	}
	rootRealPath, err := a.domainChrootRealRootDir(domain)
	if err != nil {
		return "", nil, err
	}
	if !isPathWithinRoot(rootRealPath, candidateRealPath) {
		return "", nil, errors.New("path escapes allowed domain chroot")
	}
	info, err := os.Stat(candidateRealPath)
	if err != nil {
		return "", nil, err
	}
	return candidateRealPath, info, nil
}

func (a *App) resolveChrootLocationTargetPath(domain string, location DomainChrootLocation, relativeRequestPath string) (string, os.FileInfo, error) {
	cleanRelativePath := cleanPath(relativeRequestPath)
	relativePathValue := strings.TrimPrefix(cleanRelativePath, "/")
	candidatePath := filepath.Join(location.DirectoryPath, filepath.FromSlash(relativePathValue))
	return a.resolveExistingChrootPath(domain, candidatePath)
}

func (a *App) serveDomainChrootLocation(w http.ResponseWriter, r *http.Request, domain, requestPath string) bool {
	location, relativeRequestPath, found := a.matchDomainChrootLocation(r.Context(), domain, requestPath)
	if !found {
		return false
	}
	validatedDirectoryPath, err := a.normalizeAndValidateChrootLocationPath(domain, location.DirectoryPath)
	if err != nil {
		http.NotFound(w, r)
		return true
	}
	location.DirectoryPath = validatedDirectoryPath
	targetPath, info, err := a.resolveChrootLocationTargetPath(domain, location, relativeRequestPath)
	if err != nil {
		http.NotFound(w, r)
		return true
	}
	if info.IsDir() {
		indexPath := filepath.Join(targetPath, "index.html")
		if indexRealPath, indexInfo, indexErr := a.resolveExistingChrootPath(domain, indexPath); indexErr == nil && !indexInfo.IsDir() {
			http.ServeFile(w, r, indexRealPath)
			return true
		}
		a.renderDirectoryListing(w, r, domain, requestPath, targetPath, location.URLPath)
		return true
	}
	http.ServeFile(w, r, targetPath)
	return true
}

func (a *App) chrootResourceExists(ctx context.Context, domain, requestPath string) bool {
	location, relativeRequestPath, found := a.matchDomainChrootLocation(ctx, domain, requestPath)
	if !found {
		return false
	}
	validatedDirectoryPath, err := a.normalizeAndValidateChrootLocationPath(domain, location.DirectoryPath)
	if err != nil {
		return false
	}
	location.DirectoryPath = validatedDirectoryPath
	_, _, err = a.resolveChrootLocationTargetPath(domain, location, relativeRequestPath)
	return err == nil
}

func (a *App) renderDirectoryListing(w http.ResponseWriter, r *http.Request, domain, requestPath, absoluteDirectoryPath, locationURLPath string) {
	entries, err := os.ReadDir(absoluteDirectoryPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	translations := translationsForRequest(r)
	directoryListingTitle := translationOrDefault(translations, "directory_listing_title", "Directory listing")
	directoryFolderLabel := translationOrDefault(translations, "directory_folder", "Folder")
	directoryEntriesLabel := translationOrDefault(translations, "directory_entries_label", "entries")
	directoryParentFolderLabel := translationOrDefault(translations, "directory_parent_folder", "Parent folder")
	directoryEmptyLabel := translationOrDefault(translations, "directory_empty", "This folder is empty.")
	cleanRequestPath := cleanPath(requestPath)
	listingEntries := make([]directoryListingEntry, 0, len(entries))
	for _, entry := range entries {
		entryView, ok := a.directoryListingEntryView(domain, cleanRequestPath, absoluteDirectoryPath, entry, translations)
		if ok {
			listingEntries = append(listingEntries, entryView)
		}
	}
	sort.SliceStable(listingEntries, func(i, j int) bool {
		leftIsDir := listingEntries[i].IsDir
		rightIsDir := listingEntries[j].IsDir
		if leftIsDir != rightIsDir {
			return leftIsDir
		}
		return strings.ToLower(listingEntries[i].Name) < strings.ToLower(listingEntries[j].Name)
	})
	var listing strings.Builder
	listing.WriteString("<!doctype html><html><head><meta charset=\"utf-8\"><title>")
	listing.WriteString(template.HTMLEscapeString(directoryListingTitle))
	listing.WriteString(": ")
	listing.WriteString(template.HTMLEscapeString(cleanRequestPath))
	listing.WriteString("</title><meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\"><link rel=\"icon\" href=\"/p/static/sitebrush-logo.ico\" type=\"image/x-icon\"><link rel=\"stylesheet\" href=\"/p/static/directory_listing.css\"></head><body><main class=\"page\"><header class=\"topbar\"><a class=\"brand\" href=\"/\"><img class=\"brand-logo\" src=\"/p/static/sitebrush-logo.png\" alt=\"Sitebrush\"><span><span class=\"brand-name\">sitebrush</span><span class=\"brand-line\">")
	listing.WriteString(template.HTMLEscapeString(directoryListingTitle))
	listing.WriteString("</span></span></a><div class=\"path-pill\">")
	listing.WriteString(template.HTMLEscapeString(cleanRequestPath))
	listing.WriteString("</div></header><section class=\"directory-panel\"><div class=\"directory-header\"><p class=\"eyebrow\">")
	listing.WriteString(template.HTMLEscapeString(directoryFolderLabel))
	listing.WriteString("</p><h1>")
	listing.WriteString(template.HTMLEscapeString(cleanRequestPath))
	listing.WriteString("</h1><p class=\"directory-meta\">")
	listing.WriteString(strconv.Itoa(len(listingEntries)))
	listing.WriteString(" ")
	listing.WriteString(template.HTMLEscapeString(directoryEntriesLabel))
	listing.WriteString("</p></div><div class=\"entry-list\">")
	if cleanRequestPath != locationURLPath {
		parentPath := parentPagePath(cleanRequestPath)
		if parentPath == "" {
			parentPath = locationURLPath
		}
		listing.WriteString("<a class=\"entry entry-folder\" href=\"")
		listing.WriteString(template.HTMLEscapeString(parentPath))
		listing.WriteString("\"><span class=\"entry-icon\">..</span><span class=\"entry-name\">")
		listing.WriteString(template.HTMLEscapeString(directoryParentFolderLabel))
		listing.WriteString("</span><span class=\"entry-kind\">")
		listing.WriteString(template.HTMLEscapeString(directoryFolderLabel))
		listing.WriteString("</span><span class=\"entry-size\">-</span><span class=\"entry-time\"></span></a>")
	}
	for _, entry := range listingEntries {
		listing.WriteString("<a class=\"")
		listing.WriteString("entry")
		if entry.IsDir {
			listing.WriteString(" entry-folder")
		}
		listing.WriteString("\" href=\"")
		listing.WriteString(template.HTMLEscapeString(entry.Href))
		listing.WriteString("\"><span class=\"entry-icon\">")
		listing.WriteString(template.HTMLEscapeString(entry.Icon))
		listing.WriteString("</span><span class=\"entry-name\">")
		listing.WriteString(template.HTMLEscapeString(entry.Name))
		listing.WriteString("</span><span class=\"entry-kind\">")
		listing.WriteString(template.HTMLEscapeString(entry.Kind))
		listing.WriteString("</span><span class=\"entry-size\">")
		listing.WriteString(template.HTMLEscapeString(entry.SizeLabel))
		listing.WriteString("</span><span class=\"entry-time\">")
		listing.WriteString(template.HTMLEscapeString(entry.TimeLabel))
		listing.WriteString("</span></a>")
	}
	if len(listingEntries) == 0 && cleanRequestPath == locationURLPath {
		listing.WriteString("<div class=\"empty\">")
		listing.WriteString(template.HTMLEscapeString(directoryEmptyLabel))
		listing.WriteString("</div>")
	}
	listing.WriteString("</div></section></main></body></html>")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, listing.String())
}

func (a *App) directoryListingEntryView(domain, cleanRequestPath, absoluteDirectoryPath string, entry os.DirEntry, translations map[string]string) (directoryListingEntry, bool) {
	entryName := entry.Name()
	entryPath := filepath.Join(absoluteDirectoryPath, entryName)
	_, entryInfo, err := a.resolveExistingChrootPath(domain, entryPath)
	if err != nil {
		return directoryListingEntry{}, false
	}
	isDir := entryInfo.IsDir()
	href := strings.TrimSuffix(cleanRequestPath, "/") + "/" + url.PathEscape(entryName)
	label := entryName
	entryKind := translationOrDefault(translations, "directory_file", "File")
	entryIcon := "•"
	entrySize := formatFileSize(entryInfo.Size())
	if isDir {
		href += "/"
		label += "/"
		entryKind = translationOrDefault(translations, "directory_folder", "Folder")
		entryIcon = "⌂"
		entrySize = "-"
	}
	if entry.Type()&os.ModeSymlink != 0 {
		if isDir {
			entryKind = translationOrDefault(translations, "directory_folder_link", "Folder link")
		} else {
			entryKind = translationOrDefault(translations, "directory_file_link", "File link")
		}
		entryIcon = "↗"
	}
	return directoryListingEntry{
		Name:      label,
		Href:      href,
		Kind:      entryKind,
		Icon:      entryIcon,
		SizeLabel: entrySize,
		TimeLabel: entryInfo.ModTime().Format("2006-01-02 15:04"),
		IsDir:     isDir,
	}, true
}

func (a *App) domainBelongsToSite(ctx context.Context, siteDomain string, candidateDomain string) bool {
	if candidateDomain == siteDomain {
		return true
	}
	var aliasCount int
	_ = a.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM domain_aliases WHERE primary_domain=? AND alias_domain=?`, siteDomain, candidateDomain).Scan(&aliasCount)
	return aliasCount > 0
}

func (a *App) domainAliasCount(ctx context.Context, siteDomain string) int {
	var aliasCount int
	_ = a.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM domain_aliases WHERE primary_domain=?`, siteDomain).Scan(&aliasCount)
	return aliasCount
}

func (a *App) domainAliasIsActive(ctx context.Context, siteDomain string, aliasDomain string) bool {
	var activeCount int
	_ = a.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM domain_aliases WHERE primary_domain=? AND alias_domain=? AND is_verified=1 AND dns_a_ok=1`, siteDomain, aliasDomain).Scan(&activeCount)
	return activeCount > 0
}

func (a *App) refreshDomainAliases(ctx context.Context, siteDomain string, externalIP string) {
	aliasRows, err := a.db.QueryContext(ctx, `SELECT alias_domain FROM domain_aliases WHERE primary_domain=? ORDER BY alias_domain`, siteDomain)
	if err != nil {
		return
	}
	defer aliasRows.Close()
	for aliasRows.Next() {
		var aliasDomain string
		if scanErr := aliasRows.Scan(&aliasDomain); scanErr == nil {
			a.refreshDomainAliasVerification(ctx, siteDomain, aliasDomain, externalIP)
		}
	}
}

func (a *App) refreshDomainAliasVerification(ctx context.Context, siteDomain string, aliasDomain string, externalIP string) {
	if aliasDomain == "" {
		return
	}
	var verificationTokenValue sql.NullString
	err := a.db.QueryRowContext(ctx, `SELECT verification_token FROM domain_aliases WHERE primary_domain=? AND alias_domain=?`, siteDomain, aliasDomain).Scan(&verificationTokenValue)
	if err != nil {
		return
	}
	verificationToken := verificationTokenValue.String
	if strings.TrimSpace(verificationToken) == "" {
		verificationToken = randomAccessToken()
		_, _ = a.db.ExecContext(ctx, `UPDATE domain_aliases SET verification_token=? WHERE primary_domain=? AND alias_domain=?`, verificationToken, siteDomain, aliasDomain)
	}
	txtVerified := domainTXTRecordMatches(aliasDomain, verificationToken)
	aRecordVerified := domainARecordMatches(aliasDomain, externalIP)
	lastCheckedAt := time.Now().Format(time.RFC3339)
	_, _ = a.db.ExecContext(ctx, `UPDATE domain_aliases SET is_verified=?, dns_a_ok=?, last_checked_at=? WHERE primary_domain=? AND alias_domain=?`,
		boolToInt(txtVerified), boolToInt(aRecordVerified), lastCheckedAt, siteDomain, aliasDomain)
	if !txtVerified || !aRecordVerified {
		_, _ = a.db.ExecContext(ctx, `UPDATE domain_aliases SET is_selected=0 WHERE primary_domain=? AND alias_domain=?`, siteDomain, aliasDomain)
	}
}

func domainTXTRecordMatches(aliasDomain string, verificationToken string) bool {
	txtRecords, err := lookupTXTRecords(aliasDomain)
	if err != nil {
		return false
	}
	requiredRecord := "sitebrush=" + strings.TrimSpace(verificationToken)
	for _, txtRecord := range txtRecords {
		if strings.TrimSpace(txtRecord) == requiredRecord {
			return true
		}
	}
	return false
}

func domainARecordMatches(aliasDomain string, externalIP string) bool {
	parsedExternalIP := net.ParseIP(strings.TrimSpace(externalIP))
	if parsedExternalIP == nil {
		return false
	}
	return domainARecordMatchesAny(aliasDomain, []net.IP{parsedExternalIP})
}

func domainARecordMatchesAny(aliasDomain string, serverIPs []net.IP) bool {
	if len(serverIPs) == 0 {
		return false
	}
	ipRecords, err := lookupIPRecords(aliasDomain)
	if err != nil {
		return false
	}
	for _, ipRecord := range ipRecords {
		for _, serverIP := range serverIPs {
			if serverIP != nil && ipRecord.Equal(serverIP) {
				return true
			}
		}
	}
	return false
}

func domainIPRecordsMatchAny(ipRecords []net.IP, serverIPs []net.IP) bool {
	if len(ipRecords) == 0 || len(serverIPs) == 0 {
		return false
	}
	for _, ipRecord := range ipRecords {
		for _, serverIP := range serverIPs {
			if serverIP != nil && ipRecord != nil && ipRecord.Equal(serverIP) {
				return true
			}
		}
	}
	return false
}

func publicAutoCertIPRecords(ipRecords []net.IP) []net.IP {
	publicRecords := make([]net.IP, 0, len(ipRecords))
	for _, ipRecord := range ipRecords {
		if ipRecord == nil {
			continue
		}
		if !ipRecord.IsGlobalUnicast() || ipRecord.IsPrivate() || ipRecord.IsLoopback() || ipRecord.IsLinkLocalUnicast() || ipRecord.IsLinkLocalMulticast() || ipRecord.IsUnspecified() {
			continue
		}
		publicRecords = append(publicRecords, ipRecord)
	}
	return publicRecords
}

func autoCertDomainEligibilityError(domain string) error {
	certificateDomain := normalizeDomainName(domain)
	if certificateDomain == "" {
		return fmt.Errorf("automatic SSL domain %q is not a valid public hostname", domain)
	}
	domainParts := strings.Split(certificateDomain, ".")
	if len(domainParts) < 2 {
		return fmt.Errorf("automatic SSL domain %q is not a public hostname", certificateDomain)
	}
	lastDomainPart := domainParts[len(domainParts)-1]
	switch lastDomainPart {
	case "local", "localhost", "internal", "lan", "home", "corp", "private", "test", "invalid", "example":
		return fmt.Errorf("automatic SSL domain %q uses a private or reserved suffix", certificateDomain)
	}
	return nil
}

func (a *App) startAutomaticSSLRefreshWorker(ctx context.Context) {
	go func() {
		a.refreshAutomaticSSLDomains(ctx)
		ticker := time.NewTicker(automaticSSLRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.refreshAutomaticSSLDomains(ctx)
			}
		}
	}()
}

func (a *App) refreshAutomaticSSLDomains(ctx context.Context) {
	if !a.automaticSSLAvailable {
		return
	}
	serverIPs, _, err := detectServerIPCandidates(ctx)
	if err != nil || len(serverIPs) == 0 {
		a.logProblemEvent("AUTOCERT DNS refresh skipped: server IP detection failed: %v", err)
		return
	}
	domainList := a.listAutomaticSSLDomainCandidates(ctx)
	a.logProblemEvent("AUTOCERT DNS refresh started: domains=%d server_ips=%s", len(domainList), ipListForLog(serverIPs))
	for _, domain := range domainList {
		a.refreshDomainAutomaticSSL(ctx, domain, serverIPs)
	}
}

func (a *App) listAutomaticSSLDomainCandidates(ctx context.Context) []string {
	query := `
SELECT domain FROM pages
UNION SELECT domain FROM users
UNION SELECT domain FROM domain_states
UNION SELECT primary_domain FROM domain_aliases
UNION SELECT alias_domain FROM domain_aliases
UNION SELECT domain FROM domain_ssl_settings`
	rows, err := a.db.QueryContext(ctx, query)
	if err != nil {
		return nil
	}
	defer rows.Close()
	seenDomains := make(map[string]struct{})
	domainList := make([]string, 0)
	for rows.Next() {
		var rawDomain sql.NullString
		if scanErr := rows.Scan(&rawDomain); scanErr != nil {
			continue
		}
		domain := normalizeDomainName(rawDomain.String)
		if domain == "" {
			continue
		}
		if _, seen := seenDomains[domain]; seen {
			continue
		}
		seenDomains[domain] = struct{}{}
		domainList = append(domainList, domain)
	}
	sort.Strings(domainList)
	return domainList
}

func (a *App) domainIsAutomaticSSLCandidate(ctx context.Context, domain string) bool {
	certificateDomain := normalizeDomainName(domain)
	if certificateDomain == "" {
		return false
	}
	query := `
SELECT COUNT(1) FROM (
SELECT domain FROM pages WHERE domain=?
UNION SELECT domain FROM users WHERE domain=?
UNION SELECT domain FROM domain_states WHERE domain=?
UNION SELECT primary_domain FROM domain_aliases WHERE primary_domain=?
UNION SELECT alias_domain FROM domain_aliases WHERE alias_domain=?
UNION SELECT domain FROM domain_ssl_settings WHERE domain=?
)`
	var domainCount int
	err := a.db.QueryRowContext(ctx, query, certificateDomain, certificateDomain, certificateDomain, certificateDomain, certificateDomain, certificateDomain).Scan(&domainCount)
	return err == nil && domainCount > 0
}

func (a *App) precheckDomainAutomaticSSL(ctx context.Context, domain string, serverIPs []net.IP) (DomainAutomaticSSLSetting, error) {
	setting := a.domainAutomaticSSLSetting(ctx, domain)
	if setting.Domain == "" {
		return setting, fmt.Errorf("automatic SSL domain %q is invalid", domain)
	}
	setting.Available = a.automaticSSLAvailable
	if !setting.Available {
		return setting, fmt.Errorf("automatic SSL is unavailable because Sitebrush is not listening on ports 80 and 443")
	}
	if setting.ManuallyDisabled {
		return setting, fmt.Errorf("automatic SSL is manually disabled for %q", setting.Domain)
	}
	ipRecords, err := lookupIPRecords(setting.Domain)
	if err != nil {
		setting.Enabled = false
		setting.LastCheckedAt = time.Now().UTC().Format(time.RFC3339)
		a.upsertDomainAutomaticSSLSetting(ctx, setting)
		a.recordDomainAutomaticSSLFailure(ctx, setting.Domain, fmt.Sprintf("DNS lookup failed: %v", err), automaticSSLRetryDelayDNS)
		return setting, fmt.Errorf("automatic SSL DNS lookup failed for %q: %w", setting.Domain, err)
	}
	publicRecords := publicAutoCertIPRecords(ipRecords)
	if len(publicRecords) == 0 {
		setting.Enabled = false
		setting.LastCheckedAt = time.Now().UTC().Format(time.RFC3339)
		a.upsertDomainAutomaticSSLSetting(ctx, setting)
		err := fmt.Errorf("automatic SSL domain %q resolves only to private or non-public addresses", setting.Domain)
		a.recordDomainAutomaticSSLFailure(ctx, setting.Domain, err.Error(), automaticSSLRetryDelayEligibility)
		return setting, err
	}
	setting.Enabled = domainIPRecordsMatchAny(publicRecords, serverIPs)
	setting.LastCheckedAt = time.Now().UTC().Format(time.RFC3339)
	a.upsertDomainAutomaticSSLSetting(ctx, setting)
	if domainFromContext(ctx) != setting.Domain {
		a.upsertDomainAutomaticSSLSetting(contextWithDomain(ctx, setting.Domain), setting)
	}
	a.logDomainEvent(setting.Domain, "AUTOCERT DNS precheck domain=%s matched=%t dns_ips=%s server_ips=%s", setting.Domain, setting.Enabled, ipListForLog(publicRecords), ipListForLog(serverIPs))
	if !setting.Enabled {
		err := fmt.Errorf("automatic SSL domain %q does not point to this server", setting.Domain)
		a.recordDomainAutomaticSSLFailure(ctx, setting.Domain, err.Error(), automaticSSLRetryDelayDNS)
		return setting, err
	}
	if !a.automaticSSLAvailable {
		err := fmt.Errorf("automatic SSL challenge endpoint is unavailable for %q", setting.Domain)
		a.recordDomainAutomaticSSLFailure(ctx, setting.Domain, err.Error(), automaticSSLRetryDelayChallenge)
		return setting, err
	}
	return setting, nil
}

func (a *App) refreshDomainAutomaticSSL(ctx context.Context, domain string, serverIPs []net.IP) DomainAutomaticSSLSetting {
	setting := a.domainAutomaticSSLSetting(ctx, domain)
	if setting.Domain == "" {
		return setting
	}
	setting.Available = a.automaticSSLAvailable
	if !setting.Available || setting.ManuallyDisabled {
		return setting
	}
	setting.Enabled = domainARecordMatchesAny(setting.Domain, serverIPs)
	setting.LastCheckedAt = time.Now().UTC().Format(time.RFC3339)
	a.upsertDomainAutomaticSSLSetting(ctx, setting)
	if domainFromContext(ctx) != setting.Domain {
		a.upsertDomainAutomaticSSLSetting(contextWithDomain(ctx, setting.Domain), setting)
	}
	a.logDomainEvent(setting.Domain, "AUTOCERT DNS check domain=%s matched=%t server_ips=%s", setting.Domain, setting.Enabled, ipListForLog(serverIPs))
	return setting
}

func (a *App) refreshDomainAutomaticSSLIfStale(ctx context.Context, domain string, serverIPs []net.IP) DomainAutomaticSSLSetting {
	setting := a.domainAutomaticSSLSetting(ctx, domain)
	if setting.Domain == "" {
		return setting
	}
	setting.Available = a.automaticSSLAvailable
	if !setting.Available || setting.ManuallyDisabled {
		return setting
	}
	lastCheckedAt, err := time.Parse(time.RFC3339, setting.LastCheckedAt)
	if err == nil && time.Since(lastCheckedAt) < automaticSSLRefreshInterval {
		return setting
	}
	if len(serverIPs) == 0 {
		return setting
	}
	return a.refreshDomainAutomaticSSL(ctx, domain, serverIPs)
}

func (a *App) domainAutomaticSSLSetting(ctx context.Context, domain string) DomainAutomaticSSLSetting {
	certificateDomain := normalizeDomainName(domain)
	if certificateDomain == "" {
		return DomainAutomaticSSLSetting{Available: a.automaticSSLAvailable}
	}
	setting := DomainAutomaticSSLSetting{Domain: certificateDomain, Available: a.automaticSSLAvailable}
	var enabled int
	var manuallyDisabled int
	var lastCheckedAt sql.NullString
	var lastFailedAt sql.NullString
	var lastFailureReason sql.NullString
	var retryAfter sql.NullString
	err := a.db.QueryRowContext(ctx, `SELECT auto_ssl_enabled,manually_disabled,last_checked_at,last_failed_at,last_failure_reason,retry_after FROM domain_ssl_settings WHERE domain=?`, certificateDomain).Scan(&enabled, &manuallyDisabled, &lastCheckedAt, &lastFailedAt, &lastFailureReason, &retryAfter)
	if err == nil {
		setting.Enabled = enabled == 1
		setting.ManuallyDisabled = manuallyDisabled == 1
		setting.LastCheckedAt = lastCheckedAt.String
		setting.LastFailedAt = lastFailedAt.String
		setting.LastFailureReason = lastFailureReason.String
		setting.RetryAfter = retryAfter.String
	}
	return setting
}

func (a *App) domainAutomaticSSLEnabled(ctx context.Context, domain string) bool {
	setting := a.domainAutomaticSSLSetting(ctx, domain)
	return setting.Enabled && !setting.ManuallyDisabled
}

func automaticSSLStatusView(setting DomainAutomaticSSLSetting, externalIPErr error) DomainAutomaticSSLStatus {
	if !setting.Available {
		return DomainAutomaticSSLStatus{
			OverallClass:       "status-bad",
			OverallTextKey:     "domain_settings_ssl_status_error",
			DomainCheckClass:   "status-warn",
			DomainCheckTextKey: "domain_settings_ssl_domain_check_not_checked",
			CertificateClass:   "status-bad",
			CertificateTextKey: "domain_settings_ssl_certificate_ports_error",
		}
	}
	if externalIPErr != nil {
		return DomainAutomaticSSLStatus{
			OverallClass:       "status-bad",
			OverallTextKey:     "domain_settings_ssl_status_error",
			DomainCheckClass:   "status-bad",
			DomainCheckTextKey: "domain_settings_ssl_domain_check_error",
			CertificateClass:   "status-warn",
			CertificateTextKey: "domain_settings_ssl_certificate_waiting",
		}
	}
	if setting.ManuallyDisabled {
		return DomainAutomaticSSLStatus{
			OverallClass:       "status-warn",
			OverallTextKey:     "domain_settings_ssl_status_disabled",
			DomainCheckClass:   "status-warn",
			DomainCheckTextKey: "domain_settings_ssl_domain_check_manual_off",
			CertificateClass:   "status-warn",
			CertificateTextKey: "domain_settings_ssl_certificate_disabled",
		}
	}
	if setting.Enabled {
		return DomainAutomaticSSLStatus{
			OverallClass:       "status-ok",
			OverallTextKey:     "domain_settings_ssl_status_ok",
			DomainCheckClass:   "status-ok",
			DomainCheckTextKey: "domain_settings_ssl_domain_check_ok",
			CertificateClass:   "status-ok",
			CertificateTextKey: "domain_settings_ssl_certificate_active",
		}
	}
	return DomainAutomaticSSLStatus{
		OverallClass:       "status-warn",
		OverallTextKey:     "domain_settings_ssl_status_waiting",
		DomainCheckClass:   "status-warn",
		DomainCheckTextKey: "domain_settings_ssl_domain_check_waiting",
		CertificateClass:   "status-warn",
		CertificateTextKey: "domain_settings_ssl_certificate_waiting",
	}
}

func (a *App) setDomainAutomaticSSLManual(ctx context.Context, domain string, enabled bool) {
	setting := a.domainAutomaticSSLSetting(ctx, domain)
	if setting.Domain == "" {
		return
	}
	setting.Enabled = enabled
	setting.ManuallyDisabled = !enabled
	if strings.TrimSpace(setting.LastCheckedAt) == "" {
		setting.LastCheckedAt = time.Now().UTC().Format(time.RFC3339)
	}
	a.upsertDomainAutomaticSSLSetting(ctx, setting)
	if domainFromContext(ctx) != setting.Domain {
		a.upsertDomainAutomaticSSLSetting(contextWithDomain(ctx, setting.Domain), setting)
	}
}

func (a *App) upsertDomainAutomaticSSLSetting(ctx context.Context, setting DomainAutomaticSSLSetting) {
	if setting.Domain == "" {
		return
	}
	_, _ = a.db.ExecContext(ctx, `INSERT INTO domain_ssl_settings(domain,auto_ssl_enabled,manually_disabled,last_checked_at)
VALUES(?,?,?,?)
ON CONFLICT(domain) DO UPDATE SET auto_ssl_enabled=excluded.auto_ssl_enabled,manually_disabled=excluded.manually_disabled,last_checked_at=excluded.last_checked_at`,
		setting.Domain, boolToInt(setting.Enabled), boolToInt(setting.ManuallyDisabled), setting.LastCheckedAt)
}

func (a *App) recordDomainAutomaticSSLFailure(ctx context.Context, domain string, reason string, retryDelay time.Duration) {
	certificateDomain := normalizeDomainName(domain)
	if certificateDomain == "" {
		return
	}
	now := time.Now().UTC()
	retryAfter := now.Add(retryDelay)
	_, _ = a.db.ExecContext(ctx, `INSERT INTO domain_ssl_settings(domain,auto_ssl_enabled,manually_disabled,last_checked_at,last_failed_at,last_failure_reason,retry_after)
VALUES(?,?,?,?,?,?,?)
ON CONFLICT(domain) DO UPDATE SET last_checked_at=excluded.last_checked_at,last_failed_at=excluded.last_failed_at,last_failure_reason=excluded.last_failure_reason,retry_after=excluded.retry_after`,
		certificateDomain, 0, 0, now.Format(time.RFC3339), now.Format(time.RFC3339), strings.TrimSpace(reason), retryAfter.Format(time.RFC3339))
	if domainFromContext(ctx) != certificateDomain {
		_, _ = a.db.ExecContext(contextWithDomain(ctx, certificateDomain), `INSERT INTO domain_ssl_settings(domain,auto_ssl_enabled,manually_disabled,last_checked_at,last_failed_at,last_failure_reason,retry_after)
VALUES(?,?,?,?,?,?,?)
ON CONFLICT(domain) DO UPDATE SET last_checked_at=excluded.last_checked_at,last_failed_at=excluded.last_failed_at,last_failure_reason=excluded.last_failure_reason,retry_after=excluded.retry_after`,
			certificateDomain, 0, 0, now.Format(time.RFC3339), now.Format(time.RFC3339), strings.TrimSpace(reason), retryAfter.Format(time.RFC3339))
	}
}

func (a *App) clearDomainAutomaticSSLFailure(ctx context.Context, domain string) {
	certificateDomain := normalizeDomainName(domain)
	if certificateDomain == "" {
		return
	}
	_, _ = a.db.ExecContext(ctx, `UPDATE domain_ssl_settings SET last_failed_at=NULL,last_failure_reason=NULL,retry_after=NULL WHERE domain=?`, certificateDomain)
	if domainFromContext(ctx) != certificateDomain {
		_, _ = a.db.ExecContext(contextWithDomain(ctx, certificateDomain), `UPDATE domain_ssl_settings SET last_failed_at=NULL,last_failure_reason=NULL,retry_after=NULL WHERE domain=?`, certificateDomain)
	}
}

func (a *App) autoCertRetryDelayError(ctx context.Context, domain string, now time.Time) error {
	setting := a.domainAutomaticSSLSetting(ctx, domain)
	if strings.TrimSpace(setting.RetryAfter) == "" {
		return nil
	}
	retryAfter, err := time.Parse(time.RFC3339, setting.RetryAfter)
	if err != nil || !retryAfter.After(now) {
		return nil
	}
	reason := strings.TrimSpace(setting.LastFailureReason)
	if reason == "" {
		reason = "previous automatic SSL attempt failed"
	}
	return fmt.Errorf("automatic SSL retry for %q delayed until %s: %s", setting.Domain, retryAfter.Format(time.RFC3339), reason)
}

func automaticSSLRetryDelayForCertificateError(err error) time.Duration {
	if err == nil {
		return automaticSSLRetryDelayACME
	}
	errorText := strings.ToLower(err.Error())
	if strings.Contains(errorText, "dns") || strings.Contains(errorText, "does not point") || strings.Contains(errorText, "server ip") {
		return automaticSSLRetryDelayDNS
	}
	if strings.Contains(errorText, "challenge") || strings.Contains(errorText, "ports 80 and 443") || strings.Contains(errorText, "not listening") {
		return automaticSSLRetryDelayChallenge
	}
	if strings.Contains(errorText, "not managed") || strings.Contains(errorText, "invalid") || strings.Contains(errorText, "private") || strings.Contains(errorText, "reserved") || strings.Contains(errorText, "manually disabled") {
		return automaticSSLRetryDelayEligibility
	}
	return automaticSSLRetryDelayACME
}

func (a *App) listDomainAliases(ctx context.Context, siteDomain string) ([]DomainAlias, error) {
	aliasRows, err := a.db.QueryContext(ctx, `SELECT alias_domain,verification_token,is_verified,dns_a_ok,is_selected,last_checked_at FROM domain_aliases WHERE primary_domain=? ORDER BY alias_domain`, siteDomain)
	if err != nil {
		return nil, err
	}
	defer aliasRows.Close()
	domainAliases := make([]DomainAlias, 0, 10)
	for aliasRows.Next() {
		var domainAlias DomainAlias
		var verificationToken sql.NullString
		var lastCheckedAt sql.NullString
		var txtVerified int
		var aRecordVerified int
		var isSelected int
		if scanErr := aliasRows.Scan(&domainAlias.Domain, &verificationToken, &txtVerified, &aRecordVerified, &isSelected, &lastCheckedAt); scanErr != nil {
			return nil, scanErr
		}
		domainAlias.VerificationToken = verificationToken.String
		domainAlias.LastCheckedAt = lastCheckedAt.String
		domainAlias.TXTVerified = txtVerified == 1
		domainAlias.ARecordVerified = aRecordVerified == 1
		domainAlias.IsSelected = isSelected == 1
		domainAlias.IsActive = domainAlias.TXTVerified && domainAlias.ARecordVerified
		domainAliases = append(domainAliases, domainAlias)
	}
	return domainAliases, aliasRows.Err()
}

func boolToInt(state bool) int {
	if state {
		return 1
	}
	return 0
}

func (a *App) backupTokenForDomain(ctx context.Context, domain string) string {
	var token string
	err := a.db.QueryRowContext(ctx, `SELECT token FROM domain_backup_tokens WHERE domain=?`, domain).Scan(&token)
	if err == nil && strings.TrimSpace(token) != "" {
		return token
	}
	token = randomAccessToken()
	_, _ = a.db.ExecContext(ctx, `INSERT INTO domain_backup_tokens(domain,token,updated_at)
VALUES(?,?,?)
ON CONFLICT(domain) DO UPDATE SET token=excluded.token,updated_at=excluded.updated_at`,
		domain, token, time.Now().UTC().Format(time.RFC3339))
	return token
}

func (a *App) rotateBackupTokenForDomain(ctx context.Context, domain string) string {
	token := randomAccessToken()
	_, _ = a.db.ExecContext(ctx, `INSERT INTO domain_backup_tokens(domain,token,updated_at)
VALUES(?,?,?)
ON CONFLICT(domain) DO UPDATE SET token=excluded.token,updated_at=excluded.updated_at`,
		domain, token, time.Now().UTC().Format(time.RFC3339))
	return token
}

func (a *App) domainSettingsPage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		if !a.hasAdmin(r.Context(), a.siteDomain(r.Context(), r)) {
			http.Redirect(w, r, r.URL.Path+"?register", http.StatusFound)
			return
		}
		http.Redirect(w, r, loginURLForRequest(r), http.StatusFound)
		return
	}
	siteDomain := a.siteDomain(r.Context(), r)
	returnPath := r.FormValue("return")
	if strings.TrimSpace(returnPath) == "" {
		returnPath = r.URL.Query().Get("return")
	}
	if strings.TrimSpace(returnPath) == "" {
		returnPath = requestedReturnPath(r)
	}
	if r.Method == http.MethodPost {
		externalIP := ""
		action := strings.TrimSpace(r.FormValue("action"))
		if action == "add_alias" || action == "select_alias" || action == "check_alias" || action == "check_all" || action == "update_auto_ssl" {
			_, externalIP, _ = detectServerIPCandidates(r.Context())
		}
		a.handleDomainSettingsPost(r.Context(), r, siteDomain, externalIP)
		http.Redirect(w, r, returnPath+"?settings", http.StatusFound)
		return
	}
	serverIPs, externalIP, externalIPErr := detectServerIPCandidates(r.Context())
	domainAliases, err := a.listDomainAliases(r.Context(), siteDomain)
	if err != nil {
		http.Error(w, "failed to load domain aliases", http.StatusInternalServerError)
		return
	}
	chrootLocations, err := a.listDomainChrootLocations(r.Context(), siteDomain)
	if err != nil {
		http.Error(w, "failed to load chroot locations", http.StatusInternalServerError)
		return
	}
	selectedDomain := siteDomain
	for _, domainAlias := range domainAliases {
		if domainAlias.IsSelected && domainAlias.IsActive {
			selectedDomain = domainAlias.Domain
			break
		}
	}
	automaticSSLSetting := a.refreshDomainAutomaticSSLIfStale(r.Context(), selectedDomain, serverIPs)
	automaticSSLStatus := automaticSSLStatusView(automaticSSLSetting, externalIPErr)
	backupToken := a.backupTokenForDomain(r.Context(), siteDomain)
	backupDownloadPath := "/?backup_download&token=" + url.QueryEscape(backupToken)
	backupDownloadURL := absoluteURLForPath(r, backupDownloadPath)
	externalIPError := ""
	if externalIPErr != nil {
		externalIPError = externalIPErr.Error()
	}
	a.render(w, r, "domain_settings.html", map[string]any{
		"Domain":             siteDomain,
		"SelectedDomain":     selectedDomain,
		"Aliases":            domainAliases,
		"ChrootLocations":    chrootLocations,
		"AliasCount":         len(domainAliases),
		"CanAddAlias":        len(domainAliases) < 10,
		"ReturnPath":         returnPath,
		"ExternalIP":         externalIP,
		"ExternalIPError":    externalIPError,
		"AutomaticSSL":       automaticSSLSetting,
		"AutomaticSSLStatus": automaticSSLStatus,
		"AutomaticSSLDomain": automaticSSLSetting.Domain,
		"AutomaticSSLReady":  automaticSSLSetting.Available && automaticSSLSetting.Domain != "",
		"BackupDownloadURL":  backupDownloadURL,
		"BackupDownloadPath": backupDownloadPath,
		"NativeFileDialog":   a.nativeFileDialog,
	})
}

func (a *App) freezeDomain(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	domain := a.siteDomain(r.Context(), r)
	a.setDomainFrozenState(r.Context(), domain, 1)
	http.Redirect(w, r, requestedReturnPath(r), http.StatusFound)
}

func (a *App) publishDomain(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	domain := a.siteDomain(r.Context(), r)
	progressToken := strings.TrimSpace(r.FormValue("publish_token"))
	progressTracker := a.activePublishTracker()
	publishProgress := func(stage, currentPath string, completed, total int, message string) {
		if progressToken == "" {
			return
		}
		completedPercent := 0
		if total > 0 {
			completedPercent = int(math.Round(float64(completed) * 100 / float64(total)))
		}
		if completedPercent < 0 {
			completedPercent = 0
		}
		if completedPercent > 100 {
			completedPercent = 100
		}
		progressTracker.publish(publishProgressEvent{
			Token:            progressToken,
			Stage:            stage,
			CurrentPath:      currentPath,
			Completed:        completed,
			Total:            total,
			CompletedPercent: completedPercent,
			Message:          message,
		})
	}
	log.Printf("publish started domain=%s", domain)
	publishProgress("preparing", "", 0, 1, "preparing")
	pageList, err := a.collectPublishPageCandidates(r.Context(), domain)
	if err != nil {
		publishProgress("error", "", 0, 1, err.Error())
		http.Error(w, "failed to read pages for publishing", http.StatusInternalServerError)
		return
	}
	updatedPagesCount := 0
	skippedPagesCount := 0
	totalSteps := len(pageList) + 2
	if totalSteps < 2 {
		totalSteps = 2
	}
	for pageIndex, pageCandidate := range pageList {
		publishProgress("pages", pageCandidate.Path, pageIndex, totalSteps, pageCandidate.Path)
		if !a.shouldUpdatePublishedPage(r.Context(), domain, pageCandidate.Path, pageCandidate.HTML) {
			skippedPagesCount++
			continue
		}
		publishedHTMLBytes := int64(len([]byte(pageCandidate.HTML)))
		var previousPublishedHTML string
		_ = a.db.QueryRowContext(r.Context(), `SELECT html FROM published_pages WHERE domain=? AND path=?`, domain, pageCandidate.Path).Scan(&previousPublishedHTML)
		publishedPageDelta := publishedHTMLBytes - int64(len([]byte(previousPublishedHTML)))
		publishedStaticDelta := publishedHTMLBytes - fileSizeBytes(filepath.Join(a.domainStaticDir(domain), staticRelativePathForPage(pageCandidate.Path)))
		if storageErr := a.applyDomainStorageDelta(r.Context(), domain, 0, publishedPageDelta, 0, 0, publishedStaticDelta); storageErr != nil {
			log.Printf("publish skipped by storage limit domain=%s path=%s error=%v", domain, pageCandidate.Path, storageErr)
			skippedPagesCount++
			continue
		}
		_, _ = a.db.ExecContext(r.Context(), `INSERT OR REPLACE INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, domain, pageCandidate.Path, pageCandidate.Title, pageCandidate.HTML)
		a.writePublishedStaticHTML(domain, pageCandidate.Path, pageCandidate.HTML)
		updatedPagesCount++
		log.Printf("publish page updated domain=%s path=%s", domain, pageCandidate.Path)
	}
	log.Printf("publish pages processed domain=%s updated=%d unchanged=%d", domain, updatedPagesCount, skippedPagesCount)
	publishProgress("pack", "", len(pageList)+1, totalSteps, "pack")
	if packErr := a.generateDomainPack(domain); packErr != nil {
		log.Printf("publish pack failed domain=%s error=%v", domain, packErr)
	} else {
		log.Printf("publish pack updated domain=%s", domain)
	}
	publishProgress("unfreeze", "", totalSteps-1, totalSteps, "unfreeze")
	a.setDomainFrozenState(r.Context(), domain, 0)
	publishProgress("done", "", totalSteps, totalSteps, "done")
	log.Printf("publish completed domain=%s", domain)
	http.Redirect(w, r, requestedReturnPath(r), http.StatusFound)
}

func (a *App) publishPreviewJSON(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	domain := a.siteDomain(r.Context(), r)
	totalPagesCount, changedPagesCount, changedPagePaths := a.countPublishChanges(r.Context(), domain)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"total": totalPagesCount, "changed": changedPagesCount, "unchanged": totalPagesCount - changedPagesCount, "paths": changedPagePaths})
}

func (a *App) countPublishChanges(ctx context.Context, domain string) (int, int, []string) {
	changedPagesCount := 0
	changedPagePaths := make([]string, 0)
	pageList, err := a.collectPublishPageCandidates(ctx, domain)
	if err != nil {
		return 0, 0, changedPagePaths
	}
	for _, pageCandidate := range pageList {
		if a.shouldUpdatePublishedPage(ctx, domain, pageCandidate.Path, pageCandidate.HTML) {
			changedPagesCount++
			changedPagePaths = append(changedPagePaths, pageCandidate.Path)
		}
	}
	return len(pageList), changedPagesCount, changedPagePaths
}

func (a *App) collectPublishPageCandidates(ctx context.Context, domain string) ([]publishPageCandidate, error) {
	revisionRows, err := a.db.QueryContext(ctx, `SELECT page_path,html FROM revisions WHERE domain=? AND is_active=1 ORDER BY page_path ASC, id DESC`, domain)
	if err != nil {
		return nil, err
	}
	defer revisionRows.Close()
	latestRevisionByPath := make(map[string]string)
	for revisionRows.Next() {
		var pagePath string
		var pageHTML string
		if scanErr := revisionRows.Scan(&pagePath, &pageHTML); scanErr != nil {
			continue
		}
		if _, alreadyStored := latestRevisionByPath[pagePath]; !alreadyStored {
			latestRevisionByPath[pagePath] = pageHTML
		}
	}
	pageRows, pageQueryErr := a.db.QueryContext(ctx, `SELECT path,title,html FROM pages WHERE domain=? ORDER BY path ASC`, domain)
	if pageQueryErr != nil {
		return nil, pageQueryErr
	}
	defer pageRows.Close()
	pageList := make([]publishPageCandidate, 0)
	for pageRows.Next() {
		var pagePath, pageTitle, draftHTML string
		if scanErr := pageRows.Scan(&pagePath, &pageTitle, &draftHTML); scanErr != nil {
			continue
		}
		pageHTMLToPublish := draftHTML
		if latestActiveHTML, foundLatestActiveRevision := latestRevisionByPath[pagePath]; foundLatestActiveRevision {
			pageHTMLToPublish = latestActiveHTML
		}
		pageList = append(pageList, publishPageCandidate{Path: pagePath, Title: pageTitle, HTML: pageHTMLToPublish})
	}
	return pageList, nil
}

func normalizePublishedHTML(html string) string {
	return strings.TrimSpace(strings.ReplaceAll(html, "\r\n", "\n"))
}

func (a *App) shouldUpdatePublishedPage(ctx context.Context, domain, pagePath, nextRenderedHTML string) bool {
	var previousPublishedHTML string
	publishedErr := a.db.QueryRowContext(ctx, `SELECT html FROM published_pages WHERE domain=? AND path=?`, domain, pagePath).Scan(&previousPublishedHTML)
	if publishedErr != nil || normalizePublishedHTML(previousPublishedHTML) != normalizePublishedHTML(nextRenderedHTML) {
		return true
	}
	staticFilePath := filepath.Join(a.domainStaticDir(domain), staticRelativePathForPage(pagePath))
	previousRenderedHTMLBytes, readErr := os.ReadFile(staticFilePath)
	if readErr != nil {
		return true
	}
	return normalizePublishedHTML(string(previousRenderedHTMLBytes)) != normalizePublishedHTML(nextRenderedHTML)
}

func (a *App) downloadBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	domain := a.siteDomain(r.Context(), r)
	if !a.isAdminRequest(r) {
		requestedToken := strings.TrimSpace(r.URL.Query().Get("token"))
		backupToken := a.backupTokenForDomain(r.Context(), domain)
		if requestedToken == "" || requestedToken != backupToken {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	fileName := backupFileName(domain)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+fileName+"\"")
	if err := a.writeDomainBackupZIP(r.Context(), domain, w); err != nil {
		log.Printf("backup download failed domain=%s error=%v", domain, err)
		http.Error(w, "failed to create backup", http.StatusInternalServerError)
		return
	}
}

func (a *App) nativeSaveBackupJSON(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !a.nativeFileDialog {
		http.Error(w, "native save dialog is unavailable", http.StatusNotImplemented)
		return
	}

	domain := a.siteDomain(r.Context(), r)
	fileName := backupFileName(domain)
	destinationPath, chooseErr := desktop.ChooseSaveFilePath(fileName)
	if chooseErr != nil {
		if errors.Is(chooseErr, desktop.ErrNativeDialogCanceled) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(nativeSavedBackupResponse{Saved: false, Canceled: true})
			return
		}
		http.Error(w, chooseErr.Error(), http.StatusInternalServerError)
		return
	}

	destinationFile, err := os.Create(destinationPath)
	if err != nil {
		http.Error(w, "failed to open backup destination", http.StatusInternalServerError)
		return
	}
	writeErr := a.writeDomainBackupZIP(r.Context(), domain, destinationFile)
	closeErr := destinationFile.Close()
	if writeErr != nil {
		_ = os.Remove(destinationPath)
		log.Printf("native backup save failed domain=%s error=%v", domain, writeErr)
		http.Error(w, "failed to create backup", http.StatusInternalServerError)
		return
	}
	if closeErr != nil {
		_ = os.Remove(destinationPath)
		http.Error(w, "failed to finish backup file", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(nativeSavedBackupResponse{Saved: true, Path: destinationPath})
}

func backupFileName(domain string) string {
	return domainStorageName(domain) + "-backup-" + time.Now().UTC().Format("20060102-150405") + ".zip"
}

func (a *App) importBackup(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(512 << 20); err != nil {
		http.Error(w, "failed to parse upload", http.StatusBadRequest)
		return
	}
	backupFile, backupFileHeader, err := r.FormFile("backup_zip")
	if err != nil {
		http.Error(w, "backup zip is required", http.StatusBadRequest)
		return
	}
	defer backupFile.Close()
	if backupFileHeader == nil {
		http.Error(w, "backup zip is empty", http.StatusBadRequest)
		return
	}
	tempFile, err := os.CreateTemp("", "sitebrush-backup-*.zip")
	if err != nil {
		http.Error(w, "failed to stage backup zip", http.StatusInternalServerError)
		return
	}
	tempFilePath := tempFile.Name()
	defer os.Remove(tempFilePath)
	writtenBytes, copyErr := io.Copy(tempFile, backupFile)
	if closeErr := tempFile.Close(); closeErr != nil && copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		http.Error(w, "failed to stage backup zip", http.StatusInternalServerError)
		return
	}
	if writtenBytes <= 0 {
		http.Error(w, "backup zip is empty", http.StatusBadRequest)
		return
	}
	readerAt, err := os.Open(tempFilePath)
	if err != nil {
		http.Error(w, "failed to open backup zip", http.StatusInternalServerError)
		return
	}
	defer readerAt.Close()
	backupZIP, err := zip.NewReader(readerAt, writtenBytes)
	if err != nil {
		http.Error(w, "invalid backup zip", http.StatusBadRequest)
		return
	}
	domain := a.siteDomain(r.Context(), r)
	importBasePath := normalizeImportBasePath(r.FormValue("base_path"))
	redirectPath, err := a.importDomainBackupZIP(r.Context(), domain, importBasePath, backupZIP)
	if err != nil {
		http.Error(w, "failed to import backup: "+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, redirectPath+"?visual", http.StatusFound)
}

func (a *App) generateDomainPack(domain string) error {
	domainDirName := domainStorageName(domain)
	packsDirPath := a.packsDir()
	if makeErr := os.MkdirAll(packsDirPath, 0o755); makeErr != nil {
		return makeErr
	}
	packFilePath := filepath.Join(packsDirPath, domainDirName+".zip")
	packFile, createErr := os.Create(packFilePath)
	if createErr != nil {
		return createErr
	}
	defer packFile.Close()
	return a.writeDomainBackupZIP(context.Background(), domain, packFile)
}

func (a *App) writeDomainBackupZIP(ctx context.Context, domain string, writer io.Writer) error {
	backup, err := a.collectDomainBackup(ctx, domain)
	if err != nil {
		return err
	}
	backupJSON, err := json.MarshalIndent(backup, "", "  ")
	if err != nil {
		return err
	}
	zipWriter := zip.NewWriter(writer)
	backupEntryWriter, err := zipWriter.Create("backup.json")
	if err != nil {
		_ = zipWriter.Close()
		return err
	}
	if _, err := backupEntryWriter.Write(backupJSON); err != nil {
		_ = zipWriter.Close()
		return err
	}
	pageArchivePathBySitePath := staticExportPageArchivePaths(backup.Pages)
	for _, backupPage := range backup.Pages {
		archivePath := pageArchivePathBySitePath[cleanPath(backupPage.Path)]
		if archivePath == "" {
			continue
		}
		pageFileWriter, createErr := zipWriter.Create(archivePath)
		if createErr != nil {
			_ = zipWriter.Close()
			return createErr
		}
		pageHTML := backupPage.HTML
		if shouldRewriteStaticExportDocument(backupPage.Path, pageHTML) {
			pageHTML = rewriteStaticExportText(pageHTML, domain, backupPage.Path, archivePath, pageArchivePathBySitePath)
		}
		if _, writeErr := io.WriteString(pageFileWriter, pageHTML); writeErr != nil {
			_ = zipWriter.Close()
			return writeErr
		}
	}
	rewriteArchiveFile := func(archivePath string, fileBytes []byte) []byte {
		if !shouldRewriteStaticExportAsset(archivePath) {
			return fileBytes
		}
		sitePath := "/" + strings.TrimPrefix(filepath.ToSlash(archivePath), "/")
		return []byte(rewriteStaticExportText(string(fileBytes), domain, sitePath, archivePath, pageArchivePathBySitePath))
	}
	if addFilesErr := addDirectoryToZip(zipWriter, a.domainFilesDirForDomain(domain), "p", rewriteArchiveFile); addFilesErr != nil {
		_ = zipWriter.Close()
		return addFilesErr
	}
	if closeZipErr := zipWriter.Close(); closeZipErr != nil {
		return closeZipErr
	}
	return nil
}

func (a *App) collectDomainBackup(ctx context.Context, domain string) (domainBackup, error) {
	backup := domainBackup{
		Version:         1,
		ExportedAt:      time.Now().UTC().Format(time.RFC3339),
		Domain:          domain,
		Pages:           make([]backupPage, 0, 64),
		FileMetadata:    make([]backupFileMetadata, 0, 128),
		FileAccessRules: make([]backupFileAccessRule, 0, 128),
		Redirects:       make([]backupRedirect, 0, 64),
	}
	latestRevisionHTMLByPath := make(map[string]string)
	revisionRows, revisionErr := a.db.QueryContext(ctx, `SELECT page_path,html FROM revisions WHERE domain=? AND is_active=1 ORDER BY page_path ASC, id DESC`, domain)
	if revisionErr == nil {
		for revisionRows.Next() {
			var pagePath string
			var pageHTML string
			if scanErr := revisionRows.Scan(&pagePath, &pageHTML); scanErr != nil {
				continue
			}
			normalizedPath := cleanPath(pagePath)
			if _, alreadyStored := latestRevisionHTMLByPath[normalizedPath]; alreadyStored {
				continue
			}
			latestRevisionHTMLByPath[normalizedPath] = pageHTML
		}
		_ = revisionRows.Close()
	}
	pageRows, err := a.db.QueryContext(ctx, `SELECT path,title,html,published FROM pages WHERE domain=? ORDER BY path ASC`, domain)
	if err != nil {
		return backup, err
	}
	for pageRows.Next() {
		var page backupPage
		if scanErr := pageRows.Scan(&page.Path, &page.Title, &page.HTML, &page.Published); scanErr != nil {
			continue
		}
		page.Path = cleanPath(page.Path)
		if revisionHTML, foundRevision := latestRevisionHTMLByPath[page.Path]; foundRevision {
			page.HTML = revisionHTML
		}
		backup.Pages = append(backup.Pages, page)
	}
	_ = pageRows.Close()

	metadataRows, metadataErr := a.db.QueryContext(ctx, `SELECT file_name,page_path,size,mime_type,created_at,download_count FROM file_metadata WHERE domain=? ORDER BY file_name ASC`, domainStorageName(domain))
	if metadataErr == nil {
		for metadataRows.Next() {
			var currentMetadata backupFileMetadata
			if scanErr := metadataRows.Scan(&currentMetadata.FileName, &currentMetadata.PagePath, &currentMetadata.Size, &currentMetadata.MimeType, &currentMetadata.CreatedAt, &currentMetadata.DownloadCount); scanErr != nil {
				continue
			}
			currentMetadata.FileName = safeRelativeAssetPath(currentMetadata.FileName)
			if currentMetadata.FileName == "" {
				continue
			}
			currentMetadata.PagePath = cleanPath(currentMetadata.PagePath)
			backup.FileMetadata = append(backup.FileMetadata, currentMetadata)
		}
		_ = metadataRows.Close()
	}

	accessRows, accessErr := a.db.QueryContext(ctx, `SELECT file_name,access_mode,token,expires_at,single_use_left,token_use_count FROM file_access_rules WHERE domain=? ORDER BY file_name ASC`, domainStorageName(domain))
	if accessErr == nil {
		for accessRows.Next() {
			var currentRule backupFileAccessRule
			if scanErr := accessRows.Scan(&currentRule.FileName, &currentRule.AccessMode, &currentRule.Token, &currentRule.ExpiresAt, &currentRule.SingleUseLeft, &currentRule.TokenUseCount); scanErr != nil {
				continue
			}
			currentRule.FileName = safeRelativeAssetPath(currentRule.FileName)
			if currentRule.FileName == "" {
				continue
			}
			backup.FileAccessRules = append(backup.FileAccessRules, currentRule)
		}
		_ = accessRows.Close()
	}

	redirectRows, redirectErr := a.db.QueryContext(ctx, `SELECT old_path,new_path FROM page_redirects WHERE domain=? ORDER BY old_path ASC`, domain)
	if redirectErr == nil {
		for redirectRows.Next() {
			var currentRedirect backupRedirect
			if scanErr := redirectRows.Scan(&currentRedirect.OldPath, &currentRedirect.NewPath); scanErr != nil {
				continue
			}
			currentRedirect.OldPath = cleanPath(currentRedirect.OldPath)
			currentRedirect.NewPath = cleanPath(currentRedirect.NewPath)
			backup.Redirects = append(backup.Redirects, currentRedirect)
		}
		_ = redirectRows.Close()
	}
	return backup, nil
}

func addDirectoryToZip(zipWriter *zip.Writer, sourceDirPath, archiveDirPrefix string, rewriteFile func(string, []byte) []byte) error {
	directoryInfo, statErr := os.Stat(sourceDirPath)
	if statErr != nil || !directoryInfo.IsDir() {
		return nil
	}
	return filepath.WalkDir(sourceDirPath, func(currentPath string, currentEntry fs.DirEntry, walkErr error) error {
		if walkErr != nil || currentEntry.IsDir() {
			return walkErr
		}
		relativePath, relErr := filepath.Rel(sourceDirPath, currentPath)
		if relErr != nil {
			return relErr
		}
		archivePath := filepath.ToSlash(filepath.Join(archiveDirPrefix, relativePath))
		archiveFileWriter, createErr := zipWriter.Create(archivePath)
		if createErr != nil {
			return createErr
		}
		if rewriteFile != nil && shouldReadStaticExportArchiveFile(archivePath) {
			sourceBytes, readErr := os.ReadFile(currentPath)
			if readErr != nil {
				return readErr
			}
			_, writeErr := archiveFileWriter.Write(rewriteFile(archivePath, sourceBytes))
			return writeErr
		}
		sourceFile, openErr := os.Open(currentPath)
		if openErr != nil {
			return openErr
		}
		defer sourceFile.Close()
		_, copyErr := io.Copy(archiveFileWriter, sourceFile)
		return copyErr
	})
}

func staticExportPageArchivePaths(pages []backupPage) map[string]string {
	pageArchivePathBySitePath := make(map[string]string, len(pages))
	for _, page := range pages {
		pagePath := cleanPath(page.Path)
		archivePath := staticArchivePathForURI(pagePath)
		if archivePath == "" {
			continue
		}
		pageArchivePathBySitePath[pagePath] = archivePath
	}
	return pageArchivePathBySitePath
}

func shouldRewriteStaticExportDocument(pagePath, content string) bool {
	switch strings.ToLower(path.Ext(strings.TrimSpace(pagePath))) {
	case ".css", ".htm", ".html", ".js", ".mjs", ".svg":
		return true
	case "":
		return pageContentKind(pagePath, content) == "html"
	default:
		return false
	}
}

func shouldRewriteStaticExportAsset(archivePath string) bool {
	switch strings.ToLower(path.Ext(strings.TrimSpace(archivePath))) {
	case ".css", ".htm", ".html", ".js", ".mjs", ".svg":
		return true
	default:
		return false
	}
}

func shouldReadStaticExportArchiveFile(archivePath string) bool {
	return shouldRewriteStaticExportAsset(archivePath)
}

func rewriteStaticExportText(source, domain, sourceSitePath, sourceArchivePath string, pageArchivePathBySitePath map[string]string) string {
	switch strings.ToLower(path.Ext(strings.TrimSpace(sourceArchivePath))) {
	case ".js", ".mjs":
		return rewriteStaticExportJavaScriptReferences(source, domain, sourceSitePath, sourceArchivePath, pageArchivePathBySitePath)
	default:
		return rewriteStaticExportDocumentLinks(source, domain, sourceSitePath, sourceArchivePath, pageArchivePathBySitePath)
	}
}

func rewriteStaticExportDocumentLinks(source, domain, sourceSitePath, sourceArchivePath string, pageArchivePathBySitePath map[string]string) string {
	if strings.TrimSpace(source) == "" {
		return source
	}
	rewriteSingle := func(rawReference string) string {
		return rewriteStaticExportReference(rawReference, domain, sourceSitePath, sourceArchivePath, pageArchivePathBySitePath)
	}
	return grabber.RewriteDocumentResourceReferences(source, rewriteSingle)
}

func rewriteStaticExportJavaScriptReferences(source, domain, sourceSitePath, sourceArchivePath string, pageArchivePathBySitePath map[string]string) string {
	if strings.TrimSpace(source) == "" {
		return source
	}
	var rewritten strings.Builder
	for index := 0; index < len(source); {
		quote := source[index]
		if quote != '\'' && quote != '"' && quote != '`' {
			rewritten.WriteByte(source[index])
			index++
			continue
		}
		stringEnd := index + 1
		escaped := false
		for stringEnd < len(source) {
			currentByte := source[stringEnd]
			if escaped {
				escaped = false
				stringEnd++
				continue
			}
			if currentByte == '\\' {
				escaped = true
				stringEnd++
				continue
			}
			if currentByte == quote {
				break
			}
			stringEnd++
		}
		if stringEnd >= len(source) {
			rewritten.WriteString(source[index:])
			break
		}
		literalValue := source[index+1 : stringEnd]
		nextLiteralValue := literalValue
		if !strings.Contains(literalValue, `\`) && !(quote == '`' && strings.Contains(literalValue, "${")) {
			nextLiteralValue = rewriteStaticExportReference(literalValue, domain, sourceSitePath, sourceArchivePath, pageArchivePathBySitePath)
		}
		rewritten.WriteByte(quote)
		rewritten.WriteString(nextLiteralValue)
		rewritten.WriteByte(quote)
		index = stringEnd + 1
	}
	return rewritten.String()
}

func rewriteStaticExportReference(rawReference, domain, sourceSitePath, sourceArchivePath string, pageArchivePathBySitePath map[string]string) string {
	trimmedReference := strings.TrimSpace(rawReference)
	if trimmedReference == "" || strings.HasPrefix(trimmedReference, "#") {
		return rawReference
	}
	loweredReference := strings.ToLower(trimmedReference)
	for _, blockedPrefix := range []string{"mailto:", "tel:", "javascript:", "data:", "blob:", "about:"} {
		if strings.HasPrefix(loweredReference, blockedPrefix) {
			return rawReference
		}
	}
	if parsedReference, parseErr := url.Parse(trimmedReference); parseErr == nil {
		if parsedReference.IsAbs() || parsedReference.Host != "" {
			if !sameStaticExportHost(parsedReference.Host, domain) {
				return rawReference
			}
			if rewrittenReference, foundReference := staticExportReferenceForSitePath(parsedReference.Path, staticExportURLSuffix(parsedReference), sourceArchivePath, pageArchivePathBySitePath); foundReference {
				return rewrittenReference
			}
			return rawReference
		}
	}
	pathPart, suffixPart := splitReferencePathAndSuffix(trimmedReference)
	if pathPart == "" {
		return rawReference
	}
	if strings.HasPrefix(pathPart, "/") {
		if rewrittenReference, foundReference := staticExportReferenceForSitePath(pathPart, suffixPart, sourceArchivePath, pageArchivePathBySitePath); foundReference {
			return rewrittenReference
		}
		return rawReference
	}
	for _, resolvedSitePath := range resolveStaticExportRelativeSitePaths(pathPart, sourceSitePath) {
		rewrittenReference, foundReference := staticExportReferenceForSitePath(resolvedSitePath, suffixPart, sourceArchivePath, pageArchivePathBySitePath)
		if foundReference {
			return rewrittenReference
		}
	}
	return rawReference
}

func staticExportReferenceForSitePath(sitePath, suffixPart, sourceArchivePath string, pageArchivePathBySitePath map[string]string) (string, bool) {
	targetArchivePath, foundTarget := staticExportTargetArchivePath(sitePath, pageArchivePathBySitePath)
	if !foundTarget {
		return "", false
	}
	return relativeArchiveReference(sourceArchivePath, targetArchivePath) + suffixPart, true
}

func staticExportTargetArchivePath(sitePath string, pageArchivePathBySitePath map[string]string) (string, bool) {
	normalizedSitePath := cleanPath(sitePath)
	if strings.HasPrefix(normalizedSitePath, "/p/") {
		assetPath := safeRelativeAssetPath(strings.TrimPrefix(normalizedSitePath, "/p/"))
		if assetPath == "" {
			return "", false
		}
		return path.Join("p", assetPath), true
	}
	archivePath, found := pageArchivePathBySitePath[normalizedSitePath]
	return archivePath, found
}

func relativeArchiveReference(sourceArchivePath, targetArchivePath string) string {
	sourceDir := path.Dir(filepath.ToSlash(sourceArchivePath))
	if sourceDir == "." {
		sourceDir = ""
	}
	relativePath, err := filepath.Rel(filepath.FromSlash(sourceDir), filepath.FromSlash(targetArchivePath))
	if err != nil {
		return targetArchivePath
	}
	relativePath = filepath.ToSlash(relativePath)
	if relativePath == "." || relativePath == "" {
		return path.Base(targetArchivePath)
	}
	return relativePath
}

func sameStaticExportHost(referenceHost, domain string) bool {
	referenceName := normalizedStaticExportHost(referenceHost)
	domainName := normalizedStaticExportHost(domain)
	return referenceName != "" && domainName != "" && referenceName == domainName
}

func normalizedStaticExportHost(rawHost string) string {
	trimmedHost := strings.ToLower(strings.Trim(strings.TrimSpace(rawHost), "[]"))
	if trimmedHost == "" {
		return ""
	}
	hostURL := &url.URL{Scheme: "http", Host: trimmedHost}
	if parsedHost := hostURL.Hostname(); parsedHost != "" {
		return canonicalLocalDomain(parsedHost)
	}
	if parsedHost, _, splitErr := net.SplitHostPort(trimmedHost); splitErr == nil {
		return canonicalLocalDomain(parsedHost)
	}
	return canonicalLocalDomain(trimmedHost)
}

func staticExportURLSuffix(parsedReference *url.URL) string {
	var suffixBuilder strings.Builder
	if parsedReference.ForceQuery || parsedReference.RawQuery != "" {
		suffixBuilder.WriteString("?")
		suffixBuilder.WriteString(parsedReference.RawQuery)
	}
	if parsedReference.Fragment != "" {
		suffixBuilder.WriteString("#")
		suffixBuilder.WriteString(parsedReference.EscapedFragment())
	}
	return suffixBuilder.String()
}

func splitReferencePathAndSuffix(rawReference string) (string, string) {
	markerIndex := strings.IndexAny(rawReference, "?#")
	if markerIndex < 0 {
		return rawReference, ""
	}
	return rawReference[:markerIndex], rawReference[markerIndex:]
}

func resolveStaticExportRelativeSitePaths(rawPath, sourceSitePath string) []string {
	normalizedSourcePath := cleanPath(sourceSitePath)
	baseURL := &url.URL{Scheme: "http", Host: "sitebrush.local", Path: normalizedSourcePath}
	referenceURL := &url.URL{Path: rawPath}
	resolvedPaths := []string{cleanPath(baseURL.ResolveReference(referenceURL).Path)}
	if path.Ext(normalizedSourcePath) == "" && !strings.HasSuffix(normalizedSourcePath, "/") {
		directoryBaseURL := &url.URL{Scheme: "http", Host: "sitebrush.local", Path: normalizedSourcePath + "/"}
		directoryResolvedPath := cleanPath(directoryBaseURL.ResolveReference(referenceURL).Path)
		if directoryResolvedPath != resolvedPaths[0] {
			resolvedPaths = append(resolvedPaths, directoryResolvedPath)
		}
	}
	return resolvedPaths
}

func (a *App) importDomainBackupZIP(ctx context.Context, domain string, importBasePath string, backupZIP *zip.Reader) (string, error) {
	var backupJSON []byte
	filesByName := make(map[string]*zip.File)
	for _, zipEntry := range backupZIP.File {
		entryName := filepath.ToSlash(strings.TrimSpace(zipEntry.Name))
		switch {
		case entryName == "backup.json":
			reader, openErr := zipEntry.Open()
			if openErr != nil {
				return "/", openErr
			}
			payload, readErr := io.ReadAll(reader)
			_ = reader.Close()
			if readErr != nil {
				return "/", readErr
			}
			backupJSON = payload
		case strings.HasPrefix(entryName, "files/"):
			relativeName := safeRelativeAssetPath(strings.TrimPrefix(entryName, "files/"))
			if relativeName == "" || strings.HasSuffix(entryName, "/") {
				continue
			}
			filesByName[relativeName] = zipEntry
		case strings.HasPrefix(entryName, "p/"):
			relativeName := safeRelativeAssetPath(strings.TrimPrefix(entryName, "p/"))
			if relativeName == "" || strings.HasSuffix(entryName, "/") {
				continue
			}
			filesByName[relativeName] = zipEntry
		}
	}
	if len(backupJSON) == 0 {
		return "/", errors.New("backup.json is missing")
	}
	var backup domainBackup
	if err := json.Unmarshal(backupJSON, &backup); err != nil {
		return "/", err
	}
	if backup.Version <= 0 {
		return "/", errors.New("unsupported backup format")
	}

	basePath := normalizeImportBasePath(importBasePath)
	filePrefix := importFilePrefix(basePath)
	rootRedirectPath := applyImportBasePath(basePath, "/")
	domainDir := a.domainFilesDirForDomain(domain)
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		return rootRedirectPath, err
	}

	for _, currentPage := range backup.Pages {
		nextPagePath := applyImportBasePath(basePath, currentPage.Path)
		nextPageHTML := rewriteBackupInternalLinks(currentPage.HTML, basePath, filePrefix)
		nextTitle := strings.TrimSpace(currentPage.Title)
		if nextTitle == "" {
			nextTitle = nextPagePath
		}
		a.clearPageRedirectSource(ctx, domain, nextPagePath)
		_, _ = a.db.ExecContext(ctx, `INSERT OR REPLACE INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, domain, nextPagePath, nextTitle, nextPageHTML)
		_, _ = a.db.ExecContext(ctx, `INSERT OR REPLACE INTO published_pages(domain,path,title,html) VALUES(?,?,?,?)`, domain, nextPagePath, nextTitle, nextPageHTML)
		_, _ = a.db.ExecContext(ctx, `INSERT INTO revisions(domain,page_path,html,created_at,is_active) VALUES(?,?,?,?,1)`, domain, nextPagePath, nextPageHTML, time.Now().UTC().Format(time.RFC3339))
		a.writePublishedStaticHTML(domain, nextPagePath, nextPageHTML)
	}

	for _, currentRedirect := range backup.Redirects {
		oldPath := applyImportBasePath(basePath, currentRedirect.OldPath)
		newPath := applyImportBasePath(basePath, currentRedirect.NewPath)
		if oldPath == newPath {
			continue
		}
		redirectCreatedAt := time.Now().UTC().Format(time.RFC3339)
		_, _ = a.db.ExecContext(ctx, `INSERT INTO page_redirects(domain,old_path,new_path,created_at) VALUES(?,?,?,?) ON CONFLICT(domain,old_path) DO UPDATE SET new_path=excluded.new_path, created_at=excluded.created_at`, domain, oldPath, newPath, redirectCreatedAt)
	}

	for sourceFileName, zipEntry := range filesByName {
		nextFileName := applyImportFilePrefix(filePrefix, sourceFileName)
		if nextFileName == "" {
			continue
		}
		entryReader, openErr := zipEntry.Open()
		if openErr != nil {
			return rootRedirectPath, openErr
		}
		fileBytes, readErr := io.ReadAll(entryReader)
		_ = entryReader.Close()
		if readErr != nil {
			return rootRedirectPath, readErr
		}
		if shouldRewriteImportedTextFile(nextFileName) {
			fileBytes = []byte(rewriteBackupInternalLinks(string(fileBytes), basePath, filePrefix))
		}
		targetFilePath := filepath.Join(domainDir, filepath.FromSlash(nextFileName))
		if err := os.MkdirAll(filepath.Dir(targetFilePath), 0o755); err != nil {
			return rootRedirectPath, err
		}
		if err := os.WriteFile(targetFilePath, fileBytes, 0o644); err != nil {
			return rootRedirectPath, err
		}
	}

	for _, metadata := range backup.FileMetadata {
		nextFileName := applyImportFilePrefix(filePrefix, metadata.FileName)
		if nextFileName == "" {
			continue
		}
		nextPagePath := applyImportBasePath(basePath, metadata.PagePath)
		if nextPagePath == "/" && rootRedirectPath != "/" {
			nextPagePath = rootRedirectPath
		}
		a.upsertFileMetadata(ctx, domainStorageName(domain), nextFileName, nextPagePath, metadata.Size, metadata.MimeType, "backup-import")
		_, _ = a.db.ExecContext(ctx, `UPDATE file_metadata SET download_count=? WHERE domain=? AND file_name=?`, metadata.DownloadCount, domainStorageName(domain), nextFileName)
	}

	for _, accessRule := range backup.FileAccessRules {
		nextFileName := applyImportFilePrefix(filePrefix, accessRule.FileName)
		if nextFileName == "" {
			continue
		}
		nextAccessMode := strings.TrimSpace(accessRule.AccessMode)
		if nextAccessMode == "" {
			nextAccessMode = "public"
		}
		_, _ = a.db.ExecContext(ctx, `INSERT INTO file_access_rules(domain,file_name,access_mode,token,expires_at,single_use_left,token_use_count)
VALUES(?,?,?,?,?,?,?)
ON CONFLICT(domain,file_name) DO UPDATE SET access_mode=excluded.access_mode,token=excluded.token,expires_at=excluded.expires_at,single_use_left=excluded.single_use_left,token_use_count=excluded.token_use_count`,
			domainStorageName(domain), nextFileName, nextAccessMode, accessRule.Token, accessRule.ExpiresAt, accessRule.SingleUseLeft, accessRule.TokenUseCount)
	}
	a.rebuildDomainStorageUsage(ctx, domain)
	return rootRedirectPath, nil
}

func staticArchivePathForURI(pageURI string) string {
	normalizedPath := cleanPath(pageURI)
	trimmedPath := strings.TrimPrefix(normalizedPath, "/")
	if trimmedPath == "" {
		return "index.html"
	}
	lastSlashIndex := strings.LastIndex(trimmedPath, "/")
	lastSegment := trimmedPath
	if lastSlashIndex >= 0 {
		lastSegment = trimmedPath[lastSlashIndex+1:]
	}
	if strings.Contains(lastSegment, ".") {
		return trimmedPath
	}
	return path.Join(trimmedPath, "index.html")
}

func normalizeImportBasePath(rawBasePath string) string {
	if strings.TrimSpace(rawBasePath) == "" {
		return "/"
	}
	return cleanPath(rawBasePath)
}

func applyImportBasePath(basePath, sourcePath string) string {
	normalizedSourcePath := cleanPath(sourcePath)
	if basePath == "/" {
		return normalizedSourcePath
	}
	if normalizedSourcePath == "/" {
		return basePath
	}
	return cleanPath(basePath + normalizedSourcePath)
}

func importFilePrefix(basePath string) string {
	if basePath == "/" {
		return ""
	}
	return strings.TrimPrefix(basePath, "/")
}

func applyImportFilePrefix(filePrefix, sourceFileName string) string {
	normalizedFileName := safeRelativeAssetPath(sourceFileName)
	if normalizedFileName == "" {
		return ""
	}
	if filePrefix == "" {
		return normalizedFileName
	}
	return safeRelativeAssetPath(strings.Trim(filePrefix, "/") + "/" + normalizedFileName)
}

func shouldRewriteImportedTextFile(fileName string) bool {
	switch strings.ToLower(path.Ext(fileName)) {
	case ".html", ".htm", ".css":
		return true
	default:
		return false
	}
}

func rewriteBackupInternalLinks(source, basePath, filePrefix string) string {
	if strings.TrimSpace(source) == "" {
		return source
	}
	if basePath == "/" && filePrefix == "" {
		return source
	}
	rewriteSingle := func(rawReference string) string {
		return rewriteBackupReference(rawReference, basePath, filePrefix)
	}
	return grabber.RewriteDocumentResourceReferences(source, rewriteSingle)
}

func rewriteBackupReference(rawReference, basePath, filePrefix string) string {
	trimmedReference := strings.TrimSpace(rawReference)
	if trimmedReference == "" {
		return rawReference
	}
	loweredReference := strings.ToLower(trimmedReference)
	for _, blockedPrefix := range []string{"mailto:", "tel:", "javascript:", "data:", "blob:"} {
		if strings.HasPrefix(loweredReference, blockedPrefix) {
			return rawReference
		}
	}
	if strings.HasPrefix(trimmedReference, "//") {
		return rawReference
	}
	parsedReference, parseErr := url.Parse(trimmedReference)
	if parseErr == nil && (parsedReference.IsAbs() || parsedReference.Host != "") {
		return rawReference
	}
	pathPart := trimmedReference
	suffixPart := ""
	markerIndex := strings.IndexAny(pathPart, "?#")
	if markerIndex >= 0 {
		suffixPart = pathPart[markerIndex:]
		pathPart = pathPart[:markerIndex]
	}
	if strings.HasPrefix(pathPart, "/p/") {
		if filePrefix == "" {
			return pathPart + suffixPart
		}
		trimmedAssetPath := strings.TrimPrefix(pathPart, "/p/")
		nextFileName := applyImportFilePrefix(filePrefix, trimmedAssetPath)
		if nextFileName == "" {
			return pathPart + suffixPart
		}
		return "/p/" + nextFileName + suffixPart
	}
	if strings.HasPrefix(pathPart, "/") {
		if basePath == "/" {
			return pathPart + suffixPart
		}
		if pathPart == basePath || strings.HasPrefix(pathPart, basePath+"/") {
			return pathPart + suffixPart
		}
		return cleanPath(basePath+pathPart) + suffixPart
	}
	return rawReference
}

func (a *App) findPublishedPage(ctx context.Context, domain, pagePath string) (Page, error) {
	var current Page
	err := a.db.QueryRowContext(ctx, `SELECT domain,path,title,html FROM published_pages WHERE domain=? AND path=?`, domain, pagePath).Scan(&current.Domain, &current.Path, &current.Title, &current.HTML)
	return current, err
}

func (a *App) siteTreeJSON(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	domain := a.siteDomain(r.Context(), r)
	pageRows, err := a.db.QueryContext(r.Context(), `SELECT path FROM pages WHERE domain=? ORDER BY path ASC`, domain)
	if err != nil {
		http.Error(w, "failed to read site tree", http.StatusInternalServerError)
		return
	}
	defer pageRows.Close()
	pathList := []string{"/"}
	for pageRows.Next() {
		var pagePath string
		if scanErr := pageRows.Scan(&pagePath); scanErr != nil {
			continue
		}
		pathList = append(pathList, pagePath)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"current_path": requestedReturnPath(r), "paths": pathList})
}

func (a *App) setDomainFrozenState(ctx context.Context, domain string, frozenState int) {
	updateResult, updateErr := a.db.ExecContext(ctx, `UPDATE domain_states SET is_frozen=? WHERE domain=?`, frozenState, domain)
	if updateErr == nil {
		updatedRowsCount, rowsErr := updateResult.RowsAffected()
		if rowsErr == nil && updatedRowsCount > 0 {
			return
		}
	}
	_, _ = a.db.ExecContext(ctx, `INSERT INTO domain_states(domain,is_frozen) VALUES(?,?)`, domain, frozenState)
}

func (a *App) servePublishedStaticFile(w http.ResponseWriter, r *http.Request, domain, pagePath string) bool {
	if a.isAdminRequest(r) {
		return false
	}
	return a.servePublishedStaticFileFromDisk(w, r, domain, pagePath, true)
}

func isGuestStaticRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if strings.TrimSpace(r.URL.RawQuery) != "" {
		return false
	}
	return !hasSitebrushSessionCookie(r)
}

func hasSitebrushSessionCookie(r *http.Request) bool {
	if r == nil {
		return false
	}
	_, err := r.Cookie("sitebrush_session")
	return err == nil
}

func (a *App) servePublishedStaticFileFromDisk(w http.ResponseWriter, r *http.Request, domain, pagePath string, injectPublicMenu bool) bool {
	pagePath = cleanPath(pagePath)
	staticFilePath := filepath.Join(a.domainStaticDir(domain), staticRelativePathForPage(pagePath))
	fileInfo, statErr := os.Stat(staticFilePath)
	if statErr != nil || fileInfo.IsDir() {
		return false
	}
	if pageContentKind(pagePath, "") != "html" {
		a.logContentDelivery(w, "static-file")
		http.ServeFile(w, r, staticFilePath)
		return true
	}
	a.logContentDelivery(w, "static-file")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if injectPublicMenu {
		staticContent, readErr := a.guestStaticHTML(staticFilePath, pagePath, domain, preferredLanguageCode(r.Header.Get("Accept-Language")), fileInfo)
		if readErr != nil {
			return false
		}
		_, _ = w.Write(staticContent)
		return true
	}
	staticContent, readErr := os.ReadFile(staticFilePath)
	if readErr != nil {
		return false
	}
	_, _ = w.Write(staticContent)
	return true
}

func (a *App) publishedStaticPageExists(domain, pagePath string) bool {
	staticFilePath := filepath.Join(a.domainStaticDir(domain), staticRelativePathForPage(cleanPath(pagePath)))
	fileInfo, statErr := os.Stat(staticFilePath)
	return statErr == nil && !fileInfo.IsDir()
}

func (a *App) guestStaticHTML(staticFilePath, pagePath, domain, languageCode string, fileInfo os.FileInfo) ([]byte, error) {
	if a.guestStaticHTMLCache == nil {
		staticContent, readErr := os.ReadFile(staticFilePath)
		if readErr != nil {
			return nil, readErr
		}
		html := string(staticContent)
		menuScript := buildGuestContextMenuScriptForLanguage(pagePath, domain, languageCode)
		lowerHTML := strings.ToLower(html)
		bodyCloseIndex := strings.LastIndex(lowerHTML, "</body>")
		if bodyCloseIndex < 0 {
			return []byte(html + menuScript), nil
		}
		return []byte(html[:bodyCloseIndex] + menuScript + html[bodyCloseIndex:]), nil
	}
	response := make(chan guestStaticHTMLCacheResponse, 1)
	request := guestStaticHTMLCacheRequest{
		filePath:     staticFilePath,
		pagePath:     pagePath,
		domain:       domain,
		languageCode: languageCode,
		modTime:      fileInfo.ModTime(),
		size:         fileInfo.Size(),
		response:     response,
	}
	select {
	case a.guestStaticHTMLCache <- request:
	default:
		return buildGuestStaticHTMLBody(staticFilePath, pagePath, domain, languageCode)
	}
	select {
	case result := <-response:
		return result.body, result.err
	case <-time.After(2 * time.Second):
		return buildGuestStaticHTMLBody(staticFilePath, pagePath, domain, languageCode)
	}
}

func startGuestStaticHTMLCacheWorker(ctx context.Context, limitBytes int64) chan guestStaticHTMLCacheRequest {
	requests := make(chan guestStaticHTMLCacheRequest, guestStaticHTMLCacheQueueSize)
	go runGuestStaticHTMLCache(ctx, requests, limitBytes)
	return requests
}

func startAuthIPFailureCacheWorker(ctx context.Context) chan authIPFailureCacheRequest {
	requests := make(chan authIPFailureCacheRequest, authIPFailureCacheQueueSize)
	go runAuthIPFailureCache(ctx, requests)
	return requests
}

func runAuthIPFailureCache(ctx context.Context, requests <-chan authIPFailureCacheRequest) {
	statesByAddress := make(map[string]authIPFailure)
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-requests:
			cacheKey := strings.Join([]string{strings.TrimSpace(request.domain), strings.TrimSpace(request.clientIP)}, "\x00")
			if cacheKey == "\x00" {
				if request.response != nil {
					request.response <- authIPFailureCacheResponse{}
				}
				continue
			}
			switch request.action {
			case "get":
				state, found := statesByAddress[cacheKey]
				request.response <- authIPFailureCacheResponse{state: state, found: found}
			case "max-prefix":
				var state authIPFailure
				found := false
				now := time.Now().UTC()
				domainPrefix := strings.TrimSpace(request.domain)
				globalDomain := strings.TrimSuffix(domainPrefix, "|")
				clientIPAliases := make(map[string]struct{})
				for _, clientIPAlias := range clientIPStorageAliases(request.clientIP) {
					clientIPAliases[clientIPAlias] = struct{}{}
				}
				for storedKey, candidate := range statesByAddress {
					storedDomain, storedClientIP, splitOK := strings.Cut(storedKey, "\x00")
					if !splitOK {
						continue
					}
					if _, ok := clientIPAliases[storedClientIP]; !ok {
						continue
					}
					if storedDomain != globalDomain && !strings.HasPrefix(storedDomain, domainPrefix) {
						continue
					}
					state = strongerAuthIPFailureState(state, candidate, now)
					found = true
				}
				request.response <- authIPFailureCacheResponse{state: state, found: found}
			case "set":
				statesByAddress[cacheKey] = request.state
				if request.response != nil {
					request.response <- authIPFailureCacheResponse{state: request.state, found: true}
				}
			case "delete":
				delete(statesByAddress, cacheKey)
				if request.response != nil {
					request.response <- authIPFailureCacheResponse{}
				}
			}
		}
	}
}

func runGuestStaticHTMLCache(ctx context.Context, requests <-chan guestStaticHTMLCacheRequest, limitBytes int64) {
	if limitBytes <= 0 {
		limitBytes = defaultGuestStaticHTMLCacheLimitBytes
	}
	cacheEntries := make(map[string]guestStaticHTMLCacheEntry)
	var usedBytes int64
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-requests:
			cacheKey := strings.Join([]string{request.filePath, request.pagePath, request.domain, request.languageCode}, "\x00")
			if cachedEntry, found := cacheEntries[cacheKey]; found {
				if cachedEntry.modTime.Equal(request.modTime) && cachedEntry.size == request.size {
					request.response <- guestStaticHTMLCacheResponse{body: cachedEntry.body}
					continue
				}
				usedBytes -= int64(len(cachedEntry.body))
				delete(cacheEntries, cacheKey)
			}
			body, readErr := buildGuestStaticHTMLBody(request.filePath, request.pagePath, request.domain, request.languageCode)
			if readErr != nil {
				request.response <- guestStaticHTMLCacheResponse{err: readErr}
				continue
			}
			bodySize := int64(len(body))
			if bodySize > limitBytes {
				request.response <- guestStaticHTMLCacheResponse{body: body}
				continue
			}
			if usedBytes+bodySize > limitBytes {
				cacheEntries = make(map[string]guestStaticHTMLCacheEntry)
				usedBytes = 0
			}
			cacheEntries[cacheKey] = guestStaticHTMLCacheEntry{modTime: request.modTime, size: request.size, body: body}
			usedBytes += bodySize
			request.response <- guestStaticHTMLCacheResponse{body: body}
		}
	}
}

func buildGuestStaticHTMLBody(staticFilePath, pagePath, domain, languageCode string) ([]byte, error) {
	staticContent, readErr := os.ReadFile(staticFilePath)
	if readErr != nil {
		return nil, readErr
	}
	html := string(staticContent)
	menuScript := buildGuestContextMenuScriptForLanguage(pagePath, domain, languageCode)
	lowerHTML := strings.ToLower(html)
	bodyCloseIndex := strings.LastIndex(lowerHTML, "</body>")
	if bodyCloseIndex < 0 {
		return []byte(html + menuScript), nil
	}
	return []byte(html[:bodyCloseIndex] + menuScript + html[bodyCloseIndex:]), nil
}

func (a *App) injectPublicContextMenu(r *http.Request, pagePath, html string) string {
	menuScript := buildGuestContextMenuScriptForLanguage(pagePath, domainFromContext(r.Context()), preferredLanguageCode(r.Header.Get("Accept-Language")))
	lowerHTML := strings.ToLower(html)
	bodyCloseIndex := strings.LastIndex(lowerHTML, "</body>")
	if bodyCloseIndex < 0 {
		return html + menuScript
	}
	return html[:bodyCloseIndex] + menuScript + html[bodyCloseIndex:]
}

func (a *App) writePublishedStaticHTML(domain, pagePath, html string) {
	staticFilePath := filepath.Join(a.domainStaticDir(domain), staticRelativePathForPage(pagePath))
	_ = os.MkdirAll(filepath.Dir(staticFilePath), 0755)
	_ = os.WriteFile(staticFilePath, []byte(html), 0644)
}

func (a *App) removePublishedStaticFile(domain, pagePath string) {
	staticFilePath := filepath.Join(a.domainStaticDir(domain), staticRelativePathForPage(pagePath))
	_ = os.Remove(staticFilePath)
}

func staticRelativePathForPage(pagePath string) string {
	normalizedPath := strings.TrimPrefix(pagePath, "/")
	if normalizedPath == "" {
		return "index.html"
	}
	if strings.HasSuffix(normalizedPath, "/") {
		return normalizedPath + "index.html"
	}
	if extension := strings.ToLower(path.Ext(normalizedPath)); extension != "" {
		return normalizedPath
	}
	return normalizedPath + ".html"
}

func (a *App) domainStaticDir(domain string) string {
	return filepath.Join(a.staticRootDir(), domainStorageName(domain))
}

func (a *App) domainLogDir(domain string) string {
	return filepath.Join(a.storageRootDir(), "logs", domainStorageName(domain))
}

func (a *App) problemLogDir() string {
	return filepath.Join(a.storageRootDir(), "logs", "problems")
}

func (a *App) logContentDelivery(w http.ResponseWriter, sourceType string) {
	contentSource := "dynamic"
	if sourceType == "static-file" {
		contentSource = "static"
	}
	w.Header().Set("X-Sitebrush-Source", contentSource)
}
func domainStorageName(domain string) string {
	return strings.NewReplacer("/", "_", "\\", "_", ":", "_", "..", "_").Replace(domain)
}

func (a *App) domainFilesDir(r *http.Request) string {
	return a.domainFilesDirForDomain(a.siteDomain(r.Context(), r))
}

func (a *App) domainFilesDirForDomain(domain string) string {
	return filepath.Join(a.filesRootDir(), domainStorageName(domain))
}

func (a *App) staticRootDir() string {
	return filepath.Join(a.storageRootDir(), "static")
}

func (a *App) filesRootDir() string {
	return filepath.Join(a.storageRootDir(), "files")
}

func (a *App) packsDir() string {
	return filepath.Join(a.storageRootDir(), "packs")
}

func (a *App) storageRootDir() string {
	storagePath := a.storagePath
	if strings.TrimSpace(storagePath) == "" {
		storagePath = defaultAppStoragePath()
	}
	return filepath.Join(storagePath, "storage")
}

func defaultAppStoragePath() string {
	if appcli.LinuxServerStorageDefaultEnabled() {
		return "/var/lib/sitebrush"
	}
	basePath := defaultBaseAppDataPath()
	return filepath.Join(basePath, storageAppName)
}

func defaultBaseAppDataPath() string {
	switch runtime.GOOS {
	case "darwin":
		homeDir, err := os.UserHomeDir()
		if err == nil && strings.TrimSpace(homeDir) != "" {
			return filepath.Join(homeDir, "Library", "Application Support")
		}
	case "windows":
		localAppDataDir := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if localAppDataDir != "" {
			return localAppDataDir
		}
		roamingAppDataDir := strings.TrimSpace(os.Getenv("APPDATA"))
		if roamingAppDataDir != "" {
			return roamingAppDataDir
		}
	default:
		homeDir, err := os.UserHomeDir()
		if err == nil && strings.TrimSpace(homeDir) != "" {
			return filepath.Join(homeDir, ".sitebrush")
		}
		xdgDataHomeDir := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
		if xdgDataHomeDir != "" {
			return filepath.Join(xdgDataHomeDir, storageAppName)
		}
	}
	if configDir, err := os.UserConfigDir(); err == nil && strings.TrimSpace(configDir) != "" {
		return configDir
	}
	if homeDir, err := os.UserHomeDir(); err == nil && strings.TrimSpace(homeDir) != "" {
		return homeDir
	}
	return "."
}
