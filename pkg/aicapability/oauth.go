package aicapability

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// OAuth is a transport binding of an existing capability. Its transient codes
// and durable refresh references are owned by the capability process, never by
// an MCP-specific permission registry.
type OAuthClient struct {
	ID           string    `json:"client_id"`
	Domain       string    `json:"domain,omitempty"`
	Name         string    `json:"client_name,omitempty"`
	RedirectURIs []string  `json:"redirect_uris"`
	Expires      time.Time `json:"expires,omitempty"`
}

type OAuthAuthorization struct {
	ClientID    string
	RedirectURI string
	Resource    string
	State       string
	Challenge   string
	Scopes      []string
}

type OAuthTokens struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

type OAuthResult struct {
	Client     OAuthClient
	Pairing    Pairing
	Redirect   string
	Tokens     OAuthTokens
	Session    Session
	Capability Capability
}

type oauthRequest struct {
	action        string
	domain        string
	client        OAuthClient
	authorization OAuthAuthorization
	token         string
	verifier      string
	resource      string
	redirect      string
	clientID      string
}

type oauthApproval struct {
	OAuthAuthorization
	PairingKey string
	Expires    time.Time
}

type oauthCode struct {
	OAuthAuthorization
	CapabilityID string
	Expires      time.Time
}

type oauthRefresh struct {
	CapabilityID string    `json:"capability_id"`
	ClientID     string    `json:"client_id"`
	Domain       string    `json:"domain"`
	Resource     string    `json:"resource"`
	Family       string    `json:"family"`
	Scopes       []string  `json:"scopes"`
	Expires      time.Time `json:"expires"`
	Used         bool      `json:"used,omitempty"`
}

type oauthState struct {
	Clients   map[string]OAuthClient   `json:"clients"`
	Refresh   map[string]oauthRefresh  `json:"refresh"`
	Approvals map[string]oauthApproval `json:"-"`
	Codes     map[string]oauthCode     `json:"-"`
	loadError error
}

func (manager *Manager) oauthExchange(request oauthRequest) (OAuthResult, error) {
	request.domain = normalizeDomain(request.domain)
	response := manager.request(managerRequest{oauth: &request, reply: make(chan managerResponse, 1)})
	return response.oauth, response.err
}

func (manager *Manager) RegisterOAuthClient(domain string, client OAuthClient) (OAuthClient, error) {
	response, err := manager.oauthExchange(oauthRequest{action: "register", domain: domain, client: client})
	return response.Client, err
}

func (manager *Manager) OAuthClient(domain, clientID string) (OAuthClient, error) {
	response, err := manager.oauthExchange(oauthRequest{action: "client", domain: domain, clientID: clientID})
	return response.Client, err
}

func (manager *Manager) BeginOAuth(domain string, authorization OAuthAuthorization) (Pairing, error) {
	response, err := manager.oauthExchange(oauthRequest{action: "authorize", domain: domain, authorization: authorization})
	return response.Pairing, err
}

// OAuthApprovalRedirect is empty for legacy device pairing. The browser sees
// only a one-time PKCE code; the underlying capability never enters its URL.
func (manager *Manager) OAuthApprovalRedirect(domain, verification string) (string, error) {
	response, err := manager.oauthExchange(oauthRequest{action: "approval", domain: domain, token: verification})
	return response.Redirect, err
}

func (manager *Manager) ExchangeOAuthCode(domain, clientID, resource, redirect, code, verifier string) (OAuthTokens, error) {
	response, err := manager.oauthExchange(oauthRequest{action: "code", domain: domain, clientID: clientID, resource: resource, redirect: redirect, token: code, verifier: verifier})
	return response.Tokens, err
}

func (manager *Manager) RefreshOAuth(domain, clientID, resource, refresh string) (OAuthTokens, error) {
	response, err := manager.oauthExchange(oauthRequest{action: "refresh", domain: domain, clientID: clientID, resource: resource, token: refresh})
	return response.Tokens, err
}

func (manager *Manager) ValidateOAuth(domain, resource, bearer string) (Session, Capability, error) {
	response, err := manager.oauthExchange(oauthRequest{action: "validate", domain: domain, resource: resource, token: bearer})
	return response.Session, response.Capability, err
}

func (manager *Manager) RevokeOAuth(domain, clientID, token string) error {
	_, err := manager.oauthExchange(oauthRequest{action: "revoke", domain: domain, clientID: clientID, token: token})
	return err
}

