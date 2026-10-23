package httpsecurity

import (
	"testing"
	"time"
)

func TestRemoteGuardUsesThePublicProcessForSettingsAndBlocks(t *testing.T) {
	owner, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	remote := NewRemoteAttackGuard(owner.Relay)
	defer remote.Close()
	now := time.Now().UTC()
	if _, err = remote.Add("192.0.2.17", "manual", "fixture", "manual", now.Add(time.Hour), now); err != nil {
		t.Fatal(err)
	}
	if _, blocked := owner.Check("192.0.2.17", now); !blocked {
		t.Fatal("remote addition did not reach the public security owner")
	}
	blocks, err := remote.Snapshot(now)
	if err != nil || len(blocks) != 1 {
		t.Fatal(blocks, err)
	}
	if err = remote.Remove("192.0.2.17"); err != nil {
		t.Fatal(err)
	}
	if _, blocked := owner.Check("192.0.2.17", now); blocked {
		t.Fatal("remote unblock left public traffic blocked")
	}
	settings := SecuritySettings{AutoBlock: false, GlobalSync: false}
	if err = remote.SetSettings(settings); err != nil {
		t.Fatal(err)
	}
	actual, err := owner.Settings()
	if err != nil || actual != settings {
		t.Fatal(actual, err)
	}
	if err = remote.Allow("192.0.2.18", "fixture"); err != nil {
		t.Fatal(err)
	}
	allowed, err := remote.Allowlist()
	if err != nil || len(allowed) != 1 {
		t.Fatal(allowed, err)
	}
}
