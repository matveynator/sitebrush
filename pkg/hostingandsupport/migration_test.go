package hostingandsupport

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrateRepairsIncompleteSchemaAtCurrentVersion(t *testing.T) {
	ctx := context.Background()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "migration.db"))
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

func TestRegistrationClaimMigrationReadinessAndSourceValidation(t *testing.T) {
	ctx := context.Background()
	for _, damage := range []string{"missing-step", "changed-step", "missing-table", "blocked-step"} {
		t.Run(damage, func(t *testing.T) {
			db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "registration-migration.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := Migrate(ctx, db); err != nil {
				t.Fatal(err)
			}
			if err := CheckMigrations(ctx, db); err != nil {
				t.Fatal(err)
			}
			exec := func(query string) {
				t.Helper()
				if _, err := db.ExecContext(ctx, query); err != nil {
					t.Fatal(err)
				}
			}
			switch damage {
			case "missing-step":
				exec(`DELETE FROM application_migration_steps WHERE component='domain-registration-v1'`)
				if err := CheckMigrations(ctx, db); err == nil {
					t.Fatal("readiness accepted an unrecorded registration migration")
				}
				if err := CheckMigrationSource(ctx, db); err != nil {
					t.Fatalf("upgrade source refused missing migration: %v", err)
				}
				if err := Migrate(ctx, db); err != nil {
					t.Fatal(err)
				}
				if err := CheckMigrations(ctx, db); err != nil {
					t.Fatal(err)
				}
			case "changed-step":
				exec(`UPDATE application_migration_steps SET revision='future-incompatible-revision' WHERE component='domain-registration-v1'`)
				if err := CheckMigrations(ctx, db); err == nil {
					t.Fatal("readiness accepted changed registration history")
				}
				if err := CheckMigrationSource(ctx, db); err == nil {
					t.Fatal("source accepted changed registration history")
				}
			case "missing-table":
				exec(`DROP TABLE domain_registration_claims`)
				if err := CheckMigrations(ctx, db); err == nil {
					t.Fatal("readiness accepted missing claims")
				}
				if err := Migrate(ctx, db); err != nil {
					t.Fatal(err)
				}
				if err := CheckMigrations(ctx, db); err != nil {
					t.Fatal(err)
				}
			case "blocked-step":
				exec(`DROP TABLE domain_registration_claims`)
				exec(`DELETE FROM application_migration_steps WHERE component='domain-registration-v1'`)
				exec(`CREATE TRIGGER deny_registration_migration BEFORE INSERT ON application_migration_steps WHEN NEW.component='domain-registration-v1' BEGIN SELECT RAISE(ABORT,'registration migration unavailable'); END`)
				if err := Migrate(ctx, db); err == nil {
					t.Fatal("unavailable claim schema silently migrated")
				}
			}
		})
	}
}
