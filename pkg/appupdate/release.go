// Package appupdate owns the server version lifecycle independently of HTTP clients.
package appupdate

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Protocol 2 prevents prototype launchers from installing binaries whose frozen
// local contact schema they cannot speak. The persistent-data contract is separate.
const Protocol = 2
const manifestLimit = 256 << 10
const binaryLimit int64 = 256 << 20
const Repository = "matveynator/sitebrush"

type Artifact struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Compatibility covers all persistent formats, including non-SQL state. A release
// author must explicitly declare whether its predecessor can read and write them.
type Compatibility struct {
	Generation       int  `json:"generation"`
	ReadsFrom        int  `json:"reads_from"`
	ReadsThrough     int  `json:"reads_through"`
	WritesCompatible bool `json:"writes_compatible"`
	MigratesFrom     int  `json:"migrates_from,omitempty"`
}

func (compatibility Compatibility) MigrationMinimum() int {
	if compatibility.MigratesFrom > 0 {
		return compatibility.MigratesFrom
	}
	return compatibility.ReadsFrom
}

type Manifest struct {
	Protocol      int           `json:"protocol"`
	Version       string        `json:"version"`
	Sequence      uint64        `json:"sequence"`
	Commit        string        `json:"commit"`
	Published     time.Time     `json:"published"`
	Compatibility Compatibility `json:"compatibility"`
	Artifacts     []Artifact    `json:"artifacts"`
}

type SignedManifest struct {
	Manifest  json.RawMessage `json:"manifest"`
	Signature string          `json:"signature"`
}

func Sign(manifest Manifest, privateKey ed25519.PrivateKey) ([]byte, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid release signing key")
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	return json.Marshal(SignedManifest{Manifest: payload, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))})
}

func Verify(payload []byte, publicKey ed25519.PublicKey) (Manifest, error) {
	if len(payload) > manifestLimit || len(publicKey) != ed25519.PublicKeySize {
		return Manifest{}, errors.New("release trust is not configured or manifest is too large")
	}
	var signed SignedManifest
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&signed); err != nil {
		return Manifest{}, fmt.Errorf("decode signed release: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return Manifest{}, errors.New("trailing signed release content")
	}
	signature, err := base64.StdEncoding.DecodeString(signed.Signature)
	if err != nil || !ed25519.Verify(publicKey, signed.Manifest, signature) {
		return Manifest{}, errors.New("release signature verification failed")
	}
	var manifest Manifest
	decoder = json.NewDecoder(bytes.NewReader(signed.Manifest))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, manifest.Validate()
}

func (manifest Manifest) Validate() error {
	sequence, err := strconv.ParseUint(manifest.Version, 10, 64)
	if err != nil || sequence == 0 || sequence != manifest.Sequence || manifest.Protocol != Protocol {
		return errors.New("unsupported release version or supervisor protocol")
	}
	if len(manifest.Commit) != 40 {
		return errors.New("release commit must be a full SHA")
	}
	if _, err := hex.DecodeString(manifest.Commit); err != nil {
		return errors.New("invalid release commit")
	}
	compatibility := manifest.Compatibility
	if compatibility.Generation < 1 || compatibility.ReadsFrom < 1 || compatibility.ReadsFrom > compatibility.Generation || compatibility.ReadsThrough < compatibility.Generation || compatibility.MigrationMinimum() < 1 || compatibility.MigrationMinimum() > compatibility.ReadsFrom {
		return errors.New("missing persistent-data compatibility contract")
	}
	if manifest.Published.IsZero() || manifest.Published.After(time.Now().Add(24*time.Hour)) {
		return errors.New("invalid release publication time")
	}
	if len(manifest.Artifacts) == 0 || len(manifest.Artifacts) > 32 {
		return errors.New("invalid release artifact count")
	}
	seen := make(map[string]bool)
	for _, artifact := range manifest.Artifacts {
		key := artifact.OS + "/" + artifact.Arch
		digest, digestErr := hex.DecodeString(artifact.SHA256)
		if !Supported(artifact.OS, artifact.Arch) || seen[key] || artifact.Name != BinaryName(artifact.OS, artifact.Arch) || artifact.Size < 1 || artifact.Size > binaryLimit || digestErr != nil || len(digest) != sha256.Size {
			return fmt.Errorf("invalid release artifact %q", key)
		}
		seen[key] = true
	}
	return nil
}

func Supported(goos, goarch string) bool {
	if goarch != "amd64" && goarch != "arm64" {
		return false
	}
	switch goos {
	case "linux", "darwin", "windows", "freebsd", "openbsd", "netbsd":
		return true
	}
	return false
}

func BinaryName(goos, goarch string) string {
	name := "sitebrush_" + goos + "_" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func (manifest Manifest) Artifact(goos, goarch string) (Artifact, error) {
	for _, artifact := range manifest.Artifacts {
		if artifact.OS == goos && artifact.Arch == goarch {
			return artifact, nil
		}
	}
	return Artifact{}, fmt.Errorf("no server release for %s/%s", goos, goarch)
}

// GitHub is an external HTTPS adapter. Signed bytes, rather than API metadata or
// the mutable stable tag, establish identity and select a specific artifact.
type Catalog struct {
	PublicKey     ed25519.PublicKey
	client        *http.Client
	manifestURL   string
	artifactBase  string
	mirror        *ReleaseClient
	manifestURLs  []string
	artifactBases []string
}

func NewCatalog(publicKey ed25519.PublicKey) Catalog {
	return Catalog{PublicKey: append(ed25519.PublicKey(nil), publicKey...), client: &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(request *http.Request, previous []*http.Request) error {
		if len(previous) > 4 || !officialURL(request.URL) {
			return errors.New("untrusted release redirect")
		}
		return nil
	}}, manifestURL: "https://github.com/" + Repository + "/releases/download/stable-release/update.json", artifactBase: "https://github.com/" + Repository + "/releases/download/"}
}

