package database

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestDatabaseCoverageHelpers(t *testing.T) {
	if got := duckDBFilePath("  sample.duckdb?threads=2 "); got != filepath.Join(mustCurrentDirectory(t), "sample.duckdb") {
		t.Fatalf("DuckDB path = %q", got)
	}
	if duckDBFilePath(" ?threads=2 ") != mustCurrentDirectory(t) || duckDBFilePath(" ") != "" {
		t.Fatal("empty or query-only DSN normalization failed")
	}
	filePath := filepath.Join(t.TempDir(), "db")
	if err := os.WriteFile(filePath, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if duckDBFileSize(filePath) != 3 || duckDBFileSize(filePath+"-missing") != 0 {
		t.Fatal("database file size lookup failed")
	}
	for _, test := range []struct {
		bytes int64
		want  string
	}{{0, "0B"}, {1023, "1023B"}, {1024, "1.0KB"}, {1024 * 1024, "1.0MB"}, {1 << 50, "1.0PB"}} {
		if got := formatBytes(test.bytes); got != test.want {
			t.Errorf("formatBytes(%d) = %q, want %q", test.bytes, got, test.want)
		}
	}
	for _, test := range []struct {
		err  error
		want bool
	}{{nil, false}, {errors.New("column already exists"), true}, {errors.New("duplicate column"), true}, {errors.New("SQLSTATE 42701"), true}, {errors.New("other failure"), false}} {
		if got := isSchemaAlreadyExistsError(test.err); got != test.want {
			t.Errorf("schema error classification for %v = %t, want %t", test.err, got, test.want)
		}
	}
	if got := tuneDuckDBBatchSize(0, 0); got != 256 {
		t.Errorf("default DuckDB batch = %d", got)
	}
	if got := tuneDuckDBBatchSize(1000, 10); got != 10 {
		t.Errorf("bounded DuckDB batch = %d", got)
	}
	if got := tuneDuckDBBatchSize(1000, 500); got != 256 {
		t.Errorf("capped DuckDB batch = %d", got)
	}
	if ok, low, high := mergeContinuousRanges([]SpeedRange{{Min: 1, Max: 2}, {Min: 2, Max: 3}}); !ok || low != 1 || high != 3 {
		t.Errorf("continuous speed ranges = %t %v-%v", ok, low, high)
	}
	if ok, _, _ := mergeContinuousRanges([]SpeedRange{{Min: 1, Max: 2}, {Min: 3, Max: 4}}); ok {
		t.Error("disjoint speed ranges were merged")
	}
}

func TestDuckDBStartupAndConnectionTuningHelpers(t *testing.T) {
	if processReadBytes() < 0 {
		t.Fatal("process read byte counter is negative")
	}
	logs := make(chan string, 4)
	stop := startDuckDBStartupProgress(context.Background(), "", func(format string, args ...any) {
		logs <- fmt.Sprintf(format, args...)
	})
	select {
	case <-logs:
	case <-time.After(time.Second):
		t.Fatal("startup progress did not report immediately")
	}
	stop()
	stop()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := tuneDuckDBConnection(ctx, newTestDatabase(t).DB, func(string, ...any) {}); err == nil {
		t.Fatal("connection tuning ignored canceled context")
	}
	if err := tuneDuckDBConnection(context.Background(), newTestDatabase(t).DB, func(string, ...any) {}); err != nil {
		t.Fatalf("connection tuning failed: %v", err)
	}
}

func TestDatabaseSchemaIntrospectionAndTrackIdentity(t *testing.T) {
	db := newTestDatabase(t)
	if err := execStatements(db.DB, []string{" ", "CREATE TABLE helper_table(id INTEGER)", "CREATE INDEX helper_table_id ON helper_table(id)"}); err != nil {
		t.Fatal(err)
	}
	columns, err := db.loadColumnPresence(context.Background(), "sqlite", "helper_table")
	if err != nil || !columns["id"] || len(columns) != 1 {
		t.Fatalf("sqlite columns = %#v, %v", columns, err)
	}
	unknown, err := db.loadColumnPresence(context.Background(), "unsupported", "helper_table")
	if err != nil || len(unknown) != 0 {
		t.Fatalf("unknown database columns = %#v, %v", unknown, err)
	}
	if err := execStatements(db.DB, []string{"CREATE TABLE invalid syntax ("}); err == nil {
		t.Fatal("invalid schema statement succeeded")
	}
	markers := make([]Marker, 0, 35)
	for point := 0; point < 35; point++ {
		marker := Marker{DoseRate: 0.5, Date: int64(1000 + point), Lon: float64(point), Lat: float64(point + 1), CountRate: 10, Zoom: 12, Speed: 2, TrackID: "known-track"}
		markers = append(markers, marker)
		if _, err := db.DB.Exec(`INSERT INTO markers(doseRate,date,lon,lat,countRate,zoom,speed,trackID) VALUES(?,?,?,?,?,?,?,?)`, marker.DoseRate, marker.Date, marker.Lon, marker.Lat, marker.CountRate, marker.Zoom, marker.Speed, marker.TrackID); err != nil {
			t.Fatal(err)
		}
	}
	markers = append(markers, markers[0])
	if trackID, err := db.DetectExistingTrackID(markers, 35, "sqlite"); err != nil || trackID != "known-track" {
		t.Fatalf("detected track = %q, %v", trackID, err)
	}
	if trackID, err := db.DetectExistingTrackID(nil, 10, "sqlite"); err != nil || trackID != "" {
		t.Fatalf("empty markers track = %q, %v", trackID, err)
	}
	if trackID, err := db.DetectExistingTrackID(markers, 100, "sqlite"); err != nil || trackID != "" {
		t.Fatalf("unmatched threshold track = %q, %v", trackID, err)
	}
}

func TestDatabaseMarkerOrderingAndTrackHelpers(t *testing.T) {
	first := Marker{DoseRate: 1, Date: 10, Lon: 20, Lat: 30, CountRate: 4, Zoom: 5, Speed: 6, TrackID: "a"}
	second := first
	second.TrackID = "b"
	markers := []Marker{second, first, first}
	if got := deduplicateMarkers(markers); len(got) != 2 {
		t.Fatalf("deduplicated marker count = %d", len(got))
	}
	ordered := orderDuckDBMarkers(markers)
	if len(ordered) != 2 || !reflect.DeepEqual(ordered, []Marker{first, second}) {
		t.Fatalf("ordered markers = %#v", ordered)
	}
	if track, ok := singleTrack([]Marker{first, first}); !ok || track != "a" {
		t.Fatalf("single track = %q, %t", track, ok)
	}
	if _, ok := singleTrack([]Marker{first, second}); ok {
		t.Fatal("mixed tracks were treated as a single track")
	}
	if _, ok := singleTrack(nil); ok {
		t.Fatal("empty marker list has a track")
	}
	if !sameMarkerUniqueKey(first, first) || sameMarkerUniqueKey(first, second) || !lessMarkerByUniqueKey(first, second) {
		t.Fatal("marker key comparisons are inconsistent")
	}
}

func mustCurrentDirectory(t *testing.T) string {
	t.Helper()
	currentDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return currentDirectory
}
