package mailout

import (
	"context"
	"database/sql"
	"errors"
	"net/mail"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestBuildMessagePayloadIncludesMessageID(t *testing.T) {
	fromAddress, err := mail.ParseAddress("SiteBrush <noreply@example.com>")
	if err != nil {
		t.Fatal(err)
	}
	toAddress, err := mail.ParseAddress("User <user@example.net>")
	if err != nil {
		t.Fatal(err)
	}

	payload := string(buildMessagePayload(fromAddress, toAddress, "Привет", "Body"))
	if !strings.Contains(payload, "\r\nMessage-ID: <") {
		t.Fatalf("payload missing Message-ID header: %s", payload)
	}
	if !strings.Contains(payload, "@example.com>\r\n") {
		t.Fatalf("payload Message-ID does not use sender domain: %s", payload)
	}
	if !strings.Contains(payload, "\r\nSubject: =?utf-8?") {
		t.Fatalf("payload missing encoded subject: %s", payload)
	}
}

func TestBuildMessagePayloadUsesMultipartAlternativeForHTML(t *testing.T) {
	fromAddress, _ := mail.ParseAddress("SiteBrush <noreply@example.com>")
	toAddress, _ := mail.ParseAddress("User <user@example.net>")
	payload := string(buildMessagePayload(fromAddress, toAddress, "Invoice", "Plain invoice", "<strong>HTML invoice</strong>"))
	for _, expected := range []string{"multipart/alternative", "Content-Type: text/plain", "Content-Type: text/html", "Plain invoice", "<strong>HTML invoice</strong>"} {
		if !strings.Contains(payload, expected) {
			t.Fatalf("payload does not contain %q: %s", expected, payload)
		}
	}
}

func TestBuildMessagePayloadReusesStableDeliveryMessageID(t *testing.T) {
	fromAddress, _ := mail.ParseAddress("SiteBrush <sitebrush@sitebrush.com>")
	toAddress, _ := mail.ParseAddress("User <user@example.net>")
	for attempt := 0; attempt < 2; attempt++ {
		payload := string(buildMessagePayloadWithID(fromAddress, toAddress, "Subject", "Body", "", "stable-delivery-id"))
		if !strings.Contains(payload, "Message-ID: <stable-delivery-id@sitebrush.com>\r\n") {
			t.Fatalf("attempt %d did not preserve stable Message-ID: %s", attempt+1, payload)
		}
	}
}

func TestOutboxPersistsDeduplicatesAndRecoversDelivery(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/mail.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, query := range SchemaQueries() {
		if _, err := database.ExecContext(context.Background(), query); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	task := Task{
		ID:        "stable-message-id",
		Kind:      "invoice",
		Route:     RouteRelay,
		Message:   Message{From: "SiteBrush <sitebrush@sitebrush.com>", To: "owner@example.net", Subject: "Invoice", Body: "Body"},
		CreatedAt: now,
		ExpiresAt: now.Add(DefaultRetention),
	}
	inserted, err := Insert(context.Background(), database, task)
	if err != nil || !inserted {
		t.Fatalf("insert = %t, %v", inserted, err)
	}
	inserted, err = Insert(context.Background(), database, task)
	if err != nil || inserted {
		t.Fatalf("duplicate insert = %t, %v", inserted, err)
	}
	records, err := Due(context.Background(), database, now, 4)
	if err != nil || len(records) != 1 {
		t.Fatalf("due records = %d, %v", len(records), err)
	}
	claimed, err := Claim(context.Background(), database, task.ID)
	if err != nil || !claimed {
		t.Fatalf("claim = %t, %v", claimed, err)
	}
	if err := RecoverInterrupted(context.Background(), database, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	record, found, err := ByID(context.Background(), database, task.ID)
	if err != nil || !found || record.Status != StatusPending {
		t.Fatalf("recovered record = %#v, found=%t, err=%v", record, found, err)
	}
	if err := MarkSent(context.Background(), database, task.ID, 2, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	record, found, err = ByID(context.Background(), database, task.ID)
	if err != nil || !found || record.Status != StatusSent || record.Message.Body != "" {
		t.Fatalf("sent record = %#v, found=%t, err=%v", record, found, err)
	}
}

func TestRetryPolicyAndPermanentSMTPFailure(t *testing.T) {
	wantDelays := []time.Duration{5 * time.Second, 5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour}
	for attempts, want := range wantDelays {
		if got := RetryDelay(attempts); got != want {
			t.Fatalf("retry delay %d = %s, want %s", attempts, got, want)
		}
	}
	if !IsPermanentFailure(errors.New("mx.example 550 recipient rejected")) {
		t.Fatal("SMTP 550 was not classified as permanent")
	}
	if IsPermanentFailure(errors.New("mx.example 451 try again")) {
		t.Fatal("SMTP 451 was classified as permanent")
	}
	if !IsPermanentFailure(PermanentError{Err: errors.New("policy rejected")}) {
		t.Fatal("explicit permanent error was not classified as permanent")
	}
}
