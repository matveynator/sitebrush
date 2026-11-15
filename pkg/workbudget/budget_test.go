package workbudget

import (
	"testing"
	"time"
)

func TestBudgetRepaysWorkBeforeAcceptingMore(t *testing.T) {
	var budget Budget
	started := time.Now().Add(-time.Millisecond)
	budget.Record(started)
	if time.Until(budget.next) < 90*time.Millisecond {
		t.Fatal("background work did not reserve its idle interval")
	}
	previous := budget.next
	budget.Record(time.Now().Add(-time.Millisecond))
	if !budget.next.After(previous.Add(90 * time.Millisecond)) {
		t.Fatal("a burst discarded outstanding work debt")
	}
}

func TestBudgetSessionClosureInterruptsCooldown(t *testing.T) {
	budget := Budget{next: time.Now().Add(time.Hour)}
	stop := make(chan struct{})
	finished := make(chan bool, 1)
	go func() { finished <- budget.Wait(stop) }()
	close(stop)
	select {
	case accepted := <-finished:
		if accepted {
			t.Fatal("terminated consumer accepted more work")
		}
	case <-time.After(time.Second):
		t.Fatal("consumer closure did not interrupt budget wait")
	}
}
