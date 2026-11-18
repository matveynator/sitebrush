package appupdate

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"os"
	"strings"

	"github.com/matveynator/netchan/v2"
	"github.com/matveynator/netchan/v2/pkg/transport"
	"github.com/matveynator/netchan/v2/pkg/transport/tcp"
)

type ContactEvent struct {
	Stage   string
	Address string
	Error   error
}

// Only failures of an authenticated contact's transport may select another
// contact. Identity, protocol and target errors never grant that capability.
type ContactTransportError struct {
	Stage string
	Cause error
}

func (problem *ContactTransportError) Error() string { return problem.Cause.Error() }
func (problem *ContactTransportError) Unwrap() error { return problem.Cause }

func IsContactTransportFailure(problem error) bool {
	var failure *ContactTransportError
	return errors.As(problem, &failure) && failure != nil && recoverableContactFailure(failure.Cause)
}

func recoverableContactFailure(problem error) bool {
	if problem == nil {
		return false
	}
	var verification *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var certificate x509.CertificateInvalidError
	var record tls.RecordHeaderError
	if errors.As(problem, &verification) || errors.As(problem, &authority) || errors.As(problem, &hostname) || errors.As(problem, &certificate) || errors.As(problem, &record) || strings.Contains(problem.Error(), "tls:") {
		return false
	}
	for _, rejection := range []error{netchan.ErrSessionRejected, netchan.ErrSchemaMismatch, netchan.ErrMalformedFrame, netchan.ErrPayloadLimit, netchan.ErrUnsupportedType} {
		if errors.Is(problem, rejection) {
			return false
		}
	}
	var network net.Error
	// A lost resumable session terminates an already authenticated contact;
	// it does not reject the caller's identity or the requested operation.
	return errors.As(problem, &network) || errors.Is(problem, net.ErrClosed) || errors.Is(problem, netchan.ErrChannelClosed) || errors.Is(problem, netchan.ErrSessionExpired) || errors.Is(problem, io.EOF) || errors.Is(problem, io.ErrUnexpectedEOF) || errors.Is(problem, os.ErrDeadlineExceeded)
}

type contactDiagnostics struct {
	address  string
	observe  func(ContactEvent)
	failures chan error
	stop     <-chan struct{}
}

func (diagnostics *contactDiagnostics) record(stage string, problem error) {
	if diagnostics == nil || diagnostics.observe == nil {
		return
	}
	if problem != nil {
		select {
		case diagnostics.failures <- problem:
		default:
		}
	}
	diagnostics.observe(ContactEvent{Stage: stage, Address: diagnostics.address, Error: problem})
}

func (diagnostics *contactDiagnostics) failure(stage string, problem error) error {
	if diagnostics != nil {
		for {
			select {
			case underlying := <-diagnostics.failures:
				problem = errors.Join(problem, underlying)
			default:
				goto complete
			}
		}
	}
complete:
	diagnostics.record(stage, problem)
	if recoverableContactFailure(problem) {
		return &ContactTransportError{Stage: stage, Cause: problem}
	}
	return problem
}

// The existing TCP/TLS driver still owns dialing and authentication. This
// instance-local adapter preserves the physical error that Dial can otherwise
// reduce to ErrChannelClosed while opening the root channel.
type diagnosticContactDriver struct{ diagnostics *contactDiagnostics }

func (driver diagnosticContactDriver) Scheme() string { return "tcp" }
func (driver diagnosticContactDriver) Listen(endpoint transport.Endpoint, options transport.Options) (net.Listener, error) {
	return (tcp.Driver{}).Listen(endpoint, options)
}
func (driver diagnosticContactDriver) Dial(endpoint transport.Endpoint, options transport.Options) (net.Conn, error) {
	driver.diagnostics.record("tcp-tls-dial", nil)
	parent, owner := options.Done, options.OwnerDone
	finished := make(chan struct{})
	abandoned := make(chan struct{})
	go func() {
		defer close(abandoned)
		select {
		case <-parent:
		case <-owner:
		case <-driver.diagnostics.stop:
		case <-finished:
		}
	}()
	options.Done, options.OwnerDone = abandoned, abandoned
	connection, err := (tcp.Driver{}).Dial(endpoint, options)
	if err != nil {
		close(finished)
		driver.diagnostics.record("tcp-tls-dial-failed", err)
		return nil, err
	}
	driver.diagnostics.record("tcp-tls-authenticated", nil)
	return &diagnosticContactConnection{Conn: connection, diagnostics: driver.diagnostics}, nil
}

type diagnosticContactConnection struct {
	net.Conn
	diagnostics *contactDiagnostics
}

func (connection *diagnosticContactConnection) Underlying() net.Conn { return connection.Conn }
func (connection *diagnosticContactConnection) Read(destination []byte) (int, error) {
	count, err := connection.Conn.Read(destination)
	if err != nil {
		connection.diagnostics.record("physical-read-ended", err)
	}
	return count, err
}
func (connection *diagnosticContactConnection) Write(source []byte) (int, error) {
	count, err := connection.Conn.Write(source)
	if err != nil {
		connection.diagnostics.record("physical-write-failed", err)
	}
	return count, err
}
func (connection *diagnosticContactConnection) Close() error {
	err := connection.Conn.Close()
	connection.diagnostics.record("physical-closed", err)
	return err
}
