package hostingandsupport

import (
	"testing"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/expenses"
)

func TestServerInvoiceViewsDoNotMixInstallations(t *testing.T) {
	invoices := []Invoice{
		{Number: "MATCH", InstallationID: "server-a", CustomerEmail: "shared@example.com", Domain: "a.example", Amount: "10.00", Currency: "EUR", Status: "issued"},
		{Number: "WRONG-SERVER", InstallationID: "server-b", CustomerEmail: "shared@example.com", Domain: "a.example", Amount: "20.00", Currency: "EUR", Status: "issued"},
		{Number: "LEGACY-MATCH", CustomerEmail: "shared@example.com", Domain: "a.example", Amount: "30.00", Currency: "EUR", Status: "paid"},
		{Number: "LEGACY-WRONG", CustomerEmail: "shared@example.com", Domain: "other.example", Amount: "40.00", Currency: "EUR", Status: "paid"},
	}
	domains := map[string]struct{}{"a.example": {}}
	views := ServerInvoiceViewsForInstallation(invoices, "server-a", domains)
	if len(views) != 2 || views[0].Number != "MATCH" || views[1].Number != "LEGACY-MATCH" {
		t.Fatalf("invoice data leaked from another server: %+v", views)
	}
	if ServerInvoiceViewsForInstallation(invoices, "", domains) != nil {
		t.Fatal("missing installation ID must fail closed")
	}
}

func TestServerBillingDashboardSeparatesForecastPaymentsAndCurrency(t *testing.T) {
	now := time.Now().UTC()
	server := ServerView{
		ID: "server-a",
		Invoices: []ServerInvoiceView{
			{Status: "paid", Currency: "EUR", AmountMinor: 2000, PaidAt: now.Format(time.RFC3339)},
			{Status: "paid", Currency: "EUR", AmountMinor: 1000, PaidAt: now.AddDate(0, -1, 0).Format(time.RFC3339)},
			{Status: "issued", Currency: "EUR", AmountMinor: 4500, DueAt: now.AddDate(0, 0, -1).Format("2006-01-02")},
			{Status: "payment_error", Currency: "EUR", AmountMinor: 500, DueAt: now.AddDate(0, 0, 3).Format("2006-01-02")},
			{Status: "paid", Currency: "USD", AmountMinor: 200000, PaidAt: now.Format(time.RFC3339)},
			{Status: "cancelled", Currency: "EUR", AmountMinor: 30000},
		},
	}
	policy := expenses.ServerPolicy{InstallationID: "server-a", Mode: expenses.ModeActual, DiskRatePer100GBMinor: 1500,
		ActualMonthlyExpenseMinor: 20000, Currency: "EUR", BillingCapacityPercent: 30,
		DiskTotalBytes: 100 * expenses.DecimalGigabyte,
	}
	ApplyServerCostView(&server, policy, nil)
	if server.BillingPaidLabel != MoneyLabel(2000, "EUR") {
		t.Fatalf("paid metric %q includes wrong period or currency", server.BillingPaidLabel)
	}
	if server.BillingOpenLabel != MoneyLabel(5000, "EUR") {
		t.Fatalf("open metric %q unexpected", server.BillingOpenLabel)
	}
	if server.BillingOverdueLabel != MoneyLabel(4500, "EUR") {
		t.Fatalf("overdue metric %q unexpected", server.BillingOverdueLabel)
	}
	if server.BillingOtherCurrencies != 1 {
		t.Fatalf("foreign-currency invoice missing or counted: %d", server.BillingOtherCurrencies)
	}
}
