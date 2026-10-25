// Package requesttiming measures local request stages without delaying responses.
package requesttiming

import (
	"bufio"
	"context"
	"errors"
	"io"
	"math/bits"
	"net"
	"net/http"
	"time"
)

type Stage uint8

const (
	AcceptedToRequest Stage = iota
	FrontState
	Domain
	Policy
	AttackGuard
	ThrottleGuard
	PublicHandler
	FirstWrite
	Certificate
	AttackQueue
	AttackWork
	ThrottleQueue
	ThrottleWork
	ProxyMetadata
	ProxyBackend
	stageCount
)

func (stage Stage) String() string {
	return [...]string{"accepted_to_request", "front_state", "domain", "policy", "attack_guard", "throttle_guard", "public_handler", "first_write", "certificate", "attack_queue", "attack_work", "throttle_queue", "throttle_work", "proxy_metadata", "proxy_backend"}[stage]
}

type Event struct {
	Durations [stageCount]time.Duration
	Present   uint16
}

type Counter struct {
	Stage   string
	Count   uint64
	TotalNS uint64
	MaxNS   uint64
	Buckets [64]uint64
}

type Report struct {
	Stages         [stageCount]Counter
	DroppedMinimum uint64
	Incomplete     bool
}

type Collector struct {
	events  chan Event
	losses  chan struct{}
	reports chan chan Report
	stop    <-chan struct{}
}

func New(stop <-chan struct{}) *Collector {
	collector := &Collector{events: make(chan Event, 4096), losses: make(chan struct{}, 1), reports: make(chan chan Report), stop: stop}
	go collector.run()
	return collector
}

func (collector *Collector) run() {
	var report Report
	for stage := range report.Stages {
		report.Stages[stage].Stage = Stage(stage).String()
	}
	for {
		select {
		case <-collector.stop:
			return
		case <-collector.losses:
			report.DroppedMinimum++
			report.Incomplete = true
		case reply := <-collector.reports:
			for pending := len(collector.events); pending > 0; pending-- {
				report.record(<-collector.events)
			}
			select {
			case <-collector.losses:
				report.DroppedMinimum++
				report.Incomplete = true
			default:
			}
			reply <- report
		case event := <-collector.events:
			report.record(event)
		}
	}
}

func (report *Report) record(event Event) {
	for stage, duration := range event.Durations {
		if event.Present&(1<<stage) == 0 {
			continue
		}
		counter := &report.Stages[stage]
		nanoseconds := uint64(max(duration, 0))
		counter.Count++
		counter.TotalNS += nanoseconds
		counter.MaxNS = max(counter.MaxNS, nanoseconds)
		counter.Buckets[min(bits.Len64(nanoseconds), 63)]++
	}
}

// Histogram percentiles are bucket upper bounds; wire-level load tests retain
// individual samples and report exact percentiles separately.
func (counter Counter) PercentileUpperBound(percent uint64) time.Duration {
	threshold := (counter.Count*percent + 99) / 100
	if threshold == 0 {
		return 0
	}
	var observations uint64
	for bucket, count := range counter.Buckets {
		observations += count
		if observations >= threshold {
			return time.Duration((uint64(1) << bucket) - 1)
		}
	}
	return 0
}

func (collector *Collector) Observe(event Event) bool {
	if collector == nil {
		return true
	}
	select {
	case <-collector.stop:
		return false
	default:
	}
	select {
	case collector.events <- event:
		return true
	default:
		select {
		case collector.losses <- struct{}{}:
		default:
		}
		return false
	}
}

func (collector *Collector) Record(stage Stage, duration time.Duration) {
	var event Event
	event.Durations[stage] = duration
	event.Present = 1 << stage
	collector.Observe(event)
}

func (collector *Collector) Snapshot(stop <-chan struct{}) (Report, error) {
	reply := make(chan Report, 1)
	select {
	case collector.reports <- reply:
	case <-stop:
		return Report{}, errors.New("timing subscriber stopped")
	case <-collector.stop:
		return Report{}, errors.New("timing collector stopped")
	}
	select {
	case report := <-reply:
		return report, nil
	case <-stop:
		return Report{}, errors.New("timing subscriber stopped")
	case <-collector.stop:
		return Report{}, errors.New("timing collector stopped")
	}
}

// A trace belongs to one HTTP handler. Context is only the net/http adapter;
// completed measurements transfer to their collector through its input channel.
type trace struct {
	started, first time.Time
	event          Event
}
type traceKey struct{}
type acceptedKey struct{}

func (collector *Collector) ConnContext(parent context.Context, _ net.Conn) context.Context {
	accepted := make(chan time.Time, 1)
	accepted <- time.Now()
	return context.WithValue(parent, acceptedKey{}, (<-chan time.Time)(accepted))
}

func Start(request *http.Request) time.Time {
	if request.Context().Value(traceKey{}) == nil {
		return time.Time{}
	}
	return time.Now()
}

func Finish(request *http.Request, stage Stage, started time.Time) {
	if started.IsZero() {
		return
	}
	measurement, found := request.Context().Value(traceKey{}).(*trace)
	if !found {
		return
	}
	finished := time.Now()
	if !measurement.first.IsZero() && finished.After(measurement.first) {
		finished = measurement.first
	}
	if finished.Before(started) {
		return
	}
	measurement.event.Durations[stage] += finished.Sub(started)
	measurement.event.Present |= 1 << stage
}

func Enabled(request *http.Request) bool {
	return request.Context().Value(traceKey{}) != nil
}

func (collector *Collector) Middleware(next http.Handler) http.Handler {
	if collector == nil {
		return next
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		measurement := &trace{started: time.Now()}
		if accepted, found := request.Context().Value(acceptedKey{}).(<-chan time.Time); found {
			select {
			case started := <-accepted:
				measurement.event.Durations[AcceptedToRequest] = measurement.started.Sub(started)
				measurement.event.Present |= 1 << AcceptedToRequest
			default:
			}
		}
		request = request.WithContext(context.WithValue(request.Context(), traceKey{}, measurement))
		next.ServeHTTP(&responseWriter{ResponseWriter: writer, measurement: measurement}, request)
		collector.Observe(measurement.event)
	})
}

type responseWriter struct {
	http.ResponseWriter
	measurement *trace
}

func (writer *responseWriter) firstWrite() {
	if !writer.measurement.first.IsZero() {
		return
	}
	writer.measurement.first = time.Now()
	writer.measurement.event.Durations[FirstWrite] = writer.measurement.first.Sub(writer.measurement.started)
	writer.measurement.event.Present |= 1 << FirstWrite
}
func (writer *responseWriter) Unwrap() http.ResponseWriter { return writer.ResponseWriter }
func (writer *responseWriter) WriteHeader(status int) {
	if status >= 200 || status == http.StatusSwitchingProtocols {
		writer.firstWrite()
	}
	writer.ResponseWriter.WriteHeader(status)
}
func (writer *responseWriter) Write(body []byte) (int, error) {
	writer.firstWrite()
	return writer.ResponseWriter.Write(body)
}
func (writer *responseWriter) ReadFrom(source io.Reader) (int64, error) {
	writer.firstWrite()
	if receiver, found := writer.ResponseWriter.(io.ReaderFrom); found {
		return receiver.ReadFrom(source)
	}
	return io.Copy(struct{ io.Writer }{writer.ResponseWriter}, source)
}
func (writer *responseWriter) Flush() {
	writer.firstWrite()
	_ = http.NewResponseController(writer.ResponseWriter).Flush()
}
func (writer *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(writer.ResponseWriter).Hijack()
}
