package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestControlDatabaseShutdownBoundsAnUnfinishedOperation(t *testing.T) {
	dispatcher, err := startServerControlDatabaseDispatcher(filepath.Join(t.TempDir(), "control.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	replied := make(chan error, 1)
	go func() {
		replied <- dispatcher.execute(context.Background(), serverControlDatabaseWrite, "unfinished-test-operation", func(database *sql.DB) error {
			if _, err := database.ExecContext(context.Background(), "SELECT 1"); err != nil {
				return err
			}
			close(started)
			<-release
			return nil
		})
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("operation did not start")
	}
	closed := make(chan error, 1)
	go func() { closed <- dispatcher.closeWithin(20 * time.Millisecond) }()
	select {
	case err := <-closed:
		if err == nil || !strings.Contains(err.Error(), "writer") {
			t.Fatalf("shutdown did not report the unfinished worker: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown hung on an unfinished operation")
	}
	select {
	case err := <-replied:
		if err == nil {
			t.Fatal("subscriber did not receive termination")
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber still waiting")
	}
	if err := dispatcher.execute(context.Background(), serverControlDatabaseRead, "after-close", func(*sql.DB) error { t.Fatal("stopped dispatcher ran new work"); return nil }); err == nil {
		t.Fatal("closed dispatcher accepted work")
	}
	close(release)
	for range serverControlDatabaseReaderCount + 1 {
		select {
		case worker := <-dispatcher.workersDone:
			if worker == "writer" {
				return
			}
		case <-time.After(5 * time.Second):
			t.Fatal("released writer did not close")
		}
	}
	t.Fatal("released writer was not reported")
}

func TestControlDatabaseShutdownCompletesWithIdleWorkers(t *testing.T) {
	dispatcher, err := startServerControlDatabaseDispatcher(filepath.Join(t.TempDir(), "control.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Close(); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Close(); err != nil {
		t.Fatal(err)
	}
}
