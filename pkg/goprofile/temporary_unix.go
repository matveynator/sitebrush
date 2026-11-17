//go:build !windows

package goprofile

import "os"

// Unlinked files survive only while their owner holds the descriptor, including after a crash.
func temporaryProfile(directory string) (*os.File, error) {
	file, err := os.CreateTemp(directory, "profile-")
	if err != nil {
		return nil, err
	}
	if err = os.Remove(file.Name()); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}
