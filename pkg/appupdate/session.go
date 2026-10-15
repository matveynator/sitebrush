package appupdate

import (
	"errors"
	"io"
	"net"
	"strconv"
	"time"
)

// A process session outlives its HTTP listeners and database handles. Recovery
// first acquires this local endpoint, so it cannot restore files while an orphan
// from a crashed supervisor is still draining writes or closing databases.
func ServerSession(storagePath string, stop <-chan struct{}, wait time.Duration) (io.Closer, error) {
	host, port, err := net.SplitHostPort(Address(storagePath))
	if err != nil {
		return nil, err
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil {
		return nil, err
	}
	address := net.JoinHostPort(host, strconv.Itoa(portNumber+1))
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		listener, err := net.Listen("tcp", address)
		if err == nil {
			return listener, nil
		}
		if wait <= 0 {
			return nil, errors.New("another server process owns this installation session")
		}
		select {
		case <-stop:
			return nil, errors.New("server session canceled")
		case <-deadline.C:
			return nil, errors.New("previous server did not release its database session")
		case <-time.After(100 * time.Millisecond):
		}
	}
}
