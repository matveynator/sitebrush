package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestAnalyticsLifecycle(t *testing.T) {
	db := newTestDatabase(t)
	ctx := context.Background()
	firstSeen := time.Date(2026, time.August, 15, 10, 0, 0, 0, time.UTC).Unix()
	lastSeen := firstSeen + int64(24*time.Hour/time.Second)

	session := AnalyticsSession{
		SessionID:     "session-1",
		DisplayName:   "Alice",
		VisitorNumber: 7,
		Fingerprint:   "fingerprint-1",
		IP:            "203.0.113.10",
		UserAgent:     "SiteBrush test",
		Referer:       "https://example.com/",
		CreatedAt:     firstSeen,
		LastSeenAt:    firstSeen,
	}
	if err := db.UpsertAnalyticsSession(ctx, session, "sqlite"); err != nil {
		t.Fatalf("insert analytics session: %v", err)
	}
	session.DisplayName = ""
	session.LastSeenAt = lastSeen
	if err := db.UpsertAnalyticsSession(ctx, session, "sqlite"); err != nil {
		t.Fatalf("update analytics session: %v", err)
	}

	stored, found, err := db.AnalyticsSessionByFingerprint(ctx, session.Fingerprint, "sqlite")
	if err != nil {
		t.Fatalf("find analytics session: %v", err)
	}
	if !found || stored.SessionID != session.SessionID || stored.DisplayName != "Alice" || stored.VisitorNumber != 7 {
		t.Fatalf("stored analytics session = %+v, found=%v", stored, found)
	}
	if seed, err := db.AnalyticsVisitorSeed(ctx, "sqlite"); err != nil || seed != 7 {
		t.Fatalf("analytics visitor seed = %d, err=%v", seed, err)
	}

	events := []AnalyticsEvent{
		{SessionID: session.SessionID, DisplayName: "Alice", OccurredAt: lastSeen, Kind: "page", Path: "/", IP: session.IP, Referer: session.Referer, UserAgent: session.UserAgent, Region: "EU", Theme: "dark", Layer: "map", Speed: "walk", MapZoom: 8, CenterLat: 55.75, CenterLon: 37.61, DoseClass: "normal", TrackKind: "walk", TrackID: "track-a", Detector: "detector-a", Detail: "open"},
		{SessionID: session.SessionID, DisplayName: "", OccurredAt: lastSeen + 1, Kind: "download", Path: "/file", IP: session.IP, Referer: session.Referer, UserAgent: session.UserAgent, Region: "EU", DoseClass: "normal", TrackKind: "walk", TrackID: "track-a"},
	}
	for _, event := range events {
		if err := db.InsertAnalyticsEvent(ctx, event, "sqlite"); err != nil {
			t.Fatalf("insert analytics event: %v", err)
		}
	}

	summary, err := db.QueryAnalyticsSummary(ctx, lastSeen-1, lastSeen+10, 10, "sqlite")
	if err != nil {
		t.Fatalf("query analytics summary: %v", err)
	}
	if summary.TotalEvents != 2 || summary.UniqueSessions != 1 || len(summary.TopKinds) != 2 {
		t.Fatalf("analytics summary = %+v", summary)
	}
	if sameUTCDate(firstSeen, lastSeen) || !sameUTCDate(firstSeen, firstSeen+60) {
		t.Fatalf("sameUTCDate returned an unexpected result")
	}
}

