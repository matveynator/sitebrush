package mailguard

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const RegistrationMailAbuse = "registration-mail-abuse"

// Bounded maintenance accounts for unconfirmed delivery without requiring the
// sender to revisit the form. All transitions still belong to the SQL owner.
func SweepRegistrations(transaction *sql.Tx, now time.Time) ([]Registration, error) {
	rows, err := transaction.Query(`SELECT cooldown.client_ip,cooldown.domain FROM registration_mail_cooldowns cooldown LEFT JOIN registration_mail_abuse abuse ON abuse.client_ip=cooldown.client_ip AND abuse.domain=cooldown.domain WHERE (cooldown.phase='pending' AND cooldown.expires_at<=?) OR (cooldown.phase='failed' AND cooldown.failures>=4 AND abuse.attempt_id=cooldown.attempt_id AND abuse.examined_attempt<>cooldown.attempt_id) ORDER BY cooldown.expires_at LIMIT 100`, now.Unix())
	if err != nil {
		return nil, err
	}
	var sources [][2]string
	for rows.Next() {
		var source [2]string
		if err := rows.Scan(&source[0], &source[1]); err != nil {
			rows.Close()
			return nil, err
		}
		sources = append(sources, source)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	var detected []Registration
	for _, source := range sources {
		registration, err := ReadRegistration(transaction, source[0], source[1], now)
		if err != nil {
			return nil, err
		}
		if registration.AbuseDetected {
			detected = append(detected, registration)
		}
	}
	return detected, nil
}

func RegistrationAbuseSchemaQueries() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS registration_mail_abuse(client_ip TEXT NOT NULL,domain TEXT NOT NULL,attempt_id TEXT NOT NULL,recipient_hash TEXT NOT NULL,failed_recipients TEXT NOT NULL,examined_attempt TEXT NOT NULL,PRIMARY KEY(client_ip,domain))`,
		`CREATE INDEX IF NOT EXISTS idx_registration_mail_abuse_attempt ON registration_mail_abuse(attempt_id)`,
	}
}

// Keep only four distinct failed recipient hashes. Delivery errors never enter
// this evidence set, and email addresses need not appear in security analytics.
func TrackRegistrationRecipient(transaction *sql.Tx, attemptID, recipient string) error {
	var ip, domain string
	if err := transaction.QueryRow(`SELECT client_ip,domain FROM registration_mail_cooldowns WHERE attempt_id=?`, attemptID).Scan(&ip, &domain); err != nil {
		return err
	}
	var exists int
	if err := transaction.QueryRow(`SELECT COUNT(*) FROM registration_mail_abuse WHERE client_ip=? AND domain=?`, ip, domain).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		_, err := transaction.Exec(`INSERT INTO registration_mail_abuse(client_ip,domain,attempt_id,recipient_hash,failed_recipients,examined_attempt) VALUES(?,?,?,?,'[]','')`, ip, domain, attemptID, recipientHash(recipient))
		return err
	}
	_, err := transaction.Exec(`UPDATE registration_mail_abuse SET attempt_id=?,recipient_hash=? WHERE client_ip=? AND domain=?`, attemptID, recipientHash(recipient), ip, domain)
	return err
}

func rememberRegistrationFailure(transaction *sql.Tx, attemptID string) error {
	var activeRecipient, encoded string
	err := transaction.QueryRow(`SELECT recipient_hash,failed_recipients FROM registration_mail_abuse WHERE attempt_id=?`, attemptID).Scan(&activeRecipient, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var recipients []string
	if err := json.Unmarshal([]byte(encoded), &recipients); err != nil {
		return err
	}
	for _, previous := range recipients {
		if previous == activeRecipient {
			return nil
		}
	}
	if len(recipients) >= 4 {
		return nil
	}
	recipients = append(recipients, activeRecipient)
	encodedRecipients, err := json.Marshal(recipients)
	if err != nil {
		return err
	}
	_, err = transaction.Exec(`UPDATE registration_mail_abuse SET failed_recipients=? WHERE attempt_id=?`, string(encodedRecipients), attemptID)
	return err
}

// Detection is durable and emitted once per failed attempt. Reads during an
// existing pause cannot count as failed registrations or extend the IP block.
func DetectRegistrationAbuse(transaction *sql.Tx, registration Registration, now time.Time) (bool, error) {
	if loopbackIP(registration.IP) || registration.Phase != "failed" || registration.Failures < 4 {
		return false, nil
	}
	var encoded, reported string
	err := transaction.QueryRow(`SELECT failed_recipients,examined_attempt FROM registration_mail_abuse WHERE client_ip=? AND domain=? AND attempt_id=?`, registration.IP, registration.Domain, registration.AttemptID).Scan(&encoded, &reported)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil || reported == registration.AttemptID {
		return false, err
	}
	var recipients []string
	if err := json.Unmarshal([]byte(encoded), &recipients); err != nil {
		return false, err
	}
	if len(recipients) < 4 {
		_, err = transaction.Exec(`UPDATE registration_mail_abuse SET examined_attempt=? WHERE attempt_id=?`, registration.AttemptID, registration.AttemptID)
		return false, err
	}
	until := now.Add(72 * time.Hour)
	var previous int64
	err = transaction.QueryRow(`SELECT expires_at FROM mail_guard_blocks WHERE client_ip=?`, registration.IP).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if previous > until.Unix() {
		until = time.Unix(previous, 0)
	}
	if err := SetBlock(transaction, registration.IP, RegistrationMailAbuse, until); err != nil {
		return false, err
	}
	if _, err := transaction.Exec(`UPDATE registration_mail_cooldowns SET retry_at=? WHERE attempt_id=?`, until.Unix(), registration.AttemptID); err != nil {
		return false, err
	}
	_, err = transaction.Exec(`UPDATE registration_mail_abuse SET examined_attempt=? WHERE attempt_id=?`, registration.AttemptID, registration.AttemptID)
	return err == nil, err
}
