package mailmonitor

import (
	"context"
	"database/sql"
	"fmt"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/mailout"
)

func journalTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })
	for _, query := range SchemaQueries() {
		if _, err = database.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func TestJournalCorrelatesLocalRecoveryAndCentralSMTP(t *testing.T) {
	database := journalTestDatabase(t)
	store := Store{DB: database}
	boundary := context.Background()
	now := time.Now().UTC().Add(-time.Minute)
	metadata := &mailout.Metadata{OriginInstallationID: "desktop", InitiatorIP: "127.0.0.1", FlowID: "flow", Domain: "localhost"}
	events := []Event{
		{ID: "requested", InstallationID: "desktop", FlowID: "flow", Kind: "recovery", Stage: "recovery_requested", Recipient: "owner@example.com", Metadata: metadata},
		{ID: "queued-origin", InstallationID: "desktop", FlowID: "flow", MessageID: "message", Kind: "login_code", Stage: "queued", Metadata: metadata},
		{ID: "queued-relay", InstallationID: "local", FlowID: "flow", MessageID: "message", Kind: "login_code", Stage: "queued", Metadata: metadata},
		{ID: "accepted-relay", InstallationID: "desktop", FlowID: "flow", MessageID: "message", Kind: "login_code", Stage: "accepted", AcceptedBy: "central_relay", Metadata: metadata},
		{ID: "smtp", InstallationID: "local", FlowID: "flow", MessageID: "message", Kind: "login_code", Stage: "accepted", AcceptedBy: "recipient_smtp", Transport: "transport=smtp mx=mx.example.com destination=203.0.113.8:25", Metadata: metadata},
		{ID: "complete", InstallationID: "desktop", FlowID: "flow", Kind: "recovery", Stage: "recovery_complete", Metadata: metadata},
	}
	for index := range events {
		events[index].CreatedAt = now.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano)
		if err := store.Append(boundary, events[index]); err != nil {
			t.Fatal(err)
		}
	}
	for _, event := range events {
		if err := store.Append(boundary, event); err != nil {
			t.Fatal(err)
		}
	}
	operations, count, err := store.Journal(boundary, Filter{InstallationID: "local"})
	if err != nil || count != 1 || len(operations) != 1 {
		t.Fatalf("operations=%+v count=%d error=%v", operations, count, err)
	}
	if operations[0].Stage != "recovery_complete" || operations[0].InitiatorIP != "127.0.0.1" {
		t.Fatalf("incomplete recovery: %+v", operations[0])
	}
	for _, stage := range []string{"recovery_requested", "queued", "relay_accepted", "accepted", "recovery_complete"} {
		matched, count, err := store.Journal(boundary, Filter{InstallationID: "local", Stage: stage, Kind: " LOGIN_CODE ", Since: now.Add(-time.Second)})
		if err != nil || count != 1 || len(matched) != 1 || matched[0].Stage != "recovery_complete" {
			t.Fatalf("completed recovery lost its %s event: %+v count=%d error=%v", stage, matched, count, err)
		}
	}
	for _, installation := range []string{"local", "desktop"} {
		counts, err := store.JournalCounts(boundary, installation, now.Add(-time.Second))
		if err != nil || counts.Queued != 1 || counts.Accepted != 1 || counts.RelayAccepted != 1 || counts.RecoveryRequested != 1 || counts.RecoveryComplete != 1 {
			t.Fatalf("%s counts=%+v error=%v", installation, counts, err)
		}
	}
	counts, err := store.JournalCounts(boundary, "other", now.Add(-time.Second))
	if err != nil || counts.Queued != 0 {
		t.Fatalf("other installation leaked: %+v %v", counts, err)
	}
	// Another actual email in the same account operation is still counted once.
	second := events[1]
	second.ID = "second"
	second.MessageID = "second-message"
	if err := store.Append(boundary, second); err != nil {
		t.Fatal(err)
	}
	counts, err = store.JournalCounts(boundary, "desktop", now.Add(-time.Second))
	if err != nil || counts.Queued != 2 {
		t.Fatalf("second message not counted: %+v %v", counts, err)
	}
}

