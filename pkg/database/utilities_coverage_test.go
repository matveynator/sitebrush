package database

import (
	"database/sql"
	"strings"
	"testing"
)

func TestDatabaseUtilityCoverage(t *testing.T) {
	t.Run("normalize database type", func(t *testing.T) {
		cases := map[string]string{
			" PostgreSQL ":      "pgx",
			"postgres":          "pgx",
			"PQ":                "pgx",
			"postgres+psql":     "pgx",
			"postgresql+psql":   "pgx",
			" SQLite ":          "sqlite",
			"DUCKDB":            "duckdb",
		}
		for input, want := range cases {
			if got := normalizeDBType(input); got != want {
				t.Fatalf("normalizeDBType(%q) = %q, want %q", input, got, want)
			}
		}
	})

	t.Run("format bytes", func(t *testing.T) {
		cases := map[int64]string{
			0:                   "0B",
			1023:                "1023B",
			1024:                "1.0KB",
			1024 * 1024:         "1.0MB",
			1024 * 1024 * 1024:  "1.0GB",
			1024 * 1024 * 1024 * 1024: "1.0TB",
		}
		for input, want := range cases {
			if got := formatBytes(input); got != want {
				t.Fatalf("formatBytes(%d) = %q, want %q", input, got, want)
			}
		}
		if got := formatBytes(1024 * 1024 * 1024 * 1024 * 1024); !strings.HasSuffix(got, "PB") {
			t.Fatalf("petabyte formatting = %q", got)
		}
	})

	t.Run("id generator", func(t *testing.T) {
		ids := startIDGenerator(41)
		if got := <-ids; got != 41 {
			t.Fatalf("first id = %d, want 41", got)
		}
		if got := <-ids; got != 42 {
			t.Fatalf("second id = %d, want 42", got)
		}
	})

	t.Run("nullable and max helpers", func(t *testing.T) {
		if got := maxInt64OrZero(sql.NullInt64{}); got != 0 {
			t.Fatalf("invalid null int = %d, want 0", got)
		}
		if got := maxInt64OrZero(sql.NullInt64{Int64: 7, Valid: true}); got != 7 {
			t.Fatalf("valid null int = %d, want 7", got)
		}
		if got := nullableFloat64(false, 12.5); got != nil {
			t.Fatalf("invalid nullable float = %#v, want nil", got)
		}
		if got := nullableFloat64(true, 12.5); got != 12.5 {
			t.Fatalf("valid nullable float = %#v, want 12.5", got)
		}
	})

	t.Run("process read bytes", func(t *testing.T) {
		if got := processReadBytes(); got < 0 {
			t.Fatalf("processReadBytes() = %d, want non-negative", got)
		}
	})
}
