package database

import (
	"context"
	"database/sql"
	"testing"
)

func TestSerializedStreamErrorBranches(t *testing.T) {
	t.Run("marker stream query error", func(t *testing.T) {
		db, _ := newSQLiteConcurrencyTestDatabase(t)
		if err := db.withSerializedConnectionFor(context.Background(), WorkloadGeneral, func(ctx context.Context, conn *sql.DB) error {
			_, err := conn.ExecContext(ctx, "DROP TABLE markers")
			return err
		}); err != nil {
			t.Fatalf("drop markers table: %v", err)
		}

		markers, errs := db.StreamMarkersByZoomAndBounds(context.Background(), 8, 0, 0, 1, 1, "sqlite")
		for range markers {
			t.Fatal("marker stream returned data after markers table was dropped")
		}
		var gotErr error
		for err := range errs {
			if err != nil {
				gotErr = err
			}
		}
		if gotErr == nil {
			t.Fatal("marker stream query error was not reported")
		}
	})

	t.Run("track summary query error", func(t *testing.T) {
		db, _ := newSQLiteConcurrencyTestDatabase(t)
		if err := db.withSerializedConnectionFor(context.Background(), WorkloadGeneral, func(ctx context.Context, conn *sql.DB) error {
			_, err := conn.ExecContext(ctx, "DROP TABLE markers")
			return err
		}); err != nil {
			t.Fatalf("drop markers table: %v", err)
		}

		summaries, errs := db.StreamTrackSummaries(context.Background(), "", 10, "sqlite")
		for range summaries {
			t.Fatal("track summary stream returned data after markers table was dropped")
		}
		var gotErr error
		for err := range errs {
			if err != nil {
				gotErr = err
			}
		}
		if gotErr == nil {
			t.Fatal("track summary query error was not reported")
		}
	})

	t.Run("track range query error", func(t *testing.T) {
		db, _ := newSQLiteConcurrencyTestDatabase(t)
		if err := db.withSerializedConnectionFor(context.Background(), WorkloadGeneral, func(ctx context.Context, conn *sql.DB) error {
			_, err := conn.ExecContext(ctx, "DROP TABLE markers")
			return err
		}); err != nil {
			t.Fatalf("drop markers table: %v", err)
		}

		markers, errs := db.StreamMarkersByTrackRange(context.Background(), "missing", 1, 10, 10, "sqlite")
		for range markers {
			t.Fatal("track range stream returned data after markers table was dropped")
		}
		var gotErr error
		for err := range errs {
			if err != nil {
				gotErr = err
			}
		}
		if gotErr == nil {
			t.Fatal("track range query error was not reported")
		}
	})
}
