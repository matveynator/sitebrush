package main

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/hostingandsupport"
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

func TestControlReaderLoadsInvoicesAndShutsDownWithoutDeadline(t *testing.T) {
	dispatcher, err := startServerControlDatabaseDispatcher(filepath.Join(t.TempDir(), "control.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	boundary, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	defer dispatcher.Close()
	if err := dispatcher.execute(boundary, serverControlDatabaseWrite, "seed-invoice", func(database *sql.DB) error {
		_, err := (hostingandsupport.Store{DB: database}).CreateInvoice(boundary, hostingandsupport.Invoice{CustomerEmail: "visitor@example.com", Domain: "site.example", Amount: "10.00", Currency: "EUR", Lines: []hostingandsupport.InvoiceLine{{Domain: "site.example", Description: "Hosting", TotalAmountMinor: 1000}}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.execute(boundary, serverControlDatabaseRead, "expenses-snapshot-control-data", func(database *sql.DB) error {
		invoices := (hostingandsupport.Store{DB: database}).Invoices(boundary, 80)
		if len(invoices) != 1 || len(invoices[0].Lines) != 1 {
			return fmt.Errorf("invoice cursor retained the only reader connection: %+v", invoices)
		}
		return boundary.Err()
	}); err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- dispatcher.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("normal shutdown still waits on invoice reader")
	}
}
