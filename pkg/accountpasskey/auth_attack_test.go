package accountpasskey

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestAuthAttackConcurrentWebAuthnChallengeReplayCommitsOnce(t *testing.T) {
	database := securityPasskeyDatabase(t)
	now := time.Unix(1_800_000_000, 0).UTC()
	const token = "one-time-login"
	storeSecurityChallenge(t, database, "example.com", "", "login", "192.0.2.92", token,
		`{"challenge":"challenge-value"}`, now)
	database.SetMaxOpenConns(1)

	const attempts = 50
	start := make(chan struct{})
	var workers sync.WaitGroup
	winners := make(chan struct{}, attempts)
	for attempt := 0; attempt < attempts; attempt++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			transaction, err := database.BeginTx(context.Background(), nil)
			if err != nil {
				return
			}
			if _, _, err = consumeLoginChallenge(context.Background(), transaction, "example.com", "192.0.2.92", token, now); err != nil {
				_ = transaction.Rollback()
				return
			}
			if err = transaction.Commit(); err != nil {
				return
			}
			winners <- struct{}{}
		}()
	}
	close(start)
	workers.Wait()

	var challengeCount int
	if err := database.QueryRow(`SELECT COUNT(1) FROM account_webauthn_challenges WHERE token=?`, token).Scan(&challengeCount); err != nil {
		t.Fatal(err)
	}
	if len(winners) != 1 || challengeCount != 0 {
		t.Fatalf("SECURITY: concurrent WebAuthn replay successes=%d remaining_challenges=%d", len(winners), challengeCount)
	}
}

func TestAuthAttackFutureTimestampCannotKeepWebAuthnChallengeAlive(t *testing.T) {
	database := securityPasskeyDatabase(t)
	now := time.Unix(1_800_000_100, 0).UTC()
	storeSecurityChallenge(t, database, "example.com", "owner@example.com", "register", "192.0.2.93", "future-registration",
		`{"challenge":"future"}`, now.Add(time.Second))
	transaction := mustBeginPasskey(t, database)
	if _, err := consumeChallenge(context.Background(), transaction, "example.com", "owner@example.com", "register", "192.0.2.93", "future-registration", now); err == nil {
		_ = transaction.Rollback()
		t.Fatal("SECURITY: WebAuthn registration challenge with a future timestamp was accepted")
	}
	_ = transaction.Rollback()
}

func TestAuthAttackPasskeyDeleteCannotTargetAnotherUsersCredential(t *testing.T) {
	database := securityPasskeyDatabase(t)
	ctx := context.Background()
	credentialJSON := `{"id":"credential"}`
	if _, err := database.Exec(`INSERT INTO account_passkeys(domain,email,user_handle,credential_id,credential_json,created_at,last_used_at) VALUES(?,?,?,?,?,?,0)`,
		"example.com", "owner@example.com", "owner-handle", "owner-credential", credentialJSON, 1); err != nil {
		t.Fatal(err)
	}
	transaction := mustBeginPasskey(t, database)
	if err := Delete(ctx, transaction, "example.com", "attacker@example.com", "owner-credential"); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := database.QueryRow(`SELECT COUNT(1) FROM account_passkeys WHERE domain=? AND email=? AND credential_id=?`,
		"example.com", "owner@example.com", "owner-credential").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatal("SECURITY: another account's passkey was deleted by an IDOR request")
	}
}
