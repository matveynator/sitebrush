package appupdate

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishUsesCurrentCrosscompileUpdateMatrix(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	for _, target := range publishTargets() {
		if target.goos == "netbsd" {
			t.Fatal("publish matrix unexpectedly requires NetBSD")
		}
		name := BinaryName(target.goos, target.goarch)
		if err := os.WriteFile(filepath.Join(directory, name), []byte(target.goos+"/"+target.goarch), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	compatibility := Compatibility{Generation: 1, ReadsFrom: 1, ReadsThrough: 1, WritesCompatible: true}
	if err := publish(directory, "927", strings.Repeat("a", 40), privateKey, publicKey, compatibility); err != nil {
		t.Fatalf("publish without NetBSD artifacts failed: %v", err)
	}
	payload, err := os.ReadFile(filepath.Join(directory, "update.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := Verify(payload, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Artifacts) != len(publishTargets()) {
		t.Fatalf("manifest artifacts = %d, want %d", len(manifest.Artifacts), len(publishTargets()))
	}
	for _, artifact := range manifest.Artifacts {
		if artifact.OS == "netbsd" {
			t.Fatalf("manifest unexpectedly contains NetBSD artifact: %+v", artifact)
		}
	}
}
