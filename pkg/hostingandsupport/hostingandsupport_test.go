package hostingandsupport

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestBuildSitesMarksOnlyServerMainDomain(t *testing.T) {
	sites := BuildSitesWithDemoAndMainDomain([]SiteUsage{
		{
			Domain:     "example.com",
			LimitBytes: DefaultStorageLimitBytes,
		},
		{
			Domain:     "client.example.com",
			Aliases:    []string{"client.com"},
			LimitBytes: DefaultStorageLimitBytes,
		},
	}, nil, nil, "example.com", "", "example.com")

	if len(sites) != 2 {
		t.Fatalf("sites = %d, want 2", len(sites))
	}
	sitesByDomain := make(map[string]Site, len(sites))
	for _, site := range sites {
		sitesByDomain[site.Domain] = site
	}
	mainSite := sitesByDomain["example.com"]
	clientSite := sitesByDomain["client.example.com"]
	if !mainSite.IsMainDomain {
		t.Fatalf("main domain site was not marked: %#v", mainSite)
	}
	if clientSite.IsMainDomain {
		t.Fatalf("client subdomain with second-level alias was marked as main: %#v", clientSite)
	}
	if clientSite.Aliases != "client.com" {
		t.Fatalf("client aliases = %q, want client.com", clientSite.Aliases)
	}
}

func TestBuildSitesIncludesAssignedPlanQuotaLabels(t *testing.T) {
	plans := []Plan{{
		ID:         7,
		Name:       "Pro",
		QuotaBytes: 20 * 1024 * 1024 * 1024,
		QuotaLabel: "20 GB",
	}}
	assignments := map[string]ServiceAssignment{
		"paid.example.com": {PlanID: 7, ServiceStatus: "paid"},
	}
	sites := BuildSitesWithDemoAndMainDomain([]SiteUsage{
		{
			Domain:     "paid.example.com",
			UsedBytes:  5 * 1024 * 1024 * 1024,
			LimitBytes: 20 * 1024 * 1024 * 1024,
		},
		{
			Domain:     "free.example.com",
			UsedBytes:  1024,
			LimitBytes: DefaultStorageLimitBytes,
		},
	}, plans, assignments, "paid.example.com", "", "")

	sitesByDomain := make(map[string]Site, len(sites))
	for _, site := range sites {
		sitesByDomain[site.Domain] = site
	}
	paidSite := sitesByDomain["paid.example.com"]
	if paidSite.PlanName != "Pro" || paidSite.PlanQuotaLabel != "20 GB" {
		t.Fatalf("paid site plan = %q %q, want Pro 20 GB", paidSite.PlanName, paidSite.PlanQuotaLabel)
	}
	freeSite := sitesByDomain["free.example.com"]
	if freeSite.PlanName != "" || freeSite.PlanQuotaLabel != "" {
		t.Fatalf("free site plan = %q %q, want empty labels", freeSite.PlanName, freeSite.PlanQuotaLabel)
	}
}

func TestBillingPriceForUsedBytesRoundsToFiftyMegabytes(t *testing.T) {
	const megabyte = int64(1000 * 1000)
	tests := []struct {
		name              string
		usedBytes         int64
		wantUsedMegabytes int64
		wantBillableMB    int64
		wantAmount        string
		wantBillable      bool
		wantStatus        string
	}{
		{
			name:              "empty site is free with zero price",
			usedBytes:         0,
			wantUsedMegabytes: 0,
			wantBillableMB:    0,
			wantAmount:        "0.00",
			wantBillable:      false,
			wantStatus:        "бесплатно до 500 MB",
		},
		{
			name:              "included five hundred megabytes is displayed but free",
			usedBytes:         500 * megabyte,
			wantUsedMegabytes: 500,
			wantBillableMB:    500,
			wantAmount:        "1.00",
			wantBillable:      false,
			wantStatus:        "бесплатно до 500 MB",
		},
		{
			name:              "next megabyte is rounded up to next fifty megabytes",
			usedBytes:         501 * megabyte,
			wantUsedMegabytes: 501,
			wantBillableMB:    550,
			wantAmount:        "1.10",
			wantBillable:      true,
			wantStatus:        "к выставлению",
		},
		{
			name:              "one thousand megabytes costs two euros",
			usedBytes:         1000 * megabyte,
			wantUsedMegabytes: 1000,
			wantBillableMB:    1000,
			wantAmount:        "2.00",
			wantBillable:      true,
			wantStatus:        "к выставлению",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			price := BillingPriceForUsedBytes(test.usedBytes)
			if price.UsedMegabytes != test.wantUsedMegabytes {
				t.Fatalf("UsedMegabytes = %d, want %d", price.UsedMegabytes, test.wantUsedMegabytes)
			}
			if price.BillableMegabytes != test.wantBillableMB {
				t.Fatalf("BillableMegabytes = %d, want %d", price.BillableMegabytes, test.wantBillableMB)
			}
			if price.Amount != test.wantAmount {
				t.Fatalf("Amount = %q, want %q", price.Amount, test.wantAmount)
			}
			if price.Billable != test.wantBillable {
				t.Fatalf("Billable = %v, want %v", price.Billable, test.wantBillable)
			}
			if price.StatusText != test.wantStatus {
				t.Fatalf("StatusText = %q, want %q", price.StatusText, test.wantStatus)
			}
			if price.Currency != "EUR" {
				t.Fatalf("Currency = %q, want EUR", price.Currency)
			}
		})
	}
}

