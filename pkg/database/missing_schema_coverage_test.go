package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func newEmptySQLiteCoverageDatabase(t *testing.T) *Database {
	t.Helper()

	raw, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatalf("open empty sqlite database: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	raw.SetMaxOpenConns(1)
	raw.SetMaxIdleConns(1)

	return &Database{
		DB:          raw,
		Driver:      "sqlite",
		idGenerator: startIDGenerator(1),
		pipeline:    startSerializedPipeline(raw),
	}
}

func TestDatabaseMissingSchemaErrorBranches(t *testing.T) {
	ctx := context.Background()

	t.Run("metadata upgrades", func(t *testing.T) {
		db := newEmptySQLiteCoverageDatabase(t)

		if err := db.ensureMarkerMetadataColumns("sqlite", nil); err == nil {
			t.Fatal("marker metadata upgrade without table did not fail")
		}
		if err := db.ensureRealtimeMetadataColumns("sqlite", nil); err == nil {
			t.Fatal("realtime metadata upgrade without table did not fail")
		}
		if err := db.ensureAnalyticsSessionColumns("sqlite", nil); err == nil {
			t.Fatal("analytics metadata upgrade without table did not fail")
		}
	})

	t.Run("maintenance state", func(t *testing.T) {
		db := newEmptySQLiteCoverageDatabase(t)

		if _, _, err := db.getMaintenanceState(ctx, "sqlite", "missing"); err == nil {
			t.Fatal("maintenance read without table did not fail")
		}
		if err := db.setMaintenanceState(ctx, "sqlite", "missing", "running", "coverage"); err == nil {
			t.Fatal("maintenance write without table did not fail")
		}
	})

	t.Run("analytics summary", func(t *testing.T) {
		db := newEmptySQLiteCoverageDatabase(t)

		if _, err := db.QueryAnalyticsSummary(ctx, 0, 100, 10, "sqlite"); err == nil {
			t.Fatal("analytics summary without table did not fail")
		}
	})

	t.Run("track registry", func(t *testing.T) {
		db := newEmptySQLiteCoverageDatabase(t)

		if _, _, err := db.trackBackfillNeeded(ctx, "sqlite"); err == nil {
			t.Fatal("track backfill check without tables did not fail")
		}
		if err := db.backfillTracksTable(ctx, "sqlite", nil); err == nil {
			t.Fatal("track backfill without tables did not fail")
		}
		if _, err := db.CountTracks(ctx); err == nil {
			t.Fatal("track count without markers table did not fail")
		}
		if _, err := db.GetTrackSummary(ctx, "missing", "sqlite"); err == nil {
			t.Fatal("track summary without markers table did not fail")
		}
	})

	t.Run("realtime reads", func(t *testing.T) {
		db := newEmptySQLiteCoverageDatabase(t)

		if _, err := db.GetRealtimeHistory("missing", 0, "sqlite"); err == nil {
			t.Fatal("realtime history without table did not fail")
		}
		if _, err := db.fetchRealtimeByDevice("missing", "sqlite"); err == nil {
			t.Fatal("fetch realtime without table did not fail")
		}
		if err := db.PromoteStaleRealtime(100, "sqlite"); err == nil {
			t.Fatal("realtime promotion without table did not fail")
		}
	})

	t.Run("marker query", func(t *testing.T) {
		db := newEmptySQLiteCoverageDatabase(t)

		_, err := db.GetMarkersByZoomAndBounds(ctx, 8, 0, 0, 1, 1, "sqlite")
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "marker") {
			t.Fatalf("marker query without table error = %v", err)
		}
	})
	t.Run("track lookup and metadata writes", func(t *testing.T) {
		db := newEmptySQLiteCoverageDatabase(t)

		if _, err := db.TrackExists(ctx, "missing", "sqlite"); err == nil {
			t.Fatal("track existence without table did not fail")
		}
		if _, err := db.CountTrackIDsUpTo(ctx, "missing", "sqlite"); err == nil {
			t.Fatal("track id count without table did not fail")
		}
		if _, err := db.GetTrackIDByIndex(ctx, 1, "sqlite"); err == nil {
			t.Fatal("track id lookup without table did not fail")
		}
		if _, err := db.CountTracksInRange(ctx, 1, 100, "sqlite"); err == nil {
			t.Fatal("track range count without table did not fail")
		}
		if err := db.FillMissingTrackDeviceName(ctx, "missing", "meter", "sqlite"); err == nil {
			t.Fatal("fill device name without table did not fail")
		}
		if err := db.AnnotateTrackRadiationWindow(ctx, "missing", 1, 100, "gamma", "sqlite"); err == nil {
			t.Fatal("track annotation without table did not fail")
		}
		if err := db.AnnotateAreaRadiationWindow(ctx, 1, 100, 56, 38, 55, 37, "gamma", "sqlite"); err == nil {
			t.Fatal("area annotation without table did not fail")
		}
	})

	t.Run("additional stream query errors", func(t *testing.T) {
		db := newEmptySQLiteCoverageDatabase(t)

		latest, latestErrs := db.StreamLatestMarkersNear(ctx, 55.7, 37.6, 1000, 10, "sqlite")
		for range latest {
			t.Fatal("latest marker stream returned data without table")
		}
		var latestErr error
		for err := range latestErrs {
			if err != nil {
				latestErr = err
			}
		}
		if latestErr == nil {
			t.Fatal("latest marker stream did not report missing table")
		}

		ordered, orderedErrs := db.StreamMarkersByZoomBoundsSpeedOrderedByTrackDate(
			ctx, 8, 55, 37, 56, 38, 0, 0, nil, "sqlite",
		)
		for range ordered {
			t.Fatal("ordered marker stream returned data without table")
		}
		var orderedErr error
		for err := range orderedErrs {
			if err != nil {
				orderedErr = err
			}
		}
		if orderedErr == nil {
			t.Fatal("ordered marker stream did not report missing table")
		}
	})

}
