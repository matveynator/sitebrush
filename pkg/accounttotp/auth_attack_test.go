package accounttotp

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/accountauth"
	_ "modernc.org/sqlite"
)

// A successful challenge and its session are one database transaction, just as
// they are in the login handler. Concurrent replays must not mint extra sessions.
func TestAuthAttackConcurrentTOTPReplayCreatesOneSession(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "totp-replay.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	database.SetMaxOpenConns(20)
	for _, statement := range append(Schema(), accountauth.Schema()...) {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec(`CREATE TABLE sessions(token TEXT,user_email TEXT,created_at TEXT,client_ip TEXT,security_version INTEGER)`); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_200_000, 0).UTC()
	const domain, email, ip, secret = "example.com", "owner@example.com", "192.0.2.90", "GEZDGNBVGY3TQOJQGEZDGNBV3TQOJQGEZDGNBV3Q"
	if _, err := database.Exec(`INSERT INTO account_totp(domain,email,secret,enabled_at) VALUES(?,?,?,?)`, domain, email, secret, now.Unix()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	begin, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := BeginLogin(ctx, begin, domain, email, ip, "/admin", now)
	if err != nil {
		_ = begin.Rollback()
		t.Fatal(err)
	}
	if err := begin.Commit(); err != nil {
		t.Fatal(err)
	}
	code, err := Code(secret, now)
	if err != nil {
		t.Fatal(err)
	}

	const attempts = 50
	start := make(chan struct{})
	var workers sync.WaitGroup
	winners := make(chan struct{}, attempts)
	for attempt := 0; attempt < attempts; attempt++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			transaction, beginErr := database.BeginTx(ctx, nil)
			if beginErr != nil {
				return
			}
			_, _, verifyErr := VerifyLogin(ctx, transaction, domain, token, ip, code, now)
			if verifyErr != nil {
				_ = transaction.Rollback()
				return
			}
			if _, sessionErr := accountauth.Session(ctx, transaction, domain, email, ip, now); sessionErr != nil {
				_ = transaction.Rollback()
				return
			}
			if transaction.Commit() != nil {
				return
			}
			winners <- struct{}{}
		}()
	}
	close(start)
	workers.Wait()
	var sessionCount int
	if err := database.QueryRow(`SELECT COUNT(1) FROM sessions WHERE user_email=?`, domain+"|"+email).Scan(&sessionCount); err != nil {
		t.Fatal(err)
	}
	if len(winners) != 1 || sessionCount != 1 {
		t.Fatalf("SECURITY: concurrent replay successes=%d sessions=%d, want exactly one (attempts=%d)", len(winners), sessionCount, attempts)
	}
}

func TestAuthAttackTOTPChallengeRollbackDoesNotCreateSession(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "totp-rollback.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	for _, statement := range append(Schema(), accountauth.Schema()...) {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec(`CREATE TABLE sessions(token TEXT,user_email TEXT,created_at TEXT,client_ip TEXT,security_version INTEGER)`); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_200_000, 0).UTC()
	const domain, email, ip, secret = "example.com", "owner@example.com", "192.0.2.90", "GEZDGNBVGY3TQOJQGEZDGNBV3TQOJQGEZDGNBV3Q"
	if _, err := database.Exec(`INSERT INTO account_totp(domain,email,secret,enabled_at) VALUES(?,?,?,?)`, domain, email, secret, now.Unix()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	begin, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := BeginLogin(ctx, begin, domain, email, ip, "/", now)
	if err != nil || begin.Commit() != nil {
		t.Fatalf("create TOTP challenge: %v", err)
	}
	code, err := Code(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := VerifyLogin(ctx, transaction, domain, token, ip, code, now); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	var sessionCount int
	if err := database.QueryRow(`SELECT COUNT(1) FROM sessions`).Scan(&sessionCount); err != nil {
		t.Fatal(err)
	}
	if sessionCount != 0 {
		t.Fatalf("SECURITY: rolled-back TOTP transaction left %d sessions", sessionCount)
	}
}

func TestAuthAttackFutureTimestampCannotKeepTOTPChallengesAlive(t *testing.T) {
	database := securityTOTPDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_200_300, 0).UTC()
	const domain, email, ip, secret = "example.com", "owner@example.com", "192.0.2.94", "GEZDGNBVGY3TQOJQGEZDGNBV3TQOJQGEZDGNBV3Q"
	if _, err := database.Exec(`INSERT INTO account_totp(domain,email,secret,enabled_at) VALUES(?,?,?,?)`, domain, email, secret, now.Unix()); err != nil {
		t.Fatal(err)
	}
	code, err := Code(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"verify", "fallback"} {
		t.Run(kind, func(t *testing.T) {
			token := "future-" + kind
			if _, err := database.Exec(`INSERT INTO account_totp_challenges(token,domain,email,client_ip,return_path,created_at,attempts) VALUES(?,?,?,?,?,?,0)`, token, domain, email, ip, "/", now.Add(time.Second).Unix()); err != nil {
				t.Fatal(err)
			}
			transaction, err := database.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "verify" {
				_, _, err = VerifyLogin(ctx, transaction, domain, token, ip, code, now)
			} else {
				_, _, err = ConsumeForFallback(ctx, transaction, domain, token, ip, now)
			}
			_ = transaction.Rollback()
			if err == nil {
				t.Fatalf("SECURITY: TOTP %s challenge with future timestamp was accepted", kind)
			}
		})
	}
}
