package accountauth

import (
	"context"
	"testing"
	"time"
)

func TestSessionIdleExpiryAndCreationTimestamp(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	for _, scenario := range []struct {
		name     string
		lastUsed int64
		active   bool
	}{
		{"new", now.Unix(), true},
		{"fourteen days", now.Add(-14 * 24 * time.Hour).Unix(), true},
		{"fifteen days", now.Add(-SessionTTL).Unix(), false},
		{"expired", now.Add(-SessionTTL - time.Second).Unix(), false},
		{"future", now.Add(time.Second).Unix(), false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if SessionActive(scenario.lastUsed, now) != scenario.active {
				t.Fatal("incorrect session lifetime")
			}
		})
	}
	database := testDatabase(t)
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	token, err := Session(context.Background(), transaction, "example.org", "owner@example.org", "192.0.2.1", now)
	if err != nil {
		transaction.Rollback()
		t.Fatal(err)
	}
	if err = transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	var lastUsed int64
	if err = database.QueryRow(`SELECT last_used_at FROM sessions WHERE token=?`, token).Scan(&lastUsed); err != nil || lastUsed != now.Unix() {
		t.Fatalf("session activity timestamp = %d: %v", lastUsed, err)
	}
}
