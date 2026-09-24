package storagejail

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSecurityBoundaryRejectsTraversalAcrossEveryMutation(t *testing.T) {
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "storage")
	outsidePath := filepath.Join(parent, "outside")
	if err := os.MkdirAll(outsidePath, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := New(rootPath)
	if err != nil {
		t.Fatal(err)
	}

	escape := filepath.Join("..", "outside", "owned.txt")
	if err := root.WriteFile(escape, []byte("owned"), 0o600); err == nil {
		t.Fatal("SECURITY: WriteFile accepted parent traversal outside the storage root")
	}
	if _, err := root.Create(escape); err == nil {
		t.Fatal("SECURITY: Create accepted parent traversal outside the storage root")
	}
	if err := root.MkdirAll(filepath.Join("..", "outside", "created"), 0o755); err == nil {
		t.Fatal("SECURITY: MkdirAll accepted parent traversal outside the storage root")
	}
	if err := root.Rename("missing", escape); err == nil {
		t.Fatal("SECURITY: Rename accepted a destination outside the storage root")
	}
	if err := root.Remove(escape); err == nil {
		t.Fatal("SECURITY: Remove accepted parent traversal outside the storage root")
	}
	if err := root.RemoveAll(filepath.Join("..", "outside")); err == nil {
		t.Fatal("SECURITY: RemoveAll accepted parent traversal outside the storage root")
	}
	if _, err := root.Open(escape); err == nil {
		t.Fatal("SECURITY: Open accepted parent traversal outside the storage root")
	}
	if _, err := root.ReadFile(escape); err == nil {
		t.Fatal("SECURITY: ReadFile accepted parent traversal outside the storage root")
	}

	if _, err := os.Stat(filepath.Join(outsidePath, "owned.txt")); !os.IsNotExist(err) {
		t.Fatalf("SECURITY: traversal created an outside file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outsidePath, "created")); !os.IsNotExist(err) {
		t.Fatalf("SECURITY: traversal created an outside directory: %v", err)
	}
}

func TestSecurityBoundaryRejectsNestedSymlinkEscapeForReadsAndRenames(t *testing.T) {
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "storage")
	outsidePath := filepath.Join(parent, "outside")
	if err := os.MkdirAll(outsidePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsidePath, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := New(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(rootPath, "escape")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	if _, err := root.ReadFile(filepath.Join("escape", "secret.txt")); err == nil {
		t.Fatal("SECURITY: ReadFile followed a symlink outside the storage root")
	}
	if _, err := root.Open(filepath.Join("escape", "secret.txt")); err == nil {
		t.Fatal("SECURITY: Open followed a symlink outside the storage root")
	}
	if err := root.Rename(filepath.Join("escape", "secret.txt"), "inside.txt"); err == nil {
		t.Fatal("SECURITY: Rename accepted a source reached through an escaping symlink")
	}
	if err := root.Rename("missing.txt", filepath.Join("escape", "new.txt")); err == nil {
		t.Fatal("SECURITY: Rename accepted a destination reached through an escaping symlink")
	}

	payload, err := os.ReadFile(filepath.Join(outsidePath, "secret.txt"))
	if err != nil || string(payload) != "secret" {
		t.Fatalf("SECURITY: outside file changed during rejected operations: %q, %v", payload, err)
	}
}

func TestSecurityBoundaryRelativePathRejectsSiblingPrefixConfusion(t *testing.T) {
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "site")
	root, err := New(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(parent, "site-attacker", "payload")
	if _, err := root.RelativePath(sibling); err == nil {
		t.Fatal("SECURITY: sibling path sharing the storage-root prefix was accepted")
	}
}
