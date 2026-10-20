package mailmonitor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const Retention = 90 * 24 * time.Hour
const timestampLayout = "2006-01-02T15:04:05.000000000Z"

type Event struct {
	ID             string `json:"id"`
	InstallationID string `json:"installation_id"`
	FlowID         string `json:"flow_id,omitempty"`
	MessageID      string `json:"message_id,omitempty"`
	Kind           string `json:"kind"`
	Stage          string `json:"stage"`
	Domain         string `json:"domain,omitempty"`
	Recipient      string `json:"recipient,omitempty"`
	Route          string `json:"route,omitempty"`
	AcceptedBy     string `json:"accepted_by,omitempty"`
	Attempt        int    `json:"attempt,omitempty"`
	SMTPCode       string `json:"smtp_code,omitempty"`
	Detail         string `json:"detail,omitempty"`
	CreatedAt      string `json:"created_at"`
}

type Counts struct {
	Queued                int
	Accepted              int
	Failed                int
	RegistrationRequested int
	RegistrationComplete  int
	RecoveryRequested     int
	RecoveryComplete      int
}

type Filter struct {
	InstallationID string
	Since          time.Time
	Stage          string
	Kind           string
	Recipient      string
	Limit          int
	Offset         int
}

type Database interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type Store struct{ DB Database }

