package analytics

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/matveynator/sitebrush/v2/pkg/database/drivers"
)

func registrationTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	for _, query := range RegistrationSchemaQueries() {
		if _, err := database.Exec(query); err != nil {
			database.Close()
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func registrationTestAttempt(t *testing.T, database *sql.DB, domain, workflow, source string, now time.Time) string {
	t.Helper()
	raw, err := NewRegistrationID()
	if err != nil {
		t.Fatal(err)
	}
	id := RegistrationIDHash(raw)
	if err := StartRegistration(context.Background(), database, id, RegistrationScope(domain), workflow, source, now, now.Add(6*time.Hour)); err != nil {
		t.Fatal(err)
	}
	return id
}

func registrationTestStage(t *testing.T, database *sql.DB, id, stage, detail string, at time.Time) {
	t.Helper()
	if err := RecordRegistration(context.Background(), database, id, stage, detail, at); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrationCohortCountsAuthoritativeOutcomesAndRetries(t *testing.T) {
	database := registrationTestDatabase(t)
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	publicID := registrationTestAttempt(t, database, "one.sitebrush.com", "public", "mail-one", now.Add(-2*time.Hour))
	testDriveID := registrationTestAttempt(t, database, "two.sitebrush.com", "test_drive", "test-drive-2", now.Add(-3*time.Hour))
	igniteID := registrationTestAttempt(t, database, "three.sitebrush.com", "ignite", "ignite-3", now.Add(-4*time.Hour))
	failedID := registrationTestAttempt(t, database, "four.sitebrush.com", "public", "", now.Add(-5*time.Hour))
	registrationTestStage(t, database, publicID, "domain_check_completed", "available", now.Add(-2*time.Hour))
	registrationTestStage(t, database, publicID, "captcha_completed", "", now.Add(-time.Hour))
	registrationTestStage(t, database, publicID, "registration_failed", "email_code", now.Add(-50*time.Minute))
	for _, stage := range []string{"email_confirmation_sent", "email_confirmed", "user_created", "site_created", "public_site_created", "registration_completed"} {
		registrationTestStage(t, database, publicID, stage, "", now.Add(-time.Minute))
		registrationTestStage(t, database, publicID, stage, "", now)
	}
	registrationTestStage(t, database, testDriveID, "site_created", "", now.Add(-2*time.Hour))
	registrationTestStage(t, database, igniteID, "domain_check_completed", "available", now.Add(-4*time.Hour))
	registrationTestStage(t, database, igniteID, "registration_failed", "captcha", now.Add(-3*time.Hour))
	registrationTestStage(t, database, failedID, "domain_check_completed", "unavailable", now.Add(-5*time.Hour))
	registrationTestStage(t, database, failedID, "registration_failed", "domain_unavailable", now.Add(-5*time.Hour))
	report, err := RegistrationReportForPeriod(context.Background(), database, "", true, now.Add(-24*time.Hour), now.Add(time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Cohort.Started != 4 || report.Cohort.Completed != 1 || report.Cohort.Failed != 1 || report.Cohort.Unfinished != 2 || report.Cohort.RecoveredErrors != 1 || report.Cohort.ConversionPercent != 25 {
		t.Fatalf("incorrect cohort: %+v", report.Cohort)
	}
	if report.Outcomes.SitesCreated != 2 || report.Outcomes.PublicSitesCreated != 1 || report.Outcomes.UsersCreated != 1 || report.Outcomes.Completed != 1 {
		t.Fatalf("duplicate or missing outcome: %+v", report.Outcomes)
	}
	if report.Workflows["test_drive"].Stages["site_created"] != 1 || report.Workflows["test_drive"].Completed != 0 || report.Workflows["ignite"].Unfinished != 1 {
		t.Fatalf("workflow boundaries lost: %+v", report.Workflows)
	}
	siteReport, err := RegistrationReportForPeriod(context.Background(), database, RegistrationScope("one.sitebrush.com"), false, now.Add(-24*time.Hour), now.Add(time.Minute), now)
	if err != nil || siteReport.Cohort.Started != 1 || siteReport.Outcomes.SitesCreated != 1 || siteReport.Scope != "site" {
		t.Fatalf("site owner saw another site or lost own outcome: %+v %v", siteReport, err)
	}
}

func TestRegistrationReportExcludesOperatorCreatedSites(t *testing.T) {
	database := registrationTestDatabase(t)
	now := time.Now().UTC()
	publicID := registrationTestAttempt(t, database, "public.example.net", "public", "mail-public", now)
	registrationTestStage(t, database, publicID, "site_created", "", now)
	registrationTestStage(t, database, publicID, "registration_completed", "", now)
	for _, workflow := range []string{"local", "approval", "admin_created"} {
		id := registrationTestAttempt(t, database, workflow+".example.net", workflow, "", now)
		registrationTestStage(t, database, id, "site_created", "", now)
		registrationTestStage(t, database, id, "registration_completed", "", now)
	}
	report, err := RegistrationReportForPeriod(context.Background(), database, "", true, now.Add(-time.Minute), now.Add(time.Minute), now)
	if err != nil || report.Cohort.Started != 1 || report.Cohort.Completed != 1 || report.Outcomes.SitesCreated != 1 || report.Outcomes.Completed != 1 || len(report.Workflows) != 1 {
		t.Fatalf("operator-created sites changed self-service conversion: %+v %v", report, err)
	}
}

func TestRegistrationDelayedOutcomeExpiryAndPrivateSources(t *testing.T) {
	database := registrationTestDatabase(t)
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	oldID := registrationTestAttempt(t, database, "old.sitebrush.com", "public", "mail-old", now.Add(-36*time.Hour))
	registrationTestStage(t, database, oldID, "site_created", "", now.Add(-time.Hour))
	registrationTestStage(t, database, oldID, "registration_completed", "", now.Add(-time.Hour))
	expiredID := registrationTestAttempt(t, database, "expired.sitebrush.com", "public", "", now.Add(-8*time.Hour))
	registrationTestStage(t, database, expiredID, "registration_failed", "email_code", now.Add(-7*time.Hour))
	report, err := RegistrationReportForPeriod(context.Background(), database, "", true, now.Add(-24*time.Hour), now, now)
	if err != nil || report.Cohort.Started != 1 || report.Cohort.Expired != 1 || report.Cohort.Completed != 0 || report.Outcomes.SitesCreated != 1 || report.Outcomes.Completed != 1 {
		t.Fatalf("cohort and outcome dates were mixed: %+v %v", report, err)
	}
	if err := LinkRegistrationSource(context.Background(), database, oldID, "mail-old-retry"); err != nil {
		t.Fatal(err)
	}
	if linked, err := RegistrationIDForSource(context.Background(), database, "mail-old-retry"); err != nil || linked != oldID {
		t.Fatalf("source link lost after retry: %q %v", linked, err)
	}
	if err := CleanupRegistration(context.Background(), database, now.Add(-25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := RegistrationIDForSource(context.Background(), database, "mail-old"); err != sql.ErrNoRows {
		t.Fatalf("expired correlation source survived cleanup: %v", err)
	}
}

func TestRegistrationIdentifiersAndMetadataRejectSecrets(t *testing.T) {
	raw, err := NewRegistrationID()
	if err != nil || len(raw) != 48 || RegistrationIDHash(raw) == raw || RegistrationIDHash("owner@example.org") != "" {
		t.Fatalf("unsafe registration identifier: %q %v", raw, err)
	}
	database := registrationTestDatabase(t)
	now := time.Now().UTC()
	id := registrationTestAttempt(t, database, "private.sitebrush.com", "public", "", now)
	if err := RecordRegistration(context.Background(), database, id, "user_created", "owner@example.org", now); err == nil {
		t.Fatal("analytics accepted an email address as metadata")
	}
	if err := LinkRegistrationSource(context.Background(), database, id, strings.Repeat("x", 129)); err == nil {
		t.Fatal("analytics accepted an unbounded source")
	}
	if err := LinkRegistrationSource(context.Background(), database, id, "test-drive-123"); err != nil {
		t.Fatal(err)
	}
	var storedSource string
	if err := database.QueryRow(`SELECT source_ref FROM registration_analytics_sources`).Scan(&storedSource); err != nil || storedSource == "test-drive-123" || len(storedSource) != 64 {
		t.Fatalf("source metadata contains a raw workflow handle: %q %v", storedSource, err)
	}
}

func TestRegistrationConcurrentCompletionIsOneOutcome(t *testing.T) {
	database := registrationTestDatabase(t)
	now := time.Now().UTC()
	id := registrationTestAttempt(t, database, "parallel.sitebrush.com", "public", "mail-parallel", now)
	replies := make(chan error, 24)
	for range 24 {
		go func() {
			replies <- RecordRegistration(context.Background(), database, id, "registration_completed", "", now)
		}()
	}
	for range 24 {
		if err := <-replies; err != nil {
			t.Fatal(err)
		}
	}
	report, err := RegistrationReportForPeriod(context.Background(), database, "", true, now.Add(-time.Hour), now.Add(time.Hour), now)
	if err != nil || report.Cohort.Completed != 1 || report.Outcomes.Completed != 1 {
		t.Fatalf("concurrent requests duplicated completion: %+v %v", report, err)
	}
}

func TestRegistrationCorrelationSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	open := func() *sql.DB {
		database, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		database.SetMaxOpenConns(1)
		return database
	}
	database := open()
	for _, query := range RegistrationSchemaQueries() {
		if _, err := database.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	id := registrationTestAttempt(t, database, "restart.sitebrush.com", "public", "mail-restart", now)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database = open()
	defer database.Close()
	restored, err := RegistrationIDForSource(context.Background(), database, "mail-restart")
	if err != nil || restored != id {
		t.Fatalf("restart lost source correlation: %q %v", restored, err)
	}
	registrationTestStage(t, database, id, "site_created", "", now.Add(time.Minute))
	registrationTestStage(t, database, id, "registration_completed", "", now.Add(time.Minute))
	report, err := RegistrationReportForPeriod(context.Background(), database, RegistrationScope("restart.sitebrush.com"), false, now.Add(-time.Minute), now.Add(2*time.Minute), now.Add(2*time.Minute))
	if err != nil || report.Outcomes.SitesCreated != 1 || report.Outcomes.Completed != 1 {
		t.Fatalf("restart lost completed outcome: %+v %v", report, err)
	}
}

func TestRegistrationCommittedOutcomeRecoversAfterAnalyticsStartFailure(t *testing.T) {
	database := registrationTestDatabase(t)
	now := time.Now().UTC()
	firstID, err := RecoverRegistrationForSource(context.Background(), database, "recovered.sitebrush.com", "public", "mail-committed", now)
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := RecoverRegistrationForSource(context.Background(), database, "recovered.sitebrush.com", "public", "mail-committed", now)
	if err != nil || secondID != firstID {
		t.Fatalf("committed source created a second attempt: %q %q %v", firstID, secondID, err)
	}
	registrationTestStage(t, database, firstID, "user_created", "", now)
	registrationTestStage(t, database, firstID, "site_created", "", now)
	registrationTestStage(t, database, firstID, "registration_completed", "", now)
	report, err := RegistrationReportForPeriod(context.Background(), database, RegistrationScope("recovered.sitebrush.com"), false, now.Add(-time.Hour), now.Add(time.Hour), now)
	if err != nil || report.Cohort.Started != 1 || report.Outcomes.SitesCreated != 1 || report.Outcomes.Completed != 1 {
		t.Fatalf("committed outcome was lost: %+v %v", report, err)
	}
}
