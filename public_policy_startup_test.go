package main

import (
	"testing"

	"github.com/matveynator/sitebrush/v2/pkg/publicsite"
)

// Startup cannot require a supervisor acknowledgement for each policy before
// the supervisor's public front has been enabled. The final enable must still
// be acknowledged before dynamic public requests can be served.
func TestInitialPublicPolicySnapshotDoesNotRequireNetChanContact(t *testing.T) {
	root := t.TempDir()
	stop := make(chan struct{})
	defer close(stop)
	if err := publicsite.Write(root, publicsite.Policy{Domain: "example.test"}); err != nil {
		t.Fatal(err)
	}
	app := &App{storagePath: root, publicCatalog: publicsite.NewCatalog(root, stop), publicPolicyContact: true}
	app.publicPolicyBootstrap.Store(true)
	if err := app.notifyPublicPolicy("refresh", "example.test"); err != nil {
		t.Fatalf("initial snapshot must be local: %v", err)
	}
	if err := app.notifyPublicPolicy("open", "example.test"); err != nil {
		t.Fatalf("initial policy open must be local: %v", err)
	}
	if decision := app.publicCatalog.Lookup(publicsite.Lookup{Host: "example.test"}); !decision.Found {
		t.Fatal("local initial policy must be readable")
	}
	// Once the bootstrap phase finishes, failed supervisor contacts must
	// still fail closed; a missing installation is never treated as success.
	app.publicPolicyBootstrap.Store(false)
	if err := app.notifyPublicPolicy("refresh", "example.test"); err == nil {
		t.Fatal("live policy refresh must require a supervisor acknowledgement")
	}
}
