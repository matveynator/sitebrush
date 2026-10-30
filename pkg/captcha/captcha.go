// Package captcha keeps one-use answers on the server and renders only raster challenges.
package captcha

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const Lifetime = 5 * time.Minute

type Challenge struct {
	Token  string
	Answer string
}

func SchemaQueries() []string {
	return append(InitialSchemaQueries(),
		`CREATE TABLE IF NOT EXISTS account_captcha_fail_events(client_ip TEXT NOT NULL,purpose TEXT NOT NULL,failed_at INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_account_captcha_fail_ip_purpose_time ON account_captcha_fail_events(client_ip,purpose,failed_at)`,
	)
}

// The original three steps keep their migration IDs on existing installations.
func InitialSchemaQueries() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS account_captchas(token_hash TEXT PRIMARY KEY,client_ip TEXT NOT NULL,domain TEXT NOT NULL,purpose TEXT NOT NULL,answer_hash TEXT NOT NULL,created_at INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_account_captchas_ip_time ON account_captchas(client_ip,created_at)`,
		`CREATE TABLE IF NOT EXISTS account_captcha_rates(client_ip TEXT PRIMARY KEY,window_start INTEGER NOT NULL,issued INTEGER NOT NULL)`,
	}
}

func digest(secret string) string {
	hash := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(hash[:])
}

func New() (Challenge, error) {
	var entropy [38]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return Challenge{}, err
	}
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	answer := make([]byte, 6)
	for index := range answer {
		answer[index] = alphabet[int(entropy[index+32])%len(alphabet)]
	}
	return Challenge{Token: hex.EncodeToString(entropy[:32]), Answer: string(answer)}, nil
}

// Issuance and consumption run in the existing SQL owner's serialized process.
// Per-IP and global bounds keep image refreshes from creating unbounded storage.
func Save(transaction *sql.Tx, challenge Challenge, ip, domain, purpose string, now time.Time) (bool, error) {
	if ip == "" || domain == "" {
		return false, errors.New("CAPTCHA request identity is unavailable")
	}
	if _, err := transaction.Exec(`DELETE FROM account_captchas WHERE created_at<?`, now.Add(-Lifetime).Unix()); err != nil {
		return false, err
	}
	if _, err := transaction.Exec(`DELETE FROM account_captcha_rates WHERE window_start<?`, now.Add(-time.Minute).Unix()); err != nil {
		return false, err
	}
	var issued int
	err := transaction.QueryRow(`SELECT issued FROM account_captcha_rates WHERE client_ip=?`, ip).Scan(&issued)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	var total int
	if err := transaction.QueryRow(`SELECT COUNT(*) FROM account_captchas`).Scan(&total); err != nil {
		return false, err
	}
	issueLimit := 6
	if purpose == "trial_preview" || purpose == "test_drive" {
		issueLimit = 60
	}
	if issued >= issueLimit || total >= 10000 {
		return false, nil
	}
	if _, err := transaction.Exec(`INSERT INTO account_captcha_rates(client_ip,window_start,issued) VALUES(?,?,1) ON CONFLICT(client_ip) DO UPDATE SET issued=account_captcha_rates.issued+1`, ip, now.Unix()); err != nil {
		return false, err
	}
	_, err = transaction.Exec(`INSERT INTO account_captchas(token_hash,client_ip,domain,purpose,answer_hash,created_at) VALUES(?,?,?,?,?,?)`, digest(challenge.Token), ip, domain, purpose, digest(challenge.Token+"\x00"+challenge.Answer), now.Unix())
	return err == nil, err
}

// Every attempt spends the challenge, including a wrong answer or identity.
// Knowing the opaque cookie never provides or selects the expected answer.
func Verify(transaction *sql.Tx, token, answer, ip, domain, purpose string, now time.Time) (verified bool, resultErr error) {
	if purpose == "register" {
		if _, err := transaction.Exec(`DELETE FROM account_captcha_fail_events WHERE failed_at<?`, now.Add(-24*time.Hour).Unix()); err != nil {
			return false, err
		}
		var failures int
		if err := transaction.QueryRow(`SELECT COUNT(*) FROM account_captcha_fail_events WHERE client_ip=? AND purpose=?`, ip, purpose).Scan(&failures); err != nil {
			return false, err
		}
		if failures >= 12 {
			return false, nil
		}
		defer func() {
			if !verified && resultErr == nil {
				_, resultErr = transaction.Exec(`INSERT INTO account_captcha_fail_events(client_ip,purpose,failed_at) VALUES(?,?,?)`, ip, purpose, now.Unix())
			}
		}()
	}
	if len(token) != 64 {
		return false, nil
	}
	var expected, sourceIP, sourceDomain, sourcePurpose string
	var issued int64
	err := transaction.QueryRow(`SELECT answer_hash,client_ip,domain,purpose,created_at FROM account_captchas WHERE token_hash=?`, digest(token)).Scan(&expected, &sourceIP, &sourceDomain, &sourcePurpose, &issued)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	consumed, err := transaction.Exec(`DELETE FROM account_captchas WHERE token_hash=?`, digest(token))
	if err != nil {
		return false, err
	}
	count, err := consumed.RowsAffected()
	if err != nil || count != 1 {
		return false, err
	}
	if sourceIP != ip || sourceDomain != domain || sourcePurpose != purpose || issued > now.Unix()-2 || issued < now.Add(-Lifetime).Unix() {
		return false, nil
	}
	normalized := strings.TrimSpace(answer)
	if len(normalized) != 6 {
		return false, nil
	}
	actual := digest(token + "\x00" + strings.ToUpper(normalized))
	return subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1, nil
}
