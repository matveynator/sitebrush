package accounttotp

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestRFC6238SHA1Vector(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	code, err := Code(secret, time.Unix(59, 0))
	if err != nil {
		t.Fatal(err)
	}
	// RFC 6238 publishes eight digits; the six-digit HOTP truncation of the same value is 287082.
	if code != "287082" {
		t.Fatalf("unexpected code %q", code)
	}
	if !Verify(secret, code, time.Unix(59, 0)) {
		t.Fatal("generated code did not verify")
	}
}

func TestProvisioningURI(t *testing.T) {
	uri := ProvisioningURI("example.com", "owner@example.com", "ABCDEF")
	if uri == "" || uri[:10] != "otpauth://" {
		t.Fatalf("unexpected URI %q", uri)
	}
}

func TestTOTPValidationAndStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "totp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, statement := range Schema() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	code, err := Code(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Code("!", now); err == nil {
		t.Fatal("invalid secret unexpectedly generated a code")
	}
	if Verify(secret, "wrong", now) || Verify(secret, "12a456", now) || Verify("!", code, now) {
		t.Fatal("invalid TOTP input was accepted")
	}
	transaction := mustBeginTOTP(t, database)
	if err := Enable(ctx, transaction, "example.com", "owner@example.com", secret, "wrong", now); err == nil {
		t.Fatal("invalid code enabled TOTP")
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	transaction = mustBeginTOTP(t, database)
	if err := Enable(ctx, transaction, "example.com", "owner@example.com", secret, code, now); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	if !Enabled(ctx, database, "example.com", "owner@example.com") || Enabled(ctx, database, "other.example", "owner@example.com") {
		t.Fatal("TOTP enabled status is incorrect")
	}
	transaction = mustBeginTOTP(t, database)
	token, err := BeginLogin(ctx, transaction, "example.com", "owner@example.com", "192.0.2.1", "/settings", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	transaction = mustBeginTOTP(t, database)
	if _, _, err := VerifyLogin(ctx, transaction, "example.com", token, "192.0.2.1", "000000", now); err == nil {
		t.Fatal("invalid login code was accepted")
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	transaction = mustBeginTOTP(t, database)
	gotEmail, gotPath, err := VerifyLogin(ctx, transaction, "example.com", token, "192.0.2.1", code, now)
	if err != nil || gotEmail != "owner@example.com" || gotPath != "/settings" {
		t.Fatalf("verified login = %q %q %v", gotEmail, gotPath, err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	transaction = mustBeginTOTP(t, database)
	if err := Disable(ctx, transaction, "example.com", "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	if Enabled(ctx, database, "example.com", "owner@example.com") {
		t.Fatal("TOTP remained enabled after disable")
	}
}

func TestTOTPChallengeFallbackAndExpiry(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "totp-challenges.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, statement := range Schema() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	_, err = database.Exec(`INSERT INTO account_totp_challenges(token,domain,email,client_ip,return_path,created_at,attempts) VALUES(?,?,?,?,?,?,0)`, "expired", "example.com", "old@example.com", "192.0.2.1", "/old", now.Add(-ChallengeTTL-time.Second).Unix())
	if err != nil {
		t.Fatal(err)
	}
	transaction := mustBeginTOTP(t, database)
	token, err := BeginLogin(ctx, transaction, "example.com", "user@example.com", "192.0.2.1", "/", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	var expiredCount int
	if err := database.QueryRow(`SELECT COUNT(1) FROM account_totp_challenges WHERE token='expired'`).Scan(&expiredCount); err != nil || expiredCount != 0 {
		t.Fatalf("stale challenge count = %d, %v", expiredCount, err)
	}
	transaction = mustBeginTOTP(t, database)
	email, returnPath, err := ConsumeForFallback(ctx, transaction, "example.com", token, "192.0.2.1", now)
	if err != nil || email != "user@example.com" || returnPath != "/" {
		t.Fatalf("fallback challenge = %q %q %v", email, returnPath, err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	transaction = mustBeginTOTP(t, database)
	if _, _, err := ConsumeForFallback(ctx, transaction, "example.com", token, "192.0.2.1", now); err == nil {
		t.Fatal("consumed challenge was reused")
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`INSERT INTO account_totp_challenges(token,domain,email,client_ip,return_path,created_at,attempts) VALUES(?,?,?,?,?,?,0)`, "expired", "example.com", "old@example.com", "192.0.2.1", "/old", now.Add(-ChallengeTTL).Unix())
	if err != nil {
		t.Fatal(err)
	}
	transaction = mustBeginTOTP(t, database)
	if _, _, err := ConsumeForFallback(ctx, transaction, "example.com", "expired", "192.0.2.1", now); err == nil {
		t.Fatal("expired challenge was accepted")
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyLoginRejectsClientMismatchAndAttemptLimit(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "totp-attempts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, statement := range Schema() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec(`INSERT INTO account_totp(domain,email,secret,enabled_at) VALUES(?,?,?,?)`, "example.com", "owner@example.com", "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO account_totp_challenges(token,domain,email,client_ip,return_path,created_at,attempts) VALUES(?,?,?,?,?,?,5)`, "limited", "example.com", "owner@example.com", "192.0.2.1", "/", now.Unix()); err != nil {
		t.Fatal(err)
	}
	tx := mustBeginTOTP(t, database)
	if _, _, err := VerifyLogin(ctx, tx, "example.com", "limited", "198.51.100.1", "000000", now); err == nil {
		t.Fatal("challenge from mismatched IP accepted")
	}
	if _, _, err := VerifyLogin(ctx, tx, "example.com", "limited", "192.0.2.1", "000000", now); err == nil {
		t.Fatal("challenge over attempt limit accepted")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func mustBeginTOTP(t *testing.T, database *sql.DB) *sql.Tx {
	t.Helper()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	return transaction
}


// BEGIN TOTP replay and race regression tests.

func TestTOTPChallengeConcurrentConsumptionSucceedsOnce(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "totp-race.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	for _, statement := range Schema() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	if _, err := database.Exec(`INSERT INTO account_totp(domain,email,secret,enabled_at) VALUES(?,?,?,?)`, "example.com", "owner@example.com", secret, now.Unix()); err != nil {
		t.Fatal(err)
	}
	transaction := mustBeginTOTP(t, database)
	token, err := BeginLogin(ctx, transaction, "example.com", "owner@example.com", "192.0.2.70", "/profile", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	code, err := Code(secret, now)
	if err != nil {
		t.Fatal(err)
	}

	const consumers = 32
	results := make(chan bool, consumers)
	start := make(chan struct{})
	for consumerIndex := 0; consumerIndex < consumers; consumerIndex++ {
		go func() {
			<-start
			transaction, err := database.Begin()
			if err != nil {
				results <- false
				return
			}
			_, _, verifyErr := VerifyLogin(ctx, transaction, "example.com", token, "192.0.2.70", code, now)
			if verifyErr != nil {
				_ = transaction.Rollback()
				results <- false
				return
			}
			if err := transaction.Commit(); err != nil {
				results <- false
				return
			}
			results <- true
		}()
	}
	close(start)

	successes := 0
	for resultIndex := 0; resultIndex < consumers; resultIndex++ {
		select {
		case success := <-results:
			if success {
				successes++
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent TOTP verification did not finish")
		}
	}
	if successes != 1 {
		t.Fatalf("successful TOTP consumptions=%d want=1", successes)
	}
}

func TestTOTPFallbackConcurrentConsumptionSucceedsOnce(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "totp-fallback-race.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	for _, statement := range Schema() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	transaction := mustBeginTOTP(t, database)
	token, err := BeginLogin(ctx, transaction, "example.com", "owner@example.com", "192.0.2.71", "/profile", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}

	const consumers = 32
	results := make(chan bool, consumers)
	start := make(chan struct{})
	for consumerIndex := 0; consumerIndex < consumers; consumerIndex++ {
		go func() {
			<-start
			transaction, err := database.Begin()
			if err != nil {
				results <- false
				return
			}
			_, _, consumeErr := ConsumeForFallback(ctx, transaction, "example.com", token, "192.0.2.71", now)
			if consumeErr != nil {
				_ = transaction.Rollback()
				results <- false
				return
			}
			if err := transaction.Commit(); err != nil {
				results <- false
				return
			}
			results <- true
		}()
	}
	close(start)

	successes := 0
	for resultIndex := 0; resultIndex < consumers; resultIndex++ {
		select {
		case success := <-results:
			if success {
				successes++
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent TOTP fallback consumption did not finish")
		}
	}
	if successes != 1 {
		t.Fatalf("successful fallback consumptions=%d want=1", successes)
	}
}

// END TOTP replay and race regression tests.
