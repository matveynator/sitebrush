package accountauth

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAccountAuthFailsClosedWhenRequiredTablesAreUnavailable(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_800_300_000, 0).UTC()

	tests := []struct {
		name string
		drop string
		call func(*sql.Tx) error
	}{
		{
			name: "credentials",
			drop: "DROP TABLE users",
			call: func(tx *sql.Tx) error {
				matched, err := Credentials(ctx, tx, "example.org", "owner@example.org", "password")
				if err == nil || matched {
					t.Fatalf("SECURITY: unavailable users table authenticated user: matched=%v err=%v", matched, err)
				}
				return nil
			},
		},
		{
			name: "password",
			drop: "DROP TABLE users",
			call: func(tx *sql.Tx) error {
				outcome, err := Password(ctx, tx, "example.org", "owner@example.org", "password", "192.0.2.1", "/", "en", now)
				if err == nil || outcome.Status != "" {
					t.Fatalf("SECURITY: unavailable users table produced login outcome %#v err=%v", outcome, err)
				}
				return nil
			},
		},
		{
			name: "challenge trusted IP cleanup",
			drop: "DROP TABLE account_trusted_ips",
			call: func(tx *sql.Tx) error {
				outcome, err := Challenge(ctx, tx, "example.org", "owner@example.org", "192.0.2.1", "/", "en", now)
				if err == nil || outcome.Status != "" {
					t.Fatalf("SECURITY: challenge continued after trusted-IP storage failure: %#v err=%v", outcome, err)
				}
				return nil
			},
		},
		{
			name: "verify challenge lookup",
			drop: "DROP TABLE account_login_codes",
			call: func(tx *sql.Tx) error {
				outcome, err := Verify(ctx, tx, "example.org", "missing", "000000", "192.0.2.1", now)
				if err == nil || outcome.Status != "" {
					t.Fatalf("SECURITY: verification continued after challenge storage failure: %#v err=%v", outcome, err)
				}
				return nil
			},
		},
		{
			name: "verify link lookup",
			drop: "DROP TABLE account_login_codes",
			call: func(tx *sql.Tx) error {
				outcome, err := VerifyLink(ctx, tx, "example.org", "missing", "192.0.2.1", now)
				if err == nil || outcome.Status != "" {
					t.Fatalf("SECURITY: login link continued after challenge storage failure: %#v err=%v", outcome, err)
				}
				return nil
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := testDatabase(t)
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(tc.drop); err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
			if err := tc.call(tx); err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
			_ = tx.Rollback()
		})
	}
}

func TestAccountAuthPasswordBranchesAndAddressLifecycle(t *testing.T) {
	db := testDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_300_100, 0).UTC()

	for _, tc := range []struct {
		email    string
		password string
		status   string
	}{
		{"missing@example.org", "password", "credentials"},
		{"owner@example.org", "wrong", "credentials"},
	} {
		outcome := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
			return Password(ctx, tx, "example.org", tc.email, tc.password, "192.0.2.7", "/", "en", now)
		})
		if outcome.Status != tc.status {
			t.Fatalf("password branch outcome = %#v", outcome)
		}
	}

	local := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
		return Password(ctx, tx, "example.org", "owner@example.org", "password", "192.0.2.7", "/admin", "en", now, true)
	})
	if local.Status != "session" || local.Token == "" {
		t.Fatalf("local session = %#v", local)
	}
	if err := transactError(t, db, func(tx *sql.Tx) error {
		return RememberAddress(ctx, tx, "example.org", "owner@example.org", "192.0.2.7", now.Add(time.Minute))
	}); err != nil {
		t.Fatal(err)
	}
	var lastLogin int64
	if err := db.QueryRow("SELECT last_login FROM account_trusted_ips WHERE domain=? AND email=? AND client_ip=?", "example.org", "owner@example.org", "192.0.2.7").Scan(&lastLogin); err != nil {
		t.Fatal(err)
	}
	if lastLogin != now.Add(time.Minute).Unix() {
		t.Fatalf("trusted address last_login=%d", lastLogin)
	}
	if err := transactError(t, db, func(tx *sql.Tx) error {
		return RememberAddress(ctx, tx, "example.org", "owner@example.org", "", now)
	}); err != nil {
		t.Fatal(err)
	}
}

