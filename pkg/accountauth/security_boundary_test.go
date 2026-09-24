package accountauth

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestSecurityBoundaryPasswordChangeInvalidatesPendingChallenges(t *testing.T) {
	db := testDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()

	challenge := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
		return Challenge(ctx, tx, "example.org", "owner@example.org", "192.0.2.20", "/admin", "en", now)
	})
	if challenge.Status != "code" {
		t.Fatalf("challenge = %#v", challenge)
	}

	if _, err := db.Exec("UPDATE users SET password=? WHERE domain=? AND email=?", "changed-password", "example.org", "owner@example.org"); err != nil {
		t.Fatal(err)
	}

	codeResult := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
		return Verify(ctx, tx, "example.org", challenge.Token, challenge.Code, "192.0.2.20", now.Add(time.Second))
	})
	if codeResult.Status != "invalid" {
		t.Fatalf("SECURITY: code issued before password change remained valid: %#v", codeResult)
	}

	linkResult := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
		return VerifyLink(ctx, tx, "example.org", challenge.Token, "192.0.2.20", now.Add(time.Second))
	})
	if linkResult.Status != "invalid" {
		t.Fatalf("SECURITY: login link issued before password change remained valid: %#v", linkResult)
	}
}

func TestSecurityBoundaryChallengeCannotCrossDomainOrClientIP(t *testing.T) {
	db := testDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_100, 0).UTC()

	challenge := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
		return Challenge(ctx, tx, "example.org", "owner@example.org", "198.51.100.10", "/", "en", now)
	})
	if challenge.Status != "code" {
		t.Fatalf("challenge = %#v", challenge)
	}

	for _, test := range []struct {
		name   string
		domain string
		ip     string
	}{
		{name: "other domain", domain: "evil.example", ip: "198.51.100.10"},
		{name: "other address", domain: "example.org", ip: "198.51.100.11"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
				return Verify(ctx, tx, test.domain, challenge.Token, challenge.Code, test.ip, now.Add(time.Second))
			})
			if result.Status != "invalid" {
				t.Fatalf("SECURITY: challenge crossed account boundary: %#v", result)
			}
		})
	}

	valid := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
		return Verify(ctx, tx, "example.org", challenge.Token, challenge.Code, "198.51.100.10", now.Add(2*time.Second))
	})
	if valid.Status != "session" {
		t.Fatalf("valid challenge rejected after boundary probes: %#v", valid)
	}
}

func TestSecurityBoundaryExpiredAndAttemptLimitedLinksDoNotCreateSessions(t *testing.T) {
	db := testDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_200, 0).UTC()

	expired := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
		return Challenge(ctx, tx, "example.org", "owner@example.org", "203.0.113.20", "/", "en", now)
	})
	if expired.Status != "code" {
		t.Fatalf("challenge = %#v", expired)
	}
	result := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
		return VerifyLink(ctx, tx, "example.org", expired.Token, "203.0.113.20", now.Add(CodeTTL))
	})
	if result.Status != "invalid" {
		t.Fatalf("SECURITY: expired login link created session: %#v", result)
	}

	limited := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
		return Challenge(ctx, tx, "example.org", "owner@example.org", "203.0.113.21", "/", "en", now.Add(CodeSendCooldown))
	})
	if limited.Status != "code" {
		t.Fatalf("second challenge = %#v", limited)
	}
	if _, err := db.Exec("UPDATE account_login_codes SET attempts=5 WHERE token=?", limited.Token); err != nil {
		t.Fatal(err)
	}
	result = transact(t, db, func(tx *sql.Tx) (Outcome, error) {
		return VerifyLink(ctx, tx, "example.org", limited.Token, "203.0.113.21", now.Add(CodeSendCooldown+time.Second))
	})
	if result.Status != "invalid" {
		t.Fatalf("SECURITY: attempt-limited login link created session: %#v", result)
	}
}

func TestSecurityBoundaryRevokeRemovesEveryAddressCredential(t *testing.T) {
	db := testDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_300, 0).UTC()
	ip := "203.0.113.30"

	challenge := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
		return Challenge(ctx, tx, "example.org", "owner@example.org", ip, "/", "en", now)
	})
	if challenge.Status != "code" {
		t.Fatalf("challenge = %#v", challenge)
	}
	session := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
		return Password(ctx, tx, "example.org", "owner@example.org", "password", ip, "/", "en", now.Add(CodeSendCooldown), true)
	})
	if session.Status != "session" {
		t.Fatalf("local session = %#v", session)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := Revoke(ctx, tx, "example.org", "owner@example.org", ip); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	checks := []struct {
		query string
		args  []any
		name  string
	}{
		{"SELECT COUNT(*) FROM account_trusted_ips WHERE domain=? AND email=? AND client_ip=?", []any{"example.org", "owner@example.org", ip}, "trusted IP"},
		{"SELECT COUNT(*) FROM account_login_codes WHERE domain=? AND email=? AND client_ip=?", []any{"example.org", "owner@example.org", ip}, "login challenge"},
		{"SELECT COUNT(*) FROM sessions WHERE user_email=? AND client_ip=?", []any{"example.org|owner@example.org", ip}, "session"},
	}
	for _, check := range checks {
		var count int
		if err := db.QueryRow(check.query, check.args...).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("SECURITY: revoke left %s active", check.name)
		}
	}
}

