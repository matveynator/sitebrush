package updateannounce

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLiveContactAuthenticatesAndReturnsCurrentVersion(t *testing.T) {
	central, centralKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	peer, peerKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := probe.Addr().String()
	probe.Close()
	stop := make(chan struct{})
	defer close(stop)
	hub := NewHub(stop)
	listenerDone := make(chan error, 1)
	go func() {
		listenerDone <- hub.Listen(address, centralKey, func(id string) (ed25519.PublicKey, error) { return peer, nil }, stop)
	}()
	agentDone := make(chan error, 1)
	connectionStatuses := make(chan string, 1)
	versions := make(chan string, 2)
	requests := make(chan chan string)
	go func() {
		version := "1"
		for {
			select {
			case <-stop:
				return
			case version = <-versions:
			case reply := <-requests:
				reply <- version
			}
		}
	}()
	status := func() (Status, error) {
		reply := make(chan string, 1)
		select {
		case requests <- reply:
		case <-stop:
			return Status{}, net.ErrClosed
		}
		return Status{Version: <-reply, PanelURL: "https://server.example/?server_update", Enabled: true, Owners: []Owner{{Email: "owner@example.com", Language: "fr"}}}, nil
	}
	go func() {
		for {
			err := AgentWithStatus(address, "server-1", peerKey, central, status, connectionStatuses, stop)
			select {
			case <-stop:
				agentDone <- err
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	var current Status
	for {
		current, err = hub.Check("server-1", peer, stop)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if current.Version != "1" || current.Owners[0].Language != "fr" {
		t.Fatal("wrong signed status", current)
	}
	select {
	case status := <-connectionStatuses:
		if status != "" {
			t.Fatal("successful contact did not clear the notification warning", status)
		}
	case <-time.After(time.Second):
		t.Fatal("successful contact did not report its availability")
	}
	installations := []Installation{{ID: "server-1", Key: base64.StdEncoding.EncodeToString(peer), Domain: "server.example"}}
	selection, err := hub.Select(installations, "21", "owner@example.com", stop)
	if err != nil || len(selection.Servers) != 1 || selection.Language != "fr" {
		t.Fatal("outdated owner did not qualify", selection, err)
	}
	versions <- "21"
	deadline = time.Now().Add(3 * time.Second)
	for {
		current, err = hub.Check("server-1", peer, stop)
		if err != nil {
			t.Fatal(err)
		}
		if current.Version == "21" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cached registry version was reused")
		}
	}
	if _, eligible, err := Eligible(current, "21", "owner@example.com"); err != nil || eligible {
		t.Fatal("already updated owner selected", err)
	}
	selection, err = hub.Select(installations, "21", "owner@example.com", stop)
	if err != nil || selection.Waiting || len(selection.Servers) != 0 {
		t.Fatal("queued campaign would send an empty email after a manual upgrade", selection, err)
	}
	stranger, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := hub.Check("server-1", stranger, stop); err == nil {
		t.Fatal("wrong signing key accepted")
	}
	select {
	case err := <-listenerDone:
		t.Fatalf("listener exited: %v", err)
	default:
	}
}

func TestEligibilityDefersUnknownBusyAndUnsafeInstallations(t *testing.T) {
	base := Status{Version: "1", PanelURL: "https://server.example/?server_update", Enabled: true, Owners: []Owner{{Email: "owner@example.com", Language: "he"}}}
	owner, eligible, err := Eligible(base, "21", "owner@example.com")
	if err != nil || !eligible || owner.Language != "he" {
		t.Fatal(owner, eligible, err)
	}
	for _, version := range []string{"21", "22"} {
		status := base
		status.Version = version
		if _, eligible, err := Eligible(status, "21", "owner@example.com"); err != nil || eligible {
			t.Fatal("current version announced", err)
		}
	}
	for _, version := range []string{"dev", "unknown", ""} {
		status := base
		status.Version = version
		if _, _, err := Eligible(status, "21", "owner@example.com"); err == nil {
			t.Fatal("unknown version treated as outdated")
		}
	}
	busy := base
	busy.Busy = true
	if _, _, err := Eligible(busy, "21", "owner@example.com"); err == nil {
		t.Fatal("busy installation selected")
	}
	if _, eligible, err := Eligible(base, "21", "removed-owner@example.com"); err != nil || eligible {
		t.Fatal("former owner selected")
	}
	for _, address := range []string{"http://server.example/?server_update", "https://user:secret@server.example/?server_update", "https://127.0.0.1/?server_update", "https://server.example/?server_update&secret=token", "https://server.example/?server_update#secret", "https://server.example/evil"} {
		if err := ValidatePanelURL(address); err == nil {
			t.Fatal("unsafe URL accepted", address)
		}
	}
}

func TestContactRejectsStaleReplayAndIdentitySubstitution(t *testing.T) {
	public, key, _ := ed25519.GenerateKey(rand.Reader)
	hello := Contact{InstallationID: "owner-server", At: time.Now().UTC(), Nonce: strings.Repeat("a", 32)}
	hello.Signature = ed25519.Sign(key, contactPayload(hello))
	if !verifyContact(hello, public) {
		t.Fatal("valid contact rejected")
	}
	for _, modify := range []func(*Contact){func(contact *Contact) { contact.InstallationID = "other" }, func(contact *Contact) { contact.At = contact.At.Add(-time.Hour) }, func(contact *Contact) { contact.Nonce = "" }} {
		candidate := hello
		modify(&candidate)
		if verifyContact(candidate, public) {
			t.Fatal("stale or substituted contact accepted")
		}
	}
}

func TestPinnedCentralTLSRejectsMissingOrSubstitutedIdentity(t *testing.T) {
	central, centralKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, peerKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := verifiedCentralTLS("installation", peerKey, central)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.VerifyConnection == nil || configuration.MinVersion != tls.VersionTLS13 {
		t.Fatal("missing mandatory identity verification")
	}
	identity, err := certificate("sitebrush.com", centralKey)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(identity.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := configuration.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{parsed}}); err != nil {
		t.Fatal("pinned identity rejected", err)
	}
	wrong, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, certificates := range [][]*x509.Certificate{nil, {parsed, parsed}, {{PublicKey: wrong}}, {{PublicKey: "not an Ed25519 key"}}} {
		if err := configuration.VerifyConnection(tls.ConnectionState{PeerCertificates: certificates}); err == nil {
			t.Fatal("untrusted central certificate accepted")
		}
	}
	for _, centralKey := range []ed25519.PublicKey{nil, make([]byte, 31)} {
		if _, err := verifiedCentralTLS("installation", peerKey, centralKey); err == nil {
			t.Fatal("invalid trust input accepted")
		}
	}
	if _, err := verifiedCentralTLS("installation", nil, central); err == nil {
		t.Fatal("missing local key accepted")
	}
	if err := Agent("unused", "installation", peerKey, central, nil, nil); err == nil {
		t.Fatal("missing status source accepted")
	}
	// A real TLS handshake must reject an active endpoint with a different key,
	// even though that endpoint owns its certificate's private key.
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("untrusted contact reached HTTP") }))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{identity}, MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	if err := Agent(server.Listener.Addr().String(), "installation", peerKey, wrong, func() (Status, error) { t.Error("untrusted endpoint requested status"); return Status{}, nil }, nil); err == nil {
		t.Fatal("active impostor TLS endpoint accepted")
	}
}

