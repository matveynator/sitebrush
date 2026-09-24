package database

import (
	"context"
	"testing"
	"time"
)

func TestDuckDBMaintenanceChannelLifecycle(t *testing.T) {
	var nilMaintenance *duckDBMaintenance
	done := nilMaintenance.enqueue(context.Background(), nil)
	if _, ok := <-done; ok {
		t.Fatal("nil maintenance channel should be closed")
	}

	var nilDB *Database
	done = nilDB.ScheduleDuckDBMaintenance(context.Background(), nil)
	if _, ok := <-done; ok {
		t.Fatal("nil database maintenance channel should be closed")
	}

	db, _ := newSQLiteConcurrencyTestDatabase(t)
	db.Driver = "sqlite"
	done = db.ScheduleDuckDBMaintenance(context.Background(), nil)
	if _, ok := <-done; ok {
		t.Fatal("non-duckdb maintenance channel should be closed")
	}

	db.Driver = "duckdb"
	db.upkeep = startDuckDBMaintenance(db)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	done = db.ScheduleDuckDBMaintenance(cancelled, nil)
	select {
	case err, ok := <-done:
		if !ok {
			t.Fatal("cancelled maintenance closed without cancellation result")
		}
		if err != context.Canceled {
			t.Fatalf("cancelled maintenance error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled maintenance did not finish")
	}

	// SQLite cannot execute the complete DuckDB maintenance sequence, but routing
	// the request through the worker still exercises the channel lifecycle and
	// error delivery path deterministically.
	done = db.ScheduleDuckDBMaintenance(nil, func(string, ...any) {})
	select {
	case err, ok := <-done:
		if !ok {
			t.Fatal("maintenance closed without result")
		}
		if err == nil {
			t.Fatal("sqlite unexpectedly completed DuckDB maintenance sequence")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("maintenance worker did not return an error")
	}
}
