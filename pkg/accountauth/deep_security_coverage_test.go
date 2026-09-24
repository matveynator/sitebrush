package accountauth

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestAccountAuthChallengeAndVerifyCorruptionBranches(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_800_800_000, 0).UTC()

	t.Run("challenge unknown administrator", func(t *testing.T) {
		db := testDatabase(t)
		outcome := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
			return Challenge(ctx, tx, "example.org", "missing@example.org", "192.0.2.90", "/", "en", now)
		})
		if outcome.Status != "credentials" {
			t.Fatalf("unknown admin challenge=%#v", outcome)
		}
	})

	t.Run("verify malformed password snapshot", func(t *testing.T) {
		db := testDatabase(t)
		challenge := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
			return Challenge(ctx, tx, "example.org", "owner@example.org", "192.0.2.91", "/", "en", now)
		})
		if _, err := db.Exec("UPDATE account_login_codes SET password_hash=? WHERE token=?", "malformed", challenge.Token); err != nil {
			t.Fatal(err)
		}
		outcome := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
			return Verify(ctx, tx, "example.org", challenge.Token, challenge.Code, "192.0.2.91", now.Add(time.Second))
		})
		if outcome.Status != "invalid" {
			t.Fatalf("SECURITY: malformed snapshot authenticated: %#v", outcome)
		}
	})

	t.Run("verify administrator removed after issue", func(t *testing.T) {
		db := testDatabase(t)
		challenge := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
			return Challenge(ctx, tx, "example.org", "owner@example.org", "192.0.2.92", "/", "en", now)
		})
		if _, err := db.Exec("DELETE FROM users WHERE domain=? AND email=?", "example.org", "owner@example.org"); err != nil {
			t.Fatal(err)
		}
		outcome := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
			return Verify(ctx, tx, "example.org", challenge.Token, challenge.Code, "192.0.2.92", now.Add(time.Second))
		})
		if outcome.Status != "invalid" {
			t.Fatalf("SECURITY: removed administrator authenticated: %#v", outcome)
		}
	})

	t.Run("login link malformed snapshot", func(t *testing.T) {
		db := testDatabase(t)
		challenge := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
			return Challenge(ctx, tx, "example.org", "owner@example.org", "192.0.2.93", "/", "en", now)
		})
		if _, err := db.Exec("UPDATE account_login_codes SET password_hash=? WHERE token=?", "malformed", challenge.Token); err != nil {
			t.Fatal(err)
		}
		outcome := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
			return VerifyLink(ctx, tx, "example.org", challenge.Token, "192.0.2.93", now.Add(time.Second))
		})
		if outcome.Status != "invalid" {
			t.Fatalf("SECURITY: login link with malformed snapshot authenticated: %#v", outcome)
		}
	})

	t.Run("login link administrator removed", func(t *testing.T) {
		db := testDatabase(t)
		challenge := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
			return Challenge(ctx, tx, "example.org", "owner@example.org", "192.0.2.94", "/", "en", now)
		})
		if _, err := db.Exec("DELETE FROM users WHERE domain=? AND email=?", "example.org", "owner@example.org"); err != nil {
			t.Fatal(err)
		}
		outcome := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
			return VerifyLink(ctx, tx, "example.org", challenge.Token, "192.0.2.94", now.Add(time.Second))
		})
		if outcome.Status != "invalid" {
			t.Fatalf("SECURITY: login link authenticated removed administrator: %#v", outcome)
		}
	})
}

func TestAccountAuthStorageErrorBranches(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_800_800_100, 0).UTC()

	t.Run("session write error", func(t *testing.T) {
		db := testDatabase(t)
		if _, err := db.Exec("DROP TABLE sessions"); err != nil {
			t.Fatal(err)
		}
		tx, _ := db.Begin()
		if token, err := Session(ctx, tx, "example.org", "owner@example.org", "192.0.2.1", now); err == nil || token == "" {
			_ = tx.Rollback()
			t.Fatalf("session write token=%q err=%v", token, err)
		}
		_ = tx.Rollback()
	})

	t.Run("reserve read error", func(t *testing.T) {
		db := testDatabase(t)
		if _, err := db.Exec("DROP TABLE account_code_rates"); err != nil {
			t.Fatal(err)
		}
		tx, _ := db.Begin()
		if allowed, err := Reserve(ctx, tx, "example.org", "owner@example.org", "192.0.2.1", now); err == nil || allowed {
			_ = tx.Rollback()
			t.Fatalf("reserve after storage loss=%v err=%v", allowed, err)
		}
		_ = tx.Rollback()
	})

	t.Run("remember address write error", func(t *testing.T) {
		db := testDatabase(t)
		if _, err := db.Exec("DROP TABLE account_trusted_ips"); err != nil {
			t.Fatal(err)
		}
		tx, _ := db.Begin()
		if err := RememberAddress(ctx, tx, "example.org", "owner@example.org", "192.0.2.1", now); err == nil {
			_ = tx.Rollback()
			t.Fatal("trusted-address storage failure was hidden")
		}
		_ = tx.Rollback()
	})

	t.Run("revoke first delete error", func(t *testing.T) {
		db := testDatabase(t)
		if _, err := db.Exec("DROP TABLE account_trusted_ips"); err != nil {
			t.Fatal(err)
		}
		tx, _ := db.Begin()
		if err := Revoke(ctx, tx, "example.org", "owner@example.org", "192.0.2.1"); err == nil {
			_ = tx.Rollback()
			t.Fatal("revoke hid trusted-IP storage failure")
		}
		_ = tx.Rollback()
	})

	t.Run("revoke second delete error", func(t *testing.T) {
		db := testDatabase(t)
		if _, err := db.Exec("DROP TABLE account_login_codes"); err != nil {
			t.Fatal(err)
		}
		tx, _ := db.Begin()
		if err := Revoke(ctx, tx, "example.org", "owner@example.org", "192.0.2.1"); err == nil {
			_ = tx.Rollback()
			t.Fatal("revoke hid challenge storage failure")
		}
		_ = tx.Rollback()
	})

	t.Run("revoke session delete error", func(t *testing.T) {
		db := testDatabase(t)
		if _, err := db.Exec("DROP TABLE sessions"); err != nil {
			t.Fatal(err)
		}
		tx, _ := db.Begin()
		if err := Revoke(ctx, tx, "example.org", "owner@example.org", "192.0.2.1"); err == nil {
			_ = tx.Rollback()
			t.Fatal("revoke hid session storage failure")
		}
		_ = tx.Rollback()
	})
}

func TestAccountAuthVerifyMalformedCodesAndAttempts(t *testing.T) {
	db := testDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_800_200, 0).UTC()
	challenge := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
		return Challenge(ctx, tx, "example.org", "owner@example.org", "203.0.113.90", "/", "en", now)
	})

	for _, code := range []string{"", "1", "12345", "1234567", "abcdef"} {
		outcome := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
			return Verify(ctx, tx, "example.org", challenge.Token, code, "203.0.113.90", now.Add(time.Second))
		})
		if outcome.Status != "invalid" {
			t.Fatalf("SECURITY: malformed code %q accepted: %#v", code, outcome)
		}
	}
	if _, err := db.Exec("UPDATE account_login_codes SET attempts=5 WHERE token=?", challenge.Token); err != nil {
		t.Fatal(err)
	}
	outcome := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
		return Verify(ctx, tx, "example.org", challenge.Token, challenge.Code, "203.0.113.90", now.Add(time.Second))
	})
	if outcome.Status != "invalid" {
		t.Fatalf("SECURITY: attempt limit bypassed: %#v", outcome)
	}
}
