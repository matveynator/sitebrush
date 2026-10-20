package mailguard

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func guardDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "guard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	for _, query := range SchemaQueries() {
		if _, err := database.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func guardTransaction(t *testing.T, database *sql.DB, run func(*sql.Tx) (Decision, error)) Decision {
	t.Helper()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	decision, err := run(transaction)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	return decision
}

func TestDifferentRecipientsBlockAcrossMailPurposes(t *testing.T) {
	database := guardDatabase(t)
	now := time.Now()
	for index := 0; index < 4; index++ {
		decision := guardTransaction(t, database, func(transaction *sql.Tx) (Decision, error) {
			return Observe(transaction, "192.0.2.10", fmt.Sprintf("owner%d@example.com", index), []string{"register", "recover"}[index%2], now.Add(time.Duration(index)*2*time.Minute))
		})
		if decision.Allowed != (index < 3) || decision.NewBlock != (index == 3) {
			t.Fatalf("request %d: %+v", index, decision)
		}
	}
	decision := guardTransaction(t, database, func(transaction *sql.Tx) (Decision, error) {
		return Observe(transaction, "192.0.2.10", "other@example.com", "recover", now.Add(10*time.Minute))
	})
	if decision.Allowed || decision.NewBlock {
		t.Fatal("persisted block was bypassed or repeated", decision)
	}
	blocks, err := Blocks(database, now.Add(10*time.Minute))
	if err != nil || len(blocks) != 1 || blocks[0].IP != "192.0.2.10" {
		t.Fatal("block not visible to administrator", blocks, err)
	}
	guardTransaction(t, database, func(transaction *sql.Tx) (Decision, error) { return Decision{}, Unblock(transaction, "192.0.2.10") })
	decision = guardTransaction(t, database, func(transaction *sql.Tx) (Decision, error) {
		return Observe(transaction, "192.0.2.10", "other@example.com", "recover", now.Add(11*time.Minute))
	})
	if !decision.Allowed {
		t.Fatal("manual unblock retained stale source counters")
	}
}

func TestRecipientQuotaSurvivesIPRotationAndExcludesUnsolvedRequests(t *testing.T) {
	database := guardDatabase(t)
	now := time.Now()
	for index := 0; index < 5; index++ {
		guardTransaction(t, database, func(transaction *sql.Tx) (Decision, error) {
			return Observe(transaction, fmt.Sprintf("192.0.2.%d", index+1), "victim@example.com", "recover", now)
		})
	}
	for index := 0; index < 4; index++ {
		decision := guardTransaction(t, database, func(transaction *sql.Tx) (Decision, error) {
			return Reserve(transaction, fmt.Sprintf("192.0.2.%d", index+1), "Victim@example.com", "register", now.Add(time.Duration(index)*2*time.Minute))
		})
		if decision.Allowed != (index < 3) {
			t.Fatalf("recipient budget at %d: %+v", index, decision)
		}
	}
}

func TestSharedIPv6PrefixCannotBypassNetworkBudget(t *testing.T) {
	database := guardDatabase(t)
	now := time.Now()
	for index := 0; index < 101; index++ {
		decision := guardTransaction(t, database, func(transaction *sql.Tx) (Decision, error) {
			return Observe(transaction, fmt.Sprintf("2001:db8::%x", index+1), "victim@example.com", "recover", now)
		})
		if decision.Allowed != (index < 100) {
			t.Fatalf("network budget at %d: %+v", index, decision)
		}
	}
}
