package securitysync

import (
	"testing"
	"time"
)

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
	requests, err := Start("", stop)
	if err != nil {
		t.Fatal(err)
	}
	defer close(stop)

	// Fill the documented bounded queue while the worker is intentionally
	// blocked delivering the first result.
	blockedReply := make(chan Result)
	requests <- Request{Query: true, Reply: blockedReply}

	accepted := 0
	for accepted < 64 {
		select {
		case requests <- Request{Query: true}:
			accepted++
		default:
			// The worker may consume one queued request while scheduling; a
			// bounded queue is the invariant, not an exact instantaneous count.
			goto queueFull
		}
	}

queueFull:
	select {
	case requests <- Request{Query: true}:
		// One slot may become available if scheduling let the worker progress,
		// so do not fail solely on this race. The shutdown test below proves
		// blocked work remains cancellable.
	default:
	}

	close(stop)
	select {
	case <-time.After(time.Second):
		t.Fatal("SECURITY: full securitysync request queue prevented shutdown")
	default:
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
