package netchantransport

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/matveynator/netchan/v2/pkg/transport"
	quicgo "github.com/quic-go/quic-go"
)

// The installation owns both the QUIC transport and its UDP socket. Closing a
// quic-go Listener alone only stops admission and can retain the bound socket.
func listenQUIC(endpoint transport.Endpoint, options transport.Options) (net.Listener, error) {
	packets, err := net.ListenPacket("udp", endpoint.Host)
	if err != nil {
		return nil, err
	}
	owner := &quicgo.Transport{Conn: packets}
	configuration := options.TLS.Clone()
	configuration.NextProtos = []string{"netchan/2"}
	listener, err := owner.Listen(configuration, &quicgo.Config{MaxIncomingStreams: 1, MaxIncomingUniStreams: -1, HandshakeIdleTimeout: 15 * time.Second})
	if err != nil {
		_ = owner.Close()
		_ = packets.Close()
		return nil, err
	}
	boundary, cancel := context.WithCancel(context.Background())
	return &quicListener{listener: listener, owner: owner, packets: packets, boundary: boundary, cancel: cancel}, nil
}

type quicListener struct {
	listener *quicgo.Listener
	owner    *quicgo.Transport
	packets  net.PacketConn
	boundary context.Context
	cancel   context.CancelFunc
}

func (listener *quicListener) Addr() net.Addr { return listener.packets.LocalAddr() }

func (listener *quicListener) Close() error {
	listener.cancel()
	return errors.Join(listener.listener.Close(), listener.owner.Close(), listener.packets.Close())
}

func (listener *quicListener) Accept() (net.Conn, error) {
	connection, err := listener.listener.Accept(listener.boundary)
	if err != nil {
		return nil, err
	}
	streams := make(chan quicStreamResult)
	go func() {
		boundary, cancel := context.WithTimeout(connection.Context(), 15*time.Second)
		stream, err := connection.AcceptStream(boundary)
		cancel()
		// Publishing an immutable stream through a channel keeps concurrent
		// Read and Write independent without a shared initialization flag.
		for {
			select {
			case streams <- quicStreamResult{stream: stream, err: err}:
			case <-connection.Context().Done():
				close(streams)
				return
			}
		}
	}()
	return &quicConnection{connection: connection, streams: streams, peerFinished: make(chan struct{}, 1)}, nil
}

type quicStreamResult struct {
	stream *quicgo.Stream
	err    error
}

type quicConnection struct {
	connection   *quicgo.Conn
	streams      <-chan quicStreamResult
	peerFinished chan struct{}
}

func (connection *quicConnection) stream() (*quicgo.Stream, error) {
	response, open := <-connection.streams
	if !open {
		return nil, net.ErrClosed
	}
	return response.stream, response.err
}

func (connection *quicConnection) Read(buffer []byte) (int, error) {
	stream, err := connection.stream()
	if err != nil {
		return 0, err
	}
	return stream.Read(buffer)
}

func (connection *quicConnection) Write(payload []byte) (int, error) {
	stream, err := connection.stream()
	if err != nil {
		return 0, err
	}
	return stream.Write(payload)
}

func (connection *quicConnection) Close() error {
	return connection.connection.CloseWithError(0, "closed")
}
func (connection *quicConnection) LocalAddr() net.Addr  { return connection.connection.LocalAddr() }
func (connection *quicConnection) RemoteAddr() net.Addr { return connection.connection.RemoteAddr() }

func (connection *quicConnection) SetDeadline(deadline time.Time) error {
	stream, err := connection.stream()
	if err != nil {
		return err
	}
	return stream.SetDeadline(deadline)
}

func (connection *quicConnection) SetReadDeadline(deadline time.Time) error {
	stream, err := connection.stream()
	if err != nil {
		return err
	}
	return stream.SetReadDeadline(deadline)
}

func (connection *quicConnection) SetWriteDeadline(deadline time.Time) error {
	stream, err := connection.stream()
	if err != nil {
		return err
	}
	return stream.SetWriteDeadline(deadline)
}

func (connection *quicConnection) PeerFinished() {
	select {
	case connection.peerFinished <- struct{}{}:
	default:
	}
}

func (connection *quicConnection) Drain(deadline time.Time) error {
	stream, err := connection.stream()
	if err != nil {
		return err
	}
	if err := stream.Close(); err != nil {
		return err
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-connection.peerFinished:
		return nil
	case <-connection.connection.Context().Done():
		return context.Cause(connection.connection.Context())
	case <-timer.C:
		return nil
	}
}
