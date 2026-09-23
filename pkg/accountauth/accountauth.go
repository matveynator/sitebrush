// Package accountauth implements account-bound challenges inside caller-owned SQL transactions.
package accountauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"golang.org/x/crypto/scrypt"
	"math/big"
	"strings"
	"time"
)

const CodeTTL = 15 * time.Minute
const TrustTTL = 90 * 24 * time.Hour

func Schema() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS account_trusted_ips(domain TEXT,email TEXT,client_ip TEXT,confirmed_at INTEGER,last_login INTEGER,PRIMARY KEY(domain,email,client_ip))`,
		`CREATE TABLE IF NOT EXISTS account_login_codes(token TEXT PRIMARY KEY,domain TEXT,email TEXT,client_ip TEXT,code_hash TEXT,password_hash TEXT,created_at INTEGER,attempts INTEGER,return_path TEXT,language TEXT)`,
		`CREATE TABLE IF NOT EXISTS account_code_rates(domain TEXT,email TEXT,client_ip TEXT,window_start INTEGER,last_sent INTEGER,sent_count INTEGER,PRIMARY KEY(domain,email,client_ip))`,
	}
}

type Outcome struct{ Status, Token, Code, Email, Path, Language string }
type TrustedIP struct {
	IP                            string
	Confirmed, LastLogin, Expires time.Time
	Current                       bool
}

func RandomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
func digest(secret string) string {
	hashed := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(hashed[:])
}

// A slow salted snapshot invalidates pending challenges after a password change.
func passwordSnapshot(password, previous string) (string, error) {
	salt := make([]byte, 16)
	if previous == "" {
		if _, err := rand.Read(salt); err != nil {
			return "", err
		}
	} else {
		parts := strings.Split(previous, ":")
		if len(parts) != 2 {
			return "", errors.New("obsolete password snapshot")
		}
		decoded, err := hex.DecodeString(parts[0])
		if err != nil || len(decoded) != 16 {
			return "", errors.New("invalid password salt")
		}
		copy(salt, decoded)
	}
	key, err := scrypt.Key([]byte(password), salt, 32768, 8, 1, 32)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(salt) + ":" + hex.EncodeToString(key), nil
}

func Session(ctx context.Context, tx *sql.Tx, domain, email, ip string, now time.Time) (string, error) {
	token, err := RandomToken()
	if err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sessions(token,user_email,created_at,client_ip,security_version) VALUES(?,?,?,?,1)`, token, domain+"|"+email, now.UTC().Format(time.RFC3339), ip)
	return token, err
}

