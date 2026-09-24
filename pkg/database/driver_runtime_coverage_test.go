package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestInsertMarkersSingleStatementDriverBranches(t *testing.T) {
	db := &Database{idGenerator: startIDGenerator(10)}

	if err := db.insertMarkersSingleStatement(context.Background(), &recordingExecutor{}, nil, "duckdb"); err != nil {
		t.Fatalf("empty single statement: %v", err)
	}

	markers := []Marker{
		{DoseRate: 0.1, Date: 1, Lon: 1, Lat: 1, CountRate: 1, Zoom: 8, Speed: 1, TrackID: "a"},
		{ID: 99, DoseRate: 0.2, Date: 2, Lon: 2, Lat: 2, CountRate: 2, Zoom: 8, Speed: 2, TrackID: "b"},
	}

	t.Run("clickhouse", func(t *testing.T) {
		exec := &recordingExecutor{}
		copyMarkers := append([]Marker(nil), markers...)
		if err := db.insertMarkersSingleStatement(nil, exec, copyMarkers, "clickhouse"); err != nil {
			t.Fatalf("clickhouse single statement: %v", err)
		}
		if !strings.HasPrefix(strings.TrimSpace(exec.query), "INSERT INTO markers") {
			t.Fatalf("unexpected clickhouse query: %s", exec.query)
		}
		if strings.Contains(exec.query, "ON CONFLICT") {
			t.Fatalf("clickhouse query contains ON CONFLICT: %s", exec.query)
		}
		if len(exec.args) != 38 {
			t.Fatalf("clickhouse args = %d, want 38", len(exec.args))
		}
		if copyMarkers[0].ID != 10 || copyMarkers[1].ID != 99 {
			t.Fatalf("clickhouse ids = %d,%d", copyMarkers[0].ID, copyMarkers[1].ID)
		}
	})

	t.Run("duckdb conflict is benign", func(t *testing.T) {
		exec := &recordingExecutor{err: errors.New("Constraint Error: duplicate key")}
		copyMarkers := []Marker{{DoseRate: 1, Date: 3, TrackID: "duck"}}
		if err := db.insertMarkersSingleStatement(context.Background(), exec, copyMarkers, "duckdb"); err != nil {
			t.Fatalf("duckdb conflict: %v", err)
		}
		if !strings.Contains(exec.query, "ON CONFLICT DO NOTHING") {
			t.Fatalf("duckdb query missing conflict clause: %s", exec.query)
		}
	})

	t.Run("duckdb ordinary error", func(t *testing.T) {
		sentinel := errors.New("ordinary failure")
		exec := &recordingExecutor{err: sentinel}
		if err := db.insertMarkersSingleStatement(context.Background(), exec, []Marker{{TrackID: "duck-error"}}, "duckdb"); !errors.Is(err, sentinel) {
			t.Fatalf("duckdb error = %v, want sentinel", err)
		}
	})

	t.Run("unsupported", func(t *testing.T) {
		err := db.insertMarkersSingleStatement(context.Background(), &recordingExecutor{}, []Marker{{TrackID: "x"}}, "sqlite")
		if err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Fatalf("unsupported driver error = %v", err)
		}
	})
}

func TestClickHouseMarkerAndRealtimeBranchesOnSQLite(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)

	marker := Marker{
		DoseRate:  0.5,
		Date:      100,
		Lon:       37.6,
		Lat:       55.7,
		CountRate: 5,
		Zoom:      8,
		Speed:     3,
		TrackID:   "clickhouse-track",
	}

	exec := &recordingExecutor{}
	if err := db.SaveMarkerAtomic(context.Background(), exec, marker, "clickhouse"); err != nil {
		t.Fatalf("save clickhouse-style marker: %v", err)
	}
	if len(exec.args) != 19 {
		t.Fatalf("clickhouse marker args = %d, want 19", len(exec.args))
	}

	if err := db.withSerializedConnectionFor(context.Background(), WorkloadUserUpload, func(ctx context.Context, conn *sql.DB) error {
		_, err := conn.ExecContext(ctx, `INSERT INTO markers (
id,doseRate,date,lon,lat,countRate,zoom,speed,trackID
) VALUES (?,?,?,?,?,?,?,?,?)`, 777, marker.DoseRate, marker.Date, marker.Lon, marker.Lat, marker.CountRate, marker.Zoom, marker.Speed, marker.TrackID)
		return err
	}); err != nil {
		t.Fatalf("seed existing clickhouse marker: %v", err)
	}

	skipped := &recordingExecutor{}
	if err := db.SaveMarkerAtomic(context.Background(), skipped, marker, "clickhouse"); err != nil {
		t.Fatalf("skip existing clickhouse marker: %v", err)
	}
	if skipped.query != "" {
		t.Fatalf("existing clickhouse marker unexpectedly executed insert: %s", skipped.query)
	}

	measurement := RealtimeMeasurement{
		DeviceID:   "clickhouse-device",
		Transport:  "test",
		DeviceName: "device",
		Value:      12,
		Unit:       "cpm",
		Lat:        55.7,
		Lon:        37.6,
		MeasuredAt: 1000,
		FetchedAt:  1001,
	}
	if err := db.InsertRealtimeMeasurement(measurement, "clickhouse"); err != nil {
		t.Fatalf("insert clickhouse-style realtime: %v", err)
	}
	if err := db.InsertRealtimeMeasurement(measurement, "clickhouse"); err != nil {
		t.Fatalf("duplicate clickhouse-style realtime: %v", err)
	}

	var count int
	if err := db.withSerializedConnectionFor(context.Background(), WorkloadWebRead, func(ctx context.Context, conn *sql.DB) error {
		return conn.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM realtime_measurements WHERE device_id = ? AND measured_at = ?",
			measurement.DeviceID,
			measurement.MeasuredAt,
		).Scan(&count)
	}); err != nil {
		t.Fatalf("count clickhouse-style realtime: %v", err)
	}
	if count != 1 {
		t.Fatalf("clickhouse-style realtime count = %d, want 1", count)
	}
}

func TestClickHouseUserOwnershipBranchesOnSQLite(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)

	id, err := db.EnsureUserBySource(context.Background(), "clickhouse-provider", "external-1", "Alice", "clickhouse")
	if err != nil {
		t.Fatalf("ensure clickhouse-style user: %v", err)
	}
	if id == "" {
		t.Fatal("clickhouse-style user id is empty")
	}

	if err := db.UpdateUserNameIfEmpty(context.Background(), id, "Ignored", "clickhouse"); err != nil {
		t.Fatalf("clickhouse name no-op: %v", err)
	}

	if err := db.EnsureTrackUser(context.Background(), "track-clickhouse", id, "", "clickhouse"); err != nil {
		t.Fatalf("ensure clickhouse-style track user: %v", err)
	}
	if err := db.EnsureTrackUser(context.Background(), "track-clickhouse", id, "again", "clickhouse"); err != nil {
		t.Fatalf("repeat clickhouse-style track user: %v", err)
	}

	var count int
	if err := db.withSerializedConnectionFor(context.Background(), WorkloadWebRead, func(ctx context.Context, conn *sql.DB) error {
		return conn.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM track_users WHERE track_id = ? AND user_id = ?",
			"track-clickhouse",
			id,
		).Scan(&count)
	}); err != nil {
		t.Fatalf("count clickhouse-style track ownership: %v", err)
	}
	if count != 1 {
		t.Fatalf("clickhouse-style ownership count = %d, want 1", count)
	}
}
