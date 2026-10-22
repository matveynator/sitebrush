package mailguard

import (
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func TestRegistrationMailAbuseRequiresFourDistinctFailedRecipients(t *testing.T) {
	for _, distinct := range []bool{false, true} {
		t.Run(fmt.Sprint(distinct), func(t *testing.T) {
			database := registrationDatabase(t)
			now := time.Unix(1800000000, 0).UTC()
			for index := 0; index < 4; index++ {
				attemptID := fmt.Sprint(index)
				registrationTransaction(t, database, func(transaction *sql.Tx) error {
					registration, allowed, err := BeginRegistration(transaction, "192.0.2.10", "example.com", attemptID, now.Add(15*time.Minute), now)
					if err != nil {
						return err
					}
					if !allowed {
						t.Fatal("allowed retry was blocked", registration)
					}
					recipient := "same@example.com"
					if distinct {
						recipient = "recipient" + attemptID + "@example.com"
					}
					if err := TrackRegistrationRecipient(transaction, attemptID, recipient); err != nil {
						return err
					}
					if err := RegistrationDelivered(transaction, attemptID, now); err != nil {
						return err
					}
					if err := FailRegistration(transaction, attemptID); err != nil {
						return err
					}
					registration, err = ReadRegistration(transaction, "192.0.2.10", "example.com", now)
					if err != nil {
						return err
					}
					if registration.AbuseDetected != (distinct && index == 3) {
						t.Fatal("wrong registration abuse classification", registration)
					}
					blocked, err := Blocked(transaction, "192.0.2.10", now)
					if err != nil {
						return err
					}
					if blocked != registration.AbuseDetected {
						t.Fatal("wrong source blocking decision", blocked, registration)
					}
					previousDeadline := registration.RetryAt
					repeated, err := ReadRegistration(transaction, "192.0.2.10", "example.com", now.Add(time.Minute))
					if repeated.AbuseDetected || !repeated.RetryAt.Equal(previousDeadline) {
						t.Fatal("refresh repeated incident or extended punishment", repeated)
					}
					now = registration.RetryAt
					return err
				})
			}
		})
	}
}

func TestRegistrationTechnicalFailureDoesNotCountRecipientAsAbuse(t *testing.T) {
	database := registrationDatabase(t)
	now := time.Unix(1800000000, 0).UTC()
	registrationTransaction(t, database, func(transaction *sql.Tx) error {
		for index := 0; index < 4; index++ {
			attemptID := fmt.Sprint(index)
			if _, _, err := BeginRegistration(transaction, "192.0.2.10", "example.com", attemptID, now.Add(15*time.Minute), now); err != nil {
				return err
			}
			if err := TrackRegistrationRecipient(transaction, attemptID, "recipient"+attemptID+"@example.com"); err != nil {
				return err
			}
			if err := AbortRegistration(transaction, attemptID); err != nil {
				return err
			}
		}
		var evidence string
		if err := transaction.QueryRow(`SELECT failed_recipients FROM registration_mail_abuse WHERE client_ip=? AND domain=?`, "192.0.2.10", "example.com").Scan(&evidence); err != nil {
			return err
		}
		if evidence != "[]" {
			t.Fatal("infrastructure errors recorded as spam", evidence)
		}
		if err := CompleteRegistration(transaction, "3"); err != nil {
			return err
		}
		var count int
		if err := transaction.QueryRow(`SELECT COUNT(*) FROM registration_mail_abuse`).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Fatal("success retained spam evidence")
		}
		return nil
	})
}

func TestRegistrationSweepDetectsSpamWithoutAnotherRequest(t *testing.T) {
	database := registrationDatabase(t)
	now := time.Unix(1800000000, 0).UTC()
	registrationTransaction(t, database, func(transaction *sql.Tx) error {
		for index := 0; index < 4; index++ {
			attemptID := fmt.Sprint(index)
			registration, allowed, err := BeginRegistration(transaction, "192.0.2.10", "example.com", attemptID, now.Add(15*time.Minute), now)
			if err != nil {
				return err
			}
			if !allowed {
				t.Fatal("retry unexpectedly blocked")
			}
			if err := TrackRegistrationRecipient(transaction, attemptID, "recipient"+attemptID+"@example.com"); err != nil {
				return err
			}
			if err := RegistrationDelivered(transaction, attemptID, now); err != nil {
				return err
			}
			if index < 3 {
				if err := FailRegistration(transaction, attemptID); err != nil {
					return err
				}
				now = registration.RetryAt
			}
		}
		detected, err := SweepRegistrations(transaction, now.Add(16*time.Minute))
		if err != nil {
			return err
		}
		if len(detected) != 1 || detected[0].IP != "192.0.2.10" {
			t.Fatal("background sweep missed spam", detected)
		}
		detected, err = SweepRegistrations(transaction, now.Add(17*time.Minute))
		if len(detected) != 0 {
			t.Fatal("background sweep duplicated spam incident", detected)
		}
		return err
	})
}