func TestUserAndImportHistoryLifecycle(t *testing.T) {
	db := newTestDatabase(t)
	ctx := context.Background()

	if userID, err := db.EnsureUserBySource(ctx, "safecast", "external-1", "", "sqlite"); err != nil || userID == "" {
		t.Fatalf("ensure user = %q, err=%v", userID, err)
	} else {
		if err := db.UpdateUserNameIfEmpty(ctx, userID, "Alice", "sqlite"); err != nil {
			t.Fatalf("update user name: %v", err)
		}
		resolvedID, name, err := db.ResolveUserBySource(ctx, "safecast", "external-1", "sqlite")
		if err != nil || resolvedID != userID || name != "Alice" {
			t.Fatalf("resolved user = (%q, %q, %v)", resolvedID, name, err)
		}
		if err := db.EnsureTrackUser(ctx, "track-a", userID, "safecast", "sqlite"); err != nil {
			t.Fatalf("ensure track user: %v", err)
		}
	}

	if err := db.EnsureImportHistory(ctx, "safecast", "source-1", "track-a", "", "imported", "sqlite"); err != nil {
		t.Fatalf("ensure import history: %v", err)
	}
	if err := db.EnsureImportHistory(ctx, "safecast", "source-1", "track-a", "imported", "duplicate", "sqlite"); err != nil {
		t.Fatalf("repeat import history: %v", err)
	}
	record, found, err := db.FindImportHistory(ctx, "safecast", "source-1", "sqlite")
	if err != nil || !found || record.TrackID != "track-a" || record.Status != "imported" {
		t.Fatalf("import history = %+v, found=%v, err=%v", record, found, err)
	}
	if count, err := db.CountImportHistory(ctx, "safecast", "sqlite"); err != nil || count != 1 {
		t.Fatalf("import history count = %d, err=%v", count, err)
	}
	if count, latest, err := db.ImportHistoryStats(ctx, "safecast", "sqlite"); err != nil || count != 1 || latest.IsZero() {
		t.Fatalf("import history stats = (%d, %v, %v)", count, latest, err)
	}
	if sourceID, latest, err := db.LatestImportHistory(ctx, "safecast", "sqlite"); err != nil || sourceID != "source-1" || latest.IsZero() {
		t.Fatalf("latest import history = (%q, %v, %v)", sourceID, latest, err)
	}
}

