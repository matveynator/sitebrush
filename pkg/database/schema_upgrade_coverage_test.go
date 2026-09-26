package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func newLegacySchemaDatabase(t *testing.T) *Database {
	t.Helper()

	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatalf("open legacy sqlite database: %v", err)
	}
	t.Cleanup(func() {
		_ = raw.Close()
	})
	raw.SetMaxOpenConns(1)
	raw.SetMaxIdleConns(1)

	statements := []string{
		"CREATE TABLE markers (id INTEGER PRIMARY KEY)",
		"CREATE TABLE realtime_measurements (id INTEGER PRIMARY KEY, device_id TEXT, measured_at BIGINT)",
		"CREATE TABLE analytics_sessions (session_id TEXT PRIMARY KEY, display_name TEXT, created_at BIGINT NOT NULL, last_seen_at BIGINT NOT NULL, visit_count INTEGER NOT NULL, ip TEXT, user_agent TEXT, referer TEXT)",
	}
	for _, statement := range statements {
		if _, err := raw.Exec(statement); err != nil {
			t.Fatalf("create legacy schema: %v", err)
		}
	}

	return &Database{
		DB:          raw,
		Driver:      "sqlite",
		idGenerator: startIDGenerator(1),
		pipeline:    startSerializedPipeline(raw),
	}
}

func TestLegacySQLiteMetadataColumnsAreUpgraded(t *testing.T) {
	db := newLegacySchemaDatabase(t)
	logf := func(string, ...any) {}

	if err := db.ensureMarkerMetadataColumns("sqlite", logf); err != nil {
		t.Fatalf("upgrade marker metadata: %v", err)
	}
	if err := db.ensureRealtimeMetadataColumns("sqlite", logf); err != nil {
		t.Fatalf("upgrade realtime metadata: %v", err)
	}
	if err := db.ensureAnalyticsSessionColumns("sqlite", logf); err != nil {
		t.Fatalf("upgrade analytics session metadata: %v", err)
	}

	markerColumns, err := db.loadColumnPresence(context.Background(), "sqlite", "markers")
	if err != nil {
		t.Fatalf("read upgraded marker columns: %v", err)
	}
	for _, column := range []string{
		"altitude", "detector", "radiation", "temperature", "humidity",
		"device_id", "transport", "device_name", "tube", "country",
	} {
		if !markerColumns[column] {
			t.Fatalf("markers.%s was not added", column)
		}
	}

	realtimeColumns, err := db.loadColumnPresence(context.Background(), "sqlite", "realtime_measurements")
	if err != nil {
		t.Fatalf("read upgraded realtime columns: %v", err)
	}
	for _, column := range []string{"transport", "device_name", "tube", "country", "extra"} {
		if !realtimeColumns[column] {
			t.Fatalf("realtime_measurements.%s was not added", column)
		}
	}

	sessionColumns, err := db.loadColumnPresence(context.Background(), "sqlite", "analytics_sessions")
	if err != nil {
		t.Fatalf("read upgraded analytics columns: %v", err)
	}
	for _, column := range []string{"visitor_number", "fingerprint"} {
		if !sessionColumns[column] {
			t.Fatalf("analytics_sessions.%s was not added", column)
		}
	}

	if err := db.ensureMarkerMetadataColumns("sqlite", logf); err != nil {
		t.Fatalf("repeat marker upgrade: %v", err)
	}
	if err := db.ensureRealtimeMetadataColumns("sqlite", logf); err != nil {
		t.Fatalf("repeat realtime upgrade: %v", err)
	}
	if err := db.ensureAnalyticsSessionColumns("sqlite", logf); err != nil {
		t.Fatalf("repeat analytics upgrade: %v", err)
	}
}

func TestMetadataUpgradeEngineBranches(t *testing.T) {
	db := newLegacySchemaDatabase(t)
	logf := func(string, ...any) {}

	if err := db.ensureMarkerMetadataColumns("clickhouse", logf); err != nil {
		t.Fatalf("clickhouse marker metadata branch: %v", err)
	}
	if err := db.ensureRealtimeMetadataColumns("clickhouse", logf); err != nil {
		t.Fatalf("clickhouse realtime metadata branch: %v", err)
	}
	if err := db.ensureAnalyticsSessionColumns("clickhouse", logf); err != nil {
		t.Fatalf("clickhouse analytics metadata branch: %v", err)
	}

	if _, err := db.loadColumnPresence(context.Background(), "pgx", "markers"); err == nil {
		t.Fatal("sqlite test database unexpectedly satisfied PostgreSQL information_schema query")
	}
	if _, err := db.loadColumnPresence(context.Background(), "duckdb", "markers"); err == nil {
		t.Fatal("sqlite test database unexpectedly satisfied DuckDB information_schema query")
	}
}
