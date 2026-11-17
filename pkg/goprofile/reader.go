package goprofile

import (
	"encoding/json"
	"io"
)

type readTask struct {
	task  task
	state State
}

// File ownership moves to one reader. Closing its session revokes queued reads and releases descriptors.
func ownArtifact(record artifact, stop <-chan struct{}) artifact {
	reader := make(chan readTask, 4)
	session := make(chan struct{})
	done := make(chan struct{})
	owned := record
	record.raw, record.summary = nil, nil
	record.reader, record.session, record.done = reader, session, done
	go func() {
		defer close(done)
		defer closeArtifact(owned)
		var active *readTask
		defer func() {
			unavailable := func(job readTask) {
				job.state.Error = "Recording expired or unavailable"
				select {
				case job.task.reply <- job.state:
				default:
				}
			}
			if active != nil {
				unavailable(*active)
			}
			for {
				select {
				case job := <-reader:
					unavailable(job)
				default:
					return
				}
			}
		}()
		for {
			select {
			case <-stop:
				return
			case <-session:
				return
			case job := <-reader:
				active = &job
				select {
				case <-stop:
					return
				case <-session:
					return
				case <-job.task.abandoned:
					active = nil
					continue
				default:
				}
				report := owned.meta
				if owned.summary != nil {
					if err := json.NewDecoder(io.NewSectionReader(owned.summary, 0, owned.size)).Decode(&report); err != nil {
						job.state.Error = err.Error()
					}
				}
				select {
				case <-stop:
					return
				case <-session:
					return
				case <-job.task.abandoned:
					active = nil
					continue
				default:
				}
				if job.state.Error == "" {
					if job.task.command.Action == "export" {
						job.state.Text = exportText(report)
					} else {
						page := reportPage(report, job.task.command, &job.state.More)
						job.state.Report = &page
					}
				}
				select {
				case job.task.reply <- job.state:
					active = nil
				case <-job.task.abandoned:
				case <-stop:
					return
				case <-session:
					return
				}
			}
		}
	}()
	return record
}
