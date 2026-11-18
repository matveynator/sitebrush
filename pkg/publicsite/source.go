package publicsite

import (
	"errors"
	"os"
	"syscall"
)

// Imported routes may traverse a component that is a regular file (for example
// index.php/section). Such a route has no materialized source in this jail.
// Permission, confinement and storage failures remain distinct errors.
func MissingSource(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}
