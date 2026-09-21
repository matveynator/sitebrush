package analytics

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Storage operations exchange compact checkpoints, never raw request rows.
type Operation uint8

const (
	LoadHistory Operation = iota
	ReadBrowserReport
	ReadTechnicalReport
	SaveBrowser
	SaveTechnical
)

type StorageRequest struct {
	Operation Operation
	Domain    string
	State     string
	Report    string
	Archive   string
	Limit     int64
	Stop      <-chan struct{}
	reply     chan StorageResult
	abandoned <-chan struct{}
}
type StorageResult struct {
	Text string
	Err  error
}
type Repository interface {
	Exchange(StorageRequest) StorageResult
}

type Store struct {
	queues [2]chan StorageRequest
	stop   chan struct{}
	done   [2]chan struct{}
}

const databaseLimitBytes int64 = 16 << 20
const archiveLimitBytes int64 = 32 << 20
const archiveLimitDays = 90

// Each shard owns its connections and serializes all operations for a domain.
// Opening per operation also releases file handles between saves and deletions.
func OpenStore(root string) *Store {
	store := &Store{stop: make(chan struct{})}
	for index := range store.queues {
		store.queues[index] = make(chan StorageRequest, 2)
		store.done[index] = make(chan struct{})
		go store.run(root, store.queues[index], store.done[index])
	}
	return store
}

func (store *Store) Close() {
	close(store.stop)
	for _, done := range store.done {
		<-done
	}
}

func (store *Store) Exchange(request StorageRequest) StorageResult {
	request.reply = make(chan StorageResult, 1)
	abandoned := make(chan struct{})
	defer close(abandoned)
	request.abandoned = abandoned
	shard := 0
	for _, character := range request.Domain {
		shard = (shard + int(character)) % len(store.queues)
	}
	select {
	case <-request.Stop:
		return StorageResult{Err: errors.New("analytics request canceled")}
	case <-store.stop:
		return StorageResult{Err: errors.New("analytics store closed")}
	case store.queues[shard] <- request:
	default:
		return StorageResult{Err: errors.New("analytics storage queue full")}
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case result := <-request.reply:
		return result
	case <-request.Stop:
		return StorageResult{Err: errors.New("analytics request canceled")}
	case <-store.stop:
		return StorageResult{Err: errors.New("analytics store closed")}
	case <-timer.C:
		return StorageResult{Err: errors.New("analytics storage deadline exceeded")}
	}
}

func (store *Store) run(root string, requests <-chan StorageRequest, done chan<- struct{}) {
	defer close(done)
	archivedHour := map[string]string{}
	for {
		select {
		case <-store.stop:
			return
		case request := <-requests:
			select {
			case <-request.abandoned:
				continue
			case <-request.Stop:
				continue
			default:
			}
			result := store.execute(root, request, archivedHour)
			select {
			case request.reply <- result:
			case <-store.stop:
				return
			case <-request.abandoned:
			}
		}
	}
}

func storeDirectory(root, domain string) (string, error) {
	if domain == "" || len(domain) > 253 || strings.ContainsAny(domain, "\x00\r\n") {
		return "", errors.New("invalid analytics domain")
	}
	directory := filepath.Join(root, "site-"+url.PathEscape(domain))
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("unsafe analytics directory")
	}
	return directory, nil
}

func (store *Store) execute(root string, request StorageRequest, archivedHour map[string]string) StorageResult {
	directory, err := storeDirectory(root, request.Domain)
	if err != nil {
		return StorageResult{Err: err}
	}
	databasePath := filepath.Join(directory, "analytics.db")
	if info, err := os.Lstat(databasePath); err == nil && !info.Mode().IsRegular() {
		return StorageResult{Err: errors.New("unsafe analytics database")}
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return StorageResult{Err: err}
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	boundary, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	finished := make(chan struct{})
	defer close(finished)
	defer cancel()
	go func() {
		select {
		case <-store.stop:
			cancel()
		case <-request.Stop:
			cancel()
		case <-request.abandoned:
			cancel()
		case <-finished:
		}
	}()
	// SQLite-specific resource limits are confined to this storage boundary.
	for _, statement := range []string{`PRAGMA page_size=4096`, `PRAGMA auto_vacuum=INCREMENTAL`, `PRAGMA journal_mode=DELETE`, `PRAGMA busy_timeout=100`, `PRAGMA max_page_count=4096`, `CREATE TABLE IF NOT EXISTS checkpoints(kind TEXT PRIMARY KEY,body TEXT NOT NULL)`} {
		if _, err := database.ExecContext(boundary, statement); err != nil {
			return StorageResult{Err: err}
		}
	}
	kind := "browser-state"
	switch request.Operation {
	case ReadBrowserReport:
		kind = "browser-report"
	case ReadTechnicalReport:
		kind = "technical-report"
	}
	switch request.Operation {
	case LoadHistory, ReadBrowserReport, ReadTechnicalReport:
		var size int64
		if err := database.QueryRowContext(boundary, `SELECT LENGTH(body) FROM checkpoints WHERE kind=?`, kind).Scan(&size); err != nil {
			return StorageResult{Err: err}
		}
		maximum := request.Limit
		if maximum <= 0 {
			maximum = 4 << 20
		}
		if size > maximum {
			return StorageResult{Err: errors.New("analytics checkpoint exceeds read budget")}
		}
		var body string
		err := database.QueryRowContext(boundary, `SELECT body FROM checkpoints WHERE kind=?`, kind).Scan(&body)
		return StorageResult{Text: body, Err: err}
	case SaveTechnical:
		if len(request.Report) > 4<<20 {
			return StorageResult{Err: errors.New("analytics report exceeds storage budget")}
		}
		return StorageResult{Err: writeCheckpoint(boundary, database, "technical-report", request.Report)}
	case SaveBrowser:
		if len(request.State) > 8<<20 || len(request.Report) > 4<<20 {
			return StorageResult{Err: errors.New("analytics snapshot exceeds storage budget")}
		}
		transaction, err := database.BeginTx(boundary, nil)
		if err != nil {
			return StorageResult{Err: err}
		}
		if err = writeCheckpoint(boundary, transaction, "browser-state", request.State); err == nil {
			err = writeCheckpoint(boundary, transaction, "browser-report", request.Report)
		}
		if err != nil {
			_ = transaction.Rollback()
			return StorageResult{Err: err}
		}
		if err = transaction.Commit(); err != nil {
			return StorageResult{Err: err}
		}
		hour := time.Now().UTC().Format("2006-01-02T15")
		if request.Archive != "" && archivedHour[request.Domain] != hour {
			if err := writeDailyArchive(directory, request.Archive, time.Now().UTC()); err != nil {
				return StorageResult{Err: err}
			}
			if len(archivedHour) >= 4096 {
				clear(archivedHour)
			}
			archivedHour[request.Domain] = hour
			_, err = database.ExecContext(boundary, `PRAGMA incremental_vacuum(128)`)
			if err != nil {
				return StorageResult{Err: err}
			}
		}
		return StorageResult{}
	default:
		return StorageResult{Err: errors.New("unknown analytics operation")}
	}
}

type checkpointWriter interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func writeCheckpoint(ctx context.Context, writer checkpointWriter, kind, body string) error {
	var existing string
	err := writer.QueryRowContext(ctx, `SELECT kind FROM checkpoints WHERE kind=?`, kind).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = writer.ExecContext(ctx, `INSERT INTO checkpoints(kind,body) VALUES(?,?)`, kind, body)
	} else if err == nil {
		_, err = writer.ExecContext(ctx, `UPDATE checkpoints SET body=? WHERE kind=?`, body, kind)
	}
	return err
}

