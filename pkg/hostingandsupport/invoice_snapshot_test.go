package hostingandsupport

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestInvoiceSnapshotIncludesOldFinancialHistoryOnSingleConnection(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "invoices.db"))
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })
	boundary := context.Background()
	if err := Migrate(boundary, database); err != nil {
		t.Fatal(err)
	}
	transaction, err := database.BeginTx(boundary, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	now := time.Now().UTC()
	for index := 1; index <= 225; index++ {
		installation, status, dueAt, paidAt := "other", "cancelled", "", ""
		if index == 1 {
			installation, status, paidAt = "local:host.example", "paid", now.Format(time.RFC3339)
		}
		if index == 2 {
			installation, status, dueAt = "local:host.example", "issued", now.AddDate(0, 0, -1).Format("2006-01-02")
		}
		if _, err := transaction.ExecContext(boundary, `INSERT INTO billing_invoices(id,invoice_number,installation_id,domain,customer_email,amount_minor,currency,status,due_at,paid_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, index, fmt.Sprintf("INV-%03d", index), installation, "reused.example", "client@example.com", index*100, "EUR", status, dueAt, paidAt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := transaction.ExecContext(boundary, `INSERT INTO billing_invoice_lines(invoice_id,cost_share_minor) VALUES(1,50)`); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	store := Store{DB: database}
	invoices, err := store.InvoiceSnapshot(boundary)
	if err != nil || len(invoices) != 225 {
		t.Fatalf("snapshot count=%d error=%v", len(invoices), err)
	}
	oldest := invoices[len(invoices)-1]
	if oldest.ID != 1 || len(oldest.Lines) != 1 || oldest.Lines[0].CostShareMinor != 50 {
		t.Fatalf("old invoice lines missing: %+v", oldest)
	}
	server := BuildLocalServerView(LocalServerViewInput{MainDomain: "host.example", Invoices: invoices})
	if len(server.Invoices) != 2 || server.BillingPaidLabel != MoneyLabel(100, "EUR") || server.BillingOverdueLabel != MoneyLabel(200, "EUR") {
		t.Fatalf("old history excluded from local totals: %+v", server)
	}
	if _, err := database.Exec(`DROP TABLE billing_invoice_lines`); err != nil {
		t.Fatal(err)
	}
	if partial, err := store.InvoiceSnapshot(boundary); err == nil || partial != nil {
		t.Fatal("failed line read returned a successful partial snapshot")
	}
}

func TestLocalInvoiceOwnershipRejectsReusableDomainsAndForeignLocalAliases(t *testing.T) {
	server := BuildLocalServerView(LocalServerViewInput{
		InstallationID: "installation-a", MainDomain: "host.example",
		Sites: []Site{{Domain: "shared.example"}},
		Invoices: []Invoice{
			{Number: "FOREIGN", InstallationID: "installation-b", Domain: "shared.example"},
			{Number: "UNASSIGNED", Domain: "shared.example"},
			{Number: "FOREIGN-LOCAL", InstallationID: "local:other.example", Domain: "shared.example"},
			{Number: "OWN", InstallationID: "installation-a", Domain: "deleted.example"},
			{Number: "LOCAL-ALIAS", InstallationID: "local:host.example", Domain: "shared.example"},
		},
	})
	if len(server.Invoices) != 2 || server.Invoices[0].Number != "OWN" || server.Invoices[1].Number != "LOCAL-ALIAS" {
		t.Fatalf("invoice ownership mismatch: %+v", server.Invoices)
	}
	if server.Sites[0].InvoiceLabel != "LOCAL-ALIAS · неизвестно" {
		t.Fatalf("foreign invoice badge: %s", server.Sites[0].InvoiceLabel)
	}
}

func TestInvoiceOverdueUsesValidUnpaidDates(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		status, due string
		want        bool
	}{
		{"issued", "2026-11-21", true}, {"payment_error", "2026-11-21T12:00:00Z", true},
		{"paid", "2026-11-21", false}, {"cancelled", "2026-11-21", false},
		{"issued", "2026-11-22", false}, {"issued", "0000-00-00", false}, {"issued", "", false},
	} {
		if got := InvoiceIsOverdue(test.status, test.due, now); got != test.want {
			t.Errorf("%s %s overdue=%v", test.status, test.due, got)
		}
	}
}
