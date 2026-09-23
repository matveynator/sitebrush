// Package accounttotp implements RFC 6238 codes and account-bound TOTP challenges.
package accounttotp

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	CodeDigits   = 6
	Period       = 30 * time.Second
	ChallengeTTL = 5 * time.Minute
)

func Schema() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS account_totp(domain TEXT,email TEXT,secret TEXT,enabled_at INTEGER,PRIMARY KEY(domain,email))`,
		`CREATE TABLE IF NOT EXISTS account_totp_challenges(token TEXT PRIMARY KEY,domain TEXT,email TEXT,client_ip TEXT,return_path TEXT,created_at INTEGER,attempts INTEGER DEFAULT 0)`,
	}
}

func GenerateSecret() (string, error) {
	buffer := make([]byte, 20)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buffer), nil
}

func ProvisioningURI(domain, email, secret string) string {
	label := "SiteBrush:" + strings.TrimSpace(email)
	query := url.Values{}
	query.Set("secret", strings.TrimSpace(secret))
	query.Set("issuer", "SiteBrush")
	query.Set("algorithm", "SHA1")
	query.Set("digits", strconv.Itoa(CodeDigits))
	query.Set("period", strconv.Itoa(int(Period/time.Second)))
	return (&url.URL{Scheme: "otpauth", Host: "totp", Path: "/" + label, RawQuery: query.Encode()}).String()
}

func Code(secret string, at time.Time) (string, error) {
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", err
	}
	counter := uint64(at.Unix() / int64(Period/time.Second))
	var counterBuffer [8]byte
	binary.BigEndian.PutUint64(counterBuffer[:], counter)
	mac := hmac.New(sha1.New, decoded)
	if _, err = mac.Write(counterBuffer[:]); err != nil {
		return "", err
	}
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset])&0x7f)<<24 |
		uint32(sum[offset+1])<<16 |
		uint32(sum[offset+2])<<8 |
		uint32(sum[offset+3])
	return fmt.Sprintf("%0*d", CodeDigits, value%1000000), nil
}

func Verify(secret, code string, at time.Time) bool {
	normalizedCode := strings.TrimSpace(code)
	if len(normalizedCode) != CodeDigits {
		return false
	}
	for _, digit := range normalizedCode {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	for offset := -1; offset <= 1; offset++ {
		expected, err := Code(secret, at.Add(time.Duration(offset)*Period))
		if err == nil && hmac.Equal([]byte(expected), []byte(normalizedCode)) {
			return true
		}
	}
	return false
}

func Enabled(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, domain, email string) bool {
	var count int
	err := database.QueryRowContext(ctx, `SELECT COUNT(1) FROM account_totp WHERE domain=? AND email=?`, domain, email).Scan(&count)
	return err == nil && count > 0
}

func Enable(ctx context.Context, transaction *sql.Tx, domain, email, secret, code string, now time.Time) error {
	if !Verify(secret, code, now) {
		return errors.New("invalid TOTP code")
	}
	_, err := transaction.ExecContext(ctx, `INSERT INTO account_totp(domain,email,secret,enabled_at) VALUES(?,?,?,?) ON CONFLICT(domain,email) DO UPDATE SET secret=excluded.secret,enabled_at=excluded.enabled_at`,
		domain, email, strings.TrimSpace(secret), now.Unix())
	return err
}

func Disable(ctx context.Context, transaction *sql.Tx, domain, email string) error {
	_, err := transaction.ExecContext(ctx, `DELETE FROM account_totp WHERE domain=? AND email=?`, domain, email)
	return err
}

func BeginLogin(ctx context.Context, transaction *sql.Tx, domain, email, clientIP, returnPath string, now time.Time) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	_, err = transaction.ExecContext(ctx, `DELETE FROM account_totp_challenges WHERE created_at<? OR (domain=? AND email=? AND client_ip=?)`,
		now.Add(-ChallengeTTL).Unix(), domain, email, clientIP)
	if err != nil {
		return "", err
	}
	_, err = transaction.ExecContext(ctx, `INSERT INTO account_totp_challenges(token,domain,email,client_ip,return_path,created_at,attempts) VALUES(?,?,?,?,?,?,0)`,
		token, domain, email, clientIP, returnPath, now.Unix())
	return token, err
}

func VerifyLogin(ctx context.Context, transaction *sql.Tx, domain, token, clientIP, code string, now time.Time) (email, returnPath string, err error) {
	var secret string
	var createdAt int64
	var attempts int
	err = transaction.QueryRowContext(ctx, `SELECT c.email,c.return_path,c.created_at,c.attempts,t.secret
		FROM account_totp_challenges c
		JOIN account_totp t ON t.domain=c.domain AND t.email=c.email
		WHERE c.token=? AND c.domain=? AND c.client_ip=?`, token, domain, clientIP).
		Scan(&email, &returnPath, &createdAt, &attempts, &secret)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", errors.New("invalid TOTP challenge")
	}
	if err != nil {
		return "", "", err
	}
	if now.Unix()-createdAt >= int64(ChallengeTTL/time.Second) || attempts >= 5 {
		return "", "", errors.New("expired TOTP challenge")
	}
	if _, err = transaction.ExecContext(ctx, `UPDATE account_totp_challenges SET attempts=attempts+1 WHERE token=?`, token); err != nil {
		return "", "", err
	}
	if !Verify(secret, code, now) {
		return "", "", errors.New("invalid TOTP code")
	}
	result, err := transaction.ExecContext(ctx, `DELETE FROM account_totp_challenges WHERE token=? AND attempts<=5`, token)
	if err != nil {
		return "", "", err
	}
	consumed, err := result.RowsAffected()
	if err != nil || consumed != 1 {
		if err == nil {
			err = errors.New("TOTP challenge already consumed")
		}
		return "", "", err
	}
	return email, returnPath, nil
}

func randomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", buffer), nil
}
