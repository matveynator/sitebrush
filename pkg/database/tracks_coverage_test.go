package database

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func seedTrackSummaryCoverageData(t *testing.T, db *Database) {
	t.Helper()

	err := db.withSerializedConnectionFor(context.Background(), WorkloadUserUpload, func(ctx context.Context, conn *sql.DB) error {
		rows := []struct {
			id      int
			date    int64
			trackID string
		}{
			{1, 100, "a"},
			{2, 110, "a"},
			{3, 200, "b"},
			{4, 300, "c"},
			{5, 310, "c"},
			{6, 400, "live:device"},
		}
		for _, row := range rows {
			if _, err := conn.ExecContext(ctx, `INSERT INTO markers(
id,doseRate,date,lon,lat,countRate,zoom,speed,trackID
) VALUES(?,?,?,?,?,?,?,?,?)`,
				row.id, 0.1, row.date, 37.6, 55.7, 1, 8, 1, row.trackID); err != nil {
				return err
			}
		}
		for _, trackID := range []string{"a", "b", "c", "live:device"} {
			if _, err := conn.ExecContext(ctx, "INSERT OR IGNORE INTO tracks(trackID) VALUES(?)", trackID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed track summaries: %v", err)
	}
}

func collectTrackSummaries(t *testing.T, summaries <-chan TrackSummary, errs <-chan error) []TrackSummary {
	t.Helper()

	var out []TrackSummary
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()

	for summaries != nil || errs != nil {
		select {
		case summary, ok := <-summaries:
			if !ok {
				summaries = nil
				continue
			}
			out = append(out, summary)
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				t.Fatalf("track summary stream: %v", err)
			}
		case <-timeout.C:
			t.Fatal("track summary stream timed out")
		}
	}
	return out
}

func TestTrackSummaryPaginationAndDateRange(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)
	seedTrackSummaryCoverageData(t, db)

	t.Run("first page", func(t *testing.T) {
		summaries, errs := db.StreamTrackSummaries(context.Background(), "", 2, "sqlite")
		got := collectTrackSummaries(t, summaries, errs)
		if len(got) != 2 {
			t.Fatalf("first page length = %d, want 2", len(got))
		}
		if got[0].TrackID != "a" || got[0].Index != 1 || got[1].TrackID != "b" || got[1].Index != 2 {
			t.Fatalf("unexpected first page: %#v", got)
		}
	})

	t.Run("resume page", func(t *testing.T) {
		summaries, errs := db.StreamTrackSummaries(context.Background(), "a", 10, "sqlite")
		got := collectTrackSummaries(t, summaries, errs)
		if len(got) != 2 {
			t.Fatalf("resume page length = %d, want 2", len(got))
		}
		if got[0].TrackID != "b" || got[0].Index != 2 || got[1].TrackID != "c" || got[1].Index != 3 {
			t.Fatalf("unexpected resume page: %#v", got)
		}
	})

	t.Run("date range", func(t *testing.T) {
		summaries, errs := db.StreamTrackSummariesByDateRange(context.Background(), "", 10, 150, 350, "sqlite")
		got := collectTrackSummaries(t, summaries, errs)
		if len(got) != 2 {
			t.Fatalf("date range length = %d, want 2", len(got))
		}
		if got[0].TrackID != "b" || got[1].TrackID != "c" {
			t.Fatalf("unexpected date-range summaries: %#v", got)
		}
	})

	t.Run("unlimited page", func(t *testing.T) {
		summaries, errs := db.StreamTrackSummaries(context.Background(), "", 0, "sqlite")
		got := collectTrackSummaries(t, summaries, errs)
		if len(got) != 3 {
			t.Fatalf("unlimited page length = %d, want 3", len(got))
		}
	})

	t.Run("empty page", func(t *testing.T) {
		summaries, errs := db.StreamTrackSummaries(context.Background(), "zzzz", 0, "sqlite")
		got := collectTrackSummaries(t, summaries, errs)
		if len(got) != 0 {
			t.Fatalf("empty page length = %d, want 0", len(got))
		}
	})
}

func TestTrackIndexHelpers(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)
	seedTrackSummaryCoverageData(t, db)

	count, err := db.CountTrackIDsUpTo(context.Background(), "", "sqlite")
	if err != nil {
		t.Fatalf("empty count track IDs: %v", err)
	}
	if count != 0 {
		t.Fatalf("empty count = %d, want 0", count)
	}

	count, err = db.CountTrackIDsUpTo(context.Background(), "b", "sqlite")
	if err != nil {
		t.Fatalf("count track IDs through b: %v", err)
	}
	if count != 2 {
		t.Fatalf("count through b = %d, want 2", count)
	}

	sqlitePlaceholder := newPlaceholderGenerator("sqlite")
	if sqlitePlaceholder() != "?" || sqlitePlaceholder() != "?" {
		t.Fatal("sqlite placeholder generator did not return question marks")
	}

	pgPlaceholder := newPlaceholderGenerator("pgx")
	if pgPlaceholder() != "$1" || pgPlaceholder() != "$2" || pgPlaceholder() != "$3" {
		t.Fatal("pgx placeholder generator did not increment positions")
	}
}
