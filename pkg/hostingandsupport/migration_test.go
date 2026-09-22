package hostingandsupport

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestMigrateRepairsIncompleteSchemaAtCurrentVersion(t *testing.T) {
	ctx := context.Background()
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "migration.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}
	var version int
	if err := database.QueryRowContext(ctx, `SELECT version FROM schema_migrations WHERE component='billing'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != currentBillingSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, currentBillingSchemaVersion)
	}

	if _, err := database.ExecContext(ctx, `ALTER TABLE registration_confirmations RENAME TO registration_confirmations_complete`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `CREATE TABLE registration_confirmations(token TEXT PRIMARY KEY,domain TEXT,action TEXT,email TEXT,password TEXT,current_email TEXT,return_path TEXT,language_code TEXT,created_at TEXT,expires_at TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `DROP TABLE registration_confirmations_complete`); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("repair migrate: %v", err)
	}
	for _, columnName := range []string{"form_token", "verification_code", "attempts"} {
		found, err := hostingAndSupportColumnExists(ctx, database, "registration_confirmations", columnName)
		if err != nil {
			t.Fatalf("check %s: %v", columnName, err)
		}
		if !found {
			t.Fatalf("migration did not restore registration_confirmations.%s", columnName)
		}
	}
}
