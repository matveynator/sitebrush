package geoip

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestParseDBIPCSVRecord(t *testing.T) {
	row, ok := parseDBIPCSVRecord([]string{"8.8.8.0", "8.8.8.255", "NA", "US", "California", "Mountain View", "37.4229", "-122.085"})
	if !ok {
		t.Fatalf("parseDBIPCSVRecord returned false")
	}
	if row.ipStart != 134744064 || row.ipEnd != 134744319 {
		t.Fatalf("range = %d-%d, want 134744064-134744319", row.ipStart, row.ipEnd)
	}
	if row.countryCode != "US" || row.region != "California" || row.city != "Mountain View" {
		t.Fatalf("location = %s/%s/%s", row.countryCode, row.region, row.city)
	}
	if row.latitude != 37.4229 || row.longitude != -122.085 {
		t.Fatalf("coordinates = %f,%f", row.latitude, row.longitude)
	}
}

func TestParseDBIPCSVRecordRejectsInvalidRows(t *testing.T) {
	for _, record := range [][]string{
		{"short"},
		{"bad", "8.8.8.255", "NA", "US", "CA", "City", "1", "2"},
		{"8.8.8.0", "bad", "NA", "US", "CA", "City", "1", "2"},
		{"8.8.8.0", "8.8.8.255", "NA", "US", "CA", "City", "bad", "2"},
		{"8.8.8.0", "8.8.8.255", "NA", "US", "CA", "City", "1", "bad"},
		{"8.8.8.0", "8.8.8.255", "NA", "USA", "CA", "City", "1", "2"},
	} {
		if _, ok := parseDBIPCSVRecord(record); ok {
			t.Errorf("invalid CSV row accepted: %#v", record)
		}
	}
}

func TestGeoIPImportRejectsUnavailableAndMalformedArchives(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), geoIPDatabaseName)
	if err := importArchiveRows(context.Background(), openGeoIPTestDatabase(t), filepath.Join(t.TempDir(), "missing.gz")); err == nil {
		t.Fatal("missing archive accepted")
	}
	archivePath := filepath.Join(t.TempDir(), "malformed.gz")
	if err := os.WriteFile(archivePath, []byte("not gzip"), 0600); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", "file:"+databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := migrate(database); err != nil {
		t.Fatal(err)
	}
	if err := importArchiveRows(context.Background(), database, archivePath); err == nil {
		t.Fatal("malformed gzip archive accepted")
	}
}

func TestPrivateAndIPv6AddressesDoNotTriggerLocalLookup(t *testing.T) {
	for _, rawIP := range []string{"127.0.0.1", "10.0.0.5", "192.168.1.20", "::1", "2001:4860:4860::8888"} {
		if isPublicIPv4(rawIP) {
			t.Fatalf("%s treated as public IPv4", rawIP)
		}
	}
	if !isPublicIPv4("8.8.8.8") {
		t.Fatalf("8.8.8.8 was not treated as public IPv4")
	}
}

