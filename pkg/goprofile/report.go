package goprofile

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"runtime/pprof"
	"sort"
	"strings"
	"time"

	profile "github.com/google/pprof/profile"
	"github.com/matveynator/sitebrush/v2/pkg/requesttiming"
)

const retention = 10 * time.Minute
const storageLimit = 32 << 20
const profileLimit = 8 << 20

type Command struct {
	Action string `json:"action"`
	ID     string `json:"id,omitempty"`
	Page   int    `json:"page,omitempty"`
	View   string `json:"view,omitempty"`
	Search string `json:"search,omitempty"`
}
type Function struct {
	Name  string `json:"name"`
	File  string `json:"file"`
	Line  int64  `json:"line"`
	Self  int64  `json:"self"`
	Total int64  `json:"total"`
}
type Frame struct {
	Name   string `json:"name"`
	Parent int    `json:"parent"`
	Total  int64  `json:"total"`
	Self   int64  `json:"self"`
}
type Stage struct {
	Name   string  `json:"name"`
	Count  uint64  `json:"count"`
	MeanMS float64 `json:"meanMS"`
	P95MS  float64 `json:"p95MS"`
}
type Report struct {
	ID         string     `json:"id"`
	Role       string     `json:"role"`
	PID        int        `json:"pid"`
	Version    string     `json:"version"`
	Started    time.Time  `json:"started"`
	Finished   time.Time  `json:"finished"`
	Expires    time.Time  `json:"expires"`
	CPU        int64      `json:"cpu"`
	Functions  []Function `json:"functions,omitempty"`
	Frames     []Frame    `json:"frames,omitempty"`
	Heap       uint64     `json:"heap"`
	GC         uint32     `json:"gc"`
	Memory     []Function `json:"memory,omitempty"`
	Goroutines string     `json:"goroutines,omitempty"`
	Stages     []Stage    `json:"stages,omitempty"`
	Truncated  bool       `json:"truncated"`
	Error      string     `json:"error,omitempty"`
}
type State struct {
	Role    string    `json:"role"`
	Busy    bool      `json:"busy"`
	Started time.Time `json:"started"`
	Reports []Report  `json:"reports"`
	Report  *Report   `json:"report,omitempty"`
	More    bool      `json:"more"`
	Error   string    `json:"error,omitempty"`
	Evicted bool      `json:"evicted"`
}
type task struct {
	command   Command
	reply     chan State
	abandoned <-chan struct{}
	timings   *requesttiming.Collector
	configure bool
}
type artifact struct {
	meta         Report
	raw, summary *os.File
	size         int64
}
type capture struct{ artifact artifact }

