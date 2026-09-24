package accounttotp

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func totpCoverageDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "totp-coverage.db"))
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

func withTOTPTransaction(t *testing.T, database *sql.DB, call func(*sql.Tx) error) {
	t.Helper()
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := call(tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestTOTPSecurityEnableDisableAndSecretValidation(t *testing.T) {
	database := totpCoverageDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_400_000, 0).UTC()
	secret, err := GenerateSecret()
	if err != nil || secret == "" {
		t.Fatalf("secret=%q err=%v", secret, err)
	}
	code, err := Code(secret, now)
	if err != nil {
		t.Fatal(err)
	}

	tx, _ := database.Begin()
	if err := Enable(ctx, tx, "example.org", "owner@example.org", secret, "000000", now); err == nil {
		_ = tx.Rollback()
		t.Fatal("SECURITY: invalid TOTP code enabled second factor")
	}
	_ = tx.Rollback()

	withTOTPTransaction(t, database, func(tx *sql.Tx) error {
		return Enable(ctx, tx, "example.org", "owner@example.org", secret, code, now)
	})
	if !Enabled(ctx, database, "example.org", "owner@example.org") {
		t.Fatal("enabled TOTP was not persisted")
	}

	// Re-enabling must replace the secret only after validating the new secret's code.
	nextSecret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	nextCode, err := Code(nextSecret, now)
	if err != nil {
		t.Fatal(err)
	}
	withTOTPTransaction(t, database, func(tx *sql.Tx) error {
		return Enable(ctx, tx, "example.org", "owner@example.org", nextSecret, nextCode, now)
	})
	if Verify(secret, code, now) && !Verify(nextSecret, nextCode, now) {
		t.Fatal("new TOTP secret verification failed")
	}

	withTOTPTransaction(t, database, func(tx *sql.Tx) error {
		return Disable(ctx, tx, "example.org", "owner@example.org")
	})
	if Enabled(ctx, database, "example.org", "owner@example.org") {
		t.Fatal("disabled TOTP remained enabled")
	}

	if _, err := Code("not-base32!!!", now); err == nil {
		t.Fatal("invalid base32 TOTP secret was accepted")
	}
}

func TestTOTPSecurityChallengeBindingAttemptLimitAndReplay(t *testing.T) {
	database := totpCoverageDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_400_100, 0).UTC()
	secret, _ := GenerateSecret()
	code, _ := Code(secret, now)
	withTOTPTransaction(t, database, func(tx *sql.Tx) error {
		return Enable(ctx, tx, "example.org", "owner@example.org", secret, code, now)
	})

	var token string
	withTOTPTransaction(t, database, func(tx *sql.Tx) error {
		var err error
		token, err = BeginLogin(ctx, tx, "example.org", "owner@example.org", "192.0.2.50", "/admin", now)
		return err
	})

	for _, tc := range []struct{ domain, ip string }{
		{"evil.example", "192.0.2.50"},
		{"example.org", "192.0.2.51"},
	} {
		tx, _ := database.Begin()
		if _, _, err := VerifyLogin(ctx, tx, tc.domain, token, tc.ip, code, now); err == nil {
			_ = tx.Rollback()
			t.Fatal("SECURITY: TOTP challenge crossed domain/IP boundary")
		}
		_ = tx.Rollback()
	}

	for attempt := 0; attempt < 5; attempt++ {
		withTOTPTransaction(t, database, func(tx *sql.Tx) error {
			_, _, err := VerifyLogin(ctx, tx, "example.org", token, "192.0.2.50", "999999", now)
			if err == nil {
				t.Fatal("invalid TOTP code accepted")
			}
			return nil
		})
	}
	tx, _ := database.Begin()
	if _, _, err := VerifyLogin(ctx, tx, "example.org", token, "192.0.2.50", code, now); err == nil {
		_ = tx.Rollback()
		t.Fatal("SECURITY: correct code bypassed attempt limit")
	}
	_ = tx.Rollback()

	var validToken string
	withTOTPTransaction(t, database, func(tx *sql.Tx) error {
		var err error
		validToken, err = BeginLogin(ctx, tx, "example.org", "owner@example.org", "192.0.2.50", "/admin", now.Add(time.Second))
		return err
	})
	validCode, _ := Code(secret, now.Add(time.Second))
	var email, path string
	withTOTPTransaction(t, database, func(tx *sql.Tx) error {
		var err error
		email, path, err = VerifyLogin(ctx, tx, "example.org", validToken, "192.0.2.50", validCode, now.Add(time.Second))
		return err
	})
	if email != "owner@example.org" || path != "/admin" {
		t.Fatalf("verified TOTP=%q %q", email, path)
	}
	tx, _ = database.Begin()
	if _, _, err := VerifyLogin(ctx, tx, "example.org", validToken, "192.0.2.50", validCode, now.Add(time.Second)); err == nil {
		_ = tx.Rollback()
		t.Fatal("SECURITY: consumed TOTP challenge was replayed")
	}
	_ = tx.Rollback()
}

