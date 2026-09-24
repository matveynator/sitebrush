package database

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const singleWriterRule = `database single-writer rule violated

Runtime code for single-writer engines (SQLite, Chai, DuckDB) must never access the writable *sql.DB directly.

Do not call db.DB.Exec, db.DB.ExecContext, db.DB.Query, db.DB.QueryContext, db.DB.QueryRow, db.DB.QueryRowContext, db.DB.Begin, db.DB.BeginTx, db.DB.Conn, db.DB.Prepare, or db.DB.PrepareContext from runtime application code.

Required architecture:
  consumer -> task + reply channel -> serialized database worker -> result through reply channel

The database worker is the only runtime owner allowed to execute writes for single-writer engines.
Submit work through withSerializedConnectionFor(...). The worker is channel-oriented and coordinates jobs through channels and select/case; do not add mutexes or shared mutable state to work around this rule.

If an operation is long-lived, split it into bounded jobs so the worker can preserve fairness and backpressure. Cancellation/liveness should follow the request/reply channel lifecycle where application architecture permits it.

Direct access is allowed only for explicit bootstrap/schema setup before concurrent runtime traffic, or for database-specific paths such as PostgreSQL COPY that are intentionally outside the single-writer engines.

Rewrite the offending code to submit the database operation to the serialized worker instead of bypassing it.`

func TestSingleWriterPackagesDoNotBypassSerializedPipeline(t *testing.T) {
	allowedFunctions := map[string]bool{
		"InitSchema":                        true,
		"ensureMarkerMetadataColumns":       true,
		"ensureRealtimeMetadataColumns":     true,
		"ensureAnalyticsMetadataColumns":    true,
		"ensureAnalyticsSessionColumns":      true,
		"loadColumnPresence":                 true,
		"InsertMarkersBulk":                  true,
		"markerExistsClickHouse":             true,
		"realtimeExistsClickHouse":           true,
		"insertMarkersPostgreSQLCopy":        true,
		"insertMarkersPostgreSQLCopyBatched": true,
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read database package: %v", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}

		path := filepath.Clean(entry.Name())
		fileSet := token.NewFileSet()
		parsed, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			if allowedFunctions[function.Name.Name] {
				continue
			}

			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}

				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || !isForbiddenDirectDatabaseMethod(selector.Sel.Name) {
					return true
				}

				databaseField, ok := selector.X.(*ast.SelectorExpr)
				if !ok || databaseField.Sel.Name != "DB" {
					return true
				}

				receiver, ok := databaseField.X.(*ast.Ident)
				if !ok || receiver.Name != "db" {
					return true
				}

				position := fileSet.Position(call.Pos())
				t.Errorf(
					"%s:%d: %s calls db.DB.%s directly\n\n%s",
					path,
					position.Line,
					function.Name.Name,
					selector.Sel.Name,
					singleWriterRule,
				)
				return true
			})
		}
	}
}

func isForbiddenDirectDatabaseMethod(name string) bool {
	switch name {
	case "Exec", "ExecContext", "Query", "QueryContext", "QueryRow", "QueryRowContext", "Begin", "BeginTx", "Conn", "Prepare", "PrepareContext":
		return true
	default:
		return false
	}
}
