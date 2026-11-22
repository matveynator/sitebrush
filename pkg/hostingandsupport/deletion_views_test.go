package hostingandsupport

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestPopulateSiteDeletionViewsBatchesAndPreservesOwnership(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil { t.Fatal(err) }
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		"CREATE TABLE site_deletion_queue (domain TEXT,reason TEXT,delete_after TEXT,status TEXT)",
		"CREATE TABLE site_account_deletion_queue (domain TEXT,email TEXT,delete_after TEXT)",
		"INSERT INTO site_deletion_queue VALUES ('site.example','requested','2026-11-25','pending')",
		"INSERT INTO site_deletion_queue VALUES ('other.example','old','2026-11-01','completed')",
		"INSERT INTO site_account_deletion_queue VALUES ('site.example','z@example.com','2026-12-01')",
		"INSERT INTO site_account_deletion_queue VALUES ('site.example','a@example.com','2026-11-26')",
		"INSERT INTO site_account_deletion_queue VALUES ('unknown.example','missing@example.com','2026-11-26')",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil { t.Fatal(err) }
	}
	sites := []Site{
		{Domain: "site.example", AdminAccounts: []SiteAccountView{{Email: "owner@example.com", Protected: true}}},
		{Domain: "other.example"},
	}
	if err := PopulateSiteDeletionViews(ctx, db, sites); err != nil { t.Fatal(err) }
	if sites[0].DeletionReason != "requested" || sites[0].DeletionDeadline != "2026-11-25" {
		t.Fatalf("pending site state = %#v", sites[0])
	}
	accounts := sites[0].AdminAccounts
	if len(accounts) != 3 || accounts[0].Email != "owner@example.com" || !accounts[0].Protected ||
		accounts[1].Email != "a@example.com" || accounts[2].Email != "z@example.com" {
		t.Fatalf("account state = %#v", accounts)
	}
	if sites[1].DeletionReason != "" || len(sites[1].AdminAccounts) != 0 {
		t.Fatalf("unrelated site state = %#v", sites[1])
	}
}
