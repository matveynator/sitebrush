package updateannounce_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/hostingandsupport"
	"github.com/matveynator/sitebrush/v2/pkg/mailmonitor"
	"github.com/matveynator/sitebrush/v2/pkg/mailout"
	"github.com/matveynator/sitebrush/v2/pkg/updateannounce"
	_ "modernc.org/sqlite"
)

func newCampaignDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "campaigns.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	database.SetMaxOpenConns(1)
	if err := hostingandsupport.Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	return database
}
func TestUpdateNotificationTransactionFailureDoesNotSuppressRetry(t *testing.T) {
	database := newCampaignDatabase(t)
	seedAnnouncementOwner(t, database)
	if err := updateannounce.Start(database, "2", "feature", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`DROP TABLE mail_outbox`); err != nil {
		t.Fatal(err)
	}
	if err := updateannounce.Queue(database, time.Now()); err == nil {
		t.Fatal("accepted a delivery without durable outbox")
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(1) FROM release_campaign_deliveries`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed transaction suppressed retry", count, err)
	}
	for _, query := range mailout.SchemaQueries() {
		if _, err := database.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := updateannounce.Queue(database, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(1) FROM mail_outbox`).Scan(&count); err != nil || count != 1 {
		t.Fatal("campaign could not retry", count, err)
	}
}

