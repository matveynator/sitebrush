package analytics

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// Registration events live in the control database, alongside the durable
// registration state. Browser observations never enter this ledger.
const RegistrationRetention = 120 * 24 * time.Hour

func RegistrationSchemaQueries() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS registration_analytics_attempts(id TEXT PRIMARY KEY,site_scope TEXT NOT NULL,workflow TEXT NOT NULL,started_at TEXT NOT NULL,expires_at TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS registration_analytics_attempts_scope_time ON registration_analytics_attempts(site_scope,started_at)`,
		`CREATE TABLE IF NOT EXISTS registration_analytics_events(attempt_id TEXT NOT NULL,stage TEXT NOT NULL,detail TEXT NOT NULL DEFAULT '',occurred_at TEXT NOT NULL,PRIMARY KEY(attempt_id,stage,detail))`,
		`CREATE INDEX IF NOT EXISTS registration_analytics_events_time ON registration_analytics_events(stage,occurred_at)`,
		`CREATE TABLE IF NOT EXISTS registration_analytics_sources(source_ref TEXT PRIMARY KEY,attempt_id TEXT NOT NULL)`,
	}
}

// The raw ID is a short-lived, non-authorizing handoff value. Only its digest
// is stored in analytics and it is never placed in a URL.
func NewRegistrationID() (string, error) {
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func RegistrationIDHash(raw string) string {
	if len(raw) != 48 {
		return ""
	}
	if _, err := hex.DecodeString(raw); err != nil {
		return ""
	}
	digest := sha256.Sum256([]byte("sitebrush registration attempt\n" + raw))
	return hex.EncodeToString(digest[:])
}

// Scope is an internal lookup key, never returned by reports. The control DB
// already owns domain names; this avoids copying them into analytics rows.
func RegistrationScope(domain string) string {
	digest := sha256.Sum256([]byte("sitebrush registration site\n" + strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))))
	return hex.EncodeToString(digest[:])
}

type RegistrationSQL interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func StartRegistration(ctx context.Context, database RegistrationSQL, id, scope, workflow, sourceRef string, now, expires time.Time) error {
	if len(id) != 64 || len(scope) != 64 || !validRegistrationWorkflow(workflow) || !expires.After(now) {
		return errors.New("invalid registration analytics attempt")
	}
	_, err := database.ExecContext(ctx, `INSERT INTO registration_analytics_attempts(id,site_scope,workflow,started_at,expires_at) SELECT ?,?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM registration_analytics_attempts WHERE id=?)`, id, scope, workflow, now.UTC().Format(time.RFC3339Nano), expires.UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	if sourceRef != "" {
		if err := LinkRegistrationSource(ctx, database, id, sourceRef); err != nil {
			return err
		}
	}
	return RecordRegistration(ctx, database, id, "registration_started", "", now)
}

func validRegistrationWorkflow(workflow string) bool {
	switch workflow {
	case "public", "test_drive", "ignite", "local", "approval", "admin_created":
		return true
	}
	return false
}

func RegistrationAttemptValid(ctx context.Context, database RegistrationSQL, id, scope, workflow string, now time.Time) bool {
	if len(id) != 64 || len(scope) != 64 || !validRegistrationWorkflow(workflow) {
		return false
	}
	var count int
	err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM registration_analytics_attempts WHERE id=? AND site_scope=? AND workflow=? AND expires_at>?`, id, scope, workflow, now.UTC().Format(time.RFC3339Nano)).Scan(&count)
	return err == nil && count == 1
}

func LinkRegistrationSource(ctx context.Context, database RegistrationSQL, id, sourceRef string) error {
	if len(id) != 64 || sourceRef == "" || len(sourceRef) > 128 {
		return errors.New("invalid registration analytics source")
	}
	sourceKey := registrationSourceKey(sourceRef)
	_, err := database.ExecContext(ctx, `INSERT INTO registration_analytics_sources(source_ref,attempt_id) SELECT ?,? WHERE EXISTS(SELECT 1 FROM registration_analytics_attempts WHERE id=?) AND NOT EXISTS(SELECT 1 FROM registration_analytics_sources WHERE source_ref=?)`, sourceKey, id, id, sourceKey)
	return err
}

