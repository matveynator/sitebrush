// Package workbudget limits optional background work without shared state.
package workbudget

import "time"

// Budget belongs to one worker. Each completed unit buys an idle interval;
// waiting for I/O counts as work too, making the allowance conservative.
// A single unit may exceed its allowance; subsequent work repays that debt.
type Budget struct {
	next time.Time
}

// Record gives each worker at most one percent of sustained wall time. The
// public observer and four analytics workers together target five percent
// of one CPU; request admission and runtime overhead still need measurement.
func (budget *Budget) Record(started time.Time) {
	finished := time.Now()
	budget.next = maxTime(budget.next, finished).Add(finished.Sub(started) * 99)
}

func maxTime(first, second time.Time) time.Time {
	if first.After(second) {
		return first
	}
	return second
}

// Wait keeps a saturated queue asleep, so producers drop observations instead
// of spending CPU draining them. The consumer's session interrupts the wait.
func (budget *Budget) Wait(stop <-chan struct{}) bool {
	select {
	case <-stop:
		return false
	default:
	}
	delay := time.Until(budget.next)
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-stop:
		return false
	case <-timer.C:
		return true
	}
}
