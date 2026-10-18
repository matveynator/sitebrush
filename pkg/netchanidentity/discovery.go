// Package netchanidentity bootstraps central trust through the official HTTPS origin.
package netchanidentity

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

const URL = "https://sitebrush.com/?service_mail_relay&identity=ed25519"

type Document struct {
	Algorithm string `json:"algorithm"`
	PublicKey string `json:"public_key"`
}

// Fetch never follows redirects or inherits installation address overrides.
// Context is confined to cancellation of the standard HTTP client boundary.
func Fetch(stop <-chan struct{}) (ed25519.PublicKey, error) {
	return fetch(discoveryClient(), stop)
}

func discoveryClient() *http.Client {
	return &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func fetch(client *http.Client, stop <-chan struct{}) (ed25519.PublicKey, error) {
	select {
	case <-stop:
		return nil, errors.New("central identity discovery cancelled")
	default:
	}
	boundary, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-stop:
			cancel()
		case <-finished:
		}
	}()
	request, err := http.NewRequestWithContext(boundary, http.MethodGet, URL, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("central Ed25519 identity is unavailable over official HTTPS")
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1025))
	if err != nil {
		return nil, err
	}
	if len(payload) > 1024 {
		return nil, errors.New("central identity response exceeds its limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var document Document
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("invalid trailing central identity content")
	}
	key, err := base64.StdEncoding.DecodeString(document.PublicKey)
	if err != nil || document.Algorithm != "Ed25519" || len(key) != ed25519.PublicKeySize {
		return nil, errors.New("invalid central Ed25519 public identity")
	}
	return ed25519.PublicKey(key), nil
}
