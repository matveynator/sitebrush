package drivers

import (
	"database/sql/driver"
	"io"
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
