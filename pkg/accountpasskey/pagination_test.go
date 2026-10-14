package accountpasskey

import (
	"context"
	"fmt"
	"testing"
)

func TestPasskeyPaginationRetainsStableOrderAndAccountIsolation(t *testing.T) {
	database := passkeyCoverageDatabase(t)
	ctx := context.Background()
	for index := 0; index < 7; index++ {
		if _, err := database.Exec(`INSERT INTO account_passkeys(domain,email,credential_id,created_at,last_used_at) VALUES(?,?,?,?,?)`, "example.com", "owner@example.com", fmt.Sprintf("key-%d", index), 100, index); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec(`INSERT INTO account_passkeys(domain,email,credential_id,created_at,last_used_at) VALUES(?,?,?,?,?)`, "example.com", "other@example.com", "foreign-key", 200, 0); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct{ offset, expected int }{{0, 5}, {5, 2}, {999, 0}} {
		credentials, err := ListPage(ctx, database, "example.com", "owner@example.com", scenario.offset, 5)
		if err != nil || len(credentials) != scenario.expected {
			t.Fatalf("offset=%d count=%d err=%v", scenario.offset, len(credentials), err)
		}
		for index, credential := range credentials {
			if credential.ID != fmt.Sprintf("key-%d", scenario.offset+index) {
				t.Fatal("page order is unstable or contains a foreign credential")
			}
		}
	}
}
