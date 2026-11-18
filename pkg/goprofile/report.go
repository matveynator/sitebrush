package goprofile

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/pprof"
	"sort"
	"time"

	profile "github.com/google/pprof/profile"
	"github.com/matveynator/sitebrush/v2/pkg/publicsite"
	"github.com/matveynator/sitebrush/v2/pkg/requesttiming"
)

const retention = 10 * time.Minute
const storageLimit = 32 << 20
const profileLimit = 8 << 20

type Command struct {
	RunID     string  `json:"runID,omitempty"`
	Function  int     `json:"function,omitempty"`
	Sort      string  `json:"sort,omitempty"`
	Ascending bool    `json:"ascending,omitempty"`
	Kind      string  `json:"kind,omitempty"`
	Minimum   float64 `json:"minimum,omitempty"`
	Viewer    string  `json:"viewer,omitempty"`
	Action    string  `json:"action"`
	ID        string  `json:"id,omitempty"`
	Page      int     `json:"page,omitempty"`
	View      string  `json:"view,omitempty"`
	Search    string  `json:"search,omitempty"`
}
type Function struct {
	ID      int    `json:"id"`
	Kind    string `json:"kind"`
	Samples int64  `json:"samples"`
	Name    string `json:"name"`
	File    string `json:"file"`
	Line    int64  `json:"line"`
	Self    int64  `json:"self"`
	Total   int64  `json:"total"`
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
	P90MS  float64 `json:"p90MS"`
	P99MS  float64 `json:"p99MS"`
}
type Report struct {
	RAM             *RAMReport                `json:"ramCache,omitempty"`
	Runtime         *RuntimeReport            `json:"runtime,omitempty"`
	RunID           string                    `json:"runID"`
	System          string                    `json:"system"`
	Architecture    string                    `json:"architecture"`
	GoVersion       string                    `json:"goVersion"`
	User            string                    `json:"user"`
	CaptureFinished time.Time                 `json:"captureFinished"`
	Calls           []Call                    `json:"calls,omitempty"`
	Groups          []Group                   `json:"groups,omitempty"`
	Detail          *FunctionDetail           `json:"detail,omitempty"`
	Matches         int                       `json:"matches"`
	Code            []Function                `json:"code,omitempty"`
	Network         requesttiming.HTTPMetrics `json:"network"`
	Allocated       uint64                    `json:"allocated"`
	ID              string                    `json:"id"`
	Role            string                    `json:"role"`
	PID             int                       `json:"pid"`
	Version         string                    `json:"version"`
	Started         time.Time                 `json:"started"`
	Finished        time.Time                 `json:"finished"`
	Expires         time.Time                 `json:"expires"`
	CPU             int64                     `json:"cpu"`
	Functions       []Function                `json:"functions,omitempty"`
	Frames          []Frame                   `json:"frames,omitempty"`
	Heap            uint64                    `json:"heap"`
	GC              uint32                    `json:"gc"`
	Memory          []Function                `json:"memory,omitempty"`
	Goroutines      string                    `json:"goroutines,omitempty"`
	Stages          []Stage                   `json:"stages,omitempty"`
	Truncated       bool                      `json:"truncated"`
	Error           string                    `json:"error,omitempty"`
}
type State struct {
	Protocol int       `json:"protocol"`
	RunID    string    `json:"runID,omitempty"`
	Targets  []string  `json:"targets,omitempty"`
	Text     string    `json:"text,omitempty"`
	Role     string    `json:"role"`
	Busy     bool      `json:"busy"`
	Started  time.Time `json:"started"`
	Reports  []Report  `json:"reports"`
	Report   *Report   `json:"report,omitempty"`
	More     bool      `json:"more"`
	Error    string    `json:"error,omitempty"`
	Evicted  bool      `json:"evicted"`
}
type task struct {
	cache     *publicsite.Catalog
	command   Command
	reply     chan State
	abandoned <-chan struct{}
	timings   *requesttiming.Collector
	configure bool
}
type artifact struct {
	done         chan struct{}
	reader       chan readTask
	session      chan struct{}
	viewer       string
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
	session.configure(task{command: Command{Action: "timings"}, timings: timings, configure: true})
}

func (session *Session) SetPublicCache(cache *publicsite.Catalog) {
	session.configure(task{command: Command{Action: "ram-cache"}, cache: cache, configure: true})
}

func (session *Session) configure(configuration task) {
	if session == nil {
		return
	}
	reply := make(chan State, 1)
	configuration.reply = reply
	select {
	case session.tasks <- configuration:
	case <-session.done:
		return
	}
	select {
	case <-reply:
	case <-session.done:
	}
}
func (session *Session) Relay(encoded []byte) ([]byte, error) {
	return session.RelaySession(encoded, nil)
}

