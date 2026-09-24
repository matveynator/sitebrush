package accounttotp

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func securityTOTPDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "security-totp.db"))
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

func TestSecurityBoundaryTOTPChallengeCannotCrossDomainOrIP(t *testing.T) {
	database := securityTOTPDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_200_000, 0).UTC()
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	code, err := Code(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("INSERT INTO account_totp(domain,email,secret,enabled_at) VALUES(?,?,?,?)", "example.com", "owner@example.com", secret, now.Unix()); err != nil {
		t.Fatal(err)
	}

	tx := mustBeginTOTP(t, database)
	token, err := BeginLogin(ctx, tx, "example.com", "owner@example.com", "192.0.2.50", "/admin", now)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		domain string
		ip     string
	}{
		{name: "wrong domain", domain: "evil.example", ip: "192.0.2.50"},
		{name: "wrong ip", domain: "example.com", ip: "192.0.2.51"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := mustBeginTOTP(t, database)
			if _, _, err := VerifyLogin(ctx, tx, tc.domain, token, tc.ip, code, now); err == nil {
				_ = tx.Rollback()
				t.Fatal("SECURITY: TOTP challenge crossed domain/IP boundary")
			}
			_ = tx.Rollback()
		})
	}

	tx = mustBeginTOTP(t, database)
	email, path, err := VerifyLogin(ctx, tx, "example.com", token, "192.0.2.50", code, now)
	if err != nil || email != "owner@example.com" || path != "/admin" {
		_ = tx.Rollback()
		t.Fatalf("valid TOTP challenge = %q %q %v", email, path, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	tx = mustBeginTOTP(t, database)
	if _, _, err := VerifyLogin(ctx, tx, "example.com", token, "192.0.2.50", code, now); err == nil {
		_ = tx.Rollback()
		t.Fatal("SECURITY: consumed TOTP challenge was replayed")
	}
	_ = tx.Rollback()
}

func TestSecurityBoundaryTOTPAttemptLimitCannotBeBypassed(t *testing.T) {
	database := securityTOTPDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_200_100, 0).UTC()
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	code, _ := Code(secret, now)
	if _, err := database.Exec("INSERT INTO account_totp(domain,email,secret,enabled_at) VALUES(?,?,?,?)", "example.com", "owner@example.com", secret, now.Unix()); err != nil {
		t.Fatal(err)
	}

	tx := mustBeginTOTP(t, database)
	token, err := BeginLogin(ctx, tx, "example.com", "owner@example.com", "198.51.100.50", "/", now)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 5; attempt++ {
		tx = mustBeginTOTP(t, database)
		if _, _, err := VerifyLogin(ctx, tx, "example.com", token, "198.51.100.50", "000000", now); err == nil {
			_ = tx.Rollback()
			t.Fatal("invalid TOTP unexpectedly accepted")
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}

	tx = mustBeginTOTP(t, database)
	if _, _, err := VerifyLogin(ctx, tx, "example.com", token, "198.51.100.50", code, now); err == nil {
		_ = tx.Rollback()
		t.Fatal("SECURITY: valid TOTP bypassed the five-attempt limit")
	}
	_ = tx.Rollback()
}

func TestSecurityBoundaryFallbackChallengeBindingAndExpiry(t *testing.T) {
	database := securityTOTPDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_200_200, 0).UTC()

	tx := mustBeginTOTP(t, database)
	token, err := BeginLogin(ctx, tx, "example.com", "owner@example.com", "203.0.113.50", "/fallback", now)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		domain string
		ip     string
	}{
		{"evil.example", "203.0.113.50"},
		{"example.com", "203.0.113.51"},
	} {
		tx = mustBeginTOTP(t, database)
		if _, _, err := ConsumeForFallback(ctx, tx, tc.domain, token, tc.ip, now); err == nil {
			_ = tx.Rollback()
			t.Fatal("SECURITY: fallback TOTP challenge crossed its binding boundary")
		}
		_ = tx.Rollback()
	}

	if _, err := database.Exec("UPDATE account_totp_challenges SET created_at=? WHERE token=?", now.Add(-ChallengeTTL).Unix(), token); err != nil {
		t.Fatal(err)
	}
	tx = mustBeginTOTP(t, database)
	if _, _, err := ConsumeForFallback(ctx, tx, "example.com", token, "203.0.113.50", now); err == nil {
		_ = tx.Rollback()
		t.Fatal("SECURITY: expired fallback TOTP challenge was accepted")
	}
	_ = tx.Rollback()
}

func TestSecurityBoundaryProvisioningURIEscapesAccountLabel(t *testing.T) {
	uri := ProvisioningURI("example.com", "owner@example.com/../../evil?x=1", "ABC DEF")
	parsed, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "otpauth" || parsed.Host != "totp" {
		t.Fatalf("unexpected provisioning URI: %q", uri)
	}
	if strings.Contains(parsed.EscapedPath(), "/../") || !strings.Contains(parsed.EscapedPath(), "%2F") || parsed.Query().Get("secret") != "ABC DEF" || parsed.Query().Get("issuer") != "SiteBrush" {
		t.Fatalf("SECURITY: provisioning URI escaped structured fields: %q", uri)
	}
}
