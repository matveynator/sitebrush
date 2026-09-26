package accountauth

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestAccountAuthSnapshotAndSessionStorageFailures(t *testing.T) {
	if _, err := passwordSnapshot("password", "not-a-snapshot"); err == nil {
		t.Fatal("malformed previous password snapshot was accepted")
	}
	if _, err := passwordSnapshot("password", strings.Repeat("0", 30)+":00"); err == nil {
		t.Fatal("short password key was accepted")
	}

	database := testDatabase(t)
	if _, err := database.Exec("DROP TABLE account_session_ips"); err != nil {
		t.Fatal(err)
	}
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Session(context.Background(), transaction, "example.org", "owner@example.org", "192.0.2.50", time.Unix(1_800_900_000, 0)); err == nil {
		_ = transaction.Rollback()
		t.Fatal("session continued after IP history storage failure")
	}
	_ = transaction.Rollback()

	database = testDatabase(t)
	if _, err := database.Exec("DROP TABLE account_session_ips"); err != nil {
		t.Fatal(err)
	}
	transaction, err = database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := pruneSessionIPCandidates(context.Background(), transaction, "example.org", "owner@example.org"); err == nil {
		_ = transaction.Rollback()
		t.Fatal("session IP pruning hid storage failure")
	}
	_ = transaction.Rollback()
}

func TestAccountAuthReserveAndChallengeFailClosedOnWriteErrors(t *testing.T) {
	now := time.Unix(1_800_900_100, 0).UTC()
	for _, testCase := range []struct {
		name string
		drop string
		call func(*sql.Tx) error
	}{
		{
			name: "rate cleanup",
			drop: "DROP TABLE account_code_rates",
			call: func(transaction *sql.Tx) error {
				allowed, err := Reserve(context.Background(), transaction, "example.org", "owner@example.org", "192.0.2.51", now)
				if err == nil || allowed {
					return errUnexpectedAuthResult("Reserve", allowed, err)
				}
				return nil
			},
		},
		{
			name: "challenge code insert",
			drop: "DROP TABLE account_login_codes",
			call: func(transaction *sql.Tx) error {
				outcome, err := Challenge(context.Background(), transaction, "example.org", "owner@example.org", "192.0.2.52", "/", "en", now)
				if err == nil || outcome.Status != "" {
					return errUnexpectedAuthOutcome(outcome.Status, err)
				}
				return nil
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			database := testDatabase(t)
			transaction, err := database.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := transaction.Exec(testCase.drop); err != nil {
				_ = transaction.Rollback()
				t.Fatal(err)
			}
			if err := testCase.call(transaction); err != nil {
				_ = transaction.Rollback()
				t.Fatal(err)
			}
			_ = transaction.Rollback()
		})
	}
}

func TestAccountAuthVerificationRejectsExpiredFutureAndWrongBinding(t *testing.T) {
	database := testDatabase(t)
	now := time.Unix(1_800_900_200, 0).UTC()
	challenge := transact(t, database, func(transaction *sql.Tx) (Outcome, error) {
		return Challenge(context.Background(), transaction, "example.org", "owner@example.org", "192.0.2.53", "/return", "en", now)
	})
	for _, testCase := range []struct {
		name   string
		when   time.Time
		domain string
		ip     string
	}{
		{name: "future", when: now.Add(-time.Second), domain: "example.org", ip: "192.0.2.53"},
		{name: "expired", when: now.Add(CodeTTL), domain: "example.org", ip: "192.0.2.53"},
		{name: "wrong domain", when: now.Add(time.Second), domain: "other.example", ip: "192.0.2.53"},
		{name: "wrong address", when: now.Add(time.Second), domain: "example.org", ip: "192.0.2.54"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			outcome := transact(t, database, func(transaction *sql.Tx) (Outcome, error) {
				return Verify(context.Background(), transaction, testCase.domain, challenge.Token, challenge.Code, testCase.ip, testCase.when)
			})
			if outcome.Status != "invalid" {
				t.Fatalf("verification result = %#v", outcome)
			}
		})
	}
}

