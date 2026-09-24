package database

import (
	"context"
	"strings"
	"testing"
)

func TestInsertMarkersSingleStatementServerBranches(t *testing.T) {
	db := &Database{idGenerator: startIDGenerator(700)}
	markers := []Marker{
		{
			DoseRate: 0.1, Date: 1, Lon: 2, Lat: 3, CountRate: 4,
			Zoom: 5, Speed: 6, TrackID: "track-a",
			Altitude: 100, AltitudeValid: true,
			Temperature: 20, TemperatureValid: true,
			Humidity: 50, HumidityValid: true,
			DeviceID: "device", Transport: "test", DeviceName: "name", Tube: "tube", Country: "DE",
		},
		{
			ID: 999, DoseRate: 0.2, Date: 2, Lon: 3, Lat: 4, CountRate: 5,
			Zoom: 6, Speed: 7, TrackID: "track-b",
		},
	}

	t.Run("duckdb", func(t *testing.T) {
		exec := &recordingExecutor{}
		copyMarkers := append([]Marker(nil), markers...)
		if err := db.insertMarkersSingleStatement(nil, exec, copyMarkers, "duckdb"); err != nil {
			t.Fatalf("duckdb single statement: %v", err)
		}
		if !strings.Contains(exec.query, "ON CONFLICT DO NOTHING") {
			t.Fatalf("duckdb query missing conflict clause: %s", exec.query)
		}
		if len(exec.args) != 38 {
			t.Fatalf("duckdb argument count = %d, want 38", len(exec.args))
		}
		if got, ok := exec.args[0].(int64); !ok || got != 700 {
			t.Fatalf("generated duckdb id = %#v, want 700", exec.args[0])
		}
	})

	t.Run("clickhouse", func(t *testing.T) {
		exec := &recordingExecutor{}
		copyMarkers := append([]Marker(nil), markers...)
		if err := db.insertMarkersSingleStatement(context.Background(), exec, copyMarkers, "clickhouse"); err != nil {
			t.Fatalf("clickhouse single statement: %v", err)
		}
		if strings.Contains(exec.query, "ON CONFLICT") {
			t.Fatalf("clickhouse query unexpectedly contains conflict clause: %s", exec.query)
		}
		if len(exec.args) != 38 {
			t.Fatalf("clickhouse argument count = %d, want 38", len(exec.args))
		}
	})

	t.Run("empty", func(t *testing.T) {
		if err := db.insertMarkersSingleStatement(context.Background(), &recordingExecutor{}, nil, "duckdb"); err != nil {
			t.Fatalf("empty single statement: %v", err)
		}
	})

	t.Run("unsupported", func(t *testing.T) {
		err := db.insertMarkersSingleStatement(context.Background(), &recordingExecutor{}, markers[:1], "sqlite")
		if err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Fatalf("unsupported single statement error = %v", err)
		}
	})
}

func TestSaveMarkerAtomicClickHouseBranch(t *testing.T) {
	db := newSchemaCoverageDB(t, "clickhouse")
	db.idGenerator = startIDGenerator(800)

	exec := &recordingExecutor{}
	marker := Marker{
		DoseRate: 0.4, Date: 10, Lon: 20, Lat: 30, CountRate: 40,
		Zoom: 8, Speed: 5, TrackID: "clickhouse-track",
	}

	if err := db.SaveMarkerAtomic(context.Background(), exec, marker, "clickhouse"); err != nil {
		t.Fatalf("save clickhouse marker: %v", err)
	}
	if len(exec.args) != 19 {
		t.Fatalf("clickhouse argument count = %d, want 19", len(exec.args))
	}
	if got, ok := exec.args[0].(int64); !ok || got != 800 {
		t.Fatalf("clickhouse generated id = %#v, want 800", exec.args[0])
	}
}

func TestInsertRealtimeMeasurementServerDriverBranches(t *testing.T) {
	measurement := RealtimeMeasurement{
		DeviceID: "server-device", Transport: "test", DeviceName: "name",
		Tube: "tube", Country: "DE", Value: 42, Unit: "cpm",
		Lat: 55.7, Lon: 37.6, MeasuredAt: 100, FetchedAt: 101, Extra: "{}",
	}

	t.Run("pgx", func(t *testing.T) {
		db := newSchemaCoverageDB(t, "pgx")
		db.idGenerator = startIDGenerator(900)
		if err := db.InsertRealtimeMeasurement(measurement, "pgx"); err != nil {
			t.Fatalf("pgx realtime insert: %v", err)
		}
	})

	t.Run("clickhouse", func(t *testing.T) {
		db := newSchemaCoverageDB(t, "clickhouse")
		db.idGenerator = startIDGenerator(901)
		if err := db.InsertRealtimeMeasurement(measurement, "clickhouse"); err != nil {
			t.Fatalf("clickhouse realtime insert: %v", err)
		}
	})
}
