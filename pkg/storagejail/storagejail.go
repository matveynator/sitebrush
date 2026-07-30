package storagejail

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Root struct {
	path string
}

func New(rootPath string) (Root, error) {
	cleanedRootPath, err := filepath.Abs(strings.TrimSpace(rootPath))
	if err != nil || strings.TrimSpace(rootPath) == "" {
		return Root{}, errors.New("storage root is invalid")
	}
	if err := os.MkdirAll(cleanedRootPath, 0o755); err != nil {
		return Root{}, err
	}
	root, err := os.OpenRoot(cleanedRootPath)
	if err != nil {
		return Root{}, err
	}
	_ = root.Close()
	return Root{path: cleanedRootPath}, nil
}

func (root Root) RelativePath(candidatePath string) (string, error) {
	candidateAbsolutePath, err := filepath.Abs(candidatePath)
	if err != nil {
		return "", err
	}
	relativePath, err := filepath.Rel(root.path, candidateAbsolutePath)
	if err != nil || filepath.IsAbs(relativePath) || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(os.PathSeparator)) {
		return "", errors.New("path escapes storage root")
	}
	if relativePath == "" {
		return ".", nil
	}
	return relativePath, nil
}

func (root Root) withOpenRoot(operation func(*os.Root) error) error {
	openRoot, err := os.OpenRoot(root.path)
	if err != nil {
		return err
	}
	defer openRoot.Close()
	return operation(openRoot)
}

func (root Root) MkdirAll(relativePath string, perm fs.FileMode) error {
	return root.withOpenRoot(func(openRoot *os.Root) error {
		return openRoot.MkdirAll(relativePath, perm)
	})
}

func (root Root) Open(relativePath string) (*os.File, error) {
	openRoot, err := os.OpenRoot(root.path)
	if err != nil {
		return nil, err
	}
	file, err := openRoot.Open(relativePath)
	_ = openRoot.Close()
	return file, err
}

func (root Root) Create(relativePath string) (*os.File, error) {
	openRoot, err := os.OpenRoot(root.path)
	if err != nil {
		return nil, err
	}
	if err := openRoot.MkdirAll(filepath.Dir(relativePath), 0o755); err != nil {
		_ = openRoot.Close()
		return nil, err
	}
	file, err := openRoot.Create(relativePath)
	_ = openRoot.Close()
	return file, err
}

func (root Root) ReadFile(relativePath string) ([]byte, error) {
	var payload []byte
	err := root.withOpenRoot(func(openRoot *os.Root) error {
		var readErr error
		payload, readErr = openRoot.ReadFile(relativePath)
		return readErr
	})
	return payload, err
}

func (root Root) Stat(relativePath string) (fs.FileInfo, error) {
	var fileInfo fs.FileInfo
	err := root.withOpenRoot(func(openRoot *os.Root) error {
		var statErr error
		fileInfo, statErr = openRoot.Stat(relativePath)
		return statErr
	})
	return fileInfo, err
}

func (root Root) FileSize(relativePath string) (int64, error) {
	fileInfo, err := root.Stat(relativePath)
	if err != nil || fileInfo.IsDir() {
		return 0, err
	}
	return fileInfo.Size(), nil
}

func (root Root) DirectorySize(relativePath string) (int64, error) {
	var totalBytes int64
	err := root.withOpenRoot(func(openRoot *os.Root) error {
		return fs.WalkDir(openRoot.FS(), relativePath, func(filePath string, directoryEntry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if directoryEntry.IsDir() {
				return nil
			}
			fileInfo, infoErr := directoryEntry.Info()
			if infoErr != nil {
				return infoErr
			}
			totalBytes += fileInfo.Size()
			return nil
		})
	})
	return totalBytes, err
}

func (root Root) WriteFile(relativePath string, payload []byte, perm fs.FileMode) error {
	return root.withOpenRoot(func(openRoot *os.Root) error {
		if err := openRoot.MkdirAll(filepath.Dir(relativePath), 0o755); err != nil {
			return err
		}
		return openRoot.WriteFile(relativePath, payload, perm)
	})
}

func (root Root) Remove(relativePath string) error {
	if relativePath == "." {
		return errors.New("storage root cannot be removed")
	}
	return root.withOpenRoot(func(openRoot *os.Root) error {
		return openRoot.Remove(relativePath)
	})
}

func (root Root) RemoveAll(relativePath string) error {
	if relativePath == "." {
		return errors.New("storage root cannot be removed")
	}
	return root.withOpenRoot(func(openRoot *os.Root) error {
		return openRoot.RemoveAll(relativePath)
	})
}

func (root Root) Rename(oldRelativePath string, newRelativePath string) error {
	if oldRelativePath == "." || newRelativePath == "." {
		return errors.New("storage root cannot be renamed")
	}
	return root.withOpenRoot(func(openRoot *os.Root) error {
		if err := openRoot.MkdirAll(filepath.Dir(newRelativePath), 0o755); err != nil {
			return err
		}
		return openRoot.Rename(oldRelativePath, newRelativePath)
	})
}
