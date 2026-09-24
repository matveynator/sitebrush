package hostingandsupport

import (
	"strings"
	"testing"
	"time"
)

func TestHostingCoverageViewHelpers(t *testing.T) {
	clientHosting := ClientHosting{
		InstallationID: "install-1",
		ServerDomain:   " node.customer.example ",
		ServerIP:       "203.0.113.15",
		ServerStatus:   "online",
		OwnerEmail:     "Owner@Example.com",
		ClientEmails:   []string{"user@example.com"},
		Roles: []ClientHostingRole{
			{Email: "Owner@example.com", Role: "owner"},
			{Email: "other@example.com", Role: "viewer"},
			{Email: " ", Role: "invalid"},
		},
	}
	if HostingDisplayName(clientHosting) != "node.customer.example" || HostingStatusLabel(clientHosting) != "IP 203.0.113.15" {
		t.Fatal("hosting display labels are incorrect")
	}
	if HostingDisplayName(ClientHosting{InstallationID: "fallback"}) != "fallback" || HostingStatusLabel(ClientHosting{}) != "локальная инсталляция" {
		t.Fatal("hosting label fallbacks are incorrect")
	}
	roles := ClientRolesForEmails(clientHosting, map[string]struct{}{"owner@example.com": {}})
	if len(roles) != 1 || roles[0].Role != "owner" {
		t.Fatalf("client roles = %#v", roles)
	}
	if !HostingBelongsToClientEmails(clientHosting, map[string]struct{}{"user@example.com": {}}) || HostingBelongsToClientEmails(clientHosting, nil) {
		t.Fatal("hosting email ownership check failed")
	}
	if HostingHasSpecialStatus(clientHosting) || !HostingHasSpecialStatus(ClientHosting{ServerIP: "127.0.0.1"}) || !HostingHasSpecialStatus(ClientHosting{ServerIP: "invalid"}) {
		t.Fatal("special hosting IP classification failed")
	}
	if !DomainIsLocalDevelopment("app.localhost", "203.0.113.1") || !DomainIsLocalDevelopment("example.com", "192.168.1.1") || DomainIsLocalDevelopment("example.com", "203.0.113.1") {
		t.Fatal("local development classification failed")
	}
	for address, want := range map[[2]string]string{
		{"localhost", "203.0.113.1"}:   "локальная инсталляция · 203.0.113.1",
		{"192.0.2.1", "203.0.113.1"}:   "тестовый сервер · 203.0.113.1",
		{"example.com", "192.168.1.1"}: "частный IP 192.168.1.1",
		{"example.com", "bad-ip"}:      "IP bad-ip",
		{"example.com", "203.0.113.1"}: "IP 203.0.113.1",
		{"example.com", ""}:            "сервер без публичного IP",
	} {
		if got := InstallationStatus(address[0], address[1]); got != want {
			t.Errorf("InstallationStatus(%q, %q) = %q, want %q", address[0], address[1], got, want)
		}
	}
	if KnownParentDomain("app.customer.example", map[string]struct{}{"customer.example": {}}) != "customer.example" || KnownParentDomain("example.com", map[string]struct{}{"example.com": {}}) != "" {
		t.Fatal("known parent domain resolution failed")
	}
	plans := ClientPlansFromPlans([]Plan{{Name: "Free", Price: "0"}, {Name: "Paid", Price: "9.00", IsDefault: true}})
	if len(plans) != 2 || plans[0].PaidStatus != "free" || plans[1].PaidStatus != "paid" || !plans[1].IsDefault {
		t.Fatalf("client plans = %#v", plans)
	}
	if HostingSyncIsStale("", time.Now()) == false || HostingSyncIsStale("bad-time", time.Now()) == false || HostingSyncIsStale(time.Now().Add(-8*24*time.Hour).Format(time.RFC3339), time.Now()) == false {
		t.Fatal("stale hosting synchronization was not detected")
	}
}