func TestLiveProofRejectsReplaySubstitutionAndRevokedContact(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"nonce", "installation", "past", "future", "signature", "closed"} {
		t.Run(scenario, func(t *testing.T) {
			stop := make(chan struct{})
			defer close(stop)
			hub := NewHub(stop)
			tasks := make(chan Probe, 1)
			hub.contacts <- Contact{InstallationID: "installation", Tasks: tasks}
			deadline := time.Now().Add(time.Second)
			for {
				barrier := make(chan chan<- Probe, 1)
				hub.lookups <- lookup{id: "installation", reply: barrier}
				if <-barrier != nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("contact not registered")
				}
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				task := <-tasks
				if scenario == "closed" {
					close(task.Reply)
					return
				}
				proof := Proof{Nonce: task.Nonce, At: time.Now().UTC(), Status: Status{InstallationID: "installation", Version: "1"}}
				switch scenario {
				case "nonce":
					proof.Nonce = "replayed"
				case "installation":
					proof.Status.InstallationID = "other"
				case "past":
					proof.At = proof.At.Add(-time.Hour)
				case "future":
					proof.At = proof.At.Add(time.Hour)
				}
				payload, _ := json.Marshal(proof)
				proof.Signature = ed25519.Sign(key, payload)
				if scenario == "signature" {
					proof.Signature[0] ^= 1
				}
				task.Reply <- proof
			}()
			if _, err := hub.Check("installation", public, stop); err == nil {
				t.Fatal("invalid proof accepted", scenario)
			}
			<-done
		})
	}
	var unavailable *Hub
	if _, err := unavailable.Check("installation", public, nil); err == nil {
		t.Fatal("unavailable hub answered")
	}
	stop := make(chan struct{})
	hub := NewHub(stop)
	close(stop)
	if _, err := hub.Check("installation", public, stop); err == nil {
		t.Fatal("canceled consumer answered")
	}
	revoked := make(chan Proof)
	close(revoked)
	if err := send(revoked, Proof{}, nil); err == nil {
		t.Fatal("revoked reply capability accepted")
	}
	for _, encoded := range []string{"invalid", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := DecodeKey(encoded); err == nil {
			t.Fatal("bad key decoded")
		}
	}
}
