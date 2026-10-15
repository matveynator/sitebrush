//go:build windows

package appupdate

import "os"

// Windows file FlushFileBuffers and atomic replacement provide the durability
// boundary; directory handles opened by os.Open do not support File.Sync.
func syncDirectoryHandle(directory *os.File) error { return nil }
