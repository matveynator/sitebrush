package securitysync

import (
	"path/filepath"
	"testing"
	"time"
)

func TestReputationRequiresThreeIndependentInstallations(t *testing.T) {
	now := time.Now().UTC()
	records := map[string]map[string]evidenceSource{}
	for index, installationID := range []string{"one", "two"} {
		accepted, err := submit(records, Signal{
			IP:             "203.0.113.20",
			Category:       "injection",
			InstallationID: installationID,
			ObservedAt:     now.Add(time.Duration(index) * time.Second),
		}, now)
		if err != nil || !accepted {
			t.Fatalf("submit %s: accepted=%v err=%v", installationID, accepted, err)
		}
	}
	if entries := approved(records, now); len(entries) != 0 {
		t.Fatalf("two sources unexpectedly produced global reputation: %#v", entries)
	}
	accepted, err := submit(records, Signal{
		IP:             "203.0.113.20",
		Category:       "scanner-client",
		InstallationID: "three",
		ObservedAt:     now,
	}, now)
	if err != nil || !accepted {
		t.Fatalf("third submit: accepted=%v err=%v", accepted, err)
	}
	entries := approved(records, now)
	if len(entries) != 1 || entries[0].Confirmations != 3 {
		t.Fatalf("expected quorum entry, got %#v", entries)
	}
}

func TestReputationDoesNotCountOneInstallationMultipleTimes(t *testing.T) {
	now := time.Now().UTC()
	records := map[string]map[string]evidenceSource{}
	for index := 0; index < 5; index++ {
		accepted, err := submit(records, Signal{
			IP:             "198.51.100.15",
			Category:       "traversal",
			InstallationID: "same-installation",
			ObservedAt:     now.Add(time.Duration(index) * time.Second),
		}, now.Add(time.Duration(index)*time.Second))
		if err != nil || !accepted {
			t.Fatalf("submit %d: accepted=%v err=%v", index, accepted, err)
		}
	}
	if entries := approved(records, now.Add(10*time.Second)); len(entries) != 0 {
		t.Fatalf("one installation poisoned quorum: %#v", entries)
	}
}

func TestReputationRejectsPrivateAddressesAndWeakCategories(t *testing.T) {
	now := time.Now().UTC()
	records := map[string]map[string]evidenceSource{}
	tests := []Signal{
		{IP: "127.0.0.1", Category: "injection", InstallationID: "one", ObservedAt: now},
		{IP: "10.0.0.1", Category: "injection", InstallationID: "one", ObservedAt: now},
		{IP: "203.0.113.8", Category: "rapid-crawl", InstallationID: "one", ObservedAt: now},
	}
	for _, signal := range tests {
		if accepted, err := submit(records, signal, now); err == nil || accepted {
			t.Fatalf("unsafe signal accepted: %#v", signal)
		}
	}
}

func TestReputationPersistsEvidence(t *testing.T) {
	now := time.Now().UTC()
	path := filepath.Join(t.TempDir(), "reputation.json")
	records := map[string]map[string]evidenceSource{}
	for _, installationID := range []string{"one", "two", "three"} {
		_, err := submit(records, Signal{
			IP:             "192.0.2.44",
			Category:       "repository",
			InstallationID: installationID,
			ObservedAt:     now,
		}, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := save(path, records, now); err != nil {
		t.Fatal(err)
	}
	loaded, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := approved(loaded, now)
	if len(entries) != 1 || entries[0].IP != "192.0.2.44" {
		t.Fatalf("persisted reputation missing: %#v", entries)
	}
}
