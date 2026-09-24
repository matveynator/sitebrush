package mailout

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSecurityBoundaryDeliveryChannelAppliesBackpressureToSpamBurst(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	entered := make(chan struct{})
	release := make(chan struct{})
	jobs := StartDeliveryWorker(ctx, func(context.Context, Message) error {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-release
		return nil
	})

	jobs <- DeliveryJob{Message: Message{MessageID: "active", To: "user@example.com"}}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("delivery worker did not start the active message")
	}

	for index := 0; index < DeliveryQueueSize; index++ {
		select {
		case jobs <- DeliveryJob{Message: Message{MessageID: fmt.Sprintf("queued-%d", index), To: "user@example.com"}}:
		default:
			t.Fatalf("mail channel applied backpressure too early at %d queued jobs", index)
		}
	}

	select {
	case jobs <- DeliveryJob{Message: Message{MessageID: "spam-overflow", To: "user@example.com"}}:
		t.Fatal("SECURITY: mail delivery channel accepted work beyond its bounded queue")
	default:
	}

	close(release)
}

func TestSecurityBoundaryDurableOutboxDeduplicatesSpamReplay(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "mail-spam.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, statement := range SchemaQueries() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Unix(1_800_300_000, 0).UTC()
	task := Task{
		ID:             "stable-security-code-delivery",
		InstallationID: "installation-a",
		Kind:           "security-code",
		Route:          RouteRelay,
		Message: Message{
			From:    "sitebrush@example.com",
			To:      "owner@example.net",
			Subject: "Security code",
			Body:    "123456",
		},
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	}

	insertedCount := 0
	for attempt := 0; attempt < 100; attempt++ {
		inserted, err := Insert(context.Background(), database, task)
		if err != nil {
			t.Fatalf("replay %d: %v", attempt, err)
		}
		if inserted {
			insertedCount++
		}
	}
	if insertedCount != 1 {
		t.Fatalf("SECURITY: replaying one delivery ID created %d durable messages, want 1", insertedCount)
	}

	var rows int
	if err := database.QueryRow("SELECT COUNT(*) FROM mail_outbox WHERE message_id=?", task.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("SECURITY: durable outbox contains %d replay copies, want 1", rows)
	}
}

func TestSecurityBoundaryMailRouteAndRecipientCannotBeSmuggledThroughChannelTask(t *testing.T) {
	for _, task := range []Task{
		{Route: "relay\r\nspam", Message: Message{To: "victim@example.net"}},
		{Route: RouteRelay, Message: Message{To: "victim@example.net\r\nBcc: attacker@example.net"}},
		{Route: RouteLocal, Message: Message{To: "a@example.net, b@example.net"}},
	} {
		normalized, err := NormalizeTask(task)
		if err == nil {
			// NormalizeTask is only the durable-envelope validator; the SMTP
			// boundary must still reject any address that represents more than
			// one mailbox or contains header control characters.
			if _, parseErr := parseSingleAddress(normalized.Message.To); parseErr == nil {
				t.Fatalf("SECURITY: mail task smuggled unsafe route/recipient through validation: %#v", task)
			}
		}
	}
}

func TestSecurityBoundaryClaimAllowsOnlyOneDeliveryOwner(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "mail-claim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, statement := range SchemaQueries() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Unix(1_800_300_100, 0).UTC()
	task := Task{
		ID:        "claim-once",
		Route:     RouteLocal,
		Message:   Message{To: "owner@example.net"},
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	}
	if inserted, err := Insert(context.Background(), database, task); err != nil || !inserted {
		t.Fatalf("insert=%v err=%v", inserted, err)
	}
	first, err := Claim(context.Background(), database, task.ID)
	if err != nil || !first {
		t.Fatalf("first claim=%v err=%v", first, err)
	}
	second, err := Claim(context.Background(), database, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second {
		t.Fatal("SECURITY: a second mail worker claimed the same durable delivery")
	}
}
