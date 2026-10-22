package mailmonitor

import (
	"context"
	"testing"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/mailout"
)

func TestUnavailableMailStorageNeverAcknowledgesControlOrHistory(t *testing.T) {
	database := journalTestDatabase(t)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	store := Store{DB: database}
	boundary := context.Background()
	for name, operation := range map[string]func() error{
		"append":           func() error { return store.Append(boundary, Event{Kind: "recovery", Stage: "queued"}) },
		"blocks":           func() error { _, err := store.Blocks(boundary); return err },
		"block write":      func() error { return store.SetBlock(boundary, IPBlock{Scope: "global", IP: "192.0.2.5"}, true) },
		"block check":      func() error { _, err := store.Blocked(boundary, "desktop", "192.0.2.5"); return err },
		"revision":         func() error { _, err := store.BumpRevision(boundary); return err },
		"policy":           func() error { return store.ApplyPolicy(boundary, ControlResponse{Revision: 1}) },
		"cancellation":     func() error { return store.RequestCancel(boundary, "desktop", "message") },
		"cancel status":    func() error { _, err := store.CancelStatus(boundary, "desktop", "message"); return err },
		"commands":         func() error { _, err := store.Commands(boundary, "desktop"); return err },
		"complete command": func() error { return store.CompleteCommand(boundary, "desktop", "message", "cancelled") },
		"relay events":     func() error { _, _, err := store.RelayEvents(boundary, "desktop", ""); return err },
		"abuse":            func() error { _, err := store.Abuse(boundary, time.Time{}); return err },
		"reject":           func() error { return store.Reject(boundary, "desktop", "192.0.2.5", "recovery", "blocked", time.Now()) },
		"reject rate":      func() error { return store.RejectRate(boundary, "source_ip", "192.0.2.5", time.Now()) },
		"rate count": func() error {
			_, err := store.RejectedCount(boundary, "source_ip", "192.0.2.5", time.Time{})
			return err
		},
		"journal":          func() error { _, _, err := store.Journal(boundary, Filter{}); return err },
		"journal counters": func() error { _, err := store.JournalCounts(boundary, "local", time.Time{}); return err },
		"operation":        func() error { _, _, err := store.Operation(boundary, "missing"); return err },
		"rebuild":          func() error { return store.Rebuild(boundary) },
		"state":            func() error { return store.SetState(boundary, "cursor", "new") },
		"export":           func() error { _, err := store.Unsynced(boundary, 80); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if operation() == nil {
				t.Fatal("storage failure was acknowledged")
			}
		})
	}
}

func TestCorruptProjectionsAndEvidenceFailExplicitly(t *testing.T) {
	database := journalTestDatabase(t)
	boundary := context.Background()
	store := Store{DB: database}
	if err := store.Append(boundary, Event{ID: "queued", FlowID: "flow", Kind: "recovery", Stage: "queued"}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE mail_journal SET payload='broken'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Journal(boundary, Filter{InstallationID: "local"}); err == nil {
		t.Fatal("corrupt projection accepted")
	}
	if _, _, err := store.Operation(boundary, digestID("local\nflow")); err == nil {
		t.Fatal("corrupt operation accepted")
	}
	if err := store.Reject(boundary, "desktop", "192.0.2.5", "recovery", "one", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE mail_abuse SET samples='broken'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Abuse(boundary, time.Time{}); err == nil {
		t.Fatal("corrupt samples accepted")
	}
	if err := store.Reject(boundary, "desktop", "192.0.2.5", "recovery", "two", time.Now()); err == nil {
		t.Fatal("corrupt samples overwritten")
	}
}

func TestMailAuditAndProjectionFailuresRollBackTogether(t *testing.T) {
	for _, table := range []string{"mail_monitor_metadata", "mail_journal", "mail_journal_stages", "mail_journal_counters"} {
		t.Run(table, func(t *testing.T) {
			database := journalTestDatabase(t)
			boundary := context.Background()
			if _, err := database.Exec(`CREATE TRIGGER deny_projection BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(ABORT,'projection unavailable'); END`); err != nil {
				t.Fatal(err)
			}
			transaction, err := database.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer transaction.Rollback()
			event := Event{ID: "atomic", FlowID: "flow", MessageID: "message", Kind: "recovery", Stage: "queued", Metadata: &mailout.Metadata{OriginInstallationID: "desktop"}}
			if err := (Store{DB: transaction}).Append(boundary, event); err == nil {
				t.Fatal("projection failure was acknowledged")
			}
			if err := transaction.Rollback(); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := database.QueryRow(`SELECT COUNT(*) FROM mail_monitor_events`).Scan(&count); err != nil || count != 0 {
				t.Fatal("failed audit transaction retained a partial event", count, err)
			}
		})
	}
}
