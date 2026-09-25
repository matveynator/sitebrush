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

func TestDirectSenderValidatesAddressesBeforeDNSAndHonorsCancellation(t *testing.T) {
	sender := DirectSender{}
	if err := sender.Send(context.Background(), Message{From: "invalid", To: "recipient@example.com"}); err == nil {
		t.Fatal("invalid sender address accepted")
	}
	if err := sender.Send(context.Background(), Message{From: "sender@example.com", To: "invalid"}); err == nil {
		t.Fatal("invalid recipient address accepted")
	}
	fromAddress, err := parseSingleAddress(" Sender <sender@example.com> ")
	if err != nil || addressDomain(fromAddress) != "example.com" {
		t.Fatalf("parsed sender=%v err=%v", fromAddress, err)
	}
	if addressDomain(nil) != "" || addressDomain(&mail.Address{Address: "invalid"}) != "" {
		t.Fatal("address without a domain was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sender.sendToHost(ctx, "mx.example.com", "sender@example.com", "recipient@example.com", []byte("body")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled send error=%v", err)
	}
	lookupContext, lookupCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer lookupCancel()
	if _, err := lookupMailHosts(lookupContext, "example.com"); err == nil {
		t.Fatal("MX lookup ignored expired context")
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

func TestOutboxTaskValidationStateTransitionsAndRetention(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/outbox-coverage.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, statement := range SchemaQueries() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if len(NewDeliveryID()) != 32 {
		t.Fatal("delivery ID has an unexpected size")
	}
	defaultTask := NewTask(" invoice ", RouteLocal, Message{To: "user@example.com"}, time.Time{}, nil)
	if defaultTask.Kind != "invoice" || defaultTask.Message.MessageID != defaultTask.ID || !defaultTask.ExpiresAt.After(defaultTask.CreatedAt) {
		t.Fatalf("new task = %#v", defaultTask)
	}
	for _, task := range []Task{
		{Route: "unknown", Message: Message{To: "user@example.com"}},
		{Route: RouteLocal, Message: Message{}},
		{ID: "bad-expiry", Route: RouteLocal, Message: Message{To: "user@example.com"}, CreatedAt: now, ExpiresAt: now},
	} {
		if _, err := NormalizeTask(task); err == nil {
			t.Errorf("invalid task was normalized: %#v", task)
		}
	}
	task, err := NormalizeTask(Task{ID: " stable ", InstallationID: " host ", Route: RouteRelay, Message: Message{To: " user@example.com "}, CreatedAt: now})
	if err != nil || task.ID != "stable" || task.InstallationID != "host" || task.Message.MessageID != "stable" || task.Kind != "system" {
		t.Fatalf("normalized task = %#v, %v", task, err)
	}
	inserted, err := Insert(ctx, database, Task{ID: "stable", InstallationID: "host", Kind: "system", Route: RouteLocal, Message: Message{From: "from@example.com", To: "user@example.com", Subject: "Hi", Body: "plain", HTMLBody: "<p>hi</p>"}, CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil || !inserted {
		t.Fatalf("insert outbox task = %t, %v", inserted, err)
	}
	if inserted, err := Insert(ctx, database, Task{ID: "stable", Route: RouteLocal, Message: Message{To: "user@example.com"}}); err != nil || inserted {
		t.Fatalf("duplicate outbox insert = %t, %v", inserted, err)
	}
	record, found, err := ByID(ctx, database, " stable ")
	if err != nil || !found || record.Status != StatusPending || record.Message.Body != "plain" {
		t.Fatalf("outbox record = %#v, %t, %v", record, found, err)
	}
	if _, found, err := ByID(ctx, database, "missing"); err != nil || found {
		t.Fatalf("missing outbox record = %t, %v", found, err)
	}
	if err := MarkPending(ctx, database, "stable", 2, now.Add(time.Minute), errors.New("temporary")); err != nil {
		t.Fatal(err)
	}
	due, err := Due(ctx, database, now.Add(2*time.Minute), 0)
	if err != nil || len(due) != 1 || due[0].Attempts != 2 || due[0].LastError != "temporary" {
		t.Fatalf("due records = %#v, %v", due, err)
	}
	claimed, err := Claim(ctx, database, "stable")
	if err != nil || !claimed {
		t.Fatalf("claim outbox task = %t, %v", claimed, err)
	}
	if err := MarkFailed(ctx, database, "stable", 3, nil); err != nil {
		t.Fatal(err)
	}
	record, found, err = ByID(ctx, database, "stable")
	if err != nil || !found || record.Status != StatusFailed || record.LastError != "delivery failed" || record.Message.Body != "" {
		t.Fatalf("failed task = %#v, %t, %v", record, found, err)
	}
	if err := PurgeTerminal(ctx, database, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, found, err := ByID(ctx, database, "stable"); err != nil || found {
		t.Fatalf("purged task remains: %t, %v", found, err)
	}
	for attempt := 0; attempt <= 6; attempt++ {
		delay := RetryDelayWithJitter(attempt)
		base := RetryDelay(attempt)
		if delay < base*8/10 || delay > base*12/10 {
			t.Fatalf("jitter delay %d = %s outside base %s", attempt, delay, base)
		}
	}
	cause := errors.New("cause")
	wrapped := PermanentError{Err: cause}
	if (PermanentError{}).Error() != "permanent mail delivery failure" || wrapped.Error() != "cause" || wrapped.Unwrap() != cause {
		t.Fatal("permanent mail error representation is invalid")
	}
}

func TestDeliveryWorkerReturnsAndStopsOnSubscriberLifetime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	completed := make(chan Message, 1)
	jobs := StartDeliveryWorker(ctx, func(_ context.Context, message Message) error {
		completed <- message
		return nil
	})
	jobs <- DeliveryJob{Message: Message{MessageID: "worker-task", To: "user@example.com"}}
	select {
	case message := <-completed:
		if message.MessageID != "worker-task" {
			t.Fatalf("worker message = %#v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("delivery worker did not process the task")
	}
	cancel()
}


// BEGIN mail abuse and duplicate-delivery security tests.

func TestDirectSenderRejectsAddressHeaderInjectionBeforeLookup(t *testing.T) {
	lookupCalled := false
	sender := DirectSender{
		lookupHosts: func(context.Context, string) ([]string, error) {
			lookupCalled = true
			return []string{"mx.example.net"}, nil
		},
	}
	for _, message := range []Message{
		{From: "sender@example.com\r\nBcc: victim@example.net", To: "owner@example.net"},
		{From: "sender@example.com", To: "owner@example.net\r\nCc: victim@example.net"},
	} {
		if err := sender.Send(context.Background(), message); err == nil {
			t.Fatalf("header-injected address was accepted: %#v", message)
		}
	}
	if lookupCalled {
		t.Fatal("DNS lookup ran before rejecting a header-injected address")
	}
}

func TestMessagePayloadDoesNotPermitHeaderInjection(t *testing.T) {
	fromAddress, _ := mail.ParseAddress("SiteBrush <sitebrush@sitebrush.com>")
	toAddress, _ := mail.ParseAddress("Owner <owner@example.net>")
	payload := string(buildMessagePayloadWithID(
		fromAddress,
		toAddress,
		"Security notice\r\nBcc: victim@example.net",
		"Body",
		"",
		"stable\r\nX-Injected: yes",
	))
	if strings.Contains(payload, "\r\nBcc: victim@example.net\r\n") {
		t.Fatalf("subject created an injected Bcc header: %s", payload)
	}
	if strings.Contains(payload, "\r\nX-Injected: yes\r\n") {
		t.Fatalf("message ID created an injected header: %s", payload)
	}
	if strings.Contains(payload, "Message-ID: <stable\r\n") {
		t.Fatalf("unsafe stable Message-ID was emitted verbatim: %s", payload)
	}
}

func TestDeliveryWorkerDoesNotRetryOneFailedJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempts := make(chan string, 4)
	jobs := StartDeliveryWorker(ctx, func(_ context.Context, message Message) error {
		attempts <- message.MessageID
		return errors.New("temporary SMTP failure")
	})
	jobs <- DeliveryJob{Message: Message{MessageID: "one-action-one-message", To: "owner@example.net"}}

	select {
	case messageID := <-attempts:
		if messageID != "one-action-one-message" {
			t.Fatalf("delivery attempt message ID=%q", messageID)
		}
	case <-time.After(time.Second):
		t.Fatal("delivery worker did not attempt the submitted message")
	}
	select {
	case duplicate := <-attempts:
		t.Fatalf("one delivery job was retried inside the worker: %q", duplicate)
	case <-time.After(100 * time.Millisecond):
	}
	close(jobs)
}

func TestOutboxConcurrentClaimHasOneOwner(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/mail-claim-race.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	for _, statement := range SchemaQueries() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	inserted, err := Insert(context.Background(), database, Task{
		ID:        "single-owner",
		Route:     RouteLocal,
		Message:   Message{To: "owner@example.net"},
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil || !inserted {
		t.Fatalf("inserted=%t err=%v", inserted, err)
	}

	const claimers = 32
	results := make(chan bool, claimers)
	errorsChannel := make(chan error, claimers)
	start := make(chan struct{})
	for claimerIndex := 0; claimerIndex < claimers; claimerIndex++ {
		go func() {
			<-start
			claimed, claimErr := Claim(context.Background(), database, "single-owner")
			if claimErr != nil {
				errorsChannel <- claimErr
				return
			}
			results <- claimed
		}()
	}
	close(start)

	owners := 0
	for resultIndex := 0; resultIndex < claimers; resultIndex++ {
		select {
		case claimErr := <-errorsChannel:
			t.Fatalf("concurrent outbox claim failed: %v", claimErr)
		case claimed := <-results:
			if claimed {
				owners++
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent outbox claims did not finish")
		}
	}
	if owners != 1 {
		t.Fatalf("outbox owners=%d want=1", owners)
	}
}

// END mail abuse and duplicate-delivery security tests.
