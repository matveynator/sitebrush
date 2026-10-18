package appupdate

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type publishTarget struct {
	goos   string
	goarch string
}

// publishTargets is the native auto-update matrix produced by the current
// crosscompile pipeline. NetBSD is deliberately absent while the pinned
// modernc.org/sqlite version cannot build that target. Supported keeps accepting
// NetBSD artifacts so older signed manifests remain valid.
func publishTargets() []publishTarget {
	return []publishTarget{
		{goos: "darwin", goarch: "amd64"},
		{goos: "darwin", goarch: "arm64"},
		{goos: "linux", goarch: "amd64"},
		{goos: "linux", goarch: "arm64"},
		{goos: "windows", goarch: "amd64"},
		{goos: "windows", goarch: "arm64"},
		{goos: "freebsd", goarch: "amd64"},
		{goos: "freebsd", goarch: "arm64"},
		{goos: "openbsd", goarch: "amd64"},
		{goos: "openbsd", goarch: "arm64"},
	}
}

// Publish signs only the complete native auto-update matrix. Desktop packages,
// Linux/386 compatibility builds, and arbitrary build-directory files cannot
// become server update artifacts.
func Publish(directory, version, commit string, key ed25519.PrivateKey) error {
	trusted, err := TrustedKey()
	if err != nil {
		return err
	}
	return publish(directory, version, commit, key, trusted, CurrentCompatibility())
}

func publish(directory, version, commit string, key ed25519.PrivateKey, trusted ed25519.PublicKey, compatibility Compatibility) error {
	sequence, err := strconv.ParseUint(version, 10, 64)
	if err != nil {
		return err
	}
	if len(key) != ed25519.PrivateKeySize || !key.Public().(ed25519.PublicKey).Equal(trusted) {
		return fmt.Errorf("signing key does not match the embedded release trust anchor")
	}
	manifest := Manifest{Protocol: Protocol, Version: version, Sequence: sequence, Commit: commit, Published: time.Now().UTC(), Compatibility: compatibility}
	var checksums strings.Builder
	for _, target := range publishTargets() {
		name := BinaryName(target.goos, target.goarch)
		file, err := os.Open(filepath.Join(directory, name))
		if err != nil {
			return err
		}
		info, err := file.Stat()
		if err != nil {
			file.Close()
			return err
		}
		digest := sha256.New()
		size, err := io.Copy(digest, io.LimitReader(file, binaryLimit+1))
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if !info.Mode().IsRegular() || size != info.Size() || size > binaryLimit {
			return fmt.Errorf("invalid server artifact %s", name)
		}
		checksum := hex.EncodeToString(digest.Sum(nil))
		manifest.Artifacts = append(manifest.Artifacts, Artifact{OS: target.goos, Arch: target.goarch, Name: name, Size: size, SHA256: checksum})
		fmt.Fprintf(&checksums, "%s  %s\n", checksum, name)
	}
	payload, err := Sign(manifest, key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "SHA256SUMS"), []byte(checksums.String()), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, "update.json"), payload, 0o644)
}
