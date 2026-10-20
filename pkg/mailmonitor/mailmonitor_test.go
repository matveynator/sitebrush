package mailmonitor

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestMailActivitySurvivesSyncAndKeepsServerBoundaries(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "monitor.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	for _, query := range SchemaQueries() {
		if _, err := database.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	store := Store{DB: database}
	ctx := context.Background()
	now := time.Now().UTC()
	queued := Event{ID: "queued-1", MessageID: "mail-1", Kind: "email_confirm", Stage: "queued", Domain: "site.example", Recipient: "owner@example.com", Route: "local", CreatedAt: now.Format(time.RFC3339Nano)}
	accepted := Event{ID: "accepted-1", MessageID: "mail-1", Kind: "email_confirm", Stage: "accepted", Domain: "site.example", Recipient: "owner@example.com", AcceptedBy: "recipient_smtp", Attempt: 2, CreatedAt: now.Add(time.Second).Format(time.RFC3339Nano)}
	for _, event := range []Event{queued, accepted} {
		if err := store.Append(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	unsynced, err := store.Unsynced(ctx, 80)
	if err != nil || len(unsynced) != 2 {
		t.Fatalf("unsynced events = %d, %v", len(unsynced), err)
	}
	if err := store.Import(ctx, "remote-server", unsynced); err != nil {
		t.Fatal(err)
	}
	if err := store.Import(ctx, "remote-server", unsynced); err != nil {
		t.Fatal(err)
	}
	// A remote server uses the same immutable event IDs; replay must not add counts.
	counts, err := store.CountsSince(ctx, "remote-server", now.Add(-time.Hour))
	if err != nil || counts.Queued != 1 || counts.Accepted != 1 {
		t.Fatalf("remote events were lost or duplicated: %+v, %v", counts, err)
	}
	if err := store.MarkSynced(ctx, unsynced); err != nil {
		t.Fatal(err)
	}
	unsynced, err = store.Unsynced(ctx, 80)
	if err != nil || len(unsynced) != 0 {
		t.Fatalf("events stayed unsynced: %d, %v", len(unsynced), err)
	}
	if err := store.Purge(ctx, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	localEvents, err := store.Recent(ctx, "local", now.Add(-time.Hour), 50, 0)
	if err != nil || len(localEvents) != 0 {
		t.Fatalf("old events remained: %d, %v", len(localEvents), err)
	}
}

func TestSafeErrorRemovesSecretsAndControls(t *testing.T) {
	detail := SafeError(errors.New("SMTP 550 token=abc\nhttps://example.com/reset?code=secret rejected"))
	if strings.Contains(detail, "abc") || strings.Contains(detail, "secret") || strings.Contains(detail, "https://") || strings.Contains(detail, "\n") || !strings.Contains(detail, "550") {
		t.Fatalf("unsafe error detail: %q", detail)
	}
}

func TestJournalFiltersRetentionAndFlowReplay(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, statement := range SchemaQueries() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	store := Store{DB: database}
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	events := []Event{
		{MessageID: "one", FlowID: "flow", Kind: "email_confirm", Stage: "queued", Domain: "customer.example", Recipient: "literal_%@example.com", CreatedAt: now.Format(time.RFC3339)},
		{MessageID: "one", Kind: "email_confirm", Stage: "accepted", Recipient: "literal_%@example.com", Attempt: 1, CreatedAt: now.Add(time.Millisecond).Format(time.RFC3339Nano)},
		{MessageID: "one", Kind: "email_confirm", Stage: "accepted", Attempt: 2, CreatedAt: now.Add(2 * time.Millisecond).Format(time.RFC3339Nano)},
		{FlowID: "flow", Kind: "registration", Stage: "registration_complete", CreatedAt: now.Format(time.RFC3339)},
		{FlowID: "flow", Kind: "registration", Stage: "registration_complete", CreatedAt: now.Format(time.RFC3339)},
		{MessageID: "old", Kind: "invoice", Stage: "queued", CreatedAt: now.Add(-Retention - time.Nanosecond).Format(time.RFC3339Nano)},
		{MessageID: "boundary", Kind: "invoice", Stage: "queued", CreatedAt: now.Add(-Retention).Format(time.RFC3339Nano)},
	}
	for _, event := range events {
		if err := store.Append(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	counts, err := store.CountsSince(ctx, "local", now)
	if err != nil || counts.Accepted != 1 || counts.RegistrationComplete != 1 {
		t.Fatalf("replay or date boundary inflated counts: %+v %v", counts, err)
	}
	filtered, err := store.RecentFiltered(ctx, Filter{InstallationID: "local", Since: now, Stage: "accepted", Kind: "email_confirm", Recipient: "_%@EXAMPLE.COM"})
	if err != nil || len(filtered) != 1 || filtered[0].FlowID != "flow" || filtered[0].Domain != "customer.example" {
		t.Fatalf("filter/flow metadata: %+v %v", filtered, err)
	}
	other, err := store.Recent(ctx, "other", now.Add(-Retention), 50, 0)
	if err != nil || len(other) != 0 {
		t.Fatalf("cross-server leak: %+v %v", other, err)
	}
	if err := store.Purge(ctx, now.Add(-Retention)); err != nil {
		t.Fatal(err)
	}
	all, err := store.Recent(ctx, "local", now.Add(-2*Retention), 50, 0)
	if err != nil || len(all) != 4 {
		t.Fatalf("retention boundary: %+v %v", all, err)
	}
	first, _ := store.Recent(ctx, "local", now.Add(-Retention), 1, 0)
	second, _ := store.Recent(ctx, "local", now.Add(-Retention), 1, 1)
	if len(first) != 1 || len(second) != 1 || first[0].ID == second[0].ID {
		t.Fatal("pagination repeats events")
	}
}

func TestImportRejectsWholeInvalidBatch(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, statement := range SchemaQueries() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	store := Store{DB: database}
	valid := Event{ID: "valid", Kind: "invoice", Stage: "queued", CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := store.Import(context.Background(), "local", []Event{valid}); err == nil {
		t.Fatal("remote batch overwrote local namespace")
	}
	invalid := valid
	invalid.ID = "invalid"
	invalid.CreatedAt = "not a date"
	if err := store.Import(context.Background(), "remote", []Event{valid, invalid}); err == nil {
		t.Fatal("invalid batch accepted")
	}
	events, err := store.Recent(context.Background(), "remote", time.Time{}, 50, 0)
	if err != nil || len(events) != 0 {
		t.Fatalf("partially committed invalid batch: %+v %v", events, err)
	}
	if err := ValidateBatch(make([]Event, 81)); err == nil {
		t.Fatal("oversized batch accepted")
	}
}
