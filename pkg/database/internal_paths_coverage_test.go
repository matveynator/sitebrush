package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestDatabaseInternalSerializedPaths(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)

	t.Run("serialized enabled", func(t *testing.T) {
		if (*Database)(nil).serializedEnabled() {
			t.Fatal("nil database unexpectedly enables serialization")
		}
		if !db.serializedEnabled() {
			t.Fatal("sqlite database must enable serialization")
		}
		plain := &Database{Driver: "pgx"}
		if plain.serializedEnabled() {
			t.Fatal("pgx must not use the single-writer serialized pipeline")
		}
	})

	t.Run("maintenance state insert and update", func(t *testing.T) {
		status, ok, err := db.getMaintenanceState(nil, "sqlite", "coverage-task")
		if err != nil {
			t.Fatalf("read missing maintenance state: %v", err)
		}
		if ok || status != "" {
			t.Fatalf("missing maintenance state = %q, %v", status, ok)
		}

		if err := db.setMaintenanceState(nil, "sqlite", "coverage-task", "running", "first"); err != nil {
			t.Fatalf("insert maintenance state: %v", err)
		}
		status, ok, err = db.getMaintenanceState(context.Background(), "sqlite", "coverage-task")
		if err != nil {
			t.Fatalf("read inserted maintenance state: %v", err)
		}
		if !ok || status != "running" {
			t.Fatalf("inserted maintenance state = %q, %v", status, ok)
		}

		if err := db.setMaintenanceState(context.Background(), "sqlite", "coverage-task", "done", "second"); err != nil {
			t.Fatalf("update maintenance state: %v", err)
		}
		status, ok, err = db.getMaintenanceState(context.Background(), "sqlite", "coverage-task")
		if err != nil {
			t.Fatalf("read updated maintenance state: %v", err)
		}
		if !ok || status != "done" {
			t.Fatalf("updated maintenance state = %q, %v", status, ok)
		}
	})

	t.Run("realtime fetch delete and movement", func(t *testing.T) {
		rows := []RealtimeMeasurement{
			{
				DeviceID:   "coverage-device",
				Transport:  "test",
				Value:      1,
				Unit:       "cpm",
				Lat:        55.7000,
				Lon:        37.6000,
				MeasuredAt: 100,
				FetchedAt:  101,
			},
			{
				DeviceID:   "coverage-device",
				Transport:  "test",
				Value:      2,
				Unit:       "cpm",
				Lat:        55.7010,
				Lon:        37.6000,
				MeasuredAt: 200,
				FetchedAt:  201,
			},
		}
		for _, row := range rows {
			if err := db.InsertRealtimeMeasurement(row, "sqlite"); err != nil {
				t.Fatalf("insert realtime measurement: %v", err)
			}
		}

		got, err := db.fetchRealtimeByDevice("coverage-device", "sqlite")
		if err != nil {
			t.Fatalf("fetch realtime rows: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("realtime rows = %d, want 2", len(got))
		}
		if !moved(got) {
			t.Fatal("movement was not detected")
		}
		if moved(nil) {
			t.Fatal("empty realtime slice unexpectedly moved")
		}
		if moved([]RealtimeMeasurement{{Lat: 1, Lon: 2}, {Lat: 1.00001, Lon: 2.00001}}) {
			t.Fatal("movement below epsilon was detected")
		}

		exec := serializedExecutor{db: db, lane: WorkloadRealtime}
		if err := db.deleteRealtimeDevice(exec, "coverage-device", "sqlite"); err != nil {
			t.Fatalf("delete realtime device: %v", err)
		}
		got, err = db.fetchRealtimeByDevice("coverage-device", "sqlite")
		if err != nil {
			t.Fatalf("fetch deleted realtime rows: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("realtime rows after delete = %d, want 0", len(got))
		}
	})
}

func TestQueueFriendlyContextBranches(t *testing.T) {
	ctx, cancel := queueFriendlyContext(nil, 20*time.Millisecond)
	if ctx == nil || cancel == nil {
		t.Fatal("nil input did not create bounded context")
	}
	cancel()

	longParent, longCancel := context.WithTimeout(context.Background(), time.Second)
	defer longCancel()
	ctx, cancel = queueFriendlyContext(longParent, 20*time.Millisecond)
	if ctx != longParent {
		t.Fatal("long existing deadline should be reused")
	}
	cancel()

	shortParent, shortCancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer shortCancel()
	ctx, cancel = queueFriendlyContext(shortParent, 20*time.Millisecond)
	defer cancel()
	if ctx == shortParent {
		t.Fatal("short deadline should be extended for queue fairness")
	}

	ctx, cancel = queueFriendlyContext(context.Background(), 0)
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("zero minimum did not apply serialized wait floor")
	}
	cancel()
}

