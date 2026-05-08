//go:build !darwin || !desktop || !cgo

package desktop

import "fmt"

// ChooseSaveFilePath is unsupported outside desktop macOS CGO builds.
func ChooseSaveFilePath(defaultName string) (string, error) {
	_ = defaultName
	return "", fmt.Errorf("desktop native save dialog is supported only on macOS desktop builds with CGO")
}

// SaveFile is unsupported outside desktop macOS CGO builds.
func SaveFile(defaultName string, contents []byte) (string, error) {
	_ = defaultName
	_ = contents
	return "", fmt.Errorf("desktop native save dialog is supported only on macOS desktop builds with CGO")
}
