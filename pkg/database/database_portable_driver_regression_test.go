package database

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestInsertMarkersSingleStatementKeepsTheOnlyPortableFastPathCovered verifies
// that the remaining single-statement path still allocates IDs and keeps the
// duplicate-safe clause after removing the unsupported database backend.
func TestInsertMarkersSingleStatementKeepsPortableFastPath(t *testing.T) {
	database := &Database{idGenerator: startIDGenerator(1200)}
	executor := &recordingExecutor{}
	markers := []Marker{{TrackID: "portable-fast", DoseRate: 1}}

	if err := database.insertMarkersSingleStatement(context.Background(), executor, markers, "duckdb"); err != nil {
		t.Fatalf("portable fast path: %v", err)
	}
	if !strings.Contains(executor.query, "ON CONFLICT DO NOTHING") {
		t.Fatalf("portable fast path omitted duplicate protection: %s", executor.query)
	}
	if len(executor.args) != 19 || executor.args[0] != int64(1200) {
		t.Fatalf("portable fast path args = %#v, want generated 19-column row", executor.args)
	}
}

func TestInsertMarkersSingleStatementReportsDriverAndExecutorFailures(t *testing.T) {
	database := &Database{idGenerator: startIDGenerator(1300)}
	marker := []Marker{{TrackID: "portable-error"}}

	if err := database.insertMarkersSingleStatement(nil, &recordingExecutor{}, nil, "duckdb"); err != nil {
		t.Fatalf("empty portable fast path: %v", err)
	}
	if err := database.insertMarkersSingleStatement(context.Background(), &recordingExecutor{}, marker, "sqlite"); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unsupported driver error = %v", err)
	}

	sentinel := errors.New("portable executor failure")
	if err := database.insertMarkersSingleStatement(nil, &recordingExecutor{err: sentinel}, marker, "duckdb"); !errors.Is(err, sentinel) {
		t.Fatalf("executor error = %v, want %v", err, sentinel)
	}
}

func TestSchemaUpgradeHelpersAcceptNilLoggerAfterInitialization(t *testing.T) {
	database := newTestDatabase(t)
	if err := database.ensureMarkerMetadataColumns("sqlite", nil); err != nil {
		t.Fatalf("marker metadata recheck: %v", err)
	}
	if err := database.ensureRealtimeMetadataColumns("sqlite", nil); err != nil {
		t.Fatalf("realtime metadata recheck: %v", err)
	}
	if err := database.ensureAnalyticsSessionColumns("sqlite", nil); err != nil {
		t.Fatalf("analytics metadata recheck: %v", err)
	}
}

func TestSchemaUpgradeHelpersAddColumnsToLegacySQLiteTables(t *testing.T) {
	database := newTestDatabase(t)
	legacyTables := []string{
		"DROP TABLE markers",
		"DROP TABLE realtime_measurements",
		"DROP TABLE analytics_sessions",
		"CREATE TABLE markers (id INTEGER)",
		"CREATE TABLE realtime_measurements (id INTEGER)",
		"CREATE TABLE analytics_sessions (session_id TEXT)",
	}
	for _, statement := range legacyTables {
		if _, err := database.DB.Exec(statement); err != nil {
			t.Fatalf("prepare legacy schema with %q: %v", statement, err)
		}
	}
	if err := database.ensureMarkerMetadataColumns("sqlite", nil); err != nil {
		t.Fatalf("upgrade legacy markers: %v", err)
	}
	if err := database.ensureRealtimeMetadataColumns("sqlite", nil); err != nil {
		t.Fatalf("upgrade legacy realtime measurements: %v", err)
	}
	if err := database.ensureAnalyticsSessionColumns("sqlite", nil); err != nil {
		t.Fatalf("upgrade legacy analytics sessions: %v", err)
	}
}

func TestInsertMarkersBulkPortablePathPreservesDuplicateInvariant(t *testing.T) {
	database, _ := newSQLiteConcurrencyTestDatabase(t)
	markers := []Marker{
		{DoseRate: 0.5, Date: 42, Lon: 10, Lat: 20, CountRate: 3, Zoom: 8, Speed: 1, TrackID: "portable-bulk"},
		{DoseRate: 0.5, Date: 42, Lon: 10, Lat: 20, CountRate: 3, Zoom: 8, Speed: 1, TrackID: "portable-bulk"},
	}
	if err := database.InsertMarkersBulk(context.Background(), nil, markers, "sqlite", 100, nil, WorkloadUserUpload); err != nil {
		t.Fatalf("portable bulk insert: %v", err)
	}
	var count int
	if err := database.DB.QueryRow("SELECT COUNT(*) FROM markers WHERE trackID = ?", "portable-bulk").Scan(&count); err != nil {
		t.Fatalf("count portable bulk rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("portable bulk row count = %d, want one duplicate-safe row", count)
	}
}