func RegistrationIDForSource(ctx context.Context, database RegistrationSQL, sourceRef string) (string, error) {
	if sourceRef == "" || len(sourceRef) > 128 {
		return "", errors.New("invalid registration analytics source")
	}
	var id string
	err := database.QueryRowContext(ctx, `SELECT attempt_id FROM registration_analytics_sources WHERE source_ref=?`, registrationSourceKey(sourceRef)).Scan(&id)
	return id, err
}

// A committed workflow can outlive an analytics outage. Its private source
// reference lets the server recover a confirmed outcome without client input.
func RecoverRegistrationForSource(ctx context.Context, database RegistrationSQL, domain, workflow, sourceRef string, occurred time.Time) (string, error) {
	id, err := RegistrationIDForSource(ctx, database, sourceRef)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return id, err
	}
	rawID, err := NewRegistrationID()
	if err != nil {
		return "", err
	}
	id = RegistrationIDHash(rawID)
	if err := StartRegistration(ctx, database, id, RegistrationScope(domain), workflow, sourceRef, occurred, occurred.Add(24*time.Hour)); err != nil {
		return "", err
	}
	return RegistrationIDForSource(ctx, database, sourceRef)
}

func registrationSourceKey(sourceRef string) string {
	digest := sha256.Sum256([]byte("sitebrush registration source\n" + sourceRef))
	return hex.EncodeToString(digest[:])
}

func validRegistrationEvent(stage, detail string) bool {
	switch stage {
	case "registration_failed":
		switch detail {
		case "domain_unavailable", "captcha", "email_code", "email_expired", "mail_delivery", "site_provision", "account_creation", "rejected", "technical":
			return true
		}
		return false
	case "domain_check_completed":
		return detail == "available" || detail == "unavailable"
	case "registration_started", "captcha_completed", "email_confirmation_sent", "email_confirmed", "user_created", "site_created", "public_site_created", "registration_completed":
		return detail == ""
	}
	return false
}

// A unique key makes a retry or refresh an acknowledgement, not another event.
func RecordRegistration(ctx context.Context, database RegistrationSQL, id, stage, detail string, now time.Time) error {
	if len(id) != 64 || !validRegistrationEvent(stage, detail) {
		return errors.New("invalid registration analytics event")
	}
	_, err := database.ExecContext(ctx, `INSERT INTO registration_analytics_events(attempt_id,stage,detail,occurred_at) SELECT ?,?,?,? WHERE EXISTS(SELECT 1 FROM registration_analytics_attempts WHERE id=?) AND NOT EXISTS(SELECT 1 FROM registration_analytics_events WHERE attempt_id=? AND stage=? AND detail=?)`, id, stage, detail, now.UTC().Format(time.RFC3339Nano), id, id, stage, detail)
	return err
}

type RegistrationCohort struct {
	Started           int                      `json:"started"`
	Completed         int                      `json:"completed"`
	Failed            int                      `json:"failed"`
	Errored           int                      `json:"errored"`
	Expired           int                      `json:"expired"`
	Unfinished        int                      `json:"unfinished"`
	RecoveredErrors   int                      `json:"recovered_errors"`
	ConversionPercent float64                  `json:"conversion_percent"`
	Stages            map[string]int           `json:"stages"`
	Dropoff           map[string]int           `json:"dropoff"`
	FailureDetails    map[string]int           `json:"failure_details"`
	Funnel            []RegistrationFunnelStep `json:"funnel"`
}

type RegistrationFunnelStep struct {
	Stage      string `json:"stage"`
	Count      int    `json:"count"`
	Dropoff    int    `json:"dropoff"`
	Comparable bool   `json:"comparable"`
}

type RegistrationOutcomes struct {
	SitesCreated       int `json:"sites_created"`
	PublicSitesCreated int `json:"public_sites_created"`
	UsersCreated       int `json:"users_created"`
	Completed          int `json:"completed"`
}