func TestSecurityBoundaryPasswordSnapshotRejectsMalformedState(t *testing.T) {
	for _, previous := range []string{"broken", "zz:11", strings.Repeat("00", 15) + ":hash"} {
		if _, err := passwordSnapshot("password", previous); err == nil {
			t.Fatalf("SECURITY: malformed password snapshot %q was accepted", previous)
		}
	}
}


func TestSecurityBoundaryReserveEnforcesCooldownWindowAndReset(t *testing.T) {
	db := testDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_400, 0).UTC()

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := Reserve(ctx, tx, "example.org", "owner@example.org", "192.0.2.60", now)
	if err != nil || !allowed {
		_ = tx.Rollback()
		t.Fatalf("first reserve = %v, %v", allowed, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	tx, _ = db.Begin()
	allowed, err = Reserve(ctx, tx, "example.org", "owner@example.org", "192.0.2.60", now.Add(time.Second))
	if err != nil || allowed {
		_ = tx.Rollback()
		t.Fatalf("SECURITY: cooldown reserve = %v, %v", allowed, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Expire the current window explicitly. A fresh request must be accepted
	// instead of inheriting a stale brute-force counter forever.
	if _, err := db.Exec("UPDATE account_code_rates SET window_start=?, last_sent=?",
		now.Add(-2*CodeTTL).Unix(), now.Add(-2*CodeTTL).Unix()); err != nil {
		t.Fatal(err)
	}
	tx, _ = db.Begin()
	allowed, err = Reserve(ctx, tx, "example.org", "owner@example.org", "192.0.2.60", now.Add(CodeTTL))
	if err != nil || !allowed {
		_ = tx.Rollback()
		t.Fatalf("expired reserve window = %v, %v", allowed, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestSecurityBoundaryReserveEnforcesSendLimit(t *testing.T) {
	db := testDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_500, 0).UTC()
	domain, email, ip := "example.org", "owner@example.org", "198.51.100.60"

	if _, err := db.Exec(
		"INSERT INTO account_code_rates(domain,email,client_ip,window_start,last_sent,sent_count) VALUES(?,?,?,?,?,?)",
		domain, email, ip, now.Unix(), now.Add(-CodeSendCooldown).Unix(), CodeSendLimit,
	); err != nil {
		t.Fatal(err)
	}

	tx, _ := db.Begin()
	allowed, err := Reserve(ctx, tx, domain, email, ip, now)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if allowed {
		_ = tx.Rollback()
		t.Fatal("SECURITY: brute-force send limit was bypassed")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestAccountCredentialsAndRememberAddressBranches(t *testing.T) {
	db := testDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_600, 0).UTC()

	tx, _ := db.Begin()
	ok, err := Credentials(ctx, tx, "example.org", "missing@example.org", "password")
	if err != nil || ok {
		_ = tx.Rollback()
		t.Fatalf("missing credentials = %v, %v", ok, err)
	}
	ok, err = Credentials(ctx, tx, "example.org", "owner@example.org", "wrong")
	if err != nil || ok {
		_ = tx.Rollback()
		t.Fatalf("wrong credentials = %v, %v", ok, err)
	}
	ok, err = Credentials(ctx, tx, "example.org", "owner@example.org", "password")
	if err != nil || !ok {
		_ = tx.Rollback()
		t.Fatalf("valid credentials = %v, %v", ok, err)
	}
	if err := RememberAddress(ctx, tx, "example.org", "owner@example.org", "", now); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := RememberAddress(ctx, tx, "example.org", "owner@example.org", "203.0.113.60", now); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := RememberAddress(ctx, tx, "example.org", "owner@example.org", "203.0.113.60", now.Add(time.Minute)); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var lastLogin int64
	if err := db.QueryRow("SELECT last_login FROM account_trusted_ips WHERE domain=? AND email=? AND client_ip=?",
		"example.org", "owner@example.org", "203.0.113.60").Scan(&lastLogin); err != nil {
		t.Fatal(err)
	}
	if lastLogin != now.Add(time.Minute).Unix() {
		t.Fatalf("trusted address last_login = %d", lastLogin)
	}
}

func TestAccountRandomTokenAndDigestProperties(t *testing.T) {
	first, err := RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 64 || len(second) != 64 || first == second {
		t.Fatalf("random token properties = %q %q", first, second)
	}
	if digest("same") != digest("same") || digest("same") == digest("different") {
		t.Fatal("digest determinism/collision sanity check failed")
	}
}
