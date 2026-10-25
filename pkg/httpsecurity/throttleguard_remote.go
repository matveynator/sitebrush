package httpsecurity

import (
	"encoding/json"
	"errors"
)

type throttleReply struct {
	Result throttleResult
	Error  string
}

// Remote guards carry a contact capability; the public participant remains the
// sole owner of rate windows and persisted state across child replacement.
func NewRemoteThrottleGuard(exchange func([]byte) ([]byte, error)) *ThrottleGuard {
	guard := &ThrottleGuard{shards: make([]chan throttleRequest, 1)}
	guard.remote = func(request throttleRequest) (throttleResult, bool) {
		encoded, err := json.Marshal(request)
		if err != nil {
			return throttleResult{Err: err}, false
		}
		response, err := exchange(encoded)
		if err != nil {
			return throttleResult{Err: err}, false
		}
		var reply throttleReply
		if err = json.Unmarshal(response, &reply); err != nil {
			return throttleResult{Err: err}, false
		}
		if reply.Error != "" {
			reply.Result.Err = errors.New(reply.Error)
		}
		return reply.Result, true
	}
	return guard
}

func (guard *ThrottleGuard) Relay(encoded []byte) ([]byte, error) {
	if len(encoded) > 64<<10 {
		return nil, errors.New("throttle request is too large")
	}
	var request throttleRequest
	if err := json.Unmarshal(encoded, &request); err != nil {
		return nil, err
	}
	var result throttleResult
	switch request.Operation {
	case throttleSnapshot, throttleHistory:
		result.Throttles, result.Err = guard.collect(request.Now, request.Operation)
	case throttleObserve, throttleRemove:
		var accepted bool
		result, accepted = guard.exchangeAdmin(request)
		if !accepted {
			return nil, errors.New("throttle guard is busy")
		}
	default:
		return nil, errors.New("unknown throttle operation")
	}
	reply := throttleReply{Result: result}
	if result.Err != nil {
		reply.Error = result.Err.Error()
	}
	return json.Marshal(reply)
}
