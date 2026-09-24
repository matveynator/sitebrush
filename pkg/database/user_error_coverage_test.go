package database

import (
	"context"
	"database/sql"
	"testing"
)

func TestUserOwnershipMissingSchemaErrors(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)
	ctx := context.Background()

	if err := db.withSerializedConnectionFor(ctx, WorkloadUserUpload, func(runCtx context.Context, conn *sql.DB) error {
		if _, err := conn.ExecContext(runCtx, "DROP TABLE track_users"); err != nil {
			return err
		}
		_, err := conn.ExecContext(runCtx, "DROP TABLE users")
		return err
	}); err != nil {
		t.Fatalf("drop user tables: %v", err)
	}

	if _, _, err := db.ResolveUserBySource(ctx, "provider", "external", "sqlite"); err == nil {
		t.Fatal("resolve user unexpectedly succeeded without users table")
	}
	if _, err := db.EnsureUserBySource(ctx, "provider", "external", "Alice", "sqlite"); err == nil {
		t.Fatal("ensure user unexpectedly succeeded without users table")
	}
	if err := db.UpdateUserNameIfEmpty(ctx, "user", "Alice", "sqlite"); err == nil {
		t.Fatal("update user name unexpectedly succeeded without users table")
	}
	if err := db.EnsureTrackUser(ctx, "track", "user", "source", "sqlite"); err == nil {
		t.Fatal("ensure track user unexpectedly succeeded without track_users table")
	}
}
