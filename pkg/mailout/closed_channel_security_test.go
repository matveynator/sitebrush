package mailout

import (
	"context"
	"testing"
	"time"
)

func TestSecurityBoundaryDeliveryWorkerStopsWhenJobChannelCloses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	jobs := make(chan DeliveryJob)
	done := make(chan struct{})
	senderCalled := make(chan struct{}, 1)

	go func() {
		runDeliveryWorker(ctx, jobs, func(context.Context, Message) error {
			senderCalled <- struct{}{}
			return nil
		})
		close(done)
	}()

	close(jobs)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SECURITY: closed delivery channel did not terminate its worker")
	}
	select {
	case <-senderCalled:
		t.Fatal("SECURITY: closed delivery channel produced a synthetic empty mail task")
	default:
	}
}