func TestAccountAuthChallengeReusesRecentCodeAndLinkExpiry(t *testing.T) {
	database := testDatabase(t)
	now := time.Unix(1_800_900_300, 0).UTC()
	first := transact(t, database, func(transaction *sql.Tx) (Outcome, error) {
		return Challenge(context.Background(), transaction, "example.org", "owner@example.org", "192.0.2.55", "/first", "en", now)
	})
	second := transact(t, database, func(transaction *sql.Tx) (Outcome, error) {
		return Challenge(context.Background(), transaction, "example.org", "owner@example.org", "192.0.2.55", "/second", "ru", now.Add(time.Second))
	})
	if second.Status != "code_existing" || second.Token != first.Token || second.Path != "/first" || second.Language != "en" {
		t.Fatalf("recent challenge was not reused safely: %#v", second)
	}
	if _, err := database.Exec("UPDATE account_login_codes SET created_at=? WHERE token=?", now.Add(-CodeTTL).Unix(), first.Token); err != nil {
		t.Fatal(err)
	}
	link := transact(t, database, func(transaction *sql.Tx) (Outcome, error) {
		return VerifyLink(context.Background(), transaction, "example.org", first.Token, "192.0.2.55", now)
	})
	if link.Status != "invalid" {
		t.Fatalf("expired login link accepted: %#v", link)
	}
}

