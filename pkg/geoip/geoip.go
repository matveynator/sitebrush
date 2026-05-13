package geoip

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const AttributionHTML = `<a href="https://db-ip.com">IP Geolocation by DB-IP</a>`

const (
	lookupTimeout        = 250 * time.Millisecond
	downloadTimeout      = 30 * time.Minute
	geoIPDatabaseName    = "geoip.db"
	geoIPArchiveTemplate = "dbip-city-lite-%s.csv.gz"
)

type Location struct {
	IP          string
	CountryCode string
	Region      string
	City        string
	Latitude    float64
	Longitude   float64
	Source      string
}

type Resolver struct {
	cacheDir string
	requests chan lookupRequest
}

type lookupRequest struct {
	ip       string
	response chan lookupResponse
}

type lookupResponse struct {
	location Location
	found    bool
}

type importResult struct {
	release string
	err     error
}

func NewResolver(cacheDir string) *Resolver {
	resolver := &Resolver{
		cacheDir: strings.TrimSpace(cacheDir),
		requests: make(chan lookupRequest),
	}
	go resolver.run()
	return resolver
}

func (resolver *Resolver) Lookup(ctx context.Context, ip string) (Location, bool) {
	if resolver == nil {
		return Location{}, false
	}
	requestContext := ctx
	if requestContext == nil {
		requestContext = context.Background()
	}
	requestContext, cancel := context.WithTimeout(requestContext, lookupTimeout)
	defer cancel()

	response := make(chan lookupResponse, 1)
	request := lookupRequest{ip: ip, response: response}
	select {
	case resolver.requests <- request:
	case <-requestContext.Done():
		return Location{}, false
	}
	select {
	case result := <-response:
		return result.location, result.found
	case <-requestContext.Done():
		return Location{}, false
	}
}

func (resolver *Resolver) run() {
	if resolver.cacheDir == "" {
		resolver.cacheDir = filepath.Join(os.TempDir(), "sitebrush-geoip")
	}
	_ = os.MkdirAll(resolver.cacheDir, 0o755)
	databasePath := filepath.Join(resolver.cacheDir, geoIPDatabaseName)
	database, err := sql.Open("sqlite", "file:"+databasePath)
	if err != nil {
		resolver.drainWithoutDatabase()
		return
	}
	defer database.Close()
	if err := migrate(database); err != nil {
		resolver.drainWithoutDatabase()
		return
	}

	importResults := make(chan importResult, 1)
	importInProgress := false
	importedRelease := metadataValue(database, "imported_release")
	currentRelease := releaseMonth(time.Now().UTC())
	hasRanges := rangeCount(database) > 0

	for {
		select {
		case request := <-resolver.requests:
			location, found := lookup(database, request.ip)
			if !found && isPublicIPv4(request.ip) && (!hasRanges || importedRelease != currentRelease) && !importInProgress {
				importInProgress = true
				go importLatest(databasePath, resolver.cacheDir, time.Now().UTC(), importResults)
			}
			request.response <- lookupResponse{location: location, found: found}
		case result := <-importResults:
			importInProgress = false
			if result.err == nil && result.release != "" {
				importedRelease = result.release
				hasRanges = true
			}
		}
	}
}

func (resolver *Resolver) drainWithoutDatabase() {
	for request := range resolver.requests {
		request.response <- lookupResponse{}
	}
}

func migrate(database *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS geoip_metadata(key TEXT PRIMARY KEY,value TEXT);`,
		`CREATE TABLE IF NOT EXISTS geoip_ranges(ip_start INTEGER NOT NULL,ip_end INTEGER NOT NULL,country_code TEXT,region TEXT,city TEXT,latitude REAL,longitude REAL);`,
		`CREATE INDEX IF NOT EXISTS idx_geoip_ranges_start ON geoip_ranges(ip_start);`,
		`CREATE TABLE IF NOT EXISTS geoip_ip_cache(ip TEXT PRIMARY KEY,ip_number INTEGER,country_code TEXT,region TEXT,city TEXT,latitude REAL,longitude REAL,source TEXT,updated_at TEXT);`,
	}
	for _, statement := range statements {
		if _, err := database.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func lookup(database *sql.DB, rawIP string) (Location, bool) {
	ipNumber, ok := ipv4Number(rawIP)
	if !ok || !isPublicIPv4(rawIP) {
		return Location{}, false
	}
	if location, found := lookupCache(database, rawIP); found {
		return location, true
	}
	location, found := lookupRange(database, rawIP, ipNumber)
	if !found {
		return Location{}, false
	}
	_, _ = database.Exec(`INSERT OR REPLACE INTO geoip_ip_cache(ip,ip_number,country_code,region,city,latitude,longitude,source,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		location.IP, ipNumber, location.CountryCode, location.Region, location.City, location.Latitude, location.Longitude, location.Source, time.Now().UTC().Format(time.RFC3339))
	return location, true
}

