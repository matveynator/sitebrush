package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/matveynator/sitebrush/v2/pkg/appupdate"
)

func main() {
	generate := flag.String("generate-key", "", "create a private signing key at a new private path and print its public key")
	directory := flag.String("directory", "", "directory containing the complete native server matrix")
	version := flag.String("version", "", "numeric stable build version")
	commit := flag.String("commit", "", "full release commit SHA")
	flag.Parse()
	if *generate != "" {
		publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			fatal(err)
		}
		file, err := os.OpenFile(*generate, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			fatal(err)
		}
		_, err = file.WriteString(base64.StdEncoding.EncodeToString(privateKey) + "\n")
		if err != nil {
			file.Close()
			fatal(err)
		}
		if err := file.Sync(); err != nil {
			file.Close()
			fatal(err)
		}
		if err := file.Close(); err != nil {
			fatal(err)
		}
		fmt.Println(base64.StdEncoding.EncodeToString(publicKey))
		return
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("SITEBRUSH_RELEASE_PRIVATE_KEY")))
	if err != nil || len(key) != ed25519.PrivateKeySize {
		fatal(fmt.Errorf("SITEBRUSH_RELEASE_PRIVATE_KEY must contain an Ed25519 private key in base64"))
	}
	if *directory == "" {
		fatal(fmt.Errorf("-directory is required"))
	}
	if err := appupdate.Publish(*directory, *version, *commit, ed25519.PrivateKey(key)); err != nil {
		fatal(err)
	}
}

func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
