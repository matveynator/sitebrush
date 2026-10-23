package publicsite

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// Observation delivery never holds an HTTP handler behind a database or restart.
type publicObservation struct {
	domain string
	event  any
}
type ObservationRequest struct {
	Address string
	Ack     uint64
}
type observationPull struct {
	request ObservationRequest
	reply   chan observationResult
}
type observationResult struct {
	encoded []byte
	err     error
}
type Observations struct {
	Batch       uint64
	Events      []json.RawMessage
	LostDomains []string
}

// Admission is only a hint: Observe still checks the queue after a caller
// constructs its event. A saturated collector requires no request parsing.
func (front *Front) ObservationAvailable(domain string) bool {
	if len(front.events) < cap(front.events) {
		return true
	}
	select {
	case front.losses <- domain:
	default:
	}
	return false
}
func (front *Front) Observe(domain string, event any) {
	select {
	case front.events <- publicObservation{domain: domain, event: event}:
	default:
		select {
		case front.losses <- domain:
		default:
		}
	}
}
func (front *Front) deliverObservations() {
	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	pending := make([]json.RawMessage, 0, 64)
	batch := uint64(1)
	var inFlight []byte
	lost := make(map[string]bool)
	markLost := func(domain string) {
		if len(lost) < 128 {
			lost[domain] = true
		}
	}
	flush := func() {
		if len(pending) == 0 && len(lost) == 0 && inFlight == nil {
			return
		}
		state, err := front.exchange(nil)
		if err != nil || state.Paused || state.Address == "" || state.PolicyReady {
			return
		}
		envelope := Observations{Events: pending}
		for domain := range lost {
			envelope.LostDomains = append(envelope.LostDomains, domain)
		}
		encoded, err := json.Marshal(envelope)
		if inFlight != nil {
			encoded = inFlight
			err = nil
		}
		if err != nil {
			return
		}
		request, err := http.NewRequest(http.MethodPost, "http://"+state.Address+"/.well-known/sitebrush-public-events", bytes.NewReader(encoded))
		if err != nil {
			return
		}
		request.Header.Set(ProxyHeader, front.secret)
		request.Header.Set(InternalHeader, front.secret)
		request.Header.Set(RemoteHeader, "127.0.0.1:0")
		request.Header.Set(SchemeHeader, "http")
		response, err := client.Do(request)
		if err != nil {
			return
		}
		response.Body.Close()
		if response.StatusCode == http.StatusNoContent {
			if inFlight != nil {
				inFlight = nil
				batch++
				return
			}
			pending = make([]json.RawMessage, 0, 64)
			lost = make(map[string]bool)
		}
	}
	for {
		select {
		case <-front.stop:
			return
		case domain := <-front.losses:
			markLost(domain)
		case pull := <-front.pulls:
			state, err := front.exchange(nil)
			if err != nil || state.Paused || !state.PolicyReady || state.Address != pull.request.Address {
				pull.reply <- observationResult{err: errors.New("observation consumer is not serving")}
				continue
			}
			if pull.request.Ack == batch && inFlight != nil {
				inFlight = nil
				batch++
			}
			var marshalErr error
			if inFlight == nil {
				envelope := Observations{Events: pending, Batch: batch}
				for domain := range lost {
					envelope.LostDomains = append(envelope.LostDomains, domain)
				}
				inFlight, marshalErr = json.Marshal(envelope)
				if marshalErr == nil {
					pending = make([]json.RawMessage, 0, 64)
					lost = make(map[string]bool)
				}
			}
			pull.reply <- observationResult{encoded: append([]byte(nil), inFlight...), err: marshalErr}
		case observation := <-front.events:
			if len(pending) < 64 {
				encoded, marshalErr := json.Marshal(observation.event)
				if marshalErr != nil {
					markLost(observation.domain)
					continue
				}
				pending = append(pending, encoded)
				if len(pending) == 64 {
					flush()
				}
			} else {
				markLost(observation.domain)
			}
		case <-ticker.C:
			flush()
		}
	}
}

// A batch remains owned by the producer until the consumer acknowledges it.
// Lost contacts can retry without consuming another batch or blocking visitors.
func (front *Front) PullObservations(encoded []byte) ([]byte, error) {
	var request ObservationRequest
	if len(encoded) > 4096 || json.Unmarshal(encoded, &request) != nil {
		return nil, errors.New("invalid observation request")
	}
	reply := make(chan observationResult, 1)
	select {
	case front.pulls <- observationPull{request: request, reply: reply}:
	case <-front.stop:
		return nil, errors.New("public observer stopped")
	}
	select {
	case result := <-reply:
		return result.encoded, result.err
	case <-front.stop:
		return nil, errors.New("public observer stopped")
	}
}
