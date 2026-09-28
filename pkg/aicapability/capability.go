// Package aicapability implements the universal, scoped external AI editor protocol.
package aicapability

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	ScopeRead    = "read"
	ScopeWrite   = "write"
	ScopePublish = "publish"
)

type Manifest struct {
	Name         string           `json:"name"`
	Protocol     string           `json:"protocol"`
	Instructions string           `json:"instructions"`
	OpenAPIURL   string           `json:"openapi_url"`
	APIBase      string           `json:"api_base"`
	Domain       string           `json:"domain"`
	PagePath     string           `json:"page_path,omitempty"`
	Task         string           `json:"task,omitempty"`
	Scopes       []string         `json:"scopes"`
	Limits       map[string]int64 `json:"limits"`
	Operations   []string         `json:"operations"`
}

type Capability struct {
	ID       string    `json:"id"`
	Domain   string    `json:"domain"`
	Owner    string    `json:"owner,omitempty"`
	Scopes   []string  `json:"scopes"`
	Revoked  bool      `json:"revoked"`
	Created  time.Time `json:"created"`
	PagePath string    `json:"page_path,omitempty"`
	Task     string    `json:"task,omitempty"`
}

type Session struct {
	Token        string    `json:"token"`
	CapabilityID string    `json:"capability_id"`
	Domain       string    `json:"domain"`
	Owner        string    `json:"owner,omitempty"`
	Scopes       []string  `json:"scopes"`
	Created      time.Time `json:"created"`
	Expires      time.Time `json:"expires"`
}

type SessionInfo struct {
	ID           string    `json:"id"`
	CapabilityID string    `json:"capability_id"`
	Domain       string    `json:"domain"`
	Owner        string    `json:"owner,omitempty"`
	Scopes       []string  `json:"scopes"`
	Created      time.Time `json:"created"`
	Expires      time.Time `json:"expires"`
}

type managerRequest struct {
	kind       string
	domain     string
	owner      string
	scopes     []string
	token      string
	capability Capability
	session    Session
	sessionID  string
	duration   time.Duration
	newScopes  []string
	pagePath   string
	task       string
	sessions   []SessionInfo
	reply      chan managerResponse
}

type managerResponse struct {
	capability Capability
	session    Session
	sessions   []SessionInfo
	ok         bool
	err        error
}

type Manager struct {
	requests       chan managerRequest
	stop           chan struct{}
	persistentPath string
}

func NewManager() *Manager {
	return newManager("")
}

func NewPersistentManager(path string) *Manager {
	return newManager(strings.TrimSpace(path))
}

func newManager(persistentPath string) *Manager {
	manager := &Manager{requests: make(chan managerRequest), stop: make(chan struct{}), persistentPath: persistentPath}
	go manager.run()
	return manager
}

