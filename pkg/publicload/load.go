// Package publicload measures connection setup separately from response latency.
package publicload

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"sort"
	"time"
)

type Options struct {
	URL            string
	Requests       int
	Concurrency    int
	ExpectedBytes  int64
	ExpectedSHA256 string
	PrepareRequest func(*http.Request, int)
	Burst          bool
}

type Percentiles struct {
	Samples int
	P50US   int64
	P95US   int64
	P99US   int64
}

type Report struct {
	Requests            int
	Successful          int
	TransportErrors     int
	Non2xx              int
	LengthMismatches    int
	ContentMismatches   int
	IncompleteTraces    int
	NewConnections      int
	ReusedConnections   int
	ResumedTLS          int
	Bytes               int64
	Elapsed             time.Duration
	RequestsPerSecond   float64
	SuccessfulPerSecond float64
	DNS                 Percentiles
	TCP                 Percentiles
	TLS                 Percentiles
	ConnectionWait      Percentiles
	ResponseWait        Percentiles
	TotalTTFB           Percentiles
	SuccessfulTTFB      Percentiles
	Total               Percentiles
	Statuses            map[int]int
	Protocols           map[string]int
	TLSVersions         map[uint16]int
	FirstError          string
}

type stage uint8

const (
	dnsStart stage = iota
	dnsDone
	connectStart
	connectDone
	tlsStart
	tlsDone
	wroteRequest
	firstByte
	gotConnection
	getConnection
)

type traceEvent struct {
	stage   stage
	at      time.Time
	address string
	valid   bool
	reused  bool
	resumed bool
	version uint16
}

type sample struct {
	dns, tcp, tls, connectionWait, wait, ttfb, total time.Duration
	status                                           int
	bytes                                            int64
	protocol                                         string
	error                                            string
	incomplete, reused, resumed                      bool
	connected                                        bool
	version                                          uint16
	digest                                           string
}

// A worker owns its trace channel and sample slice. Callbacks transfer events
// instead of mutating the worker's measurements from transport goroutines.
func requestSample(client *http.Client, template *http.Request, options Options, worker int) sample {
	events := make(chan traceEvent, 32)
	lost := make(chan struct{}, 1)
	observe := func(event traceEvent) {
		event.at = time.Now()
		select {
		case events <- event:
		default:
			select {
			case lost <- struct{}{}:
			default:
			}
		}
	}
	trace := &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { observe(traceEvent{stage: dnsStart}) },
		DNSDone:  func(info httptrace.DNSDoneInfo) { observe(traceEvent{stage: dnsDone, valid: info.Err == nil}) },
		ConnectStart: func(network, address string) {
			observe(traceEvent{stage: connectStart, address: network + ":" + address})
		},
		ConnectDone: func(network, address string, err error) {
			observe(traceEvent{stage: connectDone, address: network + ":" + address, valid: err == nil})
		},
		TLSHandshakeStart: func() { observe(traceEvent{stage: tlsStart}) },
		TLSHandshakeDone: func(state tls.ConnectionState, err error) {
			observe(traceEvent{stage: tlsDone, valid: err == nil, resumed: state.DidResume, version: state.Version})
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			observe(traceEvent{stage: wroteRequest, valid: info.Err == nil})
		},
		GotFirstResponseByte: func() { observe(traceEvent{stage: firstByte}) },
		GotConn:              func(info httptrace.GotConnInfo) { observe(traceEvent{stage: gotConnection, reused: info.Reused}) },
		GetConn:              func(string) { observe(traceEvent{stage: getConnection}) },
	}
	result := sample{}
	request := template.Clone(httptrace.WithClientTrace(template.Context(), trace))
	if options.PrepareRequest != nil {
		options.PrepareRequest(request, worker)
	}
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		result.error = err.Error()
	} else {
		result.status = response.StatusCode
		result.protocol = response.Proto
		if options.ExpectedSHA256 == "" {
			result.bytes, err = io.Copy(io.Discard, response.Body)
		} else {
			digest := sha256.New()
			result.bytes, err = io.Copy(digest, response.Body)
			result.digest = hex.EncodeToString(digest.Sum(nil))
		}
		if response.TLS != nil {
			result.version = response.TLS.Version
		}
		closeErr := response.Body.Close()
		if err = errors.Join(err, closeErr); err != nil {
			result.error = err.Error()
		}
	}
	result.total = time.Since(started)
	var dnsAt, tlsAt, writtenAt, connectionAt time.Time
	connections := make(map[string]time.Time)
	for {
		select {
		case event := <-events:
			switch event.stage {
			case dnsStart:
				dnsAt = event.at
			case dnsDone:
				if event.valid && !dnsAt.IsZero() {
					result.dns += event.at.Sub(dnsAt)
				}
			case connectStart:
				connections[event.address] = event.at
			case connectDone:
				if began := connections[event.address]; event.valid && !began.IsZero() {
					result.tcp += event.at.Sub(began)
				}
			case tlsStart:
				tlsAt = event.at
			case tlsDone:
				if event.valid && !tlsAt.IsZero() {
					result.tls += event.at.Sub(tlsAt)
					result.resumed = event.resumed
				}
			case wroteRequest:
				if event.valid {
					writtenAt = event.at
				}
			case firstByte:
				result.ttfb = event.at.Sub(started)
				if !writtenAt.IsZero() {
					result.wait = event.at.Sub(writtenAt)
				}
			case gotConnection:
				result.connected, result.reused = true, event.reused
				if !connectionAt.IsZero() {
					result.connectionWait += event.at.Sub(connectionAt)
				}
			case getConnection:
				connectionAt = event.at
			}
		default:
			select {
			case <-lost:
				result.incomplete = true
			default:
			}
			if result.reused {
				result.dns, result.tcp, result.tls, result.resumed = 0, 0, 0, false
			}
			return result
		}
	}
}

