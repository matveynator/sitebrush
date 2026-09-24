package database

import (
	"context"
	"testing"
	"time"
)

func collectOrderedMarkers(t *testing.T, markers <-chan Marker, errs <-chan error) []Marker {
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
				t.Fatalf("ordered marker stream: %v", err)
			}
		case <-timeout.C:
			t.Fatal("ordered marker stream timed out")
		}
	}
	return out
}

func TestOrderedMarkerStreamBranches(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)
	seedSerializedStreamMarkers(t, db)

	t.Run("basic order", func(t *testing.T) {
		markers, errs := db.StreamMarkersByZoomBoundsSpeedOrderedByTrackDate(
			nil, 8, 55, 37, 56, 38, 0, 0, nil, "sqlite",
		)
		got := collectOrderedMarkers(t, markers, errs)
		if len(got) != 3 {
			t.Fatalf("ordered marker count = %d, want 3", len(got))
		}
		if got[0].TrackID != "track-a" || got[0].Date != 100 || got[1].TrackID != "track-a" || got[1].Date != 200 || got[2].TrackID != "track-b" {
			t.Fatalf("unexpected ordered markers: %#v", got)
		}
	})

	t.Run("date and continuous speed", func(t *testing.T) {
		markers, errs := db.StreamMarkersByZoomBoundsSpeedOrderedByTrackDate(
			context.Background(), 8, 55, 37, 56, 38, 150, 350,
			[]SpeedRange{{Min: 20, Max: 30}, {Min: 30, Max: 60}}, "sqlite",
		)
		got := collectOrderedMarkers(t, markers, errs)
		if len(got) != 2 || got[0].ID != 2 || got[1].ID != 3 {
			t.Fatalf("date/speed ordered markers = %#v", got)
		}
	})

	t.Run("disjoint speed", func(t *testing.T) {
		markers, errs := db.StreamMarkersByZoomBoundsSpeedOrderedByTrackDate(
			context.Background(), 8, 55, 37, 56, 38, 0, 0,
			[]SpeedRange{{Min: 0, Max: 10}, {Min: 50, Max: 60}}, "sqlite",
		)
		got := collectOrderedMarkers(t, markers, errs)
		if len(got) != 2 || got[0].ID != 1 || got[1].ID != 3 {
			t.Fatalf("disjoint ordered markers = %#v", got)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		markers, errs := db.StreamMarkersByZoomBoundsSpeedOrderedByTrackDate(
			ctx, 8, 55, 37, 56, 38, 0, 0, nil, "sqlite",
		)
		for range markers {
			t.Fatal("cancelled ordered stream returned a marker")
		}
		for err := range errs {
			if err != nil && err != context.Canceled {
				t.Fatalf("cancelled ordered stream error = %v", err)
			}
		}
	})
}
