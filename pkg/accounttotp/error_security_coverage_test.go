package accounttotp

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newBrokenTOTPDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "broken-totp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func TestTOTPSecurityFailsClosedWhenStorageIsUnavailable(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_800_500_000, 0).UTC()

	t.Run("enabled", func(t *testing.T) {
		database := newBrokenTOTPDatabase(t)
		if Enabled(ctx, database, "example.org", "owner@example.org") {
			t.Fatal("SECURITY: TOTP reported enabled when storage lookup failed")
		}
	})

	t.Run("enable", func(t *testing.T) {
		database := newBrokenTOTPDatabase(t)
		secret, _ := GenerateSecret()
		code, _ := Code(secret, now)
		tx := mustBeginTOTP(t, database)
		if err := Enable(ctx, tx, "example.org", "owner@example.org", secret, code, now); err == nil {
			_ = tx.Rollback()
			t.Fatal("SECURITY: TOTP enable succeeded against incomplete storage")
		}
		_ = tx.Rollback()
	})

	t.Run("begin login", func(t *testing.T) {
		database := newBrokenTOTPDatabase(t)
		tx := mustBeginTOTP(t, database)
		if _, err := BeginLogin(ctx, tx, "example.org", "owner@example.org", "192.0.2.80", "/", now); err == nil {
			_ = tx.Rollback()
			t.Fatal("SECURITY: TOTP challenge creation succeeded without challenge storage")
		}
		_ = tx.Rollback()
	})

	t.Run("verify login", func(t *testing.T) {
		database := newBrokenTOTPDatabase(t)
		tx := mustBeginTOTP(t, database)
		if _, _, err := VerifyLogin(ctx, tx, "example.org", "missing", "192.0.2.80", "123456", now); err == nil {
			_ = tx.Rollback()
			t.Fatal("SECURITY: TOTP verification succeeded without storage")
		}
		_ = tx.Rollback()
	})

	t.Run("fallback", func(t *testing.T) {
		database := newBrokenTOTPDatabase(t)
		tx := mustBeginTOTP(t, database)
		if _, _, err := ConsumeForFallback(ctx, tx, "example.org", "missing", "192.0.2.80", now); err == nil {
			_ = tx.Rollback()
			t.Fatal("SECURITY: fallback challenge succeeded without storage")
		}
		_ = tx.Rollback()
	})
}

func TestTOTPRandomTokenHasExpectedEntropyShape(t *testing.T) {
	first, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 64 || len(second) != 64 || first == second {
		t.Fatalf("TOTP challenge tokens have unexpected shape: %q %q", first, second)
	}
}
