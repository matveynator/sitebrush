package mailout

import (
	"context"
	"database/sql"
	"errors"
	"net/mail"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func mailoutCoverageDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "mailout-extra.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, query := range SchemaQueries() {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestMailoutOutboxErrorAndTransitionCoverage(t *testing.T) {
	db := mailoutCoverageDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_700_000, 0).UTC()

	if inserted, err := Insert(ctx, db, Task{Route: "bad", Message: Message{To: "x@example.com"}}); err == nil || inserted {
		t.Fatalf("invalid route insert=%v err=%v", inserted, err)
	}
	if inserted, err := Insert(ctx, db, Task{Route: RouteLocal, Message: Message{}}); err == nil || inserted {
		t.Fatalf("missing recipient insert=%v err=%v", inserted, err)
	}

	task := Task{
		ID: "one",
		Route: RouteLocal,
		Message: Message{From: "sender@example.com", To: "user@example.com", Body: "secret", HTMLBody: "<b>secret</b>"},
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	}
	inserted, err := Insert(ctx, db, task)
	if err != nil || !inserted {
		t.Fatalf("insert=%v %v", inserted, err)
	}

	claimed, err := Claim(ctx, db, "one")
	if err != nil || !claimed {
		t.Fatalf("claim=%v %v", claimed, err)
	}
	claimed, err = Claim(ctx, db, "one")
	if err != nil || claimed {
		t.Fatalf("second claim=%v %v", claimed, err)
	}

	if err := MarkPending(ctx, db, "one", 1, now.Add(time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	if err := MarkPending(ctx, db, "one", 2, now.Add(2*time.Minute), errors.New("temporary failure")); err != nil {
		t.Fatal(err)
	}
	due, err := Due(ctx, db, now.Add(3*time.Minute), 1)
	if err != nil || len(due) != 1 || due[0].LastError != "temporary failure" {
		t.Fatalf("due=%#v err=%v", due, err)
	}

	if err := MarkFailed(ctx, db, "one", 3, errors.New("550 rejected")); err != nil {
		t.Fatal(err)
	}
	record, found, err := ByID(ctx, db, "one")
	if err != nil || !found || record.Status != StatusFailed || record.Message.Body != "" || record.Message.HTMLBody != "" {
		t.Fatalf("failed record=%#v found=%v err=%v", record, found, err)
	}
	if err := PurgeTerminal(ctx, db, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, found, err := ByID(ctx, db, "one"); err != nil || found {
		t.Fatalf("purged found=%v err=%v", found, err)
	}

	if _, err := db.Exec("DROP TABLE mail_outbox"); err != nil {
		t.Fatal(err)
	}
	if _, err := Insert(ctx, db, task); err == nil {
		t.Fatal("SECURITY: Insert hid outbox storage failure")
	}
	if _, err := Due(ctx, db, now, 1); err == nil {
		t.Fatal("Due hid outbox storage failure")
	}
	if _, err := Claim(ctx, db, "one"); err == nil {
		t.Fatal("Claim hid outbox storage failure")
	}
	if err := MarkFailed(ctx, db, "one", 1, nil); err == nil {
		t.Fatal("MarkFailed hid outbox storage failure")
	}
}

func TestMailoutScanRecordRejectsCorruptedStoredTimes(t *testing.T) {
	base := []any{
		"id", "installation", "kind", "local", "from@example.com", "to@example.com", "subject", "body", "html",
		"pending", 1,
	}
	scanFor := func(next, created, expires, sent string) scanFunction {
		return func(dest ...any) error {
			values := append(append([]any{}, base...), next, created, expires, sent, "")
			for index, value := range values {
				switch destination := dest[index].(type) {
				case *string:
					*destination = value.(string)
				case *int:
					*destination = value.(int)
				default:
					return errors.New("unexpected scan destination")
				}
			}
			return nil
		}
	}
	valid := time.Unix(1_800_700_100, 0).UTC().Format(time.RFC3339Nano)
	if record, err := scanRecord(scanFor("", valid, valid, "")); err != nil || record.ID != "id" || record.Message.MessageID != "id" {
		t.Fatalf("valid scan=%#v err=%v", record, err)
	}
	for name, scan := range map[string]scanFunction{
		"next":      scanFor("bad", valid, valid, ""),
		"created":   scanFor("", "bad", valid, ""),
		"expires":   scanFor("", valid, "bad", ""),
		"sent":      scanFor("", valid, valid, "bad"),
		"scan error": func(...any) error { return errors.New("scan failed") },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := scanRecord(scan); err == nil {
				t.Fatal("corrupt outbox row was accepted")
			}
		})
	}
	if parsed, err := parseStoredTime(" "); err != nil || !parsed.IsZero() {
		t.Fatalf("empty stored time=%v %v", parsed, err)
	}
	if _, err := parseStoredTime("bad"); err == nil {
		t.Fatal("malformed stored time accepted")
	}
}

func TestMailoutAddressDomainAndPermanentFailureEdges(t *testing.T) {
	for _, raw := range []string{"", " ", "<>", "a@", "a@@example.com"} {
		if _, err := parseSingleAddress(raw); err == nil {
			t.Errorf("invalid address accepted: %q", raw)
		}
	}
	if got := addressDomain(nil); got != "" {
		t.Fatalf("nil domain=%q", got)
	}
	if got := addressDomain(&mail.Address{Address: "nodomain"}); got != "" {
		t.Fatalf("address without domain=%q", got)
	}
	if IsPermanentFailure(nil) {
		t.Fatal("nil error permanent")
	}
	for _, err := range []error{
		errors.New("server: (550) mailbox unavailable"),
		errors.New("550; rejected"),
		PermanentError{},
	} {
		if !IsPermanentFailure(err) {
			t.Errorf("permanent failure not recognized: %v", err)
		}
	}
	for _, err := range []error{errors.New("450 temporary"), errors.New("code 55 malformed"), errors.New("abc")} {
		if IsPermanentFailure(err) {
			t.Errorf("temporary/unknown failure classified permanent: %v", err)
		}
	}
	if !strings.Contains((PermanentError{}).Error(), "permanent") || (PermanentError{}).Unwrap() != nil {
		t.Fatal("PermanentError nil representation invalid")
	}
}

func TestMailoutMessageIDDomainFallbackAndWorkerErrorPath(t *testing.T) {
	if got := messageIDDomain(&mail.Address{Address: "sender@localhost"}); got != "sitebrush.local" {
		t.Fatalf("localhost Message-ID domain=%q", got)
	}
	if got := messageIDDomain(&mail.Address{Address: "sender@example.org"}); got != "example.org" {
		t.Fatalf("Message-ID domain=%q", got)
	}
	if got := messageIDDomain(nil); got != "sitebrush.local" {
		t.Fatalf("nil Message-ID domain=%q", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	called := make(chan struct{}, 1)
	jobs := StartDeliveryWorker(ctx, func(context.Context, Message) error {
		called <- struct{}{}
		return errors.New("delivery failed")
	})
	jobs <- DeliveryJob{Message: Message{To: "user@example.org"}}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("delivery worker did not execute error path")
	}
	cancel()
}
