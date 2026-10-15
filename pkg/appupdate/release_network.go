package appupdate

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/matveynator/netchan"
)

const ReleaseAddress = "sitebrush.com:9878"
const releaseChunkSize = 64 << 10
const releaseWireProtocol = 1

// Release delivery is public, like the HTTPS download directory. The server's
// pinned identity authenticates the contact; the signing key authorizes bytes.
// Neither registration nor an email campaign is a prerequisite for updating.
type ReleaseClient struct {
	Address    string
	CentralKey ed25519.PublicKey
}

type ReleaseRequest struct {
	Protocol       int
	Version        string
	ManifestDigest string
	OS             string
	Arch           string
	Offset         int64
	PrefixSHA256   string
	Reply          chan<- ReleaseResponse
}

type ReleaseResponse struct {
	Manifest []byte
	Session  chan<- ReleaseCredit
	Error    string
}

type ReleaseCredit struct {
	Offset int64
	Finish bool
	Reply  chan<- ReleaseChunk
}

type ReleaseChunk struct {
	Offset int64
	Bytes  []byte
	Final  bool
	Error  string
}

func NewNetworkCatalog(publicKey, centralKey ed25519.PublicKey) Catalog {
	catalog := NewCatalog(publicKey)
	catalog.mirror = &ReleaseClient{Address: ReleaseAddress, CentralKey: append(ed25519.PublicKey(nil), centralKey...)}
	catalog.manifestURLs = []string{"https://sitebrush.com/download/latest/server-app/update.json", "https://sitebrush.ru/download/latest/server-app/update.json", catalog.manifestURL}
	catalog.artifactBases = []string{"https://sitebrush.com/download/", "https://sitebrush.ru/download/"}
	return catalog
}

// TLS verification deliberately uses a pinned signing identity instead of a
// public CA hostname. An empty or substituted identity always fails closed.
func verifiedReleaseTLS(publicKey ed25519.PublicKey) (*tls.Config, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("invalid release mirror identity")
	}
	trusted := append(ed25519.PublicKey(nil), publicKey...)
	return &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true, VerifyConnection: func(connection tls.ConnectionState) error {
		if len(connection.PeerCertificates) != 1 {
			return errors.New("missing release mirror certificate")
		}
		actual, ok := connection.PeerCertificates[0].PublicKey.(ed25519.PublicKey)
		if !ok || !actual.Equal(trusted) {
			return errors.New("release mirror identity mismatch")
		}
		return nil
	}}, nil
}

func (client ReleaseClient) dial(stop <-chan struct{}) (*netchan.Channel[ReleaseRequest], error) {
	select {
	case <-stop:
		return nil, errors.New("release contact canceled")
	default:
	}
	settings, err := verifiedReleaseTLS(client.CentralKey)
	if err != nil {
		return nil, err
	}
	type result struct {
		contact *netchan.Channel[ReleaseRequest]
		err     error
	}
	replies := make(chan result)
	abandoned := make(chan struct{})
	defer close(abandoned)
	// The library owns its bounded dial/handshake timeout. A caller can revoke
	// interest sooner; the dial worker then aborts any late connection itself.
	go func() {
		contact, err := netchan.Dial[ReleaseRequest](client.Address, netchan.Config{Capacity: 1, TLS: settings})
		select {
		case replies <- result{contact, err}:
		case <-abandoned:
			if contact != nil {
				closeReleaseContact(contact)
			}
		}
	}()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case received := <-replies:
		return received.contact, received.err
	case <-stop:
		return nil, errors.New("release contact canceled")
	case <-timer.C:
		return nil, errors.New("release mirror connection timed out")
	}
}

func (client ReleaseClient) request(request ReleaseRequest, stop <-chan struct{}) (*netchan.Channel[ReleaseRequest], ReleaseResponse, error) {
	contact, err := client.dial(stop)
	if err != nil {
		return nil, ReleaseResponse{}, err
	}
	reply := make(chan ReleaseResponse, 1)
	request.Reply = reply
	request.Protocol = releaseWireProtocol
	if err := sendChannel(contact.Send, request, stop, 5*time.Second); err != nil {
		closeReleaseContact(contact)
		return nil, ReleaseResponse{}, err
	}
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	var response ReleaseResponse
	select {
	case received, open := <-reply:
		if !open {
			err = errors.New("release reply revoked")
		} else {
			response = received
			if response.Error != "" {
				err = errors.New(response.Error)
			}
		}
	case <-stop:
		err = errors.New("release contact canceled")
	case <-contact.Done:
		err = errors.New("release contact ended")
	case <-timer.C:
		err = errors.New("release mirror request timed out")
	}
	if err != nil {
		closeReleaseContact(contact)
		return nil, response, err
	}
	return contact, response, nil
}

