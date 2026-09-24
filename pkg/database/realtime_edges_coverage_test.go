package database

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestRealtimeFilteringAndPromotionBranches(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)
	now := time.Now().Unix()

	SetRealtimeConverter(func(value float64, unit string) (float64, bool) {
		if unit != "cpm" || value <= 0 {
			return 0, false
		}
		return value / 100, true
	})
	t.Cleanup(func() { SetRealtimeConverter(nil) })

	rows := []RealtimeMeasurement{
		{DeviceID: "good", Value: 10, Unit: "cpm", Lat: 55.7, Lon: 37.6, MeasuredAt: now - 10, FetchedAt: now - 9, Extra: "{\"temperature\":22}"},
		{DeviceID: "good", Value: 20, Unit: "cpm", Lat: 55.8, Lon: 37.7, MeasuredAt: now - 5, FetchedAt: now - 4, Extra: "{broken"},
		{DeviceID: "zero-location", Value: 10, Unit: "cpm", Lat: 0, Lon: 0, MeasuredAt: now - 1, FetchedAt: now - 1},
		{DeviceID: "zero-value", Value: 0, Unit: "cpm", Lat: 55.7, Lon: 37.6, MeasuredAt: now - 1, FetchedAt: now - 1},
		{DeviceID: "stale", Value: 10, Unit: "cpm", Lat: 55.7, Lon: 37.6, MeasuredAt: now - int64(25*time.Hour/time.Second), FetchedAt: now - int64(25*time.Hour/time.Second)},
		{DeviceID: "bad-unit", Value: 10, Unit: "unknown", Lat: 55.7, Lon: 37.6, MeasuredAt: now - 1, FetchedAt: now - 1},
	}
	for _, row := range rows {
		if err := db.InsertRealtimeMeasurement(row, "sqlite"); err != nil {
			t.Fatalf("insert realtime fixture %s: %v", row.DeviceID, err)
		}
	}

	got, err := db.GetLatestRealtimeByBounds(nil, 50, 30, 60, 40, "sqlite")
	if err != nil {
		t.Fatalf("latest realtime bounds: %v", err)
	}
	if len(got) != 1 || got[0].DeviceID != "good" {
		t.Fatalf("filtered latest realtime = %#v, want only good device", got)
	}
	if got[0].DoseRate != 0.20 {
		t.Fatalf("latest converted dose = %v, want 0.20", got[0].DoseRate)
	}

	SetRealtimeConverter(nil)
	got, err = db.GetLatestRealtimeByBounds(context.Background(), 50, 30, 60, 40, "sqlite")
	if err != nil {
		t.Fatalf("latest realtime without converter: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("latest realtime without converter = %#v, want none", got)
	}

	if _, err := db.GetRealtimeHistory("", 0, "sqlite"); err == nil {
		t.Fatal("empty realtime history device id did not fail")
	}
	history, err := db.GetRealtimeHistory("good", now-100, "sqlite")
	if err != nil {
		t.Fatalf("realtime history: %v", err)
	}
	if len(history) != 2 || history[0].MeasuredAt > history[1].MeasuredAt {
		t.Fatalf("realtime history ordering = %#v", history)
	}
}

