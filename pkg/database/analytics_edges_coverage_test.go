package database

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestAnalyticsSessionEdgeBranches(t *testing.T) {
	ctx := context.Background()

	var nilDB *Database
	if err := nilDB.UpsertAnalyticsSession(ctx, AnalyticsSession{}, "sqlite"); err == nil {
		t.Fatal("nil analytics upsert did not fail")
	}
	if _, found, err := nilDB.AnalyticsSessionByFingerprint(ctx, "fingerprint", "sqlite"); err == nil || found {
		t.Fatal("nil analytics fingerprint lookup did not fail")
	}
	if _, err := nilDB.AnalyticsVisitorSeed(ctx, "sqlite"); err == nil {
		t.Fatal("nil analytics visitor seed did not fail")
	}
	if err := nilDB.InsertAnalyticsEvent(ctx, AnalyticsEvent{}, "sqlite"); err == nil {
		t.Fatal("nil analytics event insert did not fail")
	}
	if _, err := nilDB.QueryAnalyticsSummary(ctx, 0, 1, 10, "sqlite"); err == nil {
		t.Fatal("nil analytics summary did not fail")
	}

	db, _ := newSQLiteConcurrencyTestDatabase(t)

	if session, found, err := db.AnalyticsSessionByFingerprint(ctx, " ", "sqlite"); err != nil || found || session.SessionID != "" {
		t.Fatalf("blank fingerprint lookup = %#v found=%v err=%v", session, found, err)
	}
	if session, found, err := db.AnalyticsSessionByFingerprint(ctx, "missing", "sqlite"); err != nil || found || session.SessionID != "" {
		t.Fatalf("missing fingerprint lookup = %#v found=%v err=%v", session, found, err)
	}

	day1 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC).Unix()
	day2 := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC).Unix()

	session := AnalyticsSession{
		SessionID:     "edge-session",
		DisplayName:   "Alice",
		VisitorNumber: 0,
		Fingerprint:   "edge-fingerprint",
		LastSeenAt:    day1,
		IP:            "203.0.113.1",
	}
	if err := db.UpsertAnalyticsSession(ctx, session, "sqlite"); err != nil {
		t.Fatalf("insert analytics edge session: %v", err)
	}

	resolved, found, err := db.AnalyticsSessionByFingerprint(ctx, session.Fingerprint, "sqlite")
	if err != nil || !found || resolved.SessionID != session.SessionID {
		t.Fatalf("resolved analytics edge session = %#v found=%v err=%v", resolved, found, err)
	}

	// Blank name and visitor number must preserve existing values. A new UTC day
	// must increase visit_count.
	session.DisplayName = ""
	session.VisitorNumber = 0
	session.LastSeenAt = day2
	if err := db.UpsertAnalyticsSession(ctx, session, "sqlite"); err != nil {
		t.Fatalf("update analytics edge session: %v", err)
	}

	var name string
	var visits int
	if err := db.withSerializedConnectionFor(ctx, WorkloadWebRead, func(runCtx context.Context, conn *sql.DB) error {
		return conn.QueryRowContext(runCtx,
			"SELECT display_name, visit_count FROM analytics_sessions WHERE session_id = ?",
			session.SessionID,
		).Scan(&name, &visits)
	}); err != nil {
		t.Fatalf("read analytics edge session: %v", err)
	}
	if name != "Alice" || visits != 2 {
		t.Fatalf("analytics edge session name=%q visits=%d, want Alice/2", name, visits)
	}

	// Seed uses count when it exceeds the highest explicit visitor number.
	if err := db.UpsertAnalyticsSession(ctx, AnalyticsSession{
		SessionID:   "edge-session-2",
		Fingerprint: "edge-fingerprint-2",
		LastSeenAt:  day1,
	}, "sqlite"); err != nil {
		t.Fatalf("insert second analytics session: %v", err)
	}
	seed, err := db.AnalyticsVisitorSeed(ctx, "sqlite")
	if err != nil {
		t.Fatalf("analytics visitor seed: %v", err)
	}
	if seed != 2 {
		t.Fatalf("analytics visitor seed = %d, want count fallback 2", seed)
	}

	if err := db.InsertAnalyticsEvent(ctx, AnalyticsEvent{
		SessionID:  session.SessionID,
		OccurredAt: day2,
		Kind:       "edge",
		Region:     "EU",
		DoseClass:  "normal",
		TrackKind:  "walk",
		Referer:    "https://example.test/",
	}, "sqlite"); err != nil {
		t.Fatalf("insert analytics edge event: %v", err)
	}

	summary, err := db.QueryAnalyticsSummary(ctx, day2-1, day2+1, 10, "sqlite")
	if err != nil {
		t.Fatalf("analytics edge summary: %v", err)
	}
	if summary.TotalEvents != 1 || summary.UniqueSessions != 1 || len(summary.TopRegions) != 1 || len(summary.TopDoseClasses) != 1 || len(summary.TopTrackKinds) != 1 || len(summary.TopReferrers) != 1 {
		t.Fatalf("analytics edge summary = %#v", summary)
	}
}
