package geoip

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSecurityBoundaryGeoIPRejectsNonPublicAndMalformedAddresses(t *testing.T) {
	for _, rawIP := range []string{
		"",
		"not-an-ip",
		"0.0.0.0",
		"127.0.0.1",
		"10.0.0.1",
		"172.16.0.1",
		"192.168.0.1",
		"169.254.1.1",
		"224.0.0.1",
		"255.255.255.255",
		"::1",
		"fe80::1",
	} {
		if _, ok := ipv4Number(rawIP); ok && rawIP == "not-an-ip" {
			t.Fatalf("malformed address %q converted to IPv4", rawIP)
		}
		if isPublicIPv4(rawIP) {
			t.Fatalf("SECURITY: non-public address %q was accepted for GeoIP lookup", rawIP)
		}
	}
}

func TestSecurityBoundaryGeoIPLookupNeverCachesPrivateAddress(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "geoip-security.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := migrate(database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		"INSERT INTO geoip_ranges(ip_start,ip_end,country_code,region,city,latitude,longitude) VALUES(?,?,?,?,?,?,?)",
		int64(0), int64(^uint32(0)), "ZZ", "attacker", "fake", 1, 2,
	); err != nil {
		t.Fatal(err)
	}

	for _, rawIP := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1"} {
		if location, found := lookup(database, rawIP); found || location != (Location{}) {
			t.Fatalf("SECURITY: private address %q resolved through attacker-controlled range: %#v", rawIP, location)
		}
	}
	var cached int
	if err := database.QueryRow("SELECT COUNT(*) FROM geoip_ip_cache").Scan(&cached); err != nil {
		t.Fatal(err)
	}
	if cached != 0 {
		t.Fatalf("SECURITY: private GeoIP requests were cached: %d", cached)
	}
}

func TestSecurityBoundaryDBIPCSVRejectsInvalidNetworkAndCoordinates(t *testing.T) {
	records := [][]string{
		{"8.8.8.255", "8.8.8.0", "NA", "US", "CA", "City", "1", "2"},
		{"8.8.8.0", "8.8.8.255", "NA", "", "CA", "City", "1", "2"},
		{"8.8.8.0", "8.8.8.255", "NA", "U", "CA", "City", "1", "2"},
		{"8.8.8.0", "8.8.8.255", "NA", "US", "CA", "City", "NaN", "2"},
		{"8.8.8.0", "8.8.8.255", "NA", "US", "CA", "City", "91", "2"},
		{"8.8.8.0", "8.8.8.255", "NA", "US", "CA", "City", "1", "181"},
	}
	for _, record := range records {
		if row, ok := parseDBIPCSVRecord(record); ok {
			t.Fatalf("SECURITY: invalid GeoIP CSV row was accepted: %#v", row)
		}
	}
}

func TestGeoIPResolverHonorsCancelledLookup(t *testing.T) {
	resolver := NewResolver(filepath.Join(t.TempDir(), "resolver"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	if _, found := resolver.Lookup(ctx, "8.8.8.8"); found {
		resolver.Close()
		t.Fatal("cancelled GeoIP lookup returned a result")
	}
	resolver.Close()
	if time.Since(started) > time.Second {
		t.Fatal("cancelled GeoIP lookup or shutdown did not stop promptly")
	}
}

func TestGeoIPResolverCloseBroadcastsToImportWorker(t *testing.T) {
	importStarted := make(chan struct{}, 1)
	withGeoIPHTTPClient(t, geoIPRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		importStarted <- struct{}{}
		<-request.Context().Done()
		return nil, request.Context().Err()
	}))
	resolver := NewResolver(filepath.Join(t.TempDir(), "resolver"))
	select {
	case <-importStarted:
	case <-time.After(time.Second):
		resolver.Close()
		t.Fatal("resolver did not start the expected GeoIP import")
	}
	closed := make(chan struct{})
	go func() {
		resolver.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("resolver shutdown signal was consumed by the import worker")
	}
}

