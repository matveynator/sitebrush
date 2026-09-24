package accountpasskey

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	_ "modernc.org/sqlite"
)

func securityPasskeyDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "security-passkeys.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	for _, statement := range Schema() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func storeSecurityChallenge(t *testing.T, database *sql.DB, domain, email, kind, ip, token, sessionJSON string, created time.Time) {
	t.Helper()
	_, err := database.Exec(
		"INSERT INTO account_webauthn_challenges(token,domain,email,kind,session_json,client_ip,return_path,created_at) VALUES(?,?,?,?,?,?,?,?)",
		token, domain, email, kind, sessionJSON, ip, "/return", created.Unix(),
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSecurityBoundaryRegistrationChallengeBinding(t *testing.T) {
	database := securityPasskeyDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_100_000, 0).UTC()
	session := webauthn.SessionData{Challenge: "challenge"}
	sessionJSON := "{\"challenge\":\"challenge\"}"
	storeSecurityChallenge(t, database, "example.com", "owner@example.com", "register", "192.0.2.40", "register-token", sessionJSON, now)

	tests := []struct {
		name   string
		domain string
		email  string
		kind   string
		ip     string
	}{
		{name: "domain", domain: "evil.example", email: "owner@example.com", kind: "register", ip: "192.0.2.40"},
		{name: "email", domain: "example.com", email: "other@example.com", kind: "register", ip: "192.0.2.40"},
		{name: "kind", domain: "example.com", email: "owner@example.com", kind: "login", ip: "192.0.2.40"},
		{name: "ip", domain: "example.com", email: "owner@example.com", kind: "register", ip: "192.0.2.41"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tx := mustBeginPasskey(t, database)
			if _, err := consumeChallenge(ctx, tx, tc.domain, tc.email, tc.kind, tc.ip, "register-token", now); err == nil {
				_ = tx.Rollback()
				t.Fatal("SECURITY: WebAuthn registration challenge crossed its binding boundary")
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}

	tx := mustBeginPasskey(t, database)
	consumed, err := consumeChallenge(ctx, tx, "example.com", "owner@example.com", "register", "192.0.2.40", "register-token", now)
	if err != nil || consumed.Challenge != session.Challenge {
		_ = tx.Rollback()
		t.Fatalf("valid challenge = %#v, %v", consumed, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	tx = mustBeginPasskey(t, database)
	if _, err := consumeChallenge(ctx, tx, "example.com", "owner@example.com", "register", "192.0.2.40", "register-token", now); err == nil {
		_ = tx.Rollback()
		t.Fatal("SECURITY: consumed registration challenge was replayed")
	}
	_ = tx.Rollback()
}

func TestSecurityBoundaryLoginChallengeBindingExpiryAndReplay(t *testing.T) {
	database := securityPasskeyDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_100_100, 0).UTC()
	sessionJSON := "{\"challenge\":\"login-challenge\"}"

	storeSecurityChallenge(t, database, "example.com", "", "login", "198.51.100.40", "login-token", sessionJSON, now)
	storeSecurityChallenge(t, database, "example.com", "", "login", "198.51.100.40", "expired-token", sessionJSON, now.Add(-ChallengeTTL))

	for _, tc := range []struct {
		name   string
		domain string
		ip     string
		token  string
	}{
		{name: "wrong domain", domain: "evil.example", ip: "198.51.100.40", token: "login-token"},
		{name: "wrong ip", domain: "example.com", ip: "198.51.100.41", token: "login-token"},
		{name: "expired", domain: "example.com", ip: "198.51.100.40", token: "expired-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := mustBeginPasskey(t, database)
			if _, _, err := consumeLoginChallenge(ctx, tx, tc.domain, tc.ip, tc.token, now); err == nil {
				_ = tx.Rollback()
				t.Fatal("SECURITY: invalid WebAuthn login challenge was accepted")
			}
			_ = tx.Rollback()
		})
	}

	tx := mustBeginPasskey(t, database)
	session, path, err := consumeLoginChallenge(ctx, tx, "example.com", "198.51.100.40", "login-token", now)
	if err != nil || session.Challenge != "login-challenge" || path != "/return" {
		_ = tx.Rollback()
		t.Fatalf("valid login challenge = %#v, %q, %v", session, path, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	tx = mustBeginPasskey(t, database)
	if _, _, err := consumeLoginChallenge(ctx, tx, "example.com", "198.51.100.40", "login-token", now); err == nil {
		_ = tx.Rollback()
		t.Fatal("SECURITY: consumed WebAuthn login challenge was replayed")
	}
	_ = tx.Rollback()
}

func TestSecurityBoundaryMalformedLoginSessionRollsBackThroughFinishLogin(t *testing.T) {
	database := securityPasskeyDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_100_200, 0).UTC()
	storeSecurityChallenge(t, database, "example.com", "", "login", "203.0.113.40", "malformed-login", "not-json", now)

	for attempt := 0; attempt < 2; attempt++ {
		tx := mustBeginPasskey(t, database)
		if _, _, err := FinishLogin(
			ctx,
			tx,
			"example.com",
			"example.com",
			"https://example.com",
			"203.0.113.40",
			"malformed-login",
			nil,
			now,
		); err == nil {
			_ = tx.Rollback()
			t.Fatal("SECURITY: malformed WebAuthn login session was accepted")
		}
		// Production accountTransaction rolls the transaction back when
		// FinishLogin returns an error. The challenge delete must therefore
		// roll back too rather than being tested under an artificial commit.
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}

		var count int
		if err := database.QueryRow(
			"SELECT COUNT(*) FROM account_webauthn_challenges WHERE token=?",
			"malformed-login",
		).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("SECURITY: rollback changed malformed challenge count to %d, want 1", count)
		}
	}
}

func TestSecurityBoundaryRelyingPartyConfigurationRejectsInvalidValues(t *testing.T) {
	for _, tc := range []struct {
		domain string
		origin string
	}{
		{"", "https://example.com"},
		{"example.com", ""},
		{" ", "https://example.com"},
	} {
		if _, err := service(tc.domain, tc.origin); err == nil {
			t.Fatalf("SECURITY: invalid WebAuthn relying party accepted domain=%q origin=%q", tc.domain, tc.origin)
		}
	}
}
