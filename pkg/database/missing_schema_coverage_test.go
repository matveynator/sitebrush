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
}
