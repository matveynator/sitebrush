package database

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDatabaseConfigAndIndexBranches(t *testing.T) {
	t.Run("clickhouse dsn", func(t *testing.T) {
		if got := ClickHouseDSNFromConfig(Config{DBConn: "  clickhouse://custom:9000/db  "}); got != "clickhouse://custom:9000/db" {
			t.Fatalf("explicit clickhouse dsn = %q", got)
		}

		got := ClickHouseDSNFromConfig(Config{
			DBHost:      "",
			DBPort:      0,
			DBUser:      "user",
			DBPass:      "pass",
			DBName:      "/metrics/",
			ClickSecure: true,
		})
		if !strings.Contains(got, "127.0.0.1:9000") || !strings.Contains(got, "user:pass") || !strings.Contains(got, "/metrics") || !strings.Contains(got, "secure=true") {
			t.Fatalf("assembled clickhouse dsn = %q", got)
		}

		got = ClickHouseDSNFromConfig(Config{DBHost: "localhost:9440", DBUser: "user"})
		if !strings.Contains(got, "localhost:9440") || !strings.Contains(got, "user@") {
			t.Fatalf("host:port clickhouse dsn = %q", got)
		}
	})

	t.Run("unsupported database", func(t *testing.T) {
		if _, err := NewDatabase(Config{DBType: "unsupported"}); err == nil {
			t.Fatal("unsupported database type did not fail")
		}
	})

	t.Run("desired indexes", func(t *testing.T) {
		for _, dbType := range []string{"pgx", "duckdb", "sqlite", "chai", "unknown"} {
			indexes := desiredIndexesPortable(dbType)
			if len(indexes) == 0 {
				t.Fatalf("%s index list unexpectedly empty", dbType)
			}
			for _, index := range indexes {
				if index.name == "" || !strings.Contains(strings.ToUpper(index.sql), "INDEX") {
					t.Fatalf("%s invalid index: %#v", dbType, index)
				}
			}
		}
		if indexes := desiredIndexesPortable("clickhouse"); indexes != nil {
			t.Fatalf("clickhouse indexes = %#v, want nil", indexes)
		}
	})

	t.Run("sqlite index existence", func(t *testing.T) {
		db, _ := newSQLiteConcurrencyTestDatabase(t)
		exists, err := db.indexExistsPortable(context.Background(), "sqlite", "idx_markers_unique")
		if err != nil {
			t.Fatalf("existing sqlite index: %v", err)
		}
		if !exists {
			t.Fatal("idx_markers_unique was not found")
		}

		exists, err = db.indexExistsPortable(context.Background(), "sqlite", "idx_missing_coverage")
		if err != nil {
			t.Fatalf("missing sqlite index: %v", err)
		}
		if exists {
			t.Fatal("missing sqlite index reported as existing")
		}

		exists, err = db.indexExistsPortable(context.Background(), "unknown", "whatever")
		if err != nil || exists {
			t.Fatalf("unknown index lookup = %v, %v", exists, err)
		}
	})
}

func TestDatabaseSchemaAndMaintenanceErrorBranches(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)

	if err := db.InitSchema(Config{DBType: "unsupported"}, func(string, ...any) {}); err == nil {
		t.Fatal("unsupported schema type did not fail")
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runDuckDBMaintenance(cancelled, db.DB, nil); err != context.Canceled {
		t.Fatalf("cancelled maintenance error = %v, want context.Canceled", err)
	}

	if err := tuneDuckDBConnection(context.Background(), db.DB, func(string, ...any) {}); err != nil {
		t.Fatalf("duckdb-style tuning pragmas on sqlite test connection: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	stop := startDuckDBStartupProgress(ctx, filepath.Join(t.TempDir(), "missing.duckdb"), func(string, ...any) {})
	cancel()
	stop()
	stop()
	time.Sleep(10 * time.Millisecond)
}

func TestDuckDBPathAndSizeBranches(t *testing.T) {
	if got := duckDBFilePath("   "); got != "" {
		t.Fatalf("empty duckdb path = %q", got)
	}

	path := filepath.Join(t.TempDir(), "data.duckdb")
	got := duckDBFilePath(path + "?access_mode=read_only")
	if got == "" || strings.Contains(got, "?") {
		t.Fatalf("duckdb path with query = %q", got)
	}
	if size := duckDBFileSize(filepath.Join(t.TempDir(), "missing")); size != 0 {
		t.Fatalf("missing duckdb file size = %d, want 0", size)
	}
}
