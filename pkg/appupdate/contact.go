package appupdate

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/matveynator/netchan"
)

type Request struct {
	Action       string
	ManifestHash string
	Reply        chan<- Response
	Child        chan<- ChildTask
	Detail       string
	Result       *Response
}

type ChildTask struct {
	Action string
	Reply  chan<- ChildResult
}
type ChildResult struct {
	Error   string
	Address string
	Nonce   string
}

// NetChan revokes send capabilities by closing their local bridge. Go has no
// atomic "is open" test for a send; recovery is confined to this transport
// boundary and always becomes an explicit failed communication result.
func sendChannel[T any](channel chan<- T, message T, stop <-chan struct{}, timeout time.Duration) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("channel contact terminated")
		}
	}()
	if channel == nil {
		return errors.New("missing response channel")
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-stop:
		return errors.New("channel session stopped")
	default:
	}
	select {
	case channel <- message:
		return nil
	case <-stop:
		return errors.New("channel session stopped")
	case <-timer.C:
		return errors.New("channel contact timed out")
	}
}

func ReplyChild(task ChildTask, result ChildResult, stop <-chan struct{}) error {
	return sendChannel(task.Reply, result, stop, time.Second)
}

// ChildContact transfers a private session capability to the supervisor. The
// channel stays alive with the child, including throughout probation.
func ChildContact(storagePath string, tasks chan ChildTask, stop <-chan struct{}) error {
	store, err := os.OpenRoot(filepath.Join(storagePath, Directory))
	if err != nil {
		return err
	}
	defer store.Close()
	configuration, err := tlsIdentity(store, false)
	if err != nil {
		return err
	}
	connection, err := netchan.Dial[Request](Address(storagePath), netchan.Config{Capacity: 4, TLS: configuration})
	if err != nil {
		return err
	}
	defer connection.Abort()
	if err := sendChannel(connection.Send, Request{Action: "attach", Child: tasks}, stop, 5*time.Second); err != nil {
		return err
	}
	select {
	case response, open := <-connection.Receive:
		if !open || response.Result == nil {
			return errors.New("supervisor reply contact terminated")
		}
		if response.Result.Error != "" {
			return errors.New(response.Result.Error)
		}
	case <-stop:
		return nil
	case <-connection.Done:
		return errors.New("supervisor contact lost")
	}
	select {
	case <-stop:
		return nil
	case <-connection.Done:
		return errors.New("supervisor contact lost")
	}
}

type Response struct {
	State State
	Error string
}

type identity struct {
	Certificate []byte
	Key         []byte
}

func Address(storagePath string) string {
	canonical, err := filepath.EvalSymlinks(storagePath)
	if err != nil {
		canonical = filepath.Clean(storagePath)
	}
	absolute, err := filepath.Abs(canonical)
	if err == nil {
		canonical = absolute
	}
	digest := sha256.Sum256([]byte(canonical))
	return fmt.Sprintf("127.0.0.1:%d", 20000+int(binary.BigEndian.Uint16(digest[:2]))%30000)
}

func tlsIdentity(store *os.Root, create bool) (*tls.Config, error) {
	var credentials identity
	payload, err := readLimitedFile(store, "identity.json", 64<<10)
	if errors.Is(err, os.ErrNotExist) && create {
		privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if err != nil {
			return nil, err
		}
		certificate := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "SiteBrush update supervisor"}, DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &privateKey.PublicKey, privateKey)
		if err != nil {
			return nil, err
		}
		keyDER, err := x509.MarshalECPrivateKey(privateKey)
		if err != nil {
			return nil, err
		}
		credentials = identity{Certificate: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), Key: pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})}
		// Exclusive creation makes simultaneous first starts fail closed instead
		// of replacing the identity of an already-listening supervisor.
		file, err := store.OpenFile("identity.json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, err
		}
		payload, err = json.Marshal(credentials)
		if err == nil {
			_, err = file.Write(payload)
		}
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if err := syncDirectory(store, "."); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(payload, &credentials); err != nil {
		return nil, err
	}
	certificate, err := tls.X509KeyPair(credentials.Certificate, credentials.Key)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(credentials.Certificate) {
		return nil, errors.New("invalid supervisor identity")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, RootCAs: pool, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert, ServerName: "localhost"}, nil
}

// Exchange returns through a consumer-owned reply channel. No network request
// can acquire ownership of an operation simply by supplying a callback address.
func Exchange(storagePath string, request Request, stop <-chan struct{}) (Response, error) {
	store, err := os.OpenRoot(filepath.Join(storagePath, Directory))
	if err != nil {
		return Response{}, err
	}
	defer store.Close()
	configuration, err := tlsIdentity(store, false)
	if err != nil {
		return Response{}, err
	}
	connection, err := netchan.Dial[Request](Address(storagePath), netchan.Config{Capacity: 4, TLS: configuration})
	if err != nil {
		return Response{}, err
	}
	graceful := false
	defer func() {
		if graceful {
			_ = connection.Close()
		} else {
			_ = connection.Abort()
		}
	}()
	request.Reply = nil
	request.Result = nil
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	if err := sendChannel(connection.Send, request, stop, 5*time.Second); err != nil {
		return Response{}, err
	}
	select {
	case response, open := <-connection.Receive:
		if !open || response.Result == nil {
			return Response{}, errors.New("supervisor reply contact terminated")
		}
		graceful = true
		if response.Result.Error != "" {
			return *response.Result, errors.New(response.Result.Error)
		}
		return *response.Result, nil
	case <-stop:
		return Response{}, errors.New("update request canceled")
	case <-connection.Done:
		return Response{}, errors.New("update supervisor disconnected")
	case <-timer.C:
		return Response{}, errors.New("update supervisor timed out")
	}
}
