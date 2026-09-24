package database

import (
	"context"
	"database/sql"
	"testing"
)

func TestInsertMarkersBulkCallerTransaction(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)
	ctx := context.Background()

	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin caller transaction: %v", err)
	}

	progress := make(chan MarkerBatchProgress, 2)
	markers := []Marker{
		{DoseRate: 0.1, Date: 1, Lon: 1, Lat: 1, CountRate: 1, Zoom: 8, Speed: 1, TrackID: "tx-a"},
		{DoseRate: 0.2, Date: 2, Lon: 2, Lat: 2, CountRate: 2, Zoom: 8, Speed: 2, TrackID: "tx-b"},
	}
	if err := db.InsertMarkersBulk(ctx, tx, markers, "sqlite", 1, progress, WorkloadArchive); err != nil {
		_ = tx.Rollback()
		t.Fatalf("bulk insert through caller transaction: %v", err)
	}

	var insideCount int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM markers").Scan(&insideCount); err != nil {
		_ = tx.Rollback()
		t.Fatalf("count markers inside transaction: %v", err)
	}
	if insideCount != 2 {
		_ = tx.Rollback()
		t.Fatalf("inside transaction marker count = %d, want 2", insideCount)
	}

	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback caller transaction: %v", err)
	}

	var outsideCount int
	if err := db.withSerializedConnectionFor(ctx, WorkloadWebRead, func(runCtx context.Context, conn *sql.DB) error {
		return conn.QueryRowContext(runCtx, "SELECT COUNT(*) FROM markers").Scan(&outsideCount)
	}); err != nil {
		t.Fatalf("count markers after rollback: %v", err)
	}
	if outsideCount != 0 {
		t.Fatalf("marker count after caller rollback = %d, want 0", outsideCount)
	}
	if len(progress) != 2 {
		t.Fatalf("caller transaction progress messages = %d, want 2", len(progress))
	}
}

func TestInsertMarkersBulkClickHouseMultiTrackChunkPath(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)

	markers := []Marker{
		{ID: 201, DoseRate: 0.1, Date: 1, Lon: 1, Lat: 1, CountRate: 1, Zoom: 8, Speed: 1, TrackID: "multi-a"},
		{ID: 202, DoseRate: 0.2, Date: 2, Lon: 2, Lat: 2, CountRate: 2, Zoom: 8, Speed: 2, TrackID: "multi-b"},
		{ID: 203, DoseRate: 0.3, Date: 3, Lon: 3, Lat: 3, CountRate: 3, Zoom: 8, Speed: 3, TrackID: "multi-c"},
	}
	progress := make(chan MarkerBatchProgress, 4)
	if err := db.InsertMarkersBulk(context.Background(), nil, markers, "clickhouse", 2, progress, WorkloadArchive); err != nil {
		t.Fatalf("clickhouse-style multi-track bulk: %v", err)
	}

	var count int
	if err := db.withSerializedConnectionFor(context.Background(), WorkloadWebRead, func(ctx context.Context, conn *sql.DB) error {
		return conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM markers WHERE trackID LIKE 'multi-%'").Scan(&count)
	}); err != nil {
		t.Fatalf("count multi-track markers: %v", err)
	}
	if count != 3 {
		t.Fatalf("multi-track marker count = %d, want 3", count)
	}
	if len(progress) != 2 {
		t.Fatalf("multi-track progress messages = %d, want 2", len(progress))
	}
}
