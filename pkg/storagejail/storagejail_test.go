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
	fileSize, err := root.FileSize(filepath.Join("site", "index.html"))
	if err != nil || fileSize != 2 {
		t.Fatalf("FileSize = %d, %v", fileSize, err)
	}
	directorySize, err := root.DirectorySize("site")
	if err != nil || directorySize != 2 {
		t.Fatalf("DirectorySize = %d, %v", directorySize, err)
	}
}

func TestRootFileLifecycleAndRejectedOperations(t *testing.T) {
	if _, err := New(" "); err == nil {
		t.Fatal("empty storage root was accepted")
	}
	rootPath := t.TempDir()
	root, err := New(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if relative, err := root.RelativePath(rootPath); err != nil || relative != "." {
		t.Fatalf("root relative path = %q, %v", relative, err)
	}
	if err := root.MkdirAll("nested/created", 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := root.Create("nested/created/source.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("source"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	opened, err := root.Open("nested/created/source.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := root.Stat("nested/created/source.txt")
	if err != nil || info.Size() != 6 {
		t.Fatalf("source info = %#v, %v", info, err)
	}
	if size, err := root.FileSize("nested/created"); err != nil || size != 0 {
		t.Fatalf("directory file size = %d, %v", size, err)
	}
	if err := root.Rename("nested/created/source.txt", "renamed/target.txt"); err != nil {
		t.Fatal(err)
	}
	if payload, err := root.ReadFile("renamed/target.txt"); err != nil || string(payload) != "source" {
		t.Fatalf("renamed file = %q, %v", payload, err)
	}
	for _, err := range []error{
		root.Remove("."),
		root.RemoveAll("."),
		root.Rename(".", "other"),
		root.Rename("other", "."),
	} {
		if err == nil {
			t.Fatal("root operation unexpectedly succeeded")
		}
	}
	if err := root.Remove("renamed/target.txt"); err != nil {
		t.Fatal(err)
	}
	if err := root.RemoveAll("nested"); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Open("missing"); err == nil {
		t.Fatal("missing file opened")
	}
}


// BEGIN storage capability escape regression tests.

func TestRootRejectsSymlinkParentForEveryMutation(t *testing.T) {
	parentPath := t.TempDir()
	rootPath := filepath.Join(parentPath, "storage")
	outsidePath := filepath.Join(parentPath, "outside")
	if err := os.MkdirAll(outsidePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsidePath, "existing.txt"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := New(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(rootPath, "escape")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if err := root.WriteFile("inside.txt", []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}

	if file, err := root.Create(filepath.Join("escape", "created.txt")); err == nil {
		_ = file.Close()
		t.Fatal("Create followed a symlinked parent outside storage")
	}
	if err := root.MkdirAll(filepath.Join("escape", "nested"), 0o755); err == nil {
		t.Fatal("MkdirAll followed a symlinked parent outside storage")
	}
	if err := root.Rename("inside.txt", filepath.Join("escape", "renamed.txt")); err == nil {
		t.Fatal("Rename moved a file through a symlinked destination outside storage")
	}
	if err := root.Rename(filepath.Join("escape", "existing.txt"), "renamed-from-outside.txt"); err == nil {
		t.Fatal("Rename followed a symlinked source outside storage")
	}
	if _, err := root.Stat("renamed-from-outside.txt"); !os.IsNotExist(err) {
		t.Fatalf("Rename created an in-root file from a symlinked source: %v", err)
	}
	if err := root.Remove(filepath.Join("escape", "existing.txt")); err == nil {
		t.Fatal("Remove followed a symlinked parent outside storage")
	}
	if err := root.RemoveAll(filepath.Join("escape", "nested")); err == nil {
		t.Fatal("RemoveAll followed a symlinked parent outside storage")
	}

	if payload, err := os.ReadFile(filepath.Join(outsidePath, "existing.txt")); err != nil || string(payload) != "outside" {
		t.Fatalf("outside file changed: %q, %v", payload, err)
	}
	for _, outsideName := range []string{"created.txt", "renamed.txt"} {
		if _, err := os.Stat(filepath.Join(outsidePath, outsideName)); !os.IsNotExist(err) {
			t.Fatalf("outside mutation %q occurred: %v", outsideName, err)
		}
	}
}

func TestRootRejectsTraversalAndAbsoluteMutationPaths(t *testing.T) {
	parentPath := t.TempDir()
	rootPath := filepath.Join(parentPath, "storage")
	outsidePath := filepath.Join(parentPath, "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := New(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile("inside.txt", []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}

	mutationPaths := []string{
		filepath.Join("..", "outside.txt"),
		outsidePath,
	}
	for _, mutationPath := range mutationPaths {
		if err := root.WriteFile(mutationPath, []byte("escape"), 0o600); err == nil {
			t.Fatalf("WriteFile accepted escape path %q", mutationPath)
		}
		if file, err := root.Create(mutationPath); err == nil {
			_ = file.Close()
			t.Fatalf("Create accepted escape path %q", mutationPath)
		}
		if err := root.MkdirAll(mutationPath, 0o755); err == nil {
			t.Fatalf("MkdirAll accepted escape path %q", mutationPath)
		}
		if err := root.Rename("inside.txt", mutationPath); err == nil {
			t.Fatalf("Rename accepted escape destination %q", mutationPath)
		}
		if err := root.Rename(mutationPath, "renamed-from-outside.txt"); err == nil {
			t.Fatalf("Rename accepted escape source %q", mutationPath)
		}
		if _, err := root.Stat("renamed-from-outside.txt"); !os.IsNotExist(err) {
			t.Fatalf("Rename created an in-root file from escape source %q: %v", mutationPath, err)
		}
		if err := root.Remove(mutationPath); err == nil {
			t.Fatalf("Remove accepted escape path %q", mutationPath)
		}
		if err := root.RemoveAll(mutationPath); err == nil {
			t.Fatalf("RemoveAll accepted escape path %q", mutationPath)
		}
	}
	payload, err := os.ReadFile(outsidePath)
	if err != nil || string(payload) != "outside" {
		t.Fatalf("outside file changed: %q, %v", payload, err)
	}
}

func TestRootRejectsSymlinkSwapBeforeWrite(t *testing.T) {
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
	safeParent := filepath.Join(rootPath, "candidate")
	if err := os.MkdirAll(safeParent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(safeParent); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsidePath, safeParent); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	if err := root.WriteFile(filepath.Join("candidate", "swapped.txt"), []byte("escape"), 0o600); err == nil {
		t.Fatal("write followed a parent replaced by a symlink")
	}
	if _, err := os.Stat(filepath.Join(outsidePath, "swapped.txt")); !os.IsNotExist(err) {
		t.Fatalf("symlink swap created outside file: %v", err)
	}
}

// END storage capability escape regression tests.
