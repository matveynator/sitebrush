package httpsecurity

import (
	"encoding/json"
	"errors"
)

type attackGuardReply struct {
	Result attackGuardResult
	Error  string
}

// NewRemoteAttackGuard transfers jobs to the installation's sole security
// participant. The public process retains ownership across database restarts.
func NewRemoteAttackGuard(exchange func([]byte) ([]byte, error)) *AttackGuard {
	guard := &AttackGuard{shards: make([]chan attackGuardRequest, 1)}
	guard.remote = func(request attackGuardRequest) (attackGuardResult, bool) {
		encoded, err := json.Marshal(request)
		if err != nil {
			return attackGuardResult{Err: err}, false
		}
		response, err := exchange(encoded)
		if err != nil {
			return attackGuardResult{Err: err}, false
		}
		var reply attackGuardReply
		if err = json.Unmarshal(response, &reply); err != nil {
			return attackGuardResult{Err: err}, false
		}
		if reply.Error != "" {
			reply.Result.Err = errors.New(reply.Error)
		}
		return reply.Result, true
	}
	return guard
}

// Relay keeps the existing local and remote adapters on the same shard owners.
func (guard *AttackGuard) Relay(encoded []byte) ([]byte, error) {
	if len(encoded) > 64<<10 {
		return nil, errors.New("security request is too large")
	}
	var request attackGuardRequest
	if err := json.Unmarshal(encoded, &request); err != nil {
		return nil, err
	}
	var result attackGuardResult
	switch request.Operation {
	case attackGuardSnapshot:
		result.Blocks, result.Err = guard.Snapshot(request.Now)
	case attackGuardAllowSnapshot:
		result.Allowlist, result.Err = guard.Allowlist()
	case attackGuardSetSettings:
		result.Err = guard.SetSettings(request.Settings)
	default:
		if request.Operation > attackGuardFinishIncidentReport {
			return nil, errors.New("unknown security operation")
		}
		var accepted bool
		result, accepted = guard.exchangeAdmin(request)
		if !accepted {
			return nil, errors.New("security guard is busy")
		}
		if result.Changed {
			guard.signalSave()
		}
	}
	reply := attackGuardReply{Result: result}
	if result.Err != nil {
		reply.Error = result.Err.Error()
	}
	return json.Marshal(reply)
}
