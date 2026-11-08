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
// remainingQuota is -1 only when a site's quota is unavailable; otherwise
// every chunk is checked even when the remote server omits Content-Length.
func StreamImportResource(ctx context.Context, body io.Reader, declaredLength, remainingQuota int64, progress func(int64)) (result StreamedImportResource, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if body == nil {
		return result, errors.New("missing import body")
	}
	if remainingQuota >= 0 && declaredLength > remainingQuota {
		return result, fmt.Errorf("storage limit reached: imported resource exceeds site quota")
	}
	file, err := os.CreateTemp("", "sitebrush-import-*.part")
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
		if err := ctx.Err(); err != nil {
			return StreamedImportResource{}, err
		}
		n, readErr := body.Read(buf)
		if n > 0 {
			if remainingQuota >= 0 && int64(n) > remainingQuota-size {
				return StreamedImportResource{}, fmt.Errorf("storage limit reached: imported resource exceeds site quota")
			}
			if len(first) < cap(first) {
				take := n
				if take > cap(first)-len(first) {
					take = cap(first)-len(first)
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
	copy(result.SHA256[:], digest.Sum(nil))
	result.Path = file.Name()
	result.Size = size
	result.FirstBytes = first
	return result, nil
}
