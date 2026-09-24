package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

type recordingExecutor struct {
	query string
	args  []any
	err   error
}

func (r *recordingExecutor) Exec(query string, args ...interface{}) (sql.Result, error) {
	r.query = query
	r.args = append([]any(nil), args...)
	return nil, r.err
}

func (r *recordingExecutor) ExecContext(_ context.Context, query string, args ...interface{}) (sql.Result, error) {
	r.query = query
	r.args = append([]any(nil), args...)
	return nil, r.err
}

func TestSaveMarkerAtomicDriverBranches(t *testing.T) {
	db := &Database{idGenerator: startIDGenerator(100)}
	marker := Marker{
		DoseRate:         0.2,
		Date:             123,
		Lon:              37.6,
		Lat:              55.7,
		CountRate:        2,
		Zoom:             8,
		Speed:            3,
		TrackID:          "track",
		Altitude:         100,
		AltitudeValid:    true,
		Detector:         "detector",
		Radiation:        "gamma",
		Temperature:      20,
		TemperatureValid: true,
		Humidity:         50,
		HumidityValid:    true,
		DeviceID:         "device",
		Transport:        "usb",
		DeviceName:       "name",
		Tube:             "tube",
		Country:          "DE",
	}

	t.Run("sqlite assigns id", func(t *testing.T) {
		exec := &recordingExecutor{}
		if err := db.SaveMarkerAtomic(nil, exec, marker, "sqlite"); err != nil {
			t.Fatalf("save sqlite marker: %v", err)
		}
		if !strings.Contains(exec.query, "ON CONFLICT DO NOTHING") {
			t.Fatalf("unexpected sqlite query: %s", exec.query)
		}
		if len(exec.args) != 19 {
			t.Fatalf("sqlite argument count = %d, want 19", len(exec.args))
		}
		if got, ok := exec.args[0].(int64); !ok || got != 100 {
			t.Fatalf("generated marker id = %#v, want 100", exec.args[0])
		}
	})

	t.Run("explicit id is preserved", func(t *testing.T) {
		exec := &recordingExecutor{}
		explicit := marker
		explicit.ID = 999
		if err := db.SaveMarkerAtomic(context.Background(), exec, explicit, "chai"); err != nil {
			t.Fatalf("save explicit-id marker: %v", err)
		}
		if got := exec.args[0]; got != int64(999) {
			t.Fatalf("explicit marker id = %#v, want 999", got)
		}
	})

	t.Run("pgx query", func(t *testing.T) {
		exec := &recordingExecutor{}
		if err := db.SaveMarkerAtomic(context.Background(), exec, marker, "postgresql"); err != nil {
			t.Fatalf("save pgx marker: %v", err)
		}
		if !strings.Contains(exec.query, "ON CONFLICT ON CONSTRAINT markers_unique") {
			t.Fatalf("unexpected pgx query: %s", exec.query)
		}
		if len(exec.args) != 18 {
			t.Fatalf("pgx argument count = %d, want 18", len(exec.args))
		}
	})

	t.Run("duckdb duplicate error is benign", func(t *testing.T) {
		exec := &recordingExecutor{err: errors.New("Constraint Error: duplicate key")}
		if err := db.SaveMarkerAtomic(context.Background(), exec, marker, "duckdb"); err != nil {
			t.Fatalf("duckdb duplicate should be ignored: %v", err)
		}
	})

	t.Run("ordinary error is returned", func(t *testing.T) {
		sentinel := errors.New("write failed")
		exec := &recordingExecutor{err: sentinel}
		if err := db.SaveMarkerAtomic(context.Background(), exec, marker, "sqlite"); !errors.Is(err, sentinel) {
			t.Fatalf("save error = %v, want sentinel", err)
		}
	})
}

