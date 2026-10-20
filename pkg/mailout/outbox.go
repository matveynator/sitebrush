package mailout

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	StatusPending = "pending"
	StatusSending = "sending"
	StatusSent    = "sent"
	StatusFailed  = "failed"

	RouteLocal = "local"
	RouteRelay = "relay"
)

const (
	DefaultRetention  = 7 * 24 * time.Hour
	TerminalRetention = 30 * 24 * time.Hour
)

// Task transfers responsibility for one durable delivery to the mail process.
// Reply belongs to the submitting consumer and may be reused for later tasks.
type Task struct {
	ID             string
	InstallationID string
	Kind           string
	Route          string
	Message        Message
	CreatedAt      time.Time
	ExpiresAt      time.Time
	Accepted       chan error
	Reply          chan Result
	Done           <-chan struct{}
}

type Result struct {
	ID          string
	Status      string
	Attempts    int
	NextAttempt time.Time
	Err         error
}

type Record struct {
	Task
	Status      string
	Attempts    int
	NextAttempt time.Time
	SentAt      time.Time
	LastError   string
}

// Database is the database/sql surface required by the outbox store.
type Database interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func SchemaQueries() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS mail_outbox(message_id TEXT PRIMARY KEY,installation_id TEXT,kind TEXT,route TEXT,from_address TEXT,recipient TEXT,subject TEXT,body TEXT,html_body TEXT,status TEXT,attempts INTEGER DEFAULT 0,next_attempt_at TEXT,created_at TEXT,expires_at TEXT,sent_at TEXT,last_error TEXT);`,
		`CREATE INDEX IF NOT EXISTS idx_mail_outbox_due ON mail_outbox(status,next_attempt_at);`,
		`CREATE INDEX IF NOT EXISTS idx_mail_outbox_installation ON mail_outbox(installation_id,created_at);`,
	}
}

func NewTask(kind, route string, message Message, expiresAt time.Time, reply chan Result) Task {
	now := time.Now().UTC()
	if expiresAt.IsZero() {
		expiresAt = now.Add(DefaultRetention)
	}
	deliveryID := NewDeliveryID()
	message.MessageID = deliveryID
	return Task{
		ID:        deliveryID,
		Kind:      strings.TrimSpace(kind),
		Route:     strings.TrimSpace(route),
		Message:   message,
		CreatedAt: now,
		ExpiresAt: expiresAt.UTC(),
		Reply:     reply,
	}
}

func NewDeliveryID() string {
	var randomBytes [16]byte
	if _, err := rand.Read(randomBytes[:]); err == nil {
		return hex.EncodeToString(randomBytes[:])
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func NormalizeTask(task Task) (Task, error) {
	task.ID = strings.TrimSpace(task.ID)
	task.InstallationID = strings.TrimSpace(task.InstallationID)
	task.Kind = strings.TrimSpace(task.Kind)
	task.Route = strings.TrimSpace(task.Route)
	if task.ID == "" {
		task.ID = NewDeliveryID()
	}
	task.Message.MessageID = task.ID
	if task.Kind == "" {
		task.Kind = "system"
	}
	if task.Route != RouteLocal && task.Route != RouteRelay {
		return Task{}, fmt.Errorf("unknown mail route %q", task.Route)
	}
	if strings.TrimSpace(task.Message.To) == "" {
		return Task{}, errors.New("mail recipient is required")
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now().UTC()
	} else {
		task.CreatedAt = task.CreatedAt.UTC()
	}
	if task.ExpiresAt.IsZero() {
		task.ExpiresAt = task.CreatedAt.Add(DefaultRetention)
	} else {
		task.ExpiresAt = task.ExpiresAt.UTC()
	}
	if !task.ExpiresAt.After(task.CreatedAt) {
		return Task{}, errors.New("mail task expiration must be after creation")
	}
	return task, nil
}

func Insert(ctx context.Context, database Database, task Task) (bool, error) {
	normalizedTask, err := NormalizeTask(task)
	if err != nil {
		return false, err
	}
	result, err := database.ExecContext(ctx, `INSERT INTO mail_outbox(message_id,installation_id,kind,route,from_address,recipient,subject,body,html_body,status,attempts,next_attempt_at,created_at,expires_at,sent_at,last_error) VALUES(?,?,?,?,?,?,?,?,?,'pending',0,?,?,?,'','') ON CONFLICT(message_id) DO NOTHING`,
		normalizedTask.ID,
		normalizedTask.InstallationID,
		normalizedTask.Kind,
		normalizedTask.Route,
		normalizedTask.Message.From,
		normalizedTask.Message.To,
		normalizedTask.Message.Subject,
		normalizedTask.Message.Body,
		normalizedTask.Message.HTMLBody,
		normalizedTask.CreatedAt.Format(time.RFC3339Nano),
		normalizedTask.CreatedAt.Format(time.RFC3339Nano),
		normalizedTask.ExpiresAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	return rowsAffected > 0, err
}

func ByID(ctx context.Context, database Database, messageID string) (Record, bool, error) {
	row := database.QueryRowContext(ctx, `SELECT message_id,installation_id,kind,route,from_address,recipient,subject,body,html_body,status,attempts,next_attempt_at,created_at,expires_at,sent_at,last_error FROM mail_outbox WHERE message_id=?`, strings.TrimSpace(messageID))
	record, err := scanRecord(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, false, nil
	}
	return record, err == nil, err
}

func Due(ctx context.Context, database Database, now time.Time, limit int) ([]Record, error) {
	if limit <= 0 {
		limit = 32
	}
	rows, err := database.QueryContext(ctx, `SELECT message_id,installation_id,kind,route,from_address,recipient,subject,body,html_body,status,attempts,next_attempt_at,created_at,expires_at,sent_at,last_error FROM mail_outbox WHERE status='pending' AND next_attempt_at<=? ORDER BY next_attempt_at ASC,message_id ASC LIMIT ?`, now.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]Record, 0, limit)
	for rows.Next() {
		record, scanErr := scanRecord(rows.Scan)
		if scanErr != nil {
			return nil, scanErr
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func MarkPending(ctx context.Context, database Database, messageID string, attempts int, nextAttempt time.Time, deliveryError error) error {
	errorText := ""
	if deliveryError != nil {
		errorText = deliveryError.Error()
	}
	_, err := database.ExecContext(ctx, `UPDATE mail_outbox SET status='pending',attempts=?,next_attempt_at=?,last_error=? WHERE message_id=?`, attempts, nextAttempt.UTC().Format(time.RFC3339Nano), errorText, strings.TrimSpace(messageID))
	return err
}

func Claim(ctx context.Context, database Database, messageID string) (bool, error) {
	result, err := database.ExecContext(ctx, `UPDATE mail_outbox SET status='sending' WHERE message_id=? AND status='pending'`, strings.TrimSpace(messageID))
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	return rowsAffected > 0, err
}

func RecoverInterrupted(ctx context.Context, database Database, now time.Time) error {
	_, err := database.ExecContext(ctx, `UPDATE mail_outbox SET status='pending',next_attempt_at=?,last_error=CASE WHEN last_error='' THEN 'delivery interrupted by restart' ELSE last_error END WHERE status='sending'`, now.UTC().Format(time.RFC3339Nano))
	return err
}

func MarkSent(ctx context.Context, database Database, messageID string, attempts int, sentAt time.Time) error {
	_, err := database.ExecContext(ctx, `UPDATE mail_outbox SET status='sent',attempts=?,sent_at=?,next_attempt_at='',last_error='',body='',html_body='' WHERE message_id=?`, attempts, sentAt.UTC().Format(time.RFC3339Nano), strings.TrimSpace(messageID))
	return err
}

func MarkFailed(ctx context.Context, database Database, messageID string, attempts int, deliveryError error) error {
	errorText := "delivery failed"
	if deliveryError != nil {
		errorText = deliveryError.Error()
	}
	_, err := database.ExecContext(ctx, `UPDATE mail_outbox SET status='failed',attempts=?,next_attempt_at='',last_error=?,body='',html_body='' WHERE message_id=?`, attempts, errorText, strings.TrimSpace(messageID))
	return err
}

func PurgeTerminal(ctx context.Context, database Database, before time.Time) error {
	_, err := database.ExecContext(ctx, `DELETE FROM mail_outbox WHERE status IN ('sent','failed') AND created_at<?`, before.UTC().Format(time.RFC3339Nano))
	return err
}

func RetryDelay(attempts int) time.Duration {
	switch attempts {
	case 0, 1:
		return 5 * time.Second
	case 2:
		return 30 * time.Second
	case 3:
		return 2 * time.Minute
	case 4:
		return 10 * time.Minute
	case 5:
		return 30 * time.Minute
	default:
		return time.Hour
	}
}

func RetryDelayWithJitter(attempts int) time.Duration {
	baseDelay := RetryDelay(attempts)
	var randomByte [1]byte
	if _, err := rand.Read(randomByte[:]); err != nil {
		return baseDelay
	}
	// Scale the delay from 80% through 120% without floating point state.
	percent := 80 + int(randomByte[0])%41
	return time.Duration(int64(baseDelay) * int64(percent) / 100)
}

func IsPermanentFailure(err error) bool {
	if err == nil {
		return false
	}
	var permanentError PermanentError
	if errors.As(err, &permanentError) {
		return true
	}
	for _, field := range strings.Fields(err.Error()) {
		field = strings.Trim(field, ":;,()[]")
		if len(field) != 3 || field[0] != '5' {
			continue
		}
		if _, parseErr := strconv.Atoi(field); parseErr == nil {
			return true
		}
	}
	return false
}

type PermanentError struct {
	Err error
}

func (deliveryError PermanentError) Error() string {
	if deliveryError.Err == nil {
		return "permanent mail delivery failure"
	}
	return deliveryError.Err.Error()
}

func (deliveryError PermanentError) Unwrap() error {
	return deliveryError.Err
}

type scanFunction func(...any) error

func scanRecord(scan scanFunction) (Record, error) {
	var record Record
	var nextAttemptText string
	var createdAtText string
	var expiresAtText string
	var sentAtText string
	err := scan(
		&record.ID,
		&record.InstallationID,
		&record.Kind,
		&record.Route,
		&record.Message.From,
		&record.Message.To,
		&record.Message.Subject,
		&record.Message.Body,
		&record.Message.HTMLBody,
		&record.Status,
		&record.Attempts,
		&nextAttemptText,
		&createdAtText,
		&expiresAtText,
		&sentAtText,
		&record.LastError,
	)
	if err != nil {
		return Record{}, err
	}
	record.NextAttempt, err = parseStoredTime(nextAttemptText)
	if err != nil {
		return Record{}, fmt.Errorf("parse mail next attempt: %w", err)
	}
	record.CreatedAt, err = parseStoredTime(createdAtText)
	if err != nil {
		return Record{}, fmt.Errorf("parse mail creation time: %w", err)
	}
	record.ExpiresAt, err = parseStoredTime(expiresAtText)
	if err != nil {
		return Record{}, fmt.Errorf("parse mail expiration time: %w", err)
	}
	record.SentAt, err = parseStoredTime(sentAtText)
	if err != nil {
		return Record{}, fmt.Errorf("parse mail sent time: %w", err)
	}
	record.Message.MessageID = record.ID
	return record, nil
}

func parseStoredTime(rawTime string) (time.Time, error) {
	if strings.TrimSpace(rawTime) == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, strings.TrimSpace(rawTime))
}
