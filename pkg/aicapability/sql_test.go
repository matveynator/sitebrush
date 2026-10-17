package aicapability

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func legacySQLFixture(t *testing.T) (*sql.DB, string, []byte, []byte) {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })
	if err := InitSQL(database); err != nil {
		t.Fatal(err)
	}
	capabilityID := tokenHash("legacy-grant")
	capabilities, err := json.Marshal(map[string]Capability{capabilityID: {
		ID: capabilityID, Domain: "example.org", Scopes: []string{ScopeRead}, Revoked: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	oauth, err := json.Marshal(oauthState{
		Clients: map[string]OAuthClient{"example.org\nclient": {ID: "client", Domain: "example.org", RedirectURIs: []string{"https://client.example/callback"}}},
		Refresh: map[string]oauthRefresh{tokenHash("legacy-refresh"): {
			CapabilityID: capabilityID, ClientID: "client", Domain: "example.org", Resource: "https://example.org/?mcp",
			Scopes: []string{ScopeRead}, Family: "family", Expires: time.Now().Add(time.Hour).UTC(), Used: true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "capabilities.json")
	for filename, document := range map[string][]byte{source: capabilities, source + ".oauth": oauth} {
		if err := os.WriteFile(filename, document, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return database, source, capabilities, oauth
}

func TestLegacySQLMigrationRollsBackVerificationAndJournalFailures(t *testing.T) {
	for _, scenario := range []struct{ name, trigger string }{
		{"insert", `CREATE TRIGGER reject_import BEFORE INSERT ON ai_security_state BEGIN SELECT RAISE(ABORT,'write failure'); END`},
		{"verification", `CREATE TRIGGER reject_import AFTER INSERT ON ai_security_state BEGIN UPDATE ai_security_state SET capabilities='{}' WHERE id=1; END`},
		{"journal", `CREATE TRIGGER reject_import BEFORE INSERT ON ai_file_migration BEGIN SELECT RAISE(ABORT,'journal failure'); END`},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			database, source, capabilities, oauth := legacySQLFixture(t)
			if _, err := database.Exec(`CREATE TABLE ai_file_migration(id INTEGER PRIMARY KEY)`); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(scenario.trigger); err != nil {
				t.Fatal(err)
			}
			if err := migrateStandaloneSQL(database, source); err == nil {
				t.Fatal("incomplete import reported success")
			}
			for _, table := range []string{"ai_security_state", "ai_file_migration"} {
				var count int
				if err := database.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
					t.Fatal("partial migration escaped rollback", table, count, err)
				}
			}
			for filename, document := range map[string][]byte{source: capabilities, source + ".oauth": oauth} {
				preserved, err := os.ReadFile(filename)
				if err != nil || string(preserved) != string(document) {
					t.Fatal("failed import modified a legacy source", err)
				}
			}
			if _, err := database.Exec(`DROP TRIGGER reject_import`); err != nil {
				t.Fatal(err)
			}
			if err := migrateStandaloneSQL(database, source); err != nil {
				t.Fatal("migration could not retry after rollback", err)
			}
			storedCapabilities, storedOAuth, err := LoadSQL(database)
			if err != nil {
				t.Fatal(err)
			}
			var storedGrants map[string]Capability
			var storedBindings oauthState
			var expectedBindings oauthState
			if err := json.Unmarshal(storedCapabilities, &storedGrants); err != nil || !storedGrants[tokenHash("legacy-grant")].Revoked {
				t.Fatal("retry lost durable revocation", err)
			}
			if json.Unmarshal(storedOAuth, &storedBindings) != nil || json.Unmarshal(oauth, &expectedBindings) != nil || !reflect.DeepEqual(storedBindings.Clients, expectedBindings.Clients) || !reflect.DeepEqual(storedBindings.Refresh, expectedBindings.Refresh) {
				t.Fatal("retry lost client bindings, narrowed scopes or refresh replay state")
			}
			for _, filename := range []string{source, source + ".oauth"} {
				if _, err := os.Stat(filename); !os.IsNotExist(err) {
					t.Fatal("committed migration retained a source", err)
				}
			}
			if err := migrateStandaloneSQL(database, source); err != nil {
				t.Fatal("completed migration was not idempotent", err)
			}
		})
	}
}

func TestLegacySQLMigrationRejectsCorruptSourcesAndConflictingDestinations(t *testing.T) {
	for _, scenario := range []string{"oversized", "capability directory", "OAuth directory", "existing grants", "corrupt journal"} {
		t.Run(scenario, func(t *testing.T) {
			database, source, _, _ := legacySQLFixture(t)
			switch scenario {
			case "oversized":
				if err := os.Truncate(source, (32<<20)+1); err != nil {
					t.Fatal(err)
				}
			case "capability directory", "OAuth directory":
				filename := source
				if scenario == "OAuth directory" {
					filename += ".oauth"
				}
				if err := os.Remove(filename); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filename, 0700); err != nil {
					t.Fatal(err)
				}
			case "existing grants":
				if err := SaveSQL(database, []byte(`{"newer":"revocation"}`), []byte(`{"newer":"refresh"}`)); err != nil {
					t.Fatal(err)
				}
			case "corrupt journal":
				if _, err := database.Exec(`CREATE TABLE ai_file_migration(wrong_column TEXT)`); err != nil {
					t.Fatal(err)
				}
			}
			if err := migrateStandaloneSQL(database, source); err == nil {
				t.Fatal("unsafe import reported success")
			}
			if _, err := os.Stat(source); err != nil {
				t.Fatal("failed import removed its source", err)
			}
			if scenario == "existing grants" {
				capabilities, oauth, err := LoadSQL(database)
				if err != nil || string(capabilities) != `{"newer":"revocation"}` || string(oauth) != `{"newer":"refresh"}` {
					t.Fatal("legacy import overwrote newer destination security state", err)
				}
			}
		})
	}
}

func TestSecuritySQLFailureCannotEraseThePreviousSnapshotOrLegacySources(t *testing.T) {
	database, source, capabilities, oauth := legacySQLFixture(t)
	if err := SaveSQL(database, capabilities, oauth); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TRIGGER deny_security_delete BEFORE DELETE ON ai_security_state BEGIN SELECT RAISE(ABORT,'control database unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := SaveSQL(database, []byte(`{}`), []byte(`{}`)); err == nil {
		t.Fatal("failed security deletion reported success")
	}
	storedCapabilities, storedOAuth, err := LoadSQL(database)
	if err != nil || string(storedCapabilities) != string(capabilities) || string(storedOAuth) != string(oauth) {
		t.Fatal("failed deletion lost the previous security snapshot", err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := SaveSQL(database, []byte(`{}`), []byte(`{}`)); err == nil {
		t.Fatal("closed database accepted a security write")
	}
	if err := migrateStandaloneSQL(database, source); err == nil {
		t.Fatal("closed database accepted a legacy import")
	}
	for filename, document := range map[string][]byte{source: capabilities, source + ".oauth": oauth} {
		preserved, err := os.ReadFile(filename)
		if err != nil || string(preserved) != string(document) {
			t.Fatal("unavailable database modified a legacy source", err)
		}
	}
}
