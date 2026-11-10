//go:build windows

package servicepriv

import (
	"errors"
	"net"
)

func Launch([]string, string, int, bool) error {
	return errors.New("privileged socket handoff is not used on Windows")
}
func DropForMaintenance([]string, string) (bool, error) { return false, nil }
func InheritedListeners(int, bool) (net.Listener, net.Listener, error) {
	return nil, nil, errors.New("inherited listeners are not used on Windows")
}
