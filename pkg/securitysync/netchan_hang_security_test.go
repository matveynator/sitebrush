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
