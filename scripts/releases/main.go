package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/matveynator/sitebrush/v2/pkg/appupdate"
)

func main() {
	if err := run(os.Args[1:], os.Getenv("SITEBRUSH_RELEASE_PRIVATE_KEY"), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string, privateKeyText string, output io.Writer) error {
	flags := flag.NewFlagSet("releases", flag.ContinueOnError)
	flags.SetOutput(output)
	generate := flags.String("generate-key", "", "create a private signing key at a new private path and print its public key")
	directory := flags.String("directory", "", "directory containing the complete native server matrix")
	version := flags.String("version", "", "numeric stable build version")
	commit := flags.String("commit", "", "full release commit SHA")
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected release arguments")
	}
	if *generate != "" {
		publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		file, err := os.OpenFile(*generate, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, err = file.WriteString(base64.StdEncoding.EncodeToString(privateKey) + "\n")
		if err != nil {
			file.Close()
			return err
		}
		if err := file.Sync(); err != nil {
			file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		_, err = fmt.Fprintln(output, base64.StdEncoding.EncodeToString(publicKey))
		return err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(privateKeyText))
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return fmt.Errorf("SITEBRUSH_RELEASE_PRIVATE_KEY must contain an Ed25519 private key in base64")
	}
	if *directory == "" {
		return fmt.Errorf("-directory is required")
	}
	return appupdate.Publish(*directory, *version, *commit, ed25519.PrivateKey(key))
}
