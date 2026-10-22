package mailmonitor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAccountOutcomeCommitsWithAccountTransactionAndReplays(t *testing.T) {
	database := journalTestDatabase(t)
	boundary := context.Background()
	event := Event{FlowID: "recovery", Kind: "recovery", Stage: "recovery_complete", Recipient: "owner@example.com"}
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = SaveAccountEvent(boundary, transaction, event); err != nil {
		t.Fatal(err)
	}
	if err = transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	pending, err := PendingAccountEvents(boundary, database)
	if err != nil || len(pending) != 0 {
		t.Fatal("rolled-back account outcome escaped", pending, err)
	}
	transaction, err = database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	for range 2 {
		if err = SaveAccountEvent(boundary, transaction, event); err != nil {
			t.Fatal(err)
		}
	}
	if err = transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	pending, err = PendingAccountEvents(boundary, database)
	if err != nil || len(pending) != 1 || pending[0].ID == "" || pending[0].CreatedAt == "" || pending[0].FlowID != event.FlowID {
		t.Fatal("account outcome lost or duplicated", pending, err)
	}
	if _, err = database.Exec(`UPDATE mail_account_events SET payload='broken'`); err != nil {
		t.Fatal(err)
	}
	if _, err = PendingAccountEvents(boundary, database); err == nil {
		t.Fatal("corrupt pending outcome was acknowledged")
	}
}

func TestAccountExportBatchIsBoundedAndUnavailableStorageFails(t *testing.T) {
	database := journalTestDatabase(t)
	boundary := context.Background()
	for index := range 81 {
		if err := SaveAccountEvent(boundary, database, Event{ID: fmt.Sprint(index), Kind: "registration", Stage: "registration_complete"}); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := PendingAccountEvents(boundary, database)
	if err != nil || len(pending) != 80 {
		t.Fatal("unbounded account export", len(pending), err)
	}
	if err = database.Close(); err != nil {
		t.Fatal(err)
	}
	if SaveAccountEvent(boundary, database, Event{}) == nil {
		t.Fatal("unavailable account write was accepted")
	}
	if _, err = PendingAccountEvents(boundary, database); err == nil {
		t.Fatal("unavailable export was acknowledged")
	}
	if (Store{DB: database}).BackfillRecovery(boundary) == nil {
		t.Fatal("unavailable recovery history was accepted")
	}
}

func TestRecoveryBackfillRequiresExplicitOutcomeAndUnambiguousFlow(t *testing.T) {
	for _, scenario := range []struct {
		name                 string
		flows                int
		malformed, completed bool
	}{
		{name: "no surviving flow"}, {name: "one flow", flows: 1}, {name: "ambiguous flows", flows: 2}, {name: "already completed", flows: 1, completed: true}, {name: "invalid timestamp", malformed: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			database := journalTestDatabase(t)
			boundary := context.Background()
			store := Store{DB: database}
			if _, err := database.Exec(`CREATE TABLE support_events(id INTEGER PRIMARY KEY,kind TEXT,status TEXT,message TEXT,domain TEXT,email TEXT,created_at TEXT)`); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			created := now.Format(time.RFC3339)
			if scenario.malformed {
				created = "zz-invalid-date"
			}
			if _, err := database.Exec(`INSERT INTO support_events VALUES(1,'password_changed','success','password recovered','localhost','owner@example.com',?)`, created); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`INSERT INTO support_events VALUES(2,'mail_sent','success','password recovered','localhost','owner@example.com',?)`, created); err != nil {
				t.Fatal(err)
			}
			if err := store.SetState(boundary, "local_origin", "desktop"); err != nil {
				t.Fatal(err)
			}
			for index := range scenario.flows {
				if err := store.Append(boundary, Event{FlowID: fmt.Sprint(index), Kind: "recovery", Stage: "recovery_requested", Domain: "localhost", Recipient: "owner@example.com", CreatedAt: now.Add(-time.Minute).Format(time.RFC3339)}); err != nil {
					t.Fatal(err)
				}
			}
			if scenario.completed {
				if err := store.Append(boundary, Event{FlowID: "0", Kind: "recovery", Stage: "recovery_complete", Domain: "localhost", Recipient: "owner@example.com", CreatedAt: created}); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := store.BackfillRecovery(boundary); err != nil {
					t.Fatal(err)
				}
			}
			outcomes, err := store.RecentFiltered(boundary, Filter{InstallationID: "local", Stage: "recovery_complete"})
			want := 1
			if scenario.malformed {
				want = 0
			}
			if err != nil || len(outcomes) != want {
				t.Fatal("backfill duplicated or invented success", outcomes, err)
			}
			if len(outcomes) > 0 && scenario.flows == 1 && outcomes[0].FlowID != "0" {
				t.Fatal("explicit outcome did not join its unique request", outcomes)
			}
			if len(outcomes) > 0 && !scenario.completed && !strings.Contains(outcomes[0].Detail, "saved password recovery") {
				t.Fatal("backfill did not retain its evidence boundary", outcomes)
			}
		})
	}
}
