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

const Protocol = 1
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
	PublicKey    ed25519.PublicKey
	client       *http.Client
	manifestURL  string
	artifactBase string
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
	case "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return true
	}
	return false
}

func (catalog Catalog) Check(stop <-chan struct{}) (Manifest, string, error) {
	if len(catalog.PublicKey) != ed25519.PublicKeySize {
		return Manifest{}, "", errors.New("official release signing key is not configured")
	}
	response, err := catalog.get(catalog.manifestURL, stop)
	if err != nil {
		return Manifest{}, "", err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, manifestLimit+1))
	if err != nil {
		return Manifest{}, "", err
	}
	manifest, err := Verify(payload, catalog.PublicKey)
	digest := sha256.Sum256(payload)
	return manifest, hex.EncodeToString(digest[:]), err
}

func (catalog Catalog) Download(manifest Manifest, destination string, stop <-chan struct{}) error {
	artifact, err := manifest.Artifact(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	response, err := catalog.get(catalog.artifactBase+"release-"+manifest.Version+"/"+path.Base(artifact.Name), stop)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
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
	digest := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, digest), io.LimitReader(response.Body, artifact.Size+1))
	if err != nil {
		return err
	}
	if written != artifact.Size || hex.EncodeToString(digest.Sum(nil)) != artifact.SHA256 {
		return errors.New("release size or SHA-256 verification failed")
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	complete = true
	return nil
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
