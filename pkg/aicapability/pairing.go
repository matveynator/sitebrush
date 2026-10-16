package aicapability

import (
	"errors"
	"strings"
	"time"
)

const PairingLifetime = 10 * time.Minute
const PairingInterval = 3

var ErrPairingPending = errors.New("authorization_pending")
var ErrPairingSlowDown = errors.New("slow_down")
var ErrPairingDenied = errors.New("access_denied")
var ErrPairingInvalid = errors.New("expired_or_invalid_device_code")
var ErrPairingBusy = errors.New("too_many_pairing_requests")

// DeviceCode is delivered only to the initiating agent; VerificationCode only
// identifies the browser approval. Neither alone can approve or use a grant.
type Pairing struct {
	DeviceCode          string    `json:"device_code,omitempty"`
	VerificationCode    string    `json:"-"`
	Domain              string    `json:"domain"`
	Status              string    `json:"status"`
	Expires             time.Time `json:"expires"`
	Interval            int       `json:"interval"`
	ClientName          string    `json:"client_name,omitempty"`
	RequestedScopes     []string  `json:"requested_scopes,omitempty"`
	OAuthRedirectOrigin string    `json:"-"`
}

type pairingState struct {
	Pairing
	capabilityToken string
	capabilityID    string
	nextPoll        time.Time
}

func StandardScopes() []string {
	return []string{ScopeRead, ScopeWrite, ScopePublish, ScopeAnalytics}
}

func (manager *Manager) BeginPairing(domain string) (Pairing, error) {
	response := manager.request(managerRequest{kind: "pairing-begin", domain: normalizeDomain(domain), reply: make(chan managerResponse, 1)})
	return response.pairing, response.err
}

func (manager *Manager) PairingForApproval(domain, verificationCode string) (Pairing, error) {
	response := manager.request(managerRequest{kind: "pairing-view", domain: normalizeDomain(domain), token: verificationCode, reply: make(chan managerResponse, 1)})
	return response.pairing, response.err
}

func (manager *Manager) DecidePairing(domain, owner, verificationCode string, allow bool, scopes []string) error {
	decision := "pairing-deny"
	if allow {
		decision = "pairing-allow"
	}
	response := manager.request(managerRequest{kind: decision, domain: normalizeDomain(domain), owner: strings.ToLower(strings.TrimSpace(owner)), token: verificationCode, scopes: append([]string(nil), scopes...), reply: make(chan managerResponse, 1)})
	return response.err
}

func (manager *Manager) ClaimPairing(domain, deviceCode string) (string, error) {
	response := manager.request(managerRequest{kind: "pairing-claim", domain: normalizeDomain(domain), token: deviceCode, reply: make(chan managerResponse, 1)})
	return response.session.Token, response.err
}

// All pairing state, issuance and one-time consumption share the capability
// owner's select loop, so simultaneous claims cannot mint duplicate grants.
func (manager *Manager) handlePairing(request managerRequest, pairings map[string]pairingState, capabilities map[string]Capability) managerResponse {
	if request.domain == "" {
		return managerResponse{err: ErrPairingInvalid}
	}
	if request.kind == "pairing-begin" {
		if len(pairings) >= 512 {
			return managerResponse{err: ErrPairingBusy}
		}
		deviceCode, err := randomToken()
		if err != nil {
			return managerResponse{err: err}
		}
		verificationCode, err := randomToken()
		if err != nil {
			return managerResponse{err: err}
		}
		pairing := Pairing{Domain: request.domain, Status: "pending", Expires: time.Now().UTC().Add(PairingLifetime), Interval: PairingInterval, VerificationCode: verificationCode}
		pairings[tokenHash(deviceCode)] = pairingState{Pairing: pairing}
		pairing.DeviceCode = deviceCode
		return managerResponse{pairing: pairing}
	}
	if request.kind == "pairing-claim" {
		key := tokenHash(request.token)
		pairing, exists := pairings[key]
		if !exists || pairing.Domain != request.domain {
			return managerResponse{err: ErrPairingInvalid}
		}
		if pairing.Status == "denied" {
			return managerResponse{err: ErrPairingDenied}
		}
		if pairing.Status == "connected" {
			return managerResponse{err: ErrPairingInvalid}
		}
		if pairing.Status == "pending" {
			if time.Now().Before(pairing.nextPoll) {
				return managerResponse{err: ErrPairingSlowDown}
			}
			pairing.nextPoll = time.Now().Add(PairingInterval * time.Second)
			pairings[key] = pairing
			return managerResponse{err: ErrPairingPending}
		}
		capability, exists := capabilities[pairing.capabilityID]
		if !exists || capability.Revoked {
			return managerResponse{err: ErrPairingDenied}
		}
		token := pairing.capabilityToken
		pairing.Status, pairing.capabilityToken = "connected", ""
		pairings[key] = pairing
		return managerResponse{session: Session{Token: token}}
	}
	for key, pairing := range pairings {
		if pairing.Domain != request.domain || pairing.VerificationCode != request.token {
			continue
		}
		if request.kind == "pairing-view" {
			pairing.RequestedScopes = append([]string(nil), pairing.RequestedScopes...)
			return managerResponse{pairing: pairing.Pairing}
		}
		if pairing.Status != "pending" || request.owner == "" {
			return managerResponse{err: ErrPairingInvalid}
		}
		if request.kind == "pairing-deny" {
			pairing.Status = "denied"
			pairings[key] = pairing
			return managerResponse{ok: true}
		}
		if request.kind != "pairing-allow" || len(request.scopes) == 0 || !scopesAreSubset(request.scopes, StandardScopes()) {
			return managerResponse{err: errors.New("invalid permissions")}
		}
		if len(pairing.RequestedScopes) > 0 {
			request.scopes = intersectScopes(request.scopes, pairing.RequestedScopes)
			if len(request.scopes) == 0 {
				return managerResponse{err: errors.New("invalid permissions")}
			}
		}
		token, err := randomToken()
		if err != nil {
			return managerResponse{err: err}
		}
		capability := Capability{ID: tokenHash(token), Domain: request.domain, Owner: request.owner, Scopes: append([]string(nil), request.scopes...), Created: time.Now().UTC()}
		capabilities[capability.ID] = capability
		if err := manager.saveCapabilities(capabilities); err != nil {
			delete(capabilities, capability.ID)
			return managerResponse{err: err}
		}
		pairing.Status, pairing.capabilityToken, pairing.capabilityID = "approved", token, capability.ID
		pairings[key] = pairing
		return managerResponse{ok: true}
	}
	return managerResponse{err: ErrPairingInvalid}
}

func prunePairings(pairings map[string]pairingState, now time.Time) {
	for key, pairing := range pairings {
		if !now.Before(pairing.Expires) {
			delete(pairings, key)
		}
	}
}
