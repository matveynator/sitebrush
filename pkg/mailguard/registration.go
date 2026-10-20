package mailguard

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

type Registration struct {
	IP        string
	Domain    string
	AttemptID string
	MessageID string
	Phase     string
	Failures  int
	RetryAt   time.Time
	ExpiresAt time.Time
}

func RegistrationSchemaQueries() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS registration_mail_cooldowns(client_ip TEXT NOT NULL,domain TEXT NOT NULL,attempt_id TEXT NOT NULL,message_id TEXT NOT NULL,phase TEXT NOT NULL,failures INTEGER NOT NULL,retry_at INTEGER NOT NULL,expires_at INTEGER NOT NULL,PRIMARY KEY(client_ip,domain))`,
		`CREATE INDEX IF NOT EXISTS idx_registration_mail_attempt ON registration_mail_cooldowns(attempt_id)`,
	}
}

func RegistrationDelay(failures int) time.Duration {
	switch failures {
	case 0, 1:
		return time.Hour
	case 2:
		return 6 * time.Hour
	case 3:
		return 24 * time.Hour
	default:
		return 72 * time.Hour
	}
}

func (registration Registration) Blocked(now time.Time) bool {
	return registration.Phase == "delivering" || registration.RetryAt.After(now)
}

func ReadRegistration(transaction *sql.Tx, ip, domain string, now time.Time) (Registration, error) {
	registration, err := scanRegistration(transaction.QueryRow(`SELECT client_ip,domain,attempt_id,message_id,phase,failures,retry_at,expires_at FROM registration_mail_cooldowns WHERE client_ip=? AND domain=?`, ip, strings.ToLower(strings.TrimSuffix(domain, "."))))
	if err != nil {
		return registration, err
	}
	if registration.Phase == "pending" && !registration.ExpiresAt.After(now) {
		if err := FailRegistration(transaction, registration.AttemptID); err != nil {
			return registration, err
		}
		registration.Phase = "failed"
		registration.Failures = min(4, registration.Failures+1)
	}
	// A preparation abandoned before durable queueing has no delivery evidence.
	// It must never become a user failure merely because the process stopped.
	if registration.Phase == "delivering" && registration.MessageID == "" && !registration.ExpiresAt.After(now) {
		if err := AbortRegistration(transaction, registration.AttemptID); err != nil {
			return registration, err
		}
		registration.Phase, registration.AttemptID = "idle", ""
		registration.RetryAt = time.Time{}
	}
	return registration, nil
}

// Reserve before enqueueing, regardless of recipient. Failures remain unchanged
// until a delivered attempt expires or the user's confirmation is rejected.
func BeginRegistration(transaction *sql.Tx, ip, domain, attemptID string, expires, now time.Time) (Registration, bool, error) {
	if _, err := networkForIP(ip); err != nil {
		return Registration{}, false, err
	}
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if domain == "" || len(domain) > 253 || attemptID == "" || !expires.After(now) {
		return Registration{}, false, errors.New("invalid registration admission")
	}
	previous, err := ReadRegistration(transaction, ip, domain, now)
	if err != nil || previous.Blocked(now) {
		return previous, false, err
	}
	registration := Registration{IP: ip, Domain: domain, AttemptID: attemptID, Phase: "delivering", Failures: previous.Failures, RetryAt: now.Add(RegistrationDelay(previous.Failures + 1)), ExpiresAt: expires}
	if previous.IP == "" {
		_, err = transaction.Exec(`INSERT INTO registration_mail_cooldowns(client_ip,domain,attempt_id,message_id,phase,failures,retry_at,expires_at) VALUES(?,?,?,'','delivering',?,?,?)`, ip, domain, attemptID, registration.Failures, registration.RetryAt.Unix(), expires.Unix())
	} else {
		_, err = transaction.Exec(`UPDATE registration_mail_cooldowns SET attempt_id=?,message_id='',phase='delivering',retry_at=?,expires_at=? WHERE client_ip=? AND domain=?`, attemptID, registration.RetryAt.Unix(), expires.Unix(), ip, domain)
	}
	return registration, err == nil, err
}

func QueueRegistration(transaction *sql.Tx, attemptID, messageID string) error {
	_, err := transaction.Exec(`UPDATE registration_mail_cooldowns SET message_id=? WHERE attempt_id=? AND phase='delivering'`, messageID, attemptID)
	return err
}

func RegistrationDelivered(transaction *sql.Tx, attemptID string, now time.Time) error {
	registration, err := scanRegistration(transaction.QueryRow(`SELECT client_ip,domain,attempt_id,message_id,phase,failures,retry_at,expires_at FROM registration_mail_cooldowns WHERE attempt_id=?`, attemptID))
	if err != nil || registration.Phase != "delivering" {
		return err
	}
	if !registration.ExpiresAt.After(now) {
		return AbortRegistration(transaction, attemptID)
	}
	_, err = transaction.Exec(`UPDATE registration_mail_cooldowns SET phase='pending' WHERE attempt_id=? AND phase='delivering'`, attemptID)
	return err
}

// The phase transition makes failure accounting idempotent, including repeated
// rejected-code requests, page refreshes, and late worker completions.
func FailRegistration(transaction *sql.Tx, attemptID string) error {
	registration, err := scanRegistration(transaction.QueryRow(`SELECT client_ip,domain,attempt_id,message_id,phase,failures,retry_at,expires_at FROM registration_mail_cooldowns WHERE attempt_id=?`, attemptID))
	if err != nil || registration.Phase != "pending" {
		return err
	}
	_, err = transaction.Exec(`UPDATE registration_mail_cooldowns SET phase='failed',failures=? WHERE attempt_id=? AND phase='pending'`, min(4, registration.Failures+1), attemptID)
	return err
}

func AbortRegistration(transaction *sql.Tx, attemptID string) error {
	_, err := transaction.Exec(`UPDATE registration_mail_cooldowns SET attempt_id='',message_id='',phase='idle',retry_at=0,expires_at=0 WHERE attempt_id=? AND phase IN ('delivering','pending')`, attemptID)
	return err
}

func CompleteRegistration(transaction *sql.Tx, attemptID string) error {
	_, err := transaction.Exec(`DELETE FROM registration_mail_cooldowns WHERE attempt_id=?`, attemptID)
	return err
}

func scanRegistration(row *sql.Row) (Registration, error) {
	var registration Registration
	var retry, expires int64
	err := row.Scan(&registration.IP, &registration.Domain, &registration.AttemptID, &registration.MessageID, &registration.Phase, &registration.Failures, &retry, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Registration{}, nil
	}
	registration.RetryAt, registration.ExpiresAt = time.Unix(retry, 0).UTC(), time.Unix(expires, 0).UTC()
	return registration, err
}
