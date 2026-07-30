package crawler

import (
	"testing"
	"time"
)

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
