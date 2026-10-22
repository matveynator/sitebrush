package aiingress

import (
	"crypto/tls"
	"errors"
	"net/netip"
)

type admission struct {
	policy   Policy
	address  string
	response chan bool
	done     <-chan struct{}
}

type replacement struct {
	source   string
	document []byte
	response chan error
	done     <-chan struct{}
}

// Networks owns parsed snapshots in one process. Requests exchange decisions,
// never a mutable range map, and never wait for a provider's network refresh.
type Networks struct {
	admissions   chan admission
	replacements chan replacement
	stop         <-chan struct{}
}

func NewNetworks(stop <-chan struct{}, cached map[string][]byte) *Networks {
	networks := &Networks{admissions: make(chan admission), replacements: make(chan replacement), stop: stop}
	snapshots := make(map[string]Snapshot)
	for _, source := range Sources() {
		snapshot, err := source.Snapshot(OfficialSourceSnapshot(source.ID))
		if err != nil {
			continue
		}
		if previous, cacheErr := source.Snapshot(cached[source.ID]); cacheErr == nil && !previous.Created.Before(snapshot.Created) {
			snapshot = previous
		}
		snapshots[source.ID] = snapshot
	}
	go networks.run(snapshots)
	return networks
}

func (networks *Networks) run(snapshots map[string]Snapshot) {
	sources := Sources()
	for {
		select {
		case <-networks.stop:
			return
		case request := <-networks.admissions:
			select {
			case <-request.done:
				continue
			default:
			}
			allowed := allowsSnapshots(sources, snapshots, request.policy, request.address)
			select {
			case request.response <- allowed:
			case <-request.done:
			case <-networks.stop:
				return
			}
		case request := <-networks.replacements:
			select {
			case <-request.done:
				continue
			default:
			}
			var selected Source
			for _, source := range sources {
				if source.ID == request.source {
					selected = source
					break
				}
			}
			snapshot, err := selected.Snapshot(request.document)
			previous, known := snapshots[request.source]
			if !known {
				err = errors.New("unknown official AI source")
			}
			if err == nil && snapshot.Created.Before(previous.Created) {
				err = errors.New("official AI snapshot would move backwards")
			}
			if err == nil {
				snapshots[request.source] = snapshot
			}
			select {
			case request.response <- err:
			case <-request.done:
			case <-networks.stop:
				return
			}
		}
	}
}

func (networks *Networks) Allows(policy Policy, address string, transport *tls.ConnectionState, done <-chan struct{}) bool {
	// Keep certificate work at the TLS boundary, outside the range participant.
	if !openAICertificateAllowed(transport) {
		policy.OpenAI = false
	}
	request := admission{policy: policy, address: address, response: make(chan bool), done: done}
	select {
	case <-done:
		return false
	case <-networks.stop:
		return false
	case networks.admissions <- request:
	}
	select {
	case <-done:
		return false
	case <-networks.stop:
		return false
	case allowed := <-request.response:
		return allowed
	}
}

func (networks *Networks) Replace(source string, document []byte, done <-chan struct{}) error {
	request := replacement{source: source, document: document, response: make(chan error), done: done}
	select {
	case <-done:
		return errors.New("AI range refresh cancelled")
	case <-networks.stop:
		return errors.New("AI ranges stopped")
	case networks.replacements <- request:
	}
	select {
	case <-done:
		return errors.New("AI range refresh cancelled")
	case <-networks.stop:
		return errors.New("AI ranges stopped")
	case err := <-request.response:
		return err
	}
}

// Embedded admission is also available to small adapters without a long-lived
// range participant. The server always uses Networks to avoid parsing per request.
func AllowsOfficial(policy Policy, address string, transport *tls.ConnectionState) bool {
	if !openAICertificateAllowed(transport) {
		policy.OpenAI = false
	}
	snapshots := make(map[string]Snapshot)
	sources := Sources()
	for _, source := range sources {
		if policy.Enabled(source.Provider) {
			snapshots[source.ID], _ = source.Snapshot(OfficialSourceSnapshot(source.ID))
		}
	}
	return allowsSnapshots(sources, snapshots, policy, address)
}

func allowsSnapshots(sources []Source, snapshots map[string]Snapshot, policy Policy, address string) bool {
	if (Ranges{}).Allows(policy, address, nil) {
		return true
	}
	client, err := netip.ParseAddr(address)
	if err != nil || !policy.External {
		return false
	}
	for _, source := range sources {
		if policy.Enabled(source.Provider) && contains(snapshots[source.ID].Prefixes, client.Unmap()) {
			return true
		}
	}
	return false
}
