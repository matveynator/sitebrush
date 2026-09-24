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

func TestSingleWriterPackagesDoNotBypassSerializedPipeline(t *testing.T) {
	allowedFunctions := map[string]bool{
		"InitSchema":                     true,
		"ensureMarkerMetadataColumns":    true,
		"ensureRealtimeMetadataColumns":  true,
		"ensureAnalyticsMetadataColumns": true,
		"ensureAnalyticsSessionColumns":  true,
		"InsertMarkersBulk":              true,
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
				if !ok || !isDatabaseWriteMethod(selector.Sel.Name) {
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
				t.Errorf("%s:%d %s bypasses serialized database writer with db.DB.%s", path, position.Line, function.Name.Name, selector.Sel.Name)
				return true
			})
		}
	}
}

func isDatabaseWriteMethod(name string) bool {
	switch name {
	case "Exec", "ExecContext", "Begin", "BeginTx":
		return true
	default:
		return false
	}
}
