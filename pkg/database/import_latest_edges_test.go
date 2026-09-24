package database

import (
	"context"
	"testing"
)

func TestImportHistoryEdgeBranches(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)
	ctx := context.Background()

	if record, found, err := db.FindImportHistory(ctx, "", "id", "sqlite"); err != nil || found || record.Source != "" {
		t.Fatalf("blank import history lookup = %#v found=%v err=%v", record, found, err)
	}
	if record, found, err := db.FindImportHistory(ctx, "source", "missing", "sqlite"); err != nil || found || record.Source != "" {
		t.Fatalf("missing import history lookup = %#v found=%v err=%v", record, found, err)
	}
	if count, err := db.CountImportHistory(ctx, " ", "sqlite"); err != nil || count != 0 {
		t.Fatalf("blank import count = %d, %v", count, err)
	}
	if count, latest, err := db.ImportHistoryStats(ctx, "", "sqlite"); err != nil || count != 0 || !latest.IsZero() {
		t.Fatalf("blank import stats = %d %v %v", count, latest, err)
	}
	if sourceID, latest, err := db.LatestImportHistory(ctx, "", "sqlite"); err != nil || sourceID != "" || !latest.IsZero() {
		t.Fatalf("blank latest import = %q %v %v", sourceID, latest, err)
	}
	if sourceID, latest, err := db.LatestImportHistory(ctx, "missing", "sqlite"); err != nil || sourceID != "" || !latest.IsZero() {
		t.Fatalf("missing latest import = %q %v %v", sourceID, latest, err)
	}

	if err := db.EnsureImportHistory(ctx, "", "id", "track", "", "", "sqlite"); err != nil {
		t.Fatalf("blank import ensure: %v", err)
	}
	if err := db.EnsureImportHistory(ctx, "source", "", "track", "", "", "sqlite"); err != nil {
		t.Fatalf("blank source id ensure: %v", err)
	}

	if err := db.EnsureImportHistory(nil, "source", "id-1", " track ", "", " message ", "sqlite"); err != nil {
		t.Fatalf("ensure default-status import: %v", err)
	}
	record, found, err := db.FindImportHistory(ctx, "source", "id-1", "sqlite")
	if err != nil || !found {
		t.Fatalf("find default-status import = %#v found=%v err=%v", record, found, err)
	}
	if record.TrackID != "track" || record.Status != "imported" || record.Message != "message" {
		t.Fatalf("normalized import history = %#v", record)
	}

	if err := db.EnsureImportHistory(ctx, "source", "id-2", "", "failed", "", "sqlite"); err != nil {
		t.Fatalf("ensure second import history: %v", err)
	}
	count, latest, err := db.ImportHistoryStats(ctx, "source", "sqlite")
	if err != nil || count != 2 || latest.IsZero() {
		t.Fatalf("import stats = %d %v %v", count, latest, err)
	}
	sourceID, latestAt, err := db.LatestImportHistory(ctx, "source", "sqlite")
	if err != nil || sourceID == "" || latestAt.IsZero() {
		t.Fatalf("latest import = %q %v %v", sourceID, latestAt, err)
	}
}

func TestLatestMarkerStreamEdgeBranches(t *testing.T) {
	var nilDB *Database
	markers, errs := nilDB.StreamLatestMarkersNear(context.Background(), 0, 0, 100, 1, "sqlite")
	for range markers {
		t.Fatal("nil database latest stream returned marker")
	}
	if err := <-errs; err == nil {
		t.Fatal("nil database latest stream did not fail")
	}

	db, _ := newSQLiteConcurrencyTestDatabase(t)
	seedSerializedStreamMarkers(t, db)

	markers, errs = db.StreamLatestMarkersNear(context.Background(), 55.7, 37.6, 0, 1, "sqlite")
	for range markers {
		t.Fatal("zero-radius latest stream returned marker")
	}
	if err := <-errs; err == nil {
		t.Fatal("zero-radius latest stream did not fail")
	}

	markers, errs = db.StreamLatestMarkersNear(context.Background(), 55.7, 37.6, 100000, 0, "sqlite")
	got := collectMarkerStream(t, markers, errs)
	if len(got) != 1 {
		t.Fatalf("default limit marker count = %d, want 1", len(got))
	}

	markers, errs = db.StreamLatestMarkersNear(context.Background(), 90, 180, 1000000, 1000, "sqlite")
	_ = collectMarkerStream(t, markers, errs)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	markers, errs = db.StreamLatestMarkersNear(ctx, 55.7, 37.6, 100000, 10, "sqlite")
	for range markers {
		t.Fatal("cancelled latest stream returned marker")
	}
	for err := range errs {
		if err != nil && err != context.Canceled {
			t.Fatalf("cancelled latest stream error = %v", err)
		}
	}

	if got := distanceMeters(10, 20, 10, 20); got != 0 {
		t.Fatalf("same-point distance = %v, want 0", got)
	}
	if got := clampLatitude(100); got != 90 {
		t.Fatalf("clamp latitude high = %v", got)
	}
	if got := clampLatitude(-100); got != -90 {
		t.Fatalf("clamp latitude low = %v", got)
	}
	if got := clampLongitude(200); got != 180 {
		t.Fatalf("clamp longitude high = %v", got)
	}
	if got := clampLongitude(-200); got != -180 {
		t.Fatalf("clamp longitude low = %v", got)
	}

}
