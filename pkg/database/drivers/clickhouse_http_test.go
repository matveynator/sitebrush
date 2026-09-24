package drivers

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRenderQueryEncodesClickHouseLiterals(t *testing.T) {
	when := time.Date(2026, 5, 2, 12, 30, 45, 0, time.FixedZone("MSK", 3*60*60))
	rendered, err := renderQuery(
		"INSERT INTO events VALUES (?, ?, ?, ?, ?, ?)",
		[]driver.NamedValue{
			{Ordinal: 1, Value: int64(42)},
			{Ordinal: 2, Value: "Bob's page"},
			{Ordinal: 3, Value: []byte{0xde, 0xad}},
			{Ordinal: 4, Value: true},
			{Ordinal: 5, Value: nil},
			{Ordinal: 6, Value: when},
		},
	)
	if err != nil {
		t.Fatalf("render query: %v", err)
	}

	expected := "INSERT INTO events VALUES (42, 'Bob''s page', unhex('dead'), 1, NULL, '2026-05-02 09:30:45')"
	if rendered != expected {
		t.Fatalf("rendered query = %q, want %q", rendered, expected)
	}
}

func TestRenderQueryReportsArgumentMismatches(t *testing.T) {
	if _, err := renderQuery("SELECT ?", nil); err != nil {
		t.Fatalf("query without args should be returned unchanged: %v", err)
	}
	if _, err := renderQuery("SELECT ?", []driver.NamedValue{}); err != nil {
		t.Fatalf("empty args should be returned unchanged: %v", err)
	}
	if _, err := renderQuery("SELECT ?", []driver.NamedValue{{Ordinal: 1, Value: int64(1)}, {Ordinal: 2, Value: int64(2)}}); err == nil {
		t.Fatalf("expected too many arguments error")
	}
	if _, err := renderQuery("SELECT ?, ?", []driver.NamedValue{{Ordinal: 1, Value: int64(1)}}); err == nil {
		t.Fatalf("expected not enough arguments error")
	}
	if _, err := encodeLiteral(struct{}{}); err == nil {
		t.Fatalf("expected unsupported literal error")
	}
}

func TestEnsureJSONFormatOnlyChangesReadableQueries(t *testing.T) {
	if got := ensureJSONFormat("SELECT 1;"); got != "SELECT 1 FORMAT JSONCompactEachRowWithNamesAndTypes;" {
		t.Fatalf("formatted SELECT = %q", got)
	}
	if got := ensureJSONFormat("WITH x AS (SELECT 1) SELECT * FROM x"); !strings.HasSuffix(got, "FORMAT JSONCompactEachRowWithNamesAndTypes") {
		t.Fatalf("WITH query missing format: %q", got)
	}
	if got := ensureJSONFormat("INSERT INTO x VALUES (1)"); got != "INSERT INTO x VALUES (1)" {
		t.Fatalf("INSERT query changed to %q", got)
	}
	if got := ensureJSONFormat("SELECT 1 FORMAT JSON"); got != "SELECT 1 FORMAT JSON" {
		t.Fatalf("existing format changed to %q", got)
	}
}

func TestParseClickHouseDSN(t *testing.T) {
	cfg, err := parseClickHouseDSN("clickhouse://user:pass@example.com:9440/radiation?secure=true&compress=1")
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	if cfg.scheme != "https" || cfg.host != "example.com:9440" || cfg.database != "radiation" {
		t.Fatalf("unexpected endpoint config: %+v", cfg)
	}
	if cfg.username != "user" || cfg.password != "pass" {
		t.Fatalf("unexpected credentials: %+v", cfg)
	}
	if cfg.params.Get("secure") != "" || cfg.params.Get("compress") != "1" {
		t.Fatalf("unexpected params: %v", cfg.params)
	}

	cfg, err = parseClickHouseDSN("")
	if err != nil {
		t.Fatalf("parse empty dsn: %v", err)
	}
	if cfg.scheme != "http" || cfg.host != "127.0.0.1:9000" {
		t.Fatalf("empty dsn config = %+v", cfg)
	}

	if _, err := parseClickHouseDSN("ftp://example.com"); err == nil {
		t.Fatalf("expected unsupported scheme error")
	}
}

