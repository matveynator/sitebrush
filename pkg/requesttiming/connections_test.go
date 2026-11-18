package requesttiming

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

type pipeTimingListener struct {
	connections chan net.Conn
	closing     chan struct{}
	done        chan struct{}
}

func (listener *pipeTimingListener) Accept() (net.Conn, error) {
	select {
	case connection := <-listener.connections:
		return connection, nil
	case <-listener.done:
		return nil, net.ErrClosed
	}
}
func (listener *pipeTimingListener) Close() error {
	select {
	case <-listener.closing:
		close(listener.done)
	default:
	}
	return nil
}
func (listener *pipeTimingListener) Addr() net.Addr { return timingAddress{} }

type timingAddress struct{}

func (timingAddress) Network() string { return "pipe" }
func (timingAddress) String() string  { return "timing-test" }

type heldTimingConnection struct {
	net.Conn
	writing chan struct{}
	release <-chan struct{}
}

func (connection *heldTimingConnection) Write(body []byte) (int, error) {
	select {
	case connection.writing <- struct{}{}:
	default:
	}
	<-connection.release
	return connection.Conn.Write(body)
}

func TestHTTP1CompletionIncludesFinalBufferedWrite(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	collector := New(stop)
	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	writing := make(chan struct{}, 1)
	listener := &pipeTimingListener{connections: make(chan net.Conn, 1), closing: make(chan struct{}, 1), done: make(chan struct{})}
	listener.closing <- struct{}{}
	listener.connections <- &heldTimingConnection{Conn: serverSide, writing: writing, release: release}
	idle := make(chan struct{}, 1)
	server := &http.Server{Handler: collector.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		MarkRAM(request, true)
		writer.Write([]byte("prepared"))
	})), ConnContext: collector.ConnContext, ConnState: func(connection net.Conn, state http.ConnState) {
		collector.ConnState(connection, state)
		if state == http.StateIdle {
			idle <- struct{}{}
		}
	}}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	defer func() { server.Close(); <-finished }()
	clientResult := make(chan error, 1)
	go func() {
		if _, err := fmt.Fprint(clientSide, "GET / HTTP/1.1\r\nHost: site.test\r\n\r\n"); err != nil {
			clientResult <- err
			return
		}
		response, err := http.ReadResponse(bufio.NewReader(clientSide), nil)
		if err == nil {
			_, err = io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
		clientResult <- err
	}()
	select {
	case <-writing:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not flush")
	}
	before, err := collector.Snapshot(stop)
	if err != nil || before.Stages[HandlerComplete].Count != 1 || before.Stages[HTTP1ResponseComplete].Count != 0 {
		close(release)
		t.Fatal("buffered write counted as complete", before, err)
	}
	close(release)
	select {
	case err := <-clientResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not read response")
	}
	select {
	case <-idle:
	case <-time.After(2 * time.Second):
		t.Fatal("connection did not return idle")
	}
	after, err := collector.Snapshot(stop)
	if err != nil || after.Stages[HTTP1ResponseComplete].Count != 1 || after.Stages[HTTP1ResponseComplete].TotalNS < after.Stages[HandlerComplete].TotalNS || after.RAM.Hits != 1 {
		t.Fatal(after, err)
	}
}

func TestHTTP2IsNotReportedAsOneCompletedResponse(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	collector := New(stop)
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	defer clientSide.Close()
	collector.ConnState(serverSide, http.StateActive)
	request, _ := http.NewRequest("GET", "http://site.test/", nil)
	request.ProtoMajor = 2
	request = request.WithContext(collector.ConnContext(context.Background(), serverSide))
	collector.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { writer.WriteHeader(204) })).ServeHTTP(discardTimingResponse{}, request)
	collector.ConnState(serverSide, http.StateIdle)
	report, err := collector.Snapshot(stop)
	if err != nil || report.Stages[HTTP1ResponseComplete].Count != 0 {
		t.Fatal(report, err)
	}
}

type discardTimingResponse struct{}

func (discardTimingResponse) Header() http.Header            { return make(http.Header) }
func (discardTimingResponse) Write(body []byte) (int, error) { return len(body), nil }
func (discardTimingResponse) WriteHeader(int)                {}