func SchemaQueries() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS mail_monitor_sync(installation_id TEXT PRIMARY KEY,received_at TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS mail_monitor_events(id TEXT NOT NULL,installation_id TEXT NOT NULL,flow_id TEXT NOT NULL DEFAULT '',message_id TEXT NOT NULL DEFAULT '',kind TEXT NOT NULL,stage TEXT NOT NULL,domain TEXT NOT NULL DEFAULT '',recipient TEXT NOT NULL DEFAULT '',route TEXT NOT NULL DEFAULT '',accepted_by TEXT NOT NULL DEFAULT '',attempt INTEGER NOT NULL DEFAULT 0,smtp_code TEXT NOT NULL DEFAULT '',detail TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,synced_at TEXT NOT NULL DEFAULT '',PRIMARY KEY(installation_id,id));`,
		`CREATE INDEX IF NOT EXISTS idx_mail_monitor_installation_created ON mail_monitor_events(installation_id,created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_mail_monitor_unsynced ON mail_monitor_events(installation_id,synced_at,created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_mail_monitor_message ON mail_monitor_events(message_id,created_at);`,
	}
}

func NewID() string {
	var randomBytes [16]byte
	if _, err := rand.Read(randomBytes[:]); err == nil {
		return hex.EncodeToString(randomBytes[:])
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func (store Store) Append(ctx context.Context, event Event) error {
	if store.DB == nil {
		return errors.New("mail monitor database is unavailable")
	}
	if event.ID == "" {
		identity := event.MessageID
		if identity == "" {
			identity = event.FlowID
		}
		if identity == "" {
			event.ID = NewID()
		} else {
			attempt := event.Attempt
			if event.Stage == "accepted" || event.Stage == "failed" {
				attempt = 0
			}
			digest := sha256.Sum256([]byte(fmt.Sprintf("%s/%s/%d", identity, event.Stage, attempt)))
			event.ID = hex.EncodeToString(digest[:])
		}
	}
	if event.InstallationID == "" {
		event.InstallationID = "local"
	}
	if event.CreatedAt == "" {
		event.CreatedAt = time.Now().UTC().Format(timestampLayout)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, event.CreatedAt)
	if err != nil {
		return err
	}
	event.CreatedAt = createdAt.UTC().Format(timestampLayout)
	for _, field := range []*string{&event.Kind, &event.Stage, &event.Route, &event.AcceptedBy, &event.SMTPCode} {
		*field = boundedField(*field, 60)
	}
	for _, field := range []*string{&event.ID, &event.FlowID, &event.MessageID} {
		*field = boundedField(*field, 128)
	}
	event.Domain = boundedField(event.Domain, 253)
	event.Recipient = boundedField(event.Recipient, 320)
	if event.Detail != "" {
		event.Detail = SafeError(errors.New(event.Detail))
	}
	// Later attempts are reconstructed from the outbox; inherit non-secret flow
	// metadata from the first event so it survives process restarts.
	if event.MessageID != "" && event.FlowID == "" {
		rows, queryErr := store.DB.QueryContext(ctx, `SELECT flow_id,domain FROM mail_monitor_events WHERE installation_id=? AND message_id=? AND stage='queued' LIMIT 1`, event.InstallationID, event.MessageID)
		if queryErr != nil {
			return queryErr
		}
		if rows.Next() {
			queryErr = rows.Scan(&event.FlowID, &event.Domain)
		}
		if queryErr == nil {
			queryErr = rows.Err()
		}
		rows.Close()
		if queryErr != nil {
			return queryErr
		}
	}
	_, err = store.DB.ExecContext(ctx, `INSERT INTO mail_monitor_events(id,installation_id,flow_id,message_id,kind,stage,domain,recipient,route,accepted_by,attempt,smtp_code,detail,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(installation_id,id) DO NOTHING`,
		event.ID, event.InstallationID, event.FlowID, event.MessageID, event.Kind, event.Stage, event.Domain, event.Recipient, event.Route, event.AcceptedBy, event.Attempt, event.SMTPCode, event.Detail, event.CreatedAt)
	return err
}

// Validate the complete batch before writing so malformed events cannot cause
// partial acknowledgment or unbounded storage growth on the receiving server.
func ValidateBatch(events []Event) error {
	if len(events) > 80 {
		return errors.New("mail monitor batch must contain at most 80 events")
	}
	for _, event := range events {
		if event.ID == "" || event.Kind == "" || event.Stage == "" {
			return errors.New("mail monitor event is incomplete")
		}
		for _, field := range []string{event.ID, event.FlowID, event.MessageID} {
			if len(field) > 128 || field != boundedField(field, 128) {
				return errors.New("mail monitor identifier is invalid")
			}
		}
		for _, field := range []string{event.Kind, event.Stage, event.Route, event.AcceptedBy, event.SMTPCode} {
			if len(field) > 60 || field != boundedField(field, 60) {
				return errors.New("mail monitor field is invalid")
			}
		}
		if event.Domain != boundedField(event.Domain, 253) || event.Recipient != boundedField(event.Recipient, 320) {
			return errors.New("mail monitor address is invalid")
		}
		if len(event.Detail) > 500 || event.Attempt < 0 {
			return errors.New("mail monitor diagnostic is invalid")
		}
		createdAt, err := time.Parse(time.RFC3339Nano, event.CreatedAt)
		if err != nil || createdAt.After(time.Now().Add(5*time.Minute)) {
			return errors.New("mail monitor timestamp is invalid")
		}
	}
	return nil
}

func (store Store) Import(ctx context.Context, installationID string, events []Event) error {
	if strings.TrimSpace(installationID) == "" || strings.EqualFold(strings.TrimSpace(installationID), "local") {
		return errors.New("remote installation ID is required")
	}
	if err := ValidateBatch(events); err != nil {
		return err
	}
	for _, event := range events {
		if event.ID == "" || event.Kind == "" || event.Stage == "" || event.CreatedAt == "" {
			return errors.New("mail monitor event is incomplete")
		}
		event.InstallationID = installationID
		event.Detail = SafeError(errors.New(event.Detail))
		if err := store.Append(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (store Store) Unsynced(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 || limit > 80 {
		limit = 80
	}
	rows, err := store.DB.QueryContext(ctx, `SELECT id,installation_id,flow_id,message_id,kind,stage,domain,recipient,route,accepted_by,attempt,smtp_code,detail,created_at FROM mail_monitor_events WHERE installation_id='local' AND synced_at='' ORDER BY created_at,id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

func (store Store) MarkSynced(ctx context.Context, events []Event) error {
	for _, event := range events {
		if _, err := store.DB.ExecContext(ctx, `UPDATE mail_monitor_events SET synced_at=? WHERE id=? AND installation_id='local'`, time.Now().UTC().Format(timestampLayout), event.ID); err != nil {
			return err
		}
	}
	return nil
}

func (store Store) Recent(ctx context.Context, installationID string, since time.Time, limit, offset int) ([]Event, error) {
	return store.RecentFiltered(ctx, Filter{InstallationID: installationID, Since: since, Limit: limit, Offset: offset})
}

func (store Store) RecentFiltered(ctx context.Context, filter Filter) ([]Event, error) {
	if filter.Limit <= 0 || filter.Limit > 50 {
		filter.Limit = 50
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	query := `SELECT id,installation_id,flow_id,message_id,kind,stage,domain,recipient,route,accepted_by,attempt,smtp_code,detail,created_at FROM mail_monitor_events WHERE installation_id=? AND created_at>=?`
	arguments := []any{filter.InstallationID, filter.Since.UTC().Format(timestampLayout)}
	if filter.Stage != "" {
		query += ` AND stage=?`
		arguments = append(arguments, filter.Stage)
	}
	if filter.Kind != "" {
		query += ` AND kind=?`
		arguments = append(arguments, filter.Kind)
	}
	if filter.Recipient != "" {
		query += ` AND LOWER(recipient) LIKE ? ESCAPE '!'`
		escapedRecipient := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(strings.ToLower(filter.Recipient))
		arguments = append(arguments, "%"+escapedRecipient+"%")
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?`
	arguments = append(arguments, filter.Limit, filter.Offset)
	rows, err := store.DB.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

func (store Store) CountsSince(ctx context.Context, installationID string, since time.Time) (Counts, error) {
	rows, err := store.DB.QueryContext(ctx, `SELECT stage,COUNT(*) FROM mail_monitor_events WHERE installation_id=? AND created_at>=? AND stage IN ('queued','accepted','failed','registration_requested','registration_complete','recovery_requested','recovery_complete') GROUP BY stage`, installationID, since.UTC().Format(timestampLayout))
	if err != nil {
		return Counts{}, err
	}
	defer rows.Close()
	var counts Counts
	for rows.Next() {
		var stage string
		var count int
		if err := rows.Scan(&stage, &count); err != nil {
			return Counts{}, err
		}
		switch stage {
		case "queued":
			counts.Queued = count
		case "accepted":
			counts.Accepted = count
		case "failed":
			counts.Failed = count
		case "registration_requested":
			counts.RegistrationRequested = count
		case "recovery_requested":
			counts.RecoveryRequested = count
		case "registration_complete":
			counts.RegistrationComplete = count
		case "recovery_complete":
			counts.RecoveryComplete = count
		}
	}
	return counts, rows.Err()
}

func (store Store) MarkReceived(ctx context.Context, installationID string) error {
	_, err := store.DB.ExecContext(ctx, `INSERT INTO mail_monitor_sync(installation_id,received_at) VALUES(?,?) ON CONFLICT(installation_id) DO UPDATE SET received_at=excluded.received_at`, installationID, time.Now().UTC().Format(timestampLayout))
	return err
}

func (store Store) LastReceived(ctx context.Context, installationID string) (string, error) {
	rows, err := store.DB.QueryContext(ctx, `SELECT received_at FROM mail_monitor_sync WHERE installation_id=?`, installationID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var receivedAt string
	if rows.Next() {
		if err := rows.Scan(&receivedAt); err != nil {
			return "", err
		}
	}
	return receivedAt, rows.Err()
}

func (store Store) Purge(ctx context.Context, before time.Time) error {
	_, err := store.DB.ExecContext(ctx, `DELETE FROM mail_monitor_events WHERE created_at<?`, before.UTC().Format(timestampLayout))
	return err
}

func scanEvents(rows *sql.Rows) ([]Event, error) {
	events := make([]Event, 0)
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ID, &event.InstallationID, &event.FlowID, &event.MessageID, &event.Kind, &event.Stage, &event.Domain, &event.Recipient, &event.Route, &event.AcceptedBy, &event.Attempt, &event.SMTPCode, &event.Detail, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func boundedField(field string, limit int) string {
	field = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return ' '
		}
		return character
	}, field)
	if len(field) > limit {
		field = field[:limit]
		for !utf8.ValidString(field) {
			field = field[:len(field)-1]
		}
	}
	return field
}

var sensitiveErrorPart = regexp.MustCompile(`(?i)(https?://\S+|(?:token|code|password|secret|key)\s*[=:]\s*[^\s&]+|\b[0-9]{6,8}\b|\b[0-9a-f]{32,}\b|\b[A-Za-z0-9_-]{24,}\b)`)

// SMTP errors can contain reflected request data, so monitoring stores only bounded diagnostics.
func SafeError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.Map(func(character rune) rune {
		if character < 32 || character == 127 {
			return ' '
		}
		return character
	}, err.Error())
	message = sensitiveErrorPart.ReplaceAllString(message, "[redacted]")
	message = strings.Join(strings.Fields(message), " ")
	return boundedField(message, 500)
}