// Archives contain daily report summaries only. Identity state is never copied
// into archives, and deleting a database never triggers automatic restoration.
func writeDailyArchive(directory, encoded string, now time.Time) error {
	summaries := map[string]Report{}
	if err := json.Unmarshal([]byte(encoded), &summaries); err != nil {
		return err
	}
	archiveDirectory := filepath.Join(directory, "archives")
	if err := os.MkdirAll(archiveDirectory, 0700); err != nil {
		return err
	}
	for date, report := range summaries {
		parsed, err := time.Parse("2006-01-02", date)
		if err != nil || date != parsed.Format("2006-01-02") || parsed.After(now) || parsed.Before(dayStart(now).AddDate(0, 0, -archiveLimitDays+1)) {
			continue
		}
		destination := filepath.Join(archiveDirectory, date+".json.gz")
		if parsed.Before(dayStart(now)) {
			if previous, err := os.Open(destination); err == nil {
				archived, decodeErr := DecodeArchive(previous)
				_ = previous.Close()
				if decodeErr == nil && !archived.PeriodEnd.Before(report.PeriodEnd) {
					continue
				}
			}
		}
		temporary, err := os.CreateTemp(archiveDirectory, ".summary-*")
		if err != nil {
			return err
		}
		compressor := gzip.NewWriter(temporary)
		encodeErr := json.NewEncoder(compressor).Encode(report)
		closeErr := compressor.Close()
		fileErr := temporary.Close()
		if err := errors.Join(encodeErr, closeErr, fileErr); err != nil {
			_ = os.Remove(temporary.Name())
			return err
		}
		if err := os.Rename(temporary.Name(), destination); err != nil {
			_ = os.Remove(temporary.Name())
			return err
		}
	}
	return pruneArchives(archiveDirectory, now, archiveLimitBytes)
}

func pruneArchives(directory string, now time.Time, limit int64) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	names := []string{}
	sizes := map[string]int64{}
	var total int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".summary-") {
			_ = os.Remove(filepath.Join(directory, name))
			continue
		}
		if !strings.HasSuffix(name, ".json.gz") {
			continue
		}
		date := strings.TrimSuffix(name, ".json.gz")
		parsed, err := time.Parse("2006-01-02", date)
		if err != nil {
			continue
		}
		if parsed.Before(dayStart(now).AddDate(0, 0, -archiveLimitDays+1)) {
			if err := os.Remove(filepath.Join(directory, name)); err != nil {
				return err
			}
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		sizes[name] = info.Size()
		names = append(names, name)
	}
	sort.Strings(names)
	for len(names) > archiveLimitDays || total > limit {
		if len(names) == 0 {
			break
		}
		name := names[0]
		names = names[1:]
		if err := os.Remove(filepath.Join(directory, name)); err != nil {
			return err
		}
		total -= sizes[name]
	}
	return nil
}

// DecodeArchive allows manual inspection without opening the analytics database.
func DecodeArchive(reader io.Reader) (Report, error) {
	compressor, err := gzip.NewReader(reader)
	if err != nil {
		return Report{}, err
	}
	defer compressor.Close()
	var report Report
	err = json.NewDecoder(io.LimitReader(compressor, 4<<20)).Decode(&report)
	if err != nil {
		return report, fmt.Errorf("read analytics archive: %w", err)
	}
	return report, nil
}