func (manager *Manager) run() {
	capabilities := map[string]Capability{}
	sessions := map[string]Session{}
	manager.loadCapabilities(capabilities)
	for {
		select {
		case <-manager.stop:
			return
		case request := <-manager.requests:
			switch request.kind {
			case "close":
				request.reply <- managerResponse{ok: true}
				close(manager.stop)
				return
			case "issue":
				token, err := randomToken()
				if err != nil {
					request.reply <- managerResponse{err: err}
					continue
				}
				capability := Capability{ID: tokenHash(token), Domain: request.domain, Owner: request.owner, Scopes: append([]string(nil), request.scopes...), Created: time.Now().UTC(), PagePath: request.pagePath, Task: request.task}
				capabilities[capability.ID] = capability
				if err := manager.saveCapabilities(capabilities); err != nil {
					delete(capabilities, capability.ID)
					request.reply <- managerResponse{err: err}
					continue
				}
				request.reply <- managerResponse{capability: capability, session: Session{Token: token}}
			case "exchange":
				capability, found := capabilities[tokenHash(request.token)]
				if !found || capability.Revoked || capability.Domain != request.domain {
					request.reply <- managerResponse{err: errors.New("capability is invalid")}
					continue
				}
				sessionToken, err := randomToken()
				if err != nil {
					request.reply <- managerResponse{err: err}
					continue
				}
				session := Session{Token: sessionToken, CapabilityID: capability.ID, Domain: capability.Domain, Owner: capability.Owner, Scopes: append([]string(nil), capability.Scopes...), Created: time.Now().UTC(), Expires: time.Now().UTC().Add(15 * time.Minute)}
				sessions[tokenHash(sessionToken)] = session
				request.reply <- managerResponse{session: session}
			case "lookup":
				session, found := sessions[tokenHash(request.token)]
				if !found || time.Now().UTC().After(session.Expires) || session.Domain != request.domain {
					request.reply <- managerResponse{err: errors.New("editor session is invalid")}
					continue
				}
				request.reply <- managerResponse{session: session}
			case "list":
				result := make([]SessionInfo, 0, len(sessions))
				now := time.Now().UTC()
				for sessionKey, session := range sessions {
					if session.Expires.Before(now) || session.Domain != request.domain || session.Owner != request.owner {
						if session.Expires.Before(now) {
							delete(sessions, sessionKey)
						}
						continue
					}
					result = append(result, SessionInfo{ID: sessionKey[:12], CapabilityID: session.CapabilityID, Domain: session.Domain, Owner: session.Owner, Scopes: append([]string(nil), session.Scopes...), Created: session.Created, Expires: session.Expires})
				}
				request.reply <- managerResponse{sessions: result}
			case "revoke-session":
				sessionKey, session, found := findSession(sessions, request.sessionID)
				if !found || session.Domain != request.domain || session.Owner != request.owner {
					request.reply <- managerResponse{err: errors.New("editor session is invalid")}
					continue
				}
				delete(sessions, sessionKey)
				request.reply <- managerResponse{ok: true}
			case "extend-session":
				sessionKey, session, found := findSession(sessions, request.sessionID)
				if !found || session.Domain != request.domain || session.Owner != request.owner {
					request.reply <- managerResponse{err: errors.New("editor session is invalid")}
					continue
				}
				if request.duration <= 0 || request.duration > 24*time.Hour {
					request.reply <- managerResponse{err: errors.New("session extension is invalid")}
					continue
				}
				session.Expires = time.Now().UTC().Add(request.duration)
				sessions[sessionKey] = session
				request.reply <- managerResponse{session: session}
			case "restrict-session":
				sessionKey, session, found := findSession(sessions, request.sessionID)
				if !found || session.Domain != request.domain || session.Owner != request.owner || !scopesAreSubset(request.newScopes, session.Scopes) {
					request.reply <- managerResponse{err: errors.New("session restriction is invalid")}
					continue
				}
				session.Scopes = append([]string(nil), request.newScopes...)
				sessions[sessionKey] = session
				request.reply <- managerResponse{session: session}
			case "validate-capability":
				capability, found := capabilities[tokenHash(request.token)]
				if !found || capability.Revoked || capability.Domain != request.domain {
					request.reply <- managerResponse{err: errors.New("capability is invalid")}
					continue
				}
				request.reply <- managerResponse{capability: capability}
			case "revoke":
				capability, found := capabilities[tokenHash(request.token)]
				if !found {
					request.reply <- managerResponse{err: errors.New("capability is invalid")}
					continue
				}
				capability.Revoked = true
				capabilities[capability.ID] = capability
				if err := manager.saveCapabilities(capabilities); err != nil {
					capability.Revoked = false
					capabilities[capability.ID] = capability
					request.reply <- managerResponse{err: err}
					continue
				}
				for sessionKey, session := range sessions {
					if session.CapabilityID == capability.ID {
						delete(sessions, sessionKey)
					}
				}
				request.reply <- managerResponse{ok: true}
			case "revoke-id":
				capability, found := capabilities[request.sessionID]
				if !found || capability.Domain != request.domain || capability.Owner != request.owner {
					request.reply <- managerResponse{err: errors.New("capability is invalid")}
					continue
				}
				capability.Revoked = true
				capabilities[capability.ID] = capability
				if err := manager.saveCapabilities(capabilities); err != nil {
					capability.Revoked = false
					capabilities[capability.ID] = capability
					request.reply <- managerResponse{err: err}
					continue
				}
				for sessionKey, session := range sessions {
					if session.CapabilityID == capability.ID {
						delete(sessions, sessionKey)
					}
				}
				request.reply <- managerResponse{ok: true}
			}
		}
	}
}

func (manager *Manager) loadCapabilities(capabilities map[string]Capability) {
	if manager.persistentPath == "" {
		return
	}
	data, err := os.ReadFile(manager.persistentPath)
	if err != nil {
		return
	}
	var stored map[string]Capability
	if json.Unmarshal(data, &stored) == nil {
		for capabilityID, capability := range stored {
			if !capability.Revoked {
				capabilities[capabilityID] = capability
			}
		}
	}
}

