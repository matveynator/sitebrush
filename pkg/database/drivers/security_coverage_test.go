package drivers

import (
	"context"
	"database/sql/driver"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSecurityBoundaryClickHouseLiteralEscapingAndArgumentValidation(t *testing.T) {
	rendered, err := renderQuery(
		"SELECT ?, ?, ?, ?, ?, ?",
		[]driver.NamedValue{
			{Ordinal: 1, Value: "x'); DROP TABLE users; --"},
			{Ordinal: 2, Value: []byte{}},
			{Ordinal: 3, Value: false},
			{Ordinal: 4, Value: float64(1.25)},
			{Ordinal: 5, Value: int64(-7)},
			{Ordinal: 6, Value: time.Unix(0, 0).UTC()},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "'x''); DROP TABLE users; --'") {
		t.Fatalf("SECURITY: ClickHouse string literal was not quoted safely: %s", rendered)
	}
	if strings.Count(rendered, "?") != 0 {
		t.Fatalf("unexpanded placeholders remain: %s", rendered)
	}
	if _, err := renderQuery("SELECT ?", []driver.NamedValue{{Ordinal: 1, Value: struct{}{}}}); err == nil {
		t.Fatal("unsupported value type was accepted")
	}
}

func TestClickHouseConfigAndRequestEdgeBranches(t *testing.T) {
	cfg, err := parseClickHouseDSN("https://user:p%40ss@example.com/db?compress=1")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.scheme != "https" || cfg.username != "user" || cfg.password != "p@ss" || cfg.database != "db" {
		t.Fatalf("https config = %#v", cfg)
	}

	conn := &clickhouseConn{cfg: clickHouseConfig{
		scheme: "https",
		host: "example.com",
		params: map[string][]string{"compress": []string{"1", "2"}},
	}}
	request, err := conn.newRequest(context.Background(), "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	if request.URL.Query().Get("database") != "" || len(request.URL.Query()["compress"]) != 2 {
		t.Fatalf("request query = %v", request.URL.Query())
	}
	if _, _, ok := request.BasicAuth(); ok {
		t.Fatal("request unexpectedly contains Basic Auth")
	}

	if got := ensureJSONFormat("SHOW TABLES;"); !strings.Contains(got, "FORMAT JSONCompactEachRowWithNamesAndTypes") {
		t.Fatalf("SHOW query not formatted: %q", got)
	}
	if got := ensureJSONFormat("  update t set x=1 "); strings.Contains(strings.ToLower(got), "format json") {
		t.Fatalf("write query unexpectedly formatted: %q", got)
	}
}

func TestClickHouseRowsAndJSONEdgeBranches(t *testing.T) {
	rows := &clickhouseRows{columns: []string{"a", "b"}, data: [][]driver.Value{{int64(1), "x"}}}
	if err := rows.Next(make([]driver.Value, 1)); err == nil {
		t.Fatal("short destination buffer was accepted")
	}
	dest := make([]driver.Value, 2)
	if err := rows.Next(dest); err != nil {
		t.Fatal(err)
	}
	if err := rows.Next(dest); err != io.EOF {
		t.Fatalf("rows end = %v", err)
	}

	object := `{"names":["id","active"],"types":["UInt64","Bool"],"data":[["9",false]]}`
	decoded, err := decodeJSONResult(strings.NewReader(object))
	if err != nil {
		t.Fatal(err)
	}
	dest = make([]driver.Value, 2)
	if err := decoded.Next(dest); err != nil || dest[0] != int64(9) || dest[1] != int64(0) {
		t.Fatalf("decoded row = %#v, %v", dest, err)
	}

	compactMeta := `{"meta":[["id","UInt64"],["name","String"]],"data":[[1,"one"]]}`
	if _, err := decodeJSONResult(strings.NewReader(compactMeta)); err != nil {
		t.Fatalf("compact meta: %v", err)
	}
	for _, malformed := range []string{
		`{"names":["id"],"types":["UInt64"],"data":[[1,2]]}`,
		`{"meta":[[]],"data":[]}`,
		`["id"]
["UInt64","String"]
`,
		`["id"]
["UInt64"]
[1,2]
`,
	} {
		if _, err := decodeJSONResult(strings.NewReader(malformed)); err == nil {
			t.Fatalf("malformed ClickHouse JSON accepted: %q", malformed)
		}
	}
}

func TestClickHouseValueConversionEdgeBranches(t *testing.T) {
	tests := []struct {
		meta string
		raw  any
		want any
	}{
		{"Float64", "1.5", float64(1.5)},
		{"Int64", float64(8), int64(8)},
		{"Bool", true, int64(1)},
		{"Bool", false, int64(0)},
		{"String", int64(4), "4"},
		{"UUID", "abc", "abc"},
		{"Enum8", "x", "x"},
		{"Unknown", true, int64(1)},
		{"Unknown", "text", "text"},
	}
	for _, tc := range tests {
		got, err := convertJSONValue(tc.meta, tc.raw)
		if err != nil || got != tc.want {
			t.Fatalf("convert %q %#v = %#v, %v; want %#v", tc.meta, tc.raw, got, err, tc.want)
		}
	}
	if got, err := convertJSONValue("Nullable(String)", nil); err != nil || got != nil {
		t.Fatalf("nil conversion = %#v, %v", got, err)
	}
	if got, err := convertJSONValue("DateTime", "not-a-date"); err != nil || got != "not-a-date" {
		t.Fatalf("date fallback = %#v, %v", got, err)
	}
	if _, err := convertJSONValue("Float64", "bad"); err == nil {
		t.Fatal("invalid float string accepted")
	}
	if _, err := convertJSONValue("Int64", "bad"); err == nil {
		t.Fatal("invalid integer string accepted")
	}

	conn := &clickhouseConn{}
	value := driver.NamedValue{Value: uint64(math.MaxInt64)}
	if err := conn.CheckNamedValue(&value); err != nil || value.Value != int64(math.MaxInt64) {
		t.Fatalf("max uint64-compatible value = %#v, %v", value.Value, err)
	}
}

func TestClickHouseNetworkErrorPaths(t *testing.T) {
	sentinel := io.ErrUnexpectedEOF
	conn := &clickhouseConn{
		cfg: clickHouseConfig{scheme: "http", host: "example.invalid"},
		client: &http.Client{Transport: clickHouseRoundTrip(func(*http.Request) (*http.Response, error) {
			return nil, sentinel
		})},
	}
	if err := conn.Ping(context.Background()); err == nil {
		t.Fatal("ping hid transport error")
	}
	if _, err := conn.ExecContext(context.Background(), "SELECT 1", nil); err == nil {
		t.Fatal("exec hid transport error")
	}
	if _, err := conn.QueryContext(context.Background(), "SELECT 1", nil); err == nil {
		t.Fatal("query hid transport error")
	}
}
