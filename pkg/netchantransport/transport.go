// Package netchantransport prefers authenticated QUIC and retains TCP compatibility.
package netchantransport

import (
	"errors"
	"fmt"
	"log"
	"net"
	"runtime/debug"
	"time"

	"github.com/matveynator/netchan/v2"
	"github.com/matveynator/netchan/v2/pkg/transport"
	"github.com/matveynator/netchan/v2/pkg/transport/quic"
	"github.com/matveynator/netchan/v2/pkg/transport/tcp"
	"github.com/matveynator/sitebrush/v2/pkg/diagnosticlog"
)

const quicDialTimeout = 750 * time.Millisecond
const tcpDialTimeout = 3 * time.Second

// Configure applies priority to bare host:port and tcp:// addresses. Explicit
// alternative schemes retain their NetChan meaning. No application request is
// submitted until one transport has completed its authenticated handshake.
func Configure(configuration netchan.Config, stop <-chan struct{}) netchan.Config {
	return ConfigureWithReports(configuration, stop, nil)
}

// Each consumer owns its report channel. A successful authenticated physical
// connection reports the driver actually selected, including reconnects.
func ConfigureWithReports(configuration netchan.Config, stop <-chan struct{}, reports chan<- string) netchan.Config {
	return configureReports(configuration, stop, reports, nil)
}

type ConnectionReport struct {
	Protocol string
	Port     int
}

// The port comes from the connected socket, never the configured endpoint.
func ConfigureWithConnectionReports(configuration netchan.Config, stop <-chan struct{}, reports chan<- ConnectionReport) netchan.Config {
	return configureReports(configuration, stop, nil, reports)
}

func configureReports(configuration netchan.Config, stop <-chan struct{}, reports chan<- string, connections chan<- ConnectionReport) netchan.Config {
	drivers := append([]transport.Driver(nil), configuration.Transports...)
	configuration.Transports = append(drivers, priorityDriver{stop: stop, reports: reports, connections: connections}, reportedDriver{Driver: quic.Driver{}, stop: stop, reports: reports, connections: connections})
	return configuration
}

func LibraryVersion() string {
	build, ok := debug.ReadBuildInfo()
	if ok {
		for _, dependency := range build.Deps {
			if dependency.Path == "github.com/matveynator/netchan/v2" {
				if dependency.Replace != nil {
					if dependency.Replace.Version != "" {
						return dependency.Replace.Version
					}
					return "local-replace"
				}
				return dependency.Version
			}
		}
	}
	return "unknown"
}

type priorityDriver struct {
	stop        <-chan struct{}
	reports     chan<- string
	connections chan<- ConnectionReport
}

func (priorityDriver) Scheme() string { return "tcp" }

func (driver priorityDriver) Dial(endpoint transport.Endpoint, options transport.Options) (net.Conn, error) {
	quicEndpoint := endpoint
	quicEndpoint.Scheme, quicEndpoint.Legacy = "quic", false
	connection, quicErr := dialTransport(quic.Driver{}, quicEndpoint, options, driver.stop, quicDialTimeout)
	if quicErr == nil {
		return reportConnection(connection, "quic", driver.reports, driver.stop, options, driver.connections)
	}
	connection, tcpErr := dialTransport(tcp.Driver{}, endpoint, options, driver.stop, tcpDialTimeout)
	if tcpErr != nil {
		return nil, errors.Join(fmt.Errorf("QUIC: %w", quicErr), fmt.Errorf("TCP/TLS: %w", tcpErr))
	}
	return reportConnection(connection, "tcp+tls", driver.reports, driver.stop, options, driver.connections)
}

type reportedDriver struct {
	transport.Driver
	stop        <-chan struct{}
	reports     chan<- string
	connections chan<- ConnectionReport
}

func (driver reportedDriver) Dial(endpoint transport.Endpoint, options transport.Options) (net.Conn, error) {
	connection, err := driver.Driver.Dial(endpoint, options)
	if err != nil {
		return nil, err
	}
	return reportConnection(connection, driver.Scheme(), driver.reports, driver.stop, options, driver.connections)
}

