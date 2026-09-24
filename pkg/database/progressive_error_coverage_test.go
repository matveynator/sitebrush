package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func newPartialSQLiteCoverageDatabase(t *testing.T, statements ...string) *Database {
	t.Helper()

	raw, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "partial.db"))
	if err != nil {
		t.Fatalf("open partial sqlite database: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	raw.SetMaxOpenConns(1)
	raw.SetMaxIdleConns(1)

	for _, statement := range statements {
		if _, err := raw.Exec(statement); err != nil {
			t.Fatalf("create partial schema: %v", err)
		}
	}

	return &Database{
		DB:          raw,
		Driver:      "sqlite",
		idGenerator: startIDGenerator(1),
		pipeline:    startSerializedPipeline(raw),
	}
}

func TestShortLinkMissingSchemaErrorBranches(t *testing.T) {
	db := newPartialSQLiteCoverageDatabase(t)
	ctx := context.Background()

	if _, _, err := db.PreviewShortLink(ctx, "https://example.test/a", 8); err == nil {
		t.Fatal("preview without short_links table did not fail")
	}
	if _, err := db.PersistShortLink(ctx, "https://example.test/a", "", time.Now(), 8); err == nil {
		t.Fatal("persist without short_links table did not fail")
	}
	if _, err := db.ResolveShortLink(ctx, "Missing1"); err == nil {
		t.Fatal("resolve without short_links table did not fail")
	}
	if _, err := db.shortCodeExists(ctx, "Missing1"); err == nil {
		t.Fatal("shortCodeExists without short_links table did not fail")
	}
	if _, err := db.randomUnusedCode(ctx, 8); err == nil {
		t.Fatal("randomUnusedCode without short_links table did not fail")
	}
	if err := db.insertShortLink(ctx, "Code123", "https://example.test/a", time.Now()); err == nil {
		t.Fatal("insertShortLink without short_links table did not fail")
	}
}

func TestImportHistoryMissingSchemaErrorBranches(t *testing.T) {
	db := newPartialSQLiteCoverageDatabase(t)
	ctx := context.Background()

	if _, _, err := db.FindImportHistory(ctx, "source", "id", "sqlite"); err == nil {
		t.Fatal("find import history without table did not fail")
	}
	if _, err := db.CountImportHistory(ctx, "source", "sqlite"); err == nil {
		t.Fatal("count import history without table did not fail")
	}
	if _, err := db.ImportHistoryStats(ctx, "source", "sqlite"); err == nil {
		t.Fatal("import history stats without table did not fail")
	}
	if _, err := db.LatestImportHistory(ctx, "source", 10, "sqlite"); err == nil {
		t.Fatal("latest import history without table did not fail")
	}
	if err := db.EnsureImportHistory(ctx, "source", "id", "track", "done", 1, "message", "sqlite"); err == nil {
		t.Fatal("ensure import history without table did not fail")
	}
}

func TestAnalyticsSummaryProgressiveSchemaErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("unique sessions", func(t *testing.T) {
		db := newPartialSQLiteCoverageDatabase(t,
			"CREATE TABLE analytics_events (occurred_at BIGINT)",
		)
		if _, err := db.QueryAnalyticsSummary(ctx, 0, 100, 10, "sqlite"); err == nil {
			t.Fatal("summary without session_id did not fail")
		}
	})

	t.Run("top users", func(t *testing.T) {
		db := newPartialSQLiteCoverageDatabase(t,
			"CREATE TABLE analytics_events (occurred_at BIGINT, session_id TEXT)",
		)
		if _, err := db.QueryAnalyticsSummary(ctx, 0, 100, 10, "sqlite"); err == nil {
			t.Fatal("summary without display_name did not fail")
		}
	})

	t.Run("top kinds", func(t *testing.T) {
		db := newPartialSQLiteCoverageDatabase(t,
			"CREATE TABLE analytics_events (occurred_at BIGINT, session_id TEXT, display_name TEXT)",
		)
		if _, err := db.QueryAnalyticsSummary(ctx, 0, 100, 10, "sqlite"); err == nil {
			t.Fatal("summary without kind did not fail")
		}
	})

	t.Run("top regions", func(t *testing.T) {
		db := newPartialSQLiteCoverageDatabase(t,
			"CREATE TABLE analytics_events (occurred_at BIGINT, session_id TEXT, display_name TEXT, kind TEXT)",
		)
		if _, err := db.QueryAnalyticsSummary(ctx, 0, 100, 10, "sqlite"); err == nil {
			t.Fatal("summary without region did not fail")
		}
	})

	t.Run("top dose classes", func(t *testing.T) {
		db := newPartialSQLiteCoverageDatabase(t,
			"CREATE TABLE analytics_events (occurred_at BIGINT, session_id TEXT, display_name TEXT, kind TEXT, region TEXT)",
		)
		if _, err := db.QueryAnalyticsSummary(ctx, 0, 100, 10, "sqlite"); err == nil {
			t.Fatal("summary without dose_class did not fail")
		}
	})

	t.Run("top track kinds", func(t *testing.T) {
		db := newPartialSQLiteCoverageDatabase(t,
			"CREATE TABLE analytics_events (occurred_at BIGINT, session_id TEXT, display_name TEXT, kind TEXT, region TEXT, dose_class TEXT)",
		)
		if _, err := db.QueryAnalyticsSummary(ctx, 0, 100, 10, "sqlite"); err == nil {
			t.Fatal("summary without track_kind did not fail")
		}
	})

	t.Run("top referrers", func(t *testing.T) {
		db := newPartialSQLiteCoverageDatabase(t,
			"CREATE TABLE analytics_events (occurred_at BIGINT, session_id TEXT, display_name TEXT, kind TEXT, region TEXT, dose_class TEXT, track_kind TEXT)",
		)
		if _, err := db.QueryAnalyticsSummary(ctx, 0, 100, 10, "sqlite"); err == nil {
			t.Fatal("summary without referer did not fail")
		}
	})
}
