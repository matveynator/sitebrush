// Package netchantransport prefers authenticated QUIC and retains TCP compatibility.
package netchantransport

import (
	"errors"
	"fmt"
	"log"
	"net"
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
	drivers := append([]transport.Driver(nil), configuration.Transports...)
	configuration.Transports = append(drivers, priorityDriver{stop: stop})
	return configuration
}

type priorityDriver struct {
	stop <-chan struct{}
}

func (priorityDriver) Scheme() string { return "tcp" }

func (driver priorityDriver) Dial(endpoint transport.Endpoint, options transport.Options) (net.Conn, error) {
	quicEndpoint := endpoint
	quicEndpoint.Scheme, quicEndpoint.Legacy = "quic", false
	connection, quicErr := dialTransport(quic.Driver{}, quicEndpoint, options, driver.stop, quicDialTimeout)
	if quicErr == nil {
		return connection, nil
	}
	connection, tcpErr := dialTransport(tcp.Driver{}, endpoint, options, driver.stop, tcpDialTimeout)
	if tcpErr != nil {
		return nil, errors.Join(fmt.Errorf("QUIC: %w", quicErr), fmt.Errorf("TCP/TLS: %w", tcpErr))
	}
	return connection, nil
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
