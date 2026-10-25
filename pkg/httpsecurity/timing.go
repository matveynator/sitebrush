package httpsecurity

import (
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/requesttiming"
)

func startDecisionTiming(queued time.Time) time.Time {
	if queued.IsZero() {
		return time.Time{}
	}
	return time.Now()
}

// The owner transfers measurements, never its request history, to telemetry.
func finishDecisionTiming(collector *requesttiming.Collector, queued, started time.Time, queueStage, workStage requesttiming.Stage) {
	if started.IsZero() {
		return
	}
	var event requesttiming.Event
	event.Durations[queueStage] = started.Sub(queued)
	event.Durations[workStage] = time.Since(started)
	event.Present = 1<<queueStage | 1<<workStage
	collector.Observe(event)
}
