package database

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"strings"
	"testing"
)

type databaseClickHouseRoundTrip func(*http.Request) (*http.Response, error)

func (roundTrip databaseClickHouseRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestClickHouseMarkerDuplicateAndInsertPaths(t *testing.T) {
	originalTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	http.DefaultTransport = databaseClickHouseRoundTrip(func(request *http.Request) (*http.Response, error) {
		query, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		body := `{"meta":[{"name":"1","type":"UInt8"}],"data":[]}`
		if strings.Contains(string(query), "duplicate") {
			body = `{"meta":[{"name":"1","type":"UInt8"}],"data":[[1]]}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	database, err := sql.Open("clickhouse", "clickhouse://localhost:8123/site")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	model := &Database{DB: database, Driver: "clickhouse", idGenerator: startIDGenerator(100)}
	duplicateMarker := Marker{TrackID: "duplicate", Date: 1, DoseRate: 0.5}
	freshMarker := Marker{TrackID: "fresh", Date: 2, DoseRate: 0.5}
	if exists, err := model.markerExistsClickHouse(duplicateMarker); err != nil || !exists {
		t.Fatalf("existing ClickHouse marker = %t, %v", exists, err)
	}
	if exists, err := model.markerExistsClickHouse(freshMarker); err != nil || exists {
		t.Fatalf("fresh ClickHouse marker = %t, %v", exists, err)
	}
	if exists, err := model.realtimeExistsClickHouse("device", 1); err != nil || exists {
		t.Fatalf("ClickHouse realtime marker = %t, %v", exists, err)
	}
	filtered, err := model.filterClickHouseNewMarkers([]Marker{duplicateMarker, freshMarker, freshMarker})
	if err != nil || len(filtered) != 1 || filtered[0].TrackID != "fresh" || filtered[0].ID == 0 {
		t.Fatalf("filtered ClickHouse markers = %#v, %v", filtered, err)
	}
	if filtered, err := model.filterClickHouseNewMarkers(nil); err != nil || len(filtered) != 0 {
		t.Fatalf("empty ClickHouse filter = %#v, %v", filtered, err)
	}
	if err := model.insertMarkersSingleStatement(context.Background(), database, filtered, "clickhouse"); err != nil {
		t.Fatalf("single ClickHouse insert: %v", err)
	}
	if err := model.insertMarkersSingleStatement(context.Background(), database, nil, "clickhouse"); err != nil {
		t.Fatalf("empty ClickHouse insert: %v", err)
	}
	if err := model.insertMarkersSingleStatement(context.Background(), database, filtered, "unsupported"); err == nil {
		t.Fatal("unsupported single-statement driver was accepted")
	}
	if _, err := model.markerExistsClickHouse(Marker{}); err != nil {
		t.Fatalf("empty marker existence query: %v", err)
	}
	if _, err := model.realtimeExistsClickHouse("device", 2); err != nil {
		t.Fatalf("empty realtime existence query: %v", err)
	}
}
