// Package applog owns one installation journal across service processes.
package applog

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	activeName   = "sitebrush.log"
	archiveDays  = 30
	pipeVariable = "SITEBRUSH_LOG_PIPE"
)

type writeRequest struct {
	message []byte
	reply   chan error
}

type Journal struct {
	writes chan writeRequest
	stop   chan chan error
	done   chan struct{}
}

// Open starts the installation journal under storage/logs.
// Foreground commands should not call this unless file output was selected.
func Open(storagePath string) (*Journal, error) {
	root, err := os.OpenRoot(storagePath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := root.MkdirAll(filepath.Join("storage", "logs"), 0o700); err != nil {
		return nil, err
	}
	if err := root.Chmod(filepath.Join("storage", "logs"), 0o700); err != nil {
		return nil, err
	}
	directory, err := root.OpenRoot(filepath.Join("storage", "logs"))
	if err != nil {
		return nil, err
	}
	return openJournal(directory, activeName, storagePath)
}

// OpenFile writes to the selected absolute path without changing the
// permissions of an administrator-owned directory such as /var/log.
func OpenFile(logFile string) (*Journal, error) {
	if !filepath.IsAbs(logFile) {
		return nil, errors.New("logfile must be an absolute path")
	}
	filename := filepath.Base(logFile)
	if len(filename) <= len(".log") || !strings.HasSuffix(filename, ".log") {
		return nil, errors.New("logfile must have a .log filename")
	}
	directory, err := os.OpenRoot(filepath.Dir(logFile))
	if err != nil {
		return nil, err
	}
	return openJournal(directory, filename, "")
}

func openJournal(directory *os.Root, filename, storagePath string) (*Journal, error) {
	// Archive work owns its own directory handle.
	archiveDirectory, err := directory.OpenRoot(".")
	if err != nil {
		directory.Close()
		return nil, err
	}
	active, err := directory.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		directory.Close()
		archiveDirectory.Close()
		return nil, err
	}
	if err := active.Chmod(0o600); err != nil {
		active.Close()
		directory.Close()
		archiveDirectory.Close()
		return nil, err
	}
	initial, err := active.Stat()
	if err != nil {
		active.Close()
		directory.Close()
		archiveDirectory.Close()
		return nil, err
	}
	journal := &Journal{writes: make(chan writeRequest), stop: make(chan chan error), done: make(chan struct{})}
	go journal.run(storagePath, filename, directory, archiveDirectory, active, initial)
	return journal, nil
}

func (j *Journal) Write(message []byte) (int, error) {
	if j == nil {
		return 0, errors.New("application journal is unavailable")
	}
	copyOfMessage := append([]byte(nil), message...)
	reply := make(chan error, 1)
	select {
	case j.writes <- writeRequest{message: copyOfMessage, reply: reply}:
	case <-j.done:
		return 0, errors.New("application journal has closed")
	}
	if err := <-reply; err != nil {
		return 0, err
	}
	return len(message), nil
}

func (j *Journal) Close() error {
	if j == nil {
		return nil
	}
	reply := make(chan error, 1)
	select {
	case j.stop <- reply:
		return <-reply
	case <-j.done:
		return nil
	}
}