func TestBillableSiteCountUsesBillingThreshold(t *testing.T) {
	sites := []ServerSiteView{
		{Domain: "free.example.com", BillingBillable: false},
		{Domain: "paid.example.com", BillingBillable: true},
		{Domain: "", BillingBillable: true},
	}
	if count := BillableSiteCount(sites); count != 1 {
		t.Fatalf("BillableSiteCount = %d, want 1", count)
	}
}

func TestServiceMailInstallationsDoesNotBlockSingleConnectionDatabase(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "hostingandsupport.db"))
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	t.Cleanup(func() {
		_ = database.Close()
	})
	if err := Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	store := Store{DB: database}
	if err := store.UpsertServiceMailInstallation(context.Background(), "installation-1", "public-key", "203.0.113.10", "example.com"); err != nil {
		t.Fatal(err)
	}
	if err := store.LogServiceMailEvent(context.Background(), ServiceMailEvent{InstallationID: "installation-1", SourceIP: "203.0.113.10", Recipient: "admin@example.net", RecipientDomain: "example.net", CodeKind: "login_code", Status: "sent"}); err != nil {
		t.Fatal(err)
	}

	done := make(chan []ServiceMailInstallation, 1)
	go func() {
		done <- store.ServiceMailInstallations(context.Background())
	}()
	select {
	case installations := <-done:
		if len(installations) != 1 {
			t.Fatalf("installations = %d, want 1", len(installations))
		}
		if installations[0].SentCount != 1 {
			t.Fatalf("sent count = %d, want 1", installations[0].SentCount)
		}
	case <-time.After(time.Second):
		t.Fatal("ServiceMailInstallations blocked with a single open database connection")
	}
}

