package serverip

import (
	"errors"
	"net"
	"reflect"
	"testing"
)

func TestEnumerateAliasesAndPartialInterfaceFailure(t *testing.T) {
	interfaces := []net.Interface{{Name: "em0", Flags: net.FlagUp}, {Name: "broken", Flags: net.FlagUp}, {Name: "down"}}
	addresses, failures := enumerateInterfaces(interfaces, func(networkInterface net.Interface) ([]net.Addr, error) {
		if networkInterface.Name == "broken" {
			return nil, errors.New("unavailable")
		}
		if networkInterface.Name == "down" {
			return []net.Addr{&net.IPAddr{IP: net.ParseIP("8.8.8.8")}}, nil
		}
		return []net.Addr{&net.IPNet{IP: net.ParseIP("195.201.87.33"), Mask: net.CIDRMask(26, 32)}, &net.IPNet{IP: net.ParseIP("46.4.4.46"), Mask: net.CIDRMask(32, 32)}, &net.IPAddr{IP: net.ParseIP("2a01:4f8:13b:4224::2")}, &net.IPAddr{IP: net.ParseIP("10.0.0.2")}}, nil
	})
	snapshot := Snapshot{Addresses: addresses}
	candidates := snapshot.Candidates()
	if len(failures) != 1 || len(addresses) != 5 || len(candidates) != 3 {
		t.Fatalf("addresses=%v candidates=%v failures=%v", addresses, candidates, failures)
	}
	if primary := snapshot.Primary([]net.IP{net.ParseIP("46.4.4.46")}); primary != "46.4.4.46" {
		t.Fatalf("DNS alias primary=%s", primary)
	}
}

func TestNormalizeRejectsNonPublicAndNonAddressInput(t *testing.T) {
	for _, text := range []string{"127.0.0.1", "10.0.0.1", "100.64.0.1", "192.0.2.1", "169.254.169.254", "::1", "fc00::1", "fe80::1%em0", "http://46.4.4.46", "46.4.4.46:80", "46.4.4.46/32"} {
		if _, err := Normalize(Settings{Manual: []string{text}}); err == nil {
			t.Errorf("accepted %q", text)
		}
	}
	normalized, err := Normalize(Settings{Manual: []string{" 46.4.4.46 ", "::ffff:46.4.4.46", "2a01:4f8:13b:4224::2"}})
	if err != nil || len(normalized.Manual) != 2 {
		t.Fatalf("normalized=%+v err=%v", normalized, err)
	}
}

func TestManualNATPersistenceFailureAndSnapshotIsolation(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	persisted := make(chan Settings, 1)
	process := Start(Settings{}, func(settings Settings) error { persisted <- settings; return nil }, stop)
	wanted := Settings{Manual: []string{"46.4.4.46"}, Preferred: "46.4.4.46"}
	snapshot, err := Exchange(process, Request{Action: "save", Settings: wanted}, nil)
	if err != nil || snapshot.Primary(nil) != "46.4.4.46" {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	saved := <-persisted
	if !reflect.DeepEqual(saved, wanted) {
		t.Fatalf("saved=%+v", saved)
	}
	snapshot.Settings.Manual[0] = "1.1.1.1"
	snapshot.Addresses[0].IP = "1.1.1.1"
	again, err := Exchange(process, Request{}, nil)
	if err != nil || again.Settings.Manual[0] != "46.4.4.46" {
		t.Fatalf("snapshot ownership lost: %+v %v", again, err)
	}
	restarted := Start(saved, func(Settings) error { return errors.New("disk full") }, stop)
	failed, err := Exchange(restarted, Request{Action: "save", Settings: Settings{Manual: []string{"1.1.1.1"}, Preferred: "1.1.1.1"}}, nil)
	if err == nil || failed.Primary(nil) != "46.4.4.46" {
		t.Fatalf("failed save changed settings: %+v err=%v", failed, err)
	}
	canceled := make(chan struct{})
	close(canceled)
	if _, err := Exchange(process, Request{Action: "save", Settings: Settings{}}, canceled); err == nil {
		t.Fatal("canceled save accepted")
	}
}

func TestDisappearedPreferredAddressDoesNotBecomeManual(t *testing.T) {
	snapshot := Snapshot{Settings: Settings{Preferred: "46.4.4.46"}, Addresses: []Address{{IP: "195.201.87.33", Active: true, Public: true, Source: "interface"}}}
	if snapshot.Primary(nil) != "" || len(snapshot.Settings.Manual) != 0 {
		t.Fatal("missing preferred address silently replaced")
	}
}
