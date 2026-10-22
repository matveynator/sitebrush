package mailguard

import (
	"database/sql"
	"testing"
	"time"
)

func TestLoopbackMailLimitsCannotCreateAnIPBlock(t *testing.T) {
	database := guardDatabase(t)
	now := time.Now().UTC()
	for _, ip := range []string{"127.0.0.1", "127.9.8.7", "::1", "::ffff:127.0.0.1"} {
		guardTransaction(t, database, func(transaction *sql.Tx) (Decision, error) {
			if SetBlock(transaction, ip, "manual", now.Add(time.Hour)) == nil {
				t.Fatal("local access IP could be blocked", ip)
			}
			if _, err := transaction.Exec(`INSERT INTO mail_guard_blocks VALUES(?,?,?)`, ip, "legacy", now.Add(time.Hour).Unix()); err != nil {
				return Decision{}, err
			}
			blocked, err := Blocked(transaction, ip, now)
			if err != nil || blocked {
				t.Fatal("legacy loopback rule enforced", ip, blocked, err)
			}
			for range 9 {
				decision, err := Observe(transaction, ip, "owner@example.com", "recover", now)
				if err != nil {
					return Decision{}, err
				}
				if decision.NewBlock {
					t.Fatal("local rate limit created an IP block", ip)
				}
			}
			return Decision{}, nil
		})
	}
	blocks, err := Blocks(database, now)
	if err != nil || len(blocks) != 0 {
		t.Fatal("local access appeared in IP restrictions", blocks, err)
	}
}

func TestMailAbuseUnblockReleasesFailedRegistrationAndPreservesPendingWork(t *testing.T) {
	database := registrationDatabase(t)
	now := time.Now().UTC()
	registrationTransaction(t, database, func(transaction *sql.Tx) error {
		for _, scenario := range []struct{ domain, attempt string }{{"failed.example", "failed"}, {"pending.example", "pending"}} {
			if _, _, err := BeginRegistration(transaction, "192.0.2.10", scenario.domain, scenario.attempt, now.Add(time.Hour), now); err != nil {
				return err
			}
			if err := TrackRegistrationRecipient(transaction, scenario.attempt, "owner@example.com"); err != nil {
				return err
			}
			if err := RegistrationDelivered(transaction, scenario.attempt, now); err != nil {
				return err
			}
		}
		if err := FailRegistration(transaction, "failed"); err != nil {
			return err
		}
		if decision, err := Reserve(transaction, "192.0.2.10", "owner@example.com", "recover", now); err != nil || !decision.Allowed {
			t.Fatal(decision, err)
		}
		if err := SetBlock(transaction, "192.0.2.10", RegistrationMailAbuse, now.Add(72*time.Hour)); err != nil {
			return err
		}
		if err := Unblock(transaction, "192.0.2.10"); err != nil {
			return err
		}
		blocked, err := Blocked(transaction, "192.0.2.10", now)
		if err != nil || blocked {
			t.Fatal("mail restriction survived explicit reversal", blocked, err)
		}
		_, allowed, err := BeginRegistration(transaction, "192.0.2.10", "failed.example", "retry", now.Add(time.Hour), now)
		if err != nil || !allowed {
			t.Fatal("abuse pause survived reversal", allowed, err)
		}
		pending, err := ReadRegistration(transaction, "192.0.2.10", "pending.example", now)
		if err != nil || pending.AttemptID != "pending" || !pending.Blocked(now) {
			t.Fatal("unblock discarded an active confirmation", pending, err)
		}
		decision, err := Reserve(transaction, "192.0.2.11", "owner@example.com", "recover", now.Add(time.Second))
		if err != nil || decision.Allowed {
			t.Fatal("unblock erased independent recipient quota", decision, err)
		}
		return nil
	})
}

func TestGuardStorageFailuresAndInvalidSourcesFailClosed(t *testing.T) {
	database := registrationDatabase(t)
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := Observe(transaction, "invalid", "owner@example.com", "register", now); err == nil {
		t.Fatal("invalid source admitted")
	}
	if _, err := Reserve(transaction, "invalid", "owner@example.com", "recover", now); err == nil {
		t.Fatal("invalid delivery identity admitted")
	}
	if SetBlock(transaction, "invalid", "manual", now.Add(time.Hour)) == nil {
		t.Fatal("invalid restriction accepted")
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := Observe(transaction, "192.0.2.1", "owner@example.com", "register", now); err == nil {
		t.Fatal("lost SQL session admitted request")
	}
	if _, err := Reserve(transaction, "192.0.2.1", "owner@example.com", "recover", now); err == nil {
		t.Fatal("lost SQL session reserved delivery")
	}
	if Unblock(transaction, "192.0.2.1") == nil {
		t.Fatal("lost SQL session acknowledged reversal")
	}
	if _, err := DetectRegistrationAbuse(transaction, Registration{IP: "192.0.2.1", Domain: "example.com", AttemptID: "attempt", Phase: "failed", Failures: 4}, now); err == nil {
		t.Fatal("lost SQL session acknowledged detection")
	}
	if detected, err := DetectRegistrationAbuse(transaction, Registration{IP: "127.0.0.1", Phase: "failed", Failures: 4}, now); err != nil || detected {
		t.Fatal("loopback entered persistent spam restrictions", detected, err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Blocks(database, now); err == nil {
		t.Fatal("unavailable restriction list accepted")
	}
}
