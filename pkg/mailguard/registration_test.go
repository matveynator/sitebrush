package mailguard

import (
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func registrationDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database := guardDatabase(t)
	for _, query := range append(RegistrationSchemaQueries(), RegistrationAbuseSchemaQueries()...) {
		if _, err := database.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func registrationTransaction(t *testing.T, database *sql.DB, run func(*sql.Tx) error) {
	t.Helper()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	if err := run(transaction); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrationProgressionAndIdempotentExpiry(t *testing.T) {
	database := registrationDatabase(t)
	now := time.Unix(1800000000, 0).UTC()
	for index, delay := range []time.Duration{time.Hour, 6 * time.Hour, 24 * time.Hour, 72 * time.Hour, 72 * time.Hour} {
		attemptID := fmt.Sprintf("attempt-%d", index)
		registrationTransaction(t, database, func(transaction *sql.Tx) error {
			registration, allowed, err := BeginRegistration(transaction, "192.0.2.10", "Example.COM.", attemptID, now.Add(15*time.Minute), now)
			if err != nil {
				return err
			}
			if !allowed || !registration.RetryAt.Equal(now.Add(delay)) {
				t.Fatalf("attempt %d: %+v allowed=%v", index, registration, allowed)
			}
			if err := QueueRegistration(transaction, attemptID, "message-"+attemptID); err != nil {
				return err
			}
			return RegistrationDelivered(transaction, attemptID, now)
		})
		registrationTransaction(t, database, func(transaction *sql.Tx) error {
			blocked, allowed, err := BeginRegistration(transaction, "192.0.2.10", "example.com", "another-email", now.Add(time.Hour), now.Add(time.Minute))
			if allowed || blocked.AttemptID != attemptID || blocked.Failures != min(4, index) {
				t.Fatal("cooldown bypass or repeated penalty", blocked, allowed)
			}
			return err
		})
		for poll := 0; poll < 2; poll++ {
			registrationTransaction(t, database, func(transaction *sql.Tx) error {
				registration, err := ReadRegistration(transaction, "192.0.2.10", "example.com", now.Add(16*time.Minute))
				if registration.Failures != min(4, index+1) || registration.Phase != "failed" {
					t.Fatal("expiry counted incorrectly", registration)
				}
				return err
			})
		}
		now = now.Add(delay)
	}
}

func TestRegistrationTechnicalFailurePreservesPreviousLevel(t *testing.T) {
	database := registrationDatabase(t)
	now := time.Unix(1800000000, 0).UTC()
	registrationTransaction(t, database, func(transaction *sql.Tx) error {
		if _, _, err := BeginRegistration(transaction, "192.0.2.10", "example.com", "first", now.Add(15*time.Minute), now); err != nil {
			return err
		}
		if err := RegistrationDelivered(transaction, "first", now); err != nil {
			return err
		}
		return FailRegistration(transaction, "first")
	})
	now = now.Add(time.Hour)
	for _, outcome := range []string{"SMTP", "NetChan", "HTTPS", "database"} {
		registrationTransaction(t, database, func(transaction *sql.Tx) error {
			registration, allowed, err := BeginRegistration(transaction, "192.0.2.10", "example.com", outcome, now.Add(15*time.Minute), now)
			if err != nil {
				return err
			}
			if !allowed || registration.Failures != 1 || !registration.RetryAt.Equal(now.Add(6*time.Hour)) {
				t.Fatal("infrastructure escalated failures", registration)
			}
			// Unaccepted delivery cannot become a user failure, even if a code is rejected.
			if err := FailRegistration(transaction, outcome); err != nil {
				return err
			}
			return AbortRegistration(transaction, outcome)
		})
	}
}

func TestRegistrationIsolationResetAndStaleOutcomes(t *testing.T) {
	database := registrationDatabase(t)
	now := time.Unix(1800000000, 0).UTC()
	registrationTransaction(t, database, func(transaction *sql.Tx) error {
		for index, pair := range [][2]string{{"192.0.2.10", "example.com"}, {"192.0.2.11", "example.com"}, {"192.0.2.10", "other.example"}} {
			_, allowed, err := BeginRegistration(transaction, pair[0], pair[1], fmt.Sprint(index), now.Add(15*time.Minute), now)
			if err != nil {
				return err
			}
			if !allowed {
				t.Fatal("unrelated IP or domain blocked", pair)
			}
		}
		// Completion uses the originating attempt, regardless of confirmation IP.
		if err := CompleteRegistration(transaction, "0"); err != nil {
			return err
		}
		registration, allowed, err := BeginRegistration(transaction, "192.0.2.10", "example.com", "new", now.Add(15*time.Minute), now)
		if err != nil {
			return err
		}
		if !allowed || registration.Failures != 0 {
			t.Fatal("successful registration did not reset", registration)
		}
		if err := CompleteRegistration(transaction, "0"); err != nil {
			return err
		}
		if err := AbortRegistration(transaction, "0"); err != nil {
			return err
		}
		registration, err = ReadRegistration(transaction, "192.0.2.10", "example.com", now)
		if registration.AttemptID != "new" || !registration.Blocked(now) {
			t.Fatal("stale outcome cleared a new attempt", registration)
		}
		return err
	})
}

func TestRegistrationUncertainDeliveryNeverBecomesUserFailure(t *testing.T) {
	database := registrationDatabase(t)
	now := time.Unix(1800000000, 0).UTC()
	registrationTransaction(t, database, func(transaction *sql.Tx) error {
		if _, _, err := BeginRegistration(transaction, "192.0.2.10", "example.com", "queued", now.Add(15*time.Minute), now); err != nil {
			return err
		}
		return QueueRegistration(transaction, "queued", "durable-message")
	})
	registrationTransaction(t, database, func(transaction *sql.Tx) error {
		registration, err := ReadRegistration(transaction, "192.0.2.10", "example.com", now.Add(2*time.Hour))
		if err != nil {
			return err
		}
		if registration.Failures != 0 || !registration.Blocked(now.Add(2*time.Hour)) {
			t.Fatal("in-flight delivery counted as user failure", registration)
		}
		return RegistrationDelivered(transaction, "queued", now.Add(2*time.Hour))
	})
	registrationTransaction(t, database, func(transaction *sql.Tx) error {
		registration, allowed, err := BeginRegistration(transaction, "192.0.2.10", "example.com", "preparing", now.Add(3*time.Hour), now.Add(2*time.Hour))
		if err != nil {
			return err
		}
		if !allowed || registration.Failures != 0 {
			t.Fatal("expired delivery penalized user", registration)
		}
		return nil
	})
	registrationTransaction(t, database, func(transaction *sql.Tx) error {
		registration, err := ReadRegistration(transaction, "192.0.2.10", "example.com", now.Add(4*time.Hour))
		if registration.Failures != 0 || registration.Blocked(now.Add(4*time.Hour)) {
			t.Fatal("abandoned preparation was punished", registration)
		}
		return err
	})
}
