package crawler

import (
	"testing"
	"time"
)

func TestOwnershipProgressSubscribersAndPublisherHaveIndependentSnapshots(t *testing.T) {
	tracker := NewProgressTracker()
	first := tracker.Subscribe("ownership")
	second := tracker.Subscribe("ownership")
	defer tracker.Unsubscribe("ownership", first)
	defer tracker.Unsubscribe("ownership", second)
	event := ProgressEvent{Token: "ownership", FailedURLs: []string{"original"}, FailedReasons: map[string]string{"original": "timeout"}}
	tracker.Publish(event)
	event.FailedURLs[0], event.FailedReasons["original"] = "publisher", "publisher"
	firstReply := <-first
	firstReply.FailedURLs[0], firstReply.FailedReasons["original"] = "subscriber", "subscriber"
	secondReply := <-second
	if secondReply.FailedURLs[0] != "original" || secondReply.FailedReasons["original"] != "timeout" {
		t.Fatalf("subscriber received aliased progress: %+v", secondReply)
	}
	reconnected := tracker.Subscribe("ownership")
	defer tracker.Unsubscribe("ownership", reconnected)
	retained := <-reconnected
	if retained.FailedURLs[0] != "original" || retained.FailedReasons["original"] != "timeout" {
		t.Fatalf("subscriber changed retained progress: %+v", retained)
	}
	tracker.Unsubscribe("ownership", first)
}

func TestProgressTrackerKeepsNewestEventForSlowAndReconnectingSubscribers(t *testing.T) {
	tracker := NewProgressTracker()
	slowSubscriber := tracker.Subscribe("preview")
	defer tracker.Unsubscribe("preview", slowSubscriber)

	for eventIndex := 1; eventIndex <= 2000; eventIndex++ {
		tracker.Publish(ProgressEvent{Token: "preview", Stage: "downloaded", DownloadedTotal: eventIndex})
	}
	tracker.Publish(ProgressEvent{Token: "preview", Stage: "done", DownloadedTotal: 2000, CompletedPercent: 100})

	deadline := time.After(time.Second)
	for {
		select {
		case event := <-slowSubscriber:
			if event.Stage == "done" {
				if event.CompletedPercent != 100 {
					t.Fatalf("terminal event = %#v", event)
				}
				goto terminalReceived
			}
		case <-deadline:
			t.Fatal("slow subscriber did not receive the terminal event")
		}
	}

terminalReceived:
	reconnectingSubscriber := tracker.Subscribe("preview")
	defer tracker.Unsubscribe("preview", reconnectingSubscriber)
	select {
	case event := <-reconnectingSubscriber:
		if event.Stage != "done" || event.DownloadedTotal != 2000 {
			t.Fatalf("reconnecting subscriber received %#v, want retained terminal event", event)
		}
	case <-time.After(time.Second):
		t.Fatal("reconnecting subscriber did not receive retained progress")
	}
}