// Commands transfer to the process owner; HTTP and NetChan subscribers never share its history.
func (session *Session) Exchange(command Command, abandoned <-chan struct{}) State {
	if session == nil {
		return State{Error: "Profiler is disabled"}
	}
	reply := make(chan State, 1)
	select {
	case session.tasks <- task{command: command, reply: reply, abandoned: abandoned}:
	case <-abandoned:
		return State{Error: "Disconnected"}
	case <-session.done:
		return State{Error: "Profiler stopped"}
	}
	select {
	case result := <-reply:
		return result
	case <-abandoned:
		return State{Error: "Disconnected"}
	case <-session.done:
		return State{Error: "Profiler stopped"}
	}
}
func (session *Session) SetTimings(timings *requesttiming.Collector) {
	if session == nil {
		return
	}
	reply := make(chan State, 1)
	select {
	case session.tasks <- task{command: Command{Action: "timings"}, reply: reply, timings: timings, configure: true}:
	case <-session.done:
		return
	}
	select {
	case <-reply:
	case <-session.done:
	}
}
func (session *Session) Relay(encoded []byte) ([]byte, error) {
	if len(encoded) > 4096 {
		return nil, errors.New("profiler command too large")
	}
	var command Command
	if err := json.Unmarshal(encoded, &command); err != nil {
		return nil, err
	}
	return json.Marshal(session.Exchange(command, nil))
}
func closeArtifact(record artifact) {
	if record.raw != nil {
		record.raw.Close()
	}
	if record.summary != nil {
		record.summary.Close()
	}
}
func (session *Session) run(role, version string, server *http.Server, served <-chan error, stop <-chan struct{}) {
	defer close(session.done)
	defer server.Close()
	directory, dirErr := os.MkdirTemp("", "sitebrush-profiler-")
	if dirErr == nil {
		defer os.RemoveAll(directory)
	}
	var records []artifact
	defer func() {
		for _, record := range records {
			closeArtifact(record)
		}
	}()
	completed := make(chan capture, 1)
	var cancel chan struct{}
	var started time.Time
	var timings *requesttiming.Collector
	evicted := false
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	prune := func() { records = expireArtifacts(records, time.Now()) }
	defer func() {
		if cancel != nil {
			close(cancel)
			result := <-completed
			closeArtifact(result.artifact)
		}
	}()
	for {
		select {
		case <-stop:
			return
		case <-session.closes:
			return
		case <-served:
			return
		case <-timer.C:
			prune()
		case result := <-completed:
			cancel = nil
			records = append(records, result.artifact)
		case request := <-session.tasks:
			select {
			case <-request.abandoned:
				continue
			default:
			}
			prune()
			state := State{Role: role, Busy: cancel != nil, Started: started, Evicted: evicted}
			switch request.command.Action {
			case "timings":
				if request.configure {
					timings = request.timings
				} else {
					state.Error = "Unknown profiler command"
				}
			case "start":
				if cancel != nil {
					state.Error = "A recording is already running"
					break
				}
				if dirErr != nil {
					state.Error = dirErr.Error()
					break
				}
				// Reserve space for the bounded raw profile and its derived report before starting work.
				var removed bool
				records, removed = reserveArtifacts(records, 2*profileLimit)
				evicted = evicted || removed
				started = time.Now()
				cancel = make(chan struct{})
				go collect(directory, role, version, started, timings, cancel, stop, completed)
				state.Busy = true
				state.Started = started
			case "cancel":
				if cancel != nil {
					close(cancel)
					result := <-completed
					closeArtifact(result.artifact)
					cancel = nil
				}
				state.Busy = false
			case "delete":
				for index, record := range records {
					if record.meta.ID == request.command.ID {
						closeArtifact(record)
						records = append(records[:index], records[index+1:]...)
						break
					}
				}
			case "status":
			case "report":
				state.Error = "Recording expired or unavailable"
				for _, record := range records {
					if record.meta.ID == request.command.ID {
						var report Report
						if record.summary == nil {
							report = record.meta
						} else if err := json.NewDecoder(io.NewSectionReader(record.summary, 0, record.size)).Decode(&report); err != nil {
							state.Error = err.Error()
							break
						}
						report = reportPage(report, request.command.View, request.command.Page, request.command.Search, &state.More)
						state.Report = &report
						state.Error = ""
						break
					}
				}
			default:
				state.Error = "Unknown profiler command"
			}
			state.Evicted = evicted
			for index := len(records) - 1; index >= 0; index-- {
				state.Reports = append(state.Reports, records[index].meta)
			}
			select {
			case request.reply <- state:
			case <-request.abandoned:
			case <-stop:
				return
			}
		}
	}
}
func reportPage(report Report, view string, page int, search string, more *bool) Report {
	functions, frames, memory, goroutines, stages := report.Functions, report.Frames, report.Memory, report.Goroutines, report.Stages
	report.Functions = nil
	report.Frames = nil
	report.Memory = nil
	report.Goroutines = ""
	report.Stages = nil
	if page < 0 {
		page = 0
	}
	switch view {
	case "graph":
		report.Frames = frames
	case "memory":
		report.Memory = memory
	case "goroutines":
		report.Goroutines = goroutines
	case "stages":
		report.Stages = stages
	default:
		if search != "" {
			filtered := make([]Function, 0, len(functions))
			for _, function := range functions {
				if strings.Contains(strings.ToLower(function.Name), strings.ToLower(search)) {
					filtered = append(filtered, function)
				}
			}
			functions = filtered
		}
		begin := min(page, len(functions)/50+1) * 50
		if begin < len(functions) {
			end := min(begin+50, len(functions))
			report.Functions = functions[begin:end]
			*more = end < len(functions)
		}
	}
	return report
}

type limitedWriter struct {
	target    io.Writer
	remaining int64
}

