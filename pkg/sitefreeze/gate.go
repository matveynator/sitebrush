// Package sitefreeze owns reversible site editing boundaries.
package sitefreeze

import "errors"

var ErrBusy = errors.New("site checkpoint operation is in progress; retry after it finishes")
var ErrStale = errors.New("site editing generation changed; reload the site")

type admission struct {
	domain    string
	expected  uint64
	exclusive bool
	reply     chan admitted
	done      <-chan struct{}
	resolved  chan struct{}
}
type admitted struct {
	lease *Lease
	err   error
}
type release struct {
	domain    string
	exclusive bool
	advance   bool
	reply     chan struct{}
}
type generationRequest struct {
	domain string
	reply  chan uint64
}
type participant struct {
	active    int
	epoch     uint64
	exclusive bool
	waiting   *admission
}

// Gate has no application state outside its participant. Each accepted caller
// owns one lease; an exclusive caller waits for existing editors to finish.
type Gate struct {
	requests    chan admission
	releases    chan release
	generations chan generationRequest
	abandoned   chan *admission
	stop        <-chan struct{}
}
type Lease struct {
	gate      *Gate
	domain    string
	exclusive bool
}

func NewGate(stop <-chan struct{}) *Gate {
	gate := &Gate{requests: make(chan admission, 128), releases: make(chan release, 128), generations: make(chan generationRequest), abandoned: make(chan *admission, 128), stop: stop}
	go gate.run()
	return gate
}
func (gate *Gate) run() {
	sites := make(map[string]*participant)
	lookup := func(domain string) *participant {
		state := sites[domain]
		if state == nil {
			state = &participant{epoch: 1}
			sites[domain] = state
		}
		return state
	}
	for {
		select {
		case <-gate.stop:
			return
		case request := <-gate.generations:
			epoch := uint64(1)
			if state := sites[request.domain]; state != nil {
				epoch = state.epoch
			}
			request.reply <- epoch
		case request := <-gate.abandoned:
			state := sites[request.domain]
			if state != nil && state.waiting == request {
				state.waiting = nil
				close(request.resolved)
				request.reply <- admitted{err: errors.New("site operation canceled")}
			}
		case request := <-gate.requests:
			select {
			case <-request.done:
				request.reply <- admitted{err: errors.New("site operation canceled")}
				continue
			default:
			}
			state := lookup(request.domain)
			if request.expected != 0 && request.expected != state.epoch {
				if state.epoch == 1 && state.active == 0 && state.waiting == nil {
					delete(sites, request.domain)
				}
				request.reply <- admitted{err: ErrStale}
				continue
			}
			if state.exclusive || state.waiting != nil {
				request.reply <- admitted{err: ErrBusy}
				continue
			}
			if request.exclusive && state.active > 0 {
				request.resolved = make(chan struct{})
				state.waiting = &request
				go func(waiting *admission) {
					select {
					case <-waiting.done:
						select {
						case gate.abandoned <- waiting:
						case <-gate.stop:
						}
					case <-waiting.resolved:
					case <-gate.stop:
					}
				}(state.waiting)
				continue
			}
			state.active++
			state.exclusive = request.exclusive
			request.reply <- admitted{lease: &Lease{gate: gate, domain: request.domain, exclusive: request.exclusive}}
		case finished := <-gate.releases:
			state := lookup(finished.domain)
			if state.active > 0 {
				state.active--
			}
			if finished.exclusive {
				state.exclusive = false
			}
			if finished.advance {
				state.epoch++
			}
			if state.active == 0 && state.waiting != nil {
				waiting := state.waiting
				state.waiting = nil
				close(waiting.resolved)
				select {
				case <-waiting.done:
					waiting.reply <- admitted{err: errors.New("site operation canceled")}
				default:
					state.active = 1
					state.exclusive = true
					waiting.reply <- admitted{lease: &Lease{gate: gate, domain: waiting.domain, exclusive: true}}
				}
			}
			if state.active == 0 && state.waiting == nil && state.epoch == 1 {
				delete(sites, finished.domain)
			}
			close(finished.reply)
		}
	}
}
func (gate *Gate) Generation(domain string) uint64 {
	if gate == nil {
		return 0
	}
	reply := make(chan uint64, 1)
	select {
	case gate.generations <- generationRequest{domain: domain, reply: reply}:
	case <-gate.stop:
		return 0
	}
	select {
	case epoch := <-reply:
		return epoch
	case <-gate.stop:
		return 0
	}
}
func (gate *Gate) Enter(domain string, expected uint64, exclusive bool, done <-chan struct{}, queued ...chan<- struct{}) (*Lease, error) {
	notifyQueued := func() {
		if len(queued) > 0 && queued[0] != nil {
			close(queued[0])
		}
	}
	if gate == nil {
		notifyQueued()
		return &Lease{}, nil
	}
	reply := make(chan admitted, 1)
	select {
	case gate.requests <- admission{domain: domain, expected: expected, exclusive: exclusive, done: done, reply: reply}:
		notifyQueued()
	case <-done:
		notifyQueued()
		return nil, errors.New("site operation canceled")
	case <-gate.stop:
		notifyQueued()
		return nil, errors.New("site editing stopped")
	}
	// Once queued, the admission reply transfers ownership even if the HTTP
	// consumer disconnects. Receiving it prevents an abandoned live lease.
	select {
	case result := <-reply:
		if result.lease != nil {
			select {
			case <-done:
				result.lease.Close(false)
				return nil, errors.New("site operation canceled")
			default:
			}
		}
		return result.lease, result.err
	case <-gate.stop:
		return nil, errors.New("site editing stopped")
	}
}
func (lease *Lease) Close(advance bool) {
	if lease == nil || lease.gate == nil {
		return
	}
	reply := make(chan struct{})
	select {
	case lease.gate.releases <- release{domain: lease.domain, exclusive: lease.exclusive, advance: advance, reply: reply}:
	case <-lease.gate.stop:
		return
	}
	select {
	case <-reply:
	case <-lease.gate.stop:
	}
}
