//go:build !windows

package winservice

import "context"

func RunIfNeeded(name string, run func(context.Context) error) (bool, error) {
	return false, nil
}
