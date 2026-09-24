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

type realtimeFailureDriver struct{}

func (realtimeFailureDriver) Open(name string) (driver.Conn, error) {
	return &realtimeFailureConn{mode: name}, nil
}

type realtimeFailureConn struct {
	mode string
}

func (c *realtimeFailureConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *realtimeFailureConn) Close() error                        { return nil }
func (c *realtimeFailureConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *realtimeFailureConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	if c.mode == "begin-error" {
		return nil, errors.New("begin failure")
	}
	return &realtimeFailureTx{mode: c.mode}, nil
}

func (c *realtimeFailureConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	switch {
	case c.mode == "default-insert-error" && strings.Contains(query, "INSERT INTO realtime_measurements"):
		return nil, errors.New("default insert failure")
	case c.mode == "delete-error" && strings.HasPrefix(strings.TrimSpace(query), "DELETE"):
		return nil, errors.New("delete failure")
	case c.mode == "insert-conflict" && strings.Contains(query, "INSERT INTO realtime_measurements"):
		return nil, errors.New("Constraint Error: duplicate key")
	case c.mode == "insert-error" && strings.Contains(query, "INSERT INTO realtime_measurements"):
		return nil, errors.New("insert failure")
	default:
		return driver.RowsAffected(1), nil
	}
}

func (c *realtimeFailureConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if c.mode == "clickhouse-query-error" && strings.Contains(query, "realtime_measurements") {
		return nil, errors.New("clickhouse lookup failure")
	}
	if c.mode == "clickhouse-existing" && strings.Contains(query, "realtime_measurements") {
		return &realtimeFailureRows{values: [][]driver.Value{{int64(1)}}}, nil
	}
	return &realtimeFailureRows{}, nil
}

type realtimeFailureTx struct{ mode string }

func (t *realtimeFailureTx) Commit() error {
	switch t.mode {
	case "commit-conflict":
		return errors.New("Constraint Error: duplicate key")
	case "commit-error":
		return errors.New("commit failure")
	default:
		return nil
	}
}
func (t *realtimeFailureTx) Rollback() error { return nil }

func (t *realtimeFailureTx) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	switch {
	case t.mode == "delete-error" && strings.HasPrefix(strings.TrimSpace(query), "DELETE"):
		return nil, errors.New("delete failure")
	case t.mode == "insert-conflict" && strings.Contains(query, "INSERT INTO realtime_measurements"):
		return nil, errors.New("Constraint Error: duplicate key")
	case t.mode == "insert-error" && strings.Contains(query, "INSERT INTO realtime_measurements"):
		return nil, errors.New("insert failure")
	default:
		return driver.RowsAffected(1), nil
	}
}

type realtimeFailureRows struct {
	values [][]driver.Value
	index  int
}

func (r *realtimeFailureRows) Columns() []string { return []string{"value"} }
func (r *realtimeFailureRows) Close() error      { return nil }
func (r *realtimeFailureRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

func init() {
	sql.Register("sitebrush-realtime-failure", realtimeFailureDriver{})
}

func newRealtimeFailureDatabase(t *testing.T, mode, logicalDriver string) *Database {
	t.Helper()
	raw, err := sql.Open("sitebrush-realtime-failure", mode)
	if err != nil {
		t.Fatalf("open realtime failure database: %v", err)
	}
	raw.SetMaxOpenConns(1)
	raw.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	return &Database{
		DB:          raw,
		Driver:      logicalDriver,
		idGenerator: startIDGenerator(100),
		pipeline:    startSerializedPipeline(raw),
	}
}

func realtimeFailureMeasurement() RealtimeMeasurement {
	return RealtimeMeasurement{
		DeviceID: "failure-device", Transport: "test", DeviceName: "failure-meter",
		Tube: "tube", Country: "DE", Value: 42, Unit: "cpm",
		Lat: 55.7, Lon: 37.6, MeasuredAt: 100, FetchedAt: 101, Extra: "{}",
	}
}

func TestInsertRealtimeMeasurementDuckDBFailureBranches(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		wantMessage string
	}{
		{name: "begin", mode: "begin-error", wantMessage: "begin duckdb realtime tx"},
		{name: "commit ordinary", mode: "commit-error", wantMessage: "duckdb commit realtime"},
		{name: "commit conflict retries", mode: "commit-conflict", wantMessage: "duckdb realtime conflict after retries"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := newRealtimeFailureDatabase(t, test.mode, "duckdb")
			err := db.InsertRealtimeMeasurement(realtimeFailureMeasurement(), "duckdb")
			if err == nil || !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("%s error = %v, want substring %q", test.name, err, test.wantMessage)
			}
		})
	}
}

func TestInsertRealtimeMeasurementOtherFailureBranches(t *testing.T) {
	t.Run("default insert error", func(t *testing.T) {
		db := newRealtimeFailureDatabase(t, "default-insert-error", "sqlite")
		err := db.InsertRealtimeMeasurement(realtimeFailureMeasurement(), "sqlite")
		if err == nil || !strings.Contains(err.Error(), "default insert failure") {
			t.Fatalf("default insert error = %v", err)
		}
	})

	t.Run("clickhouse existing", func(t *testing.T) {
		db := newRealtimeFailureDatabase(t, "clickhouse-existing", "clickhouse")
		if err := db.InsertRealtimeMeasurement(realtimeFailureMeasurement(), "clickhouse"); err != nil {
			t.Fatalf("existing clickhouse realtime should be skipped: %v", err)
		}
	})

	t.Run("clickhouse lookup error", func(t *testing.T) {
		db := newRealtimeFailureDatabase(t, "clickhouse-query-error", "clickhouse")
		err := db.InsertRealtimeMeasurement(realtimeFailureMeasurement(), "clickhouse")
		if err == nil || !strings.Contains(err.Error(), "clickhouse lookup failure") {
			t.Fatalf("clickhouse lookup error = %v", err)
		}
	})
}
