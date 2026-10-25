package httpsecurity

import (
	"errors"
	"time"
)

type AttackRequest struct {
	StatusCode               int
	Domain, IP, Path, Method string
	Category, Description    string
	Trusted, PolicyTrust     bool
	Now                      time.Time
}

type AttackDecision struct {
	Block                            SecurityBlock
	Blocked, Allowed, AlreadyBlocked bool
}

// An unavailable owner is a failed decision, never an allow result. Counting
// stays synchronous so the request crossing a threshold cannot escape it.
func (guard *AttackGuard) Decide(request AttackRequest, done <-chan struct{}) (AttackDecision, error) {
	operation := attackGuardObserveFast
	if request.Category != "" {
		operation = attackGuardObserveIncident
	}
	result, accepted := guard.exchangeFast(attackGuardRequest{
		Operation: operation, Domain: normalizeSecurityDomain(request.Domain), IP: request.IP,
		Path: request.Path, Method: request.Method, Category: request.Category,
		Description: request.Description, Trusted: request.Trusted, PolicyTrust: request.PolicyTrust,
		Now: request.Now, StatusCode: request.StatusCode, Done: done,
	})
	if !accepted {
		return AttackDecision{}, errors.New("security decision unavailable")
	}
	return AttackDecision{Block: result.Block, Blocked: result.Blocked, Allowed: result.Allowed, AlreadyBlocked: result.AlreadyBlocked}, result.Err
}

type ThrottleRequest struct {
	IP              string
	Trusted, Exempt bool
	Now             time.Time
}

func (guard *ThrottleGuard) Decide(request ThrottleRequest, done <-chan struct{}) (ThrottleDecision, error) {
	result, accepted := guard.exchangeFast(throttleRequest{Operation: throttleObserve, IP: request.IP, Trusted: request.Trusted, ExemptObservation: request.Exempt, Now: request.Now, Done: done})
	if !accepted {
		return ThrottleDecision{}, errors.New("throttle decision unavailable")
	}
	return result.Decision, result.Err
}