func lookupCache(database *sql.DB, rawIP string) (Location, bool) {
	var location Location
	err := database.QueryRow(`SELECT ip,country_code,region,city,latitude,longitude,source FROM geoip_ip_cache WHERE ip=?`, strings.TrimSpace(rawIP)).Scan(
		&location.IP, &location.CountryCode, &location.Region, &location.City, &location.Latitude, &location.Longitude, &location.Source)
	if err != nil {
		return Location{}, false
	}
	return location, true
}

func lookupRange(database *sql.DB, rawIP string, ipNumber uint32) (Location, bool) {
	var location Location
	err := database.QueryRow(`SELECT country_code,region,city,latitude,longitude FROM geoip_ranges WHERE ip_start<=? AND ip_end>=? ORDER BY ip_start DESC LIMIT 1`, int64(ipNumber), int64(ipNumber)).Scan(
		&location.CountryCode, &location.Region, &location.City, &location.Latitude, &location.Longitude)
	if err != nil {
		return Location{}, false
	}
	location.IP = strings.TrimSpace(rawIP)
	location.Source = "local geoip database"
	return location, true
}

func importLatest(databasePath, cacheDir string, now time.Time, results chan<- importResult) {
	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer cancel()
	release := releaseMonth(now)
	archivePath, release, err := ensureArchive(ctx, cacheDir, now)
	if err == nil {
		err = importArchive(ctx, databasePath, archivePath, release)
	}
	select {
	case results <- importResult{release: release, err: err}:
	default:
	}
}

func ensureArchive(ctx context.Context, cacheDir string, now time.Time) (string, string, error) {
	for _, release := range candidateReleaseMonths(now) {
		archivePath := filepath.Join(cacheDir, fmt.Sprintf(geoIPArchiveTemplate, release))
		if fileHasContent(archivePath) {
			return archivePath, release, nil
		}
		if err := downloadArchive(ctx, release, archivePath); err == nil {
			return archivePath, release, nil
		}
	}
	return "", "", errors.New("geoip archive download failed")
}

func downloadArchive(ctx context.Context, release, archivePath string) error {
	requestURL := fmt.Sprintf("https://download.db-ip.com/free/dbip-city-lite-%s.csv.gz", release)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("geoip download status %d", response.StatusCode)
	}
	temporaryPath := archivePath + ".part"
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		return err
	}
	output, err := os.Create(temporaryPath)
	if err != nil {
		return err
	}
	if _, err = io.Copy(output, response.Body); err != nil {
		_ = output.Close()
		_ = os.Remove(temporaryPath)
		return err
	}
	if err = output.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	return os.Rename(temporaryPath, archivePath)
}

