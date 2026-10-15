//go:build !windows

package appupdate

import "os"

func syncDirectoryHandle(directory *os.File) error { return directory.Sync() }
