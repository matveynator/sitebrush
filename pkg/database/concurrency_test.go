package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func newSQLiteConcurrencyTestDatabase(t *testing.T) (*Database, string) {
	t.Helper()

	databasePath := filepath.Join(t.TempDir(), "concurrency.db")
	db, err := NewDatabase(Config{DBType: "sqlite", DBPath: databasePath})
	if err != nil {
		t.Fatalf("new sqlite database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.DB.Close(); err != nil {
			t.Errorf("close sqlite database: %v", err)
		}
	})

	if err := db.InitSchema(Config{DBType: "sqlite"}, func(string, ...any) {}); err != nil {
		t.Fatalf("init sqlite schema: %v", err)
	}
	return db, databasePath
}

func TestSQLiteConcurrentRealtimeWritersRemainAvailable(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)

	const (
		writerCount    = 24
		writesPerWriter = 20
	)

	start := make(chan struct{})
	results := make(chan error, writerCount)

	for writer := 0; writer < writerCount; writer++ {
		writer := writer
		go func() {
			<-start
			for write := 0; write < writesPerWriter; write++ {
				measurement := RealtimeMeasurement{
					DeviceID:   fmt.Sprintf("writer-%02d", writer),
					Transport:  "test",
					DeviceName: "concurrency",
					Value:      float64(write + 1),
					Unit:       "cpm",
					Lat:        55.75,
					Lon:        37.61,
					MeasuredAt: int64(writer*1000 + write + 1),
					FetchedAt:  int64(writer*1000 + write + 1),
				}
				if err := db.InsertRealtimeMeasurement(measurement, "sqlite"); err != nil {
					results <- err
					return
				}
			}
			results <- nil
		}()
	}

	close(start)

	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()

	for writer := 0; writer < writerCount; writer++ {
		select {
		case err := <-results:
			if err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "locked") {
					t.Fatalf("concurrent writer hit database lock: %v", err)
				}
				t.Fatalf("concurrent writer failed: %v", err)
			}
		case <-timeout.C:
			t.Fatal("concurrent writers did not finish; database may be blocked")
		}
	}

	var count int
	if err := db.withSerializedConnectionFor(context.Background(), WorkloadWebRead, func(ctx context.Context, conn *sql.DB) error {
		return conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM realtime_measurements").Scan(&count)
	}); err != nil {
		t.Fatalf("count realtime rows after concurrent writes: %v", err)
	}

	want := writerCount * writesPerWriter
	if count != want {
		t.Fatalf("realtime row count = %d, want %d", count, want)
	}
}

func TestSerializedPipelineErrorDoesNotPoisonFollowingJobs(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)

	sentinel := errors.New("expected serialized failure")
	err := db.withSerializedConnectionFor(context.Background(), WorkloadUserUpload, func(context.Context, *sql.DB) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("serialized failure = %v, want %v", err, sentinel)
	}

	measurement := RealtimeMeasurement{
		DeviceID:   "after-error",
		Transport:  "test",
		DeviceName: "recovery",
		Value:      1,
		Unit:       "cpm",
		Lat:        55.75,
		Lon:        37.61,
		MeasuredAt: 1,
		FetchedAt:  1,
	}
	if err := db.InsertRealtimeMeasurement(measurement, "sqlite"); err != nil {
		t.Fatalf("write after serialized failure: %v", err)
	}

	var count int
	if err := db.withSerializedConnectionFor(context.Background(), WorkloadWebRead, func(ctx context.Context, conn *sql.DB) error {
		return conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM realtime_measurements WHERE device_id = ?", measurement.DeviceID).Scan(&count)
	}); err != nil {
		t.Fatalf("read after serialized failure: %v", err)
	}
	if count != 1 {
		t.Fatalf("rows after serialized failure = %d, want 1", count)
	}
}

func TestSerializedPipelineQueuesSecondWriterUntilFirstFinishes(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)

	go func() {
		firstDone <- db.withSerializedConnectionFor(context.Background(), WorkloadArchive, func(ctx context.Context, conn *sql.DB) error {
			close(firstStarted)
			<-releaseFirst
			_, err := conn.ExecContext(ctx, "INSERT INTO maintenance_state(task,status,updated_at,message) VALUES(?,?,?,?)", "first", "done", 1, "")
			return err
		})
	}()

	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first serialized writer did not start")
	}

	go func() {
		secondDone <- db.withSerializedConnectionFor(context.Background(), WorkloadRealtime, func(ctx context.Context, conn *sql.DB) error {
			_, err := conn.ExecContext(ctx, "INSERT INTO maintenance_state(task,status,updated_at,message) VALUES(?,?,?,?)", "second", "done", 2, "")
			return err
		})
	}()

	select {
	case err := <-secondDone:
		t.Fatalf("second writer bypassed serialization before first completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseFirst)

	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first writer failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first writer did not finish after release")
	}

	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second writer failed after queue release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second writer remained blocked after first completed")
	}
}

