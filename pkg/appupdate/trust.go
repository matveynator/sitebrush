package appupdate

import (
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

// The public trust anchor and data-format contract are release inputs, never
// runtime settings writable through the administration panel.
//
//go:embed release-public-key.txt
var releasePublicKey string

//go:embed compatibility.json
var compatibilityJSON []byte

func TrustedKey() (ed25519.PublicKey, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(releasePublicKey))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, errors.New("official release signing key is not configured")
	}
	return ed25519.PublicKey(key), nil
}

func CurrentCompatibility() Compatibility {
	var compatibility Compatibility
	if json.Unmarshal(compatibilityJSON, &compatibility) != nil {
		return Compatibility{}
	}
	return compatibility
}
