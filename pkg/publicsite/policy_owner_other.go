//go:build !openbsd

package publicsite

import "os"

// Other platforms retain their existing publication ownership behavior.
func setPolicyFileOwner(_ *os.File, _ *os.Root) error {
	return nil
}