func TestAccountAuthReserveRejectsMalformedRateStorage(t *testing.T) {
	database := testDatabase(t)
	if _, err := database.Exec("DROP TABLE account_code_rates"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("CREATE TABLE account_code_rates(domain TEXT)"); err != nil {
		t.Fatal(err)
	}
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := Reserve(context.Background(), transaction, "example.org", "owner@example.org", "192.0.2.56", time.Now())
	if err == nil || allowed {
		_ = transaction.Rollback()
		t.Fatalf("malformed rate storage result = %t, %v", allowed, err)
	}
	_ = transaction.Rollback()

	database = testDatabase(t)
	if _, err := database.Exec("DROP TABLE account_code_rates"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("CREATE TABLE account_code_rates(domain TEXT,email TEXT,client_ip TEXT,window_start INTEGER,last_sent INTEGER,sent_count INTEGER,required TEXT NOT NULL,PRIMARY KEY(domain,email,client_ip))"); err != nil {
		t.Fatal(err)
	}
	transaction, err = database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	allowed, err = Reserve(context.Background(), transaction, "example.org", "owner@example.org", "192.0.2.57", time.Now())
	if err == nil || allowed {
		_ = transaction.Rollback()
		t.Fatalf("rate insert failure was hidden: %t, %v", allowed, err)
	}
	_ = transaction.Rollback()
}

func TestAccountAuthVerifyLinkRejectsFutureAndAttemptLimitedLinks(t *testing.T) {
	database := testDatabase(t)
	now := time.Unix(1_800_900_400, 0).UTC()
	challenge := transact(t, database, func(transaction *sql.Tx) (Outcome, error) {
		return Challenge(context.Background(), transaction, "example.org", "owner@example.org", "192.0.2.58", "/", "en", now)
	})
	if _, err := database.Exec("UPDATE account_login_codes SET created_at=? WHERE token=?", now.Add(time.Second).Unix(), challenge.Token); err != nil {
		t.Fatal(err)
	}
	future := transact(t, database, func(transaction *sql.Tx) (Outcome, error) {
		return VerifyLink(context.Background(), transaction, "example.org", challenge.Token, "192.0.2.58", now)
	})
	if future.Status != "invalid" {
		t.Fatalf("future login link accepted: %#v", future)
	}
	if _, err := database.Exec("UPDATE account_login_codes SET created_at=?,attempts=5 WHERE token=?", now.Unix(), challenge.Token); err != nil {
		t.Fatal(err)
	}
	limited := transact(t, database, func(transaction *sql.Tx) (Outcome, error) {
		return VerifyLink(context.Background(), transaction, "example.org", challenge.Token, "192.0.2.58", now)
	})
	if limited.Status != "invalid" {
		t.Fatalf("attempt-limited login link accepted: %#v", limited)
	}
}

func TestAccountAuthVerificationFailsClosedOnAtomicStorageErrors(t *testing.T) {
	now := time.Unix(1_800_900_500, 0).UTC()
	for _, testCase := range []struct {
		name    string
		trigger string
		call    func(*sql.DB, Outcome) (Outcome, error)
	}{
		{
			name:    "code attempt update",
			trigger: "CREATE TRIGGER reject_code_attempt BEFORE UPDATE ON account_login_codes BEGIN SELECT RAISE(ABORT, 'attempt storage unavailable'); END",
			call: func(database *sql.DB, challenge Outcome) (Outcome, error) {
				return transactForSecurityTest(database, func(transaction *sql.Tx) (Outcome, error) {
					return Verify(context.Background(), transaction, "example.org", challenge.Token, "wrong", "192.0.2.59", now.Add(time.Second))
				})
			},
		},
		{
			name:    "link consumption",
			trigger: "CREATE TRIGGER reject_code_delete BEFORE DELETE ON account_login_codes BEGIN SELECT RAISE(ABORT, 'consume storage unavailable'); END",
			call: func(database *sql.DB, challenge Outcome) (Outcome, error) {
				return transactForSecurityTest(database, func(transaction *sql.Tx) (Outcome, error) {
					return VerifyLink(context.Background(), transaction, "example.org", challenge.Token, "192.0.2.59", now.Add(time.Second))
				})
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			database := testDatabase(t)
			challenge := transact(t, database, func(transaction *sql.Tx) (Outcome, error) {
				return Challenge(context.Background(), transaction, "example.org", "owner@example.org", "192.0.2.59", "/", "en", now)
			})
			if _, err := database.Exec(testCase.trigger); err != nil {
				t.Fatal(err)
			}
			outcome, err := testCase.call(database, challenge)
			if err == nil || outcome.Status != "" {
				t.Fatalf("storage error was hidden: outcome=%#v err=%v", outcome, err)
			}
		})
	}
}

func TestAccountAuthChallengeFailsClosedWhenCodeInsertIsRejected(t *testing.T) {
	database := testDatabase(t)
	if _, err := database.Exec("CREATE TRIGGER reject_code_insert BEFORE INSERT ON account_login_codes BEGIN SELECT RAISE(ABORT, 'code storage unavailable'); END"); err != nil {
		t.Fatal(err)
	}
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := Challenge(context.Background(), transaction, "example.org", "owner@example.org", "192.0.2.60", "/", "en", time.Unix(1_800_900_600, 0).UTC())
	_ = transaction.Rollback()
	if err == nil {
		t.Fatalf("challenge continued after code storage failure: %#v, %v", outcome, err)
	}
}

func TestAccountAuthSessionAndRateOperationsFailClosedOnAtomicSQLErrors(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		setup string
		call  func(*sql.Tx) error
	}{
		{
			name:  "session history delete",
			setup: "CREATE TRIGGER reject_session_history_insert BEFORE INSERT ON account_session_ips BEGIN SELECT RAISE(ABORT, 'session history unavailable'); END",
			call: func(transaction *sql.Tx) error {
				_, err := Session(context.Background(), transaction, "example.org", "owner@example.org", "192.0.2.61", time.Now())
				return err
			},
		},
		{
			name:  "rate cleanup",
			setup: "CREATE TRIGGER reject_rate_insert BEFORE INSERT ON account_code_rates BEGIN SELECT RAISE(ABORT, 'rate storage unavailable'); END",
			call: func(transaction *sql.Tx) error {
				_, err := Reserve(context.Background(), transaction, "example.org", "owner@example.org", "192.0.2.62", time.Now())
				return err
			},
		},
		{
			name:  "trusted address cleanup",
			setup: "CREATE TRIGGER reject_trusted_insert BEFORE INSERT ON account_trusted_ips BEGIN SELECT RAISE(ABORT, 'trusted storage unavailable'); END",
			call: func(transaction *sql.Tx) error {
				return RememberAddress(context.Background(), transaction, "example.org", "owner@example.org", "192.0.2.63", time.Now())
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			database := testDatabase(t)
			if _, err := database.Exec(testCase.setup); err != nil {
				t.Fatal(err)
			}
			transaction, err := database.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if err := testCase.call(transaction); err == nil {
				_ = transaction.Rollback()
				t.Fatal("SQL failure was hidden")
			}
			_ = transaction.Rollback()
		})
	}
}

func TestAccountAuthDeepStorageErrorBranches(t *testing.T) {
	now := time.Unix(1_800_900_700, 0).UTC()

	t.Run("rate replacement delete", func(t *testing.T) {
		database := testDatabase(t)
		if _, err := database.Exec("INSERT INTO account_code_rates(domain,email,client_ip,window_start,last_sent,sent_count) VALUES(?,?,?,?,?,?)", "example.org", "owner@example.org", "192.0.2.64", now.Unix(), now.Add(-CodeSendCooldown-time.Second).Unix(), 1); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec("CREATE TRIGGER reject_rate_replacement BEFORE DELETE ON account_code_rates BEGIN SELECT RAISE(ABORT, 'rate replacement unavailable'); END"); err != nil {
			t.Fatal(err)
		}
		transaction, _ := database.Begin()
		allowed, err := Reserve(context.Background(), transaction, "example.org", "owner@example.org", "192.0.2.64", now)
		_ = transaction.Rollback()
		if err == nil || allowed {
			t.Fatalf("rate replacement failure was hidden: %t, %v", allowed, err)
		}
	})

	t.Run("limited challenge lookup failure", func(t *testing.T) {
		database := testDatabase(t)
		if _, err := database.Exec("INSERT INTO account_code_rates(domain,email,client_ip,window_start,last_sent,sent_count) VALUES(?,?,?,?,?,?)", "example.org", "owner@example.org", "192.0.2.65", now.Unix(), now.Unix(), 1); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec("DROP TABLE account_login_codes"); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec("CREATE TABLE account_login_codes(token TEXT)"); err != nil {
			t.Fatal(err)
		}
		transaction, _ := database.Begin()
		outcome, err := Challenge(context.Background(), transaction, "example.org", "owner@example.org", "192.0.2.65", "/", "en", now.Add(time.Second))
		_ = transaction.Rollback()
		if err == nil || outcome.Status != "" {
			t.Fatalf("limited challenge storage failure was hidden: %#v, %v", outcome, err)
		}
	})

	t.Run("password trusted address failure", func(t *testing.T) {
		database := testDatabase(t)
		if _, err := database.Exec("CREATE TRIGGER reject_trusted_update BEFORE UPDATE ON account_trusted_ips BEGIN SELECT RAISE(ABORT, 'trusted update unavailable'); END"); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec("INSERT INTO account_trusted_ips(domain,email,client_ip,confirmed_at,last_login) VALUES(?,?,?,?,?)", "example.org", "owner@example.org", "192.0.2.66", now.Unix(), now.Unix()); err != nil {
			t.Fatal(err)
		}
		transaction, _ := database.Begin()
		outcome, err := Password(context.Background(), transaction, "example.org", "owner@example.org", "password", "192.0.2.66", "/", "en", now, true)
		_ = transaction.Rollback()
		if err == nil || outcome.Status != "" {
			t.Fatalf("password storage failure was hidden: %#v, %v", outcome, err)
		}
	})
}

func transactForSecurityTest(database *sql.DB, call func(*sql.Tx) (Outcome, error)) (Outcome, error) {
	transaction, err := database.Begin()
	if err != nil {
		return Outcome{}, err
	}
	outcome, err := call(transaction)
	if err != nil {
		_ = transaction.Rollback()
		return outcome, err
	}
	if err := transaction.Commit(); err != nil {
		return outcome, err
	}
	return outcome, nil
}

type authTestError string

func (err authTestError) Error() string { return string(err) }

func errUnexpectedAuthResult(operation string, allowed bool, err error) error {
	return authTestError(operation + " unexpectedly succeeded")
}

func errUnexpectedAuthOutcome(status string, err error) error {
	return authTestError("unexpected authentication outcome: " + status)
}
