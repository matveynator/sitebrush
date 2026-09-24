package database

import (
	"context"
	"testing"
	"time"
)

func TestShortLinkServerDriverBranches(t *testing.T) {
	for _, driver := range []string{"pgx", "duckdb"} {
		t.Run(driver, func(t *testing.T) {
			db := newSchemaCoverageDB(t, driver)

			code, err := db.PersistShortLink(
				context.Background(),
				"https://example.com/"+driver,
				"AbC123",
				time.Unix(123, 0),
				8,
			)
			if err != nil {
				t.Fatalf("%s persist short link: %v", driver, err)
			}
			if code != "AbC123" {
				t.Fatalf("%s persisted code = %q, want AbC123", driver, code)
			}

			if existing, stored, err := db.PreviewShortLink(context.Background(), "https://missing.example/"+driver, 6); err != nil {
				t.Fatalf("%s preview generated code: %v", driver, err)
			} else if stored || len(existing) != 6 {
				t.Fatalf("%s preview = %q stored=%v", driver, existing, stored)
			}

			if target, err := db.ResolveShortLink(context.Background(), "Missing1"); err != nil || target != "" {
				t.Fatalf("%s missing resolve = %q, %v", driver, target, err)
			}
		})
	}
}

func TestUserServerDriverBranches(t *testing.T) {
	t.Run("pgx ensure user", func(t *testing.T) {
		db := newSchemaCoverageDB(t, "pgx")
		id, err := db.EnsureUserBySource(context.Background(), "provider", "external-pgx", "Alice", "pgx")
		if err != nil {
			t.Fatalf("pgx ensure user: %v", err)
		}
		if id == "" {
			t.Fatal("pgx ensure user returned empty id")
		}

		if err := db.EnsureTrackUser(context.Background(), "track-pgx", id, "", "pgx"); err != nil {
			t.Fatalf("pgx ensure track user: %v", err)
		}
	})

	t.Run("clickhouse ensure user", func(t *testing.T) {
		db := newSchemaCoverageDB(t, "clickhouse")
		id, err := db.EnsureUserBySource(context.Background(), "provider", "external-clickhouse", "Bob", "clickhouse")
		if err != nil {
			t.Fatalf("clickhouse ensure user: %v", err)
		}
		if id == "" {
			t.Fatal("clickhouse ensure user returned empty id")
		}

		if err := db.UpdateUserNameIfEmpty(context.Background(), id, "ignored", "clickhouse"); err != nil {
			t.Fatalf("clickhouse name update: %v", err)
		}
		if err := db.EnsureTrackUser(context.Background(), "track-clickhouse", id, "import", "clickhouse"); err != nil {
			t.Fatalf("clickhouse ensure track user: %v", err)
		}
	})
}
