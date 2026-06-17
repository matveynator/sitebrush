package hostingandsupport

import (
	"context"
	"database/sql"
	"path/filepath"
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
	snapshot := HostingSnapshot{
		Version:          1,
		InstallationID:   "installation-1",
		OwnerEmail:       "owner@example.com",
		ServerIP:         "203.0.113.10",
		ServerStatus:     "IP 203.0.113.10",
		ServerDomain:     "host.example.com",
		SitebrushVersion: "v1",
		OSName:           "linux",
		OSVersion:        "Debian",
		CPUModel:         "amd64",
		CPUCores:         4,
		RAMTotalBytes:    8 * 1024 * 1024 * 1024,
		DiskFreeBytes:    20 * 1024 * 1024 * 1024,
		DiskTotalBytes:   40 * 1024 * 1024 * 1024,
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
			CreatedAt: "2026-06-17T10:00:00Z",
		}},
		CreatedAt: "2026-06-17T10:00:00Z",
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
}
