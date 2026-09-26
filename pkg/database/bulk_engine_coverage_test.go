package database

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestInsertMarkersBulkDuckDBOrchestrationOnSQLite(t *testing.T) {
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "duck-bulk.db"))
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })

	db := &Database{
		DB:          raw,
		Driver:      "pgx",
		idGenerator: startIDGenerator(1),
	}
	if err := db.InitSchema(Config{DBType: "sqlite"}, func(string, ...any) {}); err != nil {
		t.Fatalf("init sqlite schema: %v", err)
	}

	markers := []Marker{
		{DoseRate: 0.3, Date: 3, Lon: 3, Lat: 3, CountRate: 3, Zoom: 8, Speed: 3, TrackID: "b"},
		{DoseRate: 0.1, Date: 1, Lon: 1, Lat: 1, CountRate: 1, Zoom: 8, Speed: 1, TrackID: "a"},
		{DoseRate: 0.2, Date: 2, Lon: 2, Lat: 2, CountRate: 2, Zoom: 8, Speed: 2, TrackID: "a"},
	}
	progress := make(chan MarkerBatchProgress, 4)
	if err := db.InsertMarkersBulk(context.Background(), nil, markers, "duckdb", 1000, progress, WorkloadArchive); err != nil {
		t.Fatalf("duckdb-style bulk insert: %v", err)
	}
	if len(progress) != 1 {
		t.Fatalf("duckdb-style progress messages = %d, want 1", len(progress))
	}

	var count int
	if err := raw.QueryRow("SELECT COUNT(*) FROM markers").Scan(&count); err != nil {
		t.Fatalf("count duckdb-style markers: %v", err)
	}
	if count != 3 {
		t.Fatalf("duckdb-style marker count = %d, want 3", count)
	}
}

func TestInsertMarkersBulkDuckDBConflictFallback(t *testing.T) {
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "duck-fallback.db"))
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })

	db := &Database{
		DB:          raw,
		Driver:      "pgx",
		idGenerator: startIDGenerator(1),
	}
	if err := db.InitSchema(Config{DBType: "sqlite"}, func(string, ...any) {}); err != nil {
		t.Fatalf("init fallback schema: %v", err)
	}

	if _, err := raw.Exec(`CREATE TRIGGER markers_conflict BEFORE INSERT ON markers
BEGIN
  SELECT RAISE(ABORT, 'Constraint Error: duplicate key');
END;`); err != nil {
		t.Fatalf("create conflict trigger: %v", err)
	}

	progress := make(chan MarkerBatchProgress, 2)
	markers := []Marker{
		{DoseRate: 0.1, Date: 1, Lon: 1, Lat: 1, CountRate: 1, Zoom: 8, Speed: 1, TrackID: "a"},
		{DoseRate: 0.2, Date: 2, Lon: 2, Lat: 2, CountRate: 2, Zoom: 8, Speed: 2, TrackID: "a"},
	}
	if err := db.InsertMarkersBulk(context.Background(), nil, markers, "duckdb", 2, progress, WorkloadArchive); err != nil {
		t.Fatalf("duckdb conflict fallback: %v", err)
	}
	update := <-progress
	if update.Mode != "fallback" || update.Done != 2 {
		t.Fatalf("duckdb fallback progress = %#v", update)
	}
}

func TestInsertMarkersBulkDuckDBOrdinaryErrorRollsBack(t *testing.T) {
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "duck-error.db"))
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })

	db := &Database{DB: raw, Driver: "pgx", idGenerator: startIDGenerator(1)}
	if err := db.InitSchema(Config{DBType: "sqlite"}, func(string, ...any) {}); err != nil {
		t.Fatalf("init error schema: %v", err)
	}
	if _, err := raw.Exec(`CREATE TRIGGER markers_failure BEFORE INSERT ON markers
BEGIN
  SELECT RAISE(ABORT, 'ordinary write failure');
END;`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	err = db.InsertMarkersBulk(context.Background(), nil, []Marker{{
		DoseRate: 0.1, Date: 1, Lon: 1, Lat: 1, CountRate: 1, Zoom: 8, Speed: 1, TrackID: "a",
	}}, "duckdb", 1, nil, WorkloadArchive)
	if err == nil || !strings.Contains(err.Error(), "bulk exec") {
		t.Fatalf("ordinary duckdb bulk error = %v", err)
	}

	var count int
	if scanErr := raw.QueryRow("SELECT COUNT(*) FROM markers").Scan(&count); scanErr != nil {
		t.Fatalf("count rolled-back markers: %v", scanErr)
	}
	if count != 0 {
		t.Fatalf("rolled-back marker count = %d, want 0", count)
	}
}

func TestInsertMarkersBulkClickHousePathOnSQLite(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)

	markers := []Marker{
		{ID: 100, DoseRate: 0.1, Date: 1, Lon: 1, Lat: 1, CountRate: 1, Zoom: 8, Speed: 1, TrackID: "single"},
		{ID: 101, DoseRate: 0.2, Date: 2, Lon: 2, Lat: 2, CountRate: 2, Zoom: 8, Speed: 2, TrackID: "single"},
		{ID: 101, DoseRate: 0.2, Date: 2, Lon: 2, Lat: 2, CountRate: 2, Zoom: 8, Speed: 2, TrackID: "single"},
	}
	progress := make(chan MarkerBatchProgress, 2)
	if err := db.InsertMarkersBulk(context.Background(), nil, markers, "clickhouse", 100, progress, WorkloadArchive); err != nil {
		t.Fatalf("clickhouse-style fast bulk insert: %v", err)
	}
	if len(progress) != 1 {
		t.Fatalf("clickhouse fast progress messages = %d, want 1", len(progress))
	}

	var count int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM markers WHERE trackID = ?", "single").Scan(&count); err != nil {
		t.Fatalf("count clickhouse-style markers: %v", err)
	}
	if count != 2 {
		t.Fatalf("clickhouse-style marker count = %d, want 2", count)
	}

	// Repeating the same payload exercises the all-existing fast-path.
	if err := db.InsertMarkersBulk(context.Background(), nil, markers, "clickhouse", 100, nil, WorkloadArchive); err != nil {
		t.Fatalf("repeat clickhouse-style bulk insert: %v", err)
	}
}

func TestInsertMarkersBulkPGXBuildsFallbackStatement(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)

	err := db.InsertMarkersBulk(context.Background(), nil, []Marker{{
		DoseRate: 0.1, Date: 1, Lon: 1, Lat: 1, CountRate: 1, Zoom: 8, Speed: 1, TrackID: "pgx",
	}}, "pgx", 1, nil, WorkloadArchive)
	if err == nil {
		t.Fatal("sqlite unexpectedly accepted PostgreSQL bulk SQL")
	}
}

func TestInsertMarkersBulkCancelledBeforeChunk(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := db.InsertMarkersBulk(ctx, nil, []Marker{{
		DoseRate: 1, Date: 1, Lon: 1, Lat: 1, CountRate: 1, Zoom: 1, Speed: 1, TrackID: "cancel",
	}}, "sqlite", 1, nil, WorkloadArchive)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled bulk error = %v, want context.Canceled", err)
	}
}
