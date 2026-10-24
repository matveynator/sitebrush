package sitefreeze

import (
	"errors"
	"testing"
	"time"
)

func TestPreviewIgnoresTokenConsumptionWithoutChangingSnapshot(t *testing.T) {
	before := Checkpoint{Properties: []Property{{Table: "file_access_rules", Columns: []string{"file_name", "single_use_left", "token_use_count"}, Rows: [][]any{{"file", int64(1), int64(0)}}}}}
	after := Checkpoint{CreatedAt: "later", Properties: []Property{{Table: "file_access_rules", Columns: []string{"file_name", "single_use_left", "token_use_count"}, Rows: [][]any{{"file", int64(0), int64(1)}}}}}
	if preview := Compare(before, after); preview.Total != 0 {
		t.Fatalf("visitor consumption is not an editorial change: %+v", preview)
	}
	if Compare(before, before).Digest != Compare(before, after).Digest {
		t.Fatal("visitor invalidated owner confirmation")
	}
	if before.Properties[0].Rows[0][1] != int64(1) {
		t.Fatal("comparison mutated the security snapshot")
	}
}

func TestExclusiveLeaseCancellationAndGenerationFence(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	gate := NewGate(stop)
	editor, err := gate.Enter("example.org", 1, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	canceled := make(chan struct{})
	admitted := make(chan admitted, 1)
	gate.requests <- admission{domain: "example.org", expected: 1, exclusive: true, done: canceled, reply: admitted}
	if lease, enterErr := gate.Enter("example.org", 1, false, nil); !errors.Is(enterErr, ErrBusy) {
		if lease != nil {
			lease.Close(false)
		}
		t.Fatalf("exclusive waiter did not stop new work: %v", enterErr)
	}
	close(canceled)
	select {
	case result := <-admitted:
		if result.err == nil || result.lease != nil {
			t.Fatal("canceled operation admitted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled waiter blocked behind an editor")
	}

	editor.Close(false)
	restoration, err := gate.Enter("example.org", 1, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	restoration.Close(true)
	if _, err = gate.Enter("example.org", 1, false, nil); !errors.Is(err, ErrStale) {
		t.Fatalf("old task admitted after restore: %v", err)
	}
	lease, err := gate.Enter("example.org", 2, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	lease.Close(false)
}
