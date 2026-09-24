package database

import (
	"context"
	"database/sql"
	"testing"
)

func TestAnalyticsQueryHelpers(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)

	err := db.withSerializedConnectionFor(context.Background(), WorkloadUserUpload, func(ctx context.Context, conn *sql.DB) error {
		rows := []struct {
			sessionID   string
			displayName any
			occurredAt  int64
			kind        any
			path        any
		}{
			{"s1", "Alice", 100, "view", "/a"},
			{"s1", "Alice", 110, "view", "/b"},
			{"s2", "", 120, "click", "/a"},
			{"s3", " ", 130, "view", nil},
			{"s4", "Outside", 999, "view", "/outside"},
		}
		for _, row := range rows {
			if _, err := conn.ExecContext(ctx,
				"INSERT INTO analytics_events(session_id,display_name,occurred_at,kind,path) VALUES(?,?,?,?,?)",
				row.sessionID, row.displayName, row.occurredAt, row.kind, row.path,
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed analytics events: %v", err)
	}

	err = db.withSerializedConnectionFor(context.Background(), WorkloadWebRead, func(ctx context.Context, conn *sql.DB) error {
		count, err := queryCount(ctx, conn, "sqlite",
			"SELECT COUNT(*) FROM analytics_events WHERE occurred_at >= %s AND occurred_at < %s",
			90, 200,
		)
		if err != nil {
			return err
		}
		if count != 4 {
			t.Fatalf("analytics count = %d, want 4", count)
		}

		users, err := queryTopUsers(ctx, conn, "sqlite", 90, 200, 10)
		if err != nil {
			return err
		}
		if len(users) != 3 {
			t.Fatalf("top users length = %d, want 3", len(users))
		}
		if users[0].Label != "Alice" || users[0].Count != 2 {
			t.Fatalf("top user = %#v, want Alice ×2", users[0])
		}
		foundSessionFallback := false
		for _, user := range users {
			if user.Label == "s2" && user.Count == 1 {
				foundSessionFallback = true
			}
		}
		if !foundSessionFallback {
			t.Fatalf("top users did not fall back to session id: %#v", users)
		}

		kinds, err := queryTopList(ctx, conn, "sqlite",
			"SELECT kind, COUNT(*) FROM analytics_events WHERE occurred_at >= %s AND occurred_at < %s GROUP BY kind ORDER BY COUNT(*) DESC LIMIT %s",
			90, 200, 10,
		)
		if err != nil {
			return err
		}
		if len(kinds) != 2 {
			t.Fatalf("top kinds length = %d, want 2", len(kinds))
		}

		paths, err := queryTopList(ctx, conn, "sqlite",
			"SELECT path, COUNT(*) FROM analytics_events WHERE occurred_at >= %s AND occurred_at < %s GROUP BY path ORDER BY COUNT(*) DESC LIMIT %s",
			90, 200, 10,
		)
		if err != nil {
			return err
		}
		foundUnknown := false
		for _, path := range paths {
			if path.Label == "unknown" {
				foundUnknown = true
			}
		}
		if !foundUnknown {
			t.Fatalf("top list did not normalize NULL label: %#v", paths)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("analytics helper queries: %v", err)
	}
}

func TestAnalyticsHelperDateAndDeviceSummaryBranches(t *testing.T) {
	if !sameUTCDate(1_700_000_000, 1_700_000_100) {
		t.Fatal("timestamps from same UTC date were not recognized")
	}
	if sameUTCDate(1_700_000_000, 1_700_100_000) {
		t.Fatal("timestamps from different UTC dates were treated as equal")
	}

	var nilDB *Database
	if _, err := nilDB.GetTrackDeviceSummary(context.Background(), "missing", "sqlite"); err == nil {
		t.Fatal("nil database did not return an error")
	}

	db, _ := newSQLiteConcurrencyTestDatabase(t)
	summary, err := db.GetTrackDeviceSummary(context.Background(), "missing", "sqlite")
	if err != nil {
		t.Fatalf("missing track device summary: %v", err)
	}
	if summary != (DeviceSummary{}) {
		t.Fatalf("missing track summary = %#v, want zero value", summary)
	}

	err = db.withSerializedConnectionFor(context.Background(), WorkloadUserUpload, func(ctx context.Context, conn *sql.DB) error {
		_, err := conn.ExecContext(ctx, `INSERT INTO markers(
id,doseRate,date,lon,lat,countRate,zoom,speed,trackID,detector,device_name,tube,transport
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			1, 0.1, 100, 37.6, 55.7, 1, 8, 2, "device-track", "detector-x", "device-x", "tube-x", "usb")
		return err
	})
	if err != nil {
		t.Fatalf("seed device summary marker: %v", err)
	}

	summary, err = db.GetTrackDeviceSummary(context.Background(), "device-track", "sqlite")
	if err != nil {
		t.Fatalf("read device summary: %v", err)
	}
	if summary.Detector != "detector-x" || summary.DeviceName != "device-x" || summary.Tube != "tube-x" || summary.Transport != "usb" {
		t.Fatalf("device summary = %#v", summary)
	}
}
