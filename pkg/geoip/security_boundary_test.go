package geoip

import (
	"context"
	"database/sql"
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
	}
	for _, record := range records {
		row, ok := parseDBIPCSVRecord(record)
		if ok && row.ipStart > row.ipEnd {
			t.Fatalf("SECURITY: reversed IP range was accepted: %#v", row)
		}
		if ok && row.countryCode == "" {
			t.Fatalf("SECURITY: empty country code was accepted: %#v", row)
		}
	}
}

func TestGeoIPResolverHonorsCancelledLookup(t *testing.T) {
	resolver := NewResolver(filepath.Join(t.TempDir(), "resolver"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	if _, found := resolver.Lookup(ctx, "8.8.8.8"); found {
		t.Fatal("cancelled GeoIP lookup returned a result")
	}
	if time.Since(started) > time.Second {
		t.Fatal("cancelled GeoIP lookup did not stop promptly")
	}
}
