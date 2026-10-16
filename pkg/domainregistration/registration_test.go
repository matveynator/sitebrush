package domainregistration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"path/filepath"
	"testing"
	"time"
)

func testDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "claims.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	for _, query := range []string{Schema, `CREATE TABLE registration_confirmations(token TEXT PRIMARY KEY,domain TEXT,expires_at TEXT)`, `CREATE TABLE public_trial_sites(domain TEXT,status TEXT,delete_after TEXT)`} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestConcurrentClaimsHaveOneOwner(t *testing.T) {
	db := testDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results := make(chan error, 32)
	for i := 0; i < 32; i++ {
		go func(i int) {
			_, err := Claim(ctx, db, "ero.sitebrush.ru", fmt.Sprint(i), "owner@example.org", "registration")
			results <- err
		}(i)
	}
	won := 0
	for i := 0; i < 32; i++ {
		err := <-results
		if err == nil {
			won++
		} else if !errors.Is(err, ErrTaken) {
			t.Fatal(err)
		}
	}
	if won != 1 {
		t.Fatalf("owners = %d", won)
	}
	var token string
	if err := db.QueryRow(`SELECT token FROM domain_registration_claims`).Scan(&token); err != nil {
		t.Fatal(err)
	}
	if created, err := Claim(ctx, db, "ero.sitebrush.ru", token, "owner@example.org", "registration"); err != nil || created {
		t.Fatalf("idempotent claim: %v %v", created, err)
	}
	if _, err := Claim(ctx, db, "ero.sitebrush.ru", "trial", "", "trial"); !errors.Is(err, ErrTaken) {
		t.Fatalf("trial overwrote owner: %v", err)
	}
}

func TestPendingEmailAndFinishedTrialReservations(t *testing.T) {
	db := testDatabase(t)
	ctx := context.Background()
	db.Exec(`INSERT INTO registration_confirmations VALUES('mail','ero.sitebrush.ru',?)`, time.Now().Add(time.Minute).UTC().Format(time.RFC3339))
	if _, err := Claim(ctx, db, "ero.sitebrush.ru", "trial", "", "trial"); !errors.Is(err, ErrTaken) {
		t.Fatalf("pending email not reserved: %v", err)
	}
	if _, err := Claim(ctx, db, "ero.sitebrush.ru", "mail", "owner@example.org", "registration"); err != nil {
		t.Fatal(err)
	}
	var expiry string
	db.QueryRow(`SELECT expires_at FROM registration_confirmations WHERE token='mail'`).Scan(&expiry)
	until, err := time.Parse(time.RFC3339, expiry)
	if err != nil || time.Until(until) < 23*time.Hour {
		t.Fatalf("confirmation not extended: %s %v", expiry, err)
	}
	if _, err := Claim(ctx, db, "other.sitebrush.ru", "trial", "", "trial"); err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(ctx, db, "other.sitebrush.ru", "mail", "owner@example.org", "registration"); !errors.Is(err, ErrTaken) {
		t.Fatalf("in-progress import hijacked: %v", err)
	}
	db.Exec(`INSERT INTO public_trial_sites VALUES('other.sitebrush.ru','active',?)`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	if _, err := Claim(ctx, db, "other.sitebrush.ru", "mail", "owner@example.org", "registration"); err != nil {
		t.Fatalf("finished trial cannot register: %v", err)
	}
}

func TestClaimCancellationAndSchemaFailure(t *testing.T) {
	db := testDatabase(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Claim(ctx, db, "site.example", "token", "owner@example.org", "registration"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	db.Exec(`DROP TABLE registration_confirmations`)
	if _, err := Claim(context.Background(), db, "site.example", "token", "owner@example.org", "registration"); err == nil {
		t.Fatal("claim committed without extending confirmation")
	}
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM domain_registration_claims`).Scan(&count)
	if count != 0 {
		t.Fatal("failed claim was not rolled back")
	}
}

func TestDamagedControlSchemaNeverMakesDomainAvailable(t *testing.T) {
	t.Run("missing claims", func(t *testing.T) {
		db := testDatabase(t)
		db.Exec(`DROP TABLE domain_registration_claims`)
		if _, err := Claim(context.Background(), db, "site.example", "token", "owner@example.org", "registration"); err == nil {
			t.Fatal("missing ownership registry accepted a claim")
		}
	})
	t.Run("missing trial registry", func(t *testing.T) {
		db := testDatabase(t)
		ctx := context.Background()
		if _, err := Claim(ctx, db, "site.example", "trial", "", "trial"); err != nil {
			t.Fatal(err)
		}
		db.Exec(`DROP TABLE public_trial_sites`)
		if _, err := Claim(ctx, db, "site.example", "token", "owner@example.org", "registration"); err == nil {
			t.Fatal("unreadable trial registry allowed adoption")
		}
		var kind string
		db.QueryRow(`SELECT kind FROM domain_registration_claims`).Scan(&kind)
		if kind != "trial" {
			t.Fatal("failed adoption changed ownership")
		}
	})
	t.Run("missing pending emails", func(t *testing.T) {
		db := testDatabase(t)
		db.Exec(`DROP TABLE registration_confirmations`)
		if _, err := Claim(context.Background(), db, "site.example", "trial", "", "trial"); err == nil {
			t.Fatal("missing pending-mail registry accepted an import")
		}
	})
}
