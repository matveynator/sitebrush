// Package netchantls authenticates network channels with installation identities.
package netchantls

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"time"
)

// Certificate keeps TLS possession proofs tied to the existing signing identity.
// No separate CA, certificate store, or mutable identity registry is needed.
func Certificate(name string, privateKey ed25519.PrivateKey) (tls.Certificate, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return tls.Certificate{}, errors.New("invalid network channel private key")
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	settings := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: name},
		NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(10, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	encoded, err := x509.CreateCertificate(rand.Reader, settings, settings, privateKey.Public(), privateKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{encoded}, PrivateKey: privateKey}, nil
}

func ServerConfig(name string, privateKey ed25519.PrivateKey) (*tls.Config, error) {
	identity, err := Certificate(name, privateKey)
	if err != nil {
		return nil, err
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{identity}}, nil
}

// PinnedClientConfig verifies an application identity rather than a CA hostname.
// TLS still verifies possession of the certificate's private key during handshake.
func PinnedClientConfig(publicKey ed25519.PublicKey) (*tls.Config, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("invalid network channel public key")
	}
	trusted := append(ed25519.PublicKey(nil), publicKey...)
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, // #nosec G402 -- VerifyConnection authenticates the pinned signing identity.
		VerifyConnection: func(connection tls.ConnectionState) error {
			if len(connection.PeerCertificates) != 1 {
				return errors.New("missing network channel certificate")
			}
			actual, ok := connection.PeerCertificates[0].PublicKey.(ed25519.PublicKey)
			if !ok || !actual.Equal(trusted) {
				return errors.New("network channel identity mismatch")
			}
			return nil
		},
	}, nil
}