func officialURL(address *url.URL) bool {
	if address.Scheme != "https" || address.User != nil || address.Port() != "" {
		return false
	}
	switch address.Hostname() {
	case "github.com":
		return strings.HasPrefix(address.Path, "/"+Repository+"/releases/download/")
	case "sitebrush.com", "sitebrush.ru":
		return strings.HasPrefix(address.Path, "/download/")
	case "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return true
	}
	return false
}

func (catalog Catalog) Check(stop <-chan struct{}) (Manifest, string, error) {
	if len(catalog.PublicKey) != ed25519.PublicKeySize {
		return Manifest{}, "", errors.New("official release signing key is not configured")
	}
	var failure error
	if catalog.mirror != nil {
		manifest, digest, err := catalog.mirror.check(catalog.PublicKey, stop)
		if err == nil {
			return manifest, digest, nil
		}
		failure = err
	}
	addresses := catalog.manifestURLs
	if len(addresses) == 0 {
		addresses = []string{catalog.manifestURL}
	}
	for _, address := range addresses {
		response, err := catalog.get(address, stop)
		if err != nil {
			failure = err
			continue
		}
		payload, readErr := io.ReadAll(io.LimitReader(response.Body, manifestLimit+1))
		closeErr := response.Body.Close()
		if readErr != nil {
			failure = readErr
			continue
		}
		if closeErr != nil {
			failure = closeErr
			continue
		}
		manifest, err := Verify(payload, catalog.PublicKey)
		if err != nil {
			failure = err
			continue
		}
		digest := sha256.Sum256(payload)
		return manifest, hex.EncodeToString(digest[:]), nil
	}
	return Manifest{}, "", failure
}

func (catalog Catalog) Download(manifest Manifest, destination string, stop <-chan struct{}) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	artifact, err := manifest.Artifact(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	// The consumer owns an exclusive candidate file through every transport.
	// A failed transfer never overwrites a working or pre-existing executable.
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0700)
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		file.Close()
		if !complete {
			os.Remove(destination)
		}
	}()
	var failure error
	if catalog.mirror != nil {
		failure = catalog.mirror.download(manifest, catalog.PublicKey, file, stop)
		if failure == nil {
			failure = verifyReleaseFile(file, artifact, stop)
		}
		if failure == nil {
			if err := file.Close(); err != nil {
				return err
			}
			complete = true
			return nil
		}
	}
	addresses := make([]string, 0, len(catalog.artifactBases)+1)
	for _, base := range catalog.artifactBases {
		addresses = append(addresses, base+manifest.Version+"/server-app/"+path.Base(artifact.Name))
	}
	addresses = append(addresses, catalog.artifactBase+"release-"+manifest.Version+"/"+path.Base(artifact.Name))
	for _, address := range addresses {
		select {
		case <-stop:
			return errors.New("release download canceled")
		default:
		}
		if err := file.Truncate(0); err != nil {
			return err
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		response, err := catalog.get(address, stop)
		if err != nil {
			failure = err
			continue
		}
		_, copyErr := io.Copy(file, io.LimitReader(response.Body, artifact.Size+1))
		closeErr := response.Body.Close()
		if copyErr != nil {
			failure = copyErr
			continue
		}
		if closeErr != nil {
			failure = closeErr
			continue
		}
		if err := verifyReleaseFile(file, artifact, stop); err != nil {
			failure = err
			continue
		}
		if err := file.Close(); err != nil {
			return err
		}
		complete = true
		return nil
	}
	return failure
}

func verifyReleaseFile(file *os.File, artifact Artifact, stop <-chan struct{}) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	digest := sha256.New()
	written, err := io.Copy(digest, sessionReader{reader: io.LimitReader(file, artifact.Size+1), stop: stop})
	if err != nil {
		return err
	}
	if written != artifact.Size || hex.EncodeToString(digest.Sum(nil)) != artifact.SHA256 {
		return errors.New("release size or SHA-256 verification failed")
	}
	return file.Sync()
}

func (catalog Catalog) get(address string, stop <-chan struct{}) (*http.Response, error) {
	select {
	case <-stop:
		return nil, errors.New("release consumer disconnected")
	default:
	}
	request, err := http.NewRequest(http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	// Request cancellation is confined to the standard-library HTTP boundary.
	requestStop := make(chan struct{})
	request.Cancel = requestStop
	finished := make(chan struct{})
	go func() {
		select {
		case <-stop:
			close(requestStop)
		case <-finished:
		}
	}()
	response, err := catalog.client.Do(request)
	if err != nil {
		close(finished)
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		close(finished)
		return nil, fmt.Errorf("official release download returned HTTP %d", response.StatusCode)
	}
	response.Body = &completionBody{ReadCloser: response.Body, finished: finished}
	return response, nil
}

type completionBody struct {
	io.ReadCloser
	finished chan struct{}
}

func (body *completionBody) Close() error {
	err := body.ReadCloser.Close()
	if body.finished != nil {
		close(body.finished)
		body.finished = nil
	}
	return err
}
