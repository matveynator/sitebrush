package database

import (
	"context"
	"database/sql"
	"testing"
)

func TestImportHistoryServerDriverBranches(t *testing.T) {
	ctx := context.Background()

	t.Run("pgx placeholders", func(t *testing.T) {
		db := newSchemaCoverageDB(t, "pgx")
		if err := db.EnsureImportHistory(ctx, "source", "pgx-1", "track", "", "message", "pgx"); err != nil {
			t.Fatalf("ensure pgx import history: %v", err)
		}
		if _, _, err := db.FindImportHistory(ctx, "source", "missing", "pgx"); err != nil {
			t.Fatalf("find missing pgx import history: %v", err)
		}
	})

	t.Run("clickhouse insert and existing", func(t *testing.T) {
		db, _ := newSQLiteConcurrencyTestDatabase(t)
		if err := db.EnsureImportHistory(ctx, "click", "id-1", "track", "", "message", "clickhouse"); err != nil {
			t.Fatalf("insert clickhouse-style import history: %v", err)
		}
		if err := db.EnsureImportHistory(ctx, "click", "id-1", "other", "failed", "other", "clickhouse"); err != nil {
			t.Fatalf("repeat clickhouse-style import history: %v", err)
		}
		var count int
		if err := db.withSerializedConnectionFor(ctx, WorkloadWebRead, func(runCtx context.Context, conn *sql.DB) error {
			return conn.QueryRowContext(runCtx,
				"SELECT COUNT(*) FROM import_history WHERE source = ? AND source_id = ?",
				"click",
				"id-1",
			).Scan(&count)
		}); err != nil {
			t.Fatalf("count clickhouse-style import history: %v", err)
		}
		if count != 1 {
			t.Fatalf("clickhouse-style import count = %d, want 1", count)
		}
	})
}

func TestImportHistoryQueryErrors(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)
	ctx := context.Background()

	if err := db.withSerializedConnectionFor(ctx, WorkloadUserUpload, func(runCtx context.Context, conn *sql.DB) error {
		_, err := conn.ExecContext(runCtx, "DROP TABLE import_history")
		return err
	}); err != nil {
		t.Fatalf("drop import history: %v", err)
	}

	if _, _, err := db.FindImportHistory(ctx, "source", "id", "sqlite"); err == nil {
		t.Fatal("find import history unexpectedly succeeded without table")
	}
	if _, err := db.CountImportHistory(ctx, "source", "sqlite"); err == nil {
		t.Fatal("count import history unexpectedly succeeded without table")
	}
	if _, _, err := db.ImportHistoryStats(ctx, "source", "sqlite"); err == nil {
		t.Fatal("import history stats unexpectedly succeeded without table")
	}
	if _, _, err := db.LatestImportHistory(ctx, "source", "sqlite"); err == nil {
		t.Fatal("latest import history unexpectedly succeeded without table")
	}
	if err := db.EnsureImportHistory(ctx, "source", "id", "track", "imported", "", "sqlite"); err == nil {
		t.Fatal("ensure import history unexpectedly succeeded without table")
	}
}
