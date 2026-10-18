// Package updateannounce owns manually authorized release campaigns separately
// from the updater. A release check never grants permission to send mail.
package updateannounce

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/matveynator/netchan/v2"
	"github.com/matveynator/sitebrush/v2/pkg/netchantls"
	"github.com/matveynator/sitebrush/v2/pkg/netchantransport"
)

const Address = "sitebrush.com:9877"

type Owner struct {
	Email      string
	Language   string
	LanguageAt time.Time
}
type Status struct {
	InstallationID string
	Version        string
	PanelURL       string
	Owners         []Owner
	Busy           bool
	Enabled        bool
}
type Proof struct {
	Nonce     string
	At        time.Time
	Status    Status
	Signature []byte
}
type Probe struct {
	Nonce string
	Reply chan<- Proof
}
type Contact struct {
	InstallationID string
	Tasks          chan<- Probe
	Nonce          string
	At             time.Time
	Signature      []byte
}
type lookup struct {
	id    string
	reply chan chan<- Probe
}
type Hub struct {
	contacts chan Contact
	lookups  chan lookup
}

func NewHub(stop <-chan struct{}) *Hub {
	hub := &Hub{contacts: make(chan Contact, 32), lookups: make(chan lookup, 32)}
	go func() {
		contacts := make(map[string]chan<- Probe)
		for {
			select {
			case <-stop:
				return
			case contact := <-hub.contacts:
				if len(contacts) < 10000 || contacts[contact.InstallationID] != nil {
					contacts[contact.InstallationID] = contact.Tasks
				}
			case request := <-hub.lookups:
				request.reply <- contacts[request.id]
			}
		}
	}()
	return hub
}

// The TLS peer key is the registered installation identity. The extra nonce
// proves the reply belongs to this attempt rather than a cached registry row.
func (hub *Hub) Check(id string, key ed25519.PublicKey, stop <-chan struct{}) (Status, error) {
	if hub == nil {
		return Status{}, errors.New("announcement contacts are unavailable")
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	reply := make(chan chan<- Probe, 1)
	select {
	case hub.lookups <- lookup{id: id, reply: reply}:
	case <-stop:
		return Status{}, errors.New("contact consumer stopped")
	case <-deadline.C:
		return Status{}, errors.New("contact lookup timed out")
	}
	var contact chan<- Probe
	select {
	case contact = <-reply:
	case <-stop:
		return Status{}, errors.New("contact consumer stopped")
	case <-deadline.C:
		return Status{}, errors.New("contact lookup timed out")
	}
	if contact == nil {
		return Status{}, errors.New("installation is offline")
	}
	nonce, err := identifier()
	if err != nil {
		return Status{}, err
	}
	proofs := make(chan Proof, 1)
	if err := send(contact, Probe{Nonce: nonce, Reply: proofs}, stop); err != nil {
		return Status{}, err
	}
	select {
	case proof, open := <-proofs:
		if !open || proof.Nonce != nonce || proof.Status.InstallationID != id || time.Since(proof.At) > 30*time.Second || time.Until(proof.At) > 30*time.Second {
			return Status{}, errors.New("stale installation proof")
		}
		signature := proof.Signature
		proof.Signature = nil
		payload, _ := json.Marshal(proof)
		if len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, payload, signature) {
			return Status{}, errors.New("invalid installation proof")
		}
		return proof.Status, nil
	case <-stop:
		return Status{}, errors.New("contact consumer stopped")
	case <-deadline.C:
		return Status{}, errors.New("installation status timed out")
	}
}

func certificate(id string, key ed25519.PrivateKey) (tls.Certificate, error) {
	return netchantls.Certificate(id, key)
}

type KeyLookup func(string) (ed25519.PublicKey, error)

func (hub *Hub) Listen(address string, key ed25519.PrivateKey, keys KeyLookup, stop <-chan struct{}) error {
	identity, err := certificate("sitebrush.com", key)
	if err != nil {
		return err
	}
	configuration := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{identity}, ClientAuth: tls.RequireAnyClientCert, VerifyConnection: func(connection tls.ConnectionState) error {
		if len(connection.PeerCertificates) != 1 {
			return errors.New("missing installation certificate")
		}
		peer := connection.PeerCertificates[0]
		trusted, err := keys(peer.Subject.CommonName)
		if err != nil {
			return err
		}
		publicKey, ok := peer.PublicKey.(ed25519.PublicKey)
		if !ok || !publicKey.Equal(trusted) {
			return errors.New("unregistered installation key")
		}
		return nil
	}}
	listener, err := netchan.Listen[Contact](address, netchantransport.Configure(netchan.Config{Capacity: 8, TLS: configuration}, stop))
	if err != nil {
		return err
	}
	defer listener.Close()
	sessions := make(chan struct{}, 256)
	for {
		select {
		case <-stop:
			return nil
		case <-listener.Done:
			return errors.New("announcement listener stopped")
		case connection, open := <-listener.Channels:
			if !open {
				return nil
			}
			select {
			case sessions <- struct{}{}:
				go func() {
					defer func() { <-sessions }()
					defer connection.Abort()
					select {
					case contact, open := <-connection.Receive:
						if !open || contact.Tasks == nil {
							return
						}
						trusted, err := keys(contact.InstallationID)
						if err != nil {
							return
						}
						if !verifyContact(contact, trusted) {
							return
						}
						select {
						case hub.contacts <- contact:
						case <-stop:
							return
						}
					case <-stop:
						return
					case <-time.After(10 * time.Second):
						return
					}
					select {
					case <-stop:
					case <-connection.Done:
					}
				}()
			default:
				connection.Abort()
			}
		}
	}
}

