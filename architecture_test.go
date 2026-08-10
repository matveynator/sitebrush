package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"testing"
)

func TestFirstPartyCodeDoesNotUseLockPrimitives(t *testing.T) {
	forbiddenSelectors := map[string]bool{"Mutex": true, "RWMutex": true, "Once": true, "Locker": true}
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == ".git" || path == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		parsedFile, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		syncNames := make(map[string]bool)
		for _, importedPackage := range parsedFile.Imports {
			importPath, unquoteErr := strconv.Unquote(importedPackage.Path.Value)
			if unquoteErr != nil || importPath != "sync" {
				continue
			}
			importName := "sync"
			if importedPackage.Name != nil {
				importName = importedPackage.Name.Name
			}
			syncNames[importName] = true
		}
		ast.Inspect(parsedFile, func(node ast.Node) bool {
			selector, selectorFound := node.(*ast.SelectorExpr)
			if !selectorFound || !forbiddenSelectors[selector.Sel.Name] {
				return true
			}
			packageIdentifier, identifierFound := selector.X.(*ast.Ident)
			if identifierFound && syncNames[packageIdentifier.Name] {
				t.Errorf("%s uses forbidden lock primitive %s.%s", path, packageIdentifier.Name, selector.Sel.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