func (j *Journal) run(storagePath, filename string, directory, archiveDirectory *os.Root, active *os.File, initial os.FileInfo) {
	defer close(j.done)
	defer directory.Close()
	defer archiveDirectory.Close()
	day := initial.ModTime().UTC().Format("2006-01-02")
	if initial.Size() == 0 {
		day = time.Now().UTC().Format("2006-01-02")
	}
	archives := make(chan string, 8)
	archiveDone := make(chan error, 1)
	pending := pendingSegments(archiveDirectory, filename)
	go func() {
		var archiveError error
		if err := pruneArchives(archiveDirectory, filename, time.Now().UTC()); err != nil {
			archiveError = errors.Join(archiveError, err)
		}
		if err := pruneLegacyIfConfigured(storagePath); err != nil {
			archiveError = errors.Join(archiveError, err)
		}
		for _, name := range pending {
			if err := compressSegment(archiveDirectory, filename, name); err != nil {
				log.Printf("application journal archive %s failed: %v", name, err)
				archiveError = errors.Join(archiveError, err)
			}
		}
		if err := pruneArchives(archiveDirectory, filename, time.Now().UTC()); err != nil {
			archiveError = errors.Join(archiveError, err)
		}
		for name := range archives {
			for _, err := range []error{compressSegment(archiveDirectory, filename, name), pruneArchives(archiveDirectory, filename, time.Now().UTC()), pruneLegacyIfConfigured(storagePath)} {
				if err != nil {
					log.Printf("application journal maintenance failed: %v", err)
					archiveError = errors.Join(archiveError, err)
				}
			}
		}
		archiveDone <- archiveError
	}()
	rotate := func(nextDay string) error {
		if day == nextDay {
			return nil
		}
		info, err := active.Stat()
		if err != nil {
			return err
		}
		if info.Size() == 0 {
			day = nextDay
			return nil
		}
		if err := active.Sync(); err != nil {
			return err
		}
		if err := active.Close(); err != nil {
			return err
		}
		active = nil
		name, err := nextSegmentName(directory, filename, day)
		if err == nil {
			err = directory.Rename(filename, name)
		}
		if err != nil {
			active, _ = directory.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			return err
		}
		active, err = directory.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		day = nextDay
		archives <- name
		return nil
	}
	if err := rotate(time.Now().UTC().Format("2006-01-02")); err != nil {
		fmt.Fprintf(os.Stderr, "application journal startup rotation failed: %v\n", err)
	}
	timer := time.NewTimer(time.Until(nextUTCMidnight(time.Now().UTC())))
	defer timer.Stop()
	for {
		select {
		case request := <-j.writes:
			if active == nil {
				request.reply <- errors.New("application journal file is unavailable")
				continue
			}
			now := time.Now().UTC()
			if err := rotate(now.Format("2006-01-02")); err != nil {
				request.reply <- err
				continue
			}
			written, err := active.Write(request.message)
			if err == nil && written != len(request.message) {
				err = io.ErrShortWrite
			}
			request.reply <- err
		case <-timer.C:
			if active != nil {
				if err := rotate(time.Now().UTC().Format("2006-01-02")); err != nil {
					fmt.Fprintf(os.Stderr, "application journal daily rotation failed: %v\n", err)
				}
			}
			timer.Reset(time.Until(nextUTCMidnight(time.Now().UTC())))
		case reply := <-j.stop:
			close(archives)
			archiveErr := <-archiveDone
			var syncErr, closeErr error
			if active != nil {
				syncErr = active.Sync()
				closeErr = active.Close()
			}
			reply <- errors.Join(syncErr, closeErr, archiveErr)
			return
		}
	}
}

func nextUTCMidnight(now time.Time) time.Time {
	utc := now.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day()+1, 0, 0, 0, 0, time.UTC)
}

func nextSegmentName(directory *os.Root, filename, day string) (string, error) {
	for sequence := 1; sequence < 1000000; sequence++ {
		name := fmt.Sprintf("%s-%s-%06d.log", strings.TrimSuffix(filename, ".log"), day, sequence)
		if _, err := directory.Lstat(name); errors.Is(err, os.ErrNotExist) {
			if _, err := directory.Lstat(name + ".gz"); errors.Is(err, os.ErrNotExist) {
				return name, nil
			} else if err != nil {
				return "", err
			}
		} else if err != nil {
			return "", err
		}
	}
	return "", errors.New("application journal segment numbers exhausted")
}

func readRootDir(root *os.Root) ([]os.DirEntry, error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	return directory.ReadDir(-1)
}

func pendingSegments(directory *os.Root, filename string) []string {
	entries, err := readRootDir(directory)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && segmentName(entry.Name(), ".log", filename) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

func segmentName(name, suffix, filename string) bool {
	prefix := strings.TrimSuffix(filename, ".log") + "-"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return false
	}
	base := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	if len(base) != len("2006-01-02-000001") || base[10] != '-' {
		return false
	}
	if _, err := time.Parse("2006-01-02", base[:10]); err != nil {
		return false
	}
	_, err := strconv.Atoi(base[11:])
	return err == nil
}

func compressSegment(directory *os.Root, filename, name string) error {
	if !segmentName(name, ".log", filename) {
		return errors.New("invalid application journal segment")
	}
	input, err := directory.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	outputName := name + ".gz"
	temporaryName := outputName + ".tmp"
	output, err := directory.OpenFile(temporaryName, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		input.Close()
		return err
	}
	compressor := gzip.NewWriter(output)
	_, copyErr := io.Copy(compressor, input)
	inputErr := input.Close()
	zipErr := compressor.Close()
	syncErr := output.Sync()
	closeErr := output.Close()
	if err := errors.Join(copyErr, inputErr, zipErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := directory.Rename(temporaryName, outputName); err != nil {
		return err
	}
	return directory.Remove(name)
}

func pruneArchives(directory *os.Root, filename string, now time.Time) error {
	entries, err := readRootDir(directory)
	if err != nil {
		return err
	}
	utc := now.UTC()
	cutoff := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -archiveDays)
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !segmentName(entry.Name(), ".log.gz", filename) {
			continue
		}
		prefix := strings.TrimSuffix(filename, ".log") + "-"
		day, err := time.Parse("2006-01-02", entry.Name()[len(prefix):len(prefix)+len("2006-01-02")])
		if err != nil || !day.Before(cutoff) {
			continue
		}
		if err := directory.Remove(entry.Name()); err != nil {
			return err
		}
	}
	return nil
}

