package goprofile

import (
	"golang.org/x/sys/windows"
	"os"
)

func temporaryProfile(directory string) (*os.File, error) {
	initial, err := os.CreateTemp(directory, "profile-")
	if err != nil {
		return nil, err
	}
	name := initial.Name()
	initial.Close()
	if err = os.Remove(name); err != nil {
		return nil, err
	}
	encoded, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(encoded, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_TEMPORARY|windows.FILE_FLAG_DELETE_ON_CLOSE, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), name), nil
}
