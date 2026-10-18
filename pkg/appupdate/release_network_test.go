package appupdate

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"github.com/matveynator/netchan/v2"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func releaseMirrorFixture(t *testing.T, payload []byte) (Catalog, Manifest, string, string) {
	t.Helper()
	root := t.TempDir()
	manifest, public, key := testRelease(t, payload)
	signed, err := Sign(manifest, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"2", "latest"} {
		directory := filepath.Join(root, version, "server-app")
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "update.json"), signed, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, BinaryName(runtime.GOOS, runtime.GOARCH)), payload, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.GOOS != "windows" {
		if err := os.RemoveAll(filepath.Join(root, "latest")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("2", filepath.Join(root, "latest")); err != nil {
			t.Fatal(err)
		}
	}
	central, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan string, 1)
	finished := make(chan error, 1)
	stop := make(chan struct{})
	go func() {
		finished <- ServeReleaseMirror(MirrorOptions{Address: "127.0.0.1:0", Directory: root, PrivateKey: private, ReleaseKey: public, Ready: ready}, stop)
	}()
	var address string
	select {
	case address = <-ready:
	case err := <-finished:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("mirror did not start")
	}
	t.Cleanup(func() {
		close(stop)
		select {
		case err := <-finished:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("mirror sessions did not stop")
		}
	})
	catalog := NewCatalog(public)
	catalog.mirror = &ReleaseClient{Address: address, CentralKey: central}
	return catalog, manifest, root, address
}

func TestNetChanReleaseCatalogAndChunkedDownload(t *testing.T) {
	payload := bytes.Repeat([]byte("verified server bytes"), 20000)
	catalog, manifest, _, _ := releaseMirrorFixture(t, payload)
	// No HTTP request may be needed when the official mirror has the release.
	fallback := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("successful NetChan transfer used HTTP") }))
	defer fallback.Close()
	catalog.client = fallback.Client()
	catalog.manifestURL = fallback.URL
	catalog.artifactBase = fallback.URL + "/"
	checked, digest, err := catalog.Check(nil)
	if err != nil || checked.Version != manifest.Version || digest == "" {
		t.Fatal(checked, digest, err)
	}
	destination := filepath.Join(t.TempDir(), "candidate")
	if err := catalog.Download(checked, destination, nil); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(actual, payload) {
		t.Fatal("downloaded binary differs", err)
	}
	if err := catalog.Download(checked, destination, nil); err == nil {
		t.Fatal("existing candidate overwritten")
	}
}

