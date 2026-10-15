package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGenerateSigningKeyIsPrivateAndNeverOverwrites(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "release.key")
	var output bytes.Buffer
	if err := run([]string{"-generate-key", filename}, "", &output); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(contents)))
	if err != nil || len(decoded) != ed25519.PrivateKeySize {
		t.Fatal("invalid generated key", err)
	}
	public, err := base64.StdEncoding.DecodeString(strings.TrimSpace(output.String()))
	if err != nil || !ed25519.PrivateKey(decoded).Public().(ed25519.PublicKey).Equal(ed25519.PublicKey(public)) {
		t.Fatal("public/private key mismatch", err)
	}
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("private key permissions", info.Mode())
	}
	output.Reset()
	if err := run([]string{"-generate-key", filename}, "", &output); err == nil {
		t.Fatal("existing key was overwritten")
	}
	after, err := os.ReadFile(filename)
	if err != nil || !bytes.Equal(contents, after) || output.Len() != 0 {
		t.Fatal("failed generation exposed or changed key", err)
	}
}

func TestSigningCommandRejectsMissingKeysAndArguments(t *testing.T) {
	private := base64.StdEncoding.EncodeToString(ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize)))
	for _, scenario := range []struct {
		arguments []string
		key       string
		expected  string
	}{
		{nil, "", "private key in base64"},
		{nil, "invalid", "private key in base64"},
		{nil, private, "-directory is required"},
		{[]string{"-unknown"}, private, "flag provided but not defined"},
		{[]string{"unexpected"}, private, "unexpected release arguments"},
		{[]string{"-directory", t.TempDir(), "-version", "2", "-commit", strings.Repeat("a", 40)}, private, "does not match"},
		{[]string{"-generate-key", filepath.Join(t.TempDir(), "absent", "key")}, "", ""},
	} {
		var output bytes.Buffer
		err := run(scenario.arguments, scenario.key, &output)
		if err == nil || !strings.Contains(err.Error(), scenario.expected) {
			t.Fatalf("arguments=%v error=%v", scenario.arguments, err)
		}
		if strings.Contains(output.String(), private) {
			t.Fatal("private key printed")
		}
	}
	var help bytes.Buffer
	if err := run([]string{"-help"}, "", &help); err != nil || !strings.Contains(help.String(), "generate-key") {
		t.Fatal("missing signing help", err)
	}
}
