package securitysync

import (
	"testing"
	"time"
)

func TestOwnershipAbandonedReplyReleasesSecurityOwner(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	requests := make(chan Request)
	done := make(chan struct{})
	go func() {
		defer close(done)
		run("", stop, requests, map[string]map[string]evidenceSource{})
	}()
	session := make(chan struct{})
	requests <- Request{Query: true, Reply: make(chan Result), Session: session}
	close(session)
	reply := make(chan Result, 1)
	select {
	case requests <- Request{Query: true, Reply: reply}:
	case <-time.After(time.Second):
		t.Fatal("abandoned reply blocked later security requests")
	}
	select {
	case result := <-reply:
		if result.Err != nil {
			t.Fatal(result.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("security owner did not reply after cancellation")
	}
}

func TestOwnershipCanceledSecuritySignalIsNotApplied(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	requests := make(chan Request, requiredSources+1)
	session := make(chan struct{})
	close(session)
	for _, source := range []string{"one", "two", "three"} {
		requests <- Request{Submit: true, Signal: Signal{IP: "203.0.113.77", Category: "injection", InstallationID: source, ObservedAt: time.Now().UTC()}, Session: session}
	}
	reply := make(chan Result, 1)
	requests <- Request{Query: true, Reply: reply}
	go run("", stop, requests, map[string]map[string]evidenceSource{})
	select {
	case result := <-reply:
		if len(result.Entries) != 0 || result.Err != nil {
			t.Fatalf("canceled signals changed reputation: %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("security owner did not drain canceled work")
	}
}

func TestSecurityBoundaryBlockedReplyCannotHangSubsystemShutdown(t *testing.T) {
	stop := make(chan struct{})
	requests := make(chan Request)
	done := make(chan struct{})

	go func() {
		run("", stop, requests, map[string]map[string]evidenceSource{})
		close(done)
	}()

	// No goroutine ever receives this reply. The worker must still be able to
	// terminate through stop instead of waiting forever on result delivery.
	blockedReply := make(chan Result)
	requestSent := make(chan struct{})
	go func() {
		requests <- Request{Query: true, Reply: blockedReply}
		close(requestSent)
	}()

	select {
	case <-requestSent:
	case <-time.After(time.Second):
		t.Fatal("SECURITY: securitysync worker did not accept request before reply blockage")
	}

	close(stop)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SECURITY: securitysync shutdown hung on an unread NetChan-style reply channel")
	}
}

func TestSecurityBoundaryStopBeforeReplyDeliveryCancelsRequest(t *testing.T) {
	stop := make(chan struct{})
	requests := make(chan Request)
	done := make(chan struct{})

	go func() {
		run("", stop, requests, map[string]map[string]evidenceSource{})
		close(done)
	}()

	reply := make(chan Result)
	requests <- Request{Query: true, Reply: reply}
	close(stop)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SECURITY: securitysync request remained alive after subsystem stop")
	}

	select {
	case <-reply:
		// Either result delivery or stop may win the select; both are bounded.
	default:
	}
}

func TestSecurityBoundaryBoundedRequestQueueAppliesBackpressure(t *testing.T) {
	stop := make(chan struct{})
	requests := make(chan Request, 64)
	done := make(chan struct{})
	go func() {
		run("", stop, requests, map[string]map[string]evidenceSource{})
		close(done)
	}()

	// Block the worker on result delivery, then fill the bounded request queue.
	blockedReply := make(chan Result)
	requests <- Request{Query: true, Reply: blockedReply}

	deadline := time.After(time.Second)
	for queued := 0; queued < cap(requests); {
		select {
		case requests <- Request{Query: true}:
			queued++
		case <-deadline:
			t.Fatal("SECURITY: could not fill bounded securitysync request queue")
		}
	}

	select {
	case requests <- Request{Query: true}:
		t.Fatal("SECURITY: securitysync request queue accepted work beyond its configured bound")
	default:
	}

	close(stop)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SECURITY: full securitysync request queue prevented shutdown")
	}
}

func TestSecurityBoundaryClosedRequestChannelDoesNotPreventStop(t *testing.T) {
	stop := make(chan struct{})
	requests := make(chan Request)
	done := make(chan struct{})

	go func() {
		run("", stop, requests, map[string]map[string]evidenceSource{})
		close(done)
	}()

	close(requests)
	close(stop)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SECURITY: closed NetChan-style request channel prevented subsystem shutdown")
	}
}
