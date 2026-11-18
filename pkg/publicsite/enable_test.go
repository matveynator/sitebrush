package publicsite

import (
	"testing"
	"time"
)

func TestPolicyEnableSharesPreparationAndRejectsRestart(t *testing.T) {
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
	for range 1 {
		go func() { replies <- enable("127.0.0.1:40003") }()
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("preparation did not start")
	}
	if err := front.Set("127.0.0.1:40003", false); err != nil {
		t.Fatal(err)
	}
	close(finish)
	for range 1 {
		select {
		case err := <-replies:
			if err == nil {
				t.Fatal("old preparation enabled restarted publisher")
			}
		case <-time.After(time.Second):
			t.Fatal("enable did not finish")
		}
	}
	state, err := front.Current()
	if err != nil || state.PolicyReady {
		t.Fatalf("state=%+v error=%v", state, err)
	}
}
