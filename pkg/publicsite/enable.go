package publicsite

import "errors"

type policyEnableRequest struct {
	address string
	reply   chan error
}

// Retries join one preparation session. A slow snapshot must not invalidate
// itself just because a transport timed out and its caller tried again.
func NewPolicyEnabler(front *Front, prepare func() error, stop <-chan struct{}) func(string) error {
	requests := make(chan policyEnableRequest)
	go func() {
		var waiting []policyEnableRequest
		var completed chan error
		for {
			select {
			case <-stop:
				return
			case request := <-requests:
				if len(waiting) >= 64 {
					request.reply <- errors.New("public policy enable queue is full")
					continue
				}
				if len(waiting) != 0 && waiting[0].address != request.address {
					request.reply <- errors.New("public policy publisher changed")
					continue
				}
				waiting = append(waiting, request)
				if completed == nil {
					completed = make(chan error, 1)
					go func(address string, result chan<- error) {
						result <- front.preparePolicy(address, prepare)
					}(request.address, completed)
				}
			case err := <-completed:
				for _, request := range waiting {
					request.reply <- err
				}
				waiting = nil
				completed = nil
			}
		}
	}()
	return func(address string) error {
		reply := make(chan error, 1)
		select {
		case requests <- policyEnableRequest{address: address, reply: reply}:
		case <-stop:
			return errors.New("public policy enable stopped")
		}
		select {
		case err := <-reply:
			return err
		case <-stop:
			return errors.New("public policy enable stopped")
		}
	}
}

func (front *Front) preparePolicy(address string, prepare func() error) error {
	state, err := front.Current()
	if err != nil {
		return err
	}
	if state.Address != address || state.Paused {
		return errors.New("public policy publisher is not serving")
	}
	if state.PolicyReady {
		return nil
	}
	if err = prepare(); err != nil {
		return err
	}
	// Compare and enable in the state owner, including same-address restarts.
	reply := make(chan State, 1)
	select {
	case front.tasks <- stateTask{enable: &state, reply: reply}:
	case <-front.stop:
		return errors.New("public server stopped")
	}
	select {
	case current := <-reply:
		if current.revision != state.revision || current.Address != address || current.Paused || !current.PolicyReady {
			return errors.New("public policy publisher changed during preparation")
		}
		return nil
	case <-front.stop:
		return errors.New("public server stopped")
	}
}