func TestDecodeJSONResultObjectAndStream(t *testing.T) {
	objectPayload := `{
		"meta":[{"name":"id","type":"UInt64"},{"name":"name","type":"String"},{"name":"seen","type":"DateTime"}],
		"data":[[7,"alpha","2026-05-02 09:30:45"]]
	}`
	rows, err := decodeJSONResult(strings.NewReader(objectPayload))
	if err != nil {
		t.Fatalf("decode object payload: %v", err)
	}
	if got := rows.Columns(); len(got) != 3 || got[0] != "id" || got[1] != "name" || got[2] != "seen" {
		t.Fatalf("object columns = %#v", got)
	}
	dest := make([]driver.Value, 3)
	if err := rows.Next(dest); err != nil {
		t.Fatalf("read object row: %v", err)
	}
	if dest[0] != int64(7) || dest[1] != "alpha" {
		t.Fatalf("object row = %#v", dest)
	}
	if _, ok := dest[2].(time.Time); !ok {
		t.Fatalf("DateTime value type = %T, want time.Time", dest[2])
	}
	if err := rows.Next(dest); err != io.EOF {
		t.Fatalf("second object row error = %v, want EOF", err)
	}

	streamPayload := `["id","active","dose"]
["UInt32","Bool","Float64"]
[8,true,"0.12"]
`
	rows, err = decodeJSONResult(strings.NewReader(streamPayload))
	if err != nil {
		t.Fatalf("decode stream payload: %v", err)
	}
	dest = make([]driver.Value, 3)
	if err := rows.Next(dest); err != nil {
		t.Fatalf("read stream row: %v", err)
	}
	if dest[0] != int64(8) || dest[1] != int64(1) || dest[2] != 0.12 {
		t.Fatalf("stream row = %#v", dest)
	}
}

func TestDecodeJSONResultRejectsMalformedPayloads(t *testing.T) {
	if _, err := decodeJSONResult(strings.NewReader("not json")); err == nil {
		t.Fatalf("expected unsupported payload error")
	}
	if _, err := decodeJSONResult(strings.NewReader(`{"data":[[1]]}`)); err == nil {
		t.Fatalf("expected missing metadata error")
	}
	if _, err := decodeJSONResult(strings.NewReader(`["id"]`)); err == nil {
		t.Fatalf("expected missing types error")
	}
}

type clickHouseRoundTrip func(*http.Request) (*http.Response, error)