func TestNetChanReleaseResumesOnlyTheVerifiedPrefix(t *testing.T) {
	payload := bytes.Repeat([]byte("resume bytes"), 20000)
	catalog, manifest, _, _ := releaseMirrorFixture(t, payload)
	file, err := os.OpenFile(filepath.Join(t.TempDir(), "partial"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0700)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	prefix := payload[:releaseChunkSize+73]
	if _, err := file.Write(prefix); err != nil {
		t.Fatal(err)
	}
	if err := catalog.mirror.transfer(manifest, manifest.Artifacts[0], catalog.PublicKey, file, nil); err != nil {
		t.Fatal(err)
	}
	if err := verifyReleaseFile(file, manifest.Artifacts[0], nil); err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(0); err != nil {
		t.Fatal(err)
	}
	file.Seek(0, io.SeekStart)
	if _, err := file.Write([]byte("corrupt prefix")); err != nil {
		t.Fatal(err)
	}
	if err := catalog.mirror.transfer(manifest, manifest.Artifacts[0], catalog.PublicKey, file, nil); err == nil {
		t.Fatal("corrupt prefix was resumed")
	}
}

func TestNetChanReleaseRejectsMetadataOffsetsAndCorruptPublishedBinary(t *testing.T) {
	payload := []byte("trusted server")
	catalog, manifest, root, _ := releaseMirrorFixture(t, payload)
	empty := sha256.Sum256(nil)
	base := ReleaseRequest{Version: manifest.Version, ManifestDigest: manifestIdentity(manifest), OS: runtime.GOOS, Arch: runtime.GOARCH, PrefixSHA256: hex.EncodeToString(empty[:])}
	for _, modify := range []func(*ReleaseRequest){func(request *ReleaseRequest) { request.Version = "../outside" }, func(request *ReleaseRequest) { request.ManifestDigest = strings.Repeat("a", 64) }, func(request *ReleaseRequest) { request.Offset = -1 }, func(request *ReleaseRequest) { request.Offset = 1000 }, func(request *ReleaseRequest) { request.OS = "plan9" }} {
		request := base
		modify(&request)
		connection, _, err := catalog.mirror.request(request, nil)
		if connection != nil {
			connection.Abort()
		}
		if err == nil {
			t.Fatal("invalid release transfer accepted", request)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "2", "server-app", manifest.Artifacts[0].Name), []byte("corrupt server"), 0700); err != nil {
		t.Fatal(err)
	}
	connection, _, err := catalog.mirror.request(base, nil)
	if connection != nil {
		connection.Abort()
	}
	if err == nil {
		t.Fatal("mirror served an unverified executable")
	}
}

func TestNetChanReleaseFallbackAndWrongMirrorKey(t *testing.T) {
	payload := bytes.Repeat([]byte("fallback"), 1000)
	catalog, manifest, root, _ := releaseMirrorFixture(t, payload)
	wrong, _, _ := ed25519.GenerateKey(rand.Reader)
	catalog.mirror.CentralKey = wrong
	signed, err := os.ReadFile(filepath.Join(root, "2", "server-app", "update.json"))
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests <- request.URL.Path
		if request.URL.Path == "/manifest" {
			writer.Write(signed)
		} else {
			writer.Write(payload)
		}
	}))
	defer server.Close()
	catalog.client = server.Client()
	catalog.manifestURL = server.URL + "/manifest"
	catalog.artifactBase = server.URL + "/"
	checked, _, err := catalog.Check(nil)
	if err != nil || checked.Version != manifest.Version {
		t.Fatal(checked, err)
	}
	destination := filepath.Join(t.TempDir(), "candidate")
	if err := catalog.Download(checked, destination, nil); err != nil {
		t.Fatal(err)
	}
	if actual, err := os.ReadFile(destination); err != nil || !bytes.Equal(actual, payload) {
		t.Fatal("fallback failed", err)
	}
	if len(requests) != 2 {
		t.Fatal("fallback did not use exact signed release", len(requests))
	}
	canceled := make(chan struct{})
	close(canceled)
	failed := filepath.Join(t.TempDir(), "canceled")
	if err := catalog.Download(checked, failed, canceled); err == nil {
		t.Fatal("canceled download succeeded")
	}
	if _, err := os.Stat(failed); !os.IsNotExist(err) {
		t.Fatal("canceled download retained partial executable", err)
	}
}

func TestNetChanReleaseCreditBackpressureAndCancellation(t *testing.T) {
	payload := bytes.Repeat([]byte("credit"), 30000)
	catalog, manifest, _, _ := releaseMirrorFixture(t, payload)
	empty := sha256.Sum256(nil)
	contact, response, err := catalog.mirror.request(ReleaseRequest{Version: manifest.Version, ManifestDigest: manifestIdentity(manifest), OS: runtime.GOOS, Arch: runtime.GOARCH, PrefixSHA256: hex.EncodeToString(empty[:])}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeReleaseContact(contact)
	replies := make(chan ReleaseChunk, 1)
	select {
	case <-replies:
		t.Fatal("unsolicited chunk without read credit")
	default:
	}
	if err := sendChannel(response.Session, ReleaseCredit{Offset: 0, Reply: replies}, nil, time.Second); err != nil {
		t.Fatal(err)
	}
	var first ReleaseChunk
	select {
	case first = <-replies:
	case <-time.After(3 * time.Second):
		t.Fatal("first credit unanswered")
	}
	if first.Offset != 0 || len(first.Bytes) != releaseChunkSize || first.Final {
		t.Fatal("invalid bounded chunk", first.Offset, len(first.Bytes))
	}
	select {
	case <-replies:
		t.Fatal("producer exceeded consumer credit")
	default:
	}
	if err := sendChannel(response.Session, ReleaseCredit{Offset: 1, Reply: replies}, nil, time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case invalid := <-replies:
		if invalid.Error == "" {
			t.Fatal("repeated or skipped offset accepted")
		}
	case <-contact.Done:
	case <-time.After(3 * time.Second):
		t.Fatal("invalid credit was not rejected")
	}
}

// A genuine TLS/NetChan peer can still send malicious protocol messages. Content
// authorization and framing checks must remain local to the installation.
func adversarialReleaseMirror(t *testing.T, public ed25519.PublicKey, serve func(*netchan.Channel[ReleaseRequest], ReleaseRequest, int)) Catalog {
	t.Helper()
	central, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	encoded, err := x509.CreateCertificate(rand.Reader, certificate, certificate, central, private)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := netchan.Listen[ReleaseRequest]("127.0.0.1:0", netchan.Config{Capacity: 1, TLS: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{{Certificate: [][]byte{encoded}, PrivateKey: private}}}})
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		attempt := 0
		for {
			select {
			case <-stop:
				return
			case contact, open := <-listener.Channels:
				if !open {
					return
				}
				func() {
					defer closeReleaseContact(contact)
					select {
					case request, open := <-contact.Receive:
						if open {
							serve(contact, request, attempt)
							attempt++
						}
					case <-stop:
					case <-contact.Done:
					}
				}()
			}
		}
	}()
	t.Cleanup(func() {
		close(stop)
		listener.Close()
		select {
		case <-finished:
		case <-time.After(10 * time.Second):
			t.Error("adversarial release worker did not stop")
		}
	})
	catalog := NewCatalog(public)
	catalog.mirror = &ReleaseClient{Address: listener.Address, CentralKey: central}
	return catalog
}