func TestPromoteStaleRealtimeSkipsStationaryAndUnconvertible(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)
	now := time.Now().Unix()
	cutoff := now

	SetRealtimeConverter(func(value float64, unit string) (float64, bool) {
		if unit != "cpm" {
			return 0, false
		}
		return value / 100, true
	})
	t.Cleanup(func() { SetRealtimeConverter(nil) })

	rows := []RealtimeMeasurement{
		{DeviceID: "moving", Transport: "walk", Value: 10, Unit: "cpm", Lat: 1, Lon: 1, MeasuredAt: 10, FetchedAt: 10},
		{DeviceID: "moving", Transport: "walk", Value: 20, Unit: "cpm", Lat: 2, Lon: 2, MeasuredAt: 20, FetchedAt: 20},
		{DeviceID: "stationary", Value: 30, Unit: "cpm", Lat: 3, Lon: 3, MeasuredAt: 30, FetchedAt: 30},
		{DeviceID: "stationary", Value: 40, Unit: "cpm", Lat: 3, Lon: 3, MeasuredAt: 40, FetchedAt: 40},
		{DeviceID: "unsupported", Value: 50, Unit: "unknown", Lat: 4, Lon: 4, MeasuredAt: 50, FetchedAt: 50},
		{DeviceID: "unsupported", Value: 60, Unit: "unknown", Lat: 5, Lon: 5, MeasuredAt: 60, FetchedAt: 60},
	}
	for _, row := range rows {
		if err := db.InsertRealtimeMeasurement(row, "sqlite"); err != nil {
			t.Fatalf("insert promotion fixture: %v", err)
		}
	}

	if err := db.PromoteStaleRealtime(cutoff, "sqlite"); err != nil {
		t.Fatalf("promote stale realtime: %v", err)
	}

	moving, err := db.GetMarkersByTrackID(context.Background(), "live:moving", "sqlite")
	if err != nil {
		t.Fatalf("read promoted moving markers: %v", err)
	}
	if len(moving) != 2 {
		t.Fatalf("promoted moving markers = %d, want 2", len(moving))
	}

	stationaryHistory, err := db.fetchRealtimeByDevice("stationary", "sqlite")
	if err != nil {
		t.Fatalf("fetch stationary history: %v", err)
	}
	if len(stationaryHistory) != 2 {
		t.Fatalf("stationary history was removed: %d rows", len(stationaryHistory))
	}

	unsupportedHistory, err := db.fetchRealtimeByDevice("unsupported", "sqlite")
	if err != nil {
		t.Fatalf("fetch unsupported history: %v", err)
	}
	if len(unsupportedHistory) != 0 {
		t.Fatalf("moving unsupported history should be removed after promotion attempt, got %d", len(unsupportedHistory))
	}

	var promotedUnsupported int
	if err := db.withSerializedConnectionFor(context.Background(), WorkloadWebRead, func(ctx context.Context, conn *sql.DB) error {
		return conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM markers WHERE trackID = ?", "live:unsupported").Scan(&promotedUnsupported)
	}); err != nil {
		t.Fatalf("count unsupported promoted markers: %v", err)
	}
	if promotedUnsupported != 0 {
		t.Fatalf("unsupported-unit promoted markers = %d, want 0", promotedUnsupported)
	}
}


func TestPromoteStaleRealtimePGXTransactionPath(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)

	SetRealtimeConverter(func(value float64, unit string) (float64, bool) {
		if unit != "cpm" {
			return 0, false
		}
		return value / 100, true
	})
	t.Cleanup(func() { SetRealtimeConverter(nil) })

	rows := []RealtimeMeasurement{
		{DeviceID: "pgx-moving", Transport: "walk", Value: 10, Unit: "cpm", Lat: 1, Lon: 1, MeasuredAt: 10, FetchedAt: 10},
		{DeviceID: "pgx-moving", Transport: "walk", Value: 20, Unit: "cpm", Lat: 2, Lon: 2, MeasuredAt: 20, FetchedAt: 20},
	}
	for _, row := range rows {
		if err := db.InsertRealtimeMeasurement(row, "sqlite"); err != nil {
			t.Fatalf("insert pgx promotion fixture: %v", err)
		}
	}

	// SQLite accepts numbered $1 placeholders, so it can exercise the PostgreSQL
	// transaction branch without requiring a network database in unit tests.
	db.Driver = "pgx"
	if err := db.PromoteStaleRealtime(100, "pgx"); err != nil {
		t.Fatalf("pgx-style realtime promotion: %v", err)
	}

	var markerCount int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM markers WHERE trackID = $1", "live:pgx-moving").Scan(&markerCount); err != nil {
		t.Fatalf("count pgx-style promoted markers: %v", err)
	}
	if markerCount != 2 {
		t.Fatalf("pgx-style promoted marker count = %d, want 2", markerCount)
	}

	var realtimeCount int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM realtime_measurements WHERE device_id = $1", "pgx-moving").Scan(&realtimeCount); err != nil {
		t.Fatalf("count pgx-style realtime rows: %v", err)
	}
	if realtimeCount != 0 {
		t.Fatalf("pgx-style realtime rows after promotion = %d, want 0", realtimeCount)
	}
}
