package database

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/matveynator/sitebrush/v2/pkg/database/drivers"
)

func TestNewDatabaseDefaultPathBranches(t *testing.T) {
	originalDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("current directory: %v", err)
	}
	tempDirectory := t.TempDir()
	if err := os.Chdir(tempDirectory); err != nil {
		t.Fatalf("change to temp directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalDirectory); err != nil {
			t.Errorf("restore current directory: %v", err)
		}
	})

	for _, test := range []struct {
		dbType string
		port   int
	}{
		{dbType: "sqlite", port: 4101},
		{dbType: "chai", port: 4102},
		{dbType: "duckdb", port: 4103},
	} {
		t.Run(test.dbType, func(t *testing.T) {
			db, err := NewDatabase(Config{DBType: test.dbType, Port: test.port})
			if err != nil {
				t.Fatalf("new default-path %s database: %v", test.dbType, err)
			}
			t.Cleanup(func() { _ = db.DB.Close() })

			if db.Driver != test.dbType {
				t.Fatalf("driver = %q, want %q", db.Driver, test.dbType)
			}
			if db.pipeline == nil {
				t.Fatalf("%s default-path database has no serialized pipeline", test.dbType)
			}
			if test.dbType == "duckdb" && db.upkeep == nil {
				t.Fatal("duckdb default-path database has no maintenance worker")
			}
		})
	}

	if _, err := os.Stat(filepath.Join(tempDirectory, "database-4101.sqlite")); err != nil {
		t.Fatalf("default sqlite file was not created: %v", err)
	}
}

func TestNewDatabaseAssembledPostgresDSNFailure(t *testing.T) {
	_, err := NewDatabase(Config{
		DBType:    "pgx",
		DBHost:    "127.0.0.1",
		DBPort:    1,
		DBUser:    "invalid",
		DBPass:    "invalid",
		DBName:    "missing",
		PGSSLMode: "disable",
	})
	if err == nil || !strings.Contains(err.Error(), "error connecting") {
		t.Fatalf("assembled postgres connection error = %v", err)
	}
}
