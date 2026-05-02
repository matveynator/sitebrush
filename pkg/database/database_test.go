package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func newTestDatabase(t *testing.T) *Database {
	t.Helper()

	raw, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "sitebrush-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := raw.Close(); err != nil {
			t.Fatalf("close test database: %v", err)
		}
	})

	db := &Database{
		DB:          raw,
		Driver:      "sqlite",
		idGenerator: startIDGenerator(1),
		pipeline:    startSerializedPipeline(raw),
	}
	if err := db.InitSchema(Config{DBType: "sqlite"}, func(string, ...any) {}); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	return db
}

func TestShortLinksPreviewPersistAndResolve(t *testing.T) {
	db := newTestDatabase(t)
	ctx := context.Background()
	target := " https://example.com/page?x=1 "

	code, stored, err := db.PreviewShortLink(ctx, target, 6)
	if err != nil {
		t.Fatalf("preview short link: %v", err)
	}
	if stored {
		t.Fatalf("new target should not be stored")
	}
	if len(code) != 6 || !isBase62(code) {
		t.Fatalf("preview generated invalid code %q", code)
	}

	persisted, err := db.PersistShortLink(ctx, target, "AbC123", time.Unix(1700000000, 0), 6)
	if err != nil {
		t.Fatalf("persist short link: %v", err)
	}
	if persisted != "AbC123" {
		t.Fatalf("persisted code = %q, want AbC123", persisted)
	}

	again, err := db.PersistShortLink(ctx, "https://example.com/page?x=1", "ZZZZZZ", time.Unix(1700000001, 0), 6)
	if err != nil {
		t.Fatalf("persist existing short link: %v", err)
	}
	if again != "AbC123" {
		t.Fatalf("existing target returned %q, want AbC123", again)
	}

	resolved, err := db.ResolveShortLink(ctx, " AbC123 ")
	if err != nil {
		t.Fatalf("resolve short link: %v", err)
	}
	if resolved != "https://example.com/page?x=1" {
		t.Fatalf("resolved target = %q", resolved)
	}

	code, stored, err = db.PreviewShortLink(ctx, "https://example.com/page?x=1", 6)
	if err != nil {
		t.Fatalf("preview existing short link: %v", err)
	}
	if !stored || code != "AbC123" {
		t.Fatalf("preview existing = (%q, %v), want (AbC123, true)", code, stored)
	}
}

func TestShortLinksRejectInvalidInput(t *testing.T) {
	db := newTestDatabase(t)
	ctx := context.Background()

	if _, _, err := db.PreviewShortLink(ctx, "   ", 8); err == nil {
		t.Fatalf("preview accepted empty target")
	}
	if _, err := db.PersistShortLink(ctx, "https://example.com", "bad-code", time.Now(), 8); err == nil {
		t.Fatalf("persist accepted non-base62 code")
	}
	if target, err := db.ResolveShortLink(ctx, "missing"); err != nil || target != "" {
		t.Fatalf("missing resolve = (%q, %v), want empty nil", target, err)
	}
}

func TestTrackHelpersUseAllMarkersAndSkipRealtimeIDs(t *testing.T) {
	db := newTestDatabase(t)
	ctx := context.Background()

	insertTestMarker(t, db.DB, Marker{ID: 1, DoseRate: 0.11, Date: 100, Lon: 37.61, Lat: 55.75, CountRate: 10, Zoom: 4, Speed: 1, TrackID: "track-a"})
	insertTestMarker(t, db.DB, Marker{ID: 2, DoseRate: 0.12, Date: 200, Lon: 37.62, Lat: 55.76, CountRate: 11, Zoom: 9, Speed: 2, TrackID: "track-a"})
	insertTestMarker(t, db.DB, Marker{ID: 3, DoseRate: 0.13, Date: 300, Lon: 37.70, Lat: 55.80, CountRate: 12, Zoom: 4, Speed: 3, TrackID: "track-b"})
	insertTestMarker(t, db.DB, Marker{ID: 4, DoseRate: 0.14, Date: 400, Lon: 37.80, Lat: 55.90, CountRate: 13, Zoom: 4, Speed: 4, TrackID: "live:device"})

	count, err := db.CountTracks(ctx)
	if err != nil {
		t.Fatalf("count tracks: %v", err)
	}
	if count != 2 {
		t.Fatalf("track count = %d, want 2", count)
	}

	summary, err := db.GetTrackSummary(ctx, "track-a", "sqlite")
	if err != nil {
		t.Fatalf("track summary: %v", err)
	}
	if summary.FirstID != 1 || summary.LastID != 2 || summary.MarkerCount != 2 {
		t.Fatalf("summary = %+v, want first=1 last=2 count=2", summary)
	}

	out, errs := db.StreamTrackSummaries(ctx, "", 10, "sqlite")
	var trackIDs []string
	for summary := range out {
		trackIDs = append(trackIDs, summary.TrackID)
	}
	if err := <-errs; err != nil {
		t.Fatalf("stream track summaries: %v", err)
	}
	if len(trackIDs) != 2 || trackIDs[0] != "track-a" || trackIDs[1] != "track-b" {
		t.Fatalf("streamed track IDs = %#v, want track-a, track-b", trackIDs)
	}
}

