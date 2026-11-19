// Command ownership inventories first-party Go ownership boundaries without
// interpreting a channel send or a shallow copy as proof of exclusive ownership.
package main

import (
	"bytes"
	"encoding/csv"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if err := inventory(*root, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Every Go file is parsed, including other platforms and tests. Candidates are
// evidence for review, not diagnostics: named types and interfaces can hide
// references, and this syntax inventory cannot prove whole-program alias safety.
func inventory(root string, output io.Writer) error {
	writer := csv.NewWriter(output)
	writer.Comma = '\t'
	if err := writer.Write([]string{"file", "first", "last", "scope", "kind", "detail"}); err != nil {
		return err
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".agents", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		positions := token.NewFileSet()
		file, err := parser.ParseFile(positions, path, nil, 0)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		record := func(node ast.Node, scope, kind, detail string) {
			if err != nil {
				return
			}
			err = writer.Write([]string{relative, fmt.Sprint(positions.Position(node.Pos()).Line), fmt.Sprint(positions.Position(node.End()).Line), scope, kind, detail})
		}
		record(file, file.Name.Name, "file", "parsed")
		if strings.HasSuffix(path, "_test.go") || strings.Contains(relative, "/testdata/") {
			return err
		}
		for _, declaration := range file.Decls {
			scope := "package"
			if function, ok := declaration.(*ast.FuncDecl); ok {
				scope = function.Name.Name
			}
			if group, ok := declaration.(*ast.GenDecl); ok && group.Tok == token.VAR {
				record(group, scope, "global", syntax(positions, group))
			}
			ast.Inspect(declaration, func(node ast.Node) bool {
				switch candidate := node.(type) {
				case *ast.Field:
					if referenceType(candidate.Type) {
						record(candidate, scope, "reference-field", syntax(positions, candidate))
					}
				case *ast.GoStmt:
					record(candidate, scope, "goroutine", syntax(positions, candidate.Call.Fun))
				case *ast.SendStmt:
					record(candidate, scope, "send", syntax(positions, candidate.Chan))
				case *ast.CallExpr:
					if function, ok := candidate.Fun.(*ast.Ident); ok && function.Name == "close" {
						record(candidate, scope, "close", syntax(positions, candidate))
					}
				case *ast.FuncLit:
					ast.Inspect(candidate.Body, func(nested ast.Node) bool {
						switch statement := nested.(type) {
						case *ast.AssignStmt:
							for _, target := range statement.Lhs {
								if captured(target, candidate) {
									record(target, scope, "captured-write", syntax(positions, target))
								}
							}
						case *ast.IncDecStmt:
							if captured(statement.X, candidate) {
								record(statement, scope, "captured-write", syntax(positions, statement))
							}
						}
						return true
					})
				}
				return true
			})
		}
		return err
	})
	writer.Flush()
	if err != nil {
		return err
	}
	return writer.Error()
}

func referenceType(expression ast.Expr) bool {
	switch expression.(type) {
	case *ast.StarExpr, *ast.ArrayType, *ast.MapType, *ast.InterfaceType, *ast.FuncType, *ast.ChanType:
		return true
	}
	return false
}

func captured(expression ast.Expr, closure *ast.FuncLit) bool {
	switch target := expression.(type) {
	case *ast.Ident:
		return target.Obj != nil && target.Obj.Pos() < closure.Pos()
	case *ast.SelectorExpr:
		return captured(target.X, closure)
	case *ast.IndexExpr:
		return captured(target.X, closure)
	case *ast.StarExpr:
		return captured(target.X, closure)
	}
	return false
}

func syntax(positions *token.FileSet, node ast.Node) string {
	var output bytes.Buffer
	if err := format.Node(&output, positions, node); err != nil {
		return fmt.Sprintf("%T", node)
	}
	description := strings.Join(strings.Fields(output.String()), " ")
	if len(description) > 200 {
		description = description[:200] + "..."
	}
	return description
}
