package publicsite

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"
)

// Observation delivery never holds an HTTP handler behind a database or restart.
type publicObservation struct {
	domain  string
	encoded json.RawMessage
}
type Observations struct {
	Events      []json.RawMessage
	LostDomains []string
}

func (front *Front) Observe(domain string, encoded []byte) {
	select {
	case front.events <- publicObservation{domain: domain, encoded: encoded}:
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
	lost := make(map[string]bool)
	markLost := func(domain string) {
		if len(lost) < 128 {
			lost[domain] = true
		}
	}
	flush := func() {
		if len(pending) == 0 && len(lost) == 0 {
			return
		}
		state, err := front.exchange(nil)
		if err != nil || state.Paused || state.Address == "" {
			return
		}
		envelope := Observations{Events: pending}
		for domain := range lost {
			envelope.LostDomains = append(envelope.LostDomains, domain)
		}
		encoded, err := json.Marshal(envelope)
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
		case observation := <-front.events:
			if len(pending) < 64 {
				pending = append(pending, observation.encoded)
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