func TestHostingCoverageInvoiceHelpers(t *testing.T) {
	if InvoiceStatusLabel("issued") != "ожидает оплаты" || InvoiceStatusLabel("paid") != "оплачен" || InvoiceStatusLabel("payment_error") != "ошибка оплаты" || InvoiceStatusLabel("cancelled") != "отменён" || InvoiceStatusLabel(" ") != "неизвестно" {
		t.Fatal("invoice status labels are incorrect")
	}
	invoice := Invoice{Status: "issued", Domain: "WWW.Example.com", CustomerEmail: "OWNER@example.com", Amount: "10", Currency: "USD", PaymentURL: "https://pay.example", CreatedAt: "today", PaidAt: "tomorrow", UpdatedAt: "tomorrow", DueAt: "later", Recurring: true}
	views := ServerInvoiceViews([]Invoice{invoice, {Number: "excluded", Domain: "other.example"}}, map[string]struct{}{"www.example.com": {}}, nil)
	if len(views) != 1 || !views[0].CanPay || views[0].StatusLabel != "ожидает оплаты" || views[0].PeriodLabel != "до later" || views[0].RecurringLabel != "периодический · monthly" {
		t.Fatalf("server invoice views = %#v", views)
	}
	if !strings.Contains(views[0].HistoryLabel, "создан today") || !strings.Contains(views[0].HistoryLabel, "обновлён tomorrow") || !strings.Contains(views[0].HistoryLabel, "оплачен tomorrow") {
		t.Fatalf("invoice history = %q", views[0].HistoryLabel)
	}
	if InvoicePeriodLabel(Invoice{}) != "текущий период обслуживания" || InvoiceRecurringLabel(Invoice{}) != "разовый счёт" {
		t.Fatal("invoice fallback labels are incorrect")
	}
	paid := ServerInvoiceViews([]Invoice{{Status: "paid", PaymentURL: "https://pay.example"}}, nil, nil)
	if len(paid) != 1 || paid[0].CanPay {
		t.Fatal("paid invoice remained payable")
	}
}

func TestHostingCoverageQuotaAndRetentionParsers(t *testing.T) {
	if bytes, set, err := ParseQuotaLimitBytes(""); bytes != 0 || set || err != nil {
		t.Fatalf("empty quota = %d %t %v", bytes, set, err)
	}
	for raw, want := range map[string]int64{"50mb": 50 * 1024 * 1024, "2 M": 2 * 1024 * 1024, "3gb": 3 * 1024 * 1024 * 1024, "4g": 4 * 1024 * 1024 * 1024} {
		if got, set, err := ParseQuotaLimitBytes(raw); err != nil || !set || got != want {
			t.Errorf("ParseQuotaLimitBytes(%q) = %d %t %v", raw, got, set, err)
		}
	}
	for _, raw := range []string{"50", "0mb", "-1gb", "999999999999999999999gb"} {
		if _, _, err := ParseQuotaLimitBytes(raw); err == nil {
			t.Errorf("invalid quota accepted: %q", raw)
		}
	}
	if FormatQuotaInput(2*1024*1024*1024) != "2gb" || FormatQuotaInput(3*1024*1024) != "3mb" {
		t.Fatal("quota input formatting failed")
	}
	if days, err := ParseDeletionBackupRetentionDays(""); err != nil || days != DefaultDeletionBackupRetentionDays {
		t.Fatalf("default retention = %d, %v", days, err)
	}
	if days, err := ParseDeletionBackupRetentionDays("30"); err != nil || days != 30 {
		t.Fatalf("explicit retention = %d, %v", days, err)
	}
	for _, raw := range []string{"bad", "0", "3651"} {
		if _, err := ParseDeletionBackupRetentionDays(raw); err == nil {
			t.Errorf("invalid retention accepted: %q", raw)
		}
	}
	expiresAt := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	updated := RewriteDeletionBackupMetadataRetention(`{"domain":"example.com"}`, 30, expiresAt)
	if !strings.Contains(updated, `"retention_days":30`) || !strings.Contains(updated, expiresAt.Format(time.RFC3339)) {
		t.Fatalf("updated backup metadata = %s", updated)
	}
	if RewriteDeletionBackupMetadataRetention("not-json", 30, expiresAt) != "not-json" {
		t.Fatal("invalid backup metadata was changed")
	}
}

