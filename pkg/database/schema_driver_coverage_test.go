package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"strings"
	"testing"
)

type schemaCoverageDriver struct{}

func (schemaCoverageDriver) Open(string) (driver.Conn, error) {
	return schemaCoverageConn{}, nil
}

type schemaCoverageConn struct{}

func (schemaCoverageConn) Prepare(string) (driver.Stmt, error) {
	return nil, driver.ErrSkip
}

func (schemaCoverageConn) Close() error {
	return nil
}

func (schemaCoverageConn) Begin() (driver.Tx, error) {
	return nil, driver.ErrSkip
}

func (schemaCoverageConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}

func (schemaCoverageConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	lower := strings.ToLower(query)
	switch {
	case strings.Contains(lower, "pragma journal_mode"):
		return &schemaCoverageRows{
			columns: []string{"journal_mode"},
			values:  [][]driver.Value{{"wal"}},
		}, nil
	case strings.Contains(lower, "pragma table_info"):
		return &schemaCoverageRows{
			columns: []string{"cid", "name", "type", "notnull", "dflt_value", "pk"},
		}, nil
	case strings.Contains(lower, "information_schema.columns"):
		return &schemaCoverageRows{columns: []string{"column_name"}}, nil
	default:
		return &schemaCoverageRows{columns: []string{"value"}}, nil
	}
}

type schemaCoverageRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *schemaCoverageRows) Columns() []string {
	return r.columns
}

func (r *schemaCoverageRows) Close() error {
	return nil
}

func (r *schemaCoverageRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

func init() {
	sql.Register("sitebrush-schema-coverage", schemaCoverageDriver{})
	registered := false
	for _, name := range sql.Drivers() {
		if name == "duckdb" {
			registered = true
			break
		}
	}
	if !registered {
		sql.Register("duckdb", schemaCoverageDriver{})
	}
}

func newSchemaCoverageDB(t *testing.T, driverName string) *Database {
	t.Helper()

	raw, err := sql.Open("sitebrush-schema-coverage", "")
	if err != nil {
		t.Fatalf("open schema coverage database: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })

	return &Database{
		DB:          raw,
		Driver:      driverName,
		idGenerator: startIDGenerator(1),
	}
}

func TestInitSchemaEngineSwitchBranches(t *testing.T) {
	for _, dbType := range []string{"pgx", "duckdb", "clickhouse"} {
		t.Run(dbType, func(t *testing.T) {
			db := newSchemaCoverageDB(t, dbType)
			if err := db.InitSchema(Config{DBType: dbType}, func(string, ...any) {}); err != nil {
				t.Fatalf("init %s schema through coverage driver: %v", dbType, err)
			}
		})
	}
}

func TestMetadataUpgradeServerEngineBranches(t *testing.T) {
	for _, dbType := range []string{"pgx", "duckdb"} {
		t.Run(dbType, func(t *testing.T) {
			db := newSchemaCoverageDB(t, dbType)
			if err := db.ensureMarkerMetadataColumns(dbType, nil); err != nil {
				t.Fatalf("%s marker metadata: %v", dbType, err)
			}
			if err := db.ensureRealtimeMetadataColumns(dbType, nil); err != nil {
				t.Fatalf("%s realtime metadata: %v", dbType, err)
			}
			if err := db.ensureAnalyticsSessionColumns(dbType, nil); err != nil {
				t.Fatalf("%s analytics session metadata: %v", dbType, err)
			}

			present, err := db.loadColumnPresence(context.Background(), dbType, "markers")
			if err != nil {
				t.Fatalf("%s column presence: %v", dbType, err)
			}
			if len(present) != 0 {
				t.Fatalf("%s empty catalog = %#v", dbType, present)
			}
		})
	}

	db := newSchemaCoverageDB(t, "clickhouse")
	if err := db.ensureAnalyticsSessionColumns("clickhouse", nil); err != nil {
		t.Fatalf("clickhouse analytics session metadata: %v", err)
	}
}

func TestSyncPostgresSequenceBranches(t *testing.T) {
	if err := syncPostgresSequence(context.Background(), nil, "markers", "id", 1); err == nil {
		t.Fatal("nil postgres sequence database did not fail")
	}

	db := newSchemaCoverageDB(t, "pgx")
	if err := syncPostgresSequence(nil, db.DB, "markers", "id", 0); err != nil {
		t.Fatalf("sync postgres zero sequence: %v", err)
	}
	if err := syncPostgresSequence(context.Background(), db.DB, "markers", "id", 42); err != nil {
		t.Fatalf("sync postgres sequence: %v", err)
	}
}

func TestSQLiteTuningAgainstCoverageDriver(t *testing.T) {
	db := newSchemaCoverageDB(t, "sqlite")
	if err := tuneSQLiteLikeConnection(context.Background(), db.DB, func(string, ...any) {}); err != nil {
		t.Fatalf("sqlite tuning coverage driver: %v", err)
	}
}
