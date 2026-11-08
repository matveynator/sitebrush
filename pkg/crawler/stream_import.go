package crawler

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
)

// StreamedImportResource is an on-disk, content-addressable resource body.
// The caller must remove Path after persistence or cancellation.
type StreamedImportResource struct {
	Path       string
	Size       int64
	SHA256     [32]byte
	FirstBytes []byte
}

// StreamImportResource keeps large imported binary files off the Go heap.
// maximumBytes bounds temporary disk use even when the server omits Content-Length.
// The caller owns the private staging directory and the returned file.
func StreamImportResource(done <-chan struct{}, body io.Reader, temporaryDirectory string, declaredLength, maximumBytes int64, progress func(int64)) (result StreamedImportResource, err error) {
	if body == nil {
		return result, errors.New("missing import body")
	}
	if temporaryDirectory == "" {
		return result, errors.New("missing import staging directory")
	}
	if maximumBytes >= 0 && declaredLength > maximumBytes {
		return result, fmt.Errorf("storage limit reached: imported resource exceeds site quota")
	}
	file, err := os.CreateTemp(temporaryDirectory, "sitebrush-import-*.part")
	if err != nil {
		return result, err
	}
	defer func() {
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(file.Name())
		}
	}()
	digest := sha256.New()
	buf := make([]byte, 128*1024)
	first := make([]byte, 0, 512)
	var size int64
	for {
		select {
		case <-done:
			return StreamedImportResource{}, context.Canceled
		default:
		}
		n, readErr := body.Read(buf)
		if n > 0 {
			if maximumBytes >= 0 && int64(n) > maximumBytes-size {
				return StreamedImportResource{}, fmt.Errorf("storage limit reached: imported resource exceeds site quota")
			}
			if len(first) < cap(first) {
				take := n
				if take > cap(first)-len(first) {
					take = cap(first) - len(first)
				}
				first = append(first, buf[:take]...)
			}
			if _, err := file.Write(buf[:n]); err != nil {
				return StreamedImportResource{}, err
			}
			if _, err := digest.Write(buf[:n]); err != nil {
				return StreamedImportResource{}, err
			}
			size += int64(n)
			if progress != nil {
				progress(size)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return StreamedImportResource{}, readErr
		}
	}
	if declaredLength >= 0 && size != declaredLength {
		return StreamedImportResource{}, fmt.Errorf("imported resource size mismatch: got %d, expected %d", size, declaredLength)
	}
	if err := file.Sync(); err != nil {
		return StreamedImportResource{}, err
	}
	if err := file.Chmod(0o644); err != nil {
		return StreamedImportResource{}, err
	}
	copy(result.SHA256[:], digest.Sum(nil))
	result.Path = file.Name()
	result.Size = size
	result.FirstBytes = first
	return result, nil
}