func TestSerializedPipelineLaneFallback(t *testing.T) {
	pipeline := &serializedPipeline{
		lanes: []chan serializedJob{
			make(chan serializedJob, 1),
			make(chan serializedJob, 1),
			make(chan serializedJob, 1),
			make(chan serializedJob, 1),
			make(chan serializedJob, 1),
		},
	}

	if got := pipeline.laneFor(WorkloadWebRead); got != pipeline.lanes[WorkloadWebRead] {
		t.Fatal("known workload did not resolve to its lane")
	}
	if got := pipeline.laneFor(WorkloadKind(999)); got != pipeline.lanes[WorkloadGeneral] {
		t.Fatal("unknown workload did not fall back to general lane")
	}
}

func TestQueryMarkerHelpers(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)
	seedSerializedStreamMarkers(t, db)

	tests := []struct {
		name string
		run  func() ([]Marker, error)
		want int
	}{
		{
			name: "zoom bounds",
			run: func() ([]Marker, error) {
				return db.GetMarkersByZoomAndBounds(context.Background(), 8, 55, 37, 56, 38, "sqlite")
			},
			want: 3,
		},
		{
			name: "track",
			run: func() ([]Marker, error) {
				return db.GetMarkersByTrackID(context.Background(), "track-a", "sqlite")
			},
			want: 2,
		},
		{
			name: "track bounds",
			run: func() ([]Marker, error) {
				return db.GetMarkersByTrackIDAndBounds(context.Background(), "track-a", 55, 37, 56, 38, "sqlite")
			},
			want: 2,
		},
		{
			name: "track zoom bounds",
			run: func() ([]Marker, error) {
				return db.GetMarkersByTrackIDZoomAndBounds(context.Background(), "track-a", 8, 55, 37, 56, 38, "sqlite")
			},
			want: 2,
		},
		{
			name: "date and continuous speed",
			run: func() ([]Marker, error) {
				return db.GetMarkersByZoomBoundsSpeed(
					context.Background(), 8, 55, 37, 56, 38, 100, 250,
					[]SpeedRange{{Min: 0, Max: 10}, {Min: 10, Max: 30}}, "sqlite",
				)
			},
			want: 2,
		},
		{
			name: "disjoint speed",
			run: func() ([]Marker, error) {
				return db.GetMarkersByZoomBoundsSpeed(
					context.Background(), 8, 55, 37, 56, 38, 0, 0,
					[]SpeedRange{{Min: 0, Max: 10}, {Min: 50, Max: 60}}, "sqlite",
				)
			},
			want: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.run()
			if err != nil {
				t.Fatalf("query markers: %v", err)
			}
			if len(got) != test.want {
				t.Fatalf("marker count = %d, want %d", len(got), test.want)
			}
		})
	}
}

func TestWithSerializedConnectionErrorPaths(t *testing.T) {
	var nilDB *Database
	if err := nilDB.withSerializedConnection(context.Background(), func(context.Context, *sql.DB) error { return nil }); err == nil {
		t.Fatal("nil database unexpectedly accepted serialized work")
	}

	db := &Database{DB: &sql.DB{}, Driver: "pgx"}
	sentinel := errors.New("sentinel")
	if err := db.withSerializedConnection(context.Background(), func(context.Context, *sql.DB) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("direct non-serialized error = %v, want sentinel", err)
	}
}