func importArchive(ctx context.Context, databasePath, archivePath, release string) error {
	database, err := sql.Open("sqlite", "file:"+databasePath)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := migrate(database); err != nil {
		return err
	}
	if _, err := database.ExecContext(ctx, `DROP TABLE IF EXISTS geoip_ranges_next`); err != nil {
		return err
	}
	if _, err := database.ExecContext(ctx, `CREATE TABLE geoip_ranges_next(ip_start INTEGER NOT NULL,ip_end INTEGER NOT NULL,country_code TEXT,region TEXT,city TEXT,latitude REAL,longitude REAL)`); err != nil {
		return err
	}
	if err := importArchiveRows(ctx, database, archivePath); err != nil {
		return err
	}
	statements := []string{
		`DROP TABLE IF EXISTS geoip_ranges;`,
		`ALTER TABLE geoip_ranges_next RENAME TO geoip_ranges;`,
		`CREATE INDEX IF NOT EXISTS idx_geoip_ranges_start ON geoip_ranges(ip_start);`,
		`DELETE FROM geoip_ip_cache;`,
		`INSERT OR REPLACE INTO geoip_metadata(key,value) VALUES('imported_release',?);`,
	}
	for _, statement := range statements[:4] {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	_, err = database.ExecContext(ctx, statements[4], release)
	return err
}

func importArchiveRows(ctx context.Context, database *sql.DB, archivePath string) error {
	input, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer input.Close()
	gzipReader, err := gzip.NewReader(input)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	csvReader := csv.NewReader(gzipReader)
	csvReader.FieldsPerRecord = -1
	csvReader.ReuseRecord = true

	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	statement, err := transaction.PrepareContext(ctx, `INSERT INTO geoip_ranges_next(ip_start,ip_end,country_code,region,city,latitude,longitude) VALUES(?,?,?,?,?,?,?)`)
	if err != nil {
		_ = transaction.Rollback()
		return err
	}
	defer statement.Close()
	rowCount := 0
	for {
		record, readErr := csvReader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = transaction.Rollback()
			return readErr
		}
		row, ok := parseDBIPCSVRecord(record)
		if !ok {
			continue
		}
		if _, err := statement.ExecContext(ctx, row.ipStart, row.ipEnd, row.countryCode, row.region, row.city, row.latitude, row.longitude); err != nil {
			_ = transaction.Rollback()
			return err
		}
		rowCount++
		if rowCount%50000 == 0 {
			if err := ctx.Err(); err != nil {
				_ = transaction.Rollback()
				return err
			}
		}
	}
	return transaction.Commit()
}

type dbipCSVRow struct {
	ipStart     int64
	ipEnd       int64
	countryCode string
	region      string
	city        string
	latitude    float64
	longitude   float64
}

func parseDBIPCSVRecord(record []string) (dbipCSVRow, bool) {
	if len(record) < 8 {
		return dbipCSVRow{}, false
	}
	start, ok := ipv4Number(record[0])
	if !ok {
		return dbipCSVRow{}, false
	}
	end, ok := ipv4Number(record[1])
	if !ok {
		return dbipCSVRow{}, false
	}
	latitude, latErr := strconv.ParseFloat(strings.TrimSpace(record[6]), 64)
	longitude, lonErr := strconv.ParseFloat(strings.TrimSpace(record[7]), 64)
	if latErr != nil || lonErr != nil {
		return dbipCSVRow{}, false
	}
	countryCode := strings.ToUpper(strings.TrimSpace(record[3]))
	if len(countryCode) != 2 {
		return dbipCSVRow{}, false
	}
	return dbipCSVRow{
		ipStart:     int64(start),
		ipEnd:       int64(end),
		countryCode: countryCode,
		region:      strings.TrimSpace(record[4]),
		city:        strings.TrimSpace(record[5]),
		latitude:    latitude,
		longitude:   longitude,
	}, true
}

func metadataValue(database *sql.DB, key string) string {
	var value string
	if err := database.QueryRow(`SELECT value FROM geoip_metadata WHERE key=?`, key).Scan(&value); err != nil {
		return ""
	}
	return value
}

func rangeCount(database *sql.DB) int {
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM geoip_ranges`).Scan(&count); err != nil {
		return 0
	}
	return count
}

func candidateReleaseMonths(now time.Time) []string {
	current := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return []string{releaseMonth(current), releaseMonth(current.AddDate(0, -1, 0))}
}

func releaseMonth(now time.Time) string {
	return now.UTC().Format("2006-01")
}

func fileHasContent(filePath string) bool {
	info, err := os.Stat(filePath)
	return err == nil && info.Size() > 0
}

func ipv4Number(rawIP string) (uint32, bool) {
	parsedIP := net.ParseIP(strings.TrimSpace(rawIP))
	if parsedIP == nil {
		return 0, false
	}
	ipv4 := parsedIP.To4()
	if ipv4 == nil {
		return 0, false
	}
	return uint32(ipv4[0])<<24 | uint32(ipv4[1])<<16 | uint32(ipv4[2])<<8 | uint32(ipv4[3]), true
}

func isPublicIPv4(rawIP string) bool {
	parsedIP := net.ParseIP(strings.TrimSpace(rawIP))
	if parsedIP == nil || parsedIP.To4() == nil {
		return false
	}
	return !parsedIP.IsLoopback() && !parsedIP.IsPrivate() && !parsedIP.IsLinkLocalUnicast() && !parsedIP.IsMulticast() && !parsedIP.IsUnspecified()
}
