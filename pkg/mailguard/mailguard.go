// Package mailguard applies installation-wide limits inside the database owner's transaction.
package mailguard

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/netip"
	"strings"
	"time"
)

type Decision struct {
	Allowed  bool
	NewBlock bool
	Until    time.Time
	Reason   string
}

type Block struct {
	IP     string
	Reason string
	Until  time.Time
}

func SchemaQueries() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS mail_guard_requests(id TEXT PRIMARY KEY,client_ip TEXT NOT NULL,network TEXT NOT NULL,recipient_hash TEXT NOT NULL,purpose TEXT NOT NULL,sent INTEGER NOT NULL,created_at INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_mail_guard_ip_time ON mail_guard_requests(client_ip,sent,created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_mail_guard_recipient_time ON mail_guard_requests(recipient_hash,sent,created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_mail_guard_network_time ON mail_guard_requests(network,sent,created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_mail_guard_time ON mail_guard_requests(created_at)`,
		`CREATE TABLE IF NOT EXISTS mail_guard_blocks(client_ip TEXT PRIMARY KEY,reason TEXT NOT NULL,expires_at INTEGER NOT NULL)`,
	}
}

func recipientHash(recipient string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(recipient))))
	return hex.EncodeToString(digest[:])
}

func loopbackIP(ip string) bool {
	address, err := netip.ParseAddr(strings.TrimSpace(ip))
	return err == nil && address.Unmap().IsLoopback()
}

func networkForIP(ip string) (string, error) {
	address, err := netip.ParseAddr(ip)
	if err != nil {
		return "", errors.New("request IP is unavailable")
	}
	address = address.Unmap()
	bits := 64
	if address.Is4() {
		bits = 24
	}
	return netip.PrefixFrom(address, bits).Masked().String(), nil
}

// Invalid CAPTCHAs still spend the source's request budget. Recipient delivery
// budgets are spent only after CAPTCHA verification, preventing trivial lockout.
func Observe(transaction *sql.Tx, ip, recipient, purpose string, now time.Time) (Decision, error) {
	network, err := networkForIP(ip)
	if err != nil {
		return Decision{}, err
	}
	if _, err := transaction.Exec(`DELETE FROM mail_guard_requests WHERE created_at<?`, now.Add(-24*time.Hour).Unix()); err != nil {
		return Decision{}, err
	}
	if _, err := transaction.Exec(`DELETE FROM mail_guard_blocks WHERE expires_at<=?`, now.Unix()); err != nil {
		return Decision{}, err
	}
	var expires int64
	var reason string
	err = transaction.QueryRow(`SELECT reason,expires_at FROM mail_guard_blocks WHERE client_ip=?`, ip).Scan(&reason, &expires)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Decision{}, err
	}
	if expires > now.Unix() && !loopbackIP(ip) {
		return Decision{Until: time.Unix(expires, 0), Reason: reason}, nil
	}
	var storedRequests int
	if err := transaction.QueryRow(`SELECT COUNT(*) FROM mail_guard_requests`).Scan(&storedRequests); err != nil {
		return Decision{}, err
	}
	if storedRequests >= 20000 {
		return Decision{Until: now.Add(time.Hour), Reason: "Email request capacity reached"}, nil
	}
	for _, window := range []struct {
		duration             time.Duration
		requests, recipients int
	}{
		{time.Minute, 8, 3}, {time.Hour, 20, 3}, {24 * time.Hour, 40, 5},
	} {
		var requests, recipients int
		err = transaction.QueryRow(`SELECT COUNT(*),COUNT(DISTINCT recipient_hash) FROM mail_guard_requests WHERE client_ip=? AND sent=0 AND created_at>=?`, ip, now.Add(-window.duration).Unix()).Scan(&requests, &recipients)
		if err != nil {
			return Decision{}, err
		}
		var seen int
		if err := transaction.QueryRow(`SELECT COUNT(*) FROM mail_guard_requests WHERE client_ip=? AND recipient_hash=? AND sent=0 AND created_at>=?`, ip, recipientHash(recipient), now.Add(-window.duration).Unix()).Scan(&seen); err != nil {
			return Decision{}, err
		}
		if requests >= window.requests || (seen == 0 && recipients >= window.recipients) {
			until := now.Add(time.Hour)
			if window.duration == 24*time.Hour {
				until = now.Add(24 * time.Hour)
			}
			reason = "Too many account mail requests"
			if seen == 0 && recipients >= window.recipients {
				reason = "Account mail requested for too many different addresses"
			}
			if loopbackIP(ip) {
				return Decision{Until: until, Reason: reason}, nil
			}
			if err := SetBlock(transaction, ip, reason, until); err != nil {
				return Decision{}, err
			}
			return Decision{NewBlock: true, Until: until, Reason: reason}, nil
		}
	}
	var networkRequests int
	if err := transaction.QueryRow(`SELECT COUNT(*) FROM mail_guard_requests WHERE network=? AND sent=0 AND created_at>=?`, network, now.Add(-time.Hour).Unix()).Scan(&networkRequests); err != nil {
		return Decision{}, err
	}
	if networkRequests >= 100 {
		return Decision{Until: now.Add(time.Hour), Reason: "Too many account mail requests from this network"}, nil
	}
	return Decision{Allowed: true}, insertRequest(transaction, ip, network, recipient, purpose, 0, now)
}

func Reserve(transaction *sql.Tx, ip, recipient, purpose string, now time.Time) (Decision, error) {
	blocked, err := Blocked(transaction, ip, now)
	if err != nil || blocked {
		return Decision{Until: now.Add(time.Hour), Reason: "Account mail source IP is blocked"}, err
	}
	var hourCount, dayCount int
	var latest sql.NullInt64
	if err := transaction.QueryRow(`SELECT COUNT(*),MAX(created_at) FROM mail_guard_requests WHERE recipient_hash=? AND sent=1 AND created_at>=?`, recipientHash(recipient), now.Add(-time.Hour).Unix()).Scan(&hourCount, &latest); err != nil {
		return Decision{}, err
	}
	if err := transaction.QueryRow(`SELECT COUNT(*) FROM mail_guard_requests WHERE recipient_hash=? AND sent=1 AND created_at>=?`, recipientHash(recipient), now.Add(-24*time.Hour).Unix()).Scan(&dayCount); err != nil {
		return Decision{}, err
	}
	if hourCount >= 3 || dayCount >= 6 || (latest.Valid && now.Unix()-latest.Int64 < 60) {
		return Decision{Until: now.Add(time.Minute), Reason: "Please wait before requesting another email"}, nil
	}
	network, err := networkForIP(ip)
	if err != nil {
		return Decision{}, err
	}
	return Decision{Allowed: true}, insertRequest(transaction, ip, network, recipient, purpose, 1, now)
}

func insertRequest(transaction *sql.Tx, ip, network, recipient, purpose string, sent int, now time.Time) error {
	var identifier [16]byte
	if _, err := rand.Read(identifier[:]); err != nil {
		return err
	}
	_, err := transaction.Exec(`INSERT INTO mail_guard_requests(id,client_ip,network,recipient_hash,purpose,sent,created_at) VALUES(?,?,?,?,?,?,?)`, hex.EncodeToString(identifier[:]), ip, network, recipientHash(recipient), purpose, sent, now.Unix())
	return err
}

func SetBlock(transaction *sql.Tx, ip, reason string, until time.Time) error {
	if loopbackIP(ip) {
		return errors.New("local access IP cannot be blocked")
	}
	if address, err := netip.ParseAddr(ip); err == nil {
		ip = address.Unmap().String()
	}
	if _, err := networkForIP(ip); err != nil {
		return err
	}
	_, err := transaction.Exec(`INSERT INTO mail_guard_blocks(client_ip,reason,expires_at) VALUES(?,?,?) ON CONFLICT(client_ip) DO UPDATE SET reason=excluded.reason,expires_at=excluded.expires_at`, ip, reason, until.Unix())
	return err
}

func Blocked(database interface{ QueryRow(string, ...any) *sql.Row }, ip string, now time.Time) (bool, error) {
	if loopbackIP(ip) {
		return false, nil
	}
	var expires int64
	err := database.QueryRow(`SELECT expires_at FROM mail_guard_blocks WHERE client_ip=?`, ip).Scan(&expires)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return expires > now.Unix(), err
}

func Unblock(transaction *sql.Tx, ip string) error {
	var reason string
	if err := transaction.QueryRow(`SELECT reason FROM mail_guard_blocks WHERE client_ip=?`, ip).Scan(&reason); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if reason == RegistrationMailAbuse {
		// A manual reversal also releases the abuse-imposed pause. Pending
		// confirmations and independent delivery quotas keep their own lifetimes.
		if _, err := transaction.Exec(`DELETE FROM registration_mail_abuse WHERE attempt_id IN (SELECT attempt_id FROM registration_mail_cooldowns WHERE client_ip=? AND phase='failed')`, ip); err != nil {
			return err
		}
		if _, err := transaction.Exec(`DELETE FROM registration_mail_cooldowns WHERE client_ip=? AND phase='failed'`, ip); err != nil {
			return err
		}
	}
	if _, err := transaction.Exec(`DELETE FROM mail_guard_blocks WHERE client_ip=?`, ip); err != nil {
		return err
	}
	_, err := transaction.Exec(`DELETE FROM mail_guard_requests WHERE client_ip=? AND sent=0`, ip)
	return err
}

func Blocks(database *sql.DB, now time.Time) ([]Block, error) {
	rows, err := database.Query(`SELECT client_ip,reason,expires_at FROM mail_guard_blocks WHERE expires_at>? ORDER BY expires_at DESC LIMIT 200`, now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	blocks := make([]Block, 0)
	for rows.Next() {
		var block Block
		var expires int64
		if err := rows.Scan(&block.IP, &block.Reason, &expires); err != nil {
			return nil, err
		}
		if loopbackIP(block.IP) {
			continue
		}
		block.Until = time.Unix(expires, 0).UTC()
		blocks = append(blocks, block)
	}
	return blocks, rows.Err()
}