func (writer *limitedWriter) Write(payload []byte) (int, error) {
	if int64(len(payload)) > writer.remaining {
		return 0, errors.New("profile size limit exceeded")
	}
	count, err := writer.target.Write(payload)
	writer.remaining -= int64(count)
	return count, err
}
func parseProfile(file *os.File) (*profile.Profile, error) {
	if _, err := file.Seek(0, 0); err != nil {
		return nil, err
	}
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer compressed.Close()
	decoded, err := io.ReadAll(io.LimitReader(compressed, (64<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(decoded) > 64<<20 {
		return nil, errors.New("profile expansion limit exceeded")
	}
	return profile.ParseUncompressed(decoded)
}
func collect(directory, role, version string, started time.Time, timings *requesttiming.Collector, cancel, stop <-chan struct{}, completed chan<- capture) {
	report := Report{ID: fmt.Sprintf("%d-%d", os.Getpid(), started.UnixNano()), Role: role, PID: os.Getpid(), Version: version, Started: started}
	record := artifact{}
	defer func() {
		report.Finished = time.Now()
		report.Expires = report.Finished.Add(retention)
		summary, err := temporaryProfile(directory)
		if err == nil {
			err = json.NewEncoder(&limitedWriter{target: summary, remaining: profileLimit}).Encode(report)
		}
		if err != nil {
			if summary != nil {
				summary.Close()
			}
			report.Error = fmt.Sprint(err)
		} else {
			record.summary = summary
			info, _ := summary.Stat()
			if info != nil {
				record.size += info.Size()
			}
		}
		record.meta = report
		record.meta.Functions = nil
		record.meta.Frames = nil
		record.meta.Memory = nil
		record.meta.Goroutines = ""
		record.meta.Stages = nil
		completed <- capture{artifact: record}
	}()
	raw, err := temporaryProfile(directory)
	if err != nil {
		report.Error = err.Error()
		return
	}
	record.raw = raw
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	var first requesttiming.Report
	timingsReady := false
	if timings != nil {
		var snapshotErr error
		first, snapshotErr = timings.Snapshot(stop)
		timingsReady = snapshotErr == nil
	}
	if err = pprof.StartCPUProfile(&limitedWriter{target: raw, remaining: profileLimit}); err != nil {
		report.Error = err.Error()
		return
	}
	timer := time.NewTimer(30 * time.Second)
	select {
	case <-timer.C:
	case <-cancel:
		report.Error = "Recording cancelled"
	case <-stop:
		report.Error = "Process stopped"
	}
	timer.Stop()
	pprof.StopCPUProfile()
	if info, statErr := raw.Stat(); statErr == nil {
		record.size = info.Size()
	}
	if report.Error != "" {
		return
	}
	parsed, err := parseProfile(raw)
	if err != nil {
		report.Error = err.Error()
		return
	}
	report.Functions, report.Frames, report.CPU, report.Truncated = aggregate(parsed, "cpu", 1200)
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	report.Heap = after.HeapAlloc
	report.GC = after.NumGC - before.NumGC
	var heap bytes.Buffer
	if err := pprof.Lookup("heap").WriteTo(&limitedWriter{target: &heap, remaining: profileLimit}, 0); err == nil {
		if parsedHeap, parseErr := profile.ParseData(heap.Bytes()); parseErr == nil {
			report.Memory, _, _, _ = aggregate(parsedHeap, "inuse_space", 0)
			if len(report.Memory) > 100 {
				report.Memory = report.Memory[:100]
				report.Truncated = true
			}
		} else {
			report.Truncated = true
		}
	} else {
		report.Truncated = true
	}
	report.Goroutines, err = captureGoroutineStates(64 << 10)
	if err != nil {
		report.Truncated = true
	}

	if timings != nil {
		if last, snapshotErr := timings.Snapshot(stop); snapshotErr == nil && timingsReady {
			for index, current := range last.Stages {
				old := first.Stages[index]
				count := current.Count - old.Count
				if count == 0 {
					continue
				}
				delta := current
				delta.Count = count
				for bucket := range delta.Buckets {
					delta.Buckets[bucket] -= old.Buckets[bucket]
				}
				report.Stages = append(report.Stages, Stage{Name: current.Stage, Count: count, MeanMS: float64(current.TotalNS-old.TotalNS) / float64(count) / 1e6, P95MS: float64(delta.PercentileUpperBound(95)) / 1e6})
			}
			if first.Incomplete || last.Incomplete {
				report.Truncated = true
			}
		} else {
			report.Truncated = true
		}
	}
}

// Cumulative function costs count recursive occurrences once per sample; the flame tree retains each call path.
func aggregate(parsed *profile.Profile, kind string, maxFrames int) ([]Function, []Frame, int64, bool) {
	sampleIndex := -1
	for index, sampleType := range parsed.SampleType {
		if sampleType.Type == kind {
			sampleIndex = index
			break
		}
	}
	if sampleIndex < 0 {
		return nil, nil, 0, false
	}
	functions := make(map[string]*Function)
	frames := []Frame{{Name: "all samples", Parent: -1}}
	edges := make(map[string]int)
	var total int64
	truncated := false
	for _, sample := range parsed.Sample {
		if sampleIndex >= len(sample.Value) {
			continue
		}
		weight := sample.Value[sampleIndex]
		if weight <= 0 {
			continue
		}
		total += weight
		frames[0].Total += weight
		seen := make(map[string]bool)
		parent := 0
		graphStopped := false
		leaf := ""
		for locationIndex := len(sample.Location) - 1; locationIndex >= 0; locationIndex-- {
			location := sample.Location[locationIndex]
			for lineIndex := len(location.Line) - 1; lineIndex >= 0; lineIndex-- {
				line := location.Line[lineIndex]
				if line.Function == nil {
					continue
				}
				name := line.Function.Name
				key := name + "\x00" + line.Function.Filename
				function := functions[key]
				if function == nil {
					function = &Function{Name: name, File: line.Function.Filename, Line: line.Line}
					functions[key] = function
				}
				if !seen[key] {
					function.Total += weight
					seen[key] = true
				}
				leaf = key
				if maxFrames > 0 && !graphStopped {
					edge := fmt.Sprintf("%d\x00%s", parent, key)
					child, exists := edges[edge]
					if !exists {
						if len(frames) >= maxFrames {
							truncated = true
							graphStopped = true
							continue
						}
						child = len(frames)
						edges[edge] = child
						frames = append(frames, Frame{Name: name, Parent: parent})
					}
					frames[child].Total += weight
					parent = child
				}
			}
		}
		if leaf != "" {
			functions[leaf].Self += weight
		}
		frames[parent].Self += weight
	}
	ranked := make([]Function, 0, len(functions))
	for _, function := range functions {
		ranked = append(ranked, *function)
	}
	sort.Slice(ranked, func(left, right int) bool {
		if ranked[left].Total == ranked[right].Total {
			return ranked[left].Name < ranked[right].Name
		}
		return ranked[left].Total > ranked[right].Total
	})
	return ranked, frames, total, truncated
}

func expireArtifacts(records []artifact, now time.Time) []artifact {
	retained := records[:0]
	for _, record := range records {
		if !now.Before(record.meta.Expires) {
			closeArtifact(record)
		} else {
			retained = append(retained, record)
		}
	}
	return retained
}
func reserveArtifacts(records []artifact, reserved int64) ([]artifact, bool) {
	var used int64
	for _, record := range records {
		used += record.size
	}
	removed := false
	for used+reserved > storageLimit && len(records) > 0 {
		used -= records[0].size
		closeArtifact(records[0])
		records = records[1:]
		removed = true
	}
	return records, removed
}

// Preserve the beginning of a state-bearing stack dump even when one runtime write exceeds the budget.
type stackPrefixWriter struct {
	output    io.Writer
	remaining int64
}

func (writer *stackPrefixWriter) Write(payload []byte) (int, error) {
	length := min(int64(len(payload)), writer.remaining)
	count, err := writer.output.Write(payload[:length])
	writer.remaining -= int64(count)
	if err != nil {
		return count, err
	}
	if count < len(payload) {
		return count, io.ErrShortWrite
	}
	return count, nil
}
func captureGoroutineStates(limit int64) (string, error) {
	var output bytes.Buffer
	err := pprof.Lookup("goroutine").WriteTo(&stackPrefixWriter{output: &output, remaining: limit}, 2)
	if err != nil {
		output.WriteString("\n[goroutine output truncated]")
	}
	return output.String(), err
}
