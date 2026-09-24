package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"
)

type indexFailureDriver struct{}

func (indexFailureDriver) Open(name string) (driver.Conn, error) {
	return &indexFailureConn{mode: name}, nil
}

type indexFailureConn struct {
	mode       string
	lockedOnce bool
}

func (c *indexFailureConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *indexFailureConn) Close() error                        { return nil }
func (c *indexFailureConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }

func (c *indexFailureConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	if !strings.Contains(strings.ToUpper(query), "CREATE") {
		return driver.RowsAffected(1), nil
	}
	switch c.mode {
	case "locked-once":
		if !c.lockedOnce {
			c.lockedOnce = true
			return nil, errors.New("database is locked")
		}
		return driver.RowsAffected(1), nil
	case "already-exists":
		return nil, errors.New("index already exists")
	case "ddl-error":
		return nil, errors.New("ddl failure")
	default:
		return driver.RowsAffected(1), nil
	}
}

func (c *indexFailureConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return nil, errors.New("catalog unavailable")
}

func init() {
	sql.Register("sitebrush-index-failure", indexFailureDriver{})
}

func newIndexFailureDatabase(t *testing.T, mode string) *Database {
	t.Helper()
	raw, err := sql.Open("sitebrush-index-failure", mode)
	if err != nil {
		t.Fatalf("open index failure database: %v", err)
	}
	raw.SetMaxOpenConns(1)
	raw.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = raw.Close() })

	return &Database{
		DB:          raw,
		Driver:      "sqlite",
		idGenerator: startIDGenerator(1),
		pipeline:    startSerializedPipeline(raw),
	}
}

func runIndexFailureScenario(t *testing.T, mode string) []string {
	t.Helper()

	db := newIndexFailureDatabase(t, mode)
	logs := make(chan string, 256)
	done := db.EnsureIndexesAsync(context.Background(), Config{DBType: "unknown"}, func(format string, args ...any) {
		select {
		case logs <- format:
		default:
		}
	})
	if done == nil {
		t.Fatal("index builder unexpectedly disabled")
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("index builder did not finish for mode %s", mode)
	}
	close(logs)

	var out []string
	for line := range logs {
		out = append(out, line)
	}
	return out
}

func TestIndexBuilderLockRetryAndErrorBranches(t *testing.T) {
	t.Run("locked then retry", func(t *testing.T) {
		logs := runIndexFailureScenario(t, "locked-once")
		if len(logs) == 0 {
			t.Fatal("locked index builder emitted no logs")
		}
	})

	t.Run("already exists", func(t *testing.T) {
		logs := runIndexFailureScenario(t, "already-exists")
		found := false
		for _, line := range logs {
			if strings.Contains(line, "appears to exist") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("already-exists branch was not logged: %#v", logs)
		}
	})

	t.Run("ordinary ddl error", func(t *testing.T) {
		logs := runIndexFailureScenario(t, "ddl-error")
		found := false
		for _, line := range logs {
			if strings.Contains(line, "failed after") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("ordinary DDL failure branch was not logged: %#v", logs)
		}
	})
}
