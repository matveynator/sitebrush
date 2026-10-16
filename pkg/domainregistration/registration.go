// Package domainregistration owns durable domain reservations, independently of TLS and passwords.
package domainregistration

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const Schema = `CREATE TABLE IF NOT EXISTS domain_registration_claims(domain TEXT PRIMARY KEY,token TEXT NOT NULL,email TEXT NOT NULL DEFAULT '',kind TEXT NOT NULL,confirmed_at TEXT NOT NULL)`

var ErrTaken = errors.New("domain is already reserved; choose another name or continue using your confirmation link")

// Claim runs in the server control database's single writer. The unique key is
// the arbiter between concurrent registration confirmations and trial imports.
func Claim(ctx context.Context, db *sql.DB, domain, token, email, kind string) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var oldToken, oldKind string
	err = tx.QueryRowContext(ctx, `SELECT token,kind FROM domain_registration_claims WHERE domain=?`, domain).Scan(&oldToken, &oldKind)
	if err == nil {
		if oldToken == token {
			return false, tx.Commit()
		}
		var activeTrial int
		if oldKind == "trial" && kind == "registration" {
			if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM public_trial_sites WHERE domain=? AND status='active' AND delete_after>?`, domain, time.Now().UTC().Format(time.RFC3339)).Scan(&activeTrial); err != nil {
				return false, err
			}
		}
		if activeTrial == 0 {
			return false, ErrTaken
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM domain_registration_claims WHERE domain=?`, domain); err != nil {
			return false, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if kind == "trial" {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM registration_confirmations WHERE domain=? AND expires_at>?`, domain, time.Now().UTC().Format(time.RFC3339)).Scan(&count); err != nil {
			return false, err
		}
		if count > 0 {
			return false, ErrTaken
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO domain_registration_claims(domain,token,email,kind,confirmed_at) VALUES(?,?,?,?,?)`, domain, token, email, kind, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return false, err
	}
	if kind == "registration" {
		if _, err = tx.ExecContext(ctx, `UPDATE registration_confirmations SET expires_at=? WHERE token=? AND domain=?`, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339), token, domain); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}