func TestJournalEventFilterUsesHistoryBeyondVisibleStepsAndRespectsPeriod(t *testing.T) {
	store := Store{DB: journalTestDatabase(t)}
	boundary := context.Background()
	now := time.Now().UTC()
	for number := range 40 {
		event := Event{ID: fmt.Sprint(number), InstallationID: "desktop", FlowID: "flow", MessageID: fmt.Sprint(number), Kind: "login_code", Stage: "queued", CreatedAt: now.Add(-48 * time.Hour).Format(time.RFC3339Nano)}
		if err := store.Append(boundary, event); err != nil {
			t.Fatal(err)
		}
	}
	for index, stage := range []string{"attempt", "retry", "recovery_complete"} {
		event := Event{ID: stage, InstallationID: "desktop", FlowID: "flow", Kind: "recovery", Stage: stage, CreatedAt: now.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano)}
		if err := store.Append(boundary, event); err != nil {
			t.Fatal(err)
		}
	}
	for _, fixture := range []struct {
		stage string
		count int
	}{{"queued", 0}, {"attempt", 1}, {"retry", 1}, {"recovery_complete", 1}, {"registration_verified", 0}} {
		operations, count, err := store.Journal(boundary, Filter{InstallationID: "desktop", Stage: fixture.stage, Kind: "recovery", Since: now.Add(-time.Hour)})
		if err != nil || count != fixture.count || len(operations) != fixture.count {
			t.Fatalf("%s count=%d operations=%+v error=%v", fixture.stage, count, operations, err)
		}
		if len(operations) > 0 && (operations[0].Stage != "recovery_complete" || len(operations[0].Steps) != 32) {
			t.Fatal("history filter changed the bounded operation", operations[0])
		}
	}
	operations, count, err := store.Journal(boundary, Filter{InstallationID: "other", Stage: "retry", Since: now.Add(-time.Hour)})
	if err != nil || count != 0 || len(operations) != 0 {
		t.Fatal("history filter crossed installation ownership", operations, count, err)
	}
}