func TestTrackQueriesAndMetadataLifecycle(t *testing.T) {
	db := newTestDatabase(t)
	ctx := context.Background()
	markers := []Marker{
		{ID: 10, DoseRate: 0.10, Date: 100, Lon: 37.61, Lat: 55.75, CountRate: 10, Zoom: 8, Speed: 1, TrackID: "track-a"},
		{ID: 11, DoseRate: 0.20, Date: 200, Lon: 37.62, Lat: 55.76, CountRate: 20, Zoom: 8, Speed: 2, TrackID: "track-a"},
		{ID: 12, DoseRate: 0.30, Date: 300, Lon: 37.70, Lat: 55.80, CountRate: 30, Zoom: 8, Speed: 4, TrackID: "track-b"},
	}
	for _, marker := range markers {
		insertTestMarker(t, db.DB, marker)
		if err := db.EnsureTrackPresence(ctx, marker.TrackID, "sqlite"); err != nil {
			t.Fatalf("ensure track presence: %v", err)
		}
	}

	if exists, err := db.TrackExists(ctx, "track-a", "sqlite"); err != nil || !exists {
		t.Fatalf("track exists = %v, err=%v", exists, err)
	}
	if count, err := db.CountTracksInRange(ctx, 50, 250, "sqlite"); err != nil || count != 1 {
		t.Fatalf("tracks in range = %d, err=%v", count, err)
	}
	if trackID, err := db.GetTrackIDByIndex(ctx, 2, "sqlite"); err != nil || trackID != "track-b" {
		t.Fatalf("track by index = %q, err=%v", trackID, err)
	}

	if err := db.FillMissingTrackDeviceName(ctx, "track-a", "Detector One", "sqlite"); err != nil {
		t.Fatalf("fill device name: %v", err)
	}
	if err := db.UpdateTrackDeviceName(ctx, "track-a", "Detector Two", "sqlite"); err != nil {
		t.Fatalf("update device name: %v", err)
	}
	if hasName, err := db.TrackHasDeviceName(ctx, "track-a", "sqlite"); err != nil || !hasName {
		t.Fatalf("track has device name = %v, err=%v", hasName, err)
	}
	if name, err := db.GetTrackDeviceName(ctx, "track-a", "sqlite"); err != nil || name != "Detector Two" {
		t.Fatalf("track device name = %q, err=%v", name, err)
	}
	if err := db.AnnotateTrackRadiationWindow(ctx, "track-a", 200, 100, "gamma", "sqlite"); err != nil {
		t.Fatalf("annotate track: %v", err)
	}
	if err := db.AnnotateAreaRadiationWindow(ctx, 300, 100, 56, 38, 55, 37, "beta", "sqlite"); err != nil {
		t.Fatalf("annotate area: %v", err)
	}

	markerResult, markerErr := db.GetMarkersByZoomAndBounds(ctx, 8, 55, 37, 56, 38, "sqlite")
	assertMarkerQueryCount(t, markerResult, markerErr, 3)
	markerResult, markerErr = db.GetMarkersByTrackID(ctx, "track-a", "sqlite")
	assertMarkerQueryCount(t, markerResult, markerErr, 2)
	markerResult, markerErr = db.GetMarkersByTrackIDAndBounds(ctx, "track-a", 55, 37, 56, 38, "sqlite")
	assertMarkerQueryCount(t, markerResult, markerErr, 2)
	markerResult, markerErr = db.GetMarkersByTrackIDZoomAndBounds(ctx, "track-a", 8, 55, 37, 56, 38, "sqlite")
	assertMarkerQueryCount(t, markerResult, markerErr, 2)
	markerResult, markerErr = db.GetMarkersByZoomBoundsSpeed(ctx, 8, 55, 37, 56, 38, 0, 0, []SpeedRange{{Min: 1, Max: 2}}, "sqlite")
	assertMarkerQueryCount(t, markerResult, markerErr, 2)
	markerResult, markerErr = db.GetMarkersByTrackIDZoomBoundsSpeed(ctx, "track-a", 8, 55, 37, 56, 38, 0, 0, []SpeedRange{{Min: 1, Max: 1}, {Min: 2, Max: 2}}, "sqlite")
	assertMarkerQueryCount(t, markerResult, markerErr, 2)

	markerStream, markerStreamErrors := db.StreamMarkersByZoomAndBounds(ctx, 8, 55, 37, 56, 38, "sqlite")
	assertMarkerStreamCount(t, markerStream, markerStreamErrors, 3)
	markerStream, markerStreamErrors = db.StreamMarkersByZoomBoundsSpeed(ctx, 8, 55, 37, 56, 38, []SpeedRange{{Min: 1, Max: 2}}, "sqlite")
	assertMarkerStreamCount(t, markerStream, markerStreamErrors, 2)
	markerStream, markerStreamErrors = db.StreamMarkersByTrackIDZoomAndBounds(ctx, "track-a", 8, 55, 37, 56, 38, "sqlite")
	assertMarkerStreamCount(t, markerStream, markerStreamErrors, 2)
	markerStream, markerStreamErrors = db.StreamMarkersByTrackIDZoomBoundsSpeed(ctx, "track-a", 8, 55, 37, 56, 38, []SpeedRange{{Min: 1, Max: 1}, {Min: 2, Max: 2}}, "sqlite")
	assertMarkerStreamCount(t, markerStream, markerStreamErrors, 2)
	markerStream, markerStreamErrors = db.StreamMarkersByTrackRange(ctx, "track-a", 10, 11, 10, "sqlite")
	assertMarkerStreamCount(t, markerStream, markerStreamErrors, 2)
	markerStream, markerStreamErrors = db.StreamMarkersByZoomBoundsSpeedOrderedByTrackDate(ctx, 8, 55, 37, 56, 38, 0, 0, nil, "sqlite")
	assertMarkerStreamCount(t, markerStream, markerStreamErrors, 3)

	summaries, errs := db.StreamTrackSummariesByDateRange(ctx, "", 10, 50, 250, "sqlite")
	var summaryCount int
	for range summaries {
		summaryCount++
	}
	if err := <-errs; err != nil || summaryCount != 1 {
		t.Fatalf("date summaries count = %d, err=%v", summaryCount, err)
	}
}

