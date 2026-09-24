package database

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestIndexCatalogAndBuilderEdgeBranches(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)

	catalog, err := db.loadIndexCatalog(nil, "sqlite")
	if err != nil {
		t.Fatalf("sqlite index catalog: %v", err)
	}
	if len(catalog) == 0 {
		t.Fatal("sqlite index catalog is empty")
	}

	catalog, err = db.loadIndexCatalog(context.Background(), "unsupported")
	if err != nil || catalog != nil {
		t.Fatalf("unsupported index catalog = %#v, %v", catalog, err)
	}

	if _, err := db.loadIndexCatalog(context.Background(), "pgx"); err == nil {
		t.Fatal("sqlite database unexpectedly satisfied pgx index catalog query")
	}
	if _, err := db.loadIndexCatalog(context.Background(), "duckdb"); err == nil {
		t.Fatal("sqlite database unexpectedly satisfied duckdb index catalog query")
	}

	if done := db.EnsureIndexesAsync(context.Background(), Config{DBType: "clickhouse"}, func(string, ...any) {}); done != nil {
		t.Fatal("clickhouse index builder should be disabled")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := db.EnsureIndexesAsync(ctx, Config{DBType: "sqlite"}, func(string, ...any) {})
	if done == nil {
		t.Fatal("cancelled sqlite index builder unexpectedly disabled")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled index builder did not stop")
	}
}

func TestTrackBackfillEdgeBranches(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)
	ctx := context.Background()

	needed, reason, err := db.trackBackfillNeeded(ctx, "sqlite")
	if err != nil {
		t.Fatalf("empty backfill check: %v", err)
	}
	if needed || reason != "" {
		t.Fatalf("empty database backfill needed=%v reason=%q", needed, reason)
	}

	if err := db.EnsureTrackPresence(ctx, "already", "sqlite"); err != nil {
		t.Fatalf("ensure existing registry row: %v", err)
	}
	needed, reason, err = db.trackBackfillNeeded(ctx, "sqlite")
	if err != nil {
		t.Fatalf("registry-only backfill check: %v", err)
	}
	if needed || reason != "" {
		t.Fatalf("registry-only backfill needed=%v reason=%q", needed, reason)
	}

	if err := db.withSerializedConnectionFor(ctx, WorkloadUserUpload, func(runCtx context.Context, conn *sql.DB) error {
		_, err := conn.ExecContext(runCtx,
			"INSERT INTO markers(id,doseRate,date,lon,lat,countRate,zoom,speed,trackID) VALUES(?,?,?,?,?,?,?,?,?)",
			1, 0.1, 1, 1, 1, 1, 8, 1, "missing-track",
		)
		return err
	}); err != nil {
		t.Fatalf("seed missing registry marker: %v", err)
	}

	needed, reason, err = db.trackBackfillNeeded(ctx, "sqlite")
	if err != nil {
		t.Fatalf("missing registry backfill check: %v", err)
	}
	if !needed || reason == "" {
		t.Fatalf("missing registry backfill needed=%v reason=%q", needed, reason)
	}

	if err := db.backfillTracksTable(ctx, "sqlite", func(string, ...any) {}); err != nil {
		t.Fatalf("backfill missing registry row: %v", err)
	}
	exists, err := db.TrackExists(ctx, "missing-track", "sqlite")
	if err != nil || !exists {
		t.Fatalf("backfilled track exists=%v err=%v", exists, err)
	}

	if err := db.setMaintenanceState(ctx, "sqlite", maintenanceTaskTrackBackfill, "done", "coverage"); err != nil {
		t.Fatalf("set backfill maintenance state: %v", err)
	}
	if err := db.backfillTracksTable(ctx, "sqlite", func(string, ...any) {}); err != nil {
		t.Fatalf("skip completed backfill: %v", err)
	}
}