func validOAuthRedirect(address string) bool {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || len(address) > 2048 {
		return false
	}
	// Callback origins also enter the browser's form-action policy. Accept DNS
	// ASCII (including punycode) and IP authority characters, never CSP syntax.
	for _, character := range parsed.Host {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune(".-:[]", character) {
			continue
		}
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	return parsed.Scheme == "http" && (parsed.Hostname() == "localhost" || net.ParseIP(parsed.Hostname()).IsLoopback())
}

func (manager *Manager) loadOAuth() *oauthState {
	state := &oauthState{Clients: make(map[string]OAuthClient), Refresh: make(map[string]oauthRefresh), Approvals: make(map[string]oauthApproval), Codes: make(map[string]oauthCode)}
	if manager.persistentPath == "" {
		return state
	}
	encoded, err := os.ReadFile(manager.persistentPath + ".oauth")
	if errors.Is(err, os.ErrNotExist) {
		return state
	}
	if err != nil {
		state.loadError = errors.New("OAuth storage unavailable")
		return state
	}
	if json.Unmarshal(encoded, state) != nil || state.Clients == nil || state.Refresh == nil {
		state.loadError = errors.New("OAuth storage is invalid")
	}
	state.prune(time.Now())
	return state
}

func (manager *Manager) saveOAuth(state *oauthState) error {
	if manager.persistentPath == "" {
		return nil
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	// Capability issuance creates the private parent directory before grants are
	// committed. Registration may precede the first capability.
	if err := os.MkdirAll(filepath.Dir(manager.persistentPath), 0700); err != nil {
		return err
	}
	filename := manager.persistentPath + ".oauth"
	file, err := os.OpenFile(filename+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("cannot persist OAuth binding")
	}
	if _, err = file.Write(encoded); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(filename+".tmp", filename)
	}
	// Refresh rotation must survive a crash after the response. Windows does
	// not support syncing directory handles through os.File.
	if err == nil && runtime.GOOS != "windows" {
		directory, openErr := os.Open(filepath.Dir(filename))
		err = openErr
		if err == nil {
			err = directory.Sync()
			closeErr = directory.Close()
			if err == nil {
				err = closeErr
			}
		}
	}
	if err != nil {
		_ = os.Remove(filename + ".tmp")
		return errors.New("cannot persist OAuth binding")
	}
	return nil
}

func (state *oauthState) prune(now time.Time) {
	for key, client := range state.Clients {
		if !client.Expires.IsZero() && !now.Before(client.Expires) {
			delete(state.Clients, key)
		}
	}
	for verification, approval := range state.Approvals {
		if !now.Before(approval.Expires) {
			delete(state.Approvals, verification)
		}
	}
	for code, grant := range state.Codes {
		if !now.Before(grant.Expires) {
			delete(state.Codes, code)
		}
	}
	for token, grant := range state.Refresh {
		if !now.Before(grant.Expires) {
			delete(state.Refresh, token)
		}
	}
}

func oauthFailure(message string) managerResponse { return managerResponse{err: errors.New(message)} }

func (manager *Manager) handleOAuth(request oauthRequest, state *oauthState, capabilities map[string]Capability, sessions map[string]Session, stops map[string]chan struct{}, pairings map[string]pairingState) managerResponse {
	if state.loadError != nil {
		return managerResponse{err: state.loadError}
	}
	state.prune(time.Now())
	if request.domain == "" {
		return oauthFailure("invalid_request")
	}
	clientKey := func(identifier string) string { return request.domain + "\n" + identifier }
	switch request.action {
	case "register":
		client := request.client
		if client.ID == "" {
			identifier, err := randomToken()
			if err != nil {
				return managerResponse{err: err}
			}
			client.ID = "sb_" + identifier
		}
		if len(client.ID) > 2048 || len(client.Name) > 128 || len(client.RedirectURIs) < 1 || len(client.RedirectURIs) > 8 {
			return oauthFailure("invalid_client_metadata")
		}
		for _, redirect := range client.RedirectURIs {
			if !validOAuthRedirect(redirect) {
				return oauthFailure("invalid_redirect_uri")
			}
		}
		key := clientKey(client.ID)
		if _, exists := state.Clients[key]; exists {
			return oauthFailure("client_already_registered")
		}
		if len(state.Clients) >= 4096 {
			return oauthFailure("registration_capacity_reached")
		}
		client.Domain = request.domain
		client.Expires = time.Now().Add(24 * time.Hour)
		client.RedirectURIs = append([]string(nil), client.RedirectURIs...)
		state.Clients[key] = client
		if err := manager.saveOAuth(state); err != nil {
			delete(state.Clients, key)
			return managerResponse{err: err}
		}
		client.RedirectURIs = append([]string(nil), client.RedirectURIs...)
		return managerResponse{oauth: OAuthResult{Client: client}}
	case "client":
		client, exists := state.Clients[clientKey(request.clientID)]
		if !exists {
			return oauthFailure("invalid_client")
		}
		client.RedirectURIs = append([]string(nil), client.RedirectURIs...)
		return managerResponse{oauth: OAuthResult{Client: client}}
	case "authorize":
		authorization := request.authorization
		client, exists := state.Clients[clientKey(authorization.ClientID)]
		if !exists {
			return oauthFailure("invalid_client")
		}
		redirectAllowed := false
		for _, redirect := range client.RedirectURIs {
			if redirect == authorization.RedirectURI {
				redirectAllowed = true
			}
		}
		challenge, err := base64.RawURLEncoding.DecodeString(authorization.Challenge)
		resource, resourceErr := url.Parse(authorization.Resource)
		if !redirectAllowed || err != nil || len(challenge) != 32 || resourceErr != nil || resource.Host == "" || resource.User != nil || resource.RawQuery != "" || resource.Fragment != "" || len(authorization.State) > 4096 {
			return oauthFailure("invalid_request")
		}
		if len(authorization.Scopes) == 0 {
			authorization.Scopes = StandardScopes()
		}
		if !scopesAreSubset(authorization.Scopes, StandardScopes()) {
			return oauthFailure("invalid_scope")
		}
		authorization.Scopes = append([]string(nil), authorization.Scopes...)
		response := manager.handlePairing(managerRequest{kind: "pairing-begin", domain: request.domain}, pairings, capabilities)
		if response.err != nil {
			return response
		}
		// Unused public registrations expire; live authorizations retain their
		// exact redirect binding for the refresh inactivity window.
		client.Expires = time.Now().Add(30 * 24 * time.Hour)
		state.Clients[clientKey(client.ID)] = client
		pairing := response.pairing
		pairing.ClientName = client.Name
		redirect, _ := url.Parse(authorization.RedirectURI)
		pairing.OAuthRedirectOrigin = redirect.Scheme + "://" + redirect.Host
		pairing.RequestedScopes = append([]string(nil), authorization.Scopes...)
		storedPairing := pairings[tokenHash(pairing.DeviceCode)]
		storedPairing.Pairing = pairing
		storedPairing.DeviceCode = ""
		pairings[tokenHash(pairing.DeviceCode)] = storedPairing
		state.Approvals[pairing.VerificationCode] = oauthApproval{OAuthAuthorization: authorization, PairingKey: tokenHash(pairing.DeviceCode), Expires: pairing.Expires}
		pairing.DeviceCode = ""
		pairing.RequestedScopes = append([]string(nil), pairing.RequestedScopes...)
		return managerResponse{oauth: OAuthResult{Pairing: pairing}}
	case "approval":
		approval, exists := state.Approvals[request.token]
		if !exists {
			return managerResponse{}
		}
		pairing, exists := pairings[approval.PairingKey]
		if !exists || pairing.Domain != request.domain {
			return oauthFailure("invalid_request")
		}
		if pairing.Status == "pending" {
			return managerResponse{}
		}
		callback, _ := url.Parse(approval.RedirectURI)
		query := callback.Query()
		query.Set("state", approval.State)
		resource, _ := url.Parse(approval.Resource)
		query.Set("iss", resource.Scheme+"://"+resource.Host)
		if pairing.Status == "denied" {
			query.Set("error", "access_denied")
		} else if pairing.Status == "approved" {
			code, err := randomToken()
			if err != nil {
				return managerResponse{err: err}
			}
			state.Codes[tokenHash(code)] = oauthCode{OAuthAuthorization: approval.OAuthAuthorization, CapabilityID: pairing.capabilityID, Expires: time.Now().Add(time.Minute)}
			query.Set("code", code)
		} else {
			return oauthFailure("invalid_request")
		}
		delete(state.Approvals, request.token)
		pairing.Status, pairing.capabilityToken = "connected", ""
		pairings[approval.PairingKey] = pairing
		callback.RawQuery = query.Encode()
		return managerResponse{oauth: OAuthResult{Redirect: callback.String()}}
	case "code":
		key := tokenHash(request.token)
		code, exists := state.Codes[key]
		if !exists {
			return oauthFailure("invalid_grant")
		}
		digest := sha256.Sum256([]byte(request.verifier))
		if len(request.verifier) < 43 || len(request.verifier) > 128 || base64.RawURLEncoding.EncodeToString(digest[:]) != code.Challenge || request.clientID != code.ClientID || request.resource != code.Resource || request.redirect != code.RedirectURI {
			return oauthFailure("invalid_grant")
		}
		capability, exists := capabilities[code.CapabilityID]
		if !exists || capability.Revoked || capability.Domain != request.domain {
			return oauthFailure("invalid_grant")
		}
		delete(state.Codes, key)
		family, err := randomToken()
		if err != nil {
			return managerResponse{err: err}
		}
		grant := oauthRefresh{CapabilityID: capability.ID, ClientID: code.ClientID, Domain: request.domain, Resource: code.Resource, Family: family, Scopes: intersectScopes(code.Scopes, capability.Scopes), Expires: time.Now().Add(30 * 24 * time.Hour)}
		return manager.issueOAuthTokens(state, grant, capability, sessions, stops)
	case "refresh":
		key := tokenHash(request.token)
		grant, exists := state.Refresh[key]
		if !exists || grant.Domain != request.domain || grant.ClientID != request.clientID || grant.Resource != request.resource {
			return oauthFailure("invalid_grant")
		}
		if grant.Used {
			manager.endOAuthFamily(state, grant.Family, sessions, stops)
			if err := manager.saveOAuth(state); err != nil {
				state.loadError = err
			}
			return oauthFailure("invalid_grant")
		}
		capability, exists := capabilities[grant.CapabilityID]
		if !exists || capability.Revoked || capability.Domain != request.domain {
			return oauthFailure("invalid_grant")
		}
		previous := grant
		grant.Used = true
		state.Refresh[key] = grant
		grant.Used = false
		grant.Scopes = intersectScopes(grant.Scopes, capability.Scopes)
		grant.Expires = time.Now().Add(30 * 24 * time.Hour)
		response := manager.issueOAuthTokens(state, grant, capability, sessions, stops)
		if response.err != nil {
			state.Refresh[key] = previous
		}
		return response
	case "validate":
		session, exists := sessions[tokenHash(request.token)]
		capability, valid := capabilities[session.CapabilityID]
		if !exists || !valid || capability.Revoked || session.Domain != request.domain || capability.Domain != request.domain || session.Resource != request.resource || session.ClientID == "" || !time.Now().Before(session.Expires) {
			return oauthFailure("invalid_token")
		}
		session.Scopes = intersectScopes(session.Scopes, capability.Scopes)
		capability.Scopes = append([]string(nil), capability.Scopes...)
		return managerResponse{oauth: OAuthResult{Session: session, Capability: capability}}
	case "revoke":
		grant, exists := state.Refresh[tokenHash(request.token)]
		if !exists {
			session, found := sessions[tokenHash(request.token)]
			if !found || session.Domain != request.domain || session.ClientID != request.clientID {
				return managerResponse{}
			}
			grant = oauthRefresh{Domain: session.Domain, ClientID: session.ClientID, Family: session.OAuthFamily}
		}
		if grant.Domain != request.domain || grant.ClientID != request.clientID {
			return managerResponse{}
		}
		manager.endOAuthFamily(state, grant.Family, sessions, stops)
		if err := manager.saveOAuth(state); err != nil {
			state.loadError = err
			return managerResponse{err: err}
		}
		return managerResponse{}
	}
	return oauthFailure("invalid_request")
}

func (manager *Manager) issueOAuthTokens(state *oauthState, grant oauthRefresh, capability Capability, sessions map[string]Session, stops map[string]chan struct{}) managerResponse {
	if len(state.Refresh) >= 32768 || len(sessions) >= 4096 {
		return oauthFailure("temporarily_unavailable")
	}
	bearer, err := randomToken()
	if err != nil {
		return managerResponse{err: err}
	}
	refresh, err := randomToken()
	if err != nil {
		return managerResponse{err: err}
	}
	key := tokenHash(refresh)
	state.Refresh[key] = grant
	clientKey := grant.Domain + "\n" + grant.ClientID
	client, clientExists := state.Clients[clientKey]
	if clientExists {
		client.Expires = grant.Expires
		state.Clients[clientKey] = client
	}
	if err := manager.saveOAuth(state); err != nil {
		delete(state.Refresh, key)
		return managerResponse{err: err}
	}
	stopped := make(chan struct{})
	session := Session{Done: stopped, Token: bearer, CapabilityID: capability.ID, Domain: capability.Domain, Owner: capability.Owner, Scopes: append([]string(nil), grant.Scopes...), Created: time.Now().UTC(), Expires: time.Now().UTC().Add(15 * time.Minute), Resource: grant.Resource, ClientID: grant.ClientID, OAuthFamily: grant.Family}
	sessions[tokenHash(bearer)] = session
	stops[tokenHash(bearer)] = stopped
	return managerResponse{oauth: OAuthResult{Tokens: OAuthTokens{AccessToken: bearer, TokenType: "Bearer", ExpiresIn: 900, RefreshToken: refresh, Scope: strings.Join(session.Scopes, " ")}}}
}

func (manager *Manager) endOAuthFamily(state *oauthState, family string, sessions map[string]Session, stops map[string]chan struct{}) {
	for key, grant := range state.Refresh {
		if grant.Family == family {
			delete(state.Refresh, key)
		}
	}
	for key, session := range sessions {
		if session.OAuthFamily == family {
			stopSession(sessions, stops, key)
		}
	}
}