func TestLegacyEventEnrichmentPreservesAuditAndCounters(t *testing.T) {
	store := Store{DB: journalTestDatabase(t)}
	boundary := context.Background()
	event := Event{ID: "legacy", InstallationID: "desktop", FlowID: "recovery-flow", MessageID: "message", Kind: "login_code", Stage: "queued", Recipient: "owner@example.com", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := store.Append(boundary, event); err != nil {
		t.Fatal(err)
	}
	event.Metadata = &mailout.Metadata{OriginInstallationID: "desktop", InitiatorIP: "127.0.0.1"}
	event.Transport = "transport=netchan protocol=quic"
	tampered := event
	tampered.Recipient = "changed@example.com"
	if err := store.Append(boundary, tampered); err == nil {
		t.Fatal("enrichment changed the immutable recipient")
	}
	for range 2 {
		if err := store.Append(boundary, event); err != nil {
			t.Fatal(err)
		}
	}
	changedMetadata := event
	changedMetadata.Metadata = &mailout.Metadata{OriginInstallationID: "desktop", InitiatorIP: "192.0.2.99"}
	if err := store.Append(boundary, changedMetadata); err != nil {
		t.Fatal(err)
	}
	operations, count, err := store.Journal(boundary, Filter{InstallationID: "desktop"})
	if err != nil || count != 1 || len(operations) != 1 || operations[0].InitiatorIP != "127.0.0.1" || len(operations[0].Steps) != 1 || operations[0].Steps[0].Transport != event.Transport {
		t.Fatalf("enrichment lost details or changed existing metadata: %+v count=%d error=%v", operations, count, err)
	}
	counts, err := store.JournalCounts(boundary, "desktop", time.Time{})
	if err != nil || counts.Queued != 1 {
		t.Fatalf("enrichment inflated counters: %+v %v", counts, err)
	}
}

func TestRejectedSpamUsesOneBoundedSummary(t *testing.T) {
	database := journalTestDatabase(t)
	boundary := context.Background()
	transaction, err := database.BeginTx(boundary, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	store := Store{DB: transaction}
	now := time.Now().UTC().Truncate(10 * time.Minute).Add(time.Minute)
	for number := range 10000 {
		if err = store.Reject(boundary, "desktop", "203.0.113.6", "recovery", fmt.Sprintf("recipient-%d@example.com token=secret", number), now); err != nil {
			t.Fatal(err)
		}
		if err = store.RejectRate(boundary, "installation_id", "desktop", now); err != nil {
			t.Fatal(err)
		}
	}
	summaries, err := store.Abuse(boundary, now.Add(-time.Second))
	if err != nil || len(summaries) != 1 || summaries[0].Attempts != 10000 || len(summaries[0].Samples) != 20 {
		t.Fatalf("summaries=%+v error=%v", summaries, err)
	}
	count, err := store.RejectedCount(boundary, "installation_id", "desktop", now.Add(-time.Second))
	if err != nil || count != 10000 {
		t.Fatalf("rate count=%d %v", count, err)
	}
	if err = store.ImportAbuse(boundary, "desktop", summaries); err != nil {
		t.Fatal(err)
	}
	summaries, err = store.Abuse(boundary, now.Add(-time.Second))
	if err != nil || summaries[0].Attempts != 10000 {
		t.Fatalf("replayed report inflated count: %+v %v", summaries, err)
	}
}

func TestGlobalMailPolicyIsReversibleAndDoesNotReplaceLocalRules(t *testing.T) {
	store := Store{DB: journalTestDatabase(t)}
	boundary := context.Background()
	local := IPBlock{Scope: "installation", OriginInstallationID: "desktop", IP: "192.168.1.8", Reason: "local decision"}
	if err := store.SetBlock(boundary, local, true); err != nil {
		t.Fatal(err)
	}
	policy := ControlResponse{Revision: 1, Blocks: []IPBlock{{Scope: "global", IP: "203.0.113.8"}, {Scope: "installation", OriginInstallationID: "desktop", IP: "10.0.0.8"}}}
	if err := store.ApplyPolicy(boundary, policy); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		origin, ip string
		blocked    bool
	}{{"desktop", "192.168.1.8", true}, {"other", "192.168.1.8", false}, {"other", "203.0.113.8", true}, {"desktop", "10.0.0.8", true}, {"other", "10.0.0.8", false}} {
		blocked, err := store.Blocked(boundary, fixture.origin, fixture.ip)
		if err != nil || blocked != fixture.blocked {
			t.Fatalf("%+v blocked=%t error=%v", fixture, blocked, err)
		}
	}
	if err := store.ApplyPolicy(boundary, ControlResponse{Revision: 2}); err != nil {
		t.Fatal(err)
	}
	blocked, err := store.Blocked(boundary, "other", "203.0.113.8")
	if err != nil || blocked {
		t.Fatal("global unblock failed", err)
	}
	blocked, err = store.Blocked(boundary, "desktop", "192.168.1.8")
	if err != nil || !blocked {
		t.Fatal("local decision was removed", err)
	}
	if err = store.ApplyPolicy(boundary, policy); err == nil {
		t.Fatal("old policy reinstated a revoked block")
	}
	if err = store.SetBlock(boundary, IPBlock{Scope: "global", IP: "192.168.1.8"}, true); err == nil {
		t.Fatal("loopback allowed as global block")
	}
}

func TestLoopbackMailBlocksAreRejectedAndLegacyRulesAreIgnored(t *testing.T) {
	store := Store{DB: journalTestDatabase(t)}
	boundary := context.Background()
	for _, address := range []string{"127.0.0.1", "127.0.0.2", "127.255.255.254", "::1", "::ffff:127.0.0.1"} {
		block := IPBlock{Scope: "installation", OriginInstallationID: "desktop", IP: address}
		if !IsLoopbackIP(address) || store.SetBlock(boundary, block, true) == nil {
			t.Fatal("local access IP could be blocked", address)
		}
		canonicalAddress, _ := netip.ParseAddr(address)
		if _, err := store.DB.ExecContext(boundary, `INSERT INTO mail_ip_blocks(scope,origin_id,ip,reason,created_at) VALUES('installation','desktop',?,'legacy','')`, canonicalAddress.Unmap().String()); err != nil {
			t.Fatal(err)
		}
		blocked, err := store.Blocked(boundary, "desktop", address)
		if err != nil || blocked {
			t.Fatalf("legacy loopback rule still blocks local access: %s %t %v", address, blocked, err)
		}
		if err := store.SetBlock(boundary, block, false); err != nil {
			t.Fatal("legacy loopback rule cannot be removed", err)
		}
	}
	policy := ControlResponse{Revision: 1, Blocks: []IPBlock{{Scope: "installation", OriginInstallationID: "desktop", IP: "127.0.0.1"}, {Scope: "global", IP: "203.0.113.8"}}}
	if err := store.ApplyPolicy(boundary, policy); err != nil {
		t.Fatal("legacy loopback policy prevented other rules from synchronizing", err)
	}
	blocks, err := store.Blocks(boundary)
	if err != nil || len(blocks) != 1 || blocks[0].IP != "203.0.113.8" {
		t.Fatalf("loopback policy was imported: %+v %v", blocks, err)
	}
}

func TestAccountOutcomeSurvivesExportFailureAndReplay(t *testing.T) {
	database := journalTestDatabase(t)
	boundary := context.Background()
	event := Event{FlowID: "one-time-recovery", Kind: "recovery", Stage: "recovery_complete", Recipient: "owner@example.com"}
	transaction, err := database.BeginTx(boundary, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = SaveAccountEvent(boundary, transaction, event); err != nil {
		t.Fatal(err)
	}
	transaction.Rollback()
	pending, err := PendingAccountEvents(boundary, database)
	if err != nil || len(pending) != 0 {
		t.Fatalf("rolled back outcome leaked: %+v %v", pending, err)
	}
	if err = SaveAccountEvent(boundary, database, event); err != nil {
		t.Fatal(err)
	}
	pending, err = PendingAccountEvents(boundary, database)
	if err != nil || len(pending) != 1 {
		t.Fatalf("committed outcome missing: %+v %v", pending, err)
	}
	store := Store{DB: database}
	for range 2 {
		if err = store.Append(boundary, pending[0]); err != nil {
			t.Fatal(err)
		}
	}
	counts, err := store.JournalCounts(boundary, "local", time.Time{})
	if err != nil || counts.RecoveryComplete != 1 {
		t.Fatalf("outcome count=%+v %v", counts, err)
	}
}

func TestRebuildJoinsLegacyRelayHistoryToRecoveryOutcome(t *testing.T) {
	database := journalTestDatabase(t)
	store := Store{DB: database}
	boundary := context.Background()
	for _, query := range mailout.SchemaQueries() {
		if _, err := database.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	task := mailout.NewTask("login_code", mailout.RouteLocal, mailout.Message{From: "sender@example.com", To: "owner@example.com"}, time.Now().Add(time.Hour), nil)
	task.ID = "old-message"
	task.InstallationID = "desktop"
	if _, err := mailout.Insert(boundary, database, task); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, event := range []Event{
		{ID: "legacy-queued-source", InstallationID: "desktop", MessageID: task.ID, FlowID: "old-flow", Kind: "login_code", Stage: "queued", CreatedAt: now},
		{ID: "legacy-queued-central", MessageID: task.ID, Kind: "login_code", Stage: "queued", CreatedAt: now},
		{ID: "legacy-smtp", MessageID: task.ID, Kind: "login_code", Stage: "accepted", AcceptedBy: "recipient_smtp", CreatedAt: now},
		{ID: "legacy-complete", InstallationID: "desktop", FlowID: "old-flow", Kind: "recovery", Stage: "recovery_complete", CreatedAt: now},
	} {
		if err := store.Append(boundary, event); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Rebuild(boundary); err != nil {
		t.Fatal(err)
	}
	counts, err := store.JournalCounts(boundary, "local", time.Time{})
	if err != nil || counts.Queued != 1 || counts.Accepted != 1 || counts.RecoveryComplete != 1 {
		t.Fatalf("legacy path missing: %+v %v", counts, err)
	}
}

func TestLateOriginSyncMergesLegacyMessageOnlyProjection(t *testing.T) {
	database := journalTestDatabase(t)
	store := Store{DB: database}
	boundary := context.Background()
	task := mailout.NewTask("login_code", mailout.RouteLocal, mailout.Message{From: "sender@example.com", To: "owner@example.com"}, time.Now().Add(time.Hour), nil)
	task.ID = "offline-message"
	task.InstallationID = "desktop"
	if _, err := mailout.Insert(boundary, database, task); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(boundary, Event{ID: "old-central-queued", MessageID: task.ID, Kind: "login_code", Stage: "queued"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(boundary, Event{ID: "old-central-accepted", MessageID: task.ID, Kind: "login_code", Stage: "accepted", AcceptedBy: "recipient_smtp"}); err != nil {
		t.Fatal(err)
	}
	metadata := &mailout.Metadata{OriginInstallationID: "desktop"}
	for _, event := range []Event{{ID: "late-queue", InstallationID: "desktop", MessageID: task.ID, FlowID: "offline-flow", Kind: "recovery", Stage: "queued", Metadata: metadata}, {ID: "late-complete", InstallationID: "desktop", FlowID: "offline-flow", Kind: "recovery", Stage: "recovery_complete", Metadata: metadata}} {
		if err := store.Append(boundary, event); err != nil {
			t.Fatal(err)
		}
	}
	operations, count, err := store.Journal(boundary, Filter{InstallationID: "local"})
	if err != nil || count != 1 || len(operations) != 1 || operations[0].Stage != "recovery_complete" {
		t.Fatalf("late sync fragmented operation: %+v %d %v", operations, count, err)
	}
	counts, err := store.JournalCounts(boundary, "local", time.Time{})
	if err != nil || counts.Queued != 1 || counts.Accepted != 1 || counts.RecoveryComplete != 1 {
		t.Fatalf("late counts=%+v error=%v", counts, err)
	}
}

func TestSMTPRetryStatusSurvivesLateOriginEvents(t *testing.T) {
	store := Store{DB: journalTestDatabase(t)}
	boundary := context.Background()
	metadata := &mailout.Metadata{OriginInstallationID: "desktop"}
	now := time.Now().UTC().Add(-time.Minute)
	for _, event := range []Event{
		{ID: "relay-start", FlowID: "retry-flow", MessageID: "retry-message", Route: "local", Kind: "login_code", Stage: "relay_accepted", Metadata: metadata, CreatedAt: now.Format(time.RFC3339Nano)},
		{ID: "smtp-retry", FlowID: "retry-flow", MessageID: "retry-message", Route: "local", Kind: "login_code", Stage: "retry", Metadata: metadata, CreatedAt: now.Add(2 * time.Second).Format(time.RFC3339Nano)},
		{ID: "origin-old-queue", InstallationID: "desktop", FlowID: "retry-flow", MessageID: "retry-message", Route: "relay", Kind: "login_code", Stage: "queued", Metadata: metadata, CreatedAt: now.Add(time.Second).Format(time.RFC3339Nano)},
	} {
		if err := store.Append(boundary, event); err != nil {
			t.Fatal(err)
		}
	}
	operations, count, err := store.Journal(boundary, Filter{InstallationID: "local", Stage: "retry"})
	if err != nil || count != 1 || len(operations) != 1 {
		t.Fatalf("SMTP retry hidden by relay acceptance: %+v %d %v", operations, count, err)
	}
}
