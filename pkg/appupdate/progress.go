package appupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const operationLogLimit = 256 << 10

// Progress belongs to the operation worker. Published snapshots never share a
// writable event slice with the supervisor or a network subscriber.
type OperationProgress struct {
	Action        string           `json:"action,omitempty"`
	Stage         string           `json:"stage,omitempty"`
	FailedStage   string           `json:"failed_stage,omitempty"`
	FromVersion   string           `json:"from_version,omitempty"`
	TargetVersion string           `json:"target_version,omitempty"`
	StartedAt     time.Time        `json:"started_at,omitempty"`
	StageAt       time.Time        `json:"stage_at,omitempty"`
	FinishedAt    time.Time        `json:"finished_at,omitempty"`
	Work          WorkProgress     `json:"work"`
	Events        []OperationEvent `json:"events,omitempty"`
	Sequence      uint64           `json:"sequence"`
	Truncated     bool             `json:"truncated,omitempty"`
}

type OperationEvent struct {
	Sequence uint64    `json:"sequence"`
	At       time.Time `json:"at"`
	Stage    string    `json:"stage"`
	Code     string    `json:"code"`
	Detail   string    `json:"detail,omitempty"`
}

type WorkProgress struct {
	Bytes      int64   `json:"bytes"`
	TotalBytes int64   `json:"total_bytes"`
	Files      int64   `json:"files"`
	TotalFiles int64   `json:"total_files"`
	Speed      float64 `json:"speed"`
	Code       string  `json:"code,omitempty"`
	Detail     string  `json:"detail,omitempty"`
}

func (progress *OperationProgress) finishStage() {
	detail := ""
	work := progress.Work
	if work.TotalBytes > 0 || work.TotalFiles > 0 {
		detail = fmt.Sprintf("bytes=%d/%d files=%d/%d speed=%.0f B/s", work.Bytes, work.TotalBytes, work.Files, work.TotalFiles, work.Speed)
		if progress.Stage == "inspecting" {
			detail = fmt.Sprintf("backup bytes=%d files=%d", work.TotalBytes, work.TotalFiles)
		}
	}
	progress.event(progress.Stage, "stage_finished", detail)
}

// Both CLI subscribers and the supervisor consume the same durable sequence;
// short stages remain visible even when they finish between status requests.
func WriteOperationEvents(output io.Writer, progress OperationProgress, after uint64) (uint64, error) {
	if output == nil {
		return progress.Sequence, nil
	}
	for _, event := range progress.Events {
		if event.Sequence <= after {
			continue
		}
		detail := strings.Map(func(character rune) rune {
			if character < ' ' || character == 127 {
				return ' '
			}
			return character
		}, event.Detail)
		elapsed := event.At.Sub(progress.StartedAt).Round(time.Second)
		if _, err := fmt.Fprintf(output, "%s UPDATE +%s [%s] %s %s\n", event.At.Format(time.RFC3339), elapsed, event.Stage, event.Code, detail); err != nil {
			return after, err
		}
		after = event.Sequence
	}
	return after, nil
}

func (progress *OperationProgress) event(stage, code, detail string) {
	// Bound individual diagnostics as well as the encoded journal. This keeps
	// the complete status below the frozen local contact payload limit.
	detail = strings.ToValidUTF8(detail, "?")
	if len(detail) > 2048 {
		detail = strings.ToValidUTF8(detail[:2048], "?") + "…"
	}
	progress.Sequence++
	events := append([]OperationEvent(nil), progress.Events...)
	events = append(events, OperationEvent{Sequence: progress.Sequence, At: time.Now().UTC(), Stage: stage, Code: code, Detail: detail})
	for len(events) > 0 {
		encoded, _ := json.Marshal(events)
		if len(encoded) <= operationLogLimit {
			break
		}
		events = events[1:]
		progress.Truncated = true
	}
	progress.Events = events
}

func sendWork(updates chan<- WorkProgress, update WorkProgress, stop <-chan struct{}) error {
	if updates == nil {
		return nil
	}
	select {
	case updates <- update:
		return nil
	case <-stop:
		return errors.New("operation progress session stopped")
	}
}

// A measured task transfers counters through a dedicated session. Cancellation
// joins the task before its files or directory capabilities can be released.
func runMeasured[T any](stop <-chan struct{}, task func(chan<- WorkProgress, <-chan struct{}) (T, error), publish func(WorkProgress) error) (T, error) {
	type result struct {
		output T
		err    error
	}
	updates := make(chan WorkProgress)
	finished := make(chan result, 1)
	session := make(chan struct{})
	defer func() {
		if session != nil {
			close(session)
		}
	}()
	go func() {
		output, err := task(updates, session)
		finished <- result{output: output, err: err}
	}()
	for {
		select {
		case update := <-updates:
			if err := publish(update); err != nil {
				close(session)
				completed := <-finished
				// The session is already closed on this exit path.
				session = nil
				return completed.output, err
			}
		case completed := <-finished:
			return completed.output, completed.err
		case <-stop:
			close(session)
			completed := <-finished
			session = nil
			return completed.output, errors.New("version operation stopped")
		}
	}
}

type progressWriter struct {
	output  io.Writer
	updates chan<- WorkProgress
	stop    <-chan struct{}
	work    WorkProgress
	started time.Time
	last    time.Time
	initial int64
}

func (writer *progressWriter) Write(buffer []byte) (int, error) {
	select {
	case <-writer.stop:
		return 0, errors.New("file transfer consumer stopped")
	default:
	}
	written, err := writer.output.Write(buffer)
	writer.work.Bytes += int64(written)
	if err == nil && time.Since(writer.last) >= time.Second {
		err = writer.report()
	}
	return written, err
}

func (writer *progressWriter) report() error {
	now := time.Now()
	if writer.started.IsZero() {
		writer.started = now
		writer.initial = writer.work.Bytes
	}
	if seconds := now.Sub(writer.started).Seconds(); seconds > 0 {
		writer.work.Speed = float64(writer.work.Bytes-writer.initial) / seconds
	}
	writer.last = now
	return sendWork(writer.updates, writer.work, writer.stop)
}
