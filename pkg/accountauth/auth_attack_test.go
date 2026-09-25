package accountauth

import (
	"context"
	"database/sql"
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
