package database

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func collectMarkerStream(t *testing.T, markers <-chan Marker, errs <-chan error) []Marker {
	t.Helper()

	var out []Marker
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()

	for markers != nil || errs != nil {
		select {
		case marker, ok := <-markers:
			if !ok {
				markers = nil
				continue
			}
			out = append(out, marker)
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				t.Fatalf("marker stream failed: %v", err)
			}
		case <-timeout.C:
			t.Fatal("marker stream timed out")
		}
	}
	return out
}

func seedSerializedStreamMarkers(t *testing.T, db *Database) {
	t.Helper()

	err := db.withSerializedConnectionFor(context.Background(), WorkloadUserUpload, func(ctx context.Context, conn *sql.DB) error {
		rows := []struct {
			id        int
			dose      float64
			date      int64
			lon       float64
			lat       float64
			countRate float64
			zoom      int
			speed     float64
			trackID   string
			device    string
		}{
			{1, 0.10, 100, 37.60, 55.70, 10, 8, 5, "track-a", "detector-a"},
			{2, 0.20, 200, 37.61, 55.71, 20, 8, 25, "track-a", "detector-a"},
			{3, 0.30, 300, 37.62, 55.72, 30, 8, 55, "track-b", "detector-b"},
			{4, 0.40, 400, 10.00, 10.00, 40, 7, 90, "track-c", "detector-c"},
		}
		for _, row := range rows {
			if _, err := conn.ExecContext(ctx, `INSERT INTO markers (
id,doseRate,date,lon,lat,countRate,zoom,speed,trackID,device_name
) VALUES (?,?,?,?,?,?,?,?,?,?)`,
				row.id, row.dose, row.date, row.lon, row.lat, row.countRate, row.zoom, row.speed, row.trackID, row.device); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed stream markers: %v", err)
	}
}

func TestSerializedMarkerStreams(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)
	seedSerializedStreamMarkers(t, db)

	t.Run("zoom bounds", func(t *testing.T) {
		markers, errs := db.StreamMarkersByZoomAndBounds(context.Background(), 8, 55, 37, 56, 38, "sqlite")
		got := collectMarkerStream(t, markers, errs)
		if len(got) != 3 {
			t.Fatalf("marker count = %d, want 3", len(got))
		}
		if got[0].Date != 100 || got[2].Date != 300 {
			t.Fatalf("unexpected marker ordering: %#v", got)
		}
	})

	t.Run("continuous speed range", func(t *testing.T) {
		markers, errs := db.StreamMarkersByZoomBoundsSpeed(
			context.Background(),
			8,
			55,
			37,
			56,
			38,
			[]SpeedRange{{Min: 0, Max: 30}, {Min: 30, Max: 60}},
			"sqlite",
		)
		got := collectMarkerStream(t, markers, errs)
		if len(got) != 3 {
			t.Fatalf("continuous speed marker count = %d, want 3", len(got))
		}
	})

	t.Run("disjoint speed ranges", func(t *testing.T) {
		markers, errs := db.StreamMarkersByZoomBoundsSpeed(
			context.Background(),
			8,
			55,
			37,
			56,
			38,
			[]SpeedRange{{Min: 0, Max: 10}, {Min: 50, Max: 60}},
			"sqlite",
		)
		got := collectMarkerStream(t, markers, errs)
		if len(got) != 2 {
			t.Fatalf("disjoint speed marker count = %d, want 2", len(got))
		}
		if got[0].ID != 1 || got[1].ID != 3 {
			t.Fatalf("unexpected disjoint speed markers: %#v", got)
		}
	})

	t.Run("track bounds", func(t *testing.T) {
		markers, errs := db.StreamMarkersByTrackIDZoomAndBounds(context.Background(), "track-a", 8, 55, 37, 56, 38, "sqlite")
		got := collectMarkerStream(t, markers, errs)
		if len(got) != 2 {
			t.Fatalf("track marker count = %d, want 2", len(got))
		}
	})

	t.Run("track speed", func(t *testing.T) {
		markers, errs := db.StreamMarkersByTrackIDZoomBoundsSpeed(
			context.Background(),
			"track-a",
			8,
			55,
			37,
			56,
			38,
			[]SpeedRange{{Min: 20, Max: 30}},
			"sqlite",
		)
		got := collectMarkerStream(t, markers, errs)
		if len(got) != 1 || got[0].ID != 2 {
			t.Fatalf("track speed markers = %#v, want marker 2", got)
		}
	})
}

func TestSerializedMarkerStreamCancellation(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)
	seedSerializedStreamMarkers(t, db)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	markers, errs := db.StreamMarkersByZoomAndBounds(ctx, 8, 55, 37, 56, 38, "sqlite")
	for range markers {
		t.Fatal("cancelled marker stream returned data")
	}

	for err := range errs {
		if err == nil {
			continue
		}
		if err != context.Canceled {
			t.Fatalf("cancelled marker stream error = %v, want context.Canceled", err)
		}
	}
}