func TestBulkInsertIndexesAndBackfillLifecycle(t *testing.T) {
	db := newTestDatabase(t)
	ctx := context.Background()
	progress := make(chan MarkerBatchProgress, 4)
	markers := []Marker{
		{DoseRate: 0.11, Date: 100, Lon: 37.61, Lat: 55.75, CountRate: 11, Zoom: 8, Speed: 1, TrackID: "bulk-a", Altitude: 150, AltitudeValid: true, Detector: "detector-a", Temperature: 20, TemperatureValid: true},
		{DoseRate: 0.12, Date: 200, Lon: 37.62, Lat: 55.76, CountRate: 12, Zoom: 8, Speed: 2, TrackID: "bulk-a"},
		{DoseRate: 0.13, Date: 300, Lon: 37.63, Lat: 55.77, CountRate: 13, Zoom: 8, Speed: 3, TrackID: "bulk-b"},
	}
	if err := db.InsertMarkersBulk(ctx, nil, markers, "sqlite", 2, progress, WorkloadUserUpload); err != nil {
		t.Fatalf("bulk insert markers: %v", err)
	}
	if len(progress) != 2 {
		t.Fatalf("bulk progress messages = %d, want 2", len(progress))
	}
	if err := db.SaveMarkerAtomic(ctx, db.DB, Marker{DoseRate: 0.14, Date: 400, Lon: 37.64, Lat: 55.78, CountRate: 14, Zoom: 8, Speed: 4, TrackID: "bulk-c"}, "sqlite"); err != nil {
		t.Fatalf("save marker atomic: %v", err)
	}

	needed, reason, err := db.trackBackfillNeeded(ctx, "sqlite")
	if err != nil || !needed || reason != "tracks registry empty" {
		t.Fatalf("track backfill needed = (%v, %q, %v)", needed, reason, err)
	}
	if err := db.backfillTracksTable(ctx, "sqlite", nil); err != nil {
		t.Fatalf("backfill tracks: %v", err)
	}
	if err := db.backfillTracksTable(ctx, "sqlite", nil); err != nil {
		t.Fatalf("repeat track backfill: %v", err)
	}

	indexDone := db.EnsureIndexesAsync(ctx, Config{DBType: "sqlite"}, func(string, ...any) {})
	select {
	case <-indexDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("index creation did not finish")
	}
	catalog, err := db.loadIndexCatalog(ctx, "sqlite")
	if err != nil || len(catalog) == 0 {
		t.Fatalf("index catalog length = %d, err=%v", len(catalog), err)
	}
	if exists, err := db.indexExistsPortable(ctx, "sqlite", "idx_markers_trackid"); err != nil || !exists {
		t.Fatalf("marker track index exists = %v, err=%v", exists, err)
	}

	if _, err := db.DB.Exec(`UPDATE markers SET device_name = 'Detector One', tube = 'tube-a', transport = 'walk' WHERE trackID = 'bulk-a'`); err != nil {
		t.Fatalf("update marker metadata: %v", err)
	}
	summary, err := db.GetTrackDeviceSummary(ctx, "bulk-a", "sqlite")
	if err != nil || summary.DeviceName != "Detector One" || summary.Tube != "tube-a" || summary.Transport != "walk" {
		t.Fatalf("device summary = %+v, err=%v", summary, err)
	}
}