func Run(client *http.Client, options Options, stop <-chan struct{}) (Report, error) {
	if client == nil || options.Requests <= 0 || options.Concurrency <= 0 {
		return Report{}, errors.New("load test requires a client, positive request count and concurrency")
	}
	template, err := http.NewRequest(http.MethodGet, options.URL, nil)
	if err != nil {
		return Report{}, err
	}
	if options.ExpectedSHA256 != "" {
		decoded, decodeErr := hex.DecodeString(options.ExpectedSHA256)
		if decodeErr != nil || len(decoded) != sha256.Size {
			return Report{}, errors.New("expected SHA256 must contain 64 hexadecimal digits")
		}
		options.ExpectedSHA256 = hex.EncodeToString(decoded)
	}
	concurrency := min(options.Requests, options.Concurrency)
	results := make(chan []sample, concurrency)
	start := make(chan struct{})
	var rounds chan chan struct{}
	finished := make(chan struct{})
	defer close(finished)
	if options.Burst {
		rounds = make(chan chan struct{}, concurrency)
		go func() {
			for remaining := options.Requests; remaining > 0; {
				count := min(remaining, concurrency)
				participants := make([]chan struct{}, 0, count)
				for len(participants) < count {
					select {
					case reply := <-rounds:
						participants = append(participants, reply)
					case <-stop:
						return
					case <-finished:
						return
					}
				}
				for _, reply := range participants {
					close(reply)
				}
				remaining -= count
			}
		}()
	}
	for worker := 0; worker < concurrency; worker++ {
		count := options.Requests / concurrency
		if worker < options.Requests%concurrency {
			count++
		}
		go func(worker, count int) {
			// Context is confined to the HTTP library boundary. The load worker's
			// lifetime is controlled by its consumer's stop channel.
			boundary, cancel := context.WithCancel(template.Context())
			defer cancel()
			finished := make(chan struct{})
			defer close(finished)
			go func() {
				select {
				case <-stop:
					cancel()
				case <-finished:
				}
			}()
			workerTemplate := template.WithContext(boundary)
			samples := make([]sample, 0, count)
			<-start
			defer func() { results <- samples }()
			for request := 0; request < count; request++ {
				select {
				case <-stop:
					return
				default:
				}
				if rounds != nil {
					reply := make(chan struct{})
					select {
					case rounds <- reply:
					case <-stop:
						return
					}
					select {
					case <-reply:
					case <-stop:
						return
					}
				}
				samples = append(samples, requestSample(client, workerTemplate, options, worker))
			}
		}(worker, count)
	}
	report := Report{Statuses: make(map[int]int), Protocols: make(map[string]int), TLSVersions: make(map[uint16]int)}
	var dns, tcp, tlsSamples, connectionWait, wait, ttfb, successfulTTFB, total []time.Duration
	started := time.Now()
	close(start)
	for worker := 0; worker < concurrency; worker++ {
		for _, result := range <-results {
			report.Requests++
			report.Bytes += result.bytes
			report.Statuses[result.status]++
			report.Protocols[result.protocol]++
			if result.version != 0 {
				report.TLSVersions[result.version]++
			}
			if result.incomplete {
				report.IncompleteTraces++
			}
			if result.connected {
				if result.reused {
					report.ReusedConnections++
				} else {
					report.NewConnections++
				}
			}
			if result.resumed {
				report.ResumedTLS++
			}
			if result.error != "" {
				report.TransportErrors++
				if report.FirstError == "" {
					report.FirstError = result.error
				}
			} else if result.status < 200 || result.status >= 300 {
				report.Non2xx++
			} else if options.ExpectedBytes > 0 && result.bytes != options.ExpectedBytes {
				report.LengthMismatches++
			} else if options.ExpectedSHA256 != "" && result.digest != options.ExpectedSHA256 {
				report.ContentMismatches++
			} else {
				report.Successful++
				if result.ttfb > 0 {
					successfulTTFB = append(successfulTTFB, result.ttfb)
				}
			}
			if result.dns > 0 {
				dns = append(dns, result.dns)
			}
			if result.tcp > 0 {
				tcp = append(tcp, result.tcp)
			}
			if result.tls > 0 {
				tlsSamples = append(tlsSamples, result.tls)
			}
			if result.wait > 0 {
				wait = append(wait, result.wait)
			}
			if result.connectionWait > 0 {
				connectionWait = append(connectionWait, result.connectionWait)
			}
			if result.ttfb > 0 {
				ttfb = append(ttfb, result.ttfb)
			}
			total = append(total, result.total)
		}
	}
	report.Elapsed = time.Since(started)
	report.RequestsPerSecond = float64(report.Requests) / report.Elapsed.Seconds()
	report.SuccessfulPerSecond = float64(report.Successful) / report.Elapsed.Seconds()
	report.DNS, report.TCP, report.TLS = percentiles(dns), percentiles(tcp), percentiles(tlsSamples)
	report.ResponseWait, report.TotalTTFB, report.Total = percentiles(wait), percentiles(ttfb), percentiles(total)
	report.ConnectionWait, report.SuccessfulTTFB = percentiles(connectionWait), percentiles(successfulTTFB)
	if report.Requests != options.Requests {
		return report, fmt.Errorf("load test stopped after %d of %d requests", report.Requests, options.Requests)
	}
	return report, nil
}

func percentiles(samples []time.Duration) Percentiles {
	if len(samples) == 0 {
		return Percentiles{}
	}
	sort.Slice(samples, func(left, right int) bool { return samples[left] < samples[right] })
	return Percentiles{Samples: len(samples), P50US: samples[(len(samples)-1)*50/100].Microseconds(), P95US: samples[(len(samples)-1)*95/100].Microseconds(), P99US: samples[(len(samples)-1)*99/100].Microseconds()}
}
