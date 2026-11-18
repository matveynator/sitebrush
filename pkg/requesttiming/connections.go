package requesttiming

import (
	"net"
	"net/http"
	"time"
)

type connectionTiming struct {
	active, started, finished time.Time
	protocol                  int
}

// net/http reports Idle after finishRequest has flushed its response buffers.
// This measures HTTP/1 keep-alive completion without wrapping sockets, forcing
// Flush or disabling the standard library's platform-specific file transfers.
func (collector *Collector) ConnState(connection net.Conn, state http.ConnState) {
	if collector == nil || state == http.StateNew {
		return
	}
	collector.Observe(Event{connection: connection, transition: true, state: state, at: time.Now()})
}

func recordConnectionTiming(report *Report, connections map[net.Conn]connectionTiming, event Event, cutoff time.Time) {
	if event.at.Before(cutoff) {
		return
	}
	switch event.state {
	case http.StateActive:
		if _, exists := connections[event.connection]; !exists && len(connections) >= 65536 {
			report.Incomplete = true
			return
		}
		connections[event.connection] = connectionTiming{active: event.at}
	case http.StateIdle:
		current, found := connections[event.connection]
		delete(connections, event.connection)
		if !found || current.protocol != 1 || current.finished.IsZero() || current.started.Before(current.active) || event.at.Before(current.finished) {
			return
		}
		var completed Event
		completed.Durations[HTTP1ResponseComplete] = event.at.Sub(current.active)
		completed.Present = 1 << HTTP1ResponseComplete
		report.record(completed)
	case http.StateClosed, http.StateHijacked:
		delete(connections, event.connection)
	}
}

// Only the request's own trace changes; aggregation occurs after serving it.
func MarkRAM(request *http.Request, hit bool) {
	measurement, found := request.Context().Value(traceKey{}).(*trace)
	if !found {
		return
	}
	if hit {
		measurement.event.RAM.Hits++
	} else {
		measurement.event.RAM.Misses++
	}
}
