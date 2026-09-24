package database

import (
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/matveynator/sitebrush/v2/pkg/database/drivers"
)

func TestNewDatabaseEngineBranches(t *testing.T) {
	t.Run("chai", func(t *testing.T) {
		db, err := NewDatabase(Config{DBType: "chai", DBPath: filepath.Join(t.TempDir(), "sitebrush.chai")})
		if err != nil {
			t.Fatalf("new chai database: %v", err)
		}
		t.Cleanup(func() { _ = db.DB.Close() })

		if db.Driver != "chai" || db.pipeline == nil {
			t.Fatalf("chai database driver=%q pipeline=%v", db.Driver, db.pipeline != nil)
		}
		if got := db.DB.Stats().MaxOpenConnections; got != 1 {
			t.Fatalf("chai max open connections = %d, want 1", got)
		}
		if err := db.InitSchema(Config{DBType: "chai"}, func(string, ...any) {}); err != nil {
			t.Fatalf("init chai schema: %v", err)
		}
	})

	t.Run("duckdb test driver", func(t *testing.T) {
		db, err := NewDatabase(Config{DBType: "duckdb", DBPath: filepath.Join(t.TempDir(), "sitebrush.duckdb")})
		if err != nil {
			t.Fatalf("new duckdb test database: %v", err)
		}
		t.Cleanup(func() { _ = db.DB.Close() })

		if db.Driver != "duckdb" || db.pipeline == nil || db.upkeep == nil {
			t.Fatalf("duckdb driver=%q pipeline=%v upkeep=%v", db.Driver, db.pipeline != nil, db.upkeep != nil)
		}
		if got := db.DB.Stats().MaxOpenConnections; got != 1 {
			t.Fatalf("duckdb max open connections = %d, want 1", got)
		}
	})

	t.Run("postgres connection failure", func(t *testing.T) {
		_, err := NewDatabase(Config{
			DBType:    "pgx",
			DBConn:    "postgres://invalid:invalid@127.0.0.1:1/missing?sslmode=disable&connect_timeout=1",
			PGSSLMode: "disable",
		})
		if err == nil || !strings.Contains(err.Error(), "error connecting") {
			t.Fatalf("pgx connection error = %v", err)
		}
	})

	t.Run("clickhouse connection failure", func(t *testing.T) {
		_, err := NewDatabase(Config{
			DBType: "clickhouse",
			DBConn: "clickhouse://127.0.0.1:1/missing",
		})
		if err == nil || !strings.Contains(err.Error(), "error connecting") {
			t.Fatalf("clickhouse connection error = %v", err)
		}
	})
}

func TestInitSchemaSQLiteAndChaiAreIdempotent(t *testing.T) {
	for _, dbType := range []string{"sqlite", "chai"} {
		t.Run(dbType, func(t *testing.T) {
			db, err := NewDatabase(Config{DBType: dbType, DBPath: filepath.Join(t.TempDir(), "schema.db")})
			if err != nil {
				t.Fatalf("new %s database: %v", dbType, err)
			}
			t.Cleanup(func() { _ = db.DB.Close() })

			for pass := 0; pass < 2; pass++ {
				if err := db.InitSchema(Config{DBType: dbType}, nil); err != nil {
					t.Fatalf("%s schema pass %d: %v", dbType, pass+1, err)
				}
			}
		})
	}
}
