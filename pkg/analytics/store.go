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
	ReadSecurity
	SaveSecurity
	ReadConfiguration
	SaveConfiguration
	ReadUsage
	ReadPreferences
	SavePreferences
	InitializeConfiguration
)

type StorageRequest struct {
	Operation  Operation
	Domain     string
	State      string
	Report     string
	Archive    string
	Limit      int64
	Stop       <-chan struct{}
	Quota      chan<- DiskQuotaRequest
	quotaFinal bool
	reply      chan StorageResult
	abandoned  <-chan struct{}
}
type StorageResult struct {
	Text  string
	Err   error
	Bytes int64
}
type Repository interface {
	Exchange(StorageRequest) StorageResult
}

type DiskQuotaRequest struct {
	Final  bool
	Domain string
	Bytes  int64
	Reply  chan error
	Stop   <-chan struct{}
}
type Store struct {
	requests chan StorageRequest
	stop     chan struct{}
	done     chan struct{}
}
type storeSession struct {
	requests chan StorageRequest
	done     chan struct{}
	last     time.Time
}

const databaseLimitBytes int64 = 16 << 20
const archiveLimitBytes int64 = 32 << 20
const archiveLimitDays = 400

// Each site has one session channel and one database worker. The dispatcher
// transfers jobs without opening databases or waiting for another site's SQL.
func OpenStore(root string) *Store {
	store := &Store{requests: make(chan StorageRequest, 32), stop: make(chan struct{}), done: make(chan struct{})}
	go store.route(root)
	return store
}
func (store *Store) Close() { close(store.stop); <-store.done }
func (store *Store) route(root string) {
	defer close(store.done)
	sessions := map[string]*storeSession{}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-store.stop:
			for _, session := range sessions {
				<-session.done
			}
			return
		case now := <-ticker.C:
			for domain, session := range sessions {
				if now.Sub(session.last) > 5*time.Minute {
					close(session.requests)
					<-session.done
					delete(sessions, domain)
				}
			}
		case request := <-store.requests:
			session := sessions[request.Domain]
			if session == nil {
				if len(sessions) >= 128 {
					select {
					case request.reply <- StorageResult{Err: errors.New("analytics site worker limit reached")}:
					default:
					}
					continue
				}
				session = &storeSession{requests: make(chan StorageRequest, 2), done: make(chan struct{}), last: time.Now()}
				sessions[request.Domain] = session
				go store.run(root, session.requests, session.done)
			}
			session.last = time.Now()
			select {
			case session.requests <- request:
			default:
				select {
				case request.reply <- StorageResult{Err: errors.New("analytics site queue full")}:
				default:
				}
			}
		}
	}
}

func (store *Store) Exchange(request StorageRequest) StorageResult {
	request.reply = make(chan StorageResult, 1)
	abandoned := make(chan struct{})
	defer close(abandoned)
	request.abandoned = abandoned
	select {
	case <-request.Stop:
		return StorageResult{Err: errors.New("analytics request canceled")}
	case <-store.stop:
		return StorageResult{Err: errors.New("analytics store closed")}
	case store.requests <- request:
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
	var preferences StorageResult
	preferencesKnown := false
	preferencesReadAt := time.Time{}
	for {
		select {
		case <-store.stop:
			return
		case request, open := <-requests:
			if !open {
				return
			}
			select {
			case <-request.abandoned:
				continue
			case <-request.Stop:
				continue
			default:
			}
			result := StorageResult{}
			cachedPreferences := request.Operation == ReadPreferences && preferencesKnown && time.Since(preferencesReadAt) < time.Minute
			if cachedPreferences {
				directory, pathErr := storeDirectoryPath(root, request.Domain)
				cachedPreferences = pathErr == nil
				if cachedPreferences {
					_, databaseErr := os.Stat(filepath.Join(directory, DatabaseName(request.Domain)))
					cachedPreferences = databaseErr == nil
				}
			}
			if cachedPreferences {
				result = preferences
			} else {
				result = store.execute(root, request, archivedHour)
				if request.Operation == ReadPreferences && (result.Err == nil || errors.Is(result.Err, sql.ErrNoRows)) {
					preferences = result
					preferencesKnown = true
					preferencesReadAt = time.Now()
				}
				if request.Operation == SavePreferences && result.Err == nil {
					preferences = StorageResult{Text: request.State}
					preferencesKnown = true
					preferencesReadAt = time.Now()
				}
			}

			select {
			case request.reply <- result:
			case <-store.stop:
				return
			case <-request.abandoned:
			}
		}
	}
}

func storeDirectoryPath(root, domain string) (string, error) {
	domain = strings.TrimSpace(domain)
	safeDomain := filepath.Base(domain)
	if domain == "" || len(domain) > 253 || strings.ContainsAny(domain, "\x00\r\n/\\") || safeDomain != domain || safeDomain == "." || safeDomain == ".." {
		return "", errors.New("invalid analytics domain")
	}
	directory := filepath.Join(root, "site-"+url.PathEscape(safeDomain))
	relative, err := filepath.Rel(root, directory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", errors.New("unsafe analytics directory")
	}
	return directory, nil
}

func storeDirectory(root, domain string) (string, error) {
	directory, err := storeDirectoryPath(root, domain)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("unsafe analytics directory")
	}
	return directory, nil
}

