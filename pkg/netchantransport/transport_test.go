package netchantransport

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"testing"
	"time"

	"github.com/matveynator/netchan/v2/pkg/transport"
	"github.com/matveynator/netchan/v2/pkg/transport/tcp"
	"github.com/matveynator/sitebrush/v2/pkg/netchantls"
)

func TestQUICPriorityTCPCompatibilityAndPinnedIdentity(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrongKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, err := netchantls.ServerConfig("central", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		name       string
		dual       bool
		trustedKey ed25519.PublicKey
		network    string
	}{
		{"QUIC-preferred", true, publicKey, "udp"},
		{"TCP-only-peer", false, publicKey, "tcp"},
		{"wrong-pin-on-both-transports", true, wrongKey, ""},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			stop := make(chan struct{})
			defer close(stop)
			var serverDriver transport.Driver = tcp.Driver{}
			if fixture.dual {
				serverDriver = priorityDriver{stop: stop}
			}
			endpoint, err := transport.Parse("127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			listener, err := serverDriver.Listen(endpoint, transport.Options{TLS: serverTLS, Done: stop, OwnerDone: stop})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			releaseConnection := make(chan struct{})
			defer close(releaseConnection)
			serverFinished := make(chan error, 1)
			go func() {
				connection, err := listener.Accept()
				if err != nil {
					serverFinished <- err
					return
				}
				defer connection.Close()
				_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
				_, err = io.CopyN(connection, connection, 1)
				serverFinished <- err
				// QUIC writes enqueue stream bytes. Retain the connection until the
				// consumer has actually read the response, as NetChan does with ACKs.
				<-releaseConnection
			}()
			clientTLS, err := netchantls.PinnedClientConfig(fixture.trustedKey)
			if err != nil {
				t.Fatal(err)
			}
			bound, err := transport.Parse(listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			reports := make(chan string, 1)
			connection, err := (priorityDriver{stop: stop, reports: reports}).Dial(bound, transport.Options{TLS: clientTLS, Done: stop, OwnerDone: stop})
			if fixture.network == "" {
				if err == nil {
					connection.Close()
					t.Fatal("substituted identity accepted")
				}
				select {
				case <-reports:
					t.Fatal("failed TLS identity reported as an established transport")
				default:
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantProtocol := "tcp+tls"
			if fixture.network == "udp" {
				wantProtocol = "quic"
			}
			select {
			case protocol := <-reports:
				if protocol != wantProtocol {
					t.Fatalf("reported %s, want %s", protocol, wantProtocol)
				}
			default:
				t.Fatal("selected transport was not reported")
			}
			defer connection.Close()
			if network := connection.RemoteAddr().Network(); network != fixture.network {
				t.Fatalf("selected %s, want %s", network, fixture.network)
			}
			_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
			if _, err := connection.Write([]byte{42}); err != nil {
				t.Fatal("selected connection was cancelled after dialing", err)
			}
			reply := make([]byte, 1)
			if _, err := io.ReadFull(connection, reply); err != nil || reply[0] != 42 {
				t.Fatal("authenticated stream failed", reply, err)
			}
			select {
			case err := <-serverFinished:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("server did not finish")
			}
		})
	}
}

type delayedDriver struct {
	started chan<- struct{}
	ended   chan<- struct{}
}

func (delayedDriver) Scheme() string { return "test" }
func (delayedDriver) Listen(transport.Endpoint, transport.Options) (net.Listener, error) {
	return nil, net.ErrClosed
}
func (driver delayedDriver) Dial(_ transport.Endpoint, options transport.Options) (net.Conn, error) {
	driver.started <- struct{}{}
	<-options.Done
	driver.ended <- struct{}{}
	return nil, net.ErrClosed
}

func TestDialTimeoutRevokesAttemptAndStopsCancelledConsumers(t *testing.T) {
	started := make(chan struct{}, 1)
	ended := make(chan struct{}, 1)
	driver := delayedDriver{started: started, ended: ended}
	if connection, err := dialTransport(driver, transport.Endpoint{}, transport.Options{}, nil, 10*time.Millisecond); err == nil || connection != nil {
		t.Fatal("stalled attempt did not time out")
	}
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("timed out driver retained its dial")
	}
	<-started
	stop := make(chan struct{})
	close(stop)
	if _, err := dialTransport(driver, transport.Endpoint{}, transport.Options{}, stop, time.Second); err == nil {
		t.Fatal("cancelled consumer started a connection")
	}
	select {
	case <-started:
		t.Fatal("cancelled consumer invoked the driver")
	default:
	}
}

func TestListenerCloseReleasesBothPorts(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := netchantls.ServerConfig("central", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := transport.Parse("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := (priorityDriver{}).Listen(endpoint, transport.Options{TLS: configuration})
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal("repeated close failed", err)
	}
	tcpListener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("TCP port retained after Close", err)
	}
	defer tcpListener.Close()
	udpListener, err := net.ListenPacket("udp", address)
	if err != nil {
		t.Fatal("UDP port retained after Close", err)
	}
	defer udpListener.Close()
}
