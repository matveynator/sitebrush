package analytics

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreDirectoryPathRejectsTraversalComponents(t *testing.T) {
	root := t.TempDir()
	for _, domain := range []string{"../escape", "nested/domain", `nested\domain`, ".", ".."} {
		if _, err := storeDirectoryPath(root, domain); err == nil {
			t.Fatalf("storeDirectoryPath accepted unsafe domain %q", domain)
		}
	}
	directory, err := storeDirectoryPath(root, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(root, directory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatalf("analytics directory escaped root: %q (%v)", directory, err)
	}
}

func TestDatabaseNameIsSinglePathComponent(t *testing.T) {
	for _, domain := range []string{"example.com", "../escape", `..\escape`} {
		name := DatabaseName(domain)
		if name != filepath.Base(name) || strings.ContainsAny(name, `/\`) {
			t.Fatalf("DatabaseName(%q) produced unsafe component %q", domain, name)
		}
	}
}
