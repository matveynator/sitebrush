package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/matveynator/netchan/v2"
	"github.com/matveynator/sitebrush/v2/pkg/mailout"
	"github.com/matveynator/sitebrush/v2/pkg/netchantls"
	"github.com/matveynator/sitebrush/v2/pkg/netchantransport"
)

func TestMailReceiverJournalsOwnTCPAndQUICProtocols(t *testing.T) {
	for _, protocol := range []string{"tcp+tls", "quic"} {
		t.Run(protocol, func(t *testing.T) {
			app, _ := newTestApplication(t)
			enableMailMonitorForTest(t, app)
			app.sendEmail = func(context.Context, mailout.Message) error { return nil }
			request := signedServiceMailRequestForTest(t, app, serviceMailRequest{Version: 2, MessageID: "receiver-test", Subject: "Account verification", Body: "123456", ExpiresAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano), SourceDomain: "customer.example", Recipient: "owner@example.net", CodeKind: "password_change_code", SecretValue: "123456", LanguageCode: "en", CreatedAt: time.Now().UTC().Format(time.RFC3339)})
			registerServiceMailInstallationForTest(t, app, request)
			public, private, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			serverTLS, err := netchantls.ServerConfig("central", private)
			if err != nil {
				t.Fatal(err)
			}
			scheme, network := "tcp", "tcp"
			if protocol == "quic" {
				scheme, network = "quic", "udp"
			}
			listener, err := netchan.Listen[sitebrushNetChanRequest](scheme+"://127.0.0.1:0", netchantransport.ReceiverConfig(netchan.Config{TLS: serverTLS}, protocol))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			serverDone := make(chan struct{})
			receiverContext := context.WithValue(ctx, mailReceiptContextKey{}, mailReceiptTransport{"netchan", protocol, network})
			go func() {
				defer close(serverDone)
				app.serveHostingSnapshotNetChanListener(ctx.Done(), receiverContext, listener)
			}()
			defer func() { cancel(); <-serverDone }()
			clientTLS, err := netchantls.PinnedClientConfig(public)
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Address
			if !strings.Contains(address, "://") {
				address = scheme + "://" + address
			}
			connection, err := netchan.Dial[sitebrushNetChanRequest](address, netchantransport.ReceiverConfig(netchan.Config{TLS: clientTLS}, protocol))
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Abort()
			send := func(request serviceMailRequest) sitebrushNetChanResponse {
				t.Helper()
				payload, err := json.Marshal(request)
				if err != nil {
					t.Fatal(err)
				}
				replies := make(chan sitebrushNetChanResponse, 1)
				delivered := make(chan error, 1)
				go func() { delivered <- connection.Deliver(sitebrushNetChanRequest{Payload: payload, Response: replies}) }()
				select {
				case err := <-delivered:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("delivery timed out")
				}
				select {
				case response := <-replies:
					return response
				case <-ctx.Done():
					t.Fatal("response timed out")
				}
				return sitebrushNetChanResponse{}
			}
			response := send(request)
			if response.StatusCode != http.StatusOK || netChanReceiverProtocol(response.Payload) != protocol {
				t.Fatalf("wrong receiver response: %+v", response)
			}
			request.Signature = "invalid"
			if response := send(request); response.StatusCode != http.StatusForbidden {
				t.Fatalf("invalid signature accepted: %+v", response)
			}
			events := mailMonitorEventsForTest(t, app, "local")
			if len(events) != 1 || events[0].Stage != "transport_received" || !strings.Contains(events[0].Detail, "protocol="+protocol+" network="+network) {
				t.Fatalf("receipt journal: %+v", events)
			}
			if strings.Contains(events[0].Detail, "123456") {
				t.Fatal("receipt exposed secret")
			}
		})
	}
}

func TestMailReceiverProtocolRejectsUntrustedMetadata(t *testing.T) {
	// The HTTP adapter is also used by synchronization: observation is opt-in so
	// syncing a journal cannot recursively generate another unsynced event.
	for _, raw := range []string{`{"receiver_protocol":"quic"}`, `{"receiver_protocol":"ble"}`} {
		if netChanReceiverProtocol([]byte(raw)) == "unknown" {
			t.Fatal("known receiver protocol lost")
		}
	}
	if netChanReceiverProtocol([]byte(`{"receiver_protocol":"tcp password=secret"}`)) != "unknown" {
		t.Fatal("untrusted receiver protocol accepted")
	}
}
