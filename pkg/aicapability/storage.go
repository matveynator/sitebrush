package aicapability

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
)

// Storage is implemented by the site's control worker. Both documents commit
// together; temporary pairing, codes and bearer sessions never cross it.
type Storage interface {
	Load() (capabilities, oauth []byte, err error)
	Save(capabilities, oauth []byte) error
}

func NewStoredManager(storage Storage) *Manager {
	manager := &Manager{requests: make(chan managerRequest), stop: make(chan struct{}), storage: storage}
	manager.done = make(chan struct{})
	go manager.run()
	return manager
}

// NewRoutedManager is a facade: resolution admits only registered sites and
// returns their existing security participant, never an HTTP-owned database.
func NewRoutedManager(resolve func(string) (*Manager, error), domains func() []string) *Manager {
	return &Manager{resolve: resolve, domains: domains}
}

func (manager *Manager) routedRequest(request managerRequest) managerResponse {
	domain := request.domain
	if request.oauth != nil {
		domain = request.oauth.domain
	}
	if domain != "" {
		participant, err := manager.resolve(domain)
		if err != nil {
			return managerResponse{err: err}
		}
		return participant.request(request)
	}
	if request.kind == "close" {
		return managerResponse{ok: true}
	}
	// The legacy token-only revoke API has no site selector. Search the bounded
	// registered set without constructing any participant from a Host header.
	if request.kind == "revoke" {
		for _, registered := range manager.domains() {
			participant, err := manager.resolve(registered)
			if err != nil {
				return managerResponse{err: err}
			}
			response := participant.request(request)
			if response.err == nil {
				return response
			}
		}
	}
	return managerResponse{err: errors.New("capability site is required")}
}

func (manager *Manager) persist() error {
	if manager.storage == nil {
		return nil
	}
	capabilities, err := json.Marshal(manager.ownedCapabilities)
	if err != nil {
		return err
	}
	oauth, err := json.Marshal(manager.ownedOAuth)
	if err != nil {
		return err
	}
	return manager.storage.Save(capabilities, oauth)
}

// SplitLegacy validates the complete legacy source before a caller commits any
// site. Revoked hashes and used refresh hashes remain present for replay checks.
func SplitLegacy(capabilitiesJSON, oauthJSON []byte) (map[string][2][]byte, error) {
	capabilities := map[string]Capability{}
	state := &oauthState{Clients: map[string]OAuthClient{}, Refresh: map[string]oauthRefresh{}}
	if len(capabilitiesJSON) > 0 && json.Unmarshal(capabilitiesJSON, &capabilities) != nil {
		return nil, errors.New("invalid legacy capabilities")
	}
	if len(oauthJSON) > 0 && json.Unmarshal(oauthJSON, state) != nil {
		return nil, errors.New("invalid legacy OAuth")
	}
	if capabilities == nil || state.Clients == nil || state.Refresh == nil || bytes.Equal(bytes.TrimSpace(oauthJSON), []byte("null")) {
		return nil, errors.New("invalid legacy AI storage")
	}
	byDomain := map[string]map[string]Capability{}
	oauthByDomain := map[string]*oauthState{}
	ensure := func(domain string) error {
		if domain == "" || normalizeDomain(domain) != domain {
			return errors.New("invalid legacy site binding")
		}
		if byDomain[domain] == nil {
			byDomain[domain] = map[string]Capability{}
			oauthByDomain[domain] = &oauthState{Clients: map[string]OAuthClient{}, Refresh: map[string]oauthRefresh{}}
		}
		return nil
	}
	for key, capability := range capabilities {
		if key != capability.ID || !validCredentialHash(key) {
			return nil, errors.New("invalid legacy capability hash")
		}
		if err := ensure(capability.Domain); err != nil {
			return nil, err
		}
		byDomain[capability.Domain][key] = capability
	}
	for key, client := range state.Clients {
		if err := ensure(client.Domain); err != nil {
			return nil, err
		}
		if key != client.Domain+"\n"+client.ID {
			return nil, errors.New("invalid legacy client binding")
		}
		oauthByDomain[client.Domain].Clients[key] = client
	}
	for key, grant := range state.Refresh {
		if !validCredentialHash(key) {
			return nil, errors.New("invalid legacy refresh hash")
		}
		if err := ensure(grant.Domain); err != nil {
			return nil, err
		}
		capability, found := capabilities[grant.CapabilityID]
		if !found || capability.Domain != grant.Domain {
			return nil, errors.New("invalid legacy refresh binding")
		}
		oauthByDomain[grant.Domain].Refresh[key] = grant
	}
	result := map[string][2][]byte{}
	for domain, grants := range byDomain {
		encodedGrants, err := json.Marshal(grants)
		if err != nil {
			return nil, err
		}
		encodedOAuth, err := json.Marshal(oauthByDomain[domain])
		if err != nil {
			return nil, err
		}
		result[domain] = [2][]byte{encodedGrants, encodedOAuth}
	}
	return result, nil
}

func validCredentialHash(hash string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(hash)
	return err == nil && len(decoded) == 32 && base64.RawURLEncoding.EncodeToString(decoded) == hash
}

func (manager *Manager) Reload() error {
	if manager == nil || manager.storage == nil {
		return errors.New("site control storage is unavailable")
	}
	return manager.request(managerRequest{kind: "reload-storage", reply: make(chan managerResponse, 1)}).err
}