func (session *Session) RelaySession(encoded []byte, abandoned <-chan struct{}) ([]byte, error) {
	if len(encoded) > 4096 {
		return nil, errors.New("profiler command too large")
	}
	var command Command
	if err := json.Unmarshal(encoded, &command); err != nil {
		return nil, err
	}
	return json.Marshal(session.Exchange(command, abandoned))
}
func closeArtifact(record artifact) {
	if record.session != nil {
		close(record.session)
		return
	}
	if record.raw != nil {
		record.raw.Close()
	}
	if record.summary != nil {
		record.summary.Close()
	}
}
func (session *Session) run(role, version string, stop <-chan struct{}) {
	defer close(session.done)
	var directory string
	defer func() {
		if directory != "" {
			os.RemoveAll(directory)
		}
	}()
	var records []artifact
	defer func() {
		for _, record := range records {
			closeArtifact(record)
		}
		// Shutdown waits for file owners before removing their directory, including Windows delete-on-close files.
		for _, record := range records {
			if record.done != nil {
				<-record.done
			}
		}
	}()
	completed := make(chan capture, 1)
	var cancel chan struct{}
	var discarded bool
	var capturingViewer string
	var capturingRun string
	var started time.Time
	var timings *requesttiming.Collector
	var cache *publicsite.Catalog
	evicted := false
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	cleanupDirectory := func() {
		if len(records) == 0 && cancel == nil && directory != "" {
			if err := os.RemoveAll(directory); err == nil {
				directory = ""
			}
		}
	}
	prune := func() { records = expireArtifacts(records, time.Now()); cleanupDirectory() }
	defer func() {
		if cancel != nil {
			select {
			case <-cancel:
			default:
				close(cancel)
			}
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
		case <-timer.C:
			prune()
		case result := <-completed:
			cancel = nil
			result.artifact.viewer = capturingViewer
			if discarded {
				closeArtifact(result.artifact)
				discarded = false
				cleanupDirectory()
			} else {
				records = append(records, ownArtifact(result.artifact, stop))
			}
		case request := <-session.tasks:
			select {
			case <-request.abandoned:
				continue
			default:
			}
			prune()
			state := State{Protocol: 1, Role: role, Busy: cancel != nil, Started: started, Evicted: evicted}
			var selected *artifact
			switch request.command.Action {
			case "timings":
				if request.configure {
					timings = request.timings
				} else {
					state.Error = "Unknown profiler command"
				}
			case "ram-cache":
				if request.configure {
					cache = request.cache
				} else {
					state.Error = "Unknown profiler command"
				}
			case "start":
				// A fallback contact may retry a command whose reply was lost.
				// The viewer and run identify the same recording across transports.
				if request.command.RunID != "" && capturingRun == request.command.RunID && capturingViewer == request.command.Viewer {
					break
				}
				if cancel != nil {
					state.Error = "A recording is already running"
					break
				}
				if directory == "" {
					created, dirErr := os.MkdirTemp("", "sitebrush-profiler-")
					if dirErr != nil {
						state.Error = dirErr.Error()
						break
					}
					directory = created
				}
				// Reserve space for the bounded raw profile and its derived report before starting work.
				var removed bool
				records, removed = reserveArtifacts(records, 2*profileLimit)
				evicted = evicted || removed
				started = time.Now()
				capturingViewer = request.command.Viewer
				capturingRun = request.command.RunID
				if capturingRun == "" {
					capturingRun = fmt.Sprintf("%d-%d", os.Getpid(), started.UnixNano())
				}
				cancel = make(chan struct{})
				go collect(directory, role, version, capturingRun, started, timings, cache, cancel, stop, completed)
				state.Busy = true
				state.Started = started
			case "cancel":
				if cancel != nil && capturingViewer == request.command.Viewer && !discarded {
					close(cancel)
					discarded = true
				}
				state.Busy = false
			case "delete":
				for index, record := range records {
					if record.meta.ID == request.command.ID && record.viewer == request.command.Viewer {
						closeArtifact(record)
						records = append(records[:index], records[index+1:]...)
						break
					}
				}
			case "release":
				if cancel != nil && capturingViewer == request.command.Viewer && !discarded {
					close(cancel)
					discarded = true
				}
				retained := records[:0]
				for _, record := range records {
					if record.viewer == request.command.Viewer {
						closeArtifact(record)
					} else {
						retained = append(retained, record)
					}
				}
				records = retained
			case "status":
			case "report", "export":
				state.Error = "Recording expired or unavailable"
				for index := range records {
					if records[index].meta.ID == request.command.ID && records[index].viewer == request.command.Viewer {
						selected = &records[index]
						state.Error = ""
						break
					}
				}
			default:
				state.Error = "Unknown profiler command"
			}
			cleanupDirectory()
			state.Busy = cancel != nil
			state.RunID = capturingRun
			state.Evicted = evicted
			for index := len(records) - 1; index >= 0; index-- {
				if records[index].viewer == request.command.Viewer {
					state.Reports = append(state.Reports, records[index].meta)
				}
			}
			if selected != nil {
				select {
				case selected.reader <- readTask{task: request, state: state}:
					continue
				default:
					state.Error = "Report reader is busy"
				}
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
func collect(directory, role, version, runID string, started time.Time, timings *requesttiming.Collector, cache *publicsite.Catalog, cancel, stop <-chan struct{}, completed chan<- capture) {
	report := Report{RunID: runID, System: runtime.GOOS, Architecture: runtime.GOARCH, GoVersion: runtime.Version(), User: processUser(), ID: fmt.Sprintf("%d-%d", os.Getpid(), started.UnixNano()), Role: role, PID: os.Getpid(), Version: version, Started: started}
	record := artifact{}
	defer func() {
		report.Finished = time.Now()
		if report.CaptureFinished.IsZero() {
			report.CaptureFinished = report.Finished
		}
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
		record.meta.Groups = nil
		record.meta.Calls = nil
		record.meta.Detail = nil
		record.meta.Code = nil
		record.meta.Functions = nil
		record.meta.Frames = nil
		record.meta.Memory = nil
		record.meta.Goroutines = ""
		record.meta.Stages = nil
		record.meta.Runtime = nil
		completed <- capture{artifact: record}
	}()
	raw, err := temporaryProfile(directory)
	if err != nil {
		report.Error = err.Error()
		return
	}
	record.raw = raw
	sampler := newRuntimeSampler()
	report.Runtime = &RuntimeReport{Supported: sampler.supported()}
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	var first requesttiming.Report
	timingsReady := false
	if timings != nil {
		var snapshotErr error
		first, snapshotErr = timings.Snapshot(stop)
		timingsReady = snapshotErr == nil
	}
	report.Started = time.Now()
	report.Runtime.Samples = append(report.Runtime.Samples, sampler.sample(report.Started))
	if err = pprof.StartCPUProfile(&limitedWriter{target: raw, remaining: profileLimit}); err != nil {
		report.Error = err.Error()
		return
	}
	timer := time.NewTimer(30 * time.Second)
	sampling := time.NewTicker(time.Second)
recording:
	for {
		select {
		case <-timer.C:
			break recording
		case <-sampling.C:
			if len(report.Runtime.Samples) < 31 {
				report.Runtime.Samples = append(report.Runtime.Samples, sampler.sample(report.Started))
			}
		case <-cancel:
			report.Error = "Recording cancelled"
			break recording
		case <-stop:
			report.Error = "Process stopped"
			break recording
		}
	}
	sampling.Stop()
	timer.Stop()
	report.Runtime.Samples = append(report.Runtime.Samples, sampler.sample(report.Started))
	report.Runtime.Scheduler = sampler.scheduler()
	report.CaptureFinished = time.Now()
	pprof.StopCPUProfile()
	if info, statErr := raw.Stat(); statErr == nil {
		record.size = info.Size()
	}
	if report.Error != "" {
		return
	}
	// Snapshot memory before decoding the CPU report, so analysis allocations do not dominate the load sample.
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	report.Heap = after.HeapAlloc
	report.GC = after.NumGC - before.NumGC
	report.Allocated = after.TotalAlloc - before.TotalAlloc
	// End traffic observations before allocating and decoding diagnostic reports.
	if timings != nil {
		if last, snapshotErr := timings.Snapshot(stop); snapshotErr == nil && timingsReady {
			report.Network = requesttiming.HTTPMetrics{Requests: last.HTTP.Requests - first.HTTP.Requests, Received: last.HTTP.Received - first.HTTP.Received, Sent: last.HTTP.Sent - first.HTTP.Sent, Errors: last.HTTP.Errors - first.HTTP.Errors, WriteErrors: last.HTTP.WriteErrors - first.HTTP.WriteErrors}
			report.RAM = &RAMReport{Hits: last.RAM.Hits - first.RAM.Hits, Misses: last.RAM.Misses - first.RAM.Misses, MissReasons: make(map[string]uint64)}
			for index, reason := range requesttiming.RAMMissReasons() {
				if count := last.RAM.Reasons[index] - first.RAM.Reasons[index]; count != 0 {
					report.RAM.MissReasons[reason] = count
				}
			}
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
				report.Stages = append(report.Stages, Stage{Name: current.Stage, Count: count, MeanMS: float64(current.TotalNS-old.TotalNS) / float64(count) / 1e6, P90MS: float64(delta.PercentileUpperBound(90)) / 1e6, P95MS: float64(delta.PercentileUpperBound(95)) / 1e6, P99MS: float64(delta.PercentileUpperBound(99)) / 1e6})
			}
			if first.Incomplete || last.Incomplete {
				report.Truncated = true
			}
		} else {
			report.Truncated = true
		}
	}
	if cache != nil {
		stats, statsErr := cache.RAMStats(stop)
		if statsErr != nil {
			report.Truncated = true
		} else {
			if report.RAM == nil {
				report.RAM = &RAMReport{}
			}
			report.RAM.RAMCacheStats = stats
		}
	}
	var heap bytes.Buffer
	heapErr := pprof.Lookup("heap").WriteTo(&limitedWriter{target: &heap, remaining: profileLimit}, 0)
	report.Goroutines, err = captureGoroutineStates(64 << 10)
	if err != nil {
		report.Truncated = true
	}
	parsed, parseErr := parseProfile(raw)
	if parseErr != nil {
		report.Error = parseErr.Error()
		return
	}
	var cpuTruncated bool
	report.Functions, report.Frames, report.Calls, report.CPU, cpuTruncated = aggregate(parsed, "cpu", 1200)
	report.Groups = functionGroups(report.Functions)
	report.Truncated = report.Truncated || cpuTruncated
	report.Code = sampledCode(parsed, "cpu")
	if heapErr == nil {
		if parsedHeap, decodeErr := profile.ParseData(heap.Bytes()); decodeErr == nil {
			report.Memory, _, _, _, _ = aggregate(parsedHeap, "inuse_space", 0)
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
}

// Cumulative function costs count recursive occurrences once per sample; the flame tree retains each call path.
func aggregate(parsed *profile.Profile, kind string, maxFrames int) ([]Function, []Frame, []Call, int64, bool) {
	sampleIndex := -1
	for index, sampleType := range parsed.SampleType {
		if sampleType.Type == kind {
			sampleIndex = index
			break
		}
	}
	if sampleIndex < 0 {
		return nil, nil, nil, 0, false
	}
	functions := make(map[string]*Function)
	calls := make(map[[2]int]*Call)
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
		seenCalls := make(map[[2]int]bool)
		var caller *Function
		observations := int64(1)
		for observationIndex, observationType := range parsed.SampleType {
			if observationType.Type == "samples" && observationIndex < len(sample.Value) {
				observations = sample.Value[observationIndex]
				break
			}
		}
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
					function = &Function{ID: len(functions) + 1, Name: name, File: line.Function.Filename, Line: line.Line, Kind: functionKind(name)}
					functions[key] = function
				}
				if !seen[key] {
					function.Total += weight
					function.Samples += observations
					seen[key] = true
				}
				// Deduplicate recursive edges per sample, just as inclusive function costs are deduplicated.
				if maxFrames > 0 && caller != nil {
					pair := [2]int{caller.ID, function.ID}
					if !seenCalls[pair] {
						call := calls[pair]
						if call == nil && len(calls) < 8192 {
							call = &Call{Caller: caller.ID, Callee: function.ID}
							calls[pair] = call
						}
						if call != nil {
							call.Total += weight
							call.Samples += observations
						} else {
							truncated = true
						}
						seenCalls[pair] = true
					}
				}
				caller = function
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
	relationships := make([]Call, 0, len(calls))
	for _, call := range calls {
		relationships = append(relationships, *call)
	}
	sort.Slice(relationships, func(left, right int) bool {
		if relationships[left].Caller == relationships[right].Caller {
			return relationships[left].Callee < relationships[right].Callee
		}
		return relationships[left].Caller < relationships[right].Caller
	})
	return ranked, frames, relationships, total, truncated
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

// Sample locations identify executing code, rather than attributing a function's entire cost to its entry line.
func sampledCode(parsed *profile.Profile, kind string) []Function {
	index := -1
	for sampleIndex, sampleType := range parsed.SampleType {
		if sampleType.Type == kind {
			index = sampleIndex
			break
		}
	}
	if index < 0 {
		return nil
	}
	costs := make(map[string]*Function)
	for _, sample := range parsed.Sample {
		if len(sample.Location) == 0 || index >= len(sample.Value) || sample.Value[index] <= 0 {
			continue
		}
		location := sample.Location[0]
		if len(location.Line) == 0 || location.Line[0].Function == nil {
			continue
		}
		line := location.Line[0]
		function := line.Function
		key := fmt.Sprintf("%s:%d:%s", function.Filename, line.Line, function.Name)
		cost := costs[key]
		if cost == nil {
			cost = &Function{Name: function.Name, File: function.Filename, Line: line.Line, Kind: functionKind(function.Name)}
			costs[key] = cost
		}
		cost.Self += sample.Value[index]
		cost.Total = cost.Self
	}
	ranked := make([]Function, 0, len(costs))
	for _, cost := range costs {
		ranked = append(ranked, *cost)
	}
	sort.Slice(ranked, func(left, right int) bool { return ranked[left].Self > ranked[right].Self })
	return ranked
}
