package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/matveynator/netchan/v2"
)

// These are the application's existing hosting/mail wire types. Both fixture
// modules keep the same package and type names while changing the library.
type sitebrushNetChanRequest struct {
	Payload  []byte
	Response chan<- sitebrushNetChanResponse
}

type sitebrushNetChanResponse struct {
	Status     string
	StatusCode int
	Payload    []byte
}

func run() error {
	privateKey := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	settings := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	encoded, err := x509.CreateCertificate(rand.Reader, settings, settings, privateKey.Public(), privateKey)
	if err != nil {
		return err
	}
	configuration := &tls.Config{MinVersion: tls.VersionTLS13}
	if os.Args[1] == "listen" {
		configuration.Certificates = []tls.Certificate{{Certificate: [][]byte{encoded}, PrivateKey: privateKey}}
		listener, err := netchan.Listen[sitebrushNetChanRequest]("127.0.0.1:0", netchan.Config{TLS: configuration})
		if err != nil {
			return err
		}
		defer listener.Close()
		fmt.Println(listener.Address)
		connection := <-listener.Channels
		for request := range connection.Receive {
			request.Response <- sitebrushNetChanResponse{Status: "stored", StatusCode: 200, Payload: request.Payload}
		}
		return connection.Close()
	}
	configuration.InsecureSkipVerify = true
	configuration.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) != 1 {
			return fmt.Errorf("missing fixture identity")
		}
		publicKey, ok := state.PeerCertificates[0].PublicKey.(ed25519.PublicKey)
		if !ok || !publicKey.Equal(privateKey.Public()) {
			return fmt.Errorf("substituted fixture identity")
		}
		return nil
	}
	connection, err := netchan.Dial[sitebrushNetChanRequest](os.Args[2], netchan.Config{TLS: configuration})
	if err != nil {
		return err
	}
	defer connection.Abort()
	replies := make(chan sitebrushNetChanResponse)
	for sequence := 0; sequence < 3; sequence++ {
		payload := []byte(fmt.Sprintf("signed-payload-%d", sequence))
		if err := connection.Deliver(sitebrushNetChanRequest{Payload: payload, Response: replies}); err != nil {
			return err
		}
		select {
		case response := <-replies:
			if response.Status != "stored" || response.StatusCode != 200 || string(response.Payload) != string(payload) {
				return fmt.Errorf("changed response: %+v", response)
			}
		case <-time.After(5 * time.Second):
			return fmt.Errorf("reply timed out")
		}
	}
	return connection.Close()
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
