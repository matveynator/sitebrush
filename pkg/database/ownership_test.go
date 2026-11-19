package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestOwnershipSerializedCallbackReturnsCapturesAfterCancellation(t *testing.T) {
	database, _ := newSQLiteConcurrencyTestDatabase(t)
	boundary, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	reply := make(chan string, 1)
	go func() {
		captured := "before"
		_ = database.withSerializedConnection(boundary, func(context.Context, *sql.DB) error {
			close(started)
			<-release
			captured = "after"
			return nil
		})
		reply <- captured
	}()
	<-started
	cancel()
	select {
	case <-reply:
		t.Fatal("serialized callback still owned captures after return")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case result := <-reply:
		if result != "after" {
			t.Fatalf("unfinished callback: %q", result)
		}
	case <-time.After(time.Second):
		t.Fatal("serialized callback did not return ownership")
	}
}

func TestOwnershipRealtimeConverterReplacementSerializesCalls(t *testing.T) {
	defer SetRealtimeConverter(nil)
	SetRealtimeConverter(func(reading float64, _ string) (float64, bool) { return reading, true })
	results := make(chan bool, 100)
	for range 100 {
		go func() {
			SetRealtimeConverter(func(reading float64, _ string) (float64, bool) { return reading, true })
			doseRate, valid := convertRealtimeReading(12, "cpm")
			results <- valid && doseRate == 12
		}()
	}
	for range 100 {
		if !<-results {
			t.Fatal("converter returned inconsistent data during replacement")
		}
	}
}

func TestOwnershipUnclaimedCallbackCancellationKeepsOwnerResponsive(t *testing.T) {
	database, _ := newSQLiteConcurrencyTestDatabase(t)
	boundary, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 1)
	job := serializedJob{
		ctx:     boundary,
		claimed: make(chan struct{}),
		result:  make(chan serializedResult, 1),
		fn: func(context.Context, *sql.DB) (any, error) {
			started <- struct{}{}
			return nil, nil
		},
	}
	// No subscriber accepts the claim. Cancellation must release the worker
	// without executing the callback or waiting for a result receiver.
	if err := database.pipeline.enqueue(boundary, job); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	if err := database.withSerializedConnection(boundary, func(context.Context, *sql.DB) error {
		started <- struct{}{}
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled subscriber: %v", err)
	}
	probe, stopProbe := context.WithTimeout(context.Background(), time.Second)
	defer stopProbe()
	if err := database.withSerializedConnection(probe, func(context.Context, *sql.DB) error { return nil }); err != nil {
		t.Fatalf("abandoned claim blocked owner: %v", err)
	}
	select {
	case <-started:
		t.Fatal("unclaimed callback executed")
	default:
	}
}

func BenchmarkOwnershipRealtimeConversion(b *testing.B) {
	SetRealtimeConverter(func(reading float64, _ string) (float64, bool) { return reading, true })
	b.Cleanup(func() { SetRealtimeConverter(nil) })
	b.ReportAllocs()
	for b.Loop() {
		if doseRate, valid := convertRealtimeReading(12, "cpm"); !valid || doseRate != 12 {
			b.Fatal("inconsistent conversion")
		}
	}
}
