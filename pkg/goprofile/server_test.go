package goprofile

import (
	"encoding/json"
	"testing"
	"time"
)

func TestProfilerRelayUsesConsumerCancellation(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	session, err := Start("public", "fixture", "console", stop)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	abandoned := make(chan struct{})
	close(abandoned)
	encoded, err := session.RelaySession([]byte(`{"action":"status","viewer":"owner"}`), abandoned)
	if err != nil {
		t.Fatal(err)
	}
	var state State
	if err := json.Unmarshal(encoded, &state); err != nil {
		t.Fatal(err)
	}
	if state.Error != "Disconnected" {
		t.Fatalf("abandoned request survived: %+v", state)
	}
	encoded, err = session.RelaySession([]byte(`{"action":"status","viewer":"owner"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	state = State{}
	if err := json.Unmarshal(encoded, &state); err != nil {
		t.Fatal(err)
	}
	if state.Error != "" || state.Role != "public" || state.Busy {
		t.Fatalf("relay changed public session: %+v", state)
	}
}

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
