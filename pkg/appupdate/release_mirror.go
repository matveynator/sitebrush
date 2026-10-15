package appupdate

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/matveynator/netchan"
)

type MirrorOptions struct {
	Address    string
	Directory  string
	PrivateKey ed25519.PrivateKey
	ReleaseKey ed25519.PublicKey
	Ready      chan<- string
}

// One connection owns one job, file descriptor and credit channel. Public
// release reads need no owner DB access and cannot enqueue version changes.
func ServeReleaseMirror(options MirrorOptions, stop <-chan struct{}) error {
	if len(options.PrivateKey) != ed25519.PrivateKeySize || len(options.ReleaseKey) != ed25519.PublicKeySize {
		return errors.New("invalid release mirror keys")
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	settings := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "SiteBrush releases"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	encoded, err := x509.CreateCertificate(rand.Reader, settings, settings, options.PrivateKey.Public(), options.PrivateKey)
	if err != nil {
		return err
	}
	listener, err := netchan.Listen[ReleaseRequest](options.Address, netchan.Config{Capacity: 1, TLS: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{{Certificate: [][]byte{encoded}, PrivateKey: options.PrivateKey}}}})
	if err != nil {
		return err
	}
	if options.Ready != nil {
		if err := sendChannel(options.Ready, listener.Address, stop, time.Second); err != nil {
			listener.Close()
			return err
		}
	}
	slots := make(chan struct{}, 8)
	defer func() {
		listener.Close()
		for count := 0; count < cap(slots); count++ {
			slots <- struct{}{}
		}
	}()
	for {
		select {
		case <-stop:
			return nil
		case <-listener.Done:
			return errors.New("release mirror listener ended")
		case contact, open := <-listener.Channels:
			if !open {
				return nil
			}
			select {
			case slots <- struct{}{}:
				go func() { defer func() { <-slots }(); serveReleaseContact(options, contact, stop) }()
			default:
				contact.Abort()
			}
		}
	}
}

func serveReleaseContact(options MirrorOptions, contact *netchan.Channel[ReleaseRequest], stop <-chan struct{}) {
	// Closing the parent contact revokes every nested session/reply capability.
	defer closeReleaseContact(contact)
	consumer := make(chan struct{})
	rootEnded := make(chan struct{})
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		timer := time.NewTimer(15 * time.Minute)
		defer timer.Stop()
		select {
		case <-stop:
		case <-contact.Done:
		case <-rootEnded:
		case <-timer.C:
		case <-finished:
			return
		}
		close(consumer)
	}()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	var request ReleaseRequest
	select {
	case received, open := <-contact.Receive:
		if !open {
			return
		}
		request = received
	case <-consumer:
		return
	case <-timer.C:
		return
	}
	// The root stream is used only for its initial request and lifetime. Its
	// closure cancels hashing or a stalled credit wait without a shared registry.
	go func() {
		select {
		case <-contact.Receive:
			close(rootEnded)
		case <-finished:
		}
	}()
	if request.Reply == nil {
		return
	}
	reject := func(err error) {
		if sendChannel(request.Reply, ReleaseResponse{Error: err.Error()}, consumer, time.Second) == nil {
			awaitReleaseFinish(contact, consumer)
		}
	}
	if request.Protocol != releaseWireProtocol {
		reject(errors.New("unsupported release transport protocol"))
		return
	}
	root, err := os.OpenRoot(options.Directory)
	if err != nil {
		reject(err)
		return
	}
	defer root.Close()
	version := "latest"
	if request.Version != "" {
		number, err := strconv.ParseUint(request.Version, 10, 64)
		if err != nil || number == 0 {
			reject(errors.New("invalid requested release"))
			return
		}
		version = request.Version
	}
	payload, err := readLimitedFile(root, filepath.Join(version, "server-app", "update.json"), manifestLimit)
	if err != nil {
		reject(err)
		return
	}
	manifest, err := Verify(payload, options.ReleaseKey)
	if err != nil {
		reject(err)
		return
	}
	if request.Version == "" {
		_ = sendChannel(request.Reply, ReleaseResponse{Manifest: payload}, consumer, 5*time.Second)
		awaitReleaseFinish(contact, consumer)
		return
	}
	if manifest.Version != request.Version || manifestIdentity(manifest) != request.ManifestDigest {
		reject(errors.New("confirmed release metadata no longer matches"))
		return
	}
	artifact, err := manifest.Artifact(request.OS, request.Arch)
	if err != nil {
		reject(err)
		return
	}
	if request.Offset < 0 || request.Offset > artifact.Size {
		reject(errors.New("invalid release resume offset"))
		return
	}
	file, err := root.Open(filepath.Join(version, "server-app", artifact.Name))
	if err != nil {
		reject(err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != artifact.Size {
		reject(errors.New("invalid published release file"))
		return
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, sessionReader{reader: io.LimitReader(file, artifact.Size+1), stop: consumer}); err != nil {
		reject(err)
		return
	}
	if hex.EncodeToString(digest.Sum(nil)) != artifact.SHA256 {
		reject(errors.New("published release checksum mismatch"))
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		reject(err)
		return
	}
	prefix := sha256.New()
	if _, err := io.Copy(prefix, sessionReader{reader: io.LimitReader(file, request.Offset), stop: consumer}); err != nil {
		reject(err)
		return
	}
	if hex.EncodeToString(prefix.Sum(nil)) != request.PrefixSHA256 {
		reject(errors.New("release resume prefix does not match"))
		return
	}
	credits := make(chan ReleaseCredit, 1)
	if err := sendChannel(request.Reply, ReleaseResponse{Manifest: payload, Session: credits}, consumer, 5*time.Second); err != nil {
		return
	}
	offset := request.Offset
	for {
		idle := time.NewTimer(30 * time.Second)
		var credit ReleaseCredit
		select {
		case received, open := <-credits:
			idle.Stop()
			if !open {
				return
			}
			credit = received
		case <-consumer:
			idle.Stop()
			return
		case <-idle.C:
			return
		}
		if credit.Finish {
			if credit.Offset == artifact.Size && offset == artifact.Size {
				awaitReleaseFinish(contact, consumer)
			}
			return
		}
		if credit.Reply == nil {
			return
		}
		if credit.Offset != offset {
			_ = sendChannel(credit.Reply, ReleaseChunk{Error: "release read credit has an invalid offset"}, consumer, time.Second)
			return
		}
		length := int64(releaseChunkSize)
		if artifact.Size-offset < length {
			length = artifact.Size - offset
		}
		contents := make([]byte, int(length))
		_, err := io.ReadFull(file, contents)
		if err != nil {
			_ = sendChannel(credit.Reply, ReleaseChunk{Error: err.Error()}, consumer, time.Second)
			return
		}
		chunk := ReleaseChunk{Offset: offset, Bytes: contents, Final: offset+length == artifact.Size}
		if err := sendChannel(credit.Reply, chunk, consumer, 5*time.Second); err != nil {
			return
		}
		offset += length
	}
}

// A result is acknowledged over the root contact before its reply capability
// is revoked. Aborting immediately after a local send can lose the last frame.
func awaitReleaseFinish(contact *netchan.Channel[ReleaseRequest], stop <-chan struct{}) {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-contact.Receive:
	case <-stop:
	case <-contact.Done:
	case <-timer.C:
	}
}
