package hostingandsupport

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestOwnerPlanAndSiteRequestLifecycle(t *testing.T) {
	store := newLifecycleStore(t)
	requestContext := context.Background()

	if !store.AutomaticRegistrationAllowed(requestContext) {
		t.Fatal("registration should be allowed before an owner exists")
	}
	if err := store.SetOwner(requestContext, "example.com", "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	if !store.OwnerExists(requestContext) || !store.IsOwner(requestContext, "owner@example.com") {
		t.Fatal("saved owner was not found")
	}
	if domain, found := store.OwnerDomain(requestContext); !found || domain != "example.com" {
		t.Fatalf("owner domain = %q, found = %v", domain, found)
	}
	if emails := store.OwnerEmails(requestContext); len(emails) != 1 || emails[0] != "owner@example.com" {
		t.Fatalf("owner emails = %#v", emails)
	}
	store.PromoteOwnerIfMissing(requestContext, "ignored.example", "ignored@example.com")
	if store.IsOwner(requestContext, "ignored@example.com") {
		t.Fatal("an existing owner must not be replaced")
	}

	if err := store.SaveRegistrationSettings(requestContext, true, true); err != nil {
		t.Fatal(err)
	}
	if !store.AutomaticRegistrationAllowed(requestContext) || !store.PublicTrialAllowed(requestContext) {
		t.Fatal("registration settings were not saved")
	}
	if err := store.SaveSettings(requestContext, false); err != nil {
		t.Fatal(err)
	}
	if store.AutomaticRegistrationAllowed(requestContext) || store.PublicTrialAllowed(requestContext) {
		t.Fatal("disabled registration must also disable public trials")
	}
	if err := store.SaveSitebrushCommissionBPS(requestContext, 750); err != nil {
		t.Fatal(err)
	}
	if commission := store.SitebrushCommissionBPS(requestContext); commission != 750 {
		t.Fatalf("commission = %d, want 750", commission)
	}
	if err := store.SaveSitebrushCommissionBPS(requestContext, 10001); err == nil {
		t.Fatal("invalid commission was accepted")
	}

	if err := store.SavePlan(requestContext, 0, "Professional", 20*1024*1024*1024, 5, 30, "19.00", "EUR", "monthly", true); err != nil {
		t.Fatal(err)
	}
	plans := store.Plans(requestContext)
	if len(plans) < 2 || !plans[0].IsDefault {
		t.Fatalf("plans = %#v", plans)
	}
	plan, found := store.PlanByID(requestContext, plans[0].ID)
	if !found || plan.Name != "Professional" {
		t.Fatalf("plan = %#v, found = %v", plan, found)
	}
	if defaultPlan, found := store.DefaultPlan(requestContext); !found || defaultPlan.ID != plan.ID {
		t.Fatalf("default plan = %#v, found = %v", defaultPlan, found)
	}
	if err := store.AssignSite(requestContext, "client.example.com", plan.ID, "active"); err != nil {
		t.Fatal(err)
	}
	assignments := store.ServiceAssignments(requestContext)
	if assignments["client.example.com"].PlanID != plan.ID {
		t.Fatalf("assignments = %#v", assignments)
	}

	if err := store.CreateSiteRequest(requestContext, "new.example.com", "Client", "client@example.com", "+1 555 0100", plan.ID); err != nil {
		t.Fatal(err)
	}
	requests := store.SiteRequests(requestContext)
	if len(requests) != 1 || requests[0].PlanName != "Professional" || !requests[0].CanApproveOrReject {
		t.Fatalf("requests = %#v", requests)
	}
	request, found := store.SiteRequestByID(requestContext, requests[0].ID)
	if !found || request.Domain != "new.example.com" {
		t.Fatalf("request = %#v, found = %v", request, found)
	}
	if err := store.UpdateSiteRequestStatus(requestContext, request.ID, "approved", "Welcome"); err != nil {
		t.Fatal(err)
	}
	request, found = store.SiteRequestByID(requestContext, request.ID)
	if !found || request.Status != "approved" || request.CanApproveOrReject {
		t.Fatalf("updated request = %#v, found = %v", request, found)
	}

	store.RemoveSiteAssignment(requestContext, "client.example.com")
	if len(store.ServiceAssignments(requestContext)) != 0 {
		t.Fatal("site assignment was not removed")
	}
	if err := store.DeletePlan(requestContext, plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, found := store.PlanByID(requestContext, plan.ID); found {
		t.Fatal("plan was not deleted")
	}
}

func TestServiceMailAndKeyLifecycle(t *testing.T) {
	store := newLifecycleStore(t)
	requestContext := context.Background()

	if err := store.UpsertServiceMailInstallation(requestContext, "installation-1", "public-key", "192.0.2.44", "sender.example"); err != nil {
		t.Fatal(err)
	}
	if publicKey, found := store.ServiceMailInstallationPublicKey(requestContext, "installation-1"); !found || publicKey != "public-key" {
		t.Fatalf("public key = %q, found = %v", publicKey, found)
	}
	if _, found := store.ServiceMailInstallationFirstSeenAt(requestContext, "installation-1"); !found {
		t.Fatal("first-seen timestamp was not recorded")
	}
	if err := store.SetServiceMailInstallationBlocked(requestContext, "installation-1", true); err != nil {
		t.Fatal(err)
	}
	if !store.ServiceMailInstallationBlocked(requestContext, "installation-1") {
		t.Fatal("installation was not blocked")
	}

	if err := store.CreateServiceMailBlock(requestContext, "subnet", "192.0.2.0/24", "abuse"); err != nil {
		t.Fatal(err)
	}
	if reason, blocked := store.ServiceMailBlocked(requestContext, "another-installation", "192.0.2.9", "person@example.net", "example.net"); !blocked || reason == "" {
		t.Fatalf("block reason = %q, blocked = %v", reason, blocked)
	}
	blocks := store.ServiceMailBlocks(requestContext)
	if len(blocks) != 1 {
		t.Fatalf("blocks = %#v", blocks)
	}
	if err := store.DeleteServiceMailBlock(requestContext, blocks[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, blocked := store.ServiceMailBlocked(requestContext, "another-installation", "192.0.2.9", "person@example.net", "example.net"); blocked {
		t.Fatal("deleted block is still active")
	}

	if err := store.UpsertServiceMailRecipient(requestContext, "installation-1", "Person@Example.net", "verified", "transactional"); err != nil {
		t.Fatal(err)
	}
	if !store.ServiceMailRecipientVerified(requestContext, "installation-1", "person@example.net") {
		t.Fatal("recipient was not verified")
	}
	if count := store.CountServiceMailVerifiedRecipients(requestContext, "installation-1"); count != 1 {
		t.Fatalf("verified recipients = %d, want 1", count)
	}
	if count := store.CountServiceMailRecipientsSince(requestContext, "installation-1", time.Now().Add(-time.Hour)); count != 1 {
		t.Fatalf("recent recipients = %d, want 1", count)
	}

	savedKey, err := store.SaveSitebrushComKey(requestContext, "sitebrush-public-key", "/secure/sitebrush.key")
	if err != nil {
		t.Fatal(err)
	}
	if savedKey.Fingerprint != FingerprintPublicKey("sitebrush-public-key") {
		t.Fatalf("fingerprint = %q", savedKey.Fingerprint)
	}
	if loadedKey := store.SitebrushComKey(requestContext); loadedKey.PrivateKeyPath != "/secure/sitebrush.key" {
		t.Fatalf("loaded key = %#v", loadedKey)
	}
}

func TestHostingAndSupportViewHelpers(t *testing.T) {
	if MoneyMinor("12.34") != 1234 || MoneyAmount(1234) != "12.34" {
		t.Fatal("money conversion failed")
	}
	if label := MoneyTotalsLabel(map[string]int64{"USD": 200, "EUR": 100}); label != "1.00 EUR + 2.00 USD" {
		t.Fatalf("money totals = %q", label)
	}
	if MetricStatusClass(9, 5, 8) != "hosting-metric-danger" || LoadAverageStatusClass(2, 2) != "hosting-metric-warning" {
		t.Fatal("metric status classification failed")
	}
	if ServerUptimeStatusClass(2*86400) != "hosting-metric-ok" || FormatDurationDays(366*86400) != "1 г 1 д" {
		t.Fatal("duration formatting failed")
	}
	if subnet := ServiceMailIPv4Subnet("192.0.2.44"); subnet != "192.0.2.0/24" {
		t.Fatalf("subnet = %q", subnet)
	}
	if ServiceMailIPv4Subnet("not-an-ip") != "" {
		t.Fatal("invalid IP address produced a subnet")
	}
	if ServiceMailRecipientHash("person@example.net") == "" || MaskServiceMailRecipient("person@example.net") != "p***n@example.net" {
		t.Fatal("recipient privacy helpers failed")
	}

	policy := ServerCostPolicy{MonthlyCostMinor: 101, MinimumPriceGBMinor: 100, Currency: "EUR"}
	amounts := AllocateServerCost(policy, []ServerCostSite{
		{Key: "first", UsedBytes: 2 * 1024 * 1024 * 1024},
		{Key: "second", UsedBytes: 1024 * 1024 * 1024},
		{Key: "excluded", UsedBytes: 1024 * 1024 * 1024, Excluded: true},
	})
	if amounts["first"].CostShareMinor+amounts["second"].CostShareMinor != 101 {
		t.Fatalf("allocated amounts = %#v", amounts)
	}
	if amounts["excluded"].CostShareMinor != 0 {
		t.Fatalf("excluded site received a cost share: %#v", amounts["excluded"])
	}

	sites := BuildSitesWithDemoDomain([]SiteUsage{{
		Domain:      "demo.example",
		UsedBytes:   512 * 1024 * 1024,
		LimitBytes:  1024 * 1024 * 1024,
		AdminEmails: []string{"admin@example.com"},
	}}, []Plan{{ID: 4, Name: "Starter", QuotaLabel: "1 GB"}}, map[string]ServiceAssignment{
		"demo.example": {PlanID: 4, ServiceStatus: "active"},
	}, "current.example", "demo.example")
	if len(sites) != 1 || !sites[0].IsDemo || sites[0].CanDelete || sites[0].PlanName != "Starter" || sites[0].UsedPercent != 50 {
		t.Fatalf("sites = %#v", sites)
	}
}

func newLifecycleStore(t *testing.T) Store {
	t.Helper()
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "hosting.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	return Store{DB: database}
}
