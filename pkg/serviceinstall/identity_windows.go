//go:build windows

package serviceinstall

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func prepareServiceIdentity(plan installPlan) error { return nil }

func prepareServiceStorage(plan installPlan) error {
	storagePath := installOptionOrDefault(plan.Options.StoragePath, defaultStoragePath())
	relativeWorkingDirectory, err := filepath.Rel(storagePath, plan.WorkingDir)
	if err != nil || relativeWorkingDirectory == ".." || strings.HasPrefix(relativeWorkingDirectory, ".."+string(filepath.Separator)) || filepath.IsAbs(relativeWorkingDirectory) {
		return fmt.Errorf("working folder must be inside storage folder")
	}
	if err := os.MkdirAll(storagePath, 0o700); err != nil {
		return err
	}
	storageRoot, err := os.OpenRoot(storagePath)
	if err != nil {
		return err
	}
	defer storageRoot.Close()
	if err := storageRoot.MkdirAll(filepath.Join("storage", "db"), 0o700); err != nil {
		return err
	}
	if err := storageRoot.MkdirAll(filepath.Join("storage", "logs"), 0o700); err != nil {
		return err
	}
	if relativeWorkingDirectory != "." {
		return storageRoot.MkdirAll(relativeWorkingDirectory, 0o700)
	}
	return nil
}
