package storagejail

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRootRejectsTraversalAndSymlinkEscape(t *testing.T) {
	parentPath := t.TempDir()
	rootPath := filepath.Join(parentPath, "storage")
	outsidePath := filepath.Join(parentPath, "outside")
	if err := os.MkdirAll(outsidePath, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := New(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := root.RelativePath(filepath.Join(rootPath, "..", "outside", "secret")); err == nil {
		t.Fatal("parent traversal was accepted")
	}
	if err := os.Symlink(outsidePath, filepath.Join(rootPath, "escape")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if err := root.WriteFile(filepath.Join("escape", "secret"), []byte("secret"), 0o600); err == nil {
		t.Fatal("symlink escape was accepted")
	}
	if _, err := os.Stat(filepath.Join(outsidePath, "secret")); !os.IsNotExist(err) {
		t.Fatalf("outside file was created: %v", err)
	}
}

func TestRootReadsAndWritesInsideCapability(t *testing.T) {
	root, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile(filepath.Join("site", "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	payload, err := root.ReadFile(filepath.Join("site", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "ok" {
		t.Fatalf("payload = %q", payload)
	}
}