func TestCampaignJournalDomainSurvivesOutboxReload(t *testing.T) {
	database := newCampaignDatabase(t)
	seedAnnouncementOwner(t, database)
	now := time.Now().UTC()
	if err := updateannounce.Start(database, "2", "feature", now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TRIGGER reject_journal BEFORE INSERT ON mail_monitor_events BEGIN SELECT RAISE(ABORT,'journal unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := updateannounce.Queue(database, now); err == nil {
		t.Fatal("campaign acknowledged an untracked message")
	}
	for _, table := range []string{"release_campaign_deliveries", "mail_outbox"} {
		var count int
		if err := database.QueryRow(`SELECT COUNT(1) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("journal failure left %s rows: %d, %v", table, count, err)
		}
	}
	if _, err := database.Exec(`DROP TRIGGER reject_journal`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := updateannounce.Queue(database, now); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	records, err := mailout.Due(ctx, database, now.Add(time.Minute), 10)
	if err != nil || len(records) != 1 {
		t.Fatalf("campaign outbox: %+v, %v", records, err)
	}
	record := records[0]
	store := mailmonitor.Store{DB: database}
	if err := store.Append(ctx, mailmonitor.Event{MessageID: record.ID, Kind: record.Kind, Stage: "attempt", Recipient: record.Message.To, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	events, err := store.RecentFiltered(ctx, mailmonitor.Filter{InstallationID: "local", Stage: "attempt", Limit: 10})
	if err != nil || len(events) != 1 {
		t.Fatalf("campaign journal: %+v, %v", events, err)
	}
	for _, event := range events {
		if event.Domain != "sitebrush.com" || event.Recipient != "owner@example.com" || event.MessageID != record.ID {
			t.Fatalf("campaign lost monitoring metadata: %+v", event)
		}
	}
}

func TestUpdateCampaignCancellationExpiryAndOneMailForSeveralInstallations(t *testing.T) {
	database := newCampaignDatabase(t)
	seedAnnouncementOwner(t, database)
	for _, query := range []string{
		`INSERT INTO service_mail_installations(installation_id,public_key,blocked) VALUES('installation-2','key-2',0)`,
		`INSERT INTO client_hostings(installation_id,installation_kind,server_domain) VALUES('installation-2','server','second.example')`,
		`INSERT INTO registry_installation_roles(installation_id,email,role,scope,domain) VALUES('installation-2','owner@example.com','superadmin','installation','')`,
	} {
		if _, err := database.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := (hostingandsupport.Store{DB: database}).UpsertServiceMailRecipient(context.Background(), "installation-2", "owner@example.com", "verified", "confirmed"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := updateannounce.Start(database, "21", "both", now); err != nil {
		t.Fatal(err)
	}
	if err := updateannounce.Queue(database, now); err != nil {
		t.Fatal(err)
	}
	_, installations, err := updateannounce.Delivery(database, "21", "owner@example.com", now)
	if err != nil || len(installations) != 2 {
		t.Fatal("owner's installations were not grouped", len(installations), err)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(1) FROM mail_outbox`).Scan(&count); err != nil || count != 1 {
		t.Fatal("several installations generated multiple emails", count, err)
	}
	if _, _, err := updateannounce.Delivery(database, "21", "owner@example.com", now.Add(8*24*time.Hour)); err == nil {
		t.Fatal("expired campaign remained deliverable")
	}
	if err := updateannounce.Cancel(database, "21"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := updateannounce.Delivery(database, "21", "owner@example.com", now); err == nil {
		t.Fatal("canceled campaign remained deliverable")
	}
}

func seedAnnouncementOwner(t *testing.T, database *sql.DB) {
	t.Helper()
	queries := []string{
		`INSERT INTO service_mail_installations(installation_id,public_key,blocked) VALUES('installation-1','test-key',0)`,
		`INSERT INTO client_hostings(installation_id,installation_kind,sitebrush_version,server_domain) VALUES('installation-1','server','1','server.example')`,
		`INSERT INTO registry_installation_roles(installation_id,email,role,scope,domain) VALUES('installation-1','owner@example.com','superadmin','installation','')`,
	}
	for _, query := range queries {
		if _, err := database.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := (hostingandsupport.Store{DB: database}).UpsertServiceMailRecipient(context.Background(), "installation-1", "owner@example.com", "verified", "confirmed"); err != nil {
		t.Fatal(err)
	}
}

func TestNoAutomaticAnnouncementAndDurableDeduplication(t *testing.T) {
	database := newCampaignDatabase(t)
	seedAnnouncementOwner(t, database)
	// Publishing and checking many releases does not authorize any email.
	for range 20 {
		if err := updateannounce.Queue(database, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(1) FROM mail_outbox`).Scan(&count); err != nil || count != 0 {
		t.Fatal("release check created an unsolicited email", err)
	}
	if err := updateannounce.Start(database, "2", "security", time.Now()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := updateannounce.Queue(database, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.QueryRow(`SELECT COUNT(1) FROM mail_outbox`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("campaign messages=%d err=%v", count, err)
	}
	if _, err := database.Exec(`DELETE FROM mail_outbox`); err != nil {
		t.Fatal(err)
	}
	if err := updateannounce.Start(database, "2", "feature", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := updateannounce.Queue(database, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(1) FROM mail_outbox`).Scan(&count); err != nil || count != 0 {
		t.Fatal("announcement repeated after retention cleanup", err)
	}
}

func TestCampaignReservationRollsBackWhenOutboxInsertFails(t *testing.T) {
	database := newCampaignDatabase(t)
	seedAnnouncementOwner(t, database)
	if err := updateannounce.Start(database, "21", "security", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TRIGGER fail_release_outbox BEFORE INSERT ON mail_outbox BEGIN SELECT RAISE(ABORT,'simulated disk write failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := updateannounce.Queue(database, time.Now()); err == nil {
		t.Fatal("outbox failure was hidden")
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(1) FROM release_campaign_deliveries`).Scan(&count); err != nil || count != 0 {
		t.Fatal("reservation survived rollback", count, err)
	}
	if _, err := database.Exec(`DROP TRIGGER fail_release_outbox`); err != nil {
		t.Fatal(err)
	}
	if err := updateannounce.Queue(database, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(1) FROM mail_outbox`).Scan(&count); err != nil || count != 1 {
		t.Fatal("retry did not queue exactly once", count, err)
	}
}

func TestCampaignValidationAndLiveAuthorizationChanges(t *testing.T) {
	database := newCampaignDatabase(t)
	seedAnnouncementOwner(t, database)
	now := time.Now().UTC()
	for _, version := range []string{"", "0", "development", "-1"} {
		if err := updateannounce.Start(database, version, "security", now); err == nil {
			t.Fatal("invalid version accepted", version)
		}
	}
	if err := updateannounce.Start(database, "21", "routine", now); err == nil {
		t.Fatal("routine campaign accepted")
	}
	if err := updateannounce.Start(database, "21", "security", now); err != nil {
		t.Fatal(err)
	}
	if err := updateannounce.Start(database, "21", "feature", now); err != nil {
		t.Fatal(err)
	}
	if err := updateannounce.Queue(database, now); err != nil {
		t.Fatal(err)
	}
	campaign, installations, err := updateannounce.Delivery(database, "21", "owner@example.com", now)
	if err != nil || len(installations) != 1 || campaign.Reason != "security" {
		t.Fatal(campaign, installations, err)
	}
	for _, queries := range [][]string{
		{`UPDATE service_mail_installations SET blocked=1`, `UPDATE service_mail_installations SET blocked=0`},
		{`UPDATE registry_installation_roles SET role='admin'`, `UPDATE registry_installation_roles SET role='superadmin'`},
		{`UPDATE service_mail_recipients SET status='blocked'`, `UPDATE service_mail_recipients SET status='verified'`},
	} {
		if _, err := database.Exec(queries[0]); err != nil {
			t.Fatal(err)
		}
		if _, installations, err := updateannounce.Delivery(database, "21", "owner@example.com", now); err != nil || len(installations) != 0 {
			t.Fatal("revoked authorization retained", installations, err)
		}
		if _, err := database.Exec(queries[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := updateannounce.Mark(database, "21", "owner@example.com", "unrecognized"); err == nil {
		t.Fatal("invalid delivery state accepted")
	}
	for _, status := range []string{"sent", "skipped", "failed"} {
		if err := updateannounce.Mark(database, "21", "owner@example.com", status); err != nil {
			t.Fatal(err)
		}
		campaigns, err := updateannounce.Campaigns(database)
		if err != nil || len(campaigns) != 1 {
			t.Fatal(campaigns, err)
		}
		observed := campaigns[0]
		if status == "sent" && observed.Sent != 1 || status == "skipped" && observed.Skipped != 1 || status == "failed" && observed.Failed != 1 {
			t.Fatal("wrong campaign counts", observed)
		}
	}
	if _, err := database.Exec(`UPDATE release_campaign_deliveries SET status='waiting'; UPDATE mail_outbox SET last_error='temporary SMTP failure'`); err != nil {
		t.Fatal(err)
	}
	campaigns, err := updateannounce.Campaigns(database)
	if err != nil || campaigns[0].Waiting != 1 || campaigns[0].LastError != "temporary SMTP failure" {
		t.Fatal(campaigns, err)
	}
	if err := updateannounce.Queue(database, now.Add(updateannounce.Retention)); err != nil {
		t.Fatal(err)
	}
	campaigns, err = updateannounce.Campaigns(database)
	if err != nil || campaigns[0].Failed != 1 {
		t.Fatal("expiry did not settle waiting delivery", campaigns, err)
	}
}

func TestCanceledCampaignSettlesAndCorruptRecordsFailClosed(t *testing.T) {
	database := newCampaignDatabase(t)
	seedAnnouncementOwner(t, database)
	now := time.Now().UTC()
	if err := updateannounce.Start(database, "21", "feature", now); err != nil {
		t.Fatal(err)
	}
	if err := updateannounce.Queue(database, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE release_campaign_deliveries SET installations='invalid JSON'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := updateannounce.Delivery(database, "21", "owner@example.com", now); err == nil {
		t.Fatal("corrupt recipient contact accepted")
	}
	if err := updateannounce.Cancel(database, "21"); err != nil {
		t.Fatal(err)
	}
	if err := updateannounce.Queue(database, now); err != nil {
		t.Fatal(err)
	}
	campaigns, err := updateannounce.Campaigns(database)
	if err != nil || !campaigns[0].Canceled || campaigns[0].Skipped != 1 {
		t.Fatal(campaigns, err)
	}
	if _, err := database.Exec(`UPDATE release_campaigns SET expires_at='invalid date'`); err != nil {
		t.Fatal(err)
	}
	if err := updateannounce.Queue(database, now); err == nil {
		t.Fatal("corrupt expiry accepted")
	}
	if _, _, err := updateannounce.Delivery(database, "21", "owner@example.com", now); err == nil {
		t.Fatal("corrupt delivery expiry accepted")
	}
	if _, err := database.Exec(`DROP TABLE release_campaigns`); err != nil {
		t.Fatal(err)
	}
	if _, err := updateannounce.Campaigns(database); err == nil {
		t.Fatal("missing campaign store accepted")
	}
}

func TestOnlyConfirmedServerOwnersAreQueued(t *testing.T) {
	database := newCampaignDatabase(t)
	seedAnnouncementOwner(t, database)
	now := time.Now().UTC()
	if err := updateannounce.Start(database, "21", "feature", now); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{`UPDATE service_mail_recipients SET status='pending'`, `UPDATE service_mail_recipients SET status='verified'; UPDATE client_hostings SET installation_kind='desktop'`, `UPDATE client_hostings SET installation_kind='server'; UPDATE registry_installation_roles SET scope='site'`} {
		if _, err := database.Exec(mutation); err != nil {
			t.Fatal(err)
		}
		if err := updateannounce.Queue(database, now); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := database.QueryRow(`SELECT COUNT(1) FROM release_campaign_deliveries`).Scan(&count); err != nil || count != 0 {
			t.Fatal("ineligible owner queued", count, err)
		}
	}
}