func TestResolverHandlesNilAndUnavailableDatabase(t *testing.T) {
	if location, found := (*Resolver)(nil).Lookup(nil, "8.8.8.8"); found || location != (Location{}) {
		t.Fatalf("nil resolver returned %#v, %t", location, found)
	}
	cachePath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(cachePath, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(cachePath)
	location, found := resolver.Lookup(context.Background(), "8.8.8.8")
	if found || location != (Location{}) {
		t.Fatalf("unavailable database returned %#v, %t", location, found)
	}
}

func TestGeoIPImportRequiredOnStartup(t *testing.T) {
	if !geoIPImportRequired(false, "", "2026-06") {
		t.Fatal("empty database should require import")
	}
	if !geoIPImportRequired(true, "2026-05", "2026-06") {
		t.Fatal("stale database should require import")
	}
	if geoIPImportRequired(true, "2026-06", "2026-06") {
		t.Fatal("current database should not require import")
	}
}

func TestGeoIPDatabaseLookupAndCache(t *testing.T) {
	database := openGeoIPTestDatabase(t)
	if err := migrate(database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO geoip_ranges(ip_start,ip_end,country_code,region,city,latitude,longitude) VALUES(?,?,?,?,?,?,?)`, 134744064, 134744319, "US", "California", "Mountain View", 37.4, -122.1); err != nil {
		t.Fatal(err)
	}

	location, found := lookup(database, " 8.8.8.8 ")
	if !found || location.City != "Mountain View" || location.Source != "local geoip database" {
		t.Fatalf("range lookup = %#v, %t", location, found)
	}
	if cached, found := lookup(database, "8.8.8.8"); !found || cached != location {
		t.Fatalf("cached lookup = %#v, %t; want %#v", cached, found, location)
	}
	if _, found := lookup(database, "192.168.1.1"); found {
		t.Fatal("private address unexpectedly resolved")
	}
	if metadataValue(database, "missing") != "" || rangeCount(database) != 1 {
		t.Fatal("unexpected metadata or range count")
	}
}

func TestGeoIPArchiveImportReplacesRangesAndMetadata(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, geoIPDatabaseName)
	archivePath := filepath.Join(directory, "archive.csv.gz")
	archive, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(archive)
	if _, err := compressed.Write([]byte("invalid,row\n8.8.8.0,8.8.8.255,NA,US,California,Mountain View,37.4229,-122.085\n")); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := importArchive(context.Background(), databasePath, archivePath, "2026-09"); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", "file:"+databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if got := rangeCount(database); got != 1 {
		t.Fatalf("imported ranges = %d, want 1", got)
	}
	if got := metadataValue(database, "imported_release"); got != "2026-09" {
		t.Fatalf("imported release = %q", got)
	}
	if _, found := lookup(database, "8.8.8.8"); !found {
		t.Fatal("imported address did not resolve")
	}
}

func TestImportLatestUsesCachedArchiveAndReportsResult(t *testing.T) {
	directory := t.TempDir()
	release := "2026-09"
	archivePath := filepath.Join(directory, fmt.Sprintf(geoIPArchiveTemplate, release))
	archive, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(archive)
	if _, err := compressed.Write([]byte("8.8.8.0,8.8.8.255,NA,us, California , Mountain View ,37.4,-122.1\n")); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	results := make(chan importResult, 1)
	stop := make(chan struct{})
	importLatest(filepath.Join(directory, geoIPDatabaseName), directory, time.Date(2026, time.September, 24, 0, 0, 0, 0, time.UTC), stop, results)
	result := <-results
	if result.err != nil || result.release != release {
		t.Fatalf("import result = %#v", result)
	}
	database, err := sql.Open("sqlite", "file:"+filepath.Join(directory, geoIPDatabaseName))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if metadataValue(database, "imported_release") != release || rangeCount(database) != 1 {
		t.Fatal("cached archive was not imported")
	}
}

func TestImportArchiveRejectsCanceledContext(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, geoIPDatabaseName)
	archivePath := filepath.Join(directory, "archive.csv.gz")
	archive, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(archive)
	if _, err := compressed.Write([]byte("8.8.8.0,8.8.8.255,NA,US,CA,City,1,2\n")); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := importArchive(ctx, databasePath, archivePath, "2026-09"); err == nil {
		t.Fatal("archive import succeeded with canceled context")
	}
}

func TestGeoIPReleaseAndAddressHelpers(t *testing.T) {
	date := time.Date(2026, time.January, 3, 2, 0, 0, 0, time.FixedZone("offset", 3*60*60))
	if got := candidateReleaseMonths(date); len(got) != 2 || got[0] != "2026-01" || got[1] != "2025-12" {
		t.Fatalf("candidate releases = %#v", got)
	}
	if got := releaseMonth(date); got != "2026-01" {
		t.Fatalf("release month = %q", got)
	}
	if number, ok := ipv4Number(" 8.8.8.8 "); !ok || number != 0x08080808 {
		t.Fatalf("IPv4 number = %08x, %t", number, ok)
	}
	for _, rawIP := range []string{"not-an-ip", "::1"} {
		if _, ok := ipv4Number(rawIP); ok {
			t.Fatalf("invalid IPv4 %q was accepted", rawIP)
		}
	}
	archivePath := filepath.Join(t.TempDir(), "nonempty.gz")
	if err := os.WriteFile(archivePath, []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !fileHasContent(archivePath) || fileHasContent(archivePath+".missing") {
		t.Fatal("file content check returned an unexpected result")
	}
}

type geoIPRoundTrip func(*http.Request) (*http.Response, error)

func (roundTrip geoIPRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestGeoIPArchiveDownloadAndReleaseFallback(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	cacheDir := t.TempDir()
	archiveBody := []byte("compressed archive")
	http.DefaultClient = &http.Client{Transport: geoIPRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Host != "download.db-ip.com" {
			return nil, fmt.Errorf("unexpected geoip request: %s %s", request.Method, request.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(archiveBody)), Request: request}, nil
	})}
	archivePath := filepath.Join(cacheDir, fmt.Sprintf(geoIPArchiveTemplate, "2026-09"))
	if err := downloadArchive(context.Background(), "2026-09", archivePath); err != nil {
		t.Fatal(err)
	}
	if !fileHasContent(archivePath) {
		t.Fatal("downloaded archive was not persisted")
	}
	path, release, err := ensureArchive(context.Background(), cacheDir, time.Date(2026, time.September, 24, 0, 0, 0, 0, time.UTC))
	if err != nil || path != archivePath || release != "2026-09" {
		t.Fatalf("cached archive = %q %q %v", path, release, err)
	}
	archiveStatus := http.StatusServiceUnavailable
	http.DefaultClient = &http.Client{Transport: geoIPRoundTrip(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "2026-09") {
			return &http.Response{StatusCode: archiveStatus, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("unavailable")), Request: request}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("previous archive")), Request: request}, nil
	})}
	if err := downloadArchive(context.Background(), "2026-09", filepath.Join(t.TempDir(), "failed.gz")); err == nil {
		t.Fatal("failed archive response was accepted")
	}
	previousPath, previousRelease, err := ensureArchive(context.Background(), t.TempDir(), time.Date(2026, time.September, 24, 0, 0, 0, 0, time.UTC))
	if err != nil || previousRelease != "2026-08" || !strings.HasSuffix(previousPath, "2026-08.csv.gz") {
		t.Fatalf("fallback archive = %q %q %v", previousPath, previousRelease, err)
	}
}

func openGeoIPTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "geoip.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}
