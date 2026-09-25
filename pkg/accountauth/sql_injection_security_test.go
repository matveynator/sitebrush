package accountauth

import (
	"context"
	"testing"
	"time"
)

func TestSecurityBoundaryAuthenticationQueriesRejectSQLInjectionIdentity(t *testing.T) {
	database := testDatabase(t)
	ctx := context.Background()

	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ok, err := Credentials(ctx, tx, "example.org", "owner@example.org' OR 1=1 --", "password")
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if ok {
		_ = tx.Rollback()
		t.Fatal("SECURITY: injected account identity authenticated")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestSecurityBoundaryRevokeParametersCannotDeleteOtherAccounts(t *testing.T) {
	database := testDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_400_000, 0).UTC()

	if _, err := database.Exec(
		"INSERT INTO account_trusted_ips(domain,email,client_ip,first_login,last_login) VALUES(?,?,?,?,?)",
		"example.org", "owner@example.org", "203.0.113.7", now.Unix(), now.Unix(),
	); err != nil {
		t.Fatal(err)
	}

	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := Revoke(ctx, tx, "example.org", "owner@example.org' OR 1=1 --", "203.0.113.7"); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var remaining int
	if err := database.QueryRow(
		"SELECT COUNT(*) FROM account_trusted_ips WHERE domain=? AND email=? AND client_ip=?",
		"example.org", "owner@example.org", "203.0.113.7",
	).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("SECURITY: injected revoke affected another account, remaining=%d", remaining)
	}
}
