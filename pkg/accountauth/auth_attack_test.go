package accountauth

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestAuthAttackConcurrentLoginCodeReplayCreatesOneSession(t *testing.T) {
	database := testDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_800_300, 0).UTC()
	challenge := transact(t, database, func(transaction *sql.Tx) (Outcome, error) {
		return Password(ctx, transaction, "example.org", "owner@example.org", "password", "192.0.2.91", "/admin", "en", now)
	})
	if challenge.Status != "code" {
		t.Fatalf("login challenge = %#v", challenge)
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
			transaction, err := database.BeginTx(ctx, nil)
			if err != nil {
				return
			}
			result, err := Verify(ctx, transaction, "example.org", challenge.Token, challenge.Code, "192.0.2.91", now.Add(time.Second))
			if err != nil || result.Status != "session" {
				_ = transaction.Rollback()
				return
			}
			if err := transaction.Commit(); err != nil {
				return
			}
			winners <- struct{}{}
		}()
	}
	close(start)
	workers.Wait()

	var sessionCount int
	if err := database.QueryRow(`SELECT COUNT(1) FROM sessions WHERE user_email=?`, "example.org|owner@example.org").Scan(&sessionCount); err != nil {
		t.Fatal(err)
	}
	if len(winners) != 1 || sessionCount != 1 {
		t.Fatalf("SECURITY: concurrent code replay successes=%d sessions=%d, want exactly one of %d", len(winners), sessionCount, attempts)
	}
}

func TestAuthAttackConcurrentMagicLinkReplayCreatesOneSession(t *testing.T) {
	database := testDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_800_400, 0).UTC()
	challenge := transact(t, database, func(transaction *sql.Tx) (Outcome, error) {
		return Password(ctx, transaction, "example.org", "owner@example.org", "password", "192.0.2.92", "/admin", "en", now)
	})
	if challenge.Status != "code" {
		t.Fatalf("login challenge = %#v", challenge)
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
			transaction, err := database.BeginTx(ctx, nil)
			if err != nil {
				return
			}
			result, err := VerifyLink(ctx, transaction, "example.org", challenge.Token, "192.0.2.92", now.Add(time.Second))
			if err != nil || result.Status != "session" {
				_ = transaction.Rollback()
				return
			}
			if err := transaction.Commit(); err != nil {
				return
			}
			winners <- struct{}{}
		}()
	}
	close(start)
	workers.Wait()

	var sessionCount int
	if err := database.QueryRow(`SELECT COUNT(1) FROM sessions WHERE user_email=?`, "example.org|owner@example.org").Scan(&sessionCount); err != nil {
		t.Fatal(err)
	}
	if len(winners) != 1 || sessionCount != 1 {
		t.Fatalf("SECURITY: concurrent magic-link replay successes=%d sessions=%d, want exactly one of %d", len(winners), sessionCount, attempts)
	}
}

func TestAuthAttackFutureTimestampCannotExtendLoginChallengeLifetime(t *testing.T) {
	for _, useMagicLink := range []bool{false, true} {
		name := "login code"
		if useMagicLink {
			name = "magic link"
		}
		t.Run(name, func(t *testing.T) {
			database := testDatabase(t)
			ctx := context.Background()
			now := time.Unix(1_800_800_500, 0).UTC()
			challenge := transact(t, database, func(transaction *sql.Tx) (Outcome, error) {
				return Password(ctx, transaction, "example.org", "owner@example.org", "password", "192.0.2.93", "/", "en", now)
			})
			if challenge.Status != "code" {
				t.Fatalf("login challenge = %#v", challenge)
			}
			if _, err := database.Exec(`UPDATE account_login_codes SET created_at=? WHERE token=?`, now.Add(time.Minute).Unix(), challenge.Token); err != nil {
				t.Fatal(err)
			}
			result := transact(t, database, func(transaction *sql.Tx) (Outcome, error) {
				if useMagicLink {
					return VerifyLink(ctx, transaction, "example.org", challenge.Token, "192.0.2.93", now)
				}
				return Verify(ctx, transaction, "example.org", challenge.Token, challenge.Code, "192.0.2.93", now)
			})
			if result.Status != "invalid" {
				t.Fatalf("SECURITY: challenge from the future authenticated: %#v", result)
			}
		})
	}
}

func TestMailAbuseConcurrentSendReservationAllowsOnlyOneAction(t *testing.T) {
	database := testDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_800_600, 0).UTC()
	const attempts = 50
	start := make(chan struct{})
	results := make(chan bool, attempts)
	var workers sync.WaitGroup
	for attempt := 0; attempt < attempts; attempt++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			transaction, err := database.BeginTx(ctx, nil)
			if err != nil {
				results <- false
				return
			}
			allowed, reserveErr := Reserve(ctx, transaction, "example.org", "owner@example.org", "192.0.2.94", now)
			if reserveErr != nil || transaction.Commit() != nil {
				_ = transaction.Rollback()
				results <- false
				return
			}
			results <- allowed
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	allowedActions := 0
	for allowed := range results {
		if allowed {
			allowedActions++
		}
	}
	var sentCount int
	if err := database.QueryRow(`SELECT sent_count FROM account_code_rates WHERE domain=? AND email=? AND client_ip=?`, "example.org", "owner@example.org", "192.0.2.94").Scan(&sentCount); err != nil {
		t.Fatal(err)
	}
	if allowedActions != 1 || sentCount != 1 {
		t.Fatalf("SECURITY: concurrent email reservations allowed=%d recorded=%d, want exactly one", allowedActions, sentCount)
	}
}

func TestDatabaseAuthorizationStateStaysIsolatedForSameEmailAcrossDomains(t *testing.T) {
	database := testDatabase(t)
	ctx := context.Background()
	const email = "owner@example.org"
	if _, err := database.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "other.org", email, "password"); err != nil {
		t.Fatal(err)
	}

	alphaToken, err := transactSession(t, database, "example.org", email, "192.0.2.10", time.Unix(1_800_801_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	betaToken, err := transactSession(t, database, "other.org", email, "192.0.2.20", time.Unix(1_800_801_001, 0))
	if err != nil {
		t.Fatal(err)
	}
	if alphaToken == betaToken {
		t.Fatal("session tokens unexpectedly collided across domains")
	}

	for candidateIndex := 0; candidateIndex < sessionIPCandidateLimit+10; candidateIndex++ {
		clientIP := fmt.Sprintf("198.51.100.%d", candidateIndex+1)
		if _, err := transactSession(t, database, "example.org", email, clientIP, time.Unix(int64(candidateIndex+2_000), 0)); err != nil {
			t.Fatal(err)
		}
	}

	var alphaCount, betaCount int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(1) FROM account_session_ips WHERE domain=? AND email=?`, "example.org", email).Scan(&alphaCount); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(1) FROM account_session_ips WHERE domain=? AND email=?`, "other.org", email).Scan(&betaCount); err != nil {
		t.Fatal(err)
	}
	if alphaCount != sessionIPCandidateLimit || betaCount != 1 {
		t.Fatalf("session IP state crossed domain boundary: alpha=%d beta=%d", alphaCount, betaCount)
	}

	var betaSessionCount int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(1) FROM sessions WHERE user_email=?`, "other.org|"+email).Scan(&betaSessionCount); err != nil {
		t.Fatal(err)
	}
	if betaSessionCount != 1 {
		t.Fatalf("same-email account session count for other domain=%d, want 1", betaSessionCount)
	}
}