func TestNetChanReleaseRejectsMaliciousPeerFrames(t *testing.T) {
	payload := bytes.Repeat([]byte("verified"), 20000)
	manifest, public, key := testRelease(t, payload)
	signed, err := Sign(manifest, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name  string
		chunk ReleaseChunk
	}{
		{"skipped-offset", ReleaseChunk{Offset: 1, Bytes: payload[:16]}},
		{"oversized-chunk", ReleaseChunk{Bytes: payload[:releaseChunkSize+1]}},
		{"premature-final", ReleaseChunk{Bytes: payload[:16], Final: true}},
		{"empty-progress", ReleaseChunk{}},
		{"explicit-error", ReleaseChunk{Error: "download refused"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			catalog := adversarialReleaseMirror(t, public, func(contact *netchan.Channel[ReleaseRequest], request ReleaseRequest, attempt int) {
				credits := make(chan ReleaseCredit, 1)
				if err := sendChannel(request.Reply, ReleaseResponse{Manifest: signed, Session: credits}, contact.Done, time.Second); err != nil {
					return
				}
				select {
				case credit := <-credits:
					_ = sendChannel(credit.Reply, scenario.chunk, contact.Done, time.Second)
					awaitReleaseFinish(contact, contact.Done)
				case <-contact.Done:
				}
			})
			file, err := os.OpenFile(filepath.Join(t.TempDir(), "candidate"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0700)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if err := catalog.mirror.transfer(manifest, manifest.Artifacts[0], public, file, nil); err == nil {
				t.Fatal("malformed release stream accepted")
			}
			if info, err := file.Stat(); err != nil || info.Size() != 0 {
				t.Fatal("invalid chunk was written", info, err)
			}
		})
	}
}

func TestNetChanReleaseRejectsForgedAndChangedManifest(t *testing.T) {
	manifest, public, key := testRelease(t, []byte("trusted"))
	changed := manifest
	changed.Version = "3"
	changed.Sequence = 3
	signedChanged, err := Sign(changed, key)
	if err != nil {
		t.Fatal(err)
	}
	_, unrelated, _ := ed25519.GenerateKey(rand.Reader)
	forged, err := Sign(manifest, unrelated)
	if err != nil {
		t.Fatal(err)
	}
	for _, envelope := range [][]byte{forged, signedChanged, bytes.Repeat([]byte("x"), manifestLimit+1)} {
		catalog := adversarialReleaseMirror(t, public, func(contact *netchan.Channel[ReleaseRequest], request ReleaseRequest, attempt int) {
			_ = sendChannel(request.Reply, ReleaseResponse{Manifest: envelope, Session: make(chan ReleaseCredit)}, contact.Done, time.Second)
			awaitReleaseFinish(contact, contact.Done)
		})
		file, err := os.CreateTemp(t.TempDir(), "candidate")
		if err != nil {
			t.Fatal(err)
		}
		if err := catalog.mirror.transfer(manifest, manifest.Artifacts[0], public, file, nil); err == nil {
			t.Fatal("unauthorized or changed manifest accepted")
		}
		file.Close()
	}
}

func TestNetChanReleaseReconnectResumesInterruptedTransfer(t *testing.T) {
	payload := bytes.Repeat([]byte("resume-stream"), 20000)
	manifest, public, key := testRelease(t, payload)
	signed, err := Sign(manifest, key)
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan ReleaseRequest, 2)
	catalog := adversarialReleaseMirror(t, public, func(contact *netchan.Channel[ReleaseRequest], request ReleaseRequest, attempt int) {
		requests <- request
		credits := make(chan ReleaseCredit, 1)
		if err := sendChannel(request.Reply, ReleaseResponse{Manifest: signed, Session: credits}, contact.Done, time.Second); err != nil {
			return
		}
		for {
			select {
			case credit := <-credits:
				if credit.Finish {
					awaitReleaseFinish(contact, contact.Done)
					return
				}
				// Seeing the next credit proves the preceding chunk reached disk.
				if attempt == 0 && credit.Offset > 0 {
					return
				}
				end := credit.Offset + releaseChunkSize
				if end > int64(len(payload)) {
					end = int64(len(payload))
				}
				if err := sendChannel(credit.Reply, ReleaseChunk{Offset: credit.Offset, Bytes: payload[credit.Offset:end], Final: end == int64(len(payload))}, contact.Done, time.Second); err != nil {
					return
				}
			case <-contact.Done:
				return
			}
		}
	})
	fallback := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("resumed download used HTTP") }))
	defer fallback.Close()
	catalog.client = fallback.Client()
	catalog.artifactBase = fallback.URL + "/"
	destination := filepath.Join(t.TempDir(), "candidate")
	if err := catalog.Download(manifest, destination, nil); err != nil {
		t.Fatal(err)
	}
	first, second := <-requests, <-requests
	prefix := sha256.Sum256(payload[:releaseChunkSize])
	if first.Offset != 0 || second.Offset != releaseChunkSize || second.PrefixSHA256 != hex.EncodeToString(prefix[:]) || second.ManifestDigest != first.ManifestDigest {
		t.Fatal("reconnect lost the confirmed artifact or verified prefix")
	}
	if actual, err := os.ReadFile(destination); err != nil || !bytes.Equal(actual, payload) {
		t.Fatal("resumed binary differs", err)
	}
}

