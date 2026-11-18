package appupdate

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/matveynator/netchan/v2"
)

func TestContactFallbackRejectsIdentityProtocolAndTargetFailures(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		err       error
		transport bool
	}{
		{"closed", net.ErrClosed, true},
		{"root channel closed", netchan.ErrChannelClosed, true},
		{"EOF", io.EOF, true},
		{"deadline", os.ErrDeadlineExceeded, true},
		{"authority", x509.UnknownAuthorityError{Cert: &x509.Certificate{}}, false},
		{"TLS verification", &tls.CertificateVerificationError{Err: errors.New("fixture verification failure")}, false},
		{"TLS alert", errors.New("remote error: tls: bad certificate"), false},
		{"schema", netchan.ErrSchemaMismatch, false},
		{"malformed", netchan.ErrMalformedFrame, false},
		{"rejected", netchan.ErrSessionRejected, false},
		{"expired authenticated session", netchan.ErrSessionExpired, true},
		{"expired session with EOF", errors.Join(netchan.ErrSessionExpired, io.EOF), true},
		{"expired session with TLS rejection", errors.Join(netchan.ErrSessionExpired, errors.New("tls: access denied")), false},
		{"rejection with EOF", errors.Join(netchan.ErrSessionRejected, io.EOF), false},
		{"TLS with closed channel", errors.Join(netchan.ErrChannelClosed, errors.New("tls: access denied")), false},
		{"target denied", errors.New("forbidden"), false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			problem := (&contactDiagnostics{}).failure("fixture", scenario.err)
			if IsContactTransportFailure(problem) != scenario.transport {
				t.Fatalf("classification=%v want=%v error=%v", IsContactTransportFailure(problem), scenario.transport, problem)
			}
		})
	}
	if IsContactTransportFailure(errors.New("netchan: channel closed")) {
		t.Fatal("unclassified text granted fallback")
	}
}

func TestProfilerNetChanContactPreservesReplyAndFailureKinds(t *testing.T) {
	for _, scenario := range []string{"available", "target rejection", "malformed reply", "closed session", "wrong identity"} {
		t.Run(scenario, func(t *testing.T) {
			root := newFixtureInstallation(t)
			store, err := openStore(root)
			if err != nil {
				t.Fatal(err)
			}
			configuration, err := tlsIdentity(store, true)
			store.Close()
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "wrong identity" {
				other, openErr := openStore(t.TempDir())
				if openErr != nil {
					t.Fatal(openErr)
				}
				configuration, err = tlsIdentity(other, true)
				other.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			listener, err := netchan.Listen[wireRequest](Address(root), netchan.Config{TLS: configuration, Capacity: 4})
			if err != nil {
				t.Fatal(err)
			}
			stop := make(chan struct{})
			done := make(chan struct{})
			serverErrors := make(chan error, 1)
			go func() {
				defer close(done)
				select {
				case channel := <-listener.Channels:
					if channel == nil {
						return
					}
					defer channel.Abort()
					select {
					case request := <-channel.Receive:
						if request.Action != "public_profiler" {
							serverErrors <- errors.New("wrong action")
							return
						}
					case <-stop:
						return
					}
					if scenario == "closed session" {
						return
					}
					response, _ := encodeContactResult(Response{Detail: `{"role":"public","busy":false}`})
					if scenario == "target rejection" {
						response, _ = encodeContactResult(Response{Error: "fixture denied"})
					}
					if scenario == "malformed reply" {
						response.Result = []byte("invalid JSON")
					}
					if sendErr := channel.Deliver(response); sendErr != nil {
						serverErrors <- sendErr
					}
				case <-stop:
				}
			}()
			t.Cleanup(func() {
				close(stop)
				listener.Close()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("contact outlived shutdown")
				}
			})
			events := make(chan ContactEvent, 64)
			response, exchangeErr := Exchange(root, Request{Action: "public_profiler", Detail: `{"action":"status"}`}, stop, func(event ContactEvent) {
				select {
				case events <- event:
				default:
				}
			})
			if scenario == "available" {
				if exchangeErr != nil || response.Detail != `{"role":"public","busy":false}` {
					t.Fatalf("reply=%+v error=%v", response, exchangeErr)
				}
			} else if exchangeErr == nil {
				t.Fatal("failed contact was accepted")
			}
			if IsContactTransportFailure(exchangeErr) != (scenario == "closed session") {
				t.Fatalf("unexpected fallback eligibility for %s: %v", scenario, exchangeErr)
			}
			if len(events) == 0 {
				t.Fatal("missing transport lifecycle diagnostics")
			}
			select {
			case serverErr := <-serverErrors:
				t.Fatal(serverErr)
			default:
			}
		})
	}
}

func TestProfilerNetChanCancellationBeforeDial(t *testing.T) {
	stop := make(chan struct{})
	close(stop)
	_, err := Exchange(t.TempDir(), Request{Action: "public_profiler"}, stop)
	if err == nil || IsContactTransportFailure(err) {
		t.Fatalf("cancellation selected fallback: %v", err)
	}
}
