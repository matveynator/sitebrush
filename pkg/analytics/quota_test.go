package analytics

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNamedAnalyticsDatabaseMigratesWithoutTouchingSite(t *testing.T) {
	root := t.TempDir()
	directory, _ := storeDirectory(root, "example.org")
	legacy := filepath.Join(directory, "analytics.db")
	database, err := sql.Open("sqlite", legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.Exec(`CREATE TABLE checkpoints(kind TEXT PRIMARY KEY,body TEXT NOT NULL);INSERT INTO checkpoints VALUES('configuration','[]')`); err != nil {
		t.Fatal(err)
	}
	database.Close()
	store := OpenStore(root)
	defer store.Close()
	result := store.Exchange(StorageRequest{Operation: ReadConfiguration, Domain: "example.org"})
	if result.Err != nil || result.Text != "[]" {
		t.Fatalf("migration lost configuration: %#v", result)
	}
	if _, err = os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("legacy path retained")
	}
	if _, err = os.Stat(filepath.Join(directory, DatabaseName("example.org"))); err != nil {
		t.Fatal("named site database missing", err)
	}
}
func TestPerSiteDatabaseWorkerAndQuotaRefusalPreserveCheckpoint(t *testing.T) {
	store := OpenStore(t.TempDir())
	defer store.Close()
	if result := store.Exchange(StorageRequest{Operation: SaveConfiguration, Domain: "one.example", State: "[]"}); result.Err != nil {
		t.Fatal(result.Err)
	}
	quota := make(chan DiskQuotaRequest, 2)
	completed := make(chan StorageResult, 1)
	go func() {
		completed <- store.Exchange(StorageRequest{Operation: SaveConfiguration, Domain: "one.example", State: `[{"Name":"changed"}]`, Quota: quota})
	}()
	request := <-quota
	// A blocked quota contact for one site must not serialize another site's SQL.
	if result := store.Exchange(StorageRequest{Operation: SaveConfiguration, Domain: "two.example", State: "[]"}); result.Err != nil {
		t.Fatal("another site blocked", result.Err)
	}
	request.Reply <- errors.New("quota full")
	if result := <-completed; result.Err == nil {
		t.Fatal("quota refusal ignored")
	}
	if result := store.Exchange(StorageRequest{Operation: ReadConfiguration, Domain: "one.example"}); result.Err != nil || result.Text != "[]" {
		t.Fatal("quota refusal overwrote accepted configuration")
	}
	usage := store.Exchange(StorageRequest{Operation: ReadUsage, Domain: "one.example"})
	if usage.Err != nil || usage.Bytes <= 0 {
		t.Fatal("database disk bytes missing")
	}
}
func TestAnalyticsQuotaReceivesActualDiskUsageAfterReservation(t *testing.T) {
	store := OpenStore(t.TempDir())
	defer store.Close()
	quota := make(chan DiskQuotaRequest, 2)
	result := make(chan StorageResult, 1)
	go func() {
		result <- store.Exchange(StorageRequest{Operation: SaveConfiguration, Domain: "example.org", State: "[]", Quota: quota})
	}()
	reserved := <-quota
	reserved.Reply <- nil
	actual := <-quota
	actual.Reply <- nil
	if outcome := <-result; outcome.Err != nil {
		t.Fatal(outcome.Err)
	}
	if actual.Bytes <= 0 || actual.Bytes >= reserved.Bytes {
		t.Fatalf("reservation not reconciled: %d -> %d", reserved.Bytes, actual.Bytes)
	}
	if measured := store.Exchange(StorageRequest{Operation: ReadUsage, Domain: "example.org"}); measured.Bytes != actual.Bytes {
		t.Fatal("reported disk usage differs from files")
	}
}
func TestAnalyticsQuotaClosedContactDoesNotLeakWorker(t *testing.T) {
	store := OpenStore(t.TempDir())
	quota := make(chan DiskQuotaRequest)
	stop := make(chan struct{})
	close(stop)
	if result := store.Exchange(StorageRequest{Operation: SaveConfiguration, Domain: "example.org", State: "[]", Quota: quota, Stop: stop}); result.Err == nil {
		t.Fatal("closed contact accepted")
	}
	done := make(chan struct{})
	go func() { store.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("site database worker did not stop")
	}
}

func TestAnalyticsArchiveExpiryRunsBeforeQuotaAdmission(t *testing.T) {
	root := t.TempDir()
	store := OpenStore(root)
	defer store.Close()
	if result := store.Exchange(StorageRequest{Operation: SaveConfiguration, Domain: "example.org", State: "[]"}); result.Err != nil {
		t.Fatal(result.Err)
	}
	directory, err := storeDirectory(root, "example.org")
	if err != nil {
		t.Fatal(err)
	}
	archives := filepath.Join(directory, "archives")
	if err = os.MkdirAll(archives, 0700); err != nil {
		t.Fatal(err)
	}
	expired := filepath.Join(archives, time.Now().UTC().AddDate(0, 0, -archiveLimitDays-1).Format("2006-01-02")+".json.gz")
	if err = os.WriteFile(expired, []byte("expired"), 0600); err != nil {
		t.Fatal(err)
	}
	quota := make(chan DiskQuotaRequest, 2)
	completed := make(chan StorageResult, 1)
	go func() {
		completed <- store.Exchange(StorageRequest{Operation: SaveConfiguration, Domain: "example.org", State: "[]", Quota: quota})
	}()
	reservation := <-quota
	if _, err = os.Stat(expired); !os.IsNotExist(err) {
		t.Error("expired archive still consumes quota at admission")
	}
	reservation.Reply <- errors.New("quota full")
	reconciled := <-quota
	reconciled.Reply <- nil
	if result := <-completed; result.Err == nil {
		t.Fatal("quota refusal ignored")
	}
}
