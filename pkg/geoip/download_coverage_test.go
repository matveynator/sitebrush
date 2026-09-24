package geoip

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

type geoIPRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn geoIPRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type geoIPFailingReader struct {
	read bool
}

func (reader *geoIPFailingReader) Read(payload []byte) (int, error) {
	if reader.read {
		return 0, errors.New("read failure")
	}
	reader.read = true
	copy(payload, "partial")
	return len("partial"), errors.New("read failure")
}

func (reader *geoIPFailingReader) Close() error { return nil }

func withGeoIPHTTPClient(t *testing.T, transport http.RoundTripper) {
	t.Helper()
	previous := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: transport}
	t.Cleanup(func() { http.DefaultClient = previous })
}

func TestGeoIPDownloadArchiveHTTPAndFilesystemBranches(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		withGeoIPHTTPClient(t, geoIPRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			if !strings.Contains(request.URL.String(), "dbip-city-lite-2026-09.csv.gz") {
				t.Fatalf("unexpected GeoIP URL %s", request.URL)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("archive")),
				Request:    request,
			}, nil
		}))
		path := filepath.Join(t.TempDir(), "nested", "archive.gz")
		if err := downloadArchive(context.Background(), "2026-09", path); err != nil {
			t.Fatal(err)
		}
		payload, err := os.ReadFile(path)
		if err != nil || string(payload) != "archive" {
			t.Fatalf("downloaded archive=%q err=%v", payload, err)
		}
		if _, err := os.Stat(path + ".part"); !os.IsNotExist(err) {
			t.Fatalf("temporary download file survived: %v", err)
		}
	})

	t.Run("non-200", func(t *testing.T) {
		withGeoIPHTTPClient(t, geoIPRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("missing")),
				Request:    request,
			}, nil
		}))
		if err := downloadArchive(context.Background(), "2026-09", filepath.Join(t.TempDir(), "archive.gz")); err == nil {
			t.Fatal("GeoIP download accepted HTTP 404")
		}
	})

	t.Run("transport error", func(t *testing.T) {
		withGeoIPHTTPClient(t, geoIPRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("network down")
		}))
		if err := downloadArchive(context.Background(), "2026-09", filepath.Join(t.TempDir(), "archive.gz")); err == nil {
			t.Fatal("GeoIP download hid transport failure")
		}
	})

	t.Run("body read error removes partial file", func(t *testing.T) {
		withGeoIPHTTPClient(t, geoIPRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       &geoIPFailingReader{},
				Request:    request,
			}, nil
		}))
		path := filepath.Join(t.TempDir(), "archive.gz")
		if err := downloadArchive(context.Background(), "2026-09", path); err == nil {
			t.Fatal("GeoIP download ignored body read failure")
		}
		if _, err := os.Stat(path + ".part"); !os.IsNotExist(err) {
			t.Fatalf("SECURITY: partial GeoIP archive survived failed download: %v", err)
		}
	})

	t.Run("rename failure", func(t *testing.T) {
		withGeoIPHTTPClient(t, geoIPRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("archive")),
				Request:    request,
			}, nil
		}))
		path := filepath.Join(t.TempDir(), "archive.gz")
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "keep"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := downloadArchive(context.Background(), "2026-09", path); err == nil {
			t.Fatal("GeoIP download unexpectedly replaced a non-empty directory")
		}
	})
}

func TestGeoIPArchiveCancellationAndRangeFailureBranches(t *testing.T) {
	directory := t.TempDir()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(directory, "cancel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := migrate(database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("CREATE TABLE geoip_ranges_next(ip_start INTEGER NOT NULL,ip_end INTEGER NOT NULL,country_code TEXT,region TEXT,city TEXT,latitude REAL,longitude REAL)"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := importArchiveRows(ctx, database, filepath.Join(directory, "missing.gz")); err == nil {
		t.Fatal("cancelled/missing GeoIP import unexpectedly succeeded")
	}

	closed, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	if got := rangeCount(closed); got != 0 {
		t.Fatalf("rangeCount on closed database=%d", got)
	}
}