func (manager *Manager) saveCapabilities(capabilities map[string]Capability) error {
	if manager.persistentPath == "" {
		return nil
	}
	data, err := json.Marshal(capabilities)
	if err != nil {
		return err
	}
	temporaryPath := manager.persistentPath + ".tmp"
	if err := os.MkdirAll(filepath.Dir(manager.persistentPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(temporaryPath, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, manager.persistentPath); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	return nil
}

func (manager *Manager) request(request managerRequest) managerResponse {
	if manager == nil {
		return managerResponse{err: errors.New("capability manager is unavailable")}
	}
	select {
	case manager.requests <- request:
		return <-request.reply
	case <-manager.stop:
		return managerResponse{err: errors.New("capability manager is stopped")}
	}
}

func (manager *Manager) Issue(domain string, scopes []string) (string, Capability, error) {
	return manager.IssueFor(domain, "", scopes)
}

func (manager *Manager) IssueFor(domain, owner string, scopes []string) (string, Capability, error) {
	return manager.IssueForTask(domain, owner, scopes, "", "")
}

func (manager *Manager) IssueForTask(domain, owner string, scopes []string, pagePath, task string) (string, Capability, error) {
	response := manager.request(managerRequest{kind: "issue", domain: normalizeDomain(domain), owner: strings.ToLower(strings.TrimSpace(owner)), scopes: scopes, pagePath: strings.TrimSpace(pagePath), task: strings.TrimSpace(task), reply: make(chan managerResponse, 1)})
	return response.session.Token, response.capability, response.err
}

func (manager *Manager) Exchange(token, domain string) (Session, error) {
	response := manager.request(managerRequest{kind: "exchange", token: token, domain: normalizeDomain(domain), reply: make(chan managerResponse, 1)})
	return response.session, response.err
}

func (manager *Manager) ValidateCapability(token, domain string) (Capability, error) {
	response := manager.request(managerRequest{kind: "validate-capability", token: token, domain: normalizeDomain(domain), reply: make(chan managerResponse, 1)})
	return response.capability, response.err
}

func (manager *Manager) Lookup(sessionToken, domain string) (Session, error) {
	response := manager.request(managerRequest{kind: "lookup", token: sessionToken, domain: normalizeDomain(domain), reply: make(chan managerResponse, 1)})
	return response.session, response.err
}

// ValidateSession binds the short-lived session to the capability URL that was used to create it.
// This prevents a valid session from one capability link being replayed through another link.
func (manager *Manager) ValidateSession(sessionToken, capabilityToken, domain string) (Session, error) {
	session, err := manager.Lookup(sessionToken, domain)
	if err != nil {
		return Session{}, err
	}
	if capabilityToken == "" || session.CapabilityID != tokenHash(capabilityToken) {
		return Session{}, errors.New("editor session is not bound to capability")
	}
	return session, nil
}

func (manager *Manager) Revoke(token string) error {
	response := manager.request(managerRequest{kind: "revoke", token: token, reply: make(chan managerResponse, 1)})
	return response.err
}

func (manager *Manager) List(owner, domain string) ([]SessionInfo, error) {
	response := manager.request(managerRequest{kind: "list", owner: strings.ToLower(strings.TrimSpace(owner)), domain: normalizeDomain(domain), reply: make(chan managerResponse, 1)})
	return response.sessions, response.err
}

func (manager *Manager) RevokeSession(sessionToken, owner, domain string) error {
	response := manager.request(managerRequest{kind: "revoke-session", sessionID: sessionToken, owner: strings.ToLower(strings.TrimSpace(owner)), domain: normalizeDomain(domain), reply: make(chan managerResponse, 1)})
	return response.err
}

func (manager *Manager) ExtendSession(sessionToken, owner, domain string, duration time.Duration) error {
	response := manager.request(managerRequest{kind: "extend-session", sessionID: sessionToken, owner: strings.ToLower(strings.TrimSpace(owner)), domain: normalizeDomain(domain), duration: duration, reply: make(chan managerResponse, 1)})
	return response.err
}

func (manager *Manager) RestrictSession(sessionToken, owner, domain string, scopes []string) error {
	response := manager.request(managerRequest{kind: "restrict-session", sessionID: sessionToken, owner: strings.ToLower(strings.TrimSpace(owner)), domain: normalizeDomain(domain), newScopes: scopes, reply: make(chan managerResponse, 1)})
	return response.err
}

func (manager *Manager) RevokeCapabilityID(capabilityID, owner, domain string) error {
	response := manager.request(managerRequest{kind: "revoke-id", sessionID: strings.TrimSpace(capabilityID), owner: strings.ToLower(strings.TrimSpace(owner)), domain: normalizeDomain(domain), reply: make(chan managerResponse, 1)})
	return response.err
}

func (manager *Manager) Close() {
	if manager == nil {
		return
	}
	reply := make(chan managerResponse, 1)
	select {
	case manager.requests <- managerRequest{kind: "close", reply: reply}:
		<-reply
	case <-manager.stop:
	}
}

func ManifestJSON(manifest Manifest) ([]byte, error) { return json.Marshal(manifest) }

func ManifestResponse(response http.ResponseWriter, request *http.Request, manifest Manifest) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	if strings.Contains(request.Header.Get("Accept"), "text/plain") {
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = response.Write([]byte(manifest.Instructions))
		return
	}
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(manifest)
}

func tokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
func randomToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}
func normalizeDomain(domain string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
}

func scopesAreSubset(candidate, current []string) bool {
	for _, requestedScope := range candidate {
		found := false
		for _, currentScope := range current {
			if requestedScope == currentScope {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func findSession(sessions map[string]Session, sessionID string) (string, Session, bool) {
	trimmedID := strings.TrimSpace(sessionID)
	if trimmedID == "" {
		return "", Session{}, false
	}
	var foundKey string
	var foundSession Session
	for sessionKey, session := range sessions {
		if !strings.HasPrefix(sessionKey, trimmedID) {
			continue
		}
		if foundKey != "" {
			return "", Session{}, false
		}
		foundKey = sessionKey
		foundSession = session
	}
	return foundKey, foundSession, foundKey != ""
}