// Sending limits and challenge replacement commit together, even when delivery is deferred.
func Reserve(ctx context.Context, tx *sql.Tx, domain, email, ip string, now time.Time) (bool, error) {
	timestamp := now.Unix()
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_code_rates WHERE last_sent<?`, timestamp-int64(CodeTTL/time.Second)); err != nil {
		return false, err
	}
	var window, last int64
	var count int
	err := tx.QueryRowContext(ctx, `SELECT window_start,last_sent,sent_count FROM account_code_rates WHERE domain=? AND email=? AND client_ip=?`, domain, email, ip).Scan(&window, &last, &count)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil && (timestamp-last < 60 || (timestamp-window < int64(CodeTTL/time.Second) && count >= 3)) {
		return false, nil
	}
	if timestamp-window >= int64(CodeTTL/time.Second) {
		window = timestamp
		count = 0
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM account_code_rates WHERE domain=? AND email=? AND client_ip=?`, domain, email, ip); err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO account_code_rates(domain,email,client_ip,window_start,last_sent,sent_count) VALUES(?,?,?,?,?,?)`, domain, email, ip, window, timestamp, count+1)
	return err == nil, err
}

func Password(ctx context.Context, tx *sql.Tx, domain, email, password, ip, path, language string, now time.Time) (Outcome, error) {
	var stored string
	err := tx.QueryRowContext(ctx, `SELECT password FROM users WHERE domain=? AND email=? AND is_admin=1`, domain, email).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return Outcome{Status: "credentials"}, nil
	}
	if err != nil {
		return Outcome{}, err
	}
	if subtle.ConstantTimeCompare([]byte(password), []byte(stored)) != 1 {
		return Outcome{Status: "credentials"}, nil
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM account_trusted_ips WHERE last_login<=?`, now.Add(-TrustTTL).Unix()); err != nil {
		return Outcome{}, err
	}
	allowed, err := Reserve(ctx, tx, domain, email, ip, now)
	if err != nil {
		return Outcome{}, err
	}
	if !allowed {
		return Outcome{Status: "limited"}, nil
	}
	token, err := RandomToken()
	if err != nil {
		return Outcome{}, err
	}
	number, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return Outcome{}, err
	}
	code := fmt.Sprintf("%06d", number.Int64())
	if _, err = tx.ExecContext(ctx, `DELETE FROM account_login_codes WHERE created_at<? OR (domain=? AND email=? AND client_ip=?)`, now.Add(-CodeTTL).Unix(), domain, email, ip); err != nil {
		return Outcome{}, err
	}
	snapshot, err := passwordSnapshot(stored, "")
	if err != nil {
		return Outcome{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO account_login_codes(token,domain,email,client_ip,code_hash,password_hash,created_at,attempts,return_path,language) VALUES(?,?,?,?,?,?,?,0,?,?)`, token, domain, email, ip, digest(token+code), snapshot, now.Unix(), path, language)
	return Outcome{Status: "code", Token: token, Code: code, Email: email, Path: path, Language: language}, err
}

func Verify(ctx context.Context, tx *sql.Tx, domain, token, code, ip string, now time.Time) (Outcome, error) {
	var email, expected, passwordHash, path, language string
	var attempts int
	var created int64
	err := tx.QueryRowContext(ctx, `SELECT email,code_hash,password_hash,created_at,attempts,return_path,language FROM account_login_codes WHERE token=? AND domain=? AND client_ip=?`, token, domain, ip).Scan(&email, &expected, &passwordHash, &created, &attempts, &path, &language)
	if errors.Is(err, sql.ErrNoRows) {
		return Outcome{Status: "invalid"}, nil
	}
	if err != nil {
		return Outcome{}, err
	}
	if now.Unix()-created >= int64(CodeTTL/time.Second) || attempts >= 5 {
		return Outcome{Status: "invalid"}, nil
	}
	if _, err = tx.ExecContext(ctx, `UPDATE account_login_codes SET attempts=attempts+1 WHERE token=?`, token); err != nil {
		return Outcome{}, err
	}
	if len(code) != 6 || subtle.ConstantTimeCompare([]byte(digest(token+code)), []byte(expected)) != 1 {
		return Outcome{Status: "invalid", Email: email}, nil
	}
	var currentPassword string
	err = tx.QueryRowContext(ctx, `SELECT password FROM users WHERE domain=? AND email=? AND is_admin=1`, domain, email).Scan(&currentPassword)
	if errors.Is(err, sql.ErrNoRows) {
		return Outcome{Status: "invalid"}, nil
	}
	if err != nil {
		return Outcome{}, err
	}
	snapshot, snapshotErr := passwordSnapshot(currentPassword, passwordHash)
	if snapshotErr != nil || subtle.ConstantTimeCompare([]byte(snapshot), []byte(passwordHash)) != 1 {
		return Outcome{Status: "invalid"}, nil
	}
	consumed, err := tx.ExecContext(ctx, `DELETE FROM account_login_codes WHERE token=? AND attempts<=5`, token)
	if err != nil {
		return Outcome{}, err
	}
	count, err := consumed.RowsAffected()
	if err != nil {
		return Outcome{}, err
	}
	if count != 1 {
		return Outcome{Status: "invalid"}, nil
	}
	if err = RememberAddress(ctx, tx, domain, email, ip, now); err != nil {
		return Outcome{}, err
	}
	session, err := Session(ctx, tx, domain, email, ip, now)
	return Outcome{Status: "session", Token: session, Email: email, Path: path, Language: language}, err
}

// Address history is descriptive; it never replaces a new session's mail challenge.
func RememberAddress(ctx context.Context, tx *sql.Tx, domain, email, ip string, now time.Time) error {
	if ip == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_trusted_ips WHERE last_login<=?`, now.Add(-TrustTTL).Unix()); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE account_trusted_ips SET last_login=? WHERE domain=? AND email=? AND client_ip=?`, now.Unix(), domain, email, ip)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count > 0 {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO account_trusted_ips(domain,email,client_ip,confirmed_at,last_login) VALUES(?,?,?,?,?)`, domain, email, ip, now.Unix(), now.Unix())
	return err
}

func Revoke(ctx context.Context, tx *sql.Tx, domain, email, ip string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_trusted_ips WHERE domain=? AND email=? AND client_ip=?`, domain, email, ip); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_login_codes WHERE domain=? AND email=? AND client_ip=?`, domain, email, ip); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_email=? AND client_ip=?`, domain+"|"+email, ip)
	return err
}
