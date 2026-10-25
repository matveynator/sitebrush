package appupdate

import (
	"errors"
	"testing"
	"time"
)

func TestNegotiatePublicThrottleRequiresExplicitOwnership(t *testing.T) {
	tests := []struct {
		name          string
		response      Response
		exchangeError error
		owned         bool
		wantError     bool
	}{
		{name: "owned by launcher", response: Response{Detail: "1"}, owned: true},
		{name: "explicitly unsupported", response: Response{Detail: "0"}},
		{name: "legacy launcher", response: Response{Error: "unknown update command"}, exchangeError: errors.New("unknown update command")},
		{name: "failed exchange", exchangeError: errors.New("connection closed"), wantError: true},
		{name: "failed exchange with unsupported detail", response: Response{Detail: "0"}, exchangeError: errors.New("connection closed"), wantError: true},
		{name: "transport error mimics legacy reply", exchangeError: errors.New("unknown update command"), wantError: true},
		{name: "missing capability", wantError: true},
		{name: "unknown capability", response: Response{Detail: "2"}, wantError: true},
		{name: "launcher failure", response: Response{Error: "security unavailable"}, exchangeError: errors.New("security unavailable"), wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			owned, err := NegotiatePublicThrottle(func(request Request) (Response, error) {
				calls++
				if request.Action != "public_throttle_capabilities" {
					t.Fatalf("unexpected capability request: %s", request.Action)
				}
				return test.response, test.exchangeError
			})
			if calls != 1 || owned != test.owned || (err != nil) != test.wantError {
				t.Fatalf("calls=%d owned=%t error=%v", calls, owned, err)
			}
		})
	}
}

func TestObservationContactAndPolicyRelay(t *testing.T) {
	root := newFixtureInstallation(t)
	startFixtureSupervisor(t, root, "success", func(options *Options) {
		options.PublicPolicy = func(encoded []byte) ([]byte, error) { return encoded, nil }
		options.PublicObservations = func(encoded []byte) ([]byte, error) { return encoded, nil }
	})
	stop := make(chan struct{})
	requests := make(chan Request, 1)
	replies := make(chan Response, 1)
	done := make(chan struct{})
	go func() { defer close(done); ObservationContact(root, requests, stop) }()
	defer func() { close(stop); <-done }()
	for _, detail := range []string{"first", "acknowledged"} {
		requests <- Request{Action: "public_observations", Detail: detail, Reply: replies}
		select {
		case response := <-replies:
			if response.Error != "" || response.Detail != detail {
				t.Fatalf("observation response: %+v", response)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("observation contact stalled")
		}
	}
	response, err := Exchange(root, Request{Action: "public_policy", Detail: "barrier"}, stop)
	if err != nil || response.Detail != "barrier" {
		t.Fatal("policy relay", response, err)
	}
}

func TestPublicBoundaryCapabilityRejectsUnknownAndCorruptChildren(t *testing.T) {
	root := t.TempDir()
	if !PublicBoundarySupported(root, "new") {
		t.Fatal("fresh installation rejected")
	}
	store, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := atomicJSON(store, "state.json", State{Current: Version{Version: "old", Binary: "versions/old/sitebrush", SHA256: "fixture"}}); err != nil {
		t.Fatal(err)
	}
	if PublicBoundarySupported(root, "new") {
		t.Fatal("unconfirmed old child admitted")
	}
	if !PublicBoundarySupported(root, "old") {
		t.Fatal("current child rejected")
	}
	for _, invalid := range []string{"", "..", "../escape"} {
		if ReportPublicBoundary(root, invalid) == nil {
			t.Fatal("invalid capability version admitted", invalid)
		}
	}
	if err := ReportPublicBoundary(root, "old"); err != nil {
		t.Fatal(err)
	}
	if !PublicBoundarySupported(root, "new") {
		t.Fatal("confirmed old child rejected")
	}
	if err := store.WriteFile("state.json", []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if PublicBoundarySupported(root, "new") {
		t.Fatal("corrupt child state admitted")
	}
}

func TestSecurityContactKeepsTheNegotiatedSession(t *testing.T) {
	root := newFixtureInstallation(t)
	startFixtureSupervisor(t, root, "success", func(options *Options) {
		options.PublicSecurity = func(encoded []byte) ([]byte, error) { return encoded, nil }
		options.PublicThrottle = func(encoded []byte) ([]byte, error) { return encoded, nil }
	})
	stop := make(chan struct{})
	defer close(stop)
	capabilities, err := Exchange(root, Request{Action: "public_throttle_capabilities"}, stop)
	if err != nil || capabilities.Detail != "1" {
		t.Fatal("new throttle ownership was not negotiated", capabilities, err)
	}
	exchange := NewSecurityContact(root, stop)
	for _, action := range []string{"public_security", "public_throttle", "public_security", "public_throttle"} {
		response, exchangeErr := exchange(Request{Action: action, Detail: action})
		if exchangeErr != nil || response.Detail != action {
			t.Fatal("persistent security session lost a reply", response, exchangeErr)
		}
	}
	if _, err := exchange(Request{Action: "stop"}); err == nil {
		t.Fatal("security contact admitted an unrelated lifecycle operation")
	}
}

func TestPublicThrottleCapabilityReportsUnsupported(t *testing.T) {
	root := newFixtureInstallation(t)
	startFixtureSupervisor(t, root, "success")
	stop := make(chan struct{})
	defer close(stop)
	owned, err := NegotiatePublicThrottle(func(request Request) (Response, error) {
		return Exchange(root, request, stop)
	})
	if err != nil || owned {
		t.Fatal("launcher without public throttle did not explicitly reject ownership", owned, err)
	}
}

func TestThrottleOwnershipRequiresTheSelectedChildCapability(t *testing.T) {
	root := t.TempDir()
	store, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := atomicJSON(store, "state.json", State{Current: Version{Version: "old", Binary: "versions/old/sitebrush", SHA256: "fixture"}}); err != nil {
		t.Fatal(err)
	}
	if err := ReportPublicBoundary(root, "old"); err != nil {
		t.Fatal(err)
	}
	if PublicThrottleSupported(root, "new") {
		t.Fatal("a second writer was admitted for an older selected child")
	}
	if err := ReportPublicThrottle(root, "old"); err != nil {
		t.Fatal(err)
	}
	if !PublicThrottleSupported(root, "new") {
		t.Fatal("the confirmed child could not transfer throttle ownership")
	}
	for _, version := range []string{"", "..", "../escape"} {
		if ReportPublicThrottle(root, version) == nil {
			t.Fatal("invalid throttle capability admitted", version)
		}
	}
}