func (roundTrip clickHouseRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestClickHouseDriverConnectionAndHTTPOperations(t *testing.T) {
	connectionValue, err := (&clickhouseDriver{}).Open("clickhouse://user:secret@db.example:8123/site?compress=1")
	if err != nil {
		t.Fatal(err)
	}
	connection := connectionValue.(*clickhouseConn)
	responseStatus := http.StatusOK
	responseBody := "[\"id\"]\n[\"UInt64\"]\n[7]\n"
	requestCount := 0
	connection.client = &http.Client{Transport: clickHouseRoundTrip(func(request *http.Request) (*http.Response, error) {
		requestCount++
		if request.Method != http.MethodPost || request.URL.Scheme != "http" || request.URL.Host != "db.example:8123" || request.URL.Query().Get("database") != "site" || request.URL.Query().Get("compress") != "1" {
			return nil, errors.New("unexpected ClickHouse request")
		}
		username, password, ok := request.BasicAuth()
		if !ok || username != "user" || password != "secret" {
			return nil, errors.New("missing ClickHouse basic auth")
		}
		return &http.Response{StatusCode: responseStatus, Status: http.StatusText(responseStatus), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(responseBody)), Request: request}, nil
	})}
	request, err := connection.newRequest(context.Background(), "SELECT 1")
	if err != nil || request.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("new ClickHouse request = %#v, %v", request, err)
	}
	if err := connection.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := connection.ExecContext(context.Background(), "INSERT INTO t VALUES (?)", []driver.NamedValue{{Ordinal: 1, Value: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 0 {
		t.Fatalf("ClickHouse rows affected = %d, %v", affected, err)
	}
	rowsValue, err := connection.QueryContext(context.Background(), "SELECT ?", []driver.NamedValue{{Ordinal: 1, Value: int64(7)}})
	if err != nil {
		t.Fatal(err)
	}
	rows := rowsValue.(*clickhouseRows)
	if columns := rows.Columns(); len(columns) != 1 || columns[0] != "id" {
		t.Fatalf("ClickHouse query columns = %#v", columns)
	}
	destination := make([]driver.Value, 1)
	if err := rows.Next(destination); err != nil || destination[0] != int64(7) {
		t.Fatalf("ClickHouse query row = %#v, %v", destination, err)
	}
	if err := rows.Next(destination); err != io.EOF {
		t.Fatalf("ClickHouse query end = %v", err)
	}
	_ = rows.Close()
	if len(rows.data) != 0 || requestCount != 3 {
		t.Fatalf("rows after close=%d requests=%d", len(rows.data), requestCount)
	}
	responseStatus = http.StatusBadGateway
	responseBody = " upstream unavailable "
	if err := connection.Ping(context.Background()); err == nil || !strings.Contains(err.Error(), "upstream unavailable") {
		t.Fatalf("failed ping error = %v", err)
	}
	if _, err := connection.ExecContext(context.Background(), "INSERT INTO t VALUES (1)", nil); err == nil || !strings.Contains(err.Error(), "upstream unavailable") {
		t.Fatalf("failed exec error = %v", err)
	}
	if _, err := connection.QueryContext(context.Background(), "SELECT 1", nil); err == nil || !strings.Contains(err.Error(), "upstream unavailable") {
		t.Fatalf("failed query error = %v", err)
	}
	responseStatus = http.StatusOK
	responseBody = "[\"id\"]\n[\"UInt64\"]\n[7]\n"
	if _, err := connection.QueryContext(context.Background(), "SELECT ?", nil); err != nil {
		t.Fatalf("query without parameters: %v", err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Begin(); err == nil {
		t.Fatal("ClickHouse transaction unexpectedly started")
	}
	if _, err := connection.BeginTx(context.Background(), driver.TxOptions{}); err == nil {
		t.Fatal("ClickHouse context transaction unexpectedly started")
	}
}

func TestClickHouseDriverStatementsAndNamedValues(t *testing.T) {
	connection := &clickhouseConn{cfg: clickHouseConfig{scheme: "http", host: "localhost"}, client: &http.Client{Transport: clickHouseRoundTrip(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("[\"id\"]\n[\"UInt64\"]\n[7]\n")), Request: request}, nil
	})}}
	statementValue, err := connection.PrepareContext(context.Background(), "SELECT ?, ?")
	if err != nil {
		t.Fatal(err)
	}
	statement := statementValue.(*clickhouseStmt)
	if statement.NumInput() != 2 || statement.Close() != nil {
		t.Fatal("prepared statement metadata is incorrect")
	}
	statement.conn = connection
	if _, err := statement.Exec([]driver.Value{int64(1), int64(2)}); err != nil {
		t.Fatalf("statement exec: %v", err)
	}
	if _, err := statement.Query([]driver.Value{int64(1), int64(2)}); err != nil {
		t.Fatalf("statement query: %v", err)
	}
	if _, err := statement.ExecContext(context.Background(), []driver.NamedValue{{Ordinal: 1, Value: int64(1)}, {Ordinal: 2, Value: int64(2)}}); err != nil {
		t.Fatalf("context statement exec: %v", err)
	}
	if _, err := statement.QueryContext(context.Background(), []driver.NamedValue{{Ordinal: 1, Value: int64(1)}, {Ordinal: 2, Value: int64(2)}}); err != nil {
		t.Fatalf("context statement query: %v", err)
	}
	if _, err := connection.Prepare("SELECT ?"); err != nil {
		t.Fatal(err)
	}
	if named := namedValuesFromValues([]driver.Value{"a", int64(2)}); len(named) != 2 || named[1].Ordinal != 2 {
		t.Fatalf("adapted named values = %#v", named)
	}
	for _, test := range []struct {
		input any
		want  any
	}{
		{int(1), int64(1)}, {int32(2), int64(2)}, {int16(3), int64(3)}, {int8(4), int64(4)},
		{uint(5), int64(5)}, {uint32(6), int64(6)}, {uint16(7), int64(7)}, {uint8(8), int64(8)}, {float32(1.5), float64(1.5)},
	} {
		named := driver.NamedValue{Value: test.input}
		if err := connection.CheckNamedValue(&named); err != nil || named.Value != test.want {
			t.Errorf("CheckNamedValue(%T) = %#v, %v", test.input, named.Value, err)
		}
	}
	for _, input := range []any{nil, int64(1), float64(1), true, "text", []byte("bytes"), time.Now()} {
		if err := connection.CheckNamedValue(&driver.NamedValue{Value: input}); err != nil {
			t.Errorf("CheckNamedValue(%T): %v", input, err)
		}
	}
	if err := connection.CheckNamedValue(&driver.NamedValue{Value: uint64(math.MaxInt64) + 1}); err == nil {
		t.Fatal("overflowing uint64 was accepted")
	}
	if err := connection.CheckNamedValue(&driver.NamedValue{Value: struct{}{}}); err == nil {
		t.Fatal("unsupported named value was accepted")
	}
	if _, err := (&clickhouseDriver{}).Open("ftp://example.com"); err == nil {
		t.Fatal("unsupported driver DSN was accepted")
	}
}