func reportConnection(connection net.Conn, protocol string, reports chan<- string, stop <-chan struct{}, options transport.Options, consumers ...chan<- ConnectionReport) (net.Conn, error) {
	report := ConnectionReport{Protocol: protocol}
	switch address := connection.RemoteAddr().(type) {
	case *net.TCPAddr:
		report.Port = address.Port
	case *net.UDPAddr:
		report.Port = address.Port
	}
	for _, consumer := range consumers {
		if consumer != nil {
			select {
			case consumer <- report:
			case <-stop:
			case <-options.OwnerDone:
			default:
				// Optional observations cannot delay a physical connection.
			}
		}
	}
	if reports == nil {
		return connection, nil
	}
	select {
	case reports <- protocol:
		return connection, nil
	case <-stop:
	case <-options.OwnerDone:
	}
	_ = connection.Close()
	return nil, net.ErrClosed
}

type dialResult struct {
	connection net.Conn
	err        error
}

// A revoked attempt owns no connection. Late successes are closed by the worker,
// and closing Done interrupts the external driver's dial and TLS handshake.
func dialTransport(driver transport.Driver, endpoint transport.Endpoint, options transport.Options, stop <-chan struct{}, timeout time.Duration) (net.Conn, error) {
	select {
	case <-stop:
		return nil, net.ErrClosed
	case <-options.Done:
		return nil, net.ErrClosed
	case <-options.OwnerDone:
		return nil, net.ErrClosed
	default:
	}
	originalDone := options.Done
	abandoned := make(chan struct{})
	// Built-in drivers promote an authenticated connection to OwnerDone before
	// returning; this channel then revokes only unfinished handshake work.
	defer close(abandoned)
	options.Done = abandoned
	replies := make(chan dialResult)
	go func() {
		connection, err := driver.Dial(endpoint, options)
		select {
		case replies <- dialResult{connection: connection, err: err}:
		case <-abandoned:
			if connection != nil {
				_ = connection.Close()
			}
		}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case response := <-replies:
		return response.connection, response.err
	case <-stop:
		return nil, net.ErrClosed
	case <-originalDone:
		return nil, net.ErrClosed
	case <-options.OwnerDone:
		return nil, net.ErrClosed
	case <-timer.C:
		return nil, fmt.Errorf("%s connection timed out", driver.Scheme())
	}
}

func (driver priorityDriver) Listen(endpoint transport.Endpoint, options transport.Options) (net.Listener, error) {
	tcpListener, err := (tcp.Driver{}).Listen(endpoint, options)
	if err != nil {
		return nil, err
	}
	bound, err := transport.Parse(tcpListener.Addr().String())
	if err != nil {
		_ = tcpListener.Close()
		return nil, err
	}
	bound.Scheme, bound.Legacy = "quic", false
	quicListener, err := listenQUIC(bound, options)
	if err != nil {
		log.Printf("NetChan QUIC listener unavailable; TCP/TLS remains active: %s", diagnosticlog.SafeLogValue(err.Error()))
		return tcpListener, nil
	}
	accepted := make(chan dialResult)
	closeRequests := make(chan struct{}, 1)
	stopping := make(chan struct{})
	done := make(chan struct{})
	listener := &priorityListener{address: tcpListener.Addr(), accepted: accepted, closeRequests: closeRequests, done: done}
	go func() {
		// The NetChan owner drains logical sessions before closing its listener.
		// Consumer cancellation must not tear down those sessions prematurely.
		<-closeRequests
		close(stopping)
		_ = tcpListener.Close()
		_ = quicListener.Close()
		close(done)
	}()
	go acceptConnections(tcpListener, accepted, closeRequests, stopping)
	go acceptConnections(quicListener, accepted, closeRequests, stopping)
	return listener, nil
}

type priorityListener struct {
	address       net.Addr
	accepted      <-chan dialResult
	closeRequests chan<- struct{}
	done          <-chan struct{}
}

func (listener *priorityListener) Addr() net.Addr { return listener.address }

func (listener *priorityListener) Accept() (net.Conn, error) {
	select {
	case response := <-listener.accepted:
		return response.connection, response.err
	case <-listener.done:
		return nil, net.ErrClosed
	}
}

func (listener *priorityListener) Close() error {
	select {
	case listener.closeRequests <- struct{}{}:
	case <-listener.done:
	}
	<-listener.done
	return nil
}

func acceptConnections(listener net.Listener, accepted chan<- dialResult, closeRequests chan<- struct{}, done <-chan struct{}) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			select {
			case <-done:
				return
			default:
			}
			log.Printf("NetChan listener ended: %s", diagnosticlog.SafeLogValue(err.Error()))
			select {
			case closeRequests <- struct{}{}:
			case <-done:
			}
			return
		}
		select {
		case accepted <- dialResult{connection: connection}:
		case <-done:
			_ = connection.Close()
			return
		}
	}
}
