package goprofile

import (
	"testing"
	"time"
)

func TestSessionShutdownWithoutHTTPListener(t *testing.T) {
	stop := make(chan struct{})
	session, err := Start("public", "fixture", "console", stop)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if state := session.Exchange(Command{Action: "status", Viewer: "owner"}, nil); state.Role != "public" || state.Busy || len(state.Reports) != 0 {
		t.Fatal(state)
	}
	close(stop)
	select {
	case <-session.done:
	case <-time.After(5 * time.Second):
		t.Fatal("profiler outlived process")
	}
}