type RegistrationReport struct {
	Source       string                        `json:"source"`
	Scope        string                        `json:"scope"`
	Days         int                           `json:"days"`
	PeriodStart  time.Time                     `json:"period_start"`
	PeriodEnd    time.Time                     `json:"period_end"`
	CoverageFrom *time.Time                    `json:"coverage_from,omitempty"`
	Cohort       RegistrationCohort            `json:"cohort"`
	Outcomes     RegistrationOutcomes          `json:"outcomes"`
	Workflows    map[string]RegistrationCohort `json:"workflows"`
}

func newRegistrationCohort() RegistrationCohort {
	return RegistrationCohort{Stages: make(map[string]int), Dropoff: make(map[string]int), FailureDetails: make(map[string]int)}
}

// RegistrationReportForPeriod uses starts as the funnel denominator. Outcomes
// use their own occurrence dates so delayed confirmations belong to the month
// in which the account or site was actually created.
func RegistrationReportForPeriod(ctx context.Context, database RegistrationSQL, siteScope string, allSites bool, start, end, now time.Time) (RegistrationReport, error) {
	scope := "site"
	if allSites {
		scope = "installation"
	}
	report := RegistrationReport{Source: "server", Scope: scope, Days: int(end.Sub(start).Hours() / 24), PeriodStart: start, PeriodEnd: end, Cohort: newRegistrationCohort(), Workflows: map[string]RegistrationCohort{}}
	query := `SELECT attempt.id,attempt.workflow,attempt.started_at,attempt.expires_at,event.stage,event.detail,event.occurred_at FROM registration_analytics_attempts attempt LEFT JOIN registration_analytics_events event ON event.attempt_id=attempt.id WHERE attempt.started_at>=? AND attempt.started_at<?`
	args := []any{start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano)}
	if !allSites {
		query += ` AND attempt.site_scope=?`
		args = append(args, siteScope)
	}
	query += ` ORDER BY attempt.id`
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return report, err
	}
	type attemptState struct {
		workflow string
		expires  time.Time
		stages   map[string]bool
		failures map[string]bool
	}
	attempts := map[string]*attemptState{}
	for rows.Next() {
		var id, workflow, startedText, expiresText string
		var stage, detail, occurred sql.NullString
		if err = rows.Scan(&id, &workflow, &startedText, &expiresText, &stage, &detail, &occurred); err != nil {
			break
		}
		state := attempts[id]
		if state == nil {
			expires, _ := time.Parse(time.RFC3339Nano, expiresText)
			state = &attemptState{workflow: workflow, expires: expires, stages: map[string]bool{}, failures: map[string]bool{}}
			attempts[id] = state
			started, _ := time.Parse(time.RFC3339Nano, startedText)
			if report.CoverageFrom == nil || started.Before(*report.CoverageFrom) {
				report.CoverageFrom = &started
			}
		}
		if stage.Valid {
			state.stages[stage.String] = true
			if stage.String == "registration_failed" {
				state.failures[detail.String] = true
			}
			if stage.String == "domain_check_completed" && detail.String == "available" {
				state.stages["domain_available"] = true
			}
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return report, err
	}
	for _, attempt := range attempts {
		if !attempt.stages["registration_started"] {
			continue
		}
		cohort := report.Workflows[attempt.workflow]
		if cohort.Stages == nil {
			cohort = newRegistrationCohort()
		}
		addRegistrationAttempt(&report.Cohort, attempt.stages, attempt.failures, attempt.expires, now)
		addRegistrationAttempt(&cohort, attempt.stages, attempt.failures, attempt.expires, now)
		report.Workflows[attempt.workflow] = cohort
	}
	finishRegistrationCohort(&report.Cohort, "")
	for workflow, cohort := range report.Workflows {
		finishRegistrationCohort(&cohort, workflow)
		report.Workflows[workflow] = cohort
	}
	outcomeQuery := `SELECT event.stage,COUNT(*) FROM registration_analytics_events event JOIN registration_analytics_attempts attempt ON attempt.id=event.attempt_id WHERE event.occurred_at>=? AND event.occurred_at<? AND event.stage IN ('site_created','public_site_created','user_created','registration_completed')`
	if !allSites {
		outcomeQuery += ` AND attempt.site_scope=?`
	}
	outcomeQuery += ` GROUP BY event.stage`
	outcomeRows, err := database.QueryContext(ctx, outcomeQuery, args...)
	if err != nil {
		return report, err
	}
	for outcomeRows.Next() {
		var stage string
		var count int
		if err = outcomeRows.Scan(&stage, &count); err != nil {
			break
		}
		switch stage {
		case "site_created":
			report.Outcomes.SitesCreated = count
		case "public_site_created":
			report.Outcomes.PublicSitesCreated = count
		case "user_created":
			report.Outcomes.UsersCreated = count
		case "registration_completed":
			report.Outcomes.Completed = count
		}
	}
	if err == nil {
		err = outcomeRows.Err()
	}
	outcomeRows.Close()
	return report, err
}