func TestInsertRealtimeMeasurementDriverBranchesOnSQLite(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)
	measurement := RealtimeMeasurement{
		DeviceID:   "driver-branch",
		Transport:  "test",
		DeviceName: "device",
		Tube:       "tube",
		Country:    "DE",
		Value:      4.2,
		Unit:       "cpm",
		Lat:        55.7,
		Lon:        37.6,
		MeasuredAt: 100,
		FetchedAt:  101,
		Extra:      "{}",
	}

	t.Run("default duplicate", func(t *testing.T) {
		if err := db.InsertRealtimeMeasurement(measurement, "sqlite"); err != nil {
			t.Fatalf("insert sqlite realtime: %v", err)
		}
		if err := db.InsertRealtimeMeasurement(measurement, "sqlite"); err != nil {
			t.Fatalf("duplicate sqlite realtime: %v", err)
		}
		rows, err := db.fetchRealtimeByDevice(measurement.DeviceID, "sqlite")
		if err != nil {
			t.Fatalf("fetch sqlite realtime: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("duplicate realtime row count = %d, want 1", len(rows))
		}
	})

	t.Run("duckdb transaction path", func(t *testing.T) {
		duck := measurement
		duck.DeviceID = "duck-branch"
		duck.MeasuredAt = 200
		duck.FetchedAt = 201
		if err := db.InsertRealtimeMeasurement(duck, "duckdb"); err != nil {
			t.Fatalf("duckdb-style realtime on sqlite test database: %v", err)
		}
		duck.Value = 9.9
		if err := db.InsertRealtimeMeasurement(duck, "duckdb"); err != nil {
			t.Fatalf("duckdb-style replacement: %v", err)
		}
		rows, err := db.fetchRealtimeByDevice(duck.DeviceID, "sqlite")
		if err != nil {
			t.Fatalf("fetch duckdb-style realtime: %v", err)
		}
		if len(rows) != 1 || rows[0].Value != 9.9 {
			t.Fatalf("duckdb replacement rows = %#v", rows)
		}
	})

	t.Run("pgx syntax error reaches caller", func(t *testing.T) {
		pgx := measurement
		pgx.DeviceID = "pgx-branch"
		pgx.MeasuredAt = 300
		err := db.InsertRealtimeMeasurement(pgx, "pgx")
		if err == nil {
			t.Fatal("sqlite unexpectedly accepted PostgreSQL ON CONSTRAINT syntax")
		}
	})
}

func TestInsertMarkersBulkSQLiteBranches(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)

	if err := db.InsertMarkersBulk(context.Background(), nil, nil, "sqlite", 0, nil, WorkloadUserUpload); err != nil {
		t.Fatalf("empty bulk insert: %v", err)
	}

	progress := make(chan MarkerBatchProgress, 4)
	markers := []Marker{
		{DoseRate: 0.1, Date: 1, Lon: 1, Lat: 1, CountRate: 1, Zoom: 8, Speed: 1, TrackID: "a"},
		{DoseRate: 0.2, Date: 2, Lon: 2, Lat: 2, CountRate: 2, Zoom: 8, Speed: 2, TrackID: "a"},
		{DoseRate: 0.3, Date: 3, Lon: 3, Lat: 3, CountRate: 3, Zoom: 8, Speed: 3, TrackID: "b"},
	}

	if err := db.InsertMarkersBulk(nil, nil, markers, "sqlite", 2, progress, WorkloadUserUpload); err != nil {
		t.Fatalf("bulk insert markers: %v", err)
	}

	first := <-progress
	second := <-progress
	if first.Done != 2 || first.Batch != 2 || first.Mode != "bulk" {
		t.Fatalf("first progress = %#v", first)
	}
	if second.Done != 3 || second.Batch != 1 || second.Mode != "bulk" {
		t.Fatalf("second progress = %#v", second)
	}

	got, err := db.GetMarkersByZoomAndBounds(context.Background(), 8, 0, 0, 10, 10, "sqlite")
	if err != nil {
		t.Fatalf("read bulk markers: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("bulk marker count = %d, want 3", len(got))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := db.InsertMarkersBulk(ctx, nil, markers, "sqlite", 1, nil, WorkloadUserUpload); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled bulk insert error = %v, want context.Canceled", err)
	}
}

func TestMarkerUniqueOrderingBranches(t *testing.T) {
	base := Marker{DoseRate: 1, Date: 1, Lon: 1, Lat: 1, CountRate: 1, Zoom: 1, Speed: 1, TrackID: "a"}

	tests := []struct {
		name string
		edit func(*Marker)
	}{
		{"dose", func(m *Marker) { m.DoseRate = 2 }},
		{"date", func(m *Marker) { m.Date = 2 }},
		{"lon", func(m *Marker) { m.Lon = 2 }},
		{"lat", func(m *Marker) { m.Lat = 2 }},
		{"count", func(m *Marker) { m.CountRate = 2 }},
		{"zoom", func(m *Marker) { m.Zoom = 2 }},
		{"speed", func(m *Marker) { m.Speed = 2 }},
		{"track", func(m *Marker) { m.TrackID = "b" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			other := base
			test.edit(&other)
			if !lessMarkerByUniqueKey(base, other) {
				t.Fatalf("base should sort before changed marker for %s", test.name)
			}
			if lessMarkerByUniqueKey(other, base) {
				t.Fatalf("changed marker should not sort before base for %s", test.name)
			}
		})
	}

	if lessMarkerByUniqueKey(base, base) {
		t.Fatal("equal marker unexpectedly sorts before itself")
	}
}
