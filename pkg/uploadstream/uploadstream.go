// Package uploadstream keeps multipart transfers bounded by idle
// time without imposing a total duration limit on large files.
package uploadstream

import (
	"io"
	"mime"
	"net/http"
	"time"
)

const IdleTimeout = 2 * time.Minute

type idleReadCloser struct {
	io.ReadCloser
	controller *http.ResponseController
}

func (reader idleReadCloser) Read(buffer []byte) (int, error) {
	if err := reader.controller.SetReadDeadline(time.Now().Add(IdleTimeout)); err != nil {
		return 0, err
	}
	return reader.ReadCloser.Read(buffer)
}

// BoundIdle replaces a server-wide total read deadline with a rolling body
// deadline. Callers keep their own authorization and byte limits.
func BoundIdle(w http.ResponseWriter, r *http.Request) error {
	controller := http.NewResponseController(w)
	if err := controller.SetReadDeadline(time.Now().Add(IdleTimeout)); err != nil {
		return err
	}
	r.Body = idleReadCloser{ReadCloser: r.Body, controller: controller}
	return nil
}

func ClearReadDeadline(w http.ResponseWriter) error {
	return http.NewResponseController(w).SetReadDeadline(time.Time{})
}

// IsFileUploadRequest is intentionally limited to the public file manager
// route; the backend remains responsible for authorization and quota checks.
func IsFileUploadRequest(r *http.Request) bool {
	if r.Method != http.MethodPost || !r.URL.Query().Has("files") {
		return false
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && contentType == "multipart/form-data"
}
