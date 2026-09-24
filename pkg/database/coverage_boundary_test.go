package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestDatabaseCoverageBoundaryBranches(t *testing.T) {
	if isUniqueConstraintError(nil) || !isUniqueConstraintError(errors.New("UNIQUE VIOLATION")) || !isUniqueConstraintError(errors.New("duplicate key")) || !isUniqueConstraintError(errors.New("constraint failed")) {
		t.Fatal("unique constraint errors were not normalized")
	}
	if duckDBIsConflict(nil) || !duckDBIsConflict(errors.New("Constraint Error: duplicate")) || !duckDBIsConflict(errors.New("update the same row twice")) || duckDBIsConflict(errors.New("disk full")) {
		t.Fatal("DuckDB conflict classification failed")
	}
	if maxInt64OrZero(sql.NullInt64{}) != 0 || maxInt64OrZero(sql.NullInt64{Int64: 42, Valid: true}) != 42 {
		t.Fatal("nullable integer conversion failed")
	}
	if nullableFloat64(false, 2.5) != nil || nullableFloat64(true, 2.5) != 2.5 {
		t.Fatal("nullable float conversion failed")
	}
	if got := newPlaceholderGenerator("pgx")(); got != "$1" {
		t.Fatalf("first PostgreSQL placeholder = %q", got)
	}
	if got := newPlaceholderGenerator("sqlite")(); got != "?" {
		t.Fatalf("SQLite placeholder = %q", got)
	}
	if len(desiredIndexesPortable("clickhouse")) != 0 || len(desiredIndexesPortable("sqlite")) == 0 {
		t.Fatal("portable index selection failed")
	}

	database := newTestDatabase(t)
	ctx := context.Background()
	for _, dbType := range []string{"pgx", "duckdb"} {
		if _, err := database.loadColumnPresence(ctx, dbType, "markers"); err == nil {
			t.Errorf("%s column lookup unexpectedly worked against SQLite", dbType)
		}
		if err := database.ensureMarkerMetadataColumns(dbType, nil); err == nil {
			t.Errorf("%s marker migration unexpectedly worked against SQLite", dbType)
		}
		if err := database.ensureRealtimeMetadataColumns(dbType, nil); err == nil {
			t.Errorf("%s realtime migration unexpectedly worked against SQLite", dbType)
		}
		if err := database.ensureAnalyticsSessionColumns(dbType, nil); err == nil {
			t.Errorf("%s analytics migration unexpectedly worked against SQLite", dbType)
		}
	}
	if err := database.ensureMarkerMetadataColumns("clickhouse", nil); err != nil {
		t.Fatalf("ClickHouse marker migration no-op: %v", err)
	}
	if err := database.ensureRealtimeMetadataColumns("clickhouse", nil); err != nil {
		t.Fatalf("ClickHouse realtime migration no-op: %v", err)
	}
	if err := database.ensureAnalyticsSessionColumns("clickhouse", nil); err != nil {
		t.Fatalf("ClickHouse analytics migration through SQLite fixture: %v", err)
	}
	marker := Marker{DoseRate: 1, Date: 10, Lon: 20, Lat: 30, CountRate: 4, Zoom: 5, Speed: 6, TrackID: "coverage-track"}
	if err := database.SaveMarkerAtomic(ctx, database.DB, marker, "sqlite"); err != nil {
		t.Fatal(err)
	}
	if err := database.SaveMarkerAtomic(ctx, database.DB, marker, "sqlite"); err != nil {
		t.Fatalf("duplicate marker insert should be ignored: %v", err)
	}
	if err := database.InsertMarkersBulk(ctx, nil, []Marker{marker}, "sqlite", 1, nil, WorkloadUserUpload); err != nil {
		t.Fatalf("single marker batch: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := database.InsertMarkersBulk(cancelled, nil, []Marker{marker}, "sqlite", 1, nil, WorkloadUserUpload); err == nil {
		t.Fatal("cancelled batch insert succeeded")
	}
	duckDBMarkers := []Marker{marker, marker}
	duckDBMarkers[1].TrackID = "coverage-track-2"
	duckDBMarkers[1].Date++
	if err := database.InsertMarkersBulk(ctx, nil, duckDBMarkers, "duckdb", 0, nil, WorkloadUserUpload); err != nil {
		t.Fatalf("DuckDB transactional batch through SQLite fixture: %v", err)
	}
	if err := database.SaveMarkerAtomic(ctx, database.DB, Marker{TrackID: "coverage-duck", Date: 50}, "duckdb"); err != nil {
		t.Fatalf("DuckDB marker insert through SQLite fixture: %v", err)
	}
	if err := database.InsertMarkersBulk(ctx, nil, []Marker{marker}, "pgx", 1, nil, WorkloadUserUpload); err == nil {
		t.Fatal("PostgreSQL bulk SQL unexpectedly succeeded on SQLite fixture")
	}
	if err := database.SaveMarkerAtomic(ctx, database.DB, marker, "pgx"); err == nil {
		t.Fatal("PostgreSQL marker SQL unexpectedly succeeded on SQLite fixture")
	}
	if err := database.InsertRealtimeMeasurement(RealtimeMeasurement{DeviceID: "pg-device"}, "pgx"); err == nil {
		t.Fatal("PostgreSQL realtime SQL unexpectedly succeeded on SQLite fixture")
	}
	if err := database.EnsureImportHistory(ctx, "source", "empty-id", "", "", "", "sqlite"); err != nil {
		t.Fatalf("empty optional import fields: %v", err)
	}
	if found, err := database.TrackExists(ctx, "", "sqlite"); err != nil || found {
		t.Fatalf("empty track lookup = %t, %v", found, err)
	}
	if err := database.UpdateTrackDeviceName(ctx, "missing", "", "sqlite"); err != nil {
		t.Fatalf("empty device update should be a no-op: %v", err)
	}
	if err := database.AnnotateTrackRadiationWindow(ctx, "", 0, 0, "", "sqlite"); err != nil {
		t.Fatalf("empty track annotation should be a no-op: %v", err)
	}
	if _, err := database.GetTrackIDByIndex(ctx, 0, "sqlite"); err == nil {
		t.Fatal("zero index lookup was accepted")
	}
	if err := syncPostgresSequence(nil, database.DB, "tracks", "id", 0); err == nil {
		t.Fatal("PostgreSQL sequence sync ignored a nil context")
	}
	if err := database.PromoteStaleRealtime(time.Now().Unix(), "sqlite"); err != nil {
		t.Fatalf("empty realtime promotion: %v", err)
	}
}