func transactError(t *testing.T, db *sql.DB, call func(*sql.Tx) error) error {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if err := call(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func TestAccountAuthReserveWindowAndCooldownBranches(t *testing.T) {
	db := testDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_300_200, 0).UTC()

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := Reserve(ctx, tx, "example.org", "owner@example.org", "198.51.100.9", now)
	if err != nil || !allowed {
		_ = tx.Rollback()
		t.Fatalf("first reserve = %v %v", allowed, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	tx, _ = db.Begin()
	allowed, err = Reserve(ctx, tx, "example.org", "owner@example.org", "198.51.100.9", now.Add(time.Second))
	if err != nil || allowed {
		_ = tx.Rollback()
		t.Fatalf("SECURITY: cooldown reserve = %v %v", allowed, err)
	}
	_ = tx.Rollback()

	if _, err := db.Exec(
		"UPDATE account_code_rates SET window_start=?,last_sent=?,sent_count=? WHERE domain=? AND email=? AND client_ip=?",
		now.Add(-CodeTTL-time.Second).Unix(), now.Add(-CodeSendCooldown-time.Second).Unix(), CodeSendLimit,
		"example.org", "owner@example.org", "198.51.100.9",
	); err != nil {
		t.Fatal(err)
	}
	tx, _ = db.Begin()
	allowed, err = Reserve(ctx, tx, "example.org", "owner@example.org", "198.51.100.9", now)
	if err != nil || !allowed {
		_ = tx.Rollback()
		t.Fatalf("new rate window = %v %v", allowed, err)
	}
	_ = tx.Rollback()
}

func TestSecurityBoundaryClientIPRejectsMalformedForwardingChains(t *testing.T) {
	tests := []struct {
		name       string
		remote     string
		forwarded  string
		xff        string
		trusted    string
		want       string
	}{
		{name: "invalid peer", remote: "not-an-ip", want: ""},
		{name: "untrusted peer ignores spoofed xff", remote: "198.51.100.5:1234", xff: "1.2.3.4", want: "198.51.100.5"},
		{name: "trusted proxy malformed xff fails closed", remote: "127.0.0.1:1234", xff: "not-an-ip", want: ""},
		{name: "forwarded quoted address", remote: "127.0.0.1:1234", forwarded: `for="203.0.113.4:456";proto=https`, want: "203.0.113.4"},
		{name: "all trusted chain has no client", remote: "10.0.0.2:1234", xff: "10.0.0.1", trusted: "10.0.0.0/8", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", "https://example.org/", nil)
			request.RemoteAddr = tc.remote
			request.Header.Set("Forwarded", tc.forwarded)
			request.Header.Set("X-Forwarded-For", tc.xff)
			if got := ClientIP(request, tc.trusted); got != tc.want {
				t.Fatalf("ClientIP=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestSecurityBoundarySafeQueryRedactsCredentialLikeNames(t *testing.T) {
	raw := "safe=ok&TOKEN=secret&authorization_code=abc&password=x&return_path=%2Fadmin&email_confirm=y"
	got := SafeQuery(raw)
	for _, secret := range []string{"secret", "abc", "password=x", "%2Fadmin", "email_confirm=y"} {
		if strings.Contains(strings.ToLower(got), strings.ToLower(secret)) {
			t.Fatalf("SECURITY: SafeQuery leaked %q in %q", secret, got)
		}
	}
	if !strings.Contains(got, "safe=ok") || !strings.Contains(got, "%5Bredacted%5D") {
		t.Fatalf("SafeQuery = %q", got)
	}
	if got := SafeQuery("%zz"); got != "[redacted]" {
		t.Fatalf("malformed query = %q", got)
	}
}