func TestNetChanReleaseEnforcesSessionLimitAndReleasesCanceledSlots(t *testing.T) {
	catalog, manifest, _, _ := releaseMirrorFixture(t, []byte("bounded release sessions"))
	empty := sha256.Sum256(nil)
	request := ReleaseRequest{Version: manifest.Version, ManifestDigest: manifestIdentity(manifest), OS: runtime.GOOS, Arch: runtime.GOARCH, PrefixSHA256: hex.EncodeToString(empty[:])}
	var contacts []*netchan.Channel[ReleaseRequest]
	defer func() {
		for _, contact := range contacts {
			closeReleaseContact(contact)
		}
	}()
	for index := 0; index < 8; index++ {
		contact, _, err := catalog.mirror.request(request, nil)
		if err != nil {
			t.Fatal("session capacity unavailable", err)
		}
		contacts = append(contacts, contact)
	}
	contact, _, err := catalog.mirror.request(request, nil)
	if contact != nil {
		closeReleaseContact(contact)
	}
	if err == nil {
		t.Fatal("mirror exceeded its bounded session capacity")
	}
	for _, contact := range contacts {
		closeReleaseContact(contact)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, _, err := catalog.mirror.check(catalog.PublicKey, nil)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("canceled sessions retained their capacity", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNetChanReleaseFinalChecksumFailurePreservesWorkingInstallation(t *testing.T) {
	payload := []byte("working-release")
	manifest, public, key := testRelease(t, payload)
	signed, err := Sign(manifest, key)
	if err != nil {
		t.Fatal(err)
	}
	catalog := adversarialReleaseMirror(t, public, func(contact *netchan.Channel[ReleaseRequest], request ReleaseRequest, attempt int) {
		credits := make(chan ReleaseCredit, 1)
		if err := sendChannel(request.Reply, ReleaseResponse{Manifest: signed, Session: credits}, contact.Done, time.Second); err != nil {
			return
		}
		select {
		case credit := <-credits:
			_ = sendChannel(credit.Reply, ReleaseChunk{Offset: credit.Offset, Bytes: bytes.Repeat([]byte("x"), len(payload)), Final: true}, contact.Done, time.Second)
			select {
			case <-credits:
				awaitReleaseFinish(contact, contact.Done)
			case <-contact.Done:
			}
		case <-contact.Done:
		}
	})
	fallback := httptest.NewServer(http.NotFoundHandler())
	defer fallback.Close()
	catalog.client = fallback.Client()
	catalog.artifactBase = fallback.URL + "/"
	directory := t.TempDir()
	working := filepath.Join(directory, "sitebrush")
	if err := os.WriteFile(working, payload, 0700); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(directory, "candidate")
	if err := catalog.Download(manifest, candidate, nil); err == nil {
		t.Fatal("unchecked executable was accepted")
	}
	if _, err := os.Stat(candidate); !os.IsNotExist(err) {
		t.Fatal("failed candidate was retained", err)
	}
	if actual, err := os.ReadFile(working); err != nil || !bytes.Equal(actual, payload) {
		t.Fatal("working executable changed", err)
	}
}

func TestNetChanReleaseCatalogTrustAndOfficialFallbacks(t *testing.T) {
	_, public, _ := testRelease(t, []byte("release"))
	central, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	expectedPublic := append(ed25519.PublicKey(nil), public...)
	expectedCentral := append(ed25519.PublicKey(nil), central...)
	catalog := NewNetworkCatalog(public, central)
	public[0] ^= 1
	central[0] ^= 1
	if !catalog.PublicKey.Equal(expectedPublic) || !catalog.mirror.CentralKey.Equal(expectedCentral) || catalog.mirror.Address != ReleaseAddress {
		t.Fatal("catalog retained mutable caller-owned trust inputs")
	}
	if len(catalog.manifestURLs) != 3 || len(catalog.artifactBases) != 2 {
		t.Fatal("official publication fallback is missing")
	}
	for _, address := range append(catalog.manifestURLs, catalog.artifactBases...) {
		parsed, err := url.Parse(address)
		if err != nil || !officialURL(parsed) {
			t.Fatal("catalog configured an untrusted fallback", address, err)
		}
	}
	if _, err := verifiedReleaseTLS(nil); err == nil {
		t.Fatal("mirror identity can be omitted")
	}
	if err := ServeReleaseMirror(MirrorOptions{}, nil); err == nil {
		t.Fatal("mirror accepted missing keys")
	}
}

func TestNetChanReleaseCancellationWhileWaitingForChunk(t *testing.T) {
	manifest, public, key := testRelease(t, []byte("trusted release"))
	signed, err := Sign(manifest, key)
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	catalog := adversarialReleaseMirror(t, public, func(contact *netchan.Channel[ReleaseRequest], request ReleaseRequest, attempt int) {
		credits := make(chan ReleaseCredit, 1)
		if err := sendChannel(request.Reply, ReleaseResponse{Manifest: signed, Session: credits}, contact.Done, time.Second); err != nil {
			return
		}
		select {
		case <-credits:
			close(stop)
			awaitReleaseFinish(contact, contact.Done)
		case <-contact.Done:
		}
	})
	file, err := os.CreateTemp(t.TempDir(), "candidate")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := catalog.mirror.transfer(manifest, manifest.Artifacts[0], public, file, stop); err == nil {
		t.Fatal("canceled consumer continued waiting for bytes")
	}
	if info, err := file.Stat(); err != nil || info.Size() != 0 {
		t.Fatal("canceled stream wrote bytes", info, err)
	}
}

func TestNetChanReleaseShutdownDrainsActiveWorker(t *testing.T) {
	var active *netchan.Channel[ReleaseRequest]
	t.Run("active producer", func(t *testing.T) {
		catalog, manifest, _, _ := releaseMirrorFixture(t, []byte("release"))
		empty := sha256.Sum256(nil)
		contact, _, err := catalog.mirror.request(ReleaseRequest{Version: manifest.Version, ManifestDigest: manifestIdentity(manifest), OS: runtime.GOOS, Arch: runtime.GOARCH, PrefixSHA256: hex.EncodeToString(empty[:])}, nil)
		if err != nil {
			t.Fatal(err)
		}
		active = contact
		// The fixture stops the listener with this worker still awaiting credit.
	})
	if active == nil {
		return
	}
	defer closeReleaseContact(active)
	select {
	case _, open := <-active.Receive:
		if open {
			t.Fatal("stopped producer sent another message")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server shutdown left an active producer")
	}
}
