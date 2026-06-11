package billing

import "testing"

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
	if !sites[0].IsMainDomain {
		t.Fatalf("main domain site was not marked: %#v", sites[0])
	}
	if sites[1].IsMainDomain {
		t.Fatalf("client subdomain with second-level alias was marked as main: %#v", sites[1])
	}
	if sites[1].Aliases != "client.com" {
		t.Fatalf("client aliases = %q, want client.com", sites[1].Aliases)
	}
}