func TestSerializedPipelineRunsExactlyOneWriterAtATime(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)

	const writerCount = 16
	entered := make(chan int, writerCount)
	release := make([]chan struct{}, writerCount)
	results := make(chan error, writerCount)

	for writer := 0; writer < writerCount; writer++ {
		release[writer] = make(chan struct{})
		writer := writer
		go func() {
			results <- db.withSerializedConnectionFor(context.Background(), WorkloadUserUpload, func(ctx context.Context, conn *sql.DB) error {
				entered <- writer
				<-release[writer]
				_, err := conn.ExecContext(ctx, "INSERT INTO maintenance_state(task,status,updated_at,message) VALUES(?,?,?,?)",
					fmt.Sprintf("writer-%02d", writer), "done", writer, "")
				return err
			})
		}()
	}

	for completed := 0; completed < writerCount; completed++ {
		var activeWriter int
		select {
		case activeWriter = <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("no serialized writer entered the database")
		}

		select {
		case secondWriter := <-entered:
			t.Fatalf("writers %d and %d entered the database concurrently", activeWriter, secondWriter)
		case <-time.After(50 * time.Millisecond):
		}

		close(release[activeWriter])

		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("serialized writer failed: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("serialized writer did not finish after release")
		}
	}
}

func TestSQLiteSerializedWriterRecoversAfterPinnedConnectionRelease(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)

	conn, err := db.DB.Conn(context.Background())
	if err != nil {
		t.Fatalf("pin sqlite connection: %v", err)
	}

	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		_ = conn.Close()
		t.Fatalf("begin pinned sqlite transaction: %v", err)
	}

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- db.InsertRealtimeMeasurement(RealtimeMeasurement{
			DeviceID:   "blocked-writer",
			Transport:  "test",
			DeviceName: "lock-recovery",
			Value:      1,
			Unit:       "cpm",
			Lat:        55.75,
			Lon:        37.61,
			MeasuredAt: 1,
			FetchedAt:  1,
		}, "sqlite")
	}()

	select {
	case err := <-writeDone:
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		_ = conn.Close()
		t.Fatalf("serialized writer bypassed the pinned single connection: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	if _, err := conn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
		_ = conn.Close()
		t.Fatalf("release pinned sqlite transaction: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("release pinned sqlite connection: %v", err)
	}

	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("writer did not recover after pinned connection release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("writer remained blocked after the only sqlite connection was released")
	}

	var count int
	if err := db.withSerializedConnectionFor(context.Background(), WorkloadWebRead, func(ctx context.Context, runConn *sql.DB) error {
		return runConn.QueryRowContext(ctx, "SELECT COUNT(*) FROM realtime_measurements WHERE device_id = ?", "blocked-writer").Scan(&count)
	}); err != nil {
		t.Fatalf("database unavailable after pinned connection release: %v", err)
	}
	if count != 1 {
		t.Fatalf("rows after pinned connection release = %d, want 1", count)
	}
}

func TestBackgroundIndexBuildAndRealtimeWritesDoNotDeadlock(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	indexDone := db.EnsureIndexesAsync(ctx, Config{DBType: "sqlite"}, func(string, ...any) {})
	if indexDone == nil {
		t.Fatal("sqlite index build unexpectedly disabled")
	}

	const writes = 100
	writeDone := make(chan error, 1)
	go func() {
		for i := 0; i < writes; i++ {
			err := db.InsertRealtimeMeasurement(RealtimeMeasurement{
				DeviceID:   "index-writer",
				Transport:  "test",
				DeviceName: "index-concurrency",
				Value:      float64(i + 1),
				Unit:       "cpm",
				Lat:        55.75,
				Lon:        37.61,
				MeasuredAt: int64(i + 1),
				FetchedAt:  int64(i + 1),
			}, "sqlite")
			if err != nil {
				writeDone <- err
				return
			}
		}
		writeDone <- nil
	}()

	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()

	indexFinished := false
	writerFinished := false
	for !indexFinished || !writerFinished {
		select {
		case <-indexDone:
			indexFinished = true
			indexDone = nil
		case err := <-writeDone:
			writerFinished = true
			writeDone = nil
			if err != nil {
				t.Fatalf("realtime write during index build: %v", err)
			}
		case <-timeout.C:
			t.Fatal("background index build and realtime writes deadlocked")
		}
	}
}
