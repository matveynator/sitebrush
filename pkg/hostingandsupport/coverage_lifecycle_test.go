package hostingandsupport

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestAdditionalHostingPersistencePaths(t *testing.T) {
	store := newLifecycleStore(t)
	ctx := context.Background()
	if err := store.LogRegistrySyncEvent(ctx, " install-1 ", " stored ", " "); err != nil {
		t.Fatal(err)
	}
	if err := store.LogRegistrySyncEvent(ctx, " ", "ignored", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.LogRegistrySyncEventWithSummary(ctx, "install-2", "stored", "", RegistrySyncSummary{Version: 2, ServerDomain: "example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := store.LogServerNetworkCheck(ctx, "install-1", "EXAMPLE.COM", "192.0.2.1", true, 12, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSiteTLSCheck(ctx, SiteTLSCheck{Domain: "EXAMPLE.COM", InstallationID: "install-1", HTTPSAvailable: true, CertDaysLeft: 30, StatusClass: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSiteTLSCheck(ctx, SiteTLSCheck{Domain: "example.com", InstallationID: "install-2", HTTPSAvailable: false, Error: "timeout"}); err != nil {
		t.Fatal(err)
	}
	if store.IsDemoDomain(ctx, " ") {
		t.Fatal("empty domain marked as demo")
	}
	if store.IsDemoDomain(ctx, "example.com") {
		t.Fatal("unconfigured domain marked as demo")
	}
	trialResult, err := store.DB.ExecContext(ctx, `INSERT INTO public_trial_sites(domain,source_url,status,created_at,delete_after,deleted_at,last_error) VALUES(?,?,?,?,?,?,?)`, "trial.example.com", "", "active", "2026-01-01T00:00:00Z", "2026-01-01T00:30:00Z", "", "")
	if err != nil {
		t.Fatal(err)
	}
	trialID, err := trialResult.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	store.SavePublicTrialCleanupError(ctx, trialID, errors.New("cleanup failed"))
	store.SavePublicTrialCleanupError(ctx, trialID, nil)
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO server_settings(name,value) VALUES(?,?)`, "coverage-setting", " setting value "); err != nil {
		t.Fatal(err)
	}
	if got := settingText(ctx, store.DB.(*sql.DB), "coverage-setting"); got != "setting value" {
		t.Fatalf("setting text=%q", got)
	}
	if count := store.CountServiceMailSubnetEventsSince(ctx, "192.0.2.0/24", time.Now().Add(-time.Hour)); count != 0 {
		t.Fatalf("empty subnet event count=%d", count)
	}
	if count := store.CountServiceMailSubnetEventsSince(ctx, "192.0.2.0/16", time.Now()); count != 0 {
		t.Fatalf("invalid subnet event count=%d", count)
	}
	if err := store.MarkInvoiceDelivery(ctx, 1, "failed", "delivery error"); err != nil {
		t.Fatal(err)
	}
	var deliveryCount int
	if err := store.DB.(*sql.DB).QueryRow(`SELECT COUNT(*) FROM billing_invoice_deliveries WHERE invoice_id=1`).Scan(&deliveryCount); err != nil {
		t.Fatal(err)
	}
	if deliveryCount != 1 {
		t.Fatalf("invoice delivery count=%d", deliveryCount)
	}
}

func TestBillingCustomerFinancialTotals(t *testing.T) {
	store := newLifecycleStore(t)
	ctx := context.Background()
	now := time.Date(2026, time.September, 24, 12, 0, 0, 0, time.UTC)
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO billing_invoices(invoice_number,customer_email,domain,plan_name,amount,currency,status,provider,payment_url,due_at,paid_at,notes,created_at,updated_at,customer_id,amount_minor,reserve_minor) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"INV-1", "owner@example.com", "example.com", "Plan", "12.00", "USD", "paid", "stripe", "", "", now.Format(time.RFC3339), "", now.Format(time.RFC3339), now.Format(time.RFC3339), "customer-1", 1200, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO billing_invoice_lines(invoice_id,domain,description,cost_share_minor) VALUES(1,?,?,?)`, "example.com", "Hosting", 900); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO billing_payments(invoice_id,provider,external_id,amount_minor,currency,status,commission_minor,paid_at,created_at,updated_at) VALUES(1,'stripe','ext-1',1200,'USD','paid',35,?,?,?)`, now.Format(time.RFC3339), now.Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	totals := store.BillingCustomerFinancialTotals(ctx, BillingCustomer{ID: "customer-1", PrimaryEmail: "Owner@example.com"}, now)
	if totals.LifetimePaidMinor != 1200 || totals.LifetimeCoveredMinor != 900 || totals.LifetimeReserveMinor != 100 || totals.LifetimeCommissionMinor != 35 || totals.PaidThisMonthMinor != 1200 || totals.CoveredThisMonthByCurrency["USD"] != 900 {
		t.Fatalf("billing totals=%+v", totals)
	}
}

func TestBuildSitesBasicEntryPoint(t *testing.T) {
	sites := BuildSites([]SiteUsage{{Domain: "example.com", UsedBytes: 25, LimitBytes: 100}}, nil, nil, "example.com")
	if len(sites) != 1 || sites[0].Domain != "example.com" || sites[0].UsedBytes != 25 {
		t.Fatalf("built sites=%+v", sites)
	}
}

func TestHostingOverviewAndPublicServerFilters(t *testing.T) {
	view := BuildOverview(
		[]Site{{UsedPercent: 100}}, 3, []SiteRequest{{}},
		[]Invoice{{Status: "issued"}, {Status: "payment_error"}, {Status: "paid"}},
		[]ClientHosting{{LastSeenAt: time.Now().Add(-8 * 24 * time.Hour).Format(time.RFC3339)}},
		[]RegistrySyncEvent{{CreatedAt: ""}},
		[]ServiceMailEvent{{Error: "failed"}, {Status: "error"}},
	)
	if view.ProblemCount != 7 || view.UnpaidInvoices != 2 || view.MailErrors != 2 || view.StaleServers != 1 || view.LastSyncLabel == "" {
		t.Fatalf("overview counts=%+v", view)
	}
	if len(FastServerHostings([]ClientHosting{{ServerDomain: "server.example.com", ServerIP: "8.8.8.8"}, {ServerDomain: "localhost", ServerIP: "127.0.0.1"}})) != 1 {
		t.Fatal("public server filter did not exclude local hosting")
	}
	if !ClientHostingHasPublicIP(ClientHosting{ServerIP: "8.8.8.8"}) || ClientHostingHasPublicIP(ClientHosting{ServerIP: "192.168.1.1"}) {
		t.Fatal("public IP classification failed")
	}
	if len(RealClientHostings([]ClientHosting{{ServerDomain: "node.example.com", ServerIP: "8.8.8.8"}}, nil)) != 1 {
		t.Fatal("real client hosting predicate rejected public host")
	}
	if len(RealClientHostings([]ClientHosting{{ServerDomain: "node.example.com", ServerIP: "8.8.8.8"}}, func(domain, ip string) bool { return domain == "node.example.com" && ip == "8.8.8.8" })) != 1 {
		t.Fatal("real client hosting domain matcher rejected matching host")
	}
	if got := ServerClientCount([]ServerView{{Clients: []ServerClientView{{Email: " Owner@example.com "}, {Email: "owner@example.com"}, {Email: " "}}}}); got != 1 {
		t.Fatalf("unique server client count=%d", got)
	}
}