func pruneLegacyIfConfigured(storagePath string) error {
	if storagePath == "" {
		return nil
	}
	return PruneLegacy(storagePath, time.Now().UTC())
}

// Legacy site and problem logs remain readable until their dated retention
// expires. No new request or diagnostic event is written to these directories.
func PruneLegacy(storagePath string, now time.Time) error {
	root, err := os.OpenRoot(storagePath)
	if err != nil {
		return err
	}
	defer root.Close()
	directory, err := root.OpenRoot(filepath.Join("storage", "logs"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer directory.Close()
	entries, err := readRootDir(directory)
	if err != nil {
		return err
	}
	utc := now.UTC()
	cutoff := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -archiveDays)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		legacy, err := directory.OpenRoot(entry.Name())
		if err != nil {
			continue
		}
		files, err := readRootDir(legacy)
		if err != nil {
			legacy.Close()
			continue
		}
		for _, file := range files {
			if !file.Type().IsRegular() || !legacyDateName(file.Name()) {
				continue
			}
			day, parseErr := time.Parse("2006-01-02", strings.TrimSuffix(file.Name(), ".log"))
			if parseErr == nil && day.Before(cutoff) {
				if err := legacy.Remove(file.Name()); err != nil {
					legacy.Close()
					return err
				}
			}
		}
		remaining, err := readRootDir(legacy)
		legacy.Close()
		if err == nil && len(remaining) == 0 {
			if err := directory.Remove(entry.Name()); err != nil {
				return err
			}
		}
	}
	return nil
}

func legacyDateName(name string) bool {
	if len(name) != len("2006-01-02.log") || !strings.HasSuffix(name, ".log") {
		return false
	}
	_, err := time.Parse("2006-01-02", strings.TrimSuffix(name, ".log"))
	return err == nil
}

// Capture redirects the service process output to the installation journal.
func Capture(storagePath string) (func() error, error) {
	if os.Getenv(pipeVariable) == "1" {
		return func() error { return nil }, nil
	}
	journal, err := Open(storagePath)
	if err != nil {
		return nil, err
	}
	return captureJournal(journal)
}

// CaptureFile redirects process output to an explicitly selected rotating file.
func CaptureFile(logFile string) (func() error, error) {
	if os.Getenv(pipeVariable) == "1" {
		return func() error { return nil }, nil
	}
	journal, err := OpenFile(logFile)
	if err != nil {
		return nil, err
	}
	return captureJournal(journal)
}

// A single channel-owned journal serializes stdout, stderr and child output.
func captureJournal(journal *Journal) (func() error, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		journal.Close()
		return nil, err
	}
	previousOutput, previousError := os.Stdout, os.Stderr
	previousLogger := log.Writer()
	previousPipeValue, pipeWasSet := os.LookupEnv(pipeVariable)
	os.Stdout, os.Stderr = writer, writer
	log.SetOutput(writer)
	_ = os.Setenv(pipeVariable, "1")
	finished := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(journalFallback{journal: journal, output: previousError}, reader)
		finished <- copyErr
	}()
	return func() error {
		log.SetOutput(previousLogger)
		os.Stdout, os.Stderr = previousOutput, previousError
		if pipeWasSet {
			_ = os.Setenv(pipeVariable, previousPipeValue)
		} else {
			_ = os.Unsetenv(pipeVariable)
		}
		_ = writer.Close()
		copyErr := <-finished
		closeErr := reader.Close()
		journalErr := journal.Close()
		return errors.Join(copyErr, closeErr, journalErr)
	}, nil
}

type journalFallback struct {
	journal *Journal
	output  io.Writer
}

func (writer journalFallback) Write(message []byte) (int, error) {
	if _, err := writer.journal.Write(message); err != nil {
		_, _ = fmt.Fprintf(writer.output, "application journal write failed: %v\n", err)
		return writer.output.Write(message)
	}
	return len(message), nil
}