// ExecuteOwned runs inside the site's analytics participant. The SQL handle and
// archive-hour map never leave that participant's turn.
func ExecuteOwned(directory string, request StorageRequest, database *sql.DB, archivedHour map[string]string) StorageResult {
	return (&Store{}).executeWithDatabase("", request, archivedHour, database, directory)
}

func (store *Store) execute(root string, request StorageRequest, archivedHour map[string]string) StorageResult {
	return store.executeWithDatabase(root, request, archivedHour, nil, "")
}

func (store *Store) executeWithDatabase(root string, request StorageRequest, archivedHour map[string]string, owned *sql.DB, ownedDirectory string) StorageResult {
	directoryForRequest := func(create bool) (string, error) {
		if ownedDirectory != "" {
			return ownedDirectory, nil
		}
		if create {
			return storeDirectory(root, request.Domain)
		}
		return storeDirectoryPath(root, request.Domain)
	}
	if request.Operation == ReadUsage {
		directory, err := directoryForRequest(false)
		if err != nil {
			return StorageResult{Err: err}
		}
		info, err := os.Lstat(directory)
		if os.IsNotExist(err) {
			return StorageResult{}
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return StorageResult{Err: errors.New("unsafe analytics directory")}
		}
		usage, err := directoryBytes(directory)
		return StorageResult{Bytes: usage, Err: err}
	}
	directory, err := directoryForRequest(true)
	if err != nil {
		return StorageResult{Err: err}
	}
	databasePath := filepath.Join(directory, DatabaseName(request.Domain))
	if owned != nil {
		databasePath = filepath.Join(directory, "analytics.db")
	}
	legacyPath := filepath.Join(directory, "analytics.db")
	if _, pathErr := os.Lstat(databasePath); owned == nil && os.IsNotExist(pathErr) {
		if legacy, legacyErr := os.Lstat(legacyPath); legacyErr == nil && legacy.Mode().IsRegular() {
			if renameErr := os.Rename(legacyPath, databasePath); renameErr != nil {
				return StorageResult{Err: renameErr}
			}
		}
	}
	if info, err := os.Lstat(databasePath); err == nil && !info.Mode().IsRegular() {
		return StorageResult{Err: errors.New("unsafe analytics database")}
	}
	_, databaseErr := os.Stat(databasePath)
	mutating := os.IsNotExist(databaseErr) || request.Operation == SaveBrowser || request.Operation == SaveSecurity || request.Operation == SaveConfiguration || request.Operation == InitializeConfiguration || request.Operation == SavePreferences || request.Operation == SaveTechnical
	if request.Quota != nil {
		defer func() {
			if actual, actualErr := directoryBytes(directory); actualErr == nil {
				cleanup := request
				cleanup.Stop = nil
				cleanup.abandoned = nil
				cleanup.quotaFinal = true
				_ = store.diskQuota(cleanup, actual)
			}
		}()
	}
	// Expiry must reclaim space before quota admission, including when the site is full.
	if mutating {
		archiveDirectory := filepath.Join(directory, "archives")
		if archiveInfo, archiveErr := os.Lstat(archiveDirectory); archiveErr == nil {
			if !archiveInfo.IsDir() || archiveInfo.Mode()&os.ModeSymlink != 0 {
				return StorageResult{Err: errors.New("unsafe analytics archive directory")}
			}
			if pruneErr := pruneArchives(archiveDirectory, time.Now().UTC(), archiveLimitBytes); pruneErr != nil {
				return StorageResult{Err: pruneErr}
			}
		} else if !os.IsNotExist(archiveErr) {
			return StorageResult{Err: archiveErr}
		}
	}
	if mutating && request.Quota != nil {
		usage, usageErr := directoryBytes(directory)
		if usageErr != nil {
			return StorageResult{Err: usageErr}
		}
		reservation := usage + int64(len(request.State)+len(request.Report)+len(request.Archive)) + 65536
		if fileInfo, statErr := os.Stat(databasePath); statErr == nil {
			reservation += fileInfo.Size()
		}
		if quotaErr := store.diskQuota(request, reservation); quotaErr != nil {
			return StorageResult{Err: quotaErr}
		}
	}
	database := owned
	if database == nil {
		database, err = sql.Open("sqlite", databasePath)
		if err != nil {
			return StorageResult{Err: err}
		}
		defer database.Close()
		database.SetMaxOpenConns(1)
	}
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
		if owned != nil && strings.HasPrefix(statement, "PRAGMA") {
			continue
		}
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
	case ReadSecurity:
		kind = "security-summary"
	case ReadConfiguration:
		kind = "configuration"
	case ReadPreferences:
		kind = "preferences"
	}
	switch request.Operation {
	case LoadHistory, ReadBrowserReport, ReadTechnicalReport, ReadSecurity, ReadConfiguration, ReadPreferences:
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
		if err == nil && (kind == "browser-state" || kind == "browser-report") {
			cleaned, cleanErr := sanitizeBrowserCheckpoint(body)
			if cleanErr != nil {
				return StorageResult{Err: cleanErr}
			}
			if cleaned != body {
				if err := writeCheckpoint(boundary, database, kind, cleaned); err != nil {
					return StorageResult{Err: err}
				}
				body = cleaned
			}
		}
		return StorageResult{Text: body, Err: err}
	case SavePreferences:
		if len(request.State) > 1024 {
			return StorageResult{Err: errors.New("preferences exceed storage budget")}
		}
		return StorageResult{Err: writeCheckpoint(boundary, database, "preferences", request.State)}
	case SaveConfiguration, InitializeConfiguration:
		if len(request.State) > 16384 {
			return StorageResult{Err: errors.New("configuration exceeds storage budget")}
		}
		if request.Operation == InitializeConfiguration {
			var existing string
			err := database.QueryRowContext(boundary, `SELECT body FROM checkpoints WHERE kind=?`, "configuration").Scan(&existing)
			if err == nil {
				return StorageResult{Text: existing}
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return StorageResult{Err: err}
			}
		}
		return StorageResult{Err: writeCheckpoint(boundary, database, "configuration", request.State)}
	case SaveSecurity:
		if len(request.State) > 2<<20 {
			return StorageResult{Err: errors.New("security summary exceeds storage budget")}
		}
		return StorageResult{Err: writeCheckpoint(boundary, database, "security-summary", request.State)}
	case SaveTechnical:
		if len(request.Report) > 2<<20 {
			return StorageResult{Err: errors.New("analytics report exceeds storage budget")}
		}
		return StorageResult{Err: writeCheckpoint(boundary, database, "technical-report", request.Report)}
	case SaveBrowser:
		if len(request.State) > 6<<20 || len(request.Report) > 3<<20 {
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
		if err == nil && strings.Contains(request.State, `"conversion":`) {
			_, err = transaction.ExecContext(boundary, `DELETE FROM checkpoints WHERE kind=?`, "technical-report")
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
	if info, err := os.Lstat(archiveDirectory); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe analytics archive directory")
	}
	for date, report := range summaries {
		report.Experience.Recent = nil
		report.Conversion.Locations = nil
		report.Conversion.Sessions = nil
		parsed, err := time.Parse("2006-01-02", date)
		if err != nil || date != parsed.Format("2006-01-02") || parsed.After(now) || parsed.Before(dayStart(now).AddDate(0, 0, -archiveLimitDays+1)) {
			continue
		}
		destination := filepath.Join(archiveDirectory, date+".json.gz")
		if info, err := os.Lstat(destination); err == nil && !info.Mode().IsRegular() {
			return errors.New("unsafe analytics archive file")
		}
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

func DatabaseName(domain string) string {
	return url.PathEscape(filepath.Base(strings.TrimSpace(domain))) + ".analytics.db"
}
func directoryBytes(directory string) (int64, error) {
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var total int64
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return 0, errors.New("unsafe analytics storage")
		}
		if entry.IsDir() {
			if entry.Name() != "archives" {
				continue
			}
			nested, nestedErr := directoryBytes(filepath.Join(directory, entry.Name()))
			if nestedErr != nil {
				return 0, nestedErr
			}
			total += nested
		} else {
			info, infoErr := entry.Info()
			if infoErr != nil {
				return 0, infoErr
			}
			total += info.Size()
		}
	}
	return total, nil
}
func (store *Store) diskQuota(request StorageRequest, bytes int64) error {
	reply := make(chan error, 1)
	contact := make(chan struct{})
	defer close(contact)
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	job := DiskQuotaRequest{Domain: request.Domain, Bytes: bytes, Reply: reply, Stop: contact, Final: request.quotaFinal}
	select {
	case request.Quota <- job:
	case <-timer.C:
		return errors.New("analytics quota deadline exceeded")
	case <-request.Stop:
		return errors.New("analytics quota canceled")
	case <-store.stop:
		return errors.New("analytics store closed")
	case <-request.abandoned:
		return errors.New("analytics quota contact closed")
	}
	select {
	case err := <-reply:
		return err
	case <-timer.C:
		return errors.New("analytics quota deadline exceeded")
	case <-request.Stop:
		return errors.New("analytics quota canceled")
	case <-store.stop:
		return errors.New("analytics store closed")
	case <-request.abandoned:
		return errors.New("analytics quota contact closed")
	}
}