func addRegistrationAttempt(cohort *RegistrationCohort, stages, failures map[string]bool, expires, now time.Time) {
	cohort.Started++
	for stage := range stages {
		cohort.Stages[stage]++
	}
	if stages["registration_failed"] {
		cohort.Errored++
	}
	for detail := range failures {
		cohort.FailureDetails[detail]++
	}
	switch {
	case stages["registration_completed"]:
		cohort.Completed++
		if stages["registration_failed"] {
			cohort.RecoveredErrors++
		}
	case failures["domain_unavailable"] || failures["email_expired"] || failures["rejected"]:
		cohort.Failed++
	case !expires.IsZero() && !now.Before(expires):
		cohort.Expired++
	default:
		cohort.Unfinished++
	}
}

func finishRegistrationCohort(cohort *RegistrationCohort, workflow string) {
	if cohort.Started > 0 {
		cohort.ConversionPercent = 100 * float64(cohort.Completed) / float64(cohort.Started)
	}
	order := []string{"registration_completed"}
	switch workflow {
	case "public", "ignite":
		order = []string{"domain_available", "captcha_completed", "email_confirmation_sent", "email_confirmed", "user_created", "site_created", "registration_completed"}
	case "test_drive":
		order = []string{"captcha_completed", "domain_available", "site_created", "email_confirmation_sent", "email_confirmed", "user_created", "registration_completed"}
	case "approval":
		order = []string{"captcha_completed", "domain_available", "user_created", "site_created", "registration_completed"}
	case "local", "admin_created":
		order = []string{"user_created", "site_created", "registration_completed"}
	}
	cohort.Funnel = []RegistrationFunnelStep{{Stage: "registration_started", Count: cohort.Started, Comparable: true}}
	previous := cohort.Started
	for _, stage := range order {
		current := cohort.Stages[stage]
		if stage != "registration_completed" && current == 0 {
			continue
		}
		step := RegistrationFunnelStep{Stage: stage, Count: current, Comparable: previous >= current}
		if step.Comparable {
			step.Dropoff = previous - current
			cohort.Dropoff[stage] = step.Dropoff
		}
		cohort.Funnel = append(cohort.Funnel, step)
		previous = current
	}
}

func CleanupRegistration(ctx context.Context, database RegistrationSQL, before time.Time) error {
	cutoff := before.UTC().Format(time.RFC3339Nano)
	if _, err := database.ExecContext(ctx, `DELETE FROM registration_analytics_sources WHERE attempt_id IN (SELECT id FROM registration_analytics_attempts WHERE started_at<?)`, cutoff); err != nil {
		return err
	}
	if _, err := database.ExecContext(ctx, `DELETE FROM registration_analytics_events WHERE attempt_id IN (SELECT id FROM registration_analytics_attempts WHERE started_at<?)`, cutoff); err != nil {
		return err
	}
	_, err := database.ExecContext(ctx, `DELETE FROM registration_analytics_attempts WHERE started_at<?`, cutoff)
	return err
}
