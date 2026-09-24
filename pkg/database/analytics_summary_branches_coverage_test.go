package database

import (
	"context"
	"database/sql"
	"testing"
)

func TestAnalyticsSummaryPGXAndFallbackLabels(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)
	ctx := context.Background()

	events := []AnalyticsEvent{
		{
			SessionID: "session-blank-name", DisplayName: "", OccurredAt: 100,
			Kind: "", Region: "", DoseClass: "", TrackKind: "", Referer: "",
		},
		{
			SessionID: "session-named", DisplayName: "Alice", OccurredAt: 110,
			Kind: "view", Region: "EU", DoseClass: "normal", TrackKind: "walk", Referer: "https://example.test/",
		},
	}
	for _, event := range events {
		if err := db.InsertAnalyticsEvent(ctx, event, "sqlite"); err != nil {
			t.Fatalf("insert analytics event: %v", err)
		}
	}

	// SQLite accepts PostgreSQL-style numbered placeholders, which lets this
	// integration test cover the pgx query assembly without an external server.
	summary, err := db.QueryAnalyticsSummary(ctx, 90, 120, 10, "pgx")
	if err != nil {
		t.Fatalf("pgx-style analytics summary: %v", err)
	}
	if summary.TotalEvents != 2 || summary.UniqueSessions != 2 {
		t.Fatalf("summary totals = %#v", summary)
	}

	foundSessionFallback := false
	for _, item := range summary.TopUsers {
		if item.Label == "session-blank-name" {
			foundSessionFallback = true
		}
	}
	if !foundSessionFallback {
		t.Fatalf("blank display name did not fall back to session id: %#v", summary.TopUsers)
	}

	foundUnknownKind := false
	for _, item := range summary.TopKinds {
		if item.Label == "unknown" {
			foundUnknownKind = true
		}
	}
	if !foundUnknownKind {
		t.Fatalf("blank analytics kind did not become unknown: %#v", summary.TopKinds)
	}
}

func TestAnalyticsSummaryQueryErrorPropagation(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)
	ctx := context.Background()

	if err := db.withSerializedConnectionFor(ctx, WorkloadUserUpload, func(runCtx context.Context, conn *sql.DB) error {
		_, err := conn.ExecContext(runCtx, "DROP TABLE analytics_events")
		return err
	}); err != nil {
		t.Fatalf("drop analytics events: %v", err)
	}

	if _, err := db.QueryAnalyticsSummary(ctx, 0, 100, 10, "sqlite"); err == nil {
		t.Fatal("analytics summary unexpectedly succeeded without analytics_events table")
	}
}
