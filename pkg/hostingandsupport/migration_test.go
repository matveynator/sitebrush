package hostingandsupport

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"

	"github.com/matveynator/sitebrush/v2/pkg/migrations"
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

func TestStandaloneMailHistoryUpgradesWithoutReorderingRecordedSteps(t *testing.T) {
	for _, tail := range []string{"mail-monitor-019", "mail-monitor-021"} {
		t.Run(tail, func(t *testing.T) {
			boundary := context.Background()
			database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "standalone.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			var previous []migrations.Step
			for _, step := range hostingSchemaSteps(boundary, false) {
				if strings.HasPrefix(step.ID, "account-mail-guard-") || strings.HasPrefix(step.ID, "registration-mail-cooldown-") || strings.HasPrefix(step.ID, "registration-mail-abuse-") {
					continue
				}
				previous = append(previous, step)
				if step.ID == tail {
					break
				}
			}
			if err := migrations.Run(database, "billing-schema-v27", previous); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`CREATE TABLE schema_migrations(component TEXT PRIMARY KEY,version INTEGER,updated_at TEXT)`); err != nil {
				t.Fatal(err)
			}
			if err := setSchemaMigrationVersion(boundary, database, "billing", 29); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`INSERT INTO server_settings(name,value,updated_at) VALUES('owner_domain','owner.example','')`); err != nil {
				t.Fatal(err)
			}
			if err := CheckMigrationSource(boundary, database); err != nil {
				t.Fatal("known standalone history rejected", err)
			}
			for range 2 {
				if err := Migrate(boundary, database); err != nil {
					t.Fatal("standalone upgrade failed", err)
				}
			}
			if err := CheckMigrations(boundary, database); err != nil {
				t.Fatal("upgraded readiness rejected", err)
			}
			for index, step := range previous {
				var ordinal int
				var revision string
				if err := database.QueryRow(`SELECT ordinal,revision FROM application_migration_steps WHERE component='billing-schema-v27' AND step_id=?`, step.ID).Scan(&ordinal, &revision); err != nil {
					t.Fatal(err)
				}
				if ordinal != index+1 || revision != step.Revision {
					t.Fatal("upgrade rewrote recorded history", step.ID, ordinal, revision)
				}
			}
			var owner string
			if err := database.QueryRow(`SELECT value FROM server_settings WHERE name='owner_domain'`).Scan(&owner); err != nil || owner != "owner.example" {
				t.Fatal("upgrade changed owner configuration", owner, err)
			}
			if _, err := database.Exec(`UPDATE application_migration_steps SET revision='unsupported' WHERE component='billing-schema-v27' AND step_id=?`, tail); err != nil {
				t.Fatal(err)
			}
			if err := CheckMigrationSource(boundary, database); err == nil || !strings.Contains(err.Error(), tail) || !strings.Contains(err.Error(), "billing-schema-v27") {
				t.Fatal("changed history accepted or conflicting step hidden", err)
			}
		})
	}
}