func TestRealtimeLifecycle(t *testing.T) {
	db := newTestDatabase(t)
	now := time.Now().UTC().Unix()
	SetRealtimeConverter(func(value float64, unit string) (float64, bool) {
		if unit != "cpm" {
			return 0, false
		}
		return value / 100, true
	})
	t.Cleanup(func() { SetRealtimeConverter(nil) })

	measurements := []RealtimeMeasurement{
		{DeviceID: "moving", Transport: "walk", DeviceName: "Mobile detector", Tube: "tube-a", Country: "NL", Value: 10, Unit: "cpm", Lat: 55.75, Lon: 37.61, MeasuredAt: now - 200, FetchedAt: now - 100, Extra: `{"temperature":20}`},
		{DeviceID: "moving", Transport: "walk", DeviceName: "Mobile detector", Tube: "tube-a", Country: "NL", Value: 20, Unit: "cpm", Lat: 55.76, Lon: 37.62, MeasuredAt: now - 100, FetchedAt: now - 50, Extra: `{"temperature":21}`},
		{DeviceID: "stationary", Transport: "fixed", DeviceName: "Fixed detector", Value: 30, Unit: "cpm", Lat: 55.80, Lon: 37.70, MeasuredAt: now - 50, FetchedAt: now - 25},
	}
	for _, measurement := range measurements {
		if err := db.InsertRealtimeMeasurement(measurement, "sqlite"); err != nil {
			t.Fatalf("insert realtime measurement: %v", err)
		}
	}
	if err := db.InsertRealtimeMeasurement(measurements[0], "sqlite"); err != nil {
		t.Fatalf("repeat realtime measurement: %v", err)
	}

	latest, err := db.GetLatestRealtimeByBounds(context.Background(), 55, 37, 56, 38, "sqlite")
	if err != nil || len(latest) != 2 {
		t.Fatalf("latest realtime count = %d, err=%v", len(latest), err)
	}
	history, err := db.GetRealtimeHistory("moving", now-1000, "sqlite")
	if err != nil || len(history) != 2 {
		t.Fatalf("realtime history count = %d, err=%v", len(history), err)
	}
	if err := db.PromoteStaleRealtime(now, "sqlite"); err != nil {
		t.Fatalf("promote stale realtime: %v", err)
	}
	history, err = db.GetRealtimeHistory("moving", now-1000, "sqlite")
	if err != nil || len(history) != 0 {
		t.Fatalf("promoted realtime history count = %d, err=%v", len(history), err)
	}
	markers, err := db.GetMarkersByTrackID(context.Background(), "live:moving", "sqlite")
	if err != nil || len(markers) != 2 {
		t.Fatalf("promoted marker count = %d, err=%v", len(markers), err)
	}
}

func TestDatabaseBulkPureHelpers(t *testing.T) {
	duplicate := Marker{DoseRate: 1, Date: 2, Lon: 3, Lat: 4, CountRate: 5, Zoom: 6, Speed: 7, TrackID: "track"}
	other := duplicate
	other.Date++

	if got := deduplicateMarkers([]Marker{duplicate, duplicate, other}); len(got) != 2 {
		t.Fatalf("deduplicated markers = %d, want 2", len(got))
	}
	ordered := orderDuckDBMarkers([]Marker{other, duplicate, duplicate})
	if len(ordered) != 2 || ordered[0].Date != duplicate.Date {
		t.Fatalf("ordered markers = %+v", ordered)
	}
	if trackID, ok := singleTrack([]Marker{duplicate, duplicate}); !ok || trackID != "track" {
		t.Fatalf("single track = (%q, %v)", trackID, ok)
	}
	if _, ok := singleTrack([]Marker{duplicate, Marker{TrackID: "other"}}); ok {
		t.Fatalf("different tracks reported as a single track")
	}
	if size := tuneDuckDBBatchSize(1000, 10); size != 10 {
		t.Fatalf("duckdb batch size = %d, want 10", size)
	}
	if nullableFloat64(false, 1) != nil || nullableFloat64(true, 1) != float64(1) {
		t.Fatalf("nullable float conversion failed")
	}
	if !duckDBIsConflict(errors.New("Constraint Error: duplicate key")) || duckDBIsConflict(nil) {
		t.Fatalf("duckdb conflict detection failed")
	}
	if maxInt64OrZero(sql.NullInt64{Int64: 7, Valid: true}) != 7 || maxInt64OrZero(sql.NullInt64{}) != 0 {
		t.Fatalf("nullable integer conversion failed")
	}
	if formatBytes(1024) != "1.0KB" || formatBytes(0) != "0B" {
		t.Fatalf("byte formatting failed")
	}
	if len(desiredIndexesPortable("sqlite")) == 0 {
		t.Fatalf("portable index list is empty")
	}
}

func assertMarkerQueryCount(t *testing.T, markers []Marker, err error, expected int) {
	t.Helper()
	if err != nil {
		t.Fatalf("marker query: %v", err)
	}
	if len(markers) != expected {
		t.Fatalf("marker query count = %d, want %d", len(markers), expected)
	}
}

func assertMarkerStreamCount(t *testing.T, markers <-chan Marker, errs <-chan error, expected int) {
	t.Helper()
	var count int
	for range markers {
		count++
	}
	for err := range errs {
		if err != nil {
			t.Fatalf("marker stream: %v", err)
		}
	}
	if count != expected {
		t.Fatalf("marker stream count = %d, want %d", count, expected)
	}
}
