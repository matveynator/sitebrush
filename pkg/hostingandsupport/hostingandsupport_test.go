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
