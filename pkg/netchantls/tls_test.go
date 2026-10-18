package netchantls

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"net"
	"testing"
	"time"
)

func TestPinnedHandshakeAuthenticatesIdentityAndRejectsSubstitution(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPublicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverConfiguration, err := ServerConfig("central", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		name     string
		key      ed25519.PublicKey
		accepted bool
	}{
		{"trusted", publicKey, true},
		{"substituted", otherPublicKey, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			clientConfiguration, err := PinnedClientConfig(fixture.key)
			if err != nil {
				t.Fatal(err)
			}
			clientSocket, serverSocket := net.Pipe()
			defer clientSocket.Close()
			defer serverSocket.Close()
			_ = clientSocket.SetDeadline(time.Now().Add(3 * time.Second))
			_ = serverSocket.SetDeadline(time.Now().Add(3 * time.Second))
			finished := make(chan error, 1)
			go func() { finished <- tls.Server(serverSocket, serverConfiguration).Handshake() }()
			client := tls.Client(clientSocket, clientConfiguration)
			handshakeErr := client.Handshake()
			clientSocket.Close()
			<-finished
			if (handshakeErr == nil) != fixture.accepted {
				t.Fatalf("handshake error=%v, accepted=%v", handshakeErr, fixture.accepted)
			}
		})
	}
}

func TestIdentityConfigurationRejectsMissingKeysAndDoesNotAliasTrust(t *testing.T) {
	if _, err := ServerConfig("central", nil); err == nil {
		t.Fatal("missing server identity was accepted")
	}
	if _, err := PinnedClientConfig(nil); err == nil {
		t.Fatal("missing trusted identity was accepted")
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := PinnedClientConfig(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := Certificate("central", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(identity.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	publicKey[0] ^= 1
	if err := configuration.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}}); err != nil {
		t.Fatalf("caller mutation changed the trust anchor: %v", err)
	}
	for _, certificates := range [][]*x509.Certificate{nil, {certificate, certificate}} {
		if err := configuration.VerifyConnection(tls.ConnectionState{PeerCertificates: certificates}); err == nil {
			t.Fatal("invalid certificate count was accepted")
		}
	}
}