func TestTOTPSecurityFallbackBindingExpiryAndSingleUse(t *testing.T) {
	database := totpCoverageDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_400_200, 0).UTC()

	var token string
	withTOTPTransaction(t, database, func(tx *sql.Tx) error {
		var err error
		token, err = BeginLogin(ctx, tx, "example.org", "owner@example.org", "203.0.113.50", "/fallback", now)
		return err
	})

	for _, tc := range []struct{ domain, ip string }{
		{"evil.example", "203.0.113.50"},
		{"example.org", "203.0.113.51"},
	} {
		tx, _ := database.Begin()
		if _, _, err := ConsumeForFallback(ctx, tx, tc.domain, token, tc.ip, now); err == nil {
			_ = tx.Rollback()
			t.Fatal("SECURITY: fallback challenge crossed domain/IP boundary")
		}
		_ = tx.Rollback()
	}

	var expired string
	withTOTPTransaction(t, database, func(tx *sql.Tx) error {
		var err error
		expired, err = BeginLogin(ctx, tx, "example.org", "owner@example.org", "203.0.113.51", "/", now)
		return err
	})
	tx, _ := database.Begin()
	if _, _, err := ConsumeForFallback(ctx, tx, "example.org", expired, "203.0.113.51", now.Add(ChallengeTTL)); err == nil {
		_ = tx.Rollback()
		t.Fatal("SECURITY: expired fallback challenge was accepted")
	}
	_ = tx.Rollback()

	var email, path string
	withTOTPTransaction(t, database, func(tx *sql.Tx) error {
		var err error
		email, path, err = ConsumeForFallback(ctx, tx, "example.org", token, "203.0.113.50", now.Add(time.Second))
		return err
	})
	if email != "owner@example.org" || path != "/fallback" {
		t.Fatalf("fallback=%q %q", email, path)
	}
	tx, _ = database.Begin()
	if _, _, err := ConsumeForFallback(ctx, tx, "example.org", token, "203.0.113.50", now.Add(2*time.Second)); err == nil {
		_ = tx.Rollback()
		t.Fatal("SECURITY: consumed fallback challenge was replayed")
	}
	_ = tx.Rollback()
}

func TestTOTPVerificationWindowAndFormatting(t *testing.T) {
	secret, _ := GenerateSecret()
	now := time.Unix(1_800_400_300, 0).UTC()
	for _, offset := range []time.Duration{-Period, 0, Period} {
		code, err := Code(secret, now.Add(offset))
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != CodeDigits || !Verify(secret, code, now) {
			t.Fatalf("valid TOTP window offset %s rejected", offset)
		}
	}
	for _, code := range []string{"", "12345", "1234567", "12a456"} {
		if Verify(secret, code, now) {
			t.Fatalf("invalid TOTP code %q accepted", code)
		}
	}
}
