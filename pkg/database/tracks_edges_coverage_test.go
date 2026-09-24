package database

import (
	"context"
	"testing"
)

func TestTrackMetadataAndLookupBranches(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)
	seedTrackSummaryCoverageData(t, db)
	ctx := context.Background()

	if exists, err := db.TrackExists(ctx, "", "sqlite"); err != nil || exists {
		t.Fatalf("blank track exists = %v, %v", exists, err)
	}
	if exists, err := db.TrackExists(nil, "a", "sqlite"); err != nil || !exists {
		t.Fatalf("existing track = %v, %v", exists, err)
	}
	if exists, err := db.TrackExists(ctx, "missing", "sqlite"); err != nil || exists {
		t.Fatalf("missing track exists = %v, %v", exists, err)
	}

	summary, err := db.GetTrackSummary(ctx, "a", "sqlite")
	if err != nil {
		t.Fatalf("track summary: %v", err)
	}
	if summary.MarkerCount != 2 || summary.FirstID != 1 || summary.LastID != 2 {
		t.Fatalf("track summary = %#v", summary)
	}
	summary, err = db.GetTrackSummary(ctx, "missing", "sqlite")
	if err != nil {
		t.Fatalf("missing track summary: %v", err)
	}
	if summary.MarkerCount != 0 {
		t.Fatalf("missing track marker count = %d", summary.MarkerCount)
	}

	if err := db.EnsureTrackPresence(ctx, "", "sqlite"); err != nil {
		t.Fatalf("blank track presence: %v", err)
	}
	if err := db.EnsureTrackPresence(ctx, "new-track", "sqlite"); err != nil {
		t.Fatalf("ensure new track: %v", err)
	}
	if err := db.EnsureTrackPresence(ctx, "new-track", "sqlite"); err != nil {
		t.Fatalf("ensure duplicate track: %v", err)
	}

	if _, err := db.GetTrackIDByIndex(ctx, 0, "sqlite"); err == nil {
		t.Fatal("zero track index did not fail")
	}
	for index, want := range []string{"a", "b", "c"} {
		got, err := db.GetTrackIDByIndex(ctx, int64(index+1), "sqlite")
		if err != nil {
			t.Fatalf("track index %d: %v", index+1, err)
		}
		if got != want {
			t.Fatalf("track index %d = %q, want %q", index+1, got, want)
		}
	}
	got, err := db.GetTrackIDByIndex(ctx, 100, "sqlite")
	if err != nil || got != "" {
		t.Fatalf("out-of-range track index = %q, %v", got, err)
	}

	count, err := db.CountTracksInRange(ctx, 90, 250, "sqlite")
	if err != nil {
		t.Fatalf("count tracks in range: %v", err)
	}
	if count != 2 {
		t.Fatalf("tracks in range = %d, want 2", count)
	}
}

func TestTrackDeviceNameAndRadiationBranches(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)
	seedSerializedStreamMarkers(t, db)
	ctx := context.Background()

	var nilDB *Database
	if err := nilDB.UpdateTrackDeviceName(ctx, "track-a", "name", "sqlite"); err == nil {
		t.Fatal("nil database update device name did not fail")
	}
	if err := nilDB.FillMissingTrackDeviceName(ctx, "track-a", "name", "sqlite"); err == nil {
		t.Fatal("nil database fill device name did not fail")
	}

	if err := db.UpdateTrackDeviceName(ctx, "", "name", "sqlite"); err != nil {
		t.Fatalf("blank track update device name: %v", err)
	}
	if err := db.UpdateTrackDeviceName(ctx, "track-a", "", "sqlite"); err != nil {
		t.Fatalf("blank device name update: %v", err)
	}
	if err := db.UpdateTrackDeviceName(nil, "track-a", "updated", "sqlite"); err != nil {
		t.Fatalf("update device name: %v", err)
	}
	name, found, err := db.GetTrackDeviceName(ctx, "track-a", "sqlite")
	if err != nil || !found || name != "updated" {
		t.Fatalf("updated device name = %q found=%v err=%v", name, found, err)
	}
	has, err := db.TrackHasDeviceName(ctx, "track-a", "sqlite")
	if err != nil || !has {
		t.Fatalf("track has device name = %v, %v", has, err)
	}

	if err := db.FillMissingTrackDeviceName(ctx, "track-a", "replacement", "sqlite"); err != nil {
		t.Fatalf("fill existing device name: %v", err)
	}
	name, _, err = db.GetTrackDeviceName(ctx, "track-a", "sqlite")
	if err != nil || name != "updated" {
		t.Fatalf("existing device name changed to %q, err=%v", name, err)
	}

	if err := db.FillMissingTrackDeviceName(ctx, "track-b", "filled", "sqlite"); err != nil {
		t.Fatalf("fill missing device name: %v", err)
	}
	name, found, err = db.GetTrackDeviceName(ctx, "track-b", "sqlite")
	if err != nil || !found || name != "filled" {
		t.Fatalf("filled device name = %q found=%v err=%v", name, found, err)
	}

	if err := db.AnnotateTrackRadiationWindow(ctx, "", 0, 10, "gamma", "sqlite"); err != nil {
		t.Fatalf("blank track annotation: %v", err)
	}
	if err := db.AnnotateTrackRadiationWindow(ctx, "track-a", 200, 50, " gamma ", "sqlite"); err != nil {
		t.Fatalf("track radiation annotation: %v", err)
	}
	markers, err := db.GetMarkersByTrackID(ctx, "track-a", "sqlite")
	if err != nil {
		t.Fatalf("read annotated markers: %v", err)
	}
	if len(markers) != 2 || markers[0].Radiation != "gamma" || markers[1].Radiation != "gamma" {
		t.Fatalf("annotated markers = %#v", markers)
	}

	if err := db.AnnotateAreaRadiationWindow(ctx, 300, 50, 56, 38, 55, 37, "area", "sqlite"); err != nil {
		t.Fatalf("area radiation annotation: %v", err)
	}
}

func TestTrackRangeStreamBranches(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)
	seedSerializedStreamMarkers(t, db)

	markers, errs := db.StreamMarkersByTrackRange(context.Background(), "track-a", 1, 10, 1, "sqlite")
	got := collectMarkerStream(t, markers, errs)
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("limited track range = %#v", got)
	}

	markers, errs = db.StreamMarkersByTrackRange(context.Background(), "track-a", 1, 0, 0, "sqlite")
	got = collectMarkerStream(t, markers, errs)
	if len(got) != 2 {
		t.Fatalf("open-ended track range count = %d, want 2", len(got))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	markers, errs = db.StreamMarkersByTrackRange(ctx, "track-a", 1, 10, 0, "sqlite")
	for range markers {
		t.Fatal("cancelled track range returned data")
	}
	for err := range errs {
		if err != nil && err != context.Canceled {
			t.Fatalf("cancelled track range error = %v", err)
		}
	}
}
