package hostingandsupport

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestHostingSecurityPublicServerClassificationBoundaries(t *testing.T) {
	tests := []struct {
		name string
		host ClientHosting
		want bool
	}{
		{"public", ClientHosting{ServerDomain: "node.example.com", ServerIP: "8.8.8.8"}, true},
		{"missing ip", ClientHosting{ServerDomain: "node.example.com"}, false},
		{"invalid ip", ClientHosting{ServerDomain: "node.example.com", ServerIP: "not-an-ip"}, false},
		{"loopback", ClientHosting{ServerDomain: "node.example.com", ServerIP: "127.0.0.1"}, false},
		{"private", ClientHosting{ServerDomain: "node.example.com", ServerIP: "10.0.0.1"}, false},
		{"unspecified", ClientHosting{ServerDomain: "node.example.com", ServerIP: "0.0.0.0"}, false},
		{"missing domain", ClientHosting{ServerIP: "8.8.8.8"}, false},
		{"localhost", ClientHosting{ServerDomain: "localhost", ServerIP: "8.8.8.8"}, false},
		{"localhost subdomain", ClientHosting{ServerDomain: "x.localhost", ServerIP: "8.8.8.8"}, false},
		{"ip domain", ClientHosting{ServerDomain: "8.8.4.4", ServerIP: "8.8.8.8"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClientHostingLooksPublic(tc.host); got != tc.want {
				t.Fatalf("ClientHostingLooksPublic(%#v)=%v want %v", tc.host, got, tc.want)
			}
			if got := ClientHostingIsRealServer(tc.host, nil); got != tc.want {
				t.Fatalf("ClientHostingIsRealServer(%#v)=%v want %v", tc.host, got, tc.want)
			}
		})
	}

	public := ClientHosting{ServerDomain: "node.example.com", ServerIP: "8.8.8.8"}
	if ClientHostingIsRealServer(public, func(domain, ip string) bool {
		return domain == "other.example.com" || ip == "1.1.1.1"
	}) {
		t.Fatal("SECURITY: public installation was accepted when domain/IP verification failed")
	}
	if !ClientHostingIsRealServer(public, func(domain, ip string) bool {
		return domain == "node.example.com" && ip == "8.8.8.8"
	}) {
		t.Fatal("verified public installation was rejected")
	}

	if got := RealClientHostings([]ClientHosting{
		public,
		{ServerDomain: "local.example.com", ServerIP: "10.0.0.1"},
	}, nil); len(got) != 1 || got[0].ServerDomain != "node.example.com" {
		t.Fatalf("real hostings=%#v", got)
	}
}

func TestHostingDetailsLoadTLSNetworkAndDomainSecurityChecks(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "hosting-details.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	if err := Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	store := Store{DB: database}
	now := time.Now().UTC().Truncate(time.Second)

	snapshot := HostingSnapshot{
		Version:        2,
		InstallationID: "installation-security",
		ServerDomain:   "node.example.com",
		ServerIP:       "8.8.8.8",
		OwnerEmail:     "owner@example.com",
		Sites: []HostingSnapshotSite{{
			Domain:      "site.example.com",
			OwnerEmail:  "owner@example.com",
			AdminEmails: []string{"admin@example.com"},
		}},
	}
	if err := store.SaveHostingSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveClientHostingDomainCheck(ctx, ClientHostingDomainCheck{
		InstallationID: "installation-security",
		Domain:         "site.example.com",
		ServerIP:       "8.8.8.8",
		DNSMatches:     true,
		Reachable:      true,
		Scheme:         "https",
		ResponseMS:     42,
		CheckedAt:      now.Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSiteTLSCheck(ctx, SiteTLSCheck{
		InstallationID: "installation-security",
		Domain:         "site.example.com",
		HTTPSAvailable: true,
		CertExpiresAt:  now.Add(30 * 24 * time.Hour).Format(time.RFC3339),
		CertDaysLeft:   30,
		StatusClass:    "hosting-metric-ok",
	}); err != nil {
		t.Fatal(err)
	}
	for index, success := range []bool{true, true, false} {
		if err := store.LogServerNetworkCheck(
			ctx,
			"installation-security",
			"node.example.com",
			"8.8.8.8",
			success,
			20+index,
			"",
		); err != nil {
			t.Fatal(err)
		}
	}

	hostings := store.ClientHostings(ctx)
	if len(hostings) != 1 {
		t.Fatalf("hostings=%#v", hostings)
	}
	hosting := hostings[0]
	if len(hosting.Sites) != 1 {
		t.Fatalf("sites=%#v", hosting.Sites)
	}
	site := hosting.Sites[0]
	if !site.DNSMatchesServer || !site.ReachableByServer || site.ReachabilityScheme != "https" {
		t.Fatalf("domain security check not loaded: %#v", site)
	}
	if !site.HTTPSAvailable || site.CertDaysLeft != 30 || site.TLSStatusClass != "hosting-metric-ok" {
		t.Fatalf("TLS security check not loaded: %#v", site)
	}
	if hosting.NetworkUptimePercent <= 0 || hosting.LastResponseMS == 0 {
		t.Fatalf("network summary not loaded: %#v", hosting)
	}

	// A check for an old/different server IP must not be trusted for the current
	// installation address.
	if err := store.SaveClientHostingDomainCheck(ctx, ClientHostingDomainCheck{
		InstallationID: "installation-security",
		Domain:         "site.example.com",
		ServerIP:       "1.1.1.1",
		DNSMatches:     true,
		Reachable:      true,
		CheckedAt:      now.Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	hostings = store.ClientHostings(ctx)
	if hostings[0].Sites[0].DNSMatchesServer || hostings[0].Sites[0].ReachableByServer {
		t.Fatal("SECURITY: reachability result for a different server IP was trusted")
	}
}

func TestHostingViewAndLocalDevelopmentEdgeBranches(t *testing.T) {
	for _, tc := range []struct {
		domain string
		ip     string
		want   bool
	}{
		{"", "8.8.8.8", true},
		{"example.com", "", true},
		{"localhost", "8.8.8.8", true},
		{"dev.localhost", "8.8.8.8", true},
		{"127.0.0.1", "8.8.8.8", true},
		{"example.com", "bad", true},
		{"example.com", "127.0.0.1", true},
		{"example.com", "10.0.0.1", true},
		{"example.com", "8.8.8.8", false},
	} {
		if got := DomainIsLocalDevelopment(tc.domain, tc.ip); got != tc.want {
			t.Fatalf("DomainIsLocalDevelopment(%q,%q)=%v want %v", tc.domain, tc.ip, got, tc.want)
		}
	}

	local := ServerView{
		Name: "local.example.com",
		Sites: []ServerSiteView{{Domain: "local.example.com"}},
		SiteCount: 1,
	}
	remote := ClientHosting{
		InstallationID: "remote-z",
		ServerDomain:   "z.example.com",
		ServerIP:       "8.8.8.8",
		SiteCount:      2,
	}
	servers := BuildServerViews(local, []ClientHosting{remote}, nil, "v1")
	if len(servers) != 2 || servers[1].Name != "z.example.com" {
		t.Fatalf("server views=%#v", servers)
	}
}
