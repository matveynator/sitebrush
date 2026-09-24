package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
)

type schemaBranchDriver struct{}

func (schemaBranchDriver) Open(name string) (driver.Conn, error) {
	return &schemaBranchConn{mode: name}, nil
}

type schemaBranchConn struct {
	mode string
}

func (c *schemaBranchConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *schemaBranchConn) Close() error                        { return nil }
func (c *schemaBranchConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }

func (c *schemaBranchConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if c.mode == "query-error" {
		return nil, errors.New("catalog read failure")
	}
	if !strings.Contains(strings.ToLower(query), "information_schema.columns") {
		return &schemaBranchRows{columns: []string{"value"}}, nil
	}
	if c.mode == "present" {
		return &schemaBranchRows{
			columns: []string{"column_name"},
			values: [][]driver.Value{
				{"altitude"}, {"detector"}, {"radiation"}, {"temperature"}, {"humidity"},
				{"device_id"}, {"transport"}, {"device_name"}, {"tube"}, {"country"},
				{"extra"}, {"visitor_number"}, {"fingerprint"},
			},
		}, nil
	}
	return &schemaBranchRows{columns: []string{"column_name"}}, nil
}

func (c *schemaBranchConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	if !strings.Contains(strings.ToUpper(query), "ALTER TABLE") {
		return driver.RowsAffected(1), nil
	}
	switch c.mode {
	case "duplicate-alter":
		return nil, errors.New("duplicate column")
	case "alter-error":
		return nil, errors.New("alter failure")
	default:
		return driver.RowsAffected(1), nil
	}
}

type schemaBranchRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *schemaBranchRows) Columns() []string { return r.columns }
func (r *schemaBranchRows) Close() error      { return nil }
func (r *schemaBranchRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

func init() {
	sql.Register("sitebrush-schema-branches", schemaBranchDriver{})
}

func newSchemaBranchDatabase(t *testing.T, mode string) *Database {
	t.Helper()
	raw, err := sql.Open("sitebrush-schema-branches", mode)
	if err != nil {
		t.Fatalf("open schema branch database: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return &Database{DB: raw, Driver: "pgx", idGenerator: startIDGenerator(1)}
}

func TestMetadataUpgradeExistingServerColumns(t *testing.T) {
	db := newSchemaBranchDatabase(t, "present")
	logs := 0
	logf := func(string, ...any) { logs++ }

	if err := db.ensureMarkerMetadataColumns("pgx", logf); err != nil {
		t.Fatalf("marker existing columns: %v", err)
	}
	if err := db.ensureRealtimeMetadataColumns("pgx", logf); err != nil {
		t.Fatalf("realtime existing columns: %v", err)
	}
	if err := db.ensureAnalyticsSessionColumns("pgx", logf); err != nil {
		t.Fatalf("analytics existing columns: %v", err)
	}
	if logs == 0 {
		t.Fatal("existing-column branches emitted no logs")
	}

	present, err := db.loadColumnPresence(context.Background(), "pgx", "markers")
	if err != nil {
		t.Fatalf("load present columns: %v", err)
	}
	if !present["altitude"] || !present["device_name"] {
		t.Fatalf("present column catalog = %#v", present)
	}
}

func TestMetadataUpgradeCatalogAndAlterErrors(t *testing.T) {
	t.Run("catalog", func(t *testing.T) {
		db := newSchemaBranchDatabase(t, "query-error")
		if err := db.ensureMarkerMetadataColumns("pgx", nil); err == nil {
			t.Fatal("marker metadata catalog error was ignored")
		}
		if err := db.ensureRealtimeMetadataColumns("duckdb", nil); err == nil {
			t.Fatal("realtime metadata catalog error was ignored")
		}
		if err := db.ensureAnalyticsSessionColumns("pgx", nil); err == nil {
			t.Fatal("analytics metadata catalog error was ignored")
		}
	})

	t.Run("duplicate alter is benign", func(t *testing.T) {
		db := newSchemaBranchDatabase(t, "duplicate-alter")
		if err := db.ensureMarkerMetadataColumns("pgx", nil); err != nil {
			t.Fatalf("duplicate marker alter: %v", err)
		}
		if err := db.ensureRealtimeMetadataColumns("pgx", nil); err != nil {
			t.Fatalf("duplicate realtime alter: %v", err)
		}
		if err := db.ensureAnalyticsSessionColumns("pgx", nil); err != nil {
			t.Fatalf("duplicate analytics alter: %v", err)
		}
	})

	t.Run("ordinary alter fails", func(t *testing.T) {
		db := newSchemaBranchDatabase(t, "alter-error")
		if err := db.ensureMarkerMetadataColumns("pgx", nil); err == nil {
			t.Fatal("ordinary marker ALTER error was ignored")
		}
		if err := db.ensureRealtimeMetadataColumns("pgx", nil); err == nil {
			t.Fatal("ordinary realtime ALTER error was ignored")
		}
		if err := db.ensureAnalyticsSessionColumns("pgx", nil); err == nil {
			t.Fatal("ordinary analytics ALTER error was ignored")
		}
	})
}
