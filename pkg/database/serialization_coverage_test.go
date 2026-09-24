package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestDatabaseSerializedPathsAndMaintenanceQueue(t *testing.T) {
	if err := syncPostgresSequence(context.Background(), nil, "tracks", "id", 1); err == nil {
		t.Fatal("nil PostgreSQL database accepted")
	}
	if err := (*Database)(nil).withSerializedConnection(context.Background(), nil); err == nil {
		t.Fatal("nil database accepted")
	}
	if result := (*Database)(nil).ScheduleDuckDBMaintenance(context.Background(), nil); result == nil {
		t.Fatal("nil database returned nil result")
	} else if _, open := <-result; open {
		t.Fatal("nil database result should be closed")
	}
	if result := (&Database{Driver: "sqlite"}).ScheduleDuckDBMaintenance(context.Background(), nil); result == nil {
		t.Fatal("non-DuckDB returned nil result")
	} else if _, open := <-result; open {
		t.Fatal("non-DuckDB result should be closed")
	}
	if _, err := (serializedExecutor{}).Exec("SELECT 1"); err == nil {
		t.Fatal("nil executor database accepted")
	}
	if result := (*duckDBMaintenance)(nil).enqueue(context.Background(), nil); result == nil {
		t.Fatal("nil maintenance queue returned nil")
	} else if _, open := <-result; open {
		t.Fatal("nil queue result should be closed")
	}

	connection, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "serialization.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	plain := &Database{DB: connection, Driver: "postgres"}
	called := false
	if err := plain.withSerializedConnection(context.Background(), func(_ context.Context, conn *sql.DB) error { called = conn == connection; return nil }); err != nil || !called {
		t.Fatalf("direct call=%v err=%v", called, err)
	}
	serialized := &Database{DB: connection, Driver: "sqlite", pipeline: startSerializedPipeline(connection)}
	if err := serialized.withSerializedConnection(context.Background(), func(_ context.Context, conn *sql.DB) error { return conn.Ping() }); err != nil {
		t.Fatalf("serialized connection: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := serialized.withSerializedConnection(cancelled, func(context.Context, *sql.DB) error { t.Fatal("cancelled operation ran"); return nil }); err == nil {
		t.Fatal("cancelled operation succeeded")
	}
	if err := runDuckDBMaintenance(cancelled, connection, nil); err == nil {
		t.Fatal("cancelled maintenance succeeded")
	}
	maintenance := startDuckDBMaintenance(&Database{DB: connection, Driver: "duckdb"})
	if err := <-maintenance.enqueue(cancelled, nil); err == nil {
		t.Fatal("cancelled queued maintenance succeeded")
	}
}

func TestPostgreSQLCopyRejectsUnavailableDatabasesAndSkipsEmptyBatches(t *testing.T) {
	ctx := context.Background()
	var unavailable *Database
	if err := unavailable.insertMarkersPostgreSQLCopy(ctx, nil); err != nil {
		t.Fatalf("empty COPY should be a no-op: %v", err)
	}
	if err := unavailable.insertMarkersPostgreSQLCopy(ctx, []Marker{{}}); err == nil {
		t.Fatal("COPY accepted unavailable database")
	}
	if err := unavailable.insertMarkersPostgreSQLCopyBatched(ctx, nil, 0, nil); err != nil {
		t.Fatalf("empty batched COPY should be a no-op: %v", err)
	}
	if err := unavailable.insertMarkersPostgreSQLCopyBatched(ctx, []Marker{{}}, 0, nil); err == nil {
		t.Fatal("batched COPY accepted unavailable database")
	}
}

func TestInsertRealtimeMeasurementDriverBranches(t *testing.T) {
	db := newTestDatabase(t)
	measurement := RealtimeMeasurement{DeviceID: "device-1", Transport: "walk", DeviceName: "sensor", Tube: "tube", Country: "US", Value: 1.25, Unit: "uSv/h", Lat: 37.4, Lon: -122.1, MeasuredAt: 100, FetchedAt: 101, Extra: `{}`}
	if err := db.InsertRealtimeMeasurement(measurement, "sqlite"); err != nil {
		t.Fatalf("sqlite insert: %v", err)
	}
	if err := db.InsertRealtimeMeasurement(measurement, "sqlite"); err != nil {
		t.Fatalf("sqlite duplicate insert: %v", err)
	}
	measurement.DeviceID = "device-clickhouse"
	if err := db.InsertRealtimeMeasurement(measurement, "clickhouse"); err != nil {
		t.Fatalf("clickhouse insert path on compatible SQLite fixture: %v", err)
	}
	if err := db.InsertRealtimeMeasurement(measurement, "clickhouse"); err != nil {
		t.Fatalf("clickhouse duplicate path: %v", err)
	}
	if err := db.InsertRealtimeMeasurement(measurement, "pgx"); err == nil {
		t.Fatal("PostgreSQL-only SQL unexpectedly succeeded on SQLite")
	}
}

func TestRealtimeHistoryPromotionAndBounds(t *testing.T) {
	db := newTestDatabase(t)
	SetRealtimeConverter(func(value float64, _ string) (float64, bool) { return value, value > 0 })
	t.Cleanup(func() { SetRealtimeConverter(nil) })
	now := time.Now().Unix()
	for index, location := range []struct{ lat, lon float64 }{{37.4, -122.1}, {38.0, -122.1}} {
		measurement := RealtimeMeasurement{
			DeviceID: "moving-device", Transport: "walk", DeviceName: "sensor", Tube: "tube", Country: "US",
			Value: 1.5, Unit: "uSv/h", Lat: location.lat, Lon: location.lon,
			MeasuredAt: now - int64(2-index), FetchedAt: now - 10, Extra: `{"temperature":21}`,
		}
		if err := db.InsertRealtimeMeasurement(measurement, "sqlite"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.GetRealtimeHistory("", 0, "sqlite"); err == nil {
		t.Fatal("empty realtime device ID accepted")
	}
	history, err := db.GetRealtimeHistory("moving-device", now-60, "sqlite")
	if err != nil || len(history) != 2 {
		t.Fatalf("realtime history=%+v err=%v", history, err)
	}
	markers, err := db.GetLatestRealtimeByBounds(context.Background(), 0, -180, 90, 180, "sqlite")
	if err != nil || len(markers) != 1 || markers[0].DeviceID != "moving-device" || markers[0].DoseRate != 1.5 {
		t.Fatalf("latest realtime markers=%+v err=%v", markers, err)
	}
	if err := db.PromoteStaleRealtime(now, "sqlite"); err != nil {
		t.Fatalf("promote stale realtime: %v", err)
	}
	var markerCount, realtimeCount int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM markers WHERE trackID=?`, "live:moving-device").Scan(&markerCount); err != nil {
		t.Fatal(err)
	}
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM realtime_measurements WHERE device_id=?`, "moving-device").Scan(&realtimeCount); err != nil {
		t.Fatal(err)
	}
	if markerCount != 2 || realtimeCount != 0 {
		t.Fatalf("promoted marker count=%d realtime count=%d", markerCount, realtimeCount)
	}
}
