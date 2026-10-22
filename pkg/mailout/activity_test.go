package mailout

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestDurableMetadataAndCancellationCompeteWithClaim(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "outbox.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	for _, query := range SchemaQueries() {
		if _, err = database.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	boundary := context.Background()
	message := Message{From: "sender@example.com", To: "owner@example.com", Body: "secret", MonitorDomain: "example.com", MonitorFlowID: "flow", InitiatorIP: "127.0.0.1", OriginInstallationID: "desktop", RelayTransport: "transport=netchan protocol=quic destination=203.0.113.10:9876"}
	for _, id := range []string{"cancel-first", "claim-first"} {
		task := NewTask("login_code", RouteLocal, message, time.Now().Add(time.Hour), nil)
		task.ID = id
		if _, err = Insert(boundary, database, task); err != nil {
			t.Fatal(err)
		}
	}
	record, found, err := ByID(boundary, database, "cancel-first")
	if err != nil || !found || record.Message.Metadata() != message.Metadata() {
		t.Fatalf("metadata=%+v found=%t err=%v", record.Message.Metadata(), found, err)
	}
	cancelled, err := Cancel(boundary, database, "cancel-first")
	if err != nil || !cancelled {
		t.Fatal("cancel failed", err)
	}
	claimed, err := Claim(boundary, database, "cancel-first")
	if err != nil || claimed {
		t.Fatal("cancelled message claimed", err)
	}
	claimed, err = Claim(boundary, database, "claim-first")
	if err != nil || !claimed {
		t.Fatal("claim failed", err)
	}
	cancelled, err = Cancel(boundary, database, "claim-first")
	if err != nil || cancelled {
		t.Fatal("sending message cancelled", err)
	}
	if err = RecoverInterrupted(boundary, database, time.Now()); err != nil {
		t.Fatal(err)
	}
	record, _, err = ByID(boundary, database, "cancel-first")
	if err != nil || record.Status != StatusCancelled || record.Message.Body != "" {
		t.Fatalf("cancellation lost after restart: %+v %v", record, err)
	}
}
