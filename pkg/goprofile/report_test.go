package goprofile

import (
	"bytes"
	profile "github.com/google/pprof/profile"
	"io"
	"strings"
	"testing"
	"time"
)

func TestRecursiveCostsAndReportPaging(t *testing.T) {
	function := &profile.Function{ID: 1, Name: "recursive", Filename: "source.go"}
	parsed := &profile.Profile{SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}}, Sample: []*profile.Sample{{Value: []int64{10}, Location: []*profile.Location{
		{ID: 1, Line: []profile.Line{{Function: function, Line: 2}}},
		{ID: 2, Line: []profile.Line{{Function: function, Line: 1}}},
	}}}}
	functions, frames, total, truncated := aggregate(parsed, "cpu", 100)
	if total != 10 || truncated || len(functions) != 1 || functions[0].Self != 10 || functions[0].Total != 10 || len(frames) != 3 {
		t.Fatal(functions, frames, total, truncated)
	}
	_, _, _, truncated = aggregate(parsed, "cpu", 1)
	if !truncated {
		t.Fatal("missing truncation marker")
	}
	report := Report{Functions: functions, Frames: frames}
	more := false
	page := reportPage(report, "functions", 0, "absent", &more)
	if len(page.Functions) != 0 {
		t.Fatal("search did not filter")
	}
	page = reportPage(report, "functions", 0, "recursive", &more)
	if len(page.Functions) != 1 || len(page.Frames) != 0 {
		t.Fatal("unbounded report page")
	}
}
func TestRetentionQuotaAndBoundedWriter(t *testing.T) {
	directory := t.TempDir()
	expired, err := temporaryProfile(directory)
	if err != nil {
		t.Fatal(err)
	}
	current, err := temporaryProfile(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	now := time.Now()
	records := expireArtifacts([]artifact{{raw: expired, meta: Report{Expires: now}}, {raw: current, meta: Report{Expires: now.Add(time.Minute)}, size: storageLimit}}, now)
	if len(records) != 1 {
		t.Fatal("expired record retained")
	}
	if _, err = expired.Seek(0, io.SeekStart); err == nil {
		t.Fatal("expired descriptor retained")
	}
	records, removed := reserveArtifacts(records, profileLimit)
	if !removed || len(records) != 0 {
		t.Fatal("quota not enforced")
	}
	if _, err = current.Seek(0, io.SeekStart); err == nil {
		t.Fatal("evicted descriptor retained")
	}
	var output bytes.Buffer
	writer := limitedWriter{target: &output, remaining: 3}
	if _, err = writer.Write([]byte("four")); err == nil || output.Len() != 0 {
		t.Fatal("oversized write retained")
	}
}

func TestCaptureOwnerRejectsRemoteConfiguration(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	session, err := Start("application", "fixture", "console", stop)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if state := session.Exchange(Command{Action: "timings"}, nil); state.Error == "" {
		t.Fatal("remote client changed internal collector")
	}
	if state := session.Exchange(Command{Action: "status"}, nil); state.Busy || len(state.Reports) != 0 {
		t.Fatal("idle profiler started collecting")
	}
}

func blockedProfileGoroutine(ready chan<- struct{}, release <-chan struct{}) {
	close(ready)
	<-release
}

func TestGoroutineCaptureIncludesStatesAndBoundedPrefix(t *testing.T) {
	ready := make(chan struct{})
	release := make(chan struct{})
	go blockedProfileGoroutine(ready, release)
	defer close(release)
	<-ready
	dump, err := captureGoroutineStates(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump, "[chan receive]") || !strings.Contains(dump, "blockedProfileGoroutine") {
		t.Fatal("goroutine states missing from stack dump")
	}
	prefix, err := captureGoroutineStates(128)
	if err == nil || !strings.HasPrefix(prefix, "goroutine ") || !strings.Contains(prefix, "[goroutine output truncated]") || len(prefix) > 128+len("\n[goroutine output truncated]") {
		t.Fatal("oversized dump lost its bounded prefix", len(prefix), err)
	}
}

func TestViewingReleaseCancelsOnlyItsOwnCapture(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	session, err := Start("application", "fixture", "console", stop)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	state := session.Exchange(Command{Action: "start", Viewer: "first-view"}, nil)
	if state.Error != "" || !state.Busy {
		t.Fatal(state)
	}
	state = session.Exchange(Command{Action: "release", Viewer: "another-view"}, nil)
	if !state.Busy {
		t.Fatal("another viewer cancelled the recording")
	}
	state = session.Exchange(Command{Action: "release", Viewer: "first-view"}, nil)
	if state.Busy || len(state.Reports) != 0 {
		t.Fatal("view release retained its recording", state)
	}
}

func TestSampledCodeRetainsLeafLine(t *testing.T) {
	function := &profile.Function{ID: 1, Name: "work", Filename: "worker.go"}
	parsed := &profile.Profile{SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}}, Sample: []*profile.Sample{{Value: []int64{123}, Location: []*profile.Location{{Line: []profile.Line{{Function: function, Line: 42}}}}}}}
	locations := sampledCode(parsed, "cpu")
	if len(locations) != 1 || locations[0].Line != 42 || locations[0].Self != 123 {
		t.Fatal(locations)
	}
}