func (client ReleaseClient) check(key ed25519.PublicKey, stop <-chan struct{}) (Manifest, string, error) {
	contact, response, err := client.request(ReleaseRequest{}, stop)
	if err != nil {
		return Manifest{}, "", err
	}
	defer closeReleaseContact(contact)
	manifest, err := Verify(response.Manifest, key)
	if err != nil {
		return Manifest{}, "", err
	}
	digest := sha256.Sum256(response.Manifest)
	return manifest, hex.EncodeToString(digest[:]), nil
}

func manifestIdentity(manifest Manifest) string {
	encoded, _ := json.Marshal(manifest)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func (client ReleaseClient) download(manifest Manifest, key ed25519.PublicKey, file *os.File, stop <-chan struct{}) error {
	return client.downloadProgress(manifest, key, file, stop, nil)
}

func (client ReleaseClient) downloadProgress(manifest Manifest, key ed25519.PublicKey, file *os.File, stop <-chan struct{}, updates chan<- WorkProgress) error {
	artifact, err := manifest.Artifact(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	// A fresh contact can resume only a locally rehashed prefix of the same
	// confirmed artifact. HTTP fallback restarts from zero in the caller.
	var failure error
	for attempt := 0; attempt < 2; attempt++ {
		select {
		case <-stop:
			return errors.New("release download canceled")
		default:
		}
		failure = client.transferProgress(manifest, artifact, key, file, stop, updates)
		if failure == nil {
			return nil
		}
	}
	return failure
}

func (client ReleaseClient) transfer(manifest Manifest, artifact Artifact, key ed25519.PublicKey, file *os.File, stop <-chan struct{}) error {
	return client.transferProgress(manifest, artifact, key, file, stop, nil)
}

func (client ReleaseClient) transferProgress(manifest Manifest, artifact Artifact, key ed25519.PublicKey, file *os.File, stop <-chan struct{}, updates chan<- WorkProgress) error {
	offset, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if offset > artifact.Size {
		return errors.New("invalid release prefix size")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	prefix := sha256.New()
	if _, err := io.Copy(prefix, sessionReader{reader: io.LimitReader(file, offset), stop: stop}); err != nil {
		return err
	}
	request := ReleaseRequest{Version: manifest.Version, ManifestDigest: manifestIdentity(manifest), OS: artifact.OS, Arch: artifact.Arch, Offset: offset, PrefixSHA256: hex.EncodeToString(prefix.Sum(nil))}
	contact, response, err := client.request(request, stop)
	if err != nil {
		return err
	}
	defer closeReleaseContact(contact)
	confirmed, err := Verify(response.Manifest, key)
	if err != nil {
		return err
	}
	if manifestIdentity(confirmed) != request.ManifestDigest || response.Session == nil {
		return errors.New("release mirror changed confirmed metadata")
	}
	replies := make(chan ReleaseChunk, 1)
	counter := &progressWriter{output: file, updates: updates, stop: stop, work: WorkProgress{Bytes: offset, TotalBytes: artifact.Size}}
	if err := counter.report(); err != nil {
		return err
	}
	for {
		if err := sendChannel(response.Session, ReleaseCredit{Offset: offset, Reply: replies}, stop, 5*time.Second); err != nil {
			return err
		}
		timer := time.NewTimer(20 * time.Second)
		var chunk ReleaseChunk
		select {
		case received, open := <-replies:
			timer.Stop()
			if !open {
				return errors.New("release chunk reply revoked")
			}
			chunk = received
		case <-stop:
			timer.Stop()
			return errors.New("release download canceled")
		case <-contact.Done:
			timer.Stop()
			return errors.New("release stream ended")
		case <-timer.C:
			return errors.New("release chunk timed out")
		}
		if chunk.Error != "" {
			return errors.New(chunk.Error)
		}
		if chunk.Offset != offset || len(chunk.Bytes) > releaseChunkSize || int64(len(chunk.Bytes)) > artifact.Size-offset || chunk.Final != (offset+int64(len(chunk.Bytes)) == artifact.Size) || (!chunk.Final && len(chunk.Bytes) == 0) {
			return errors.New("invalid release chunk offset, length or final marker")
		}
		if _, err := counter.Write(chunk.Bytes); err != nil {
			return err
		}
		offset += int64(len(chunk.Bytes))
		if chunk.Final {
			if err := sendChannel(response.Session, ReleaseCredit{Offset: offset, Finish: true}, stop, 5*time.Second); err != nil {
				return fmt.Errorf("complete release transfer: %w", err)
			}
			return counter.report()
		}
	}
}

// Abort alone leaves the remote logical session resumable. Close the owned
// outgoing channel first so the peer can release its nested capabilities and
// file immediately; fall back to Abort if that exchange cannot complete.
func closeReleaseContact(contact *netchan.Channel[ReleaseRequest]) {
	if contact == nil {
		return
	}
	func() {
		// The transport can revoke a channel concurrently with its local owner.
		defer func() { _ = recover() }()
		close(contact.Send)
	}()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-contact.Done:
	case <-timer.C:
		contact.Abort()
	}
}
