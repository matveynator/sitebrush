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

// Publish signs only the complete native server matrix. Desktop packages and
// arbitrary build-directory files cannot become server update artifacts.
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
	for _, goos := range []string{"darwin", "linux", "windows", "freebsd", "openbsd", "netbsd"} {
		for _, goarch := range []string{"amd64", "arm64"} {
			name := BinaryName(goos, goarch)
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
			manifest.Artifacts = append(manifest.Artifacts, Artifact{OS: goos, Arch: goarch, Name: name, Size: size, SHA256: checksum})
			fmt.Fprintf(&checksums, "%s  %s\n", checksum, name)
		}
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
