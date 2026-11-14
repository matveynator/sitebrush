// Package bootstrapcode owns the short-lived first-administrator install code.
package bootstrapcode

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const codeFile = "storage/security/install-code.json"

type record struct {
	Digest   string `json:"digest"`
	Expires  int64  `json:"expires"`
	Attempts int    `json:"attempts"`
}

type Request struct {
	Code    string
	Consume bool
	Reply   chan bool
}

// Issue keeps the bearer code off disk and returns it only to the installer.
func Issue(storagePath string, replace bool) (string, error) {
	if err := os.MkdirAll(storagePath, 0o700); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(storagePath)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err := root.MkdirAll(filepath.Dir(codeFile), 0o700); err != nil {
		return "", err
	}
	if !replace {
		if _, err := root.Lstat(codeFile); err == nil {
			return "", nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	secret := make([]byte, 24)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	code := hex.EncodeToString(secret)
	digest := sha256.Sum256([]byte(code))
	stored, err := json.Marshal(record{Digest: hex.EncodeToString(digest[:]), Expires: time.Now().Add(15 * time.Minute).Unix()})
	if err != nil {
		return "", err
	}
	if err := writeRecord(root, stored); err != nil {
		return "", err
	}
	return code, nil
}

func writeRecord(root *os.Root, stored []byte) error {
	temporary := codeFile + ".new"
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	_, writeErr := file.Write(stored)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	return root.Rename(temporary, codeFile)
}

// Start serializes attempts and consumption through one channel owner.
func Start(storagePath string, stop <-chan struct{}) chan<- Request {
	requests := make(chan Request)
	go func() {
		for {
			select {
			case request := <-requests:
				valid := verify(storagePath, request.Code, request.Consume)
				select {
				case request.Reply <- valid:
				case <-stop:
					return
				}
			case <-stop:
				return
			}
		}
	}()
	return requests
}

func Verify(requests chan<- Request, code string, consume bool, stop <-chan struct{}) bool {
	if requests == nil || len(code) != 48 {
		return false
	}
	reply := make(chan bool, 1)
	select {
	case requests <- Request{Code: code, Consume: consume, Reply: reply}:
	case <-stop:
		return false
	}
	select {
	case valid := <-reply:
		return valid
	case <-stop:
		return false
	}
}

func verify(storagePath, code string, consume bool) bool {
	root, err := os.OpenRoot(storagePath)
	if err != nil {
		return false
	}
	defer root.Close()
	stored, err := root.ReadFile(codeFile)
	if err != nil {
		return false
	}
	var current record
	if json.Unmarshal(stored, &current) != nil || current.Attempts >= 8 || time.Now().Unix() >= current.Expires {
		return false
	}
	digest := sha256.Sum256([]byte(code))
	expected, err := hex.DecodeString(current.Digest)
	valid := err == nil && len(expected) == len(digest) && subtle.ConstantTimeCompare(expected, digest[:]) == 1
	if valid && consume {
		return root.Remove(codeFile) == nil
	}
	if !consume && !valid {
		current.Attempts++
		updated, err := json.Marshal(current)
		if err != nil || writeRecord(root, updated) != nil {
			return false
		}
	}
	return valid
}

// Fingerprint prints only the public certificate digest, never the private key.
func Fingerprint(storagePath string) (string, error) {
	root, err := os.OpenRoot(storagePath)
	if err != nil {
		return "", err
	}
	defer root.Close()
	certificate, err := root.ReadFile(filepath.Join("storage", "keys", "tls", "sitebrush-fallback.pem"))
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(certificate)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", fmt.Errorf("fallback certificate is invalid")
	}
	digest := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(digest[:]), nil
}
