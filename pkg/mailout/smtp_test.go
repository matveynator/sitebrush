package mailout

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSMTPCompatibility(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	for _, scenario := range []struct {
		name                                  string
		advertise, rejectTLS, rejectRecipient bool
		version                               uint16
		attempts                              int
	}{
		{name: "no TLS", attempts: 1},
		{name: "broken STARTTLS", advertise: true, rejectTLS: true, attempts: 3},
		{name: "untrusted certificate", advertise: true, version: tls.VersionTLS12, attempts: 2},
		{name: "legacy TLS", advertise: true, version: tls.VersionTLS10, attempts: 2},
		{name: "recipient rejected without retry", rejectRecipient: true, attempts: 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			connections := make(chan net.Conn)
			sender := pipeSMTPSender(connections)
			result := make(chan int, 1)
			go func() {
				messages := 0
				defer func() { result <- messages }()
				for attempt := 0; attempt < scenario.attempts; attempt++ {
					connection, available := <-connections
					if !available {
						return
					}
					func() {
						defer connection.Close()
						_ = connection.SetDeadline(time.Now().Add(4 * time.Second))
						reader := bufio.NewReader(connection)
						fmt.Fprint(connection, "220 test SMTP\r\n")
						encrypted := false
						for {
							command, readErr := reader.ReadString('\n')
							if readErr != nil {
								return
							}
							switch {
							case strings.HasPrefix(command, "EHLO"):
								if scenario.advertise && !encrypted {
									fmt.Fprint(connection, "250-test\r\n250 STARTTLS\r\n")
								} else {
									fmt.Fprint(connection, "250 test\r\n")
								}
							case strings.HasPrefix(command, "STARTTLS"):
								if scenario.rejectTLS {
									fmt.Fprint(connection, "454 TLS unavailable\r\n")
									continue
								}
								fmt.Fprint(connection, "220 ready\r\n")
								secured := tls.Server(connection, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: scenario.version, MaxVersion: scenario.version, CipherSuites: []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256, tls.TLS_RSA_WITH_AES_128_CBC_SHA}})
								if handshakeErr := secured.Handshake(); handshakeErr != nil {
									return
								}
								connection = secured
								reader = bufio.NewReader(connection)
								encrypted = true
							case strings.HasPrefix(command, "MAIL"):
								fmt.Fprint(connection, "250 sender accepted\r\n")
							case strings.HasPrefix(command, "RCPT"):
								if scenario.rejectRecipient {
									fmt.Fprint(connection, "550 unknown recipient\r\n")
								} else {
									fmt.Fprint(connection, "250 recipient accepted\r\n")
								}
							case strings.HasPrefix(command, "DATA"):
								fmt.Fprint(connection, "354 send message\r\n")
								for {
									line, bodyErr := reader.ReadString('\n')
									if bodyErr != nil {
										return
									}
									if line == ".\r\n" {
										break
									}
								}
								messages++
								fmt.Fprint(connection, "250 queued\r\n")
							case strings.HasPrefix(command, "QUIT"):
								fmt.Fprint(connection, "221 bye\r\n")
								return
							default:
								return
							}
						}
					}()
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := sender.sendToEndpoint(ctx, "localhost", "localhost:25", "sender@example.org", "recipient@example.net", []byte("Subject: test\r\n\r\ncode\r\n"))
			close(connections)
			if (err != nil) != scenario.rejectRecipient {
				t.Fatalf("delivery: %v", err)
			}
			count := <-result
			if (!scenario.rejectRecipient && count != 1) || (scenario.rejectRecipient && count != 0) {
				t.Fatalf("messages delivered: %d", count)
			}
		})
	}
}

func TestSMTPCancellationClosesConnection(t *testing.T) {
	connections := make(chan net.Conn)
	sender := pipeSMTPSender(connections)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		connection, available := <-connections
		if !available {
			return
		}
		defer connection.Close()
		cancel()
		_, _ = io.Copy(io.Discard, connection)
	}()
	started := time.Now()
	err := sender.sendToEndpoint(ctx, "localhost", "localhost:25", "sender@example.org", "recipient@example.net", nil)
	if err == nil || time.Since(started) > time.Second {
		t.Fatalf("cancellation did not stop SMTP promptly: %v", err)
	}
	<-closed
}

func pipeSMTPSender(connections chan<- net.Conn) DirectSender {
	return DirectSender{DialTimeout: time.Second, dialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		client, server := net.Pipe()
		select {
		case connections <- server:
			return client, nil
		case <-ctx.Done():
			client.Close()
			server.Close()
			return nil, ctx.Err()
		}
	}}
}