func TestGeoIPResolverOwnerLoopAndChannelShutdown(t *testing.T) {
	cacheDir := t.TempDir()
	databasePath := filepath.Join(cacheDir, geoIPDatabaseName)
	database, err := sql.Open("sqlite", "file:"+databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(database); err != nil {
		t.Fatal(err)
	}
	start, _ := ipv4Number("8.8.8.0")
	end, _ := ipv4Number("8.8.8.255")
	if _, err := database.Exec(
		"INSERT INTO geoip_ranges(ip_start,ip_end,country_code,region,city,latitude,longitude) VALUES(?,?,?,?,?,?,?)",
		int64(start), int64(end), "US", "California", "Mountain View", 37.4, -122.1,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("INSERT INTO geoip_metadata(key,value) VALUES(?,?)", "imported_release", releaseMonth(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	resolver := NewResolver(cacheDir)
	location, found := resolver.Lookup(context.Background(), "8.8.8.8")
	if !found || location.CountryCode != "US" {
		resolver.Close()
		t.Fatalf("resolver lookup = %#v found=%v", location, found)
	}
	resolver.Close()
	resolver.Close()
	if _, found := resolver.Lookup(context.Background(), "8.8.8.8"); found {
		t.Fatal("closed resolver still returned data")
	}
}

func TestGeoIPResolverDrainWithoutDatabaseHonorsShutdown(t *testing.T) {
	parent := t.TempDir()
	cachePath := filepath.Join(parent, "not-a-directory")
	if err := os.WriteFile(cachePath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(cachePath)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, found := resolver.Lookup(ctx, "8.8.8.8"); found {
		resolver.Close()
		t.Fatal("resolver with unavailable database returned data")
	}
	resolver.Close()
}

func TestGeoIPArchiveImportLifecycle(t *testing.T) {
	cacheDir := t.TempDir()
	archivePath := filepath.Join(cacheDir, "geo.csv.gz")
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	_, _ = writer.Write([]byte(
		"8.8.8.0,8.8.8.255,NA,US,California,Mountain View,37.4,-122.1\n" +
			"8.8.4.255,8.8.4.0,NA,US,California,Invalid,37.4,-122.1\n" +
			"1.1.1.0,1.1.1.255,OC,AU,Queensland,South Brisbane,-27.47,153.02\n",
	))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivePath, buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	databasePath := filepath.Join(cacheDir, "import.db")
	if err := importArchive(context.Background(), databasePath, archivePath, "2026-09"); err != nil {
		t.Fatalf("import archive: %v", err)
	}
	database, err := sql.Open("sqlite", "file:"+databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if got := rangeCount(database); got != 2 {
		t.Fatalf("imported range count = %d, want 2", got)
	}
	if got := metadataValue(database, "imported_release"); got != "2026-09" {
		t.Fatalf("imported release = %q", got)
	}
	if location, found := lookup(database, "1.1.1.1"); !found || location.CountryCode != "AU" {
		t.Fatalf("imported lookup = %#v found=%v", location, found)
	}
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("archive unexpectedly removed: %v", err)
	}
}

func TestGeoIPArchiveImportRejectsCorruptAndMissingInputs(t *testing.T) {
	cacheDir := t.TempDir()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(cacheDir, "rows.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("CREATE TABLE geoip_ranges_next(ip_start INTEGER,ip_end INTEGER,country_code TEXT,region TEXT,city TEXT,latitude REAL,longitude REAL)"); err != nil {
		t.Fatal(err)
	}

	if err := importArchiveRows(context.Background(), database, filepath.Join(cacheDir, "missing.gz")); err == nil {
		t.Fatal("missing GeoIP archive was accepted")
	}
	corrupt := filepath.Join(cacheDir, "corrupt.gz")
	if err := os.WriteFile(corrupt, []byte("not gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := importArchiveRows(context.Background(), database, corrupt); err == nil {
		t.Fatal("corrupt GeoIP archive was accepted")
	}
}