// The central contact uses an installation signing identity, not a Web PKI
// hostname. Every connection must verify that pinned key; an empty trust input
// is an error rather than an option to disable verification.
func verifiedCentralTLS(id string, key ed25519.PrivateKey, central ed25519.PublicKey) (*tls.Config, error) {
	if len(central) != ed25519.PublicKeySize {
		return nil, errors.New("invalid central public key")
	}
	identity, err := certificate(id, key)
	if err != nil {
		return nil, err
	}
	configuration, err := netchantls.PinnedClientConfig(central)
	if err != nil {
		return nil, err
	}
	configuration.Certificates = []tls.Certificate{identity}
	return configuration, nil
}

func Agent(address, id string, key ed25519.PrivateKey, central ed25519.PublicKey, status func() (Status, error), stop <-chan struct{}) error {
	return AgentWithStatus(address, id, key, central, status, nil, stop)
}

// Connection status has its own consumer; announcement availability never owns
// or authorizes an installation's release operation.
func AgentWithStatus(address, id string, key ed25519.PrivateKey, central ed25519.PublicKey, status func() (Status, error), updates chan<- string, stop <-chan struct{}) error {
	if status == nil {
		return errors.New("missing installation status source")
	}
	configuration, err := verifiedCentralTLS(id, key, central)
	if err != nil {
		return err
	}

	connection, err := netchan.Dial[Contact](address, netchantransport.Configure(netchan.Config{Capacity: 8, TLS: configuration}, stop))
	if err != nil {
		return err
	}
	defer connection.Abort()
	tasks := make(chan Probe, 8)
	hello := Contact{InstallationID: id, Tasks: tasks, At: time.Now().UTC()}
	hello.Nonce, err = identifier()
	if err != nil {
		return err
	}
	hello.Signature = ed25519.Sign(key, contactPayload(hello))
	if err := send(connection.Send, hello, stop); err != nil {
		return err
	}
	if updates != nil {
		if err := send(updates, "", stop); err != nil {
			return err
		}
	}
	for {
		select {
		case <-stop:
			return nil
		case <-connection.Done:
			return errors.New("central contact ended")
		case task, open := <-tasks:
			if !open {
				return errors.New("central probe channel ended")
			}
			if len(task.Nonce) != 32 || task.Reply == nil {
				continue
			}
			current, err := status()
			if err != nil {
				continue
			}
			current.InstallationID = id
			proof := Proof{Nonce: task.Nonce, At: time.Now().UTC(), Status: current}
			payload, err := json.Marshal(proof)
			if err != nil {
				return err
			}
			proof.Signature = ed25519.Sign(key, payload)
			if err := send(task.Reply, proof, stop); err != nil {
				return err
			}
		}
	}
}

func contactPayload(contact Contact) []byte {
	payload, _ := json.Marshal(struct {
		ID, Nonce string
		At        time.Time
	}{contact.InstallationID, contact.Nonce, contact.At})
	return payload
}
func verifyContact(contact Contact, key ed25519.PublicKey) bool {
	return len(contact.Nonce) == 32 && time.Since(contact.At) <= 30*time.Second && time.Until(contact.At) <= 30*time.Second && len(key) == ed25519.PublicKeySize && ed25519.Verify(key, contactPayload(contact), contact.Signature)
}

func identifier() (string, error) {
	var nonce [16]byte
	_, err := rand.Read(nonce[:])
	return hex.EncodeToString(nonce[:]), err
}
func DecodeKey(encoded string) (ed25519.PublicKey, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, errors.New("invalid installation key")
	}
	return ed25519.PublicKey(key), nil
}

func send[T any](contact chan<- T, message T, stop <-chan struct{}) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("network contact revoked")
		}
	}()
	select {
	case <-stop:
		return errors.New("contact consumer stopped")
	default:
	}
	select {
	case contact <- message:
		return nil
	case <-stop:
		return errors.New("contact consumer stopped")
	case <-time.After(5 * time.Second):
		return errors.New("network contact unavailable")
	}
}