func TestStreamLatestMarkersNearFiltersByDistanceAndLimit(t *testing.T) {
	db := newTestDatabase(t)
	ctx := context.Background()

	insertTestMarker(t, db.DB, Marker{ID: 1, DoseRate: 0.10, Date: 100, Lon: 37.6173, Lat: 55.7558, CountRate: 10, Zoom: 8, Speed: 0, TrackID: "near-old"})
	insertTestMarker(t, db.DB, Marker{ID: 2, DoseRate: 0.20, Date: 200, Lon: 37.6174, Lat: 55.7559, CountRate: 20, Zoom: 8, Speed: 0, TrackID: "near-new"})
	insertTestMarker(t, db.DB, Marker{ID: 3, DoseRate: 0.30, Date: 300, Lon: 38.0, Lat: 56.0, CountRate: 30, Zoom: 8, Speed: 0, TrackID: "far"})

	out, errs := db.StreamLatestMarkersNear(ctx, 55.7558, 37.6173, 1000, 1, "sqlite")
	var markers []Marker
	for marker := range out {
		markers = append(markers, marker)
	}
	if err := <-errs; err != nil {
		t.Fatalf("stream latest markers: %v", err)
	}
	if len(markers) != 1 {
		t.Fatalf("got %d markers, want 1: %#v", len(markers), markers)
	}
	if markers[0].TrackID != "near-new" {
		t.Fatalf("latest marker track = %q, want near-new", markers[0].TrackID)
	}
}

func TestDatabasePureHelpers(t *testing.T) {
	normalized := map[string]string{
		" PostgreSQL ":  "pgx",
		"postgres+psql": "pgx",
		"sqlite":        "sqlite",
		"CLICKHOUSE":    "clickhouse",
	}
	for input, expected := range normalized {
		if got := normalizeDBType(input); got != expected {
			t.Fatalf("normalizeDBType(%q) = %q, want %q", input, got, expected)
		}
	}

	dsn := ClickHouseDSNFromConfig(Config{
		DBHost:      "db.example.com",
		DBPort:      9440,
		DBUser:      "alice",
		DBPass:      "secret",
		DBName:      "/radiation/",
		ClickSecure: true,
	})
	if dsn != "clickhouse://alice:secret@db.example.com:9440/radiation?secure=true" {
		t.Fatalf("clickhouse DSN = %q", dsn)
	}

	if placeholder("pgx", 3) != "$3" {
		t.Fatalf("pgx placeholder mismatch")
	}
	if placeholder("sqlite", 3) != "?" {
		t.Fatalf("sqlite placeholder mismatch")
	}
	if clampLatitude(91) != 90 || clampLatitude(-91) != -90 {
		t.Fatalf("latitude clamp failed")
	}
	if clampLongitude(181) != 180 || clampLongitude(-181) != -180 {
		t.Fatalf("longitude clamp failed")
	}
	if distanceMeters(55.7558, 37.6173, 55.7558, 37.6173) != 0 {
		t.Fatalf("zero distance should be exact zero")
	}
}

func insertTestMarker(t *testing.T, raw *sql.DB, marker Marker) {
	t.Helper()

	_, err := raw.Exec(`INSERT INTO markers (
id, doseRate, date, lon, lat, countRate, zoom, speed, trackID,
altitude, detector, radiation, temperature, humidity, device_id, transport, device_name, tube, country
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, '', '', NULL, NULL, '', '', '', '', '')`,
		marker.ID,
		marker.DoseRate,
		marker.Date,
		marker.Lon,
		marker.Lat,
		marker.CountRate,
		marker.Zoom,
		marker.Speed,
		marker.TrackID,
	)
	if err != nil {
		t.Fatalf("insert marker %d: %v", marker.ID, err)
	}
}