func TestServiceMailSettingsRoundTrip(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "hostingandsupport.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	if err := Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	store := Store{DB: database}
	if !store.ServiceMailRelayEnabled(context.Background()) {
		t.Fatal("service mail relay should be enabled by default")
	}
	if err := store.SaveServiceMailSettings(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if store.ServiceMailRelayEnabled(context.Background()) {
		t.Fatal("service mail relay setting did not save disabled state")
	}
}

func TestPanelSnapshotRoundTrip(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "hostingandsupport.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	if err := Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	store := Store{DB: database}
	want := PanelSnapshotRecord{Version: 1, PayloadJSON: `{"main_domain":"sitebrush.com"}`, BuiltAt: "2026-07-22T12:00:00Z"}
	if err := store.SavePanelSnapshot(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, found := store.PanelSnapshot(context.Background())
	if !found || got != want {
		t.Fatalf("snapshot = %#v found=%v, want %#v", got, found, want)
	}
	want.PayloadJSON = `{"main_domain":"example.com"}`
	if err := store.SavePanelSnapshot(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, found = store.PanelSnapshot(context.Background())
	if !found || got != want {
		t.Fatalf("updated snapshot = %#v found=%v, want %#v", got, found, want)
	}
}

func TestDeleteServiceMailEventsDeletesOnlySelectedRows(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "hostingandsupport.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	if err := Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	store := Store{DB: database}
	for _, event := range []ServiceMailEvent{
		{InstallationID: "installation-1", SourceIP: "203.0.113.10", Recipient: "one@example.net", RecipientDomain: "example.net", CodeKind: "login_code", Status: "sent"},
		{InstallationID: "installation-1", SourceIP: "203.0.113.10", Recipient: "two@example.net", RecipientDomain: "example.net", CodeKind: "login_code", Status: "error"},
		{InstallationID: "installation-2", SourceIP: "203.0.113.11", Recipient: "three@example.net", RecipientDomain: "example.net", CodeKind: "email_confirm", Status: "sent"},
	} {
		if err := store.LogServiceMailEvent(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	events := store.ServiceMailEvents(context.Background(), 10)
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3", len(events))
	}
	if err := store.DeleteServiceMailEvents(context.Background(), []int{events[0].ID, events[2].ID, 0, -1}); err != nil {
		t.Fatal(err)
	}
	events = store.ServiceMailEvents(context.Background(), 10)
	if len(events) != 1 {
		t.Fatalf("events after delete = %d, want 1: %#v", len(events), events)
	}
	if events[0].Recipient != "two@example.net" {
		t.Fatalf("remaining recipient = %q, want two@example.net", events[0].Recipient)
	}
	if err := store.DeleteServiceMailEvents(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	events = store.ServiceMailEvents(context.Background(), 10)
	if len(events) != 1 {
		t.Fatalf("events after empty delete = %d, want 1", len(events))
	}
}

func TestSupportEventsRoundTrip(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "hostingandsupport.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	if err := Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	store := Store{DB: database}
	event := HostingSnapshotEvent{
		Kind:    "client_login",
		Status:  "success",
		Email:   "Owner@Example.COM ",
		Domain:  "Host.Example.COM ",
		Message: "client signed in",
	}
	if err := store.LogSupportEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	events := store.SupportEvents(context.Background(), 10)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Kind != "client_login" || events[0].Status != "success" {
		t.Fatalf("event status was not preserved: %#v", events[0])
	}
	if events[0].Email != "owner@example.com" || events[0].Domain != "host.example.com" {
		t.Fatalf("event identity was not normalized: %#v", events[0])
	}
	if events[0].Message != "client signed in" || events[0].CreatedAt == "" {
		t.Fatalf("event details missing: %#v", events[0])
	}
}

func TestPaymentProvidersAndInvoicesRoundTrip(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "hostingandsupport.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	if err := Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	store := Store{DB: database}
	providers := store.PaymentProviders(context.Background())
	if len(providers) != 4 {
		t.Fatalf("providers = %d, want 4", len(providers))
	}
	if providers[0].Provider != "sitebrush_com" || !providers[0].Enabled || providers[1].Provider != "stripe" || providers[2].Provider != "paypal" || providers[3].Provider != "sbp" {
		t.Fatalf("providers order = %#v", providers)
	}
	demoInvoice, err := store.CreateInvoice(context.Background(), Invoice{
		CustomerEmail: "demo@example.com",
		Domain:        "demo.example.com",
		Amount:        "1000",
		Currency:      "RUB",
	})
	if err != nil {
		t.Fatal(err)
	}
	if demoInvoice.Provider != "sitebrush_com" || !strings.Contains(demoInvoice.PaymentURL, "hosting_and_support_demo_payment") {
		t.Fatalf("demo invoice did not use SiteBrush.com payment defaults: %#v", demoInvoice)
	}
	if _, err := store.CreateInvoice(context.Background(), Invoice{
		CustomerEmail: "client@example.com",
		Domain:        "example.com",
		Amount:        "1500",
		Currency:      "RUB",
		Provider:      "stripe",
	}); err == nil {
		t.Fatal("CreateInvoice with disabled provider succeeded")
	}
	stripeTemplate := "https://pay.example.test/{invoice}?amount={amount}&currency={currency}&email={email}&domain={domain}"
	if err := store.SavePaymentProvider(context.Background(), PaymentProvider{
		Provider:     "stripe",
		Enabled:      true,
		PaymentURL:   stripeTemplate,
		Instructions: "Stripe Checkout link",
	}); err != nil {
		t.Fatal(err)
	}
	createdInvoice, err := store.CreateInvoice(context.Background(), Invoice{
		CustomerEmail:   "Client+Billing@Example.COM ",
		Domain:          "Example.COM ",
		PlanName:        "Pro",
		Amount:          "1500.50",
		Currency:        "rub",
		Provider:        "stripe",
		DueAt:           "2026-07-01",
		Notes:           "hosting",
		Recurring:       true,
		RecurringPeriod: "quarterly",
	})
	if err != nil {
		t.Fatal(err)
	}
	if createdInvoice.ID == 0 || createdInvoice.Number == "" || createdInvoice.Status != "issued" {
		t.Fatalf("created invoice missing identity or status: %#v", createdInvoice)
	}
	if createdInvoice.CustomerEmail != "client+billing@example.com" || createdInvoice.Domain != "example.com" || createdInvoice.Currency != "RUB" {
		t.Fatalf("created invoice was not normalized: %#v", createdInvoice)
	}
	if !createdInvoice.Recurring || createdInvoice.RecurringPeriod != "quarterly" {
		t.Fatalf("created invoice recurring settings were not saved: %#v", createdInvoice)
	}
	for _, expectedPart := range []string{"amount=1500.50", "currency=RUB", "email=client%2Bbilling%40example.com", "domain=example.com"} {
		if !strings.Contains(createdInvoice.PaymentURL, expectedPart) {
			t.Fatalf("payment URL %q does not contain %q", createdInvoice.PaymentURL, expectedPart)
		}
	}
	invoices := store.Invoices(context.Background(), 10)
	if len(invoices) != 2 {
		t.Fatalf("invoices = %d, want 2", len(invoices))
	}
	if !invoices[0].Recurring || invoices[0].RecurringPeriod != "quarterly" {
		t.Fatalf("stored invoice recurring settings were not loaded: %#v", invoices[0])
	}
	paidInvoice, err := store.UpdateInvoiceStatus(context.Background(), createdInvoice.ID, "paid")
	if err != nil {
		t.Fatal(err)
	}
	if paidInvoice.Status != "paid" || paidInvoice.PaidAt == "" {
		t.Fatalf("paid invoice status was not saved: %#v", paidInvoice)
	}
}

func TestHostingSnapshotRegistryRoundTrip(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "hostingandsupport.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	if err := Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	store := Store{DB: database}
	now := time.Now().UTC().Format(time.RFC3339)
	snapshot := HostingSnapshot{
		Version:              1,
		InstallationID:       "installation-1",
		OwnerEmail:           "owner@example.com",
		ServerIP:             "203.0.113.10",
		ServerStatus:         "IP 203.0.113.10",
		ServerDomain:         "host.example.com",
		SitebrushVersion:     "v1",
		OSName:               "linux",
		OSVersion:            "Debian",
		CPUModel:             "amd64",
		CPUCores:             4,
		CPUUsagePercent:      42.5,
		LoadAverage:          1.25,
		TopCPUProcessName:    "sitebrush",
		TopCPUProcessPID:     1234,
		TopCPUProcessPercent: 88.4,
		RAMTotalBytes:        8 * 1024 * 1024 * 1024,
		DiskFreeBytes:        20 * 1024 * 1024 * 1024,
		DiskTotalBytes:       40 * 1024 * 1024 * 1024,
		Plans: []HostingSnapshotPlan{{
			Name:          "Pro",
			QuotaBytes:    10 * 1024 * 1024 * 1024,
			SiteLimit:     3,
			Price:         "10",
			Currency:      "USD",
			BillingPeriod: "monthly",
			PaidStatus:    "paid",
			IsDefault:     true,
		}},
		Roles: []HostingSnapshotRole{
			{Email: "owner@example.com", Role: "superadmin", Scope: "installation"},
			{Email: "site@example.com", Role: "site_admin", Scope: "site", Domain: "client.example.com"},
		},
		Sites: []HostingSnapshotSite{{
			Domain:         "client.example.com",
			OwnerEmail:     "site@example.com",
			UsedBytes:      12,
			LimitBytes:     10,
			PlanName:       "Pro",
			PlanStatus:     "active",
			PlanPaidStatus: "paid",
			AdminEmails:    []string{"site@example.com"},
		}},
		Events: []HostingSnapshotEvent{{
			Kind:      "limit_exceeded",
			Status:    "active",
			Email:     "site@example.com",
			Domain:    "client.example.com",
			Message:   "site storage usage exceeds assigned limit",
			CreatedAt: now,
		}},
		CreatedAt: now,
	}
	if err := store.SaveHostingSnapshot(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}

	hostings := store.ClientHostings(context.Background())
	if len(hostings) != 1 {
		t.Fatalf("hostings = %d, want 1", len(hostings))
	}
	hosting := hostings[0]
	if hosting.ServerDomain != "host.example.com" || hosting.OSName != "linux" || hosting.CPUCores != 4 {
		t.Fatalf("hosting metadata was not preserved: %#v", hosting)
	}
	if hosting.TopCPUProcessName != "sitebrush" || hosting.TopCPUProcessPID != 1234 || hosting.TopCPUProcessPercent != 88.4 {
		t.Fatalf("top cpu process was not preserved: %#v", hosting)
	}
	if len(hosting.ResourceHistory) != 1 || hosting.ResourceHistory[0].CPUUsagePercent != 42.5 || hosting.ResourceHistory[0].TopCPUProcessName != "sitebrush" || hosting.ResourceHistory[0].DiskUsedPercent != 50 {
		t.Fatalf("resource history was not preserved: %#v", hosting.ResourceHistory)
	}
	if len(hosting.Sites) != 1 || !hosting.Sites[0].OverLimit || hosting.Sites[0].OwnerEmail != "site@example.com" {
		t.Fatalf("site metadata was not preserved: %#v", hosting.Sites)
	}
	if len(hosting.Plans) != 1 || hosting.Plans[0].PaidStatus != "paid" || !hosting.Plans[0].IsDefault {
		t.Fatalf("plan metadata was not preserved: %#v", hosting.Plans)
	}
	if len(hosting.Roles) != 2 {
		t.Fatalf("roles = %d, want 2: %#v", len(hosting.Roles), hosting.Roles)
	}
	if len(hosting.Events) != 1 || hosting.Events[0].Kind != "limit_exceeded" {
		t.Fatalf("events were not preserved: %#v", hosting.Events)
	}
	syncEvents := store.RegistrySyncEvents(context.Background(), 10)
	if len(syncEvents) != 1 {
		t.Fatalf("sync events = %d, want 1", len(syncEvents))
	}
	syncEvent := syncEvents[0]
	if !syncEvent.HasSummary || syncEvent.StatusLabel != "принято" {
		t.Fatalf("sync summary missing: %#v", syncEvent)
	}
	if syncEvent.Summary.SiteCount != 1 || syncEvent.Summary.PlanCount != 1 || syncEvent.Summary.RoleCount != 2 || syncEvent.Summary.EventCount != 1 {
		t.Fatalf("sync counts = sites:%d plans:%d roles:%d events:%d", syncEvent.Summary.SiteCount, syncEvent.Summary.PlanCount, syncEvent.Summary.RoleCount, syncEvent.Summary.EventCount)
	}
	if syncEvent.Summary.TopCPUProcessName != "sitebrush" || syncEvent.Summary.TopCPUProcessPercent != 88.4 {
		t.Fatalf("sync top cpu process was not preserved: %#v", syncEvent.Summary)
	}
	if len(syncEvent.Summary.Sites) != 1 || !syncEvent.Summary.Sites[0].OverLimit || syncEvent.Summary.Sites[0].UsedLabel == "" {
		t.Fatalf("sync site summary was not preserved: %#v", syncEvent.Summary.Sites)
	}
	if len(syncEvent.Summary.Plans) != 1 || syncEvent.Summary.Plans[0].QuotaLabel == "" {
		t.Fatalf("sync plan summary was not preserved: %#v", syncEvent.Summary.Plans)
	}
}

func TestRegistrySyncEventsReadsLegacyRowsWithoutSummary(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "hostingandsupport.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	if err := Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(context.Background(), `INSERT INTO registry_sync_events(installation_id,status,error,created_at) VALUES('installation-legacy','stored','','2026-06-17T10:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	store := Store{DB: database}
	syncEvents := store.RegistrySyncEvents(context.Background(), 10)
	if len(syncEvents) != 1 {
		t.Fatalf("sync events = %d, want 1", len(syncEvents))
	}
	if syncEvents[0].HasSummary {
		t.Fatalf("legacy event unexpectedly has summary: %#v", syncEvents[0])
	}
	if syncEvents[0].StatusLabel != "принято · старый формат" {
		t.Fatalf("legacy status label = %q", syncEvents[0].StatusLabel)
	}
}

func TestBuildServerViewsMergesDuplicateLocalAndRemoteServer(t *testing.T) {
	localServer := BuildLocalServerView(LocalServerViewInput{
		Sites: []Site{{
			Domain:            "sitebrush.com",
			UsedLabel:         "1 GB",
			LimitLabel:        "40 GB",
			QuotaInput:        "40gb",
			BillingStatusText: "к выставлению",
			BillingBillable:   true,
		}},
		SystemMetrics: []ServerMetricView{
			{Name: "OS", Value: "darwin 15", StatusClass: "hosting-metric-ok"},
			{Name: "CPU", Value: "Apple · 8 ядер", StatusClass: "hosting-metric-ok"},
		},
		MainDomain: "sitebrush.com",
	})
	remoteHosting := ClientHosting{
		InstallationID:       "installation-1",
		ServerDomain:         "sitebrush.com",
		ServerStatus:         "IP 203.0.113.10",
		OSName:               "linux",
		OSVersion:            "Debian",
		CPUModel:             "amd64",
		CPUCores:             4,
		CPUUsagePercent:      12,
		LoadAverage:          0.5,
		TopCPUProcessName:    "sitebrush",
		TopCPUProcessPID:     10,
		TopCPUProcessPercent: 7,
		RAMTotalLabel:        "8 GB",
		DiskUsedLabel:        "20 GB",
		DiskFreeLabel:        "20 GB",
		DiskTotalLabel:       "40 GB",
		DiskUsedPercent:      50,
		DiskStatusClass:      "hosting-metric-ok",
		NetworkUptimeLabel:   "100.00%",
		NetworkStatusClass:   "hosting-metric-ok",
		ServerUptimeLabel:    "1 дн.",
		ServerUptimeClass:    "hosting-metric-ok",
		Sites: []ClientHostingSite{{
			Domain:      "sitebrush.com",
			UsedLabel:   "20 GB",
			LimitLabel:  "40 GB",
			AdminEmails: []string{"owner@example.com"},
		}},
		SiteCount:      1,
		TotalUsedLabel: "20 GB",
	}

	servers := BuildServerViews(localServer, []ClientHosting{remoteHosting}, nil, "v1")
	if len(servers) != 1 {
		t.Fatalf("servers = %d, want 1: %#v", len(servers), servers)
	}
	if !servers[0].Local || servers[0].ID != "installation-1" || servers[0].OSLabel != "linux Debian" {
		t.Fatalf("merged server did not keep local badge and remote metrics: %#v", servers[0])
	}
	if len(servers[0].SystemMetrics) == 0 || servers[0].SystemMetrics[0].Name != "CPU" {
		t.Fatalf("merged server metrics were not built from remote hosting: %#v", servers[0].SystemMetrics)
	}
	if len(servers[0].Sites) != 1 || !servers[0].Sites[0].CanEditQuota || servers[0].Sites[0].QuotaInput != "40gb" || servers[0].Sites[0].LimitLabel != "40 GB" {
		t.Fatalf("merged local quota is not editable: %#v", servers[0].Sites)
	}
}

func TestServerClientViewsSortSitesByUsedBytes(t *testing.T) {
	clients := serverClientViewsFromSites([]ServerSiteView{
		{Domain: "small.example.com", OwnerEmail: "owner@example.com", UsedBytes: 10},
		{Domain: "large.example.com", OwnerEmail: "owner@example.com", UsedBytes: 30},
		{Domain: "alpha.example.com", OwnerEmail: "owner@example.com", UsedBytes: 30},
	})
	if len(clients) != 1 {
		t.Fatalf("clients = %d, want 1: %#v", len(clients), clients)
	}
	got := []string{clients[0].Sites[0].Domain, clients[0].Sites[1].Domain, clients[0].Sites[2].Domain}
	want := []string{"alpha.example.com", "large.example.com", "small.example.com"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("site order = %#v, want %#v", got, want)
		}
	}
}

func TestServerSystemMetricViewsQueuePercentAndStatus(t *testing.T) {
	tests := []struct {
		name            string
		loadAverage     float64
		wantValue       string
		wantStatusClass string
		wantPercent     int
	}{
		{name: "full queue is ok", loadAverage: 4, wantValue: "100%", wantStatusClass: "hosting-metric-ok", wantPercent: 100},
		{name: "over full queue warns", loadAverage: 5, wantValue: "125%", wantStatusClass: "hosting-metric-warning", wantPercent: 100},
		{name: "double queue is critical", loadAverage: 9, wantValue: "225%", wantStatusClass: "hosting-metric-danger", wantPercent: 100},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metrics := ServerSystemMetricViews(ClientHosting{CPUCores: 4, LoadAverage: test.loadAverage})
			var queueMetric ServerMetricView
			for _, metric := range metrics {
				if metric.Name == "Очередь" {
					queueMetric = metric
				}
				if metric.Name == "Disk" {
					t.Fatalf("unexpected Disk metric: %#v", metrics)
				}
			}
			if queueMetric.Name == "" {
				t.Fatalf("queue metric was not found: %#v", metrics)
			}
			if queueMetric.Value != test.wantValue || queueMetric.StatusClass != test.wantStatusClass || queueMetric.Percent != test.wantPercent {
				t.Fatalf("queue metric = %#v, want value=%q status=%q percent=%d", queueMetric, test.wantValue, test.wantStatusClass, test.wantPercent)
			}
		})
	}
}

func TestSaveHostingSnapshotReplacesCurrentRegistryState(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "hostingandsupport.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	if err := Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	store := Store{DB: database}
	firstSnapshot := HostingSnapshot{
		Version:        1,
		InstallationID: "installation-1",
		OwnerEmail:     "owner@example.com",
		Sites: []HostingSnapshotSite{
			{Domain: "old.example.com", OwnerEmail: "old@example.com", UsedBytes: 1, LimitBytes: 10, AdminEmails: []string{"old@example.com"}},
			{Domain: "keep.example.com", OwnerEmail: "keep@example.com", UsedBytes: 2, LimitBytes: 10, PlanName: "Free", PlanPaidStatus: "free", AdminEmails: []string{"keep@example.com"}},
		},
		Plans: []HostingSnapshotPlan{
			{Name: "Free", QuotaBytes: 10, PaidStatus: "free", IsDefault: true},
			{Name: "Old", QuotaBytes: 20, PaidStatus: "paid"},
		},
		Roles: []HostingSnapshotRole{
			{Email: "old@example.com", Role: "site_admin", Scope: "site", Domain: "old.example.com"},
			{Email: "owner@example.com", Role: "superadmin", Scope: "installation"},
		},
		CreatedAt: "2026-06-17T10:00:00Z",
	}
	if err := store.SaveHostingSnapshot(context.Background(), firstSnapshot); err != nil {
		t.Fatal(err)
	}
	secondSnapshot := firstSnapshot
	secondSnapshot.Sites = []HostingSnapshotSite{
		{Domain: "keep.example.com", OwnerEmail: "keep@example.com", UsedBytes: 12, LimitBytes: 10, PlanName: "Pro", PlanPaidStatus: "paid", AdminEmails: []string{"keep@example.com", "keep@example.com"}},
	}
	secondSnapshot.Plans = []HostingSnapshotPlan{{Name: "Pro", QuotaBytes: 10, PaidStatus: "paid", IsDefault: true}}
	secondSnapshot.Roles = []HostingSnapshotRole{{Email: "keep@example.com", Role: "site_owner", Scope: "site", Domain: "keep.example.com"}}
	secondSnapshot.CreatedAt = "2026-06-17T10:01:00Z"
	if err := store.SaveHostingSnapshot(context.Background(), secondSnapshot); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHostingSnapshot(context.Background(), secondSnapshot); err != nil {
		t.Fatal(err)
	}

	hostings := store.ClientHostings(context.Background())
	if len(hostings) != 1 {
		t.Fatalf("hostings = %d, want 1", len(hostings))
	}
	hosting := hostings[0]
	if len(hosting.Sites) != 1 || hosting.Sites[0].Domain != "keep.example.com" || !hosting.Sites[0].OverLimit || len(hosting.Sites[0].AdminEmails) != 1 {
		t.Fatalf("sites were not replaced idempotently: %#v", hosting.Sites)
	}
	if len(hosting.Plans) != 1 || hosting.Plans[0].Name != "Pro" || hosting.Plans[0].PaidStatus != "paid" {
		t.Fatalf("plans were not replaced idempotently: %#v", hosting.Plans)
	}
	if len(hosting.Roles) != 1 || hosting.Roles[0].Role != "site_owner" {
		t.Fatalf("roles were not replaced idempotently: %#v", hosting.Roles)
	}
}
