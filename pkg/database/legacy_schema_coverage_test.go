package database

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func newLegacySchemaCoverageDatabase(t *testing.T) *Database {
	t.Helper()

	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatalf("open legacy sqlite database: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })

	return &Database{
		DB:          raw,
		Driver:      "sqlite",
		idGenerator: startIDGenerator(1),
	}
}

func sqliteColumnNames(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()

	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("describe %s: %v", table, err)
	}
	defer rows.Close()

	names := make(map[string]bool)
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan %s pragma: %v", table, err)
		}
		names[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s pragma: %v", table, err)
	}
	return names
}

func TestLegacySQLiteMetadataUpgradesAddMissingColumns(t *testing.T) {
	db := newLegacySchemaCoverageDatabase(t)

	if _, err := db.DB.Exec(`CREATE TABLE markers (
id INTEGER PRIMARY KEY,
doseRate REAL,
date BIGINT,
lon REAL,
lat REAL,
countRate REAL,
zoom INTEGER,
speed REAL,
trackID TEXT
)`); err != nil {
		t.Fatalf("create legacy markers: %v", err)
	}
	if _, err := db.DB.Exec(`CREATE TABLE realtime_measurements (
id INTEGER PRIMARY KEY,
device_id TEXT,
value REAL,
unit TEXT,
lat REAL,
lon REAL,
measured_at BIGINT,
fetched_at BIGINT
)`); err != nil {
		t.Fatalf("create legacy realtime: %v", err)
	}
	if _, err := db.DB.Exec(`CREATE TABLE analytics_sessions (
session_id TEXT PRIMARY KEY,
created_at BIGINT,
last_seen_at BIGINT,
visit_count INTEGER
)`); err != nil {
		t.Fatalf("create legacy analytics sessions: %v", err)
	}

	if err := db.ensureMarkerMetadataColumns("sqlite", nil); err != nil {
		t.Fatalf("upgrade marker metadata: %v", err)
	}
	if err := db.ensureRealtimeMetadataColumns("sqlite", nil); err != nil {
		t.Fatalf("upgrade realtime metadata: %v", err)
	}
	if err := db.ensureAnalyticsSessionColumns("sqlite", nil); err != nil {
		t.Fatalf("upgrade analytics session metadata: %v", err)
	}

	markerColumns := sqliteColumnNames(t, db.DB, "markers")
	for _, name := range []string{
		"altitude", "detector", "radiation", "temperature", "humidity",
		"device_id", "transport", "device_name", "tube", "country",
	} {
		if !markerColumns[name] {
			t.Fatalf("markers.%s was not added", name)
		}
	}

	realtimeColumns := sqliteColumnNames(t, db.DB, "realtime_measurements")
	for _, name := range []string{"transport", "device_name", "tube", "country", "extra"} {
		if !realtimeColumns[name] {
			t.Fatalf("realtime_measurements.%s was not added", name)
		}
	}

	analyticsColumns := sqliteColumnNames(t, db.DB, "analytics_sessions")
	for _, name := range []string{"visitor_number", "fingerprint"} {
		if !analyticsColumns[name] {
			t.Fatalf("analytics_sessions.%s was not added", name)
		}
	}

	// Re-running upgrades covers the already-present path and guarantees
	// idempotency for installations restarted after a successful migration.
	if err := db.ensureMarkerMetadataColumns("sqlite", nil); err != nil {
		t.Fatalf("repeat marker metadata upgrade: %v", err)
	}
	if err := db.ensureRealtimeMetadataColumns("sqlite", nil); err != nil {
		t.Fatalf("repeat realtime metadata upgrade: %v", err)
	}
	if err := db.ensureAnalyticsSessionColumns("sqlite", nil); err != nil {
		t.Fatalf("repeat analytics metadata upgrade: %v", err)
	}
}

func TestIndexExistsPortableServerBranches(t *testing.T) {
	for _, dbType := range []string{"pgx", "duckdb"} {
		t.Run(dbType, func(t *testing.T) {
			db := newSchemaCoverageDB(t, dbType)
			exists, err := db.indexExistsPortable(nil, dbType, "missing_index")
			if err != nil {
				t.Fatalf("%s index lookup: %v", dbType, err)
			}
			if exists {
				t.Fatalf("%s missing index unexpectedly exists", dbType)
			}
		})
	}
}
