package diskusage

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestFileSizeMatchesFileInfoDiskBytes(t *testing.T) {
	rootPath := t.TempDir()
	filePath := filepath.Join(rootPath, "sample.bin")
	if err := os.WriteFile(filePath, []byte("sample payload"), 0o644); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	fileInfo, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat sample file: %v", err)
	}

	expectedBytes := fileInfoDiskBytes(fileInfo)
	if actualBytes := FileSize(filePath); actualBytes != expectedBytes {
		t.Fatalf("FileSize(%q) = %d, want %d", filePath, actualBytes, expectedBytes)
	}
}

func TestFileSizeReturnsZeroForMissingPath(t *testing.T) {
	if actualBytes := FileSize(filepath.Join(t.TempDir(), "missing.bin")); actualBytes != 0 {
		t.Fatalf("FileSize(missing) = %d, want 0", actualBytes)
	}
}

func TestDirectorySizeSumsNestedEntries(t *testing.T) {
	rootPath := t.TempDir()
	nestedPath := filepath.Join(rootPath, "nested")
	if err := os.MkdirAll(nestedPath, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "root.txt"), []byte("root"), 0o644); err != nil {
		t.Fatalf("write root file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nestedPath, "child.txt"), []byte("child"), 0o644); err != nil {
		t.Fatalf("write nested file: %v", err)
	}

	var expectedBytes int64
	walkErr := filepath.WalkDir(rootPath, func(currentPath string, currentEntry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fileInfo, statErr := currentEntry.Info()
		if statErr != nil {
			return statErr
		}
		expectedBytes += fileInfoDiskBytes(fileInfo)
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk test directory: %v", walkErr)
	}

	if actualBytes := DirectorySize(rootPath); actualBytes != expectedBytes {
		t.Fatalf("DirectorySize(%q) = %d, want %d", rootPath, actualBytes, expectedBytes)
	}
}

func TestDirectorySizeReturnsZeroForMissingPath(t *testing.T) {
	if actualBytes := DirectorySize(filepath.Join(t.TempDir(), "missing")); actualBytes != 0 {
		t.Fatalf("DirectorySize(missing) = %d, want 0", actualBytes)
	}
}
