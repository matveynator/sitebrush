//go:build windows

package serviceinstall

import (
	"os"
	"path/filepath"
)

func prepareServiceIdentity(plan installPlan) error { return nil }

func prepareServiceStorage(plan installPlan) error {
	storagePath := installOptionOrDefault(plan.Options.StoragePath, defaultStoragePath())
	return os.MkdirAll(filepath.Join(storagePath, "storage", "db"), 0o700)
}
