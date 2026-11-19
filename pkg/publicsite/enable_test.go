package publicsite

import (
	"testing"
	"time"
)

func TestOwnershipStalePolicyEnableCannotRestorePreviousPublisher(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	front := NewFront("test-secret", func(http.ResponseWriter, *http.Request, bool) bool { return false }, stop)
	if err := front.Set("127.0.0.1:1111", false); err != nil {
		t.Fatal(err)
	}
	previous, err := front.Current()
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"127.0.0.1:2222", "127.0.0.1:1111"} {
		if err := front.Set(address, false); err != nil {
			t.Fatal(err)
		}
		if err := front.enablePolicy(previous); err == nil {
			t.Fatal("stale readiness confirmation was accepted")
		}
		current, currentErr := front.Current()
		if currentErr != nil || current.Address != address || current.PolicyReady {
			t.Fatalf("stale confirmation changed publisher state: %+v, %v", current, currentErr)
		}
	}
	if err := front.PolicyEnabled(); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyEnableRejectsSameAddressRestart(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	front := NewFront("secret", nil, stop)
	if err := front.Set("127.0.0.1:40003", false); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 1)
	finish := make(chan struct{})
	enable := NewPolicyEnabler(front, func() error { started <- struct{}{}; <-finish; return nil }, stop)
	replies := make(chan error, 1)
	go func() { replies <- enable("127.0.0.1:40003") }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("preparation did not start")
	}
	if err := front.Set("127.0.0.1:40003", false); err != nil {
		t.Fatal(err)
	}
	close(finish)
	select {
	case err := <-replies:
		if err == nil {
			t.Fatal("old preparation enabled restarted publisher")
		}
	case <-time.After(time.Second):
		t.Fatal("enable did not finish")
	}
	state, err := front.Current()
	if err != nil || state.PolicyReady {
		t.Fatalf("state=%+v error=%v", state, err)
	}
}

func TestPolicyEnableConcurrentRequestsPrepareOnce(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	front := NewFront("secret", nil, stop)
	if err := front.Set("publisher", false); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 16)
	finish := make(chan struct{})
	enable := NewPolicyEnabler(front, func() error { started <- struct{}{}; <-finish; return nil }, stop)
	replies := make(chan error, 16)
	for range 16 {
		go func() { replies <- enable("publisher") }()
	}
	<-started
	close(finish)
	for range 16 {
		select {
		case err := <-replies:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("enable did not finish")
		}
	}
	if len(started) != 0 {
		t.Fatal("concurrent requests repeated preparation")
	}
}