func TestHostingCoverageAggregationHelpers(t *testing.T) {
	if DemoPaymentProviders(" https://pay.example ")[0].PaymentURL != "https://pay.example" {
		t.Fatal("demo payment URL was not trimmed")
	}
	if !HostingSyncIsStale(time.Now().Add(-8*24*time.Hour).Format(time.RFC3339), time.Now()) || HostingSyncIsStale(time.Now().Format(time.RFC3339), time.Now()) {
		t.Fatal("hosting sync age boundary is incorrect")
	}
	if !clientHostingLooksPublic(ClientHosting{ServerDomain: "node.example.com", ServerIP: "8.8.8.8"}) || clientHostingLooksPublic(ClientHosting{ServerDomain: "localhost", ServerIP: "8.8.8.8"}) || clientHostingLooksPublic(ClientHosting{ServerDomain: "node.example.com", ServerIP: "127.0.0.1"}) {
		t.Fatal("public hosting classification is incorrect")
	}
	if !clientHostingHasPublicIP(ClientHosting{ServerIP: "8.8.8.8"}) || clientHostingHasPublicIP(ClientHosting{ServerIP: "192.168.1.1"}) || clientHostingHasPublicIP(ClientHosting{ServerIP: "bad-ip"}) {
		t.Fatal("public IP classification is incorrect")
	}
	if FirstHostingSnapshotEmail([]string{" ", " OWNER@Example.com "}) != "owner@example.com" || FirstHostingSnapshotEmail(nil) != "" {
		t.Fatal("first snapshot email selection failed")
	}
	if got := SplitEmailList("A@example.com, b@example.com; C@example.com\n"); strings.Join(got, ",") != "a@example.com,b@example.com,c@example.com" {
		t.Fatalf("split email list = %#v", got)
	}
	if parentDomain("a.b.example.com") != "example.com" || parentDomain("example.com") != "" || normalizeDomainName(" .EXAMPLE.COM. ") != "example.com" || firstNonEmpty(" ", " second ") != "second" {
		t.Fatal("domain or text normalization failed")
	}
	if bytes, ok := parseSizeLabel("1,5 MB"); !ok || bytes != 1_572_864 {
		t.Fatalf("localized size label = %d %t", bytes, ok)
	}
	if _, ok := parseSizeLabel("unknown"); ok {
		t.Fatal("invalid size label parsed")
	}
	if got := serverTotalUsedLabel([]ServerSiteView{{UsedBytes: 1_000_000}, {UsedLabel: "2 MB"}, {UsedLabel: "unknown"}}); got == "" {
		t.Fatal("server usage total is empty")
	}
	if serverTotalUsedLabel(nil) != "" {
		t.Fatal("empty server usage total should be empty")
	}
	sites := []ServerSiteView{
		{Domain: "b.example.com", OwnerEmail: "Owner@example.com", UsedBytes: 20},
		{Domain: "a.example.com", OwnerEmail: "owner@example.com", UsedBytes: 30},
		{Domain: "demo.example.com", OwnerEmail: "demo@example.com", IsDemo: true},
		{Domain: "orphan.example.com", UsedBytes: 5},
	}
	clients := serverClientViewsFromSites(sites)
	if len(clients) != 2 || clients[0].Email != "owner@example.com" || clients[0].SiteCount != 2 || clients[0].Domains != "a.example.com, b.example.com" {
		t.Fatalf("server clients = %#v", clients)
	}
	if len(normalizedDomains([]string{" B.example ", "a.example", "b.EXAMPLE", " "})) != 2 || len(serverDomains(sites)) != 4 || len(serverClientEmails(clients)) != 2 {
		t.Fatal("server domain or email aggregation failed")
	}
	if invoice := InvoiceLabelForDomain(nil, " "); invoice != "счёт не нужен" || InvoiceLabelForDomain(nil, "example.com") != "можно выставить счёт" || InvoiceLabelForDomain([]Invoice{{Domain: "example.com", Number: "INV-1", Status: "paid"}}, "EXAMPLE.COM") != "INV-1 · оплачен" {
		t.Fatalf("invoice label = %q", invoice)
	}
	if BillableSiteCount(sites) != 0 || UnpaidInvoiceCount([]ServerInvoiceView{{StatusLabel: "ожидает оплаты"}, {StatusLabel: "ошибка оплаты"}}) != 2 {
		t.Fatal("billing count aggregation failed")
	}
	label, class := InvoiceAction(0, 0)
	if label != "Не выставлять счёт" || class != "btn-outline-secondary" {
		t.Fatal("empty invoice action is incorrect")
	}
	label, class = InvoiceAction(1, 1)
	if label != "Проверить счета" || class != "btn-outline-primary" {
		t.Fatal("unpaid invoice action is incorrect")
	}
	label, class = InvoiceAction(1, 0)
	if label != "Выставить счёт" || class != "btn-success" {
		t.Fatal("invoice action selection failed")
	}
	if actions := overviewActions(OverviewView{}); len(actions) != 1 || actions[0].Level != "ok" {
		t.Fatalf("empty overview actions = %#v", actions)
	}
	if actions := overviewActions(OverviewView{PendingRequests: 1, UnpaidInvoices: 2, OverLimitSites: 3, StaleServers: 4, MailErrors: 5}); len(actions) != 5 {
		t.Fatalf("problem overview actions = %#v", actions)
	}
	server := &ServerView{Clients: []ServerClientView{{Email: "owner@example.com"}}, Sites: []ServerSiteView{{Domain: "free.example", PlanName: "Free"}, {Domain: "paid.example", OwnerEmail: "other@example.com", PlanName: "Paid", BillingBillable: true, BillingAmount: "12.00", BillingCurrency: "USD"}}}
	applyServerInvoiceDefaults(server)
	if server.DefaultInvoiceCurrency != "USD" || server.DefaultInvoiceAmount != "12.00" || server.DefaultInvoiceDomain != "paid.example" || server.DefaultInvoiceClient != "other@example.com" || server.DefaultInvoicePlan != "Paid" {
		t.Fatalf("server invoice defaults = %#v", server)
	}
	applyServerInvoiceDefaults(nil)
}
